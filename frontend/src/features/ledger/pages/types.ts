/**
 * Local TypeScript types mirroring the protobuf-generated structures for
 * financial accounting. These types align with the Connect-ES generated
 * types but are defined locally to avoid dependency on generated code
 * in tests and components.
 */

export interface Timestamp {
  seconds: bigint | number
  nanos?: number
}

export interface Money {
  currencyCode: string
  units: bigint | number
  nanos?: number
}

export interface LedgerPosting {
  id: string
  financialBookingLogId: string
  postingDirection: string
  postingAmount: Money
  accountId: string
  accountServiceDomain?: number
  valueDate: Timestamp | null | undefined
  postingResult: string
  createdAt: Timestamp | null | undefined
  status: string
}

export interface FinancialBookingLog {
  id: string
  financialAccountType: string
  productServiceReference: string
  businessUnitReference: string
  chartOfAccountsRules: string
  instrumentCode: string
  status: string
  createdAt: Timestamp | null | undefined
  updatedAt: Timestamp | null | undefined
  postings: LedgerPosting[]
}
