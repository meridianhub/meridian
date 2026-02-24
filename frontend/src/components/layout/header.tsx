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
import { TenantSelector } from '@/components/layout/tenant-selector'

interface HeaderProps {
  onMenuToggle: () => void
  sidebarOpen?: boolean
  sidebarId?: string
}

export function Header({ onMenuToggle, sidebarOpen, sidebarId }: HeaderProps) {
  const { logout } = useAuth()
  const { isPlatformAdmin } = useTenantContext()

  return (
    <header className="relative z-50 flex h-14 items-center gap-4 border-b bg-white px-4 shadow-sm">
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
        <span className="font-semibold text-gray-900">Meridian</span>
      </div>

      <div className="ml-auto flex items-center gap-4">
        {isPlatformAdmin && <TenantSelector />}

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
