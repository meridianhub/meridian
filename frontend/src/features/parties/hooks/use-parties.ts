import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import type { Party } from '../pages/index'

function partyStatusLabel(status: unknown): string {
  if (typeof status === 'string') return status
  const map: Record<number, string> = {
    0: 'UNSPECIFIED',
    1: 'PARTY_STATUS_ACTIVE',
    2: 'PARTY_STATUS_RESTRICTED',
    3: 'PARTY_STATUS_SUSPENDED',
    4: 'PARTY_STATUS_TERMINATED',
  }
  return map[status as number] ?? 'UNKNOWN'
}

function partyTypeLabel(partyType: unknown): string {
  if (typeof partyType === 'string') return partyType
  const map: Record<number, string> = {
    0: 'UNSPECIFIED',
    1: 'PARTY_TYPE_PERSON',
    2: 'PARTY_TYPE_ORGANIZATION',
  }
  return map[partyType as number] ?? 'UNKNOWN'
}

/**
 * Fetches a paginated list of parties for use with DataTable.
 */
export function usePartiesTable() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.parties(tenantSlug ?? '')

  async function queryFn(params: DataTableQueryParams): Promise<DataTableResult<Party>> {
    const response = await clients.party.listParties({
      pageToken: params.pageToken,
      pageSize: params.pageSize,
      searchQuery: params.filters?.searchQuery,
      partyType: params.filters?.partyType,
      status: params.filters?.status,
    })

    const parties: Party[] = response.parties.map((p: Party) => ({
      partyId: p.partyId,
      legalName: p.legalName,
      partyType: partyTypeLabel(p.partyType),
      status: partyStatusLabel(p.status),
      externalReference: p.externalReference,
      createdAt: p.createdAt,
    }))

    return {
      items: parties,
      nextPageToken: response.nextPageToken,
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a single party by ID.
 */
export function usePartyDetail(partyId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.party(tenantSlug ?? '', partyId ?? ''),
    queryFn: async () => {
      const response = await clients.party.retrieveParty({ partyId: partyId ?? '' })
      return response.party
    },
    enabled: Boolean(tenantSlug && partyId),
  })
}

/**
 * Fetches associations for a party.
 */
export function usePartyAssociations(partyId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.partyAssociations(tenantSlug ?? '', partyId ?? ''),
    queryFn: () => clients.party.retrieveAssociations({ partyId: partyId ?? '' }),
    enabled: Boolean(tenantSlug && partyId),
  })
}
