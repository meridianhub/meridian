/**
 * Query key factories for TanStack Query.
 *
 * Tenant-scoped keys include the tenantId to ensure cache isolation between tenants.
 * Platform-scoped keys are for platform-level data not tied to a specific tenant.
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
} as const

export const platformKeys = {
  all: ['platform'] as const,

  tenants: () => [...platformKeys.all, 'tenants'] as const,
  tenant: (tenantId: string) => [...platformKeys.tenants(), tenantId] as const,

  health: () => [...platformKeys.all, 'health'] as const,

  metrics: () => [...platformKeys.all, 'metrics'] as const,
} as const
