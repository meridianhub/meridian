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
}))

import { createConnectTransport } from '@connectrpc/connect-web'
import { buildTenantBaseUrl, apiConfig } from '@/api/config'

describe('createTenantTransport', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('uses apiConfig.baseUrl when tenantSlug is null', () => {
    const getToken = vi.fn(() => 'token')
    createTenantTransport(null, getToken)

    expect(createConnectTransport).toHaveBeenCalledWith(
      expect.objectContaining({ baseUrl: apiConfig.baseUrl }),
    )
  })

  it('uses tenant-specific URL when tenantSlug is provided', () => {
    const getToken = vi.fn(() => 'token')
    createTenantTransport('acme', getToken)

    expect(buildTenantBaseUrl).toHaveBeenCalledWith('acme')
    expect(createConnectTransport).toHaveBeenCalledWith(
      expect.objectContaining({ baseUrl: 'https://acme.api.meridian.io' }),
    )
  })

  it('includes auth interceptor in transport config', () => {
    const getToken = vi.fn(() => 'token')
    createTenantTransport(null, getToken)

    const callArgs = vi.mocked(createConnectTransport).mock.calls[0][0]
    expect(callArgs.interceptors).toHaveLength(1)
  })

  it('passes useBinaryFormat from apiConfig', () => {
    const getToken = vi.fn(() => null)
    createTenantTransport(null, getToken)

    expect(createConnectTransport).toHaveBeenCalledWith(
      expect.objectContaining({ useBinaryFormat: false }),
    )
  })
})
