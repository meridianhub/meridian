import { describe, it, expect } from 'vitest'
import { tenantKeys, platformKeys } from '@/lib/query-keys'

describe('tenantKeys', () => {
  const tenantId = 'tenant-abc'

  it('all includes tenantId', () => {
    expect(tenantKeys.all(tenantId)).toEqual(['tenants', tenantId])
  })

  it('accounts is scoped under tenant', () => {
    expect(tenantKeys.accounts(tenantId)).toEqual(['tenants', tenantId, 'accounts'])
  })

  it('account includes accountId', () => {
    expect(tenantKeys.account(tenantId, 'acc-1')).toEqual([
      'tenants',
      tenantId,
      'accounts',
      'acc-1',
    ])
  })

  it('transactions is scoped under tenant', () => {
    expect(tenantKeys.transactions(tenantId)).toEqual([
      'tenants',
      tenantId,
      'transactions',
    ])
  })

  it('transaction includes transactionId', () => {
    expect(tenantKeys.transaction(tenantId, 'tx-1')).toEqual([
      'tenants',
      tenantId,
      'transactions',
      'tx-1',
    ])
  })

  it('sagas is scoped under tenant', () => {
    expect(tenantKeys.sagas(tenantId)).toEqual(['tenants', tenantId, 'sagas'])
  })

  it('saga includes sagaId', () => {
    expect(tenantKeys.saga(tenantId, 'saga-1')).toEqual([
      'tenants',
      tenantId,
      'sagas',
      'saga-1',
    ])
  })

  it('keys for different tenants do not overlap', () => {
    const keyA = tenantKeys.accounts('tenant-a')
    const keyB = tenantKeys.accounts('tenant-b')
    expect(keyA).not.toEqual(keyB)
  })
})

describe('platformKeys', () => {
  it('all is the base platform key', () => {
    expect(platformKeys.all).toEqual(['platform'])
  })

  it('tenants is scoped under platform', () => {
    expect(platformKeys.tenants()).toEqual(['platform', 'tenants'])
  })

  it('tenant includes tenantId', () => {
    expect(platformKeys.tenant('t-1')).toEqual(['platform', 'tenants', 't-1'])
  })

  it('health is scoped under platform', () => {
    expect(platformKeys.health()).toEqual(['platform', 'health'])
  })

  it('metrics is scoped under platform', () => {
    expect(platformKeys.metrics()).toEqual(['platform', 'metrics'])
  })
})
