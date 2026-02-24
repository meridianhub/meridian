import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'

export interface CreateMappingRequest {
  name: string
  sourceFormat: string
  targetService: string
  description: string
  mappingRules: string
}

export function useCreateMapping() {
  const clients = useApiClients()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (request: CreateMappingRequest) => {
      const response = await clients.mapping.createMapping({
        name: request.name,
        sourceFormat: request.sourceFormat as never,
        targetService: request.targetService,
        description: request.description,
        mappingRules: request.mappingRules,
      })
      return response.mapping
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['mappings'] })
    },
  })
}
