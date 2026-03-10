import { describe, it, expect, vi } from 'vitest'
import { Code, ConnectError } from '@connectrpc/connect'
import {
  handleConnectError,
  withErrorHandling,
  withToastErrorHandling,
  useErrorHandler,
  type ConnectErrorResult,
} from '@/lib/error-handling'
import { toast } from 'sonner'

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
  },
}))

// Helper to create a ConnectError with debug details (simulating wire format)
function makeConnectError(
  message: string,
  code: Code,
  details?: Array<{ type: string; debug: unknown }>,
): ConnectError {
  const err = new ConnectError(message, code)
  if (details) {
    // Simulate IncomingDetail format as received from wire
    err.details = details.map((d) => ({
      type: d.type,
      value: new Uint8Array(),
      debug: d.debug,
    }))
  }
  return err
}

describe('handleConnectError', () => {
  describe('error message mapping', () => {
    it('maps Canceled to user-friendly message', () => {
      const err = makeConnectError('operation canceled', Code.Canceled)
      const result = handleConnectError(err)
      expect(result.message).toBe('The operation was canceled.')
    })

    it('maps Unknown to user-friendly message', () => {
      const err = makeConnectError('unknown error', Code.Unknown)
      const result = handleConnectError(err)
      expect(result.message).toBe('An unexpected error occurred. Please try again.')
    })

    it('maps InvalidArgument to user-friendly message', () => {
      const err = makeConnectError('bad field', Code.InvalidArgument)
      const result = handleConnectError(err)
      expect(result.message).toBe('The request contains invalid data.')
    })

    it('maps DeadlineExceeded to user-friendly message', () => {
      const err = makeConnectError('timed out', Code.DeadlineExceeded)
      const result = handleConnectError(err)
      expect(result.message).toBe('The request timed out. Please try again.')
    })

    it('maps NotFound to user-friendly message', () => {
      const err = makeConnectError('not found', Code.NotFound)
      const result = handleConnectError(err)
      expect(result.message).toBe('The requested resource was not found.')
    })

    it('maps AlreadyExists to user-friendly message', () => {
      const err = makeConnectError('already exists', Code.AlreadyExists)
      const result = handleConnectError(err)
      expect(result.message).toBe('This resource already exists.')
    })

    it('maps PermissionDenied to user-friendly message', () => {
      const err = makeConnectError('forbidden', Code.PermissionDenied)
      const result = handleConnectError(err)
      expect(result.message).toBe('You do not have permission to perform this action.')
    })

    it('maps ResourceExhausted to user-friendly message', () => {
      const err = makeConnectError('rate limited', Code.ResourceExhausted)
      const result = handleConnectError(err)
      expect(result.message).toBe('Rate limit exceeded. Please slow down and try again.')
    })

    it('maps FailedPrecondition to user-friendly message', () => {
      const err = makeConnectError('precondition failed', Code.FailedPrecondition)
      const result = handleConnectError(err)
      expect(result.message).toBe('The operation cannot be performed in the current state.')
    })

    it('maps Aborted to user-friendly message', () => {
      const err = makeConnectError('aborted', Code.Aborted)
      const result = handleConnectError(err)
      expect(result.message).toBe('The operation was aborted. Please try again.')
    })

    it('maps OutOfRange to user-friendly message', () => {
      const err = makeConnectError('out of range', Code.OutOfRange)
      const result = handleConnectError(err)
      expect(result.message).toBe('The provided value is out of the allowed range.')
    })

    it('maps Unimplemented to user-friendly message', () => {
      const err = makeConnectError('not implemented', Code.Unimplemented)
      const result = handleConnectError(err)
      expect(result.message).toBe('This feature is not yet available.')
    })

    it('maps Internal to user-friendly message', () => {
      const err = makeConnectError('internal error', Code.Internal)
      const result = handleConnectError(err)
      expect(result.message).toBe('An internal server error occurred. Please contact support if the problem persists.')
    })

    it('maps Unavailable to user-friendly message', () => {
      const err = makeConnectError('service down', Code.Unavailable)
      const result = handleConnectError(err)
      expect(result.message).toBe('The service is temporarily unavailable. Please try again shortly.')
    })

    it('maps DataLoss to user-friendly message', () => {
      const err = makeConnectError('data lost', Code.DataLoss)
      const result = handleConnectError(err)
      expect(result.message).toBe('A data integrity error occurred. Please contact support.')
    })

    it('maps Unauthenticated to user-friendly message', () => {
      const err = makeConnectError('not authenticated', Code.Unauthenticated)
      const result = handleConnectError(err)
      expect(result.message).toBe('Your session has expired. Please sign in again.')
    })

    it('falls back to raw message for unrecognized codes', () => {
      const err = makeConnectError('some unknown message', Code.Unknown)
      // Override to simulate an unexpected code scenario
      ;(err as unknown as { code: number }).code = 999
      const result = handleConnectError(err)
      expect(result.message).toBe('An unexpected error occurred. Please try again.')
    })
  })

  describe('result structure', () => {
    it('includes the original error', () => {
      const err = makeConnectError('test', Code.Internal)
      const result = handleConnectError(err)
      expect(result.originalError).toBe(err)
    })

    it('includes the code', () => {
      const err = makeConnectError('test', Code.NotFound)
      const result = handleConnectError(err)
      expect(result.code).toBe(Code.NotFound)
    })

    it('includes empty fieldErrors when no details', () => {
      const err = makeConnectError('test', Code.InvalidArgument)
      const result = handleConnectError(err)
      expect(result.fieldErrors).toEqual({})
    })

    it('sets shouldRetry false by default', () => {
      const err = makeConnectError('test', Code.Internal)
      const result = handleConnectError(err)
      expect(result.shouldRetry).toBe(false)
    })

    it('sets redirectTo undefined by default', () => {
      const err = makeConnectError('test', Code.Internal)
      const result = handleConnectError(err)
      expect(result.redirectTo).toBeUndefined()
    })
  })

  describe('Unauthenticated handling', () => {
    it('sets redirectTo /login for Unauthenticated errors', () => {
      const err = makeConnectError('session expired', Code.Unauthenticated)
      const result = handleConnectError(err)
      expect(result.redirectTo).toBe('/login')
    })

    it('does not set redirectTo for other error codes', () => {
      const err = makeConnectError('forbidden', Code.PermissionDenied)
      const result = handleConnectError(err)
      expect(result.redirectTo).toBeUndefined()
    })
  })

  describe('InvalidArgument field error extraction', () => {
    it('extracts field violations from google.rpc.BadRequest details', () => {
      const err = makeConnectError('validation failed', Code.InvalidArgument, [
        {
          type: 'google.rpc.BadRequest',
          debug: {
            fieldViolations: [
              { field: 'amount', description: 'must be positive' },
              { field: 'currency', description: 'is not supported' },
            ],
          },
        },
      ])
      const result = handleConnectError(err)
      expect(result.fieldErrors).toEqual({
        amount: 'must be positive',
        currency: 'is not supported',
      })
    })

    it('handles multiple violations for the same field by using last one', () => {
      const err = makeConnectError('validation failed', Code.InvalidArgument, [
        {
          type: 'google.rpc.BadRequest',
          debug: {
            fieldViolations: [
              { field: 'amount', description: 'must be positive' },
              { field: 'amount', description: 'must be less than 1000000' },
            ],
          },
        },
      ])
      const result = handleConnectError(err)
      expect(result.fieldErrors['amount']).toBe('must be less than 1000000')
    })

    it('returns empty fieldErrors when BadRequest has no violations', () => {
      const err = makeConnectError('validation failed', Code.InvalidArgument, [
        {
          type: 'google.rpc.BadRequest',
          debug: { fieldViolations: [] },
        },
      ])
      const result = handleConnectError(err)
      expect(result.fieldErrors).toEqual({})
    })

    it('returns empty fieldErrors for non-BadRequest detail types', () => {
      const err = makeConnectError('validation failed', Code.InvalidArgument, [
        {
          type: 'google.rpc.ErrorInfo',
          debug: { reason: 'QUOTA_EXCEEDED' },
        },
      ])
      const result = handleConnectError(err)
      expect(result.fieldErrors).toEqual({})
    })

    it('returns empty fieldErrors when details is empty', () => {
      const err = makeConnectError('validation failed', Code.InvalidArgument)
      const result = handleConnectError(err)
      expect(result.fieldErrors).toEqual({})
    })

    it('ignores malformed BadRequest details gracefully', () => {
      const err = makeConnectError('validation failed', Code.InvalidArgument, [
        {
          type: 'google.rpc.BadRequest',
          debug: { notFieldViolations: 'garbage' },
        },
      ])
      const result = handleConnectError(err)
      expect(result.fieldErrors).toEqual({})
    })
  })

  describe('NotFound handling', () => {
    it('does not set redirectTo for NotFound by default', () => {
      const err = makeConnectError('not found', Code.NotFound)
      const result = handleConnectError(err)
      expect(result.redirectTo).toBeUndefined()
    })

    it('sets redirectTo when navigateToParent option is provided', () => {
      const err = makeConnectError('not found', Code.NotFound)
      const result = handleConnectError(err, { navigateToParent: '/accounts' })
      expect(result.redirectTo).toBe('/accounts')
    })
  })

  describe('Unavailable handling', () => {
    it('sets shouldRetry to true for Unavailable errors', () => {
      const err = makeConnectError('service down', Code.Unavailable)
      const result = handleConnectError(err)
      expect(result.shouldRetry).toBe(true)
    })

    it('does not set shouldRetry for other error codes', () => {
      const err = makeConnectError('internal error', Code.Internal)
      const result = handleConnectError(err)
      expect(result.shouldRetry).toBe(false)
    })
  })

  describe('non-ConnectError input', () => {
    it('handles plain Error objects', () => {
      const err = new Error('plain error')
      const result = handleConnectError(err)
      expect(result.message).toBe('An unexpected error occurred. Please try again.')
      expect(result.code).toBe(Code.Unknown)
    })

    it('handles string errors', () => {
      const result = handleConnectError('something went wrong')
      expect(result.message).toBe('An unexpected error occurred. Please try again.')
    })

    it('handles null', () => {
      const result = handleConnectError(null)
      expect(result.message).toBe('An unexpected error occurred. Please try again.')
    })
  })
})

describe('withErrorHandling', () => {
  it('returns onError handler that calls handleConnectError', () => {
    const onError = vi.fn()
    const handler = withErrorHandling({ onError })
    const err = makeConnectError('test error', Code.NotFound)

    handler.onError(err)

    expect(onError).toHaveBeenCalledOnce()
    const result: ConnectErrorResult = onError.mock.calls[0][0]
    expect(result.code).toBe(Code.NotFound)
    expect(result.message).toBe('The requested resource was not found.')
  })

  it('passes options to handleConnectError', () => {
    const onError = vi.fn()
    const handler = withErrorHandling({ onError, navigateToParent: '/items' })
    const err = makeConnectError('not found', Code.NotFound)

    handler.onError(err)

    const result: ConnectErrorResult = onError.mock.calls[0][0]
    expect(result.redirectTo).toBe('/items')
  })

  it('calls onRedirect when redirectTo is set and onRedirect provided', () => {
    const onError = vi.fn()
    const onRedirect = vi.fn()
    const handler = withErrorHandling({ onError, onRedirect })
    const err = makeConnectError('session expired', Code.Unauthenticated)

    handler.onError(err)

    expect(onRedirect).toHaveBeenCalledWith('/login')
  })

  it('does not call onRedirect when redirectTo is not set', () => {
    const onError = vi.fn()
    const onRedirect = vi.fn()
    const handler = withErrorHandling({ onError, onRedirect })
    const err = makeConnectError('internal error', Code.Internal)

    handler.onError(err)

    expect(onRedirect).not.toHaveBeenCalled()
  })
})

describe('useErrorHandler', () => {
  it('is exported and is a function', () => {
    expect(typeof useErrorHandler).toBe('function')
  })
})

describe('toast integration', () => {
  beforeEach(() => {
    vi.mocked(toast.error).mockClear()
  })

  it('does not show toast by default', () => {
    const err = makeConnectError('test', Code.Internal)
    handleConnectError(err)
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('shows toast when showToast option is true', () => {
    const err = makeConnectError('test', Code.Internal)
    handleConnectError(err, { showToast: true })
    expect(toast.error).toHaveBeenCalledWith(
      'An internal server error occurred. Please contact support if the problem persists.',
    )
  })

  it('shows toast with correct message for different error codes', () => {
    const err = makeConnectError('not found', Code.NotFound)
    handleConnectError(err, { showToast: true })
    expect(toast.error).toHaveBeenCalledWith('The requested resource was not found.')
  })
})

describe('withToastErrorHandling', () => {
  beforeEach(() => {
    vi.mocked(toast.error).mockClear()
  })

  it('shows toast and calls onError', () => {
    const onError = vi.fn()
    const handler = withToastErrorHandling({ onError })
    const err = makeConnectError('test', Code.Internal)

    handler.onError(err)

    expect(toast.error).toHaveBeenCalledWith(
      'An internal server error occurred. Please contact support if the problem persists.',
    )
    expect(onError).toHaveBeenCalledOnce()
  })

  it('works without onError callback', () => {
    const handler = withToastErrorHandling()
    const err = makeConnectError('test', Code.NotFound)

    handler.onError(err)

    expect(toast.error).toHaveBeenCalledWith('The requested resource was not found.')
  })

  it('calls onRedirect for Unauthenticated errors', () => {
    const onRedirect = vi.fn()
    const handler = withToastErrorHandling({ onRedirect })
    const err = makeConnectError('expired', Code.Unauthenticated)

    handler.onError(err)

    expect(onRedirect).toHaveBeenCalledWith('/login')
    expect(toast.error).toHaveBeenCalled()
  })
})
