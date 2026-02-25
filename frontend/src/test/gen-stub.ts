/**
 * Stub module for generated protobuf files.
 * All imports from `src/api/gen/` are redirected here by the vitest genStubPlugin.
 * This prevents test failures when `buf generate` has not been run.
 *
 * For Connect-ES service descriptors, we return a minimal object that satisfies
 * the `createClient()` call's requirement for a `.methods` property.
 */

/** Minimal service descriptor stub accepted by @connectrpc/connect createClient(). */
const serviceStub = { typeName: 'stub', methods: [] } as const

// Service descriptors (named exports from *_pb.ts files)
export const CurrentAccountService = serviceStub
export const PaymentOrderService = serviceStub
export const FinancialAccountingService = serviceStub
export const PositionKeepingService = serviceStub
export const AccountReconciliationService = serviceStub
export const PartyService = serviceStub
export const TenantService = serviceStub
export const SagaRegistryService = serviceStub
export const SagaAdminService = serviceStub
export const ReferenceDataService = serviceStub
export const AccountTypeRegistryService = serviceStub
export const NodeService = serviceStub
export const InternalAccountService = serviceStub
export const MarketInformationService = serviceStub
export const ForecastingService = serviceStub
export const MappingService = serviceStub
export const ManifestHistoryService = serviceStub
export const ApplyManifestService = serviceStub

// Enum exports (from types_pb.ts and other shared proto files)
// Names match what Connect-ES generates from proto enums (short names without prefix).
export const TransactionStatus = {
  UNSPECIFIED: 0,
  PENDING: 1,
  POSTED: 2,
  FAILED: 3,
  CANCELLED: 4,
  REVERSED: 5,
} as const

export const PostingDirection = {
  UNSPECIFIED: 0,
  DEBIT: 1,
  CREDIT: 2,
} as const

export const Currency = {
  UNSPECIFIED: 0,
  GBP: 1,
  USD: 2,
  EUR: 3,
} as const

// Default export for wildcard imports
export default {}
