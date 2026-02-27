import * as React from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useClients } from '@/api/context'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/ui/empty-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { useForm } from 'react-hook-form'

interface PaymentMethodsTabProps {
  partyId: string
}

interface PaymentMethod {
  id: string
  provider: number
  providerCustomerId: string
  providerMethodId: string
  methodType: number
  isDefault: boolean
}

interface AddPaymentMethodFormData {
  providerCustomerId: string
  providerMethodId: string
}

export function PaymentMethodsTab({ partyId }: PaymentMethodsTabProps) {
  const clients = useClients()
  const queryClient = useQueryClient()
  const [isDialogOpen, setIsDialogOpen] = React.useState(false)
  const { register, handleSubmit, reset } = useForm<AddPaymentMethodFormData>()

  const { data, isLoading } = useQuery({
    queryKey: ['party', partyId, 'payment-methods'],
    queryFn: async () => {
      return await clients.party.listPaymentMethods({ partyId })
    },
  })

  const addMutation = useMutation({
    mutationFn: async (data: AddPaymentMethodFormData) => {
      return await clients.party.addPaymentMethod({
        partyId,
        provider: 1,
        providerCustomerId: data.providerCustomerId,
        providerMethodId: data.providerMethodId,
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['party', partyId, 'payment-methods'] })
      setIsDialogOpen(false)
      reset()
    },
  })

  const removeMutation = useMutation({
    mutationFn: async (id: string) => {
      return await clients.party.removePaymentMethod({
        partyId,
        paymentMethodId: id,
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['party', partyId, 'payment-methods'] })
    },
  })

  const setDefaultMutation = useMutation({
    mutationFn: async (id: string) => {
      return await clients.party.setDefaultPaymentMethod({
        partyId,
        paymentMethodId: id,
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['party', partyId, 'payment-methods'] })
    },
  })

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-10 w-32" />
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-20 w-full" />
      </div>
    )
  }

  const paymentMethods = data?.paymentMethods ?? []

  return (
    <div className="space-y-6">
      <div>
        <Dialog open={isDialogOpen} onOpenChange={setIsDialogOpen}>
          <DialogTrigger asChild>
            <Button>Add Payment Method</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add Payment Method</DialogTitle>
            </DialogHeader>
            <form onSubmit={handleSubmit((data) => void addMutation.mutateAsync(data))} className="space-y-4">
              <div>
                <label className="text-sm font-medium">Provider Customer ID</label>
                <Input
                  {...register('providerCustomerId', { required: true })}
                  placeholder="e.g. cus_xxx"
                  className="mt-1"
                />
              </div>

              <div>
                <label className="text-sm font-medium">Provider Method ID</label>
                <Input
                  {...register('providerMethodId', { required: true })}
                  placeholder="e.g. pm_xxx"
                  className="mt-1"
                />
              </div>

              <div className="flex gap-2">
                <Button type="submit" disabled={addMutation.isPending}>
                  Add
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => setIsDialogOpen(false)}
                >
                  Cancel
                </Button>
              </div>
            </form>
          </DialogContent>
        </Dialog>
      </div>

      {paymentMethods.length === 0 ? (
        <EmptyState
          title="No payment methods"
          description="Add a payment method to get started."
        />
      ) : (
        <div className="space-y-4">
          {paymentMethods.map((method) => (
            <Card key={method.id} className="p-4">
              <div className="flex items-start justify-between">
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <span className="font-medium font-mono text-sm">{method.providerMethodId}</span>
                    {method.isDefault && (
                      <span className="inline-block rounded bg-blue-100 px-2 py-1 text-xs font-medium text-blue-700">
                        Default
                      </span>
                    )}
                  </div>
                  <p className="text-sm text-muted-foreground">
                    Customer: {method.providerCustomerId}
                  </p>
                </div>

                <div className="flex gap-2">
                  {!method.isDefault && (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => void setDefaultMutation.mutateAsync(method.id)}
                      disabled={setDefaultMutation.isPending}
                    >
                      Set Default
                    </Button>
                  )}
                  <Button
                    size="sm"
                    variant="destructive"
                    onClick={() => void removeMutation.mutateAsync(method.id)}
                    disabled={removeMutation.isPending}
                  >
                    Remove
                  </Button>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
