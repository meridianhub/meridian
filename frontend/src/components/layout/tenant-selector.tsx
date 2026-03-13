import * as React from 'react'
import { Check, ChevronsUpDown } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { useTenants } from '@/hooks/use-tenants'
import { useTenantContext } from '@/contexts/tenant-context'
import { TenantStatus } from '@/api/gen/meridian/tenant/v1/tenant_pb'

export function TenantSelector() {
  const [open, setOpen] = React.useState(false)
  const { data: tenants, isLoading } = useTenants()
  const { tenantSlug, currentTenant: contextTenant, switchTenant } = useTenantContext()

  const activeTenants = tenants?.filter((t) => t.status !== TenantStatus.DEPROVISIONED)

  // Use loaded tenant data when available; only fall back to context while loading
  const resolvedTenant = tenants
    ? activeTenants?.find((t) => t.slug === tenantSlug)
    : contextTenant

  if (isLoading) {
    return (
      <div
        data-testid="tenant-selector-loading"
        className="flex h-9 w-full sm:w-[200px] items-center justify-between rounded-md border px-3 py-2 text-sm opacity-50"
        aria-busy="true"
        aria-label="Loading tenants"
      >
        <span className="text-muted-foreground">Loading...</span>
        <ChevronsUpDown className="size-4 shrink-0 opacity-50" />
      </div>
    )
  }

  return (
    <div data-testid="tenant-selector">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            role="combobox"
            aria-expanded={open}
            aria-label="Select tenant"
            aria-haspopup="listbox"
            className="w-full sm:w-[200px] justify-between"
          >
            <span className="truncate">
              {resolvedTenant ? resolvedTenant.displayName ?? resolvedTenant.name : 'Select tenant...'}
            </span>
            <ChevronsUpDown className="ml-2 size-4 shrink-0 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-full sm:w-[200px] p-0">
          <Command>
            <CommandInput placeholder="Search tenants..." />
            <CommandList
              role="listbox"
              aria-label="Available tenants"
            >
              <CommandEmpty>No tenants found.</CommandEmpty>
              <CommandGroup>
                {activeTenants?.map((tenant) => (
                  <CommandItem
                    key={tenant.tenantId}
                    value={`${tenant.displayName} ${tenant.slug}`}
                    role="option"
                    aria-selected={tenant.slug === tenantSlug}
                    onSelect={() => {
                      switchTenant({
                        id: tenant.tenantId,
                        slug: tenant.slug,
                        name: tenant.displayName,
                      })
                      setOpen(false)
                    }}
                  >
                    <Check
                      className={cn(
                        'mr-2 size-4',
                        tenant.slug === tenantSlug ? 'opacity-100' : 'opacity-0',
                      )}
                    />
                    <div className="flex flex-col">
                      <span>{tenant.displayName}</span>
                      <span className="text-xs text-muted-foreground">{tenant.slug}</span>
                    </div>
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
    </div>
  )
}
