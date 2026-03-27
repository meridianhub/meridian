import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createServiceClients } from '@/api/clients'
import { IdentityService } from '@/api/gen/meridian/identity/v1/identity_pb'
import type { Transport } from '@connectrpc/connect'

vi.mock('@connectrpc/connect', () => ({
  createClient: vi.fn((service, transport) => ({
    __service: service.typeName,
    __transport: transport,
  })),
}))

import { createClient } from '@connectrpc/connect'

function makeTransport(): Transport {
  return {} as Transport
}

describe('createServiceClients', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('returns an object with all expected service clients', () => {
    const transport = makeTransport()
    const clients = createServiceClients(transport)

    expect(Object.keys(clients)).toHaveLength(21)
  })

  it('creates clients for all expected services', () => {
    const transport = makeTransport()
    const clients = createServiceClients(transport)

    expect(clients).toHaveProperty('currentAccount')
    expect(clients).toHaveProperty('paymentOrder')
    expect(clients).toHaveProperty('financialAccounting')
    expect(clients).toHaveProperty('positionKeeping')
    expect(clients).toHaveProperty('accountReconciliation')
    expect(clients).toHaveProperty('party')
    expect(clients).toHaveProperty('tenant')
    expect(clients).toHaveProperty('sagaRegistry')
    expect(clients).toHaveProperty('sagaAdmin')
    expect(clients).toHaveProperty('referenceData')
    expect(clients).toHaveProperty('accountTypeRegistry')
    expect(clients).toHaveProperty('node')
    expect(clients).toHaveProperty('internalAccount')
    expect(clients).toHaveProperty('marketInformation')
    expect(clients).toHaveProperty('mapping')
    expect(clients).toHaveProperty('forecasting')
    expect(clients).toHaveProperty('manifestHistory')
    expect(clients).toHaveProperty('manifestApplier')
    expect(clients).toHaveProperty('audit')
    expect(clients).toHaveProperty('identity')
    expect(clients).toHaveProperty('billing')
  })

  it('wires identity client to IdentityService descriptor', () => {
    const transport = makeTransport()
    const clients = createServiceClients(transport)

    expect((clients.identity as { __service: string }).__service).toBe(IdentityService.typeName)
    expect(createClient).toHaveBeenCalledWith(IdentityService, transport)
  })

  it('calls createClient for each service with the provided transport', () => {
    const transport = makeTransport()
    createServiceClients(transport)

    expect(createClient).toHaveBeenCalledTimes(21)
    vi.mocked(createClient).mock.calls.forEach(([, t]) => {
      expect(t).toBe(transport)
    })
  })

  it('returns different client instances for different transports', () => {
    const transport1 = makeTransport()
    const transport2 = makeTransport()

    const clients1 = createServiceClients(transport1)
    const clients2 = createServiceClients(transport2)

    expect(clients1.currentAccount).not.toBe(clients2.currentAccount)
  })
})
