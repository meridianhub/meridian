import { useCallback, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import yaml from 'js-yaml'
import { create } from '@bufbuild/protobuf'
import { useApiClients } from '@/api/context'
import { manifestKeys } from '@/lib/query-keys'
import { ManifestSchema, type Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
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

// ── Page ──────────────────────────────────────────────────────────────────────

export function EconomyEditPage() {
  const { manifestHistory } = useApiClients()
  const { validate, result: validationResult } = useManifestValidate()

  const { data, isLoading } = useQuery({
    queryKey: manifestKeys.current(),
    queryFn: () => manifestHistory.getCurrentManifest({}),
  })

  // Initialise YAML once the query resolves; fall back to skeleton manifest
  const [initialised, setInitialised] = useState(false)
  const [manifestYaml, setManifestYaml] = useState(SKELETON_MANIFEST)
  const [draftManifest, setDraftManifest] = useState<Manifest | null>(null)
  const [yamlParseError, setYamlParseError] = useState(false)
  const [manifestChangedSincePlan, setManifestChangedSincePlan] = useState(false)

  // Hydrate state from loaded manifest on first successful fetch
  if (!initialised && !isLoading && data !== undefined) {
    const loadedManifest = data?.version?.manifest
    if (loadedManifest) {
      // Convert proto manifest → plain object → YAML string
      const plainObj = JSON.parse(JSON.stringify(loadedManifest)) as Record<string, unknown>
      const yamlStr = yaml.dump(plainObj, { lineWidth: 120 })
      setManifestYaml(yamlStr)
      setDraftManifest(loadedManifest)
    }
    setInitialised(true)
  }

  // Use validationResult from the hook directly. When YAML is unparseable, the
  // draft manifest is not updated and no new validation is dispatched, so hide
  // the previous result to avoid showing stale errors for a different manifest.
  const errors: ValidationError[] = yamlParseError ? [] : (validationResult?.errors ?? [])
  const warnings: ValidationError[] = yamlParseError ? [] : (validationResult?.warnings ?? [])
  const validationPassed = errors.length === 0

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
