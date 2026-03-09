import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createTenantTransport } from '@/api/transport'

vi.mock('@connectrpc/connect-web', () => ({
  createConnectTransport: vi.fn((opts) => ({ __type: 'transport', opts })),
}))

vi.mock('@/api/config', () => ({
  apiConfig: {
    baseUrl: 'http://localhost:8080',
    useBinaryFormat: false,
  },
  buildTenantBaseUrl: vi.fn((slug: string) => `https://${slug}.api.meridian.io`),
  isOnTenantSubdomain: vi.fn(() => false),
}))

import { createConnectTransport } from '@connectrpc/connect-web'
import { buildTenantBaseUrl, apiConfig } from '@/api/config'

describe('createTenantTransport', () => {
  const getTenantSlug = vi.fn(() => null)

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('uses apiConfig.baseUrl when tenantSlug is null', () => {
    const getToken = vi.fn(() => 'token')
    createTenantTransport(null, getToken, getTenantSlug)

    expect(createConnectTransport).toHaveBeenCalledWith(
      expect.objectContaining({ baseUrl: apiConfig.baseUrl }),
    )
  })

  it('uses apiConfig.baseUrl in dev mode even when tenantSlug is provided', () => {
    // import.meta.env.DEV is true in vitest
    const getToken = vi.fn(() => 'token')
    createTenantTransport('acme', getToken, getTenantSlug)

    expect(buildTenantBaseUrl).not.toHaveBeenCalled()
    expect(createConnectTransport).toHaveBeenCalledWith(
      expect.objectContaining({ baseUrl: apiConfig.baseUrl }),
    )
  })

  it('includes both auth and tenant interceptors in transport config', () => {
    const getToken = vi.fn(() => 'token')
    createTenantTransport(null, getToken, getTenantSlug)

    const callArgs = vi.mocked(createConnectTransport).mock.calls[0][0]
    expect(callArgs.interceptors).toHaveLength(2)
  })

  it('passes useBinaryFormat from apiConfig', () => {
    const getToken = vi.fn(() => null)
    createTenantTransport(null, getToken, getTenantSlug)

    expect(createConnectTransport).toHaveBeenCalledWith(
      expect.objectContaining({ useBinaryFormat: false }),
    )
  })
})
