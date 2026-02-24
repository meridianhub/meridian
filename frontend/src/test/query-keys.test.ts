import { describe, it, expect } from 'vitest'
import { tenantKeys, platformKeys, referenceKeys } from '@/lib/query-keys'

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

  // Parties
  it('parties is scoped under tenant', () => {
    expect(tenantKeys.parties(tenantId)).toEqual(['tenants', tenantId, 'parties'])
  })

  it('party includes partyId', () => {
    expect(tenantKeys.party(tenantId, 'party-1')).toEqual([
      'tenants',
      tenantId,
      'parties',
      'party-1',
    ])
  })

  it('partyAssociations is scoped under party', () => {
    expect(tenantKeys.partyAssociations(tenantId, 'party-1')).toEqual([
      'tenants',
      tenantId,
      'parties',
      'party-1',
      'associations',
    ])
  })

  // Internal Accounts
  it('internalAccounts is scoped under tenant', () => {
    expect(tenantKeys.internalAccounts(tenantId)).toEqual([
      'tenants',
      tenantId,
      'internal-accounts',
    ])
  })

  it('internalAccount includes accountId', () => {
    expect(tenantKeys.internalAccount(tenantId, 'ia-1')).toEqual([
      'tenants',
      tenantId,
      'internal-accounts',
      'ia-1',
    ])
  })

  // Liens
  it('accountLiens is scoped under tenant with accountId', () => {
    expect(tenantKeys.accountLiens(tenantId, 'acc-1')).toEqual([
      'tenants',
      tenantId,
      'liens',
      'acc-1',
    ])
  })

  // Market Data
  it('marketDataSets is scoped under tenant', () => {
    expect(tenantKeys.marketDataSets(tenantId)).toEqual([
      'tenants',
      tenantId,
      'market-data',
      'datasets',
    ])
  })

  it('marketDataSet includes datasetCode', () => {
    expect(tenantKeys.marketDataSet(tenantId, 'ds-1')).toEqual([
      'tenants',
      tenantId,
      'market-data',
      'datasets',
      'ds-1',
    ])
  })

  // Reconciliation Runs
  it('reconciliationRuns is scoped under tenant', () => {
    expect(tenantKeys.reconciliationRuns(tenantId)).toEqual([
      'tenants',
      tenantId,
      'reconciliation-runs',
    ])
  })

  it('reconciliationRun includes runId', () => {
    expect(tenantKeys.reconciliationRun(tenantId, 'run-1')).toEqual([
      'tenants',
      tenantId,
      'reconciliation-runs',
      'run-1',
    ])
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

describe('referenceKeys', () => {
  it('all is the base reference key', () => {
    expect(referenceKeys.all).toEqual(['reference'])
  })

  it('partyTypes is scoped under reference', () => {
    expect(referenceKeys.partyTypes()).toEqual(['reference', 'party-types'])
  })

  it('instruments is scoped under reference', () => {
    expect(referenceKeys.instruments()).toEqual(['reference', 'instruments'])
  })

  it('accountTypes is scoped under reference', () => {
    expect(referenceKeys.accountTypes()).toEqual(['reference', 'account-types'])
  })

  it('nodes is scoped under reference', () => {
    expect(referenceKeys.nodes()).toEqual(['reference', 'nodes'])
  })

  it('nodeChildren includes parentId', () => {
    expect(referenceKeys.nodeChildren('parent-1')).toEqual([
      'reference',
      'nodes',
      'children',
      'parent-1',
    ])
  })

  it('sagas is scoped under reference', () => {
    expect(referenceKeys.sagas()).toEqual(['reference', 'sagas'])
  })

  it('saga includes sagaId', () => {
    expect(referenceKeys.saga('saga-1')).toEqual(['reference', 'sagas', 'saga-1'])
  })

  it('mappings is scoped under reference', () => {
    expect(referenceKeys.mappings()).toEqual(['reference', 'mappings'])
  })

  it('mapping includes mappingId', () => {
    expect(referenceKeys.mapping('map-1')).toEqual(['reference', 'mappings', 'map-1'])
  })
})
