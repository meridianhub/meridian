import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { useApiClients } from '@/api/context'
import { useAuth } from '@/contexts/auth-context'
import { manifestKeys } from '@/lib/query-keys'
import {
  ApplyManifestStatus,
  type ApplyManifestResponse,
  type StepResult,
  type ValidationError,
} from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import { create } from '@bufbuild/protobuf'
import { ManifestSchema } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

interface ManifestApplyDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function ManifestApplyDialog({ open, onOpenChange }: ManifestApplyDialogProps) {
  const { manifestApplier } = useApiClients()
  const { claims } = useAuth()
  const queryClient = useQueryClient()
  const [manifestJson, setManifestJson] = useState('')
  const [dryRunResult, setDryRunResult] = useState<ApplyManifestResponse | null>(null)
  const [parseError, setParseError] = useState<string | null>(null)
  const [applyError, setApplyError] = useState<string | null>(null)

  const appliedBy = claims?.userId ?? 'unknown'

  function resetState() {
    setManifestJson('')
    setDryRunResult(null)
    setParseError(null)
    setApplyError(null)
  }

  function handleOpenChange(nextOpen: boolean) {
    if (!nextOpen) resetState()
    onOpenChange(nextOpen)
  }

  const dryRunMutation = useMutation({
    mutationFn: async () => {
      const parsed = JSON.parse(manifestJson) as Record<string, unknown>
      const manifest = create(ManifestSchema, parsed)
      return manifestApplier.applyManifest({
        manifest,
        dryRun: true,
        appliedBy,
      })
    },
    onSuccess: (result) => {
      setDryRunResult(result)
      setParseError(null)
    },
    onError: (err: Error) => {
      setParseError(err.message)
      setDryRunResult(null)
    },
  })

  const applyMutation = useMutation({
    mutationFn: async () => {
      const parsed = JSON.parse(manifestJson) as Record<string, unknown>
      const manifest = create(ManifestSchema, parsed)
      return manifestApplier.applyManifest({
        manifest,
        dryRun: false,
        appliedBy,
      })
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: manifestKeys.all })
      handleOpenChange(false)
    },
    onError: (err: Error) => {
      setApplyError(err.message)
    },
  })

  function handleJsonChange(value: string) {
    setManifestJson(value)
    setDryRunResult(null)
    setParseError(null)
    setApplyError(null)
  }

  function handlePreview() {
    setParseError(null)
    setDryRunResult(null)
    setApplyError(null)
    dryRunMutation.mutate()
  }

  function handleApply() {
    applyMutation.mutate()
  }

  const previewSucceeded =
    dryRunResult != null &&
    dryRunResult.status !== ApplyManifestStatus.VALIDATION_FAILED &&
    dryRunResult.status !== ApplyManifestStatus.FAILED

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Apply Manifest</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1">
            <label htmlFor="manifest-json" className="text-sm font-medium">
              Manifest JSON
            </label>
            <textarea
              id="manifest-json"
              value={manifestJson}
              onChange={(e) => handleJsonChange(e.target.value)}
              className="min-h-[200px] w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-sm"
              placeholder='{"version": "1.0", "metadata": {...}, "instruments": [...]}'
            />
          </div>

          {parseError && (
            <div data-testid="parse-error" className="rounded border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
              {parseError}
            </div>
          )}

          {dryRunResult && <DryRunResultPanel result={dryRunResult} />}

          {applyError && (
            <div data-testid="apply-error" className="rounded border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
              Apply failed: {applyError}
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => handleOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={handlePreview}
            disabled={!manifestJson.trim() || dryRunMutation.isPending}
          >
            {dryRunMutation.isPending ? 'Previewing...' : 'Preview Changes'}
          </Button>
          <Button
            type="button"
            onClick={handleApply}
            disabled={!previewSucceeded || applyMutation.isPending}
          >
            {applyMutation.isPending ? 'Applying...' : 'Apply Manifest'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function DryRunResultPanel({ result }: { result: ApplyManifestResponse }) {
  const isFailed =
    result.status === ApplyManifestStatus.VALIDATION_FAILED ||
    result.status === ApplyManifestStatus.FAILED

  return (
    <div data-testid="dry-run-result" className="rounded border border-border bg-muted/30 p-3 text-sm">
      <h4 className="mb-2 font-medium">Preview Results</h4>

      {result.diffSummary && (
        <p className="mb-2 text-muted-foreground">{result.diffSummary}</p>
      )}

      {result.stepResults.length > 0 && (
        <div className="mb-2">
          <p className="text-xs font-medium text-muted-foreground">Steps:</p>
          <ul className="mt-1 space-y-1">
            {result.stepResults.map((step: StepResult, i: number) => (
              <li key={i} className="flex items-center gap-2 text-xs">
                <span className="font-mono">{step.stepName}</span>
                <span className={step.status === 1 ? 'text-green-600' : step.status === 2 ? 'text-red-600' : 'text-muted-foreground'}>
                  {step.status === 1 ? 'SUCCESS' : step.status === 2 ? 'FAILED' : 'SKIPPED'}
                </span>
                {step.message && <span className="text-muted-foreground">- {step.message}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}

      {isFailed && result.validationErrors.length > 0 && (
        <div data-testid="validation-errors">
          <p className="text-xs font-medium text-destructive">Validation Errors:</p>
          <ul className="mt-1 space-y-1">
            {result.validationErrors.map((err: ValidationError, i: number) => (
              <li key={i} className="text-xs text-destructive">
                <span className="font-mono">{err.path}</span>: {err.message}
                {err.suggestion && <span className="ml-1 text-muted-foreground">({err.suggestion})</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
