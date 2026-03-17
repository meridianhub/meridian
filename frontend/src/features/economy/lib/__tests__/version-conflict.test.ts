import { describe, it, expect } from 'vitest'
import { ConnectError, Code } from '@connectrpc/connect'
import { isVersionConflict } from '../version-conflict'

describe('isVersionConflict', () => {
  it('returns true for ConnectError with Aborted code', () => {
    const err = new ConnectError('sequence mismatch', Code.Aborted)
    expect(isVersionConflict(err)).toBe(true)
  })

  it('returns false for ConnectError with other codes', () => {
    expect(isVersionConflict(new ConnectError('not found', Code.NotFound))).toBe(false)
    expect(isVersionConflict(new ConnectError('internal', Code.Internal))).toBe(false)
    expect(isVersionConflict(new ConnectError('permission', Code.PermissionDenied))).toBe(false)
  })

  it('returns false for plain Error', () => {
    expect(isVersionConflict(new Error('something'))).toBe(false)
  })

  it('returns false for non-error values', () => {
    expect(isVersionConflict(null)).toBe(false)
    expect(isVersionConflict(undefined)).toBe(false)
    expect(isVersionConflict('string')).toBe(false)
  })
})
