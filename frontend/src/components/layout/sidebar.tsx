import { Link } from 'react-router-dom'
import {
  LayoutDashboard,
  Wallet,
  Building,
  ArrowLeftRight,
  Building2,
  Activity,
  Users,
  TrendingUp,
  BookOpen,
  Code,
  LineChart,
  BarChart3,
  Database,
  Map,
  ClipboardList,
  CheckSquare,
  FileJson,
} from 'lucide-react'
import { cn } from '@/lib/utils'

interface NavItem {
  label: string
  href: string
  icon: React.ComponentType<{ className?: string }>
}

const TENANT_NAV_ITEMS: NavItem[] = [
  { label: 'Dashboard', href: '/', icon: LayoutDashboard },
  { label: 'Accounts', href: '/accounts', icon: Wallet },
  { label: 'Internal Accounts', href: '/internal-accounts', icon: Building },
  { label: 'Payments', href: '/payments', icon: ArrowLeftRight },
  { label: 'Positions', href: '/positions', icon: TrendingUp },
  { label: 'Ledger', href: '/ledger', icon: BookOpen },
  { label: 'Parties', href: '/parties', icon: Users },
  { label: 'Reconciliation', href: '/reconciliation', icon: CheckSquare },
  { label: 'Starlark Config', href: '/starlark-config', icon: Code },
  { label: 'Market Data', href: '/market-data', icon: LineChart },
  { label: 'Forecasting', href: '/forecasting', icon: BarChart3 },
  { label: 'Reference Data', href: '/reference-data', icon: Database },
  { label: 'Gateway Mappings', href: '/gateway-mappings', icon: Map },
  { label: 'Manifests', href: '/manifests', icon: FileJson },
  { label: 'Audit Log', href: '/audit-log', icon: ClipboardList },
]

const PLATFORM_NAV_ITEMS: NavItem[] = [
  { label: 'Tenant Management', href: '/tenants', icon: Building2 },
  { label: 'Platform Monitoring', href: '/platform', icon: Activity },
]

interface SidebarProps {
  lens: 'platform' | 'tenant'
  currentPath?: string
  isOpen?: boolean
  id?: string
  onClose?: () => void
}

export function Sidebar({ lens, currentPath = '/', isOpen = false, id, onClose }: SidebarProps) {
  const showPlatformItems = lens === 'platform'

  return (
    <>
      {isOpen && onClose && (
        <div
          className="fixed inset-0 z-30 bg-black/50 md:hidden"
          aria-hidden="true"
          onClick={onClose}
        />
      )}
      <aside
      id={id}
      data-open={String(isOpen)}
      className={cn(
        'flex h-full w-64 flex-col bg-gray-900 text-white transition-transform duration-200',
        'max-md:fixed max-md:inset-y-0 max-md:left-0 max-md:z-40',
        !isOpen && 'max-md:-translate-x-full',
      )}
    >
      <nav aria-label="Main navigation" className="flex-1 overflow-y-auto py-4">
        <ul role="list" className="space-y-1 px-2">
          {TENANT_NAV_ITEMS.map((item) => {
            const Icon = item.icon
            const isActive = currentPath === item.href
            return (
              <li key={item.href}>
                <Link
                  to={item.href}
                  aria-current={isActive ? 'page' : undefined}
                  className={cn(
                    'flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors',
                    isActive
                      ? 'bg-gray-700 text-white'
                      : 'text-gray-300 hover:bg-gray-700 hover:text-white',
                  )}
                >
                  <Icon className="size-4 shrink-0" />
                  {item.label}
                </Link>
              </li>
            )
          })}

          {showPlatformItems && (
            <>
              <li role="separator" className="my-2 border-t border-gray-700" />
              {PLATFORM_NAV_ITEMS.map((item) => {
                const Icon = item.icon
                const isActive = currentPath === item.href
                return (
                  <li key={item.href}>
                    <Link
                      to={item.href}
                      aria-current={isActive ? 'page' : undefined}
                      className={cn(
                        'flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors',
                        isActive
                          ? 'bg-gray-700 text-white'
                          : 'text-gray-300 hover:bg-gray-700 hover:text-white',
                      )}
                    >
                      <Icon className="size-4 shrink-0" />
                      {item.label}
                    </Link>
                  </li>
                )
              })}
            </>
          )}
        </ul>
      </nav>
    </aside>
    </>
  )
}
