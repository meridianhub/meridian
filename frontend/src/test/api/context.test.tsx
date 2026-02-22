import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, renderHook } from '@testing-library/react'
import { ApiClientProvider, useApiClients } from '@/api/context'
import type { ReactNode } from 'react'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn((transport) => ({
    currentAccount: { __transport: transport },
    paymentOrder: { __transport: transport },
    financialAccounting: { __transport: transport },
    positionKeeping: { __transport: transport },
    accountReconciliation: { __transport: transport },
    party: { __transport: transport },
    tenant: { __transport: transport },
    sagaRegistry: { __transport: transport },
    sagaAdmin: { __transport: transport },
    referenceData: { __transport: transport },
    accountTypeRegistry: { __transport: transport },
    node: { __transport: transport },
    internalBankAccount: { __transport: transport },
    marketInformation: { __transport: transport },
  })),
}))

import { createTenantTransport } from '@/api/transport'
import { createServiceClients } from '@/api/clients'

describe('ApiClientProvider and useApiClients', () => {
  const getToken = vi.fn(() => 'test-token')

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('provides service clients to children via hook', () => {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ApiClientProvider tenantSlug="acme" getToken={getToken}>
        {children}
      </ApiClientProvider>
    )

    const { result } = renderHook(() => useApiClients(), { wrapper })

    expect(result.current).toHaveProperty('currentAccount')
    expect(result.current).toHaveProperty('paymentOrder')
  })

  it('throws when used outside ApiClientProvider', () => {
    expect(() => renderHook(() => useApiClients())).toThrow(
      'useApiClients must be used within an ApiClientProvider',
    )
  })

  it('creates transport with tenant slug', () => {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ApiClientProvider tenantSlug="acme" getToken={getToken}>
        {children}
      </ApiClientProvider>
    )

    renderHook(() => useApiClients(), { wrapper })

    expect(createTenantTransport).toHaveBeenCalledWith('acme', getToken)
  })

  it('creates transport with null when no tenant slug', () => {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ApiClientProvider tenantSlug={null} getToken={getToken}>
        {children}
      </ApiClientProvider>
    )

    renderHook(() => useApiClients(), { wrapper })

    expect(createTenantTransport).toHaveBeenCalledWith(null, getToken)
  })

  it('does not recreate clients when tenant slug stays the same', () => {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ApiClientProvider tenantSlug="acme" getToken={getToken}>
        {children}
      </ApiClientProvider>
    )

    const { rerender } = renderHook(() => useApiClients(), { wrapper })
    expect(createServiceClients).toHaveBeenCalledTimes(1)

    rerender()
    expect(createServiceClients).toHaveBeenCalledTimes(1)
  })

  it('recreates clients when tenant slug changes', () => {
    let tenantSlug = 'acme'
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ApiClientProvider tenantSlug={tenantSlug} getToken={getToken}>
        {children}
      </ApiClientProvider>
    )

    const { rerender } = renderHook(() => useApiClients(), { wrapper })
    expect(createServiceClients).toHaveBeenCalledTimes(1)

    tenantSlug = 'newcorp'
    rerender()
    expect(createServiceClients).toHaveBeenCalledTimes(2)
  })

  it('renders children', () => {
    const { getByText } = render(
      <ApiClientProvider tenantSlug="acme" getToken={getToken}>
        <span>test child</span>
      </ApiClientProvider>,
    )

    expect(getByText('test child')).toBeDefined()
  })
})
