import { useState, useCallback, useMemo } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { AlertCircle, CheckCircle2, Loader2 } from 'lucide-react'
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
import { ApplyManifestStatus } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import type { ManifestNodeType } from '@/features/manifests/lib/manifest-graph-model'
import { ResourceForm } from './resource-form'
import { type ConflictResolution } from './conflict-resolution-modal'
import { isVersionConflict } from '../lib/version-conflict'
import {
  getResourceSchema,
  buildResourcePayload,
  extractFormValues,
  type ResourceSchema,
} from '../lib/resource-schema-registry'

type ModalState = 'editing' | 'applying' | 'success' | 'error'

export interface ApplyResourceModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  nodeType: ManifestNodeType
  initialData?: Record<string, unknown>
}

function validateRequiredFields(
  schema: ResourceSchema,
  values: Record<string, string>,
): string[] {
  const missing: string[] = []
  for (const field of schema.fields) {
    if (field.required && !values[field.name]?.trim()) {
      missing.push(field.label)
    }
  }
  return missing
}

export function ApplyResourceModal({
  open,
  onOpenChange,
  nodeType,
  initialData,
}: ApplyResourceModalProps) {
  const { claims } = useAuth()
  const { manifestApplier } = useApiClients()
  const queryClient = useQueryClient()

  const schema = getResourceSchema(nodeType)

  const defaultValues = useMemo(() => {
    if (!schema) return {}
    if (initialData) return extractFormValues(schema, initialData)
    return {}
  }, [schema, initialData])

  const [values, setValues] = useState<Record<string, string>>(defaultValues)
  const [state, setState] = useState<ModalState>('editing')
  const [errorMessage, setErrorMessage] = useState<string | null>(null)

  // Conflict resolution state
  const [conflictOpen, setConflictOpen] = useState(false)

  const resetState = useCallback(() => {
    setValues(defaultValues)
    setState('editing')
    setErrorMessage(null)
    setConflictOpen(false)
  }, [defaultValues])

  const handleOpenChange = useCallback(
    (isOpen: boolean) => {
      if (!isOpen) {
        resetState()
      }
      onOpenChange(isOpen)
    },
    [onOpenChange, resetState],
  )

  const applyMutation = useMutation({
    mutationFn: async () => {
      if (!schema) throw new Error('No schema for this resource type')

      const payload = buildResourcePayload(schema, values)

      return manifestApplier.applyResource({
        resourceType: schema.resourceType,
        resource: {
          case: schema.oneofCase as never,
          value: payload as never,
        },
        dryRun: false,
        appliedBy: claims?.userId ?? '',
        expectedSequenceNumber: BigInt(0),
      })
    },
    onSuccess: (response) => {
      void queryClient.invalidateQueries({ queryKey: manifestKeys.all })

      if (
        response.status === ApplyManifestStatus.APPLIED
      ) {
        setState('success')
      } else {
        const validationMessages = response.validationErrors
          ?.map((e) => e.message)
          .join('; ')
        setErrorMessage(
          validationMessages || response.diffSummary || 'Apply failed',
        )
        setState('error')
      }
    },
    onError: (err: unknown) => {
      if (isVersionConflict(err)) {
        setConflictOpen(true)
        setState('error')
        return
      }
      const message =
        err instanceof Error ? err.message : 'Apply failed. Please try again.'
      setErrorMessage(message)
      setState('error')
    },
  })

  const missingFields = schema ? validateRequiredFields(schema, values) : []
  const canApply =
    state === 'editing' &&
    missingFields.length === 0 &&
    Boolean(claims?.userId)

  const handleApply = useCallback(() => {
    setState('applying')
    applyMutation.mutate()
  }, [applyMutation])

  const handleConflictResolve = useCallback((resolution: ConflictResolution) => {
    setConflictOpen(false)
    switch (resolution) {
      case 'force':
        if (!schema) return
        setState('applying')
        manifestApplier.applyResource({
          resourceType: schema.resourceType,
          resource: {
            case: schema.oneofCase as never,
            value: buildResourcePayload(schema, values) as never,
          },
          dryRun: false,
          appliedBy: claims?.userId ?? '',
          expectedSequenceNumber: BigInt(0),
        }).then((response) => {
          void queryClient.invalidateQueries({ queryKey: manifestKeys.all })
          if (response.status === ApplyManifestStatus.APPLIED) {
            setState('success')
          } else {
            const validationMessages = response.validationErrors
              ?.map((e) => e.message)
              .join('; ')
            setErrorMessage(validationMessages || response.diffSummary || 'Apply failed')
            setState('error')
          }
        }).catch((err: unknown) => {
          const message = err instanceof Error ? err.message : 'Force apply failed.'
          setErrorMessage(message)
          setState('error')
        })
        break
      case 'reload':
        resetState()
        break
      case 'cancel':
        setErrorMessage('Apply cancelled due to version conflict.')
        setState('error')
        break
    }
  }, [schema, values, claims?.userId, manifestApplier, queryClient, resetState])

  if (!schema) {
    return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit Resource</DialogTitle>
            <DialogDescription>
              Editing is not supported for this resource type.
            </DialogDescription>
          </DialogHeader>
        </DialogContent>
      </Dialog>
    )
  }

  const isEditing = initialData !== undefined

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {isEditing ? `Edit ${schema.label}` : `Add ${schema.label}`}
          </DialogTitle>
          <DialogDescription>
            {isEditing
              ? `Update this ${schema.label.toLowerCase()} and apply changes to the live economy.`
              : `Create a new ${schema.label.toLowerCase()} and apply it to the live economy.`}
          </DialogDescription>
        </DialogHeader>

        {state === 'success' ? (
          <div
            className="flex items-center gap-2 rounded-md border border-success/30 bg-success-muted px-3 py-2 text-sm text-success-foreground"
            data-testid="apply-resource-success"
          >
            <CheckCircle2 className="h-4 w-4 shrink-0" />
            <span>{schema.label} applied successfully.</span>
          </div>
        ) : (
          <>
            <ResourceForm
              schema={schema}
              values={values}
              onChange={setValues}
              disabled={state === 'applying'}
            />

            {state === 'error' && errorMessage && (
              <div
                className="flex items-center gap-2 rounded-md border border-destructive/20 bg-destructive/5 px-3 py-2 text-sm text-destructive"
                data-testid="apply-resource-error"
              >
                <AlertCircle className="h-4 w-4 shrink-0" />
                <span>{errorMessage}</span>
              </div>
            )}

            {conflictOpen && (
              <div
                className="space-y-2 rounded-md border border-warning/30 bg-warning-muted p-3"
                data-testid="apply-resource-conflict"
              >
                <div className="flex items-center gap-2 text-sm font-medium text-warning-foreground">
                  <AlertCircle className="h-4 w-4 shrink-0" />
                  Version conflict: the manifest was modified by another user.
                </div>
                <div className="flex gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => handleConflictResolve('reload')}
                    data-testid="conflict-reload"
                  >
                    Reload
                  </Button>
                  <Button
                    variant="destructive"
                    size="sm"
                    onClick={() => handleConflictResolve('force')}
                    data-testid="conflict-force"
                  >
                    Force Apply
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => handleConflictResolve('cancel')}
                    data-testid="conflict-cancel"
                  >
                    Cancel
                  </Button>
                </div>
              </div>
            )}
          </>
        )}

        <DialogFooter showCloseButton={state !== 'applying'}>
          {state === 'success' ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => handleOpenChange(false)}
            >
              Close
            </Button>
          ) : (
            <Button
              onClick={handleApply}
              disabled={!canApply || state === 'applying'}
              data-testid="apply-resource-submit"
            >
              {state === 'applying' ? (
                <>
                  <Loader2 className="animate-spin" />
                  Applying...
                </>
              ) : (
                'Apply'
              )}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
