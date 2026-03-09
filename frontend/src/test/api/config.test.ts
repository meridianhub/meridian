import { describe, it, expect } from 'vitest'
import { buildMcpTenantUrl, buildTenantBaseUrl } from '@/api/config'

describe('buildMcpTenantUrl', () => {
  it('returns base URL unchanged for localhost', () => {
    const url = buildMcpTenantUrl('http://localhost:8091', 'acme')
    expect(url).toBe('http://localhost:8091')
  })

  it('returns base URL unchanged for 127.0.0.1', () => {
    const url = buildMcpTenantUrl('http://127.0.0.1:8091', 'acme')
    expect(url).toBe('http://127.0.0.1:8091')
  })

  it('injects tenant subdomain for production URLs', () => {
    const url = buildMcpTenantUrl('https://demo.meridianhub.cloud', 'acme')
    expect(url).toBe('https://acme.demo.meridianhub.cloud')
  })

  it('returns base URL unchanged when tenantSlug is null', () => {
    const url = buildMcpTenantUrl('https://demo.meridianhub.cloud', null)
    expect(url).toBe('https://demo.meridianhub.cloud')
  })

  it('strips trailing slash from result', () => {
    const url = buildMcpTenantUrl('https://demo.meridianhub.cloud/', 'acme')
    expect(url).not.toMatch(/\/$/)
  })

  it('preserves port in production URLs', () => {
    const url = buildMcpTenantUrl('https://demo.meridianhub.cloud:8090', 'acme')
    expect(url).toBe('https://acme.demo.meridianhub.cloud:8090')
  })
})

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
