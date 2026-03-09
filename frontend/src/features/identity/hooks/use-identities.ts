import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { platformKeys } from '@/lib/query-keys'
import type { Role } from '@/api/gen/meridian/identity/v1/identity_pb'
import { IdentityStatus } from '@/api/gen/meridian/identity/v1/identity_pb'

export function useIdentities() {
  const { identity } = useApiClients()

  return {
    queryKey: platformKeys.identities(),
    queryFn: async (_params: {
      pageToken?: string
      pageSize: number
      filters?: Record<string, string>
    }) => {
      // Backend does not yet support pagination or status filtering.
      // Pass zeros to avoid Unimplemented error.
      const response = await identity.listIdentities({
        pageSize: 0,
        pageToken: '',
        statusFilter: IdentityStatus.UNSPECIFIED,
      })

      // Client-side status filtering until backend implements it
      let items = response.identities ?? []
      const statusFilterValue = _params.filters?.statusFilter
      if (statusFilterValue) {
        const filterNum = Number(statusFilterValue)
        items = items.filter((i) => i.status === filterNum)
      }

      return {
        items,
        nextPageToken: undefined,
      }
    },
  }
}

export function useIdentity(identityId: string) {
  const { identity } = useApiClients()

  return useQuery({
    queryKey: platformKeys.identity(identityId),
    queryFn: async () => {
      const response = await identity.retrieveIdentity({ id: identityId })
      return response.identity
    },
    enabled: !!identityId,
  })
}

export function useIdentityRoles(identityId: string) {
  const { identity } = useApiClients()

  return useQuery({
    queryKey: platformKeys.identityRoles(identityId),
    queryFn: async () => {
      const response = await identity.listRoleAssignments({
        identityId,
        includeRevoked: false,
      })
      return response.roleAssignments ?? []
    },
    enabled: !!identityId,
  })
}

export function useInviteUser() {
  const { identity } = useApiClients()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (request: { email: string; role: Role }) => {
      return identity.inviteUser({
        email: request.email,
        role: request.role,
      })
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: platformKeys.identities() })
    },
  })
}

export function useSuspendIdentity() {
  const { identity } = useApiClients()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (request: { id: string; reason: string }) => {
      const response = await identity.suspendIdentity({
        id: request.id,
        reason: request.reason,
      })
      return response.identity
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({ queryKey: platformKeys.identity(variables.id) })
      void queryClient.invalidateQueries({ queryKey: platformKeys.identities() })
    },
  })
}

export function useReactivateIdentity() {
  const { identity } = useApiClients()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (request: { id: string; reason: string }) => {
      const response = await identity.reactivateIdentity({
        id: request.id,
        reason: request.reason,
      })
      return response.identity
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({ queryKey: platformKeys.identity(variables.id) })
      void queryClient.invalidateQueries({ queryKey: platformKeys.identities() })
    },
  })
}

export function useGrantRole() {
  const { identity } = useApiClients()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (request: { identityId: string; role: Role }) => {
      const response = await identity.grantRole({
        identityId: request.identityId,
        role: request.role,
      })
      return response.roleAssignment
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: platformKeys.identityRoles(variables.identityId),
      })
    },
  })
}

export function useRevokeRole() {
  const { identity } = useApiClients()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (request: { identityId: string; roleAssignmentId: string }) => {
      const response = await identity.revokeRole({
        identityId: request.identityId,
        roleAssignmentId: request.roleAssignmentId,
      })
      return response.roleAssignment
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: platformKeys.identityRoles(variables.identityId),
      })
    },
  })
}
