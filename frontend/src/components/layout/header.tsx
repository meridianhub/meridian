import { Menu, User, LogOut } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useAuth } from '@/contexts/auth-context'
import { useTenantContext } from '@/contexts/tenant-context'
import { formatSlugAsDisplayName } from '@/lib/tenant-utils'
import { TenantSelector } from '@/components/layout/tenant-selector'
import { ThemeToggle } from '@/components/layout/theme-toggle'

interface HeaderProps {
  onMenuToggle: () => void
  sidebarOpen?: boolean
  sidebarId?: string
}

function useBrandName(): string {
  const { claims } = useAuth()
  const { isPlatformAdmin, currentTenant, tenantSlug } = useTenantContext()

  // Platform admins viewing a tenant: use the tenant name from the selector
  if (isPlatformAdmin && currentTenant) return currentTenant.name
  // Tenant users: JWT display name -> formatted slug -> fallback
  if (claims?.tenantDisplayName) return claims.tenantDisplayName
  if (tenantSlug) return formatSlugAsDisplayName(tenantSlug)
  return 'Meridian'
}

export function Header({ onMenuToggle, sidebarOpen, sidebarId }: HeaderProps) {
  const { logout } = useAuth()
  const { isPlatformAdmin } = useTenantContext()
  const brandName = useBrandName()

  return (
    <header className="relative z-50 flex h-14 items-center gap-4 border-b bg-background px-4 shadow-sm">
      <Button
        variant="ghost"
        size="icon"
        aria-label="Toggle menu"
        aria-expanded={sidebarOpen}
        aria-controls={sidebarId}
        onClick={onMenuToggle}
        className="shrink-0 md:hidden"
      >
        <Menu className="size-5" />
      </Button>

      <div className="flex items-center gap-2">
        <span className="font-semibold text-foreground">{brandName}</span>
      </div>

      <div className="ml-auto flex items-center gap-4">
        {isPlatformAdmin && <TenantSelector />}

        <ThemeToggle />

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="User menu">
              <User className="size-5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => logout()}>
              <LogOut className="mr-2 size-4" />
              Log out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
