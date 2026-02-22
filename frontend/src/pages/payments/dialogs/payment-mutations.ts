import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { tenantKeys } from '@/lib/query-keys'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { amountToBigInt } from './payment-form-utils'

function generateIdempotencyKey(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`
}

export interface InitiatePaymentRequest {
  debtorAccountId: string
  creditorReference: string
  amount: string
  currency: string
}

export interface CancelPaymentRequest {
  paymentOrderId: string
  cancellationReason: string
  cancelledBy?: string
}

export interface ReversePaymentRequest {
  paymentOrderId: string
  reversalReason: string
  reversedBy?: string
}

export function useInitiatePayment() {
  const { paymentOrder } = useApiClients()
  const queryClient = useQueryClient()
  const tenantSlug = useTenantSlug()

  return useMutation({
    mutationFn: async (request: InitiatePaymentRequest) => {
      const response = await paymentOrder.initiatePaymentOrder({
        debtorAccountId: request.debtorAccountId,
        creditorReference: request.creditorReference,
        amount: {
          units: amountToBigInt(request.amount).toString(),
          currency: request.currency,
        },
        idempotencyKey: {
          key: generateIdempotencyKey(),
        },
      })
      return response.paymentOrder
    },
    onSuccess: () => {
      if (tenantSlug) {
        void queryClient.invalidateQueries({ queryKey: tenantKeys.payments(tenantSlug) })
      }
    },
  })
}

export function useCancelPayment() {
  const { paymentOrder } = useApiClients()
  const queryClient = useQueryClient()
  const tenantSlug = useTenantSlug()

  return useMutation({
    mutationFn: async (request: CancelPaymentRequest) => {
      const response = await paymentOrder.cancelPaymentOrder({
        paymentOrderId: request.paymentOrderId,
        cancellationReason: request.cancellationReason,
        cancelledBy: request.cancelledBy ?? 'operations-console',
        idempotencyKey: {
          key: generateIdempotencyKey(),
        },
      })
      return response.paymentOrder
    },
    onSuccess: (_, variables) => {
      if (tenantSlug) {
        void queryClient.invalidateQueries({
          queryKey: tenantKeys.payment(tenantSlug, variables.paymentOrderId),
        })
        void queryClient.invalidateQueries({ queryKey: tenantKeys.payments(tenantSlug) })
      }
    },
  })
}

export function useReversePayment() {
  const { paymentOrder } = useApiClients()
  const queryClient = useQueryClient()
  const tenantSlug = useTenantSlug()

  return useMutation({
    mutationFn: async (request: ReversePaymentRequest) => {
      const response = await paymentOrder.reversePaymentOrder({
        paymentOrderId: request.paymentOrderId,
        reversalReason: request.reversalReason,
        reversedBy: request.reversedBy ?? 'operations-console',
        idempotencyKey: {
          key: generateIdempotencyKey(),
        },
      })
      return response.paymentOrder
    },
    onSuccess: (_, variables) => {
      if (tenantSlug) {
        void queryClient.invalidateQueries({
          queryKey: tenantKeys.payment(tenantSlug, variables.paymentOrderId),
        })
        void queryClient.invalidateQueries({ queryKey: tenantKeys.payments(tenantSlug) })
      }
    },
  })
}
