import { describe, it, expect, vi } from 'vitest'
import { getTenantSlugFromSubdomain, isBaseDomain } from '../tenant-utils'

// VITE_BASE_DOMAIN defaults to 'meridianhub.cloud' when not set
describe('getTenantSlugFromSubdomain', () => {
  it('extracts slug from tenant subdomain', () => {
    expect(getTenantSlugFromSubdomain('acme.meridianhub.cloud')).toBe('acme')
  })

  it('returns null for bare base domain', () => {
    expect(getTenantSlugFromSubdomain('meridianhub.cloud')).toBeNull()
  })

  it('returns null for localhost', () => {
    expect(getTenantSlugFromSubdomain('localhost')).toBeNull()
  })

  it('returns null for 127.0.0.1', () => {
    expect(getTenantSlugFromSubdomain('127.0.0.1')).toBeNull()
  })

  it('returns null for unrelated domain', () => {
    expect(getTenantSlugFromSubdomain('example.com')).toBeNull()
  })

  it('returns null for nested subdomains (multiple segments before base)', () => {
    expect(getTenantSlugFromSubdomain('sub.acme.meridianhub.cloud')).toBeNull()
  })

  it('handles demo environment subdomain', () => {
    // With VITE_BASE_DOMAIN=demo.meridianhub.cloud
    vi.stubEnv('VITE_BASE_DOMAIN', 'demo.meridianhub.cloud')
    expect(getTenantSlugFromSubdomain('acme.demo.meridianhub.cloud')).toBe('acme')
    vi.unstubAllEnvs()
  })

  it('returns null for bare demo domain', () => {
    vi.stubEnv('VITE_BASE_DOMAIN', 'demo.meridianhub.cloud')
    expect(getTenantSlugFromSubdomain('demo.meridianhub.cloud')).toBeNull()
    vi.unstubAllEnvs()
  })
})

describe('isBaseDomain', () => {
  it('returns true for exact base domain match', () => {
    expect(isBaseDomain('meridianhub.cloud')).toBe(true)
  })

  it('returns true case-insensitively', () => {
    expect(isBaseDomain('MeridianHub.Cloud')).toBe(true)
  })

  it('returns false for tenant subdomain', () => {
    expect(isBaseDomain('acme.meridianhub.cloud')).toBe(false)
  })

  it('returns false for localhost', () => {
    expect(isBaseDomain('localhost')).toBe(false)
  })

  it('returns false for unrelated domain', () => {
    expect(isBaseDomain('example.com')).toBe(false)
  })

  it('returns true for custom base domain', () => {
    vi.stubEnv('VITE_BASE_DOMAIN', 'demo.meridianhub.cloud')
    expect(isBaseDomain('demo.meridianhub.cloud')).toBe(true)
    expect(isBaseDomain('acme.demo.meridianhub.cloud')).toBe(false)
    vi.unstubAllEnvs()
  })
})
