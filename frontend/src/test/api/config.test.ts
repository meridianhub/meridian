import { describe, it, expect } from 'vitest'
import { buildTenantBaseUrl } from '@/api/config'

describe('buildTenantBaseUrl', () => {
  it('returns localhost base URL unchanged in local dev', () => {
    // In local dev (localhost), tenant is identified via JWT, not subdomain
    const url = buildTenantBaseUrl('acme')
    expect(url).toBe('http://localhost:8090')
  })

  it('returns a valid URL string', () => {
    const url = buildTenantBaseUrl('my-tenant')
    expect(() => new URL(url)).not.toThrow()
  })

  it('does not end with a trailing slash', () => {
    const url = buildTenantBaseUrl('acme')
    expect(url).not.toMatch(/\/$/)
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
