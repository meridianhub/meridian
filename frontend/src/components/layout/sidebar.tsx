import { Link } from 'react-router-dom'
import { BuildInfo } from './build-info'
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
  Bot,
  Library,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { useTenantFeatures } from '@/hooks/use-tenant-features'
import { useTenantContext } from '@/contexts/tenant-context'

interface NavItem {
  label: string
  href: string
  icon: React.ComponentType<{ className?: string }>
  feature?: string
}

interface NavGroup {
  label: string
  items: NavItem[]
}

const TENANT_NAV_GROUPS: NavGroup[] = [
  {
    label: 'Operations',
    items: [
      { label: 'Dashboard', href: '/', icon: LayoutDashboard, feature: 'dashboard' },
      { label: 'Accounts', href: '/accounts', icon: Wallet, feature: 'accounts' },
      { label: 'Internal Accounts', href: '/internal-accounts', icon: Building, feature: 'internal-accounts' },
      { label: 'Payments', href: '/payments', icon: ArrowLeftRight, feature: 'payments' },
      { label: 'Transactions', href: '/transactions', icon: Activity },
      { label: 'Positions', href: '/positions', icon: TrendingUp, feature: 'positions' },
      { label: 'Ledger', href: '/ledger', icon: BookOpen, feature: 'ledger' },
      { label: 'Parties', href: '/parties', icon: Users, feature: 'parties' },
      { label: 'Reconciliation', href: '/reconciliation', icon: CheckSquare, feature: 'reconciliation' },
    ],
  },
  {
    label: 'Configuration',
    items: [
      { label: 'Starlark Config', href: '/starlark-config', icon: Code, feature: 'sagas' },
      { label: 'Market Data', href: '/market-data', icon: LineChart, feature: 'market-data' },
      { label: 'Forecasting', href: '/forecasting', icon: BarChart3, feature: 'forecasting' },
      { label: 'Reference Data', href: '/reference-data', icon: Database, feature: 'reference-data' },
      { label: 'Gateway Mappings', href: '/gateway-mappings', icon: Map, feature: 'mappings' },
      { label: 'Manifests', href: '/manifests', icon: FileJson, feature: 'manifests' },
      { label: 'MCP Config', href: '/mcp-config', icon: Bot, feature: 'mcp-config' },
      { label: 'Cookbook', href: '/cookbook', icon: Library },
    ],
  },
  {
    label: 'Admin',
    items: [
      { label: 'Audit Log', href: '/audit-log', icon: ClipboardList, feature: 'audit' },
    ],
  },
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
  const { isFeatureEnabled } = useTenantFeatures()
  const { isPlatformAdmin } = useTenantContext()

  function isItemVisible(item: NavItem): boolean {
    if (!item.feature) return true
    if (isPlatformAdmin) return true
    return isFeatureEnabled(item.feature)
  }

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
      <nav aria-label="Main navigation" className="min-h-0 flex-1 overflow-y-auto py-4">
        <ul role="list" className="px-2">
          {TENANT_NAV_GROUPS.reduce<{ elements: React.ReactNode[]; visibleIndex: number }>((acc, group) => {
            const visibleItems = group.items.filter(isItemVisible)
            if (visibleItems.length === 0) return acc

            acc.elements.push(
              <li key={group.label}>
                <div className={cn('mb-1 px-3 text-[10px] font-semibold uppercase tracking-wider text-gray-500', acc.visibleIndex > 0 && 'mt-4')}>
                  {group.label}
                </div>
                <ul role="list" className="space-y-0.5">
                  {visibleItems.map((item) => {
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
                </ul>
              </li>
            )
            acc.visibleIndex++
            return acc
          }, { elements: [], visibleIndex: 0 }).elements}

          {showPlatformItems && (
            <>
              <li role="separator" className="my-3 border-t border-gray-700" />
              <li>
                <div className="mb-1 px-3 text-[10px] font-semibold uppercase tracking-wider text-gray-500">
                  Platform
                </div>
                <ul role="list" className="space-y-0.5">
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
                </ul>
              </li>
            </>
          )}
        </ul>
      </nav>
      <BuildInfo />
    </aside>
    </>
  )
}
