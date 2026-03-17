import { useState, useCallback } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { AlertCircle, CheckCircle2, Loader2, TriangleAlert } from 'lucide-react'
import { useManifestPlan } from '../hooks/use-manifest-plan'
import { ValidationPanel } from './validation-panel'
import { ApplyPhasesStepper } from './apply-phases-stepper'
import { ConflictResolutionModal, type ConflictResolution } from './conflict-resolution-modal'
import { isVersionConflict } from '../lib/version-conflict'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useApiClients } from '@/api/context'
import { useAuth } from '@/contexts/auth-context'
import { manifestKeys } from '@/lib/query-keys'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import type {
  StepResult,
  ValidationError,
} from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import {
  ApplyManifestStatus,
  StepResultStatus,
} from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import type { ManifestPlan } from '../hooks/use-manifest-plan'

// ── Step state machine ──────────────────────────────────────────────────────

type DeployStep = 'idle' | 'planning' | 'review' | 'applying' | 'success' | 'error'

// ── Props ───────────────────────────────────────────────────────────────────

export interface DeployWizardProps {
  manifest: Manifest
  /** When true, the manifest has been edited since the last successful plan. */
  manifestChanged: boolean
  /** Called when the user clicks a validation error path. */
  onLineClick?: (path: string) => void
  /** Called when the user applies a validation suggestion. */
  onSuggestionApply?: (path: string, suggestion: string) => void
  /** Called when a plan run is initiated so callers can reset stale flags. */
  onPlanStart?: () => void
  /** Called when the user chooses to reload the server version after a version conflict. */
  onReloadManifest: (serverManifest: Manifest) => void
}

// ── Plan hash ───────────────────────────────────────────────────────────────

/** Simple stable hash of manifest content to detect staleness. */
function hashManifest(manifest: Manifest): string {
  return JSON.stringify(manifest)
}

// ── Component ───────────────────────────────────────────────────────────────

export function DeployWizard({
  manifest,
  manifestChanged,
  onLineClick,
  onSuggestionApply,
  onPlanStart,
  onReloadManifest,
}: DeployWizardProps) {
  const { claims } = useAuth()
  const { manifestApplier, manifestHistory } = useApiClients()
  const queryClient = useQueryClient()

  const [step, setStep] = useState<DeployStep>('idle')
  const [applyError, setApplyError] = useState<string | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [applyStepResults, setApplyStepResults] = useState<StepResult[]>([])

  // Conflict resolution state
  const [conflictOpen, setConflictOpen] = useState(false)
  const [serverManifest, setServerManifest] = useState<Manifest | null>(null)

  // Track plan hash to detect manifest edits after planning
  const [planHash, setPlanHash] = useState<string | null>(null)
  const isPlanStale = planHash !== null && (manifestChanged || planHash !== hashManifest(manifest))

  const { plan, planManifestAsync, isPlanning } = useManifestPlan()

  // ── Subtask 1: Plan ──────────────────────────────────────────────────────

  const handlePlan = useCallback(async () => {
    setStep('planning')
    setApplyError(null)
    onPlanStart?.()
    try {
      await planManifestAsync(manifest)
      setPlanHash(hashManifest(manifest))
      setStep('review')
    } catch {
      setStep('error')
      setApplyError('Planning failed. Please try again.')
    }
  }, [manifest, planManifestAsync, onPlanStart])

  // ── Subtask 2: Validation check ──────────────────────────────────────────

  const validationErrors: ValidationError[] = plan?.validationErrors ?? []
  const errors = validationErrors.filter((e) => e.severity !== 'WARNING')
  const warnings = validationErrors.filter((e) => e.severity === 'WARNING')
  const hasBlockingErrors = errors.length > 0

  // ── Subtask 3: Apply mutation ────────────────────────────────────────────

  const applyMutation = useMutation({
    mutationFn: async () => {
      return manifestApplier.applyManifest({
        manifest,
        dryRun: false,
        force: false,
        appliedBy: claims?.userId ?? '',
      })
    },
    onSuccess: (response) => {
      setApplyStepResults(response.stepResults ?? [])
      const isFailed = response.status === ApplyManifestStatus.FAILED
      const hasAnyStepSuccess = (response.stepResults ?? []).some(
        (s) => s.status === StepResultStatus.SUCCESS,
      )
      void queryClient.invalidateQueries({ queryKey: manifestKeys.all })
      if (isFailed) {
        setApplyError(
          hasAnyStepSuccess
            ? 'Apply completed with partial failures. Some phases did not succeed.'
            : 'Apply failed. No phases succeeded.',
        )
        setStep('error')
      } else {
        setStep('success')
      }
      setConfirmOpen(false)
    },
    onError: (err: unknown) => {
      setConfirmOpen(false)
      if (isVersionConflict(err)) {
        setStep('error')
        setApplyError('Version conflict detected. Loading server version…')
        // Fetch the current server manifest to show the diff
        void manifestHistory.getCurrentManifest({}).then((res) => {
          if (res.version?.manifest) {
            setApplyError(null)
            setServerManifest(res.version.manifest)
            setConflictOpen(true)
          } else {
            setApplyError('Version conflict detected, but the server returned no manifest.')
          }
        }).catch(() => {
          setApplyError('Version conflict detected, but failed to fetch current version.')
        })
        return
      }
      const message = err instanceof Error ? err.message : 'Apply failed. Please try again.'
      setApplyError(message)
      setStep('error')
    },
  })

  // ── Conflict resolution ──────────────────────────────────────────────────

  const handleConflictResolve = useCallback((resolution: ConflictResolution) => {
    setConflictOpen(false)
    switch (resolution) {
      case 'force':
        setStep('applying')
        // Re-apply with force=true and expectedSequenceNumber=0 to skip the check
        manifestApplier.applyManifest({
          manifest,
          dryRun: false,
          force: true,
          appliedBy: claims?.userId ?? '',
          expectedSequenceNumber: BigInt(0),
        }).then((response) => {
          setApplyStepResults(response.stepResults ?? [])
          const isFailed = response.status === ApplyManifestStatus.FAILED
          const hasAnyStepSuccess = (response.stepResults ?? []).some(
            (s) => s.status === StepResultStatus.SUCCESS,
          )
          void queryClient.invalidateQueries({ queryKey: manifestKeys.all })
          if (isFailed) {
            setApplyError(
              hasAnyStepSuccess
                ? 'Apply completed with partial failures. Some phases did not succeed.'
                : 'Apply failed. No phases succeeded.',
            )
            setStep('error')
          } else {
            setStep('success')
          }
        }).catch((err: unknown) => {
          const message = err instanceof Error ? err.message : 'Force apply failed.'
          setApplyError(message)
          setStep('error')
        })
        break
      case 'reload':
        if (serverManifest) {
          onReloadManifest(serverManifest)
        }
        setStep('idle')
        setApplyError(null)
        setServerManifest(null)
        break
      case 'cancel':
        setStep('error')
        setApplyError('Apply cancelled due to version conflict.')
        setServerManifest(null)
        break
    }
  }, [manifest, claims?.userId, manifestApplier, queryClient, serverManifest, onReloadManifest])

  // ── Subtask 4: Plan hash invalidation ────────────────────────────────────

  // Shared eligibility guard — re-evaluated inside the confirm path to prevent
  // deploying a stale or invalid plan even if the manifest changes while the
  // dialog is already open.
  const isApplyAllowed =
    plan !== null && !hasBlockingErrors && !isPlanStale && Boolean(claims?.userId)

  const canApply = step === 'review' && isApplyAllowed

  const handleConfirmApply = useCallback(() => {
    if (!isApplyAllowed) return
    setStep('applying')
    applyMutation.mutate()
  }, [applyMutation, isApplyAllowed])

  const handleRetry = useCallback(() => {
    setStep('idle')
    setApplyError(null)
    setApplyStepResults([])
  }, [])

  // ── Render ───────────────────────────────────────────────────────────────

  return (
    <div className="space-y-4">
      {/* Step header */}
      <StepIndicator step={step} />

      {/* Validation errors shown in review step */}
      {step === 'review' && plan && (
        <div className="space-y-3">
          {/* Diff summary */}
          <div className="rounded-md border bg-muted/40 px-4 py-3 text-sm">
            <span className="font-medium">Plan summary: </span>
            <span className="text-muted-foreground">
              {plan.diffSummary || 'No changes detected'}
            </span>
          </div>

          {/* Count badges */}
          {(plan.counts.add > 0 || plan.counts.modify > 0 || plan.counts.remove > 0) && (
            <div className="flex gap-2 text-xs">
              {plan.counts.add > 0 && (
                <span className="rounded-full border border-success/30 bg-success-muted px-2 py-0.5 text-success-foreground">
                  +{plan.counts.add} add
                </span>
              )}
              {plan.counts.modify > 0 && (
                <span className="rounded-full border border-warning/30 bg-warning-muted px-2 py-0.5 text-warning-foreground">
                  ~{plan.counts.modify} modify
                </span>
              )}
              {plan.counts.remove > 0 && (
                <span className="rounded-full border border-destructive/30 bg-destructive/10 px-2 py-0.5 text-destructive">
                  -{plan.counts.remove} remove
                </span>
              )}
            </div>
          )}

          {/* Validation errors / warnings */}
          {validationErrors.length > 0 && (
            <div className="rounded-md border border-destructive/20 bg-destructive/5 p-3">
              <ValidationPanel
                errors={errors}
                warnings={warnings}
                onLineClick={onLineClick ?? (() => {})}
                onSuggestionApply={onSuggestionApply ?? (() => {})}
              />
            </div>
          )}

          {/* Stale plan warning */}
          {isPlanStale && (
            <div className="flex items-center gap-2 rounded-md border border-warning/30 bg-warning-muted px-3 py-2 text-sm text-warning-foreground">
              <TriangleAlert className="h-4 w-4 shrink-0" />
              <span>Manifest has changed since last plan. Re-plan before applying.</span>
            </div>
          )}
        </div>
      )}

      {/* Apply phases stepper — shown while applying or after apply completes */}
      {(step === 'applying' || step === 'success' || (step === 'error' && applyStepResults.length > 0)) && (
        <div className="rounded-md border bg-muted/40 px-4 py-3">
          <ApplyPhasesStepper
            steps={applyStepResults}
            isApplying={step === 'applying'}
          />
        </div>
      )}

      {/* Success state */}
      {step === 'success' && (
        <div className="flex items-center gap-2 rounded-md border border-success/30 bg-success-muted px-3 py-2 text-sm text-success-foreground">
          <CheckCircle2 className="h-4 w-4 shrink-0" />
          <span>Manifest applied successfully.</span>
        </div>
      )}

      {/* Error state */}
      {step === 'error' && applyError && (
        <div className="flex items-center gap-2 rounded-md border border-destructive/20 bg-destructive/5 px-3 py-2 text-sm text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" />
          <span>{applyError}</span>
        </div>
      )}

      {/* Action buttons */}
      <div className="flex items-center gap-2">
        {(step === 'idle' || step === 'review' || step === 'error') && step !== 'success' && (
          <Button
            onClick={() => void handlePlan()}
            disabled={isPlanning}
            variant="outline"
            size="sm"
          >
            {isPlanning ? (
              <>
                <Loader2 className="animate-spin" />
                Planning…
              </>
            ) : step === 'review' && !isPlanStale ? (
              'Re-plan'
            ) : (
              'Plan'
            )}
          </Button>
        )}

        {step === 'review' && (
          <>
            <Button
              onClick={() => setConfirmOpen(true)}
              disabled={!canApply}
              size="sm"
              data-testid="deploy-apply-button"
            >
              Apply
            </Button>
            {!canApply && (
              <span className="text-xs text-muted-foreground" data-testid="apply-disabled-reason">
                {hasBlockingErrors
                  ? 'Cannot apply: plan contains validation errors'
                  : isPlanStale
                    ? 'Cannot apply: manifest changed since last plan'
                    : 'Cannot apply'}
              </span>
            )}
          </>
        )}

        {step === 'error' && (
          <Button onClick={handleRetry} variant="ghost" size="sm">
            Dismiss
          </Button>
        )}

        {step === 'success' && (
          <Button onClick={handleRetry} variant="ghost" size="sm">
            Plan again
          </Button>
        )}
      </div>

      {/* Confirmation modal */}
      <ConfirmApplyDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        plan={plan}
        isApplying={step === 'applying'}
        canConfirm={isApplyAllowed}
        onConfirm={handleConfirmApply}
      />

      {/* Version conflict modal */}
      {serverManifest && (
        <ConflictResolutionModal
          open={conflictOpen}
          onResolve={handleConflictResolve}
          userManifest={manifest}
          serverManifest={serverManifest}
        />
      )}
    </div>
  )
}

// ── Step indicator ───────────────────────────────────────────────────────────

const STEP_LABELS: Record<DeployStep, string> = {
  idle: 'Ready to plan',
  planning: 'Planning…',
  review: 'Review plan',
  applying: 'Applying…',
  success: 'Applied',
  error: 'Failed',
}

function StepIndicator({ step }: { step: DeployStep }) {
  return (
    <div className="flex items-center gap-2 text-sm text-muted-foreground">
      {(step === 'planning' || step === 'applying') && (
        <Loader2 className="h-4 w-4 animate-spin" />
      )}
      {step === 'success' && <CheckCircle2 className="h-4 w-4 text-success" />}
      {step === 'error' && <AlertCircle className="h-4 w-4 text-destructive" />}
      <span data-testid="deploy-step-label">{STEP_LABELS[step]}</span>
    </div>
  )
}

// ── Confirmation dialog ──────────────────────────────────────────────────────

interface ConfirmApplyDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  plan: ManifestPlan | null
  isApplying: boolean
  canConfirm: boolean
  onConfirm: () => void
}

function ConfirmApplyDialog({
  open,
  onOpenChange,
  plan,
  isApplying,
  canConfirm,
  onConfirm,
}: ConfirmApplyDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Confirm Apply</DialogTitle>
          <DialogDescription>
            This will apply the planned changes to your live economy. This action cannot be
            undone.
          </DialogDescription>
        </DialogHeader>

        {plan && (
          <div className="rounded-md border bg-muted/40 px-4 py-3 text-sm">
            <span className="font-medium">Changes: </span>
            <span className="text-muted-foreground">
              {plan.diffSummary || 'No changes detected'}
            </span>
          </div>
        )}

        <DialogFooter showCloseButton={!isApplying}>
          <Button
            onClick={onConfirm}
            disabled={isApplying || !canConfirm}
            data-testid="confirm-apply-button"
          >
            {isApplying ? (
              <>
                <Loader2 className="animate-spin" />
                Applying…
              </>
            ) : (
              'Apply'
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
