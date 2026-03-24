import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useOAuthFlow } from './use-oauth-flow'

describe('useOAuthFlow', () => {
  const originalLocation = window.location

  beforeEach(() => {
    // Replace window.location with a writable mock
    Object.defineProperty(window, 'location', {
      value: {
        pathname: '/dashboard',
        search: '?tab=overview',
        hash: '#section',
        href: '',
      },
      writable: true,
      configurable: true,
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'location', {
      value: originalLocation,
      writable: true,
      configurable: true,
    })
  })

  it('returns a startFlow function', () => {
    const { result } = renderHook(() => useOAuthFlow())
    expect(typeof result.current.startFlow).toBe('function')
  })

  it('redirects to BFF SSO endpoint with connector ID and return URL', () => {
    const { result } = renderHook(() => useOAuthFlow())

    act(() => {
      result.current.startFlow('google')
    })

    expect(window.location.href).toBe(
      '/api/auth/sso/google?return_url=%2Fdashboard%3Ftab%3Doverview%23section',
    )
  })

  it('encodes connector ID in the URL path', () => {
    const { result } = renderHook(() => useOAuthFlow())

    act(() => {
      result.current.startFlow('my connector/special')
    })

    expect(window.location.href).toContain('/api/auth/sso/my%20connector%2Fspecial')
  })

  it('handles empty search and hash', () => {
    Object.defineProperty(window, 'location', {
      value: { pathname: '/login', search: '', hash: '', href: '' },
      writable: true,
      configurable: true,
    })

    const { result } = renderHook(() => useOAuthFlow())

    act(() => {
      result.current.startFlow('github')
    })

    expect(window.location.href).toBe('/api/auth/sso/github?return_url=%2Flogin')
  })

  it('returns stable function reference across renders', () => {
    const { result, rerender } = renderHook(() => useOAuthFlow())
    const first = result.current.startFlow
    rerender()
    expect(result.current.startFlow).toBe(first)
  })
})
