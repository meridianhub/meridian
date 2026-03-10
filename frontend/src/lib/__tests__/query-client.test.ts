import { describe, it, expect } from 'vitest'
import { ConnectError, Code } from '@connectrpc/connect'
import { shouldRetry, retryDelay } from '../query-client'

describe('shouldRetry', () => {
  const nonRetryableCodes = [
    Code.InvalidArgument,
    Code.NotFound,
    Code.AlreadyExists,
    Code.PermissionDenied,
    Code.Unauthenticated,
    Code.FailedPrecondition,
    Code.OutOfRange,
    Code.Unimplemented,
  ]

  it.each(nonRetryableCodes)(
    'does not retry ConnectError with code %s',
    (code) => {
      const error = new ConnectError('client error', code)
      expect(shouldRetry(0, error)).toBe(false)
    },
  )

  it('retries server errors (Internal)', () => {
    const error = new ConnectError('server error', Code.Internal)
    expect(shouldRetry(0, error)).toBe(true)
    expect(shouldRetry(1, error)).toBe(true)
    expect(shouldRetry(2, error)).toBe(true)
  })

  it('retries Unavailable errors', () => {
    const error = new ConnectError('unavailable', Code.Unavailable)
    expect(shouldRetry(0, error)).toBe(true)
  })

  it('stops retrying after 3 failures for server errors', () => {
    const error = new ConnectError('server error', Code.Internal)
    expect(shouldRetry(3, error)).toBe(false)
  })

  it('retries generic network errors', () => {
    const error = new Error('Failed to fetch')
    expect(shouldRetry(0, error)).toBe(true)
    expect(shouldRetry(2, error)).toBe(true)
  })

  it('stops retrying generic errors after 3 failures', () => {
    const error = new Error('Failed to fetch')
    expect(shouldRetry(3, error)).toBe(false)
  })
})

describe('retryDelay', () => {
  it('uses exponential backoff', () => {
    expect(retryDelay(0)).toBe(1000)
    expect(retryDelay(1)).toBe(2000)
    expect(retryDelay(2)).toBe(4000)
  })

  it('caps at 10 seconds', () => {
    expect(retryDelay(5)).toBe(10_000)
    expect(retryDelay(10)).toBe(10_000)
  })
})
