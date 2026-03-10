import { describe, it, expect, vi } from 'vitest'
import { getTenantSlugFromSubdomain } from '../tenant-utils'

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
