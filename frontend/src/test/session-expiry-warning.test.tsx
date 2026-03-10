import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import { toast } from 'sonner'
import { AuthProvider, SESSION_WARNING_BEFORE_EXPIRY_MS } from '@/contexts/auth-context'
import { createTestToken } from './jwt-helpers'

vi.mock('sonner', () => ({
  toast: Object.assign(vi.fn(), {
    warning: vi.fn(),
    success: vi.fn(),
    error: vi.fn(),
    dismiss: vi.fn(),
  }),
}))

function TestConsumer() {
  return <div>authenticated</div>
}

describe('Session expiry warning', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows warning toast 2 minutes before expiry', () => {
    // Token expires in 3 minutes
    const token = createTestToken({
      exp: Math.floor(Date.now() / 1000) + 180,
    })

    render(
      <AuthProvider initialToken={token}>
        <TestConsumer />
      </AuthProvider>,
    )

    // Warning should fire at expiresInMs - 120_000 = ~60s from now
    expect(toast.warning).not.toHaveBeenCalled()

    // Advance to just before warning time
    act(() => {
      vi.advanceTimersByTime(59_000)
    })
    expect(toast.warning).not.toHaveBeenCalled()

    // Advance past the warning threshold
    act(() => {
      vi.advanceTimersByTime(2_000)
    })
    expect(toast.warning).toHaveBeenCalledWith(
      'Your session is about to expire.',
      expect.objectContaining({
        id: 'session-expiry-warning',
        duration: Infinity,
        action: expect.objectContaining({
          label: 'Extend session',
        }),
      }),
    )
  })

  it('shows warning immediately when token expires in less than 2 minutes', () => {
    // Token expires in 90 seconds (less than the 2-minute warning threshold)
    const token = createTestToken({
      exp: Math.floor(Date.now() / 1000) + 90,
    })

    render(
      <AuthProvider initialToken={token}>
        <TestConsumer />
      </AuthProvider>,
    )

    // Warning should fire immediately (warningInMs = max(90000 - 120000, 0) = 0)
    act(() => {
      vi.advanceTimersByTime(0)
    })
    expect(toast.warning).toHaveBeenCalled()
  })

  it('dismisses warning toast when refresh timer fires', () => {
    // Token expires in 3 minutes
    const token = createTestToken({
      exp: Math.floor(Date.now() / 1000) + 180,
    })

    render(
      <AuthProvider initialToken={token}>
        <TestConsumer />
      </AuthProvider>,
    )

    // Advance to refresh time (expiresInMs - 60_000 = ~120s)
    act(() => {
      vi.advanceTimersByTime(120_000)
    })

    expect(toast.dismiss).toHaveBeenCalledWith('session-expiry-warning')
  })

  it('dismisses warning toast on unmount', () => {
    const token = createTestToken({
      exp: Math.floor(Date.now() / 1000) + 300,
    })

    const { unmount } = render(
      <AuthProvider initialToken={token}>
        <TestConsumer />
      </AuthProvider>,
    )

    unmount()
    expect(toast.dismiss).toHaveBeenCalledWith('session-expiry-warning')
  })

  it('does not show warning when user is not authenticated', () => {
    render(
      <AuthProvider>
        <TestConsumer />
      </AuthProvider>,
    )

    act(() => {
      vi.advanceTimersByTime(300_000)
    })
    expect(toast.warning).not.toHaveBeenCalled()
  })

  it('exports SESSION_WARNING_BEFORE_EXPIRY_MS as 120 seconds', () => {
    expect(SESSION_WARNING_BEFORE_EXPIRY_MS).toBe(120_000)
  })
})
