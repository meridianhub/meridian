import { useCallback, useRef, useState } from 'react'
import { useApiClients } from '@/api/context'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import type {
  ApplyManifestResponse,
  ValidationError,
} from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import { ApplyManifestStatus } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'

export interface ValidationResult {
  errors: ValidationError[]
  warnings: ValidationError[]
  sequenceNumber: number
}

export function useManifestValidate() {
  const { manifestApplier } = useApiClients()
  const [isValidating, setIsValidating] = useState(false)
  const [result, setResult] = useState<ValidationResult | null>(null)

  const sequenceRef = useRef(0)
  const latestCompletedRef = useRef(0)
  const debounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const abortControllerRef = useRef<AbortController | null>(null)

  const validate = useCallback(
    (manifest: Manifest) => {
      // Clear any pending debounced call
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current)
      }

      debounceTimerRef.current = setTimeout(() => {
        const seq = ++sequenceRef.current

        // Abort previous in-flight request
        if (abortControllerRef.current) {
          abortControllerRef.current.abort()
        }
        const controller = new AbortController()
        abortControllerRef.current = controller

        setIsValidating(true)

        manifestApplier
          .applyManifest(
            { manifest, dryRun: true, force: false, appliedBy: '' },
            { signal: controller.signal },
          )
          .then((response: ApplyManifestResponse) => {
            // Discard stale responses
            if (seq < latestCompletedRef.current) return

            latestCompletedRef.current = seq

            const errors = response.validationErrors.filter(
              (e) => e.severity === 'ERROR',
            )
            const warnings = response.validationErrors.filter(
              (e) => e.severity !== 'ERROR',
            )

            // Also treat VALIDATION_FAILED status as an error signal
            if (
              response.status === ApplyManifestStatus.VALIDATION_FAILED &&
              errors.length === 0
            ) {
              // The status indicates failure but no structured errors came back;
              // surface step-level messages as errors.
              for (const step of response.stepResults) {
                if (step.status !== 1 && step.message) {
                  errors.push({
                    severity: 'ERROR',
                    path: '',
                    code: step.stepName,
                    message: step.message,
                    suggestion: '',
                    $typeName: 'meridian.control_plane.v1.ValidationError',
                    $unknown: undefined as never,
                  } as unknown as ValidationError)
                }
              }
            }

            setResult({ errors, warnings, sequenceNumber: seq })
          })
          .catch((err: unknown) => {
            if ((err as Error).name === 'AbortError') return
            if (seq < latestCompletedRef.current) return

            latestCompletedRef.current = seq
            setResult({
              errors: [
                {
                  severity: 'ERROR',
                  path: '',
                  code: 'NETWORK_ERROR',
                  message:
                    err instanceof Error ? err.message : 'Validation request failed',
                  suggestion: '',
                  $typeName: 'meridian.control_plane.v1.ValidationError',
                  $unknown: undefined as never,
                } as unknown as ValidationError,
              ],
              warnings: [],
              sequenceNumber: seq,
            })
          })
          .finally(() => {
            if (seq >= latestCompletedRef.current) {
              setIsValidating(false)
            }
          })
      }, 500)
    },
    [manifestApplier],
  )

  return { validate, isValidating, result }
}
