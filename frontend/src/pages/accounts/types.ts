/**
 * Account-related types for the Operations Console.
 * These mirror the protobuf message shapes from
 * meridian.current_account.v1.CurrentAccountService.
 */

export interface CurrentAccount {
  accountId: string
  externalReference: string
  status: AccountStatus
  baseCurrency: string
  availableBalance: string
  reservedBalance?: string
  name?: string
  partyId?: string
  createdAt?: { seconds: number | bigint; nanos?: number }
  updatedAt?: { seconds: number | bigint; nanos?: number }
}

export type AccountStatus = 'ACTIVE' | 'FROZEN' | 'CLOSED' | 'SUSPENDED'

export interface ListCurrentAccountsResponse {
  accounts: CurrentAccount[]
  nextPageToken: string
}

export interface RetrieveCurrentAccountResponse {
  account: CurrentAccount
}
