/**
 * Query key factories for TanStack Query.
 *
 * Tenant-scoped keys include the tenantId to ensure cache isolation between tenants.
 * Platform-scoped keys are for platform-level data not tied to a specific tenant.
 * Reference keys are for non-tenant-scoped reference/configuration data.
 */

export const tenantKeys = {
  all: (tenantId: string) => ['tenants', tenantId] as const,

  accounts: (tenantId: string) =>
    [...tenantKeys.all(tenantId), 'accounts'] as const,
  account: (tenantId: string, accountId: string) =>
    [...tenantKeys.accounts(tenantId), accountId] as const,

  transactions: (tenantId: string) =>
    [...tenantKeys.all(tenantId), 'transactions'] as const,
  transaction: (tenantId: string, transactionId: string) =>
    [...tenantKeys.transactions(tenantId), transactionId] as const,

  sagas: (tenantId: string) =>
    [...tenantKeys.all(tenantId), 'sagas'] as const,
  saga: (tenantId: string, sagaId: string) =>
    [...tenantKeys.sagas(tenantId), sagaId] as const,

  payments: (tenantId: string) =>
    [...tenantKeys.all(tenantId), 'payments'] as const,
  payment: (tenantId: string, paymentOrderId: string) =>
    [...tenantKeys.payments(tenantId), paymentOrderId] as const,

  parties: (tenantId: string) =>
    [...tenantKeys.all(tenantId), 'parties'] as const,
  party: (tenantId: string, partyId: string) =>
    [...tenantKeys.parties(tenantId), partyId] as const,
  partyAssociations: (tenantId: string, partyId: string) =>
    [...tenantKeys.party(tenantId, partyId), 'associations'] as const,

  partyTypes: (tenantId: string) =>
    [...tenantKeys.all(tenantId), 'party-types'] as const,

  internalAccounts: (tenantId: string) =>
    [...tenantKeys.all(tenantId), 'internal-accounts'] as const,
  internalAccount: (tenantId: string, accountId: string) =>
    [...tenantKeys.internalAccounts(tenantId), accountId] as const,

  accountLiens: (tenantId: string, accountId: string) =>
    [...tenantKeys.all(tenantId), 'liens', accountId] as const,

  marketDataSets: (tenantId: string) =>
    [...tenantKeys.all(tenantId), 'market-data', 'datasets'] as const,
  marketDataSet: (tenantId: string, datasetCode: string) =>
    [...tenantKeys.marketDataSets(tenantId), datasetCode] as const,

  reconciliationRuns: (tenantId: string) =>
    [...tenantKeys.all(tenantId), 'reconciliation-runs'] as const,
  reconciliationRun: (tenantId: string, runId: string) =>
    [...tenantKeys.reconciliationRuns(tenantId), runId] as const,
} as const

export const platformKeys = {
  all: ['platform'] as const,

  tenants: () => [...platformKeys.all, 'tenants'] as const,
  tenant: (tenantId: string) => [...platformKeys.tenants(), tenantId] as const,

  health: () => [...platformKeys.all, 'health'] as const,

  metrics: () => [...platformKeys.all, 'metrics'] as const,
} as const

export const manifestKeys = {
  all: ['manifest'] as const,
  current: () => [...manifestKeys.all, 'current'] as const,
  history: () => [...manifestKeys.all, 'history'] as const,
  version: (version: string) => [...manifestKeys.all, 'version', version] as const,
} as const

export const referenceKeys = {
  all: ['reference'] as const,

  partyTypes: () => [...referenceKeys.all, 'party-types'] as const,

  instruments: () => [...referenceKeys.all, 'instruments'] as const,

  accountTypes: () => [...referenceKeys.all, 'account-types'] as const,

  nodes: () => [...referenceKeys.all, 'nodes'] as const,
  nodeChildren: (parentId: string) =>
    [...referenceKeys.nodes(), 'children', parentId] as const,

  sagas: () => [...referenceKeys.all, 'sagas'] as const,
  saga: (sagaId: string) => [...referenceKeys.sagas(), sagaId] as const,

  mappings: () => [...referenceKeys.all, 'mappings'] as const,
  mapping: (mappingId: string) => [...referenceKeys.mappings(), mappingId] as const,
} as const
