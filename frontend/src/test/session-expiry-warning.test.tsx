import { describe, it, expect, vi, beforeEach, afterEach, beforeAll, afterAll } from 'vitest'
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

const mockFetch = vi.fn()
const originalFetch = global.fetch

beforeAll(() => {
  global.fetch = mockFetch
})

afterAll(() => {
  global.fetch = originalFetch
})

function TestConsumer() {
  return <div>authenticated</div>
}

describe('Session expiry warning', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    sessionStorage.clear()
    mockFetch.mockReset()
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

  describe('onClick handler on "Extend session" action', () => {
    function renderAndTriggerWarning() {
      // Token expires in 90 seconds — warning fires immediately
      const token = createTestToken({
        exp: Math.floor(Date.now() / 1000) + 90,
      })

      render(
        <AuthProvider initialToken={token}>
          <TestConsumer />
        </AuthProvider>,
      )

      act(() => {
        vi.advanceTimersByTime(0)
      })

      expect(toast.warning).toHaveBeenCalled()

      // Extract the onClick from the action passed to toast.warning
      const call = vi.mocked(toast.warning).mock.calls[0]
      const options = call[1] as { action: { onClick: () => void } }
      return options.action.onClick
    }

    it('dismisses warning and shows success toast when refresh succeeds', async () => {
      const newToken = createTestToken({ exp: Math.floor(Date.now() / 1000) + 3600 })
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: async () => ({ accessToken: newToken }),
      })

      const onClick = renderAndTriggerWarning()

      await act(async () => {
        onClick()
        await vi.advanceTimersByTimeAsync(0)
      })

      expect(toast.dismiss).toHaveBeenCalledWith('session-expiry-warning')
      expect(toast.success).toHaveBeenCalledWith('Session extended.')
      expect(toast.error).not.toHaveBeenCalled()
    })

    it('shows error toast without redirect message when refresh fails', async () => {
      mockFetch.mockResolvedValueOnce({ ok: false, status: 503 })

      const onClick = renderAndTriggerWarning()

      await act(async () => {
        onClick()
        // Only flush microtasks — do not advance timers so background refresh
        // timer does not fire and confound the assertions.
        await Promise.resolve()
      })

      expect(toast.error).toHaveBeenCalledWith(
        'Failed to extend session. Please refresh the page to log in again.',
      )
      expect(toast.success).not.toHaveBeenCalled()
    })

    it('does not issue a second refresh if one is already in flight (button guard)', async () => {
      // Never resolves — simulates an in-flight request
      mockFetch.mockReturnValue(new Promise(() => {}))

      const onClick = renderAndTriggerWarning()

      act(() => {
        onClick() // first call — starts the in-flight request
        onClick() // second call — should be a no-op
      })

      // Only one fetch should have been made
      expect(mockFetch).toHaveBeenCalledTimes(1)
    })

    it('deduplicates when background timer fires while button refresh is in flight', async () => {
      // Never resolves — keeps in-flight promise alive
      mockFetch.mockReturnValue(new Promise(() => {}))

      // Token expires in 90s — warning fires at t=0, timer fires at t=30s
      const token = createTestToken({
        exp: Math.floor(Date.now() / 1000) + 90,
      })

      render(
        <AuthProvider initialToken={token}>
          <TestConsumer />
        </AuthProvider>,
      )

      // Trigger warning immediately
      act(() => {
        vi.advanceTimersByTime(0)
      })

      // Extract and invoke the button onClick (starts in-flight request)
      const call = vi.mocked(toast.warning).mock.calls[0]
      const options = call[1] as { action: { onClick: () => void } }
      act(() => {
        options.action.onClick()
      })

      // Advance to when the background refresh timer fires
      act(() => {
        vi.advanceTimersByTime(30_000)
      })

      // Both the button and the timer share the same in-flight promise:
      // only one fetch should have been issued
      expect(mockFetch).toHaveBeenCalledTimes(1)
    })
  })
})
