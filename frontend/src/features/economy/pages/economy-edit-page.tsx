import { useCallback, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ConnectError, Code } from '@connectrpc/connect'
import yaml from 'js-yaml'
import { create } from '@bufbuild/protobuf'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'
import { ManifestSchema, type Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ManifestEditor } from '../components/manifest-editor'
import { ValidationPanel } from '../components/validation-panel'
import { EditorGraphPanel } from '../components/editor-graph-panel'
import { DeployWizard } from '../components/deploy-wizard'
import { useManifestValidate } from '../hooks/use-manifest-validate'
import type { ValidationError } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'

// ── Skeleton manifest for create-new mode ────────────────────────────────────

const SKELETON_MANIFEST = `version: "1.0"
metadata:
  name: My Economy
  industry: finance
  description: A new economy configuration
instruments: []
accountTypes: []
valuationRules: []
sagas: []
`

// ── Loading state ─────────────────────────────────────────────────────────────

function LoadingSkeleton() {
  return (
    <div data-testid="edit-page-loading" className="flex h-full gap-4 p-4">
      <div className="flex flex-[7] flex-col gap-3">
        <Skeleton className="h-[60vh]" />
        <Skeleton className="h-24" />
        <Skeleton className="h-20" />
      </div>
      <div className="flex-[3]">
        <Skeleton className="h-full" />
      </div>
    </div>
  )
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  return (
    <div data-testid="edit-page-error" className="p-6 flex flex-col items-center gap-3 py-16 text-muted-foreground">
      <span className="text-sm font-medium">Unable to load current manifest</span>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export function EconomyEditPage() {
  const { manifestHistory } = useApiClients()
  const { validate, result: validationResult } = useManifestValidate()

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: manifestKeys.current(),
    queryFn: () => manifestHistory.getCurrentManifest({}),
  })
  const isNotFound = error instanceof ConnectError && error.code === Code.NotFound
  const isError = !!error && !isNotFound

  // Initialise YAML once the query resolves; fall back to skeleton manifest
  const [initialised, setInitialised] = useState(false)
  const [manifestYaml, setManifestYaml] = useState(SKELETON_MANIFEST)
  const [draftManifest, setDraftManifest] = useState<Manifest | null>(null)
  const [yamlParseError, setYamlParseError] = useState(false)
  const [manifestChangedSincePlan, setManifestChangedSincePlan] = useState(false)

  // Hydrate state from loaded manifest on first successful fetch (or NotFound → create-new)
  if (!initialised && !isLoading && !isError && (data !== undefined || isNotFound)) {
    const loadedManifest = data?.version?.manifest
    if (loadedManifest) {
      // Convert proto manifest → plain object → YAML string
      const plainObj = JSON.parse(JSON.stringify(loadedManifest)) as Record<string, unknown>
      const yamlStr = yaml.dump(plainObj, { lineWidth: 120 })
      setManifestYaml(yamlStr)
      setDraftManifest(loadedManifest)
    } else {
      // Create-new mode: parse and hydrate draftManifest from SKELETON_MANIFEST
      try {
        const parsedSkeleton = yaml.load(SKELETON_MANIFEST) as Record<string, unknown>
        setDraftManifest(create(ManifestSchema, parsedSkeleton))
      } catch {
        // In test environments the ManifestSchema stub may not support create();
        // leave draftManifest null and let the first editor change hydrate it.
      }
    }
    setInitialised(true)
  }

  // Use validationResult from the hook directly. When YAML is unparseable, the
  // draft manifest is not updated and no new validation is dispatched, so hide
  // the previous result to avoid showing stale errors for a different manifest.
  const errors: ValidationError[] = yamlParseError ? [] : (validationResult?.errors ?? [])
  const warnings: ValidationError[] = yamlParseError ? [] : (validationResult?.warnings ?? [])
  // Invalid YAML is not passing validation even if errors is empty
  const validationPassed = !yamlParseError && errors.length === 0

  const handleEditorChange = useCallback(
    (value: string) => {
      setManifestYaml(value)
      setManifestChangedSincePlan(true)

      // Try to parse YAML → proto Manifest for live graph + validation
      try {
        const parsed = yaml.load(value) as Record<string, unknown> | null
        if (parsed && typeof parsed === 'object') {
          const manifest = create(ManifestSchema, parsed)
          setDraftManifest(manifest)
          setYamlParseError(false)
          validate(manifest)
        } else {
          setYamlParseError(true)
        }
      } catch {
        // Invalid YAML — keep previous draft manifest, hide stale validation
        setYamlParseError(true)
      }
    },
    [validate],
  )

  const handlePlanStart = useCallback(() => {
    setManifestChangedSincePlan(false)
  }, [])

  if (isLoading) return <LoadingSkeleton />
  if (isError) return <ErrorState onRetry={() => void refetch()} />

  return (
    <div className="flex h-full gap-0 overflow-hidden">
      {/* Left panel: 70% — editor + validation + deploy */}
      <div className="flex flex-[7] flex-col overflow-hidden border-r">
        {/* Editor takes remaining height */}
        <div className="min-h-0 flex-1 overflow-auto">
          <ManifestEditor
            value={manifestYaml}
            onChange={handleEditorChange}
            validationErrors={[...errors, ...warnings]}
          />
        </div>

        {/* Validation panel (conditionally shown) */}
        {(errors.length > 0 || warnings.length > 0) && (
          <div className="shrink-0 border-t p-3">
            <ValidationPanel
              errors={errors}
              warnings={warnings}
              onLineClick={() => {}}
              onSuggestionApply={() => {}}
            />
          </div>
        )}

        {/* Deploy wizard */}
        {draftManifest && (
          <div className="shrink-0 border-t p-4">
            <DeployWizard
              manifest={draftManifest}
              manifestChanged={manifestChangedSincePlan}
              onLineClick={() => {}}
              onSuggestionApply={() => {}}
              onPlanStart={handlePlanStart}
            />
          </div>
        )}
      </div>

      {/* Right panel: 30% — relationship graph */}
      <div className="flex-[3] overflow-hidden p-4">
        <EditorGraphPanel
          manifest={draftManifest}
          validationPassed={validationPassed}
          className="h-full"
        />
      </div>

    </div>
  )
}
