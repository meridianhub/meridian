import { describe, it, expect } from 'vitest'
import { buildTenantBaseUrl } from '@/api/config'

describe('buildTenantBaseUrl', () => {
  it('builds a tenant-scoped URL for a given slug', () => {
    const url = buildTenantBaseUrl('acme')
    expect(url).toContain('acme')
  })

  it('returns a valid URL string', () => {
    const url = buildTenantBaseUrl('my-tenant')
    expect(() => new URL(url)).not.toThrow()
  })

  it('does not end with a trailing slash', () => {
    const url = buildTenantBaseUrl('acme')
    expect(url).not.toMatch(/\/$/)
  })

  it('includes the tenant slug in the hostname', () => {
    const url = buildTenantBaseUrl('my-org')
    const parsed = new URL(url)
    expect(parsed.hostname).toContain('my-org')
  })
})

describe('apiConfig', () => {
  it('exports a baseUrl string', async () => {
    const { apiConfig } = await import('@/api/config')
    expect(typeof apiConfig.baseUrl).toBe('string')
    expect(apiConfig.baseUrl.length).toBeGreaterThan(0)
  })

  it('exports useBinaryFormat boolean', async () => {
    const { apiConfig } = await import('@/api/config')
    expect(typeof apiConfig.useBinaryFormat).toBe('boolean')
  })
})
