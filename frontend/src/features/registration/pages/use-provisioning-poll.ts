import { useCallback, useEffect, useRef, useState } from 'react'
import type { ProvisioningStatus } from './registration-helpers'

// How long to poll the provisioning status endpoint before showing the
// timeout state. 60s matches the typical worst-case for tenant schema
// provisioning in the demo environment.
const PROVISIONING_POLL_TIMEOUT_MS = 60_000
const PROVISIONING_POLL_INTERVAL_MS = 1_000

interface UseProvisioningPollResult {
  status: ProvisioningStatus | null
  /** Begin polling for the given tenant. Cancels any in-flight loop first. */
  start: (tenantId: string) => void
  /** Returns true if the poll loop is active or in a terminal UI state. */
  isActive: boolean
  /** Restart polling for the most recent target (used by the timeout 'Keep waiting' button). */
  retry: () => void
}

/**
 * Polls the provisioning status endpoint until the tenant is active or
 * provisioning fails/times out, then invokes onComplete.
 *
 * Treats any transient fetch errors as "still pending" so a network blip
 * does not bounce the user out. Returns null status until polling starts.
 *
 * Contract (parsed defensively to absorb both REST and proto-style shapes):
 *   GET /api/v1/provisioning-status?tenant_id=<id>
 *     200 → one of:
 *       { overall: 'PENDING' | 'IN_PROGRESS' | 'COMPLETED' | 'FAILED' | 'active' }
 *       { overall_status: 'TENANT_STATUS_ACTIVE' | 'TENANT_STATUS_PROVISIONING_FAILED' | ... }
 *       { overallStatus: <same enum, camelCase variant> }
 *     non-200 → treated as pending and retried until timeout
 */
export function useProvisioningPoll(
  onComplete: () => void,
): UseProvisioningPollResult {
  const [status, setStatus] = useState<ProvisioningStatus | null>(null)
  const timerRef = useRef<number | null>(null)
  const cancelledRef = useRef(false)
  const targetRef = useRef<string | null>(null)
  // Generation guard: each call to start() bumps this counter. Async ticks
  // captured by an older generation bail out before mutating state, so a
  // stale in-flight fetch from a previous run cannot race the current loop
  // (e.g. user clicks 'Keep waiting' just as the previous loop's tick was
  // about to fire setStatus('timeout')).
  const pollGenerationRef = useRef(0)
  // Always read the freshest onComplete in async ticks without re-running the
  // poll loop. The ref is updated in an effect so render stays pure.
  const onCompleteRef = useRef(onComplete)
  useEffect(() => {
    onCompleteRef.current = onComplete
  }, [onComplete])

  // Cancel any pending tick on unmount so React state updates don't fire on
  // an unmounted component.
  useEffect(() => {
    return () => {
      cancelledRef.current = true
      if (timerRef.current !== null) {
        window.clearTimeout(timerRef.current)
        timerRef.current = null
      }
    }
  }, [])

  const startPolling = useCallback((tenantId: string) => {
    // Cancel any prior loop before starting a new one. Bumping the generation
    // invalidates ticks already queued by the previous run; clearing the
    // timer drops the next-scheduled poll.
    pollGenerationRef.current += 1
    const generation = pollGenerationRef.current
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current)
      timerRef.current = null
    }

    cancelledRef.current = false
    targetRef.current = tenantId
    setStatus('pending')
    const startTime = Date.now()

    const isStale = () => cancelledRef.current || generation !== pollGenerationRef.current

    const tick = async () => {
      if (isStale()) return
      if (Date.now() - startTime >= PROVISIONING_POLL_TIMEOUT_MS) {
        setStatus('timeout')
        return
      }
      try {
        const res = await fetch(
          `/api/v1/provisioning-status?tenant_id=${encodeURIComponent(tenantId)}`,
        )
        if (res.ok) {
          const body = (await res.json().catch(() => null)) as {
            overall?: string
            overall_status?: string
            overallStatus?: string
          } | null
          const overall = body?.overall ?? body?.overall_status ?? body?.overallStatus ?? ''
          if (
            overall === 'COMPLETED' ||
            overall === 'active' ||
            overall === 'TENANT_STATUS_ACTIVE'
          ) {
            if (isStale()) return
            setStatus(null)
            onCompleteRef.current()
            return
          }
          if (overall === 'FAILED' || overall === 'TENANT_STATUS_PROVISIONING_FAILED') {
            if (isStale()) return
            setStatus('failed')
            return
          }
        }
      } catch {
        // Treat as pending; we'll retry on the next tick.
      }
      if (isStale()) return
      timerRef.current = window.setTimeout(
        () => void tick(),
        PROVISIONING_POLL_INTERVAL_MS,
      )
    }

    void tick()
  }, [])

  const retry = useCallback(() => {
    const tenantId = targetRef.current
    if (tenantId !== null) {
      startPolling(tenantId)
    }
  }, [startPolling])

  return {
    status,
    start: startPolling,
    isActive: status !== null,
    retry,
  }
}
