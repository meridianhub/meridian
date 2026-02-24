import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'

export interface CreateMappingRequest {
  name: string
  targetService: string
  targetRpc: string
  version: number
  externalSchema: string
}

export function useCreateMapping() {
  const clients = useApiClients()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (request: CreateMappingRequest) => {
      const response = await clients.mapping.createMapping({
        name: request.name,
        targetService: request.targetService,
        targetRpc: request.targetRpc,
        version: request.version,
        externalSchema: request.externalSchema,
      })
      return response.mapping
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['mappings'] })
    },
  })
}
