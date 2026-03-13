import { useCallback, useEffect, useRef, useState } from 'react'
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
  UserCog,
  TrendingUp,
  BookOpen,
  Code,
  LineChart,
  BarChart3,
  Database,
  Map,
  ClipboardList,
  CheckSquare,
  Bot,
  Library,
  Boxes,
  ChevronDown,
  ChevronRight,
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
  collapsible?: boolean
  feature?: string
}

const STORAGE_KEY = 'meridian:sidebar-collapsed'

const FOCUSABLE_SELECTORS =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function useFocusTrap(containerRef: React.RefObject<HTMLElement | null>, active: boolean) {
  const previousFocusRef = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!active) return

    previousFocusRef.current = document.activeElement as HTMLElement

    const container = containerRef.current
    if (!container) return

    const focusables = () =>
      Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTORS)).filter(
        el => !el.closest('[hidden]') && getComputedStyle(el).display !== 'none',
      )

    const first = focusables()[0]
    if (first) first.focus()

    function handleKeyDown(e: KeyboardEvent) {
      if (e.key !== 'Tab') return
      const items = focusables()
      if (items.length === 0) return
      const firstItem = items[0]
      const lastItem = items[items.length - 1]
      if (e.shiftKey) {
        if (document.activeElement === firstItem) {
          e.preventDefault()
          lastItem.focus()
        }
      } else {
        if (document.activeElement === lastItem) {
          e.preventDefault()
          firstItem.focus()
        }
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      previousFocusRef.current?.focus()
    }
  }, [active, containerRef])
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
    label: 'Economy',
    collapsible: true,
    feature: 'economy',
    items: [
      { label: 'Overview', href: '/economy', icon: Boxes, feature: 'economy' },
      { label: 'Reference Data', href: '/reference-data', icon: Database, feature: 'reference-data' },
      { label: 'Starlark Config', href: '/starlark-config', icon: Code, feature: 'sagas' },
      { label: 'Market Data', href: '/market-data', icon: LineChart, feature: 'market-data' },
      { label: 'Forecasting', href: '/forecasting', icon: BarChart3, feature: 'forecasting' },
    ],
  },
  {
    label: 'Configuration',
    items: [
      { label: 'Gateway Mappings', href: '/gateway-mappings', icon: Map, feature: 'mappings' },
      { label: 'MCP Config', href: '/mcp-config', icon: Bot, feature: 'mcp-config' },
      { label: 'Cookbook', href: '/cookbook', icon: Library },
    ],
  },
  {
    label: 'Admin',
    items: [
      { label: 'Users', href: '/users', icon: UserCog },
      { label: 'Audit Log', href: '/audit-log', icon: ClipboardList, feature: 'audit' },
    ],
  },
]

const PLATFORM_NAV_ITEMS: NavItem[] = [
  { label: 'Tenant Management', href: '/tenants', icon: Building2 },
  { label: 'Platform Monitoring', href: '/platform', icon: Activity },
]

function loadCollapsedGroups(): Set<string> {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored) return new Set(JSON.parse(stored))
  } catch { /* ignore malformed data */ }
  return new Set()
}

function saveCollapsedGroups(collapsed: Set<string>): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify([...collapsed]))
  } catch { /* ignore storage write failures (quota, private browsing) */ }
}

function useCollapsedGroups(currentPath: string) {
  const [collapsed, setCollapsed] = useState<Set<string>>(loadCollapsedGroups)

  const toggle = useCallback((label: string) => {
    setCollapsed(prev => {
      const next = new Set(prev)
      if (next.has(label)) {
        next.delete(label)
      } else {
        next.add(label)
      }
      saveCollapsedGroups(next)
      return next
    })
  }, [])

  // Derive effective collapsed state: auto-expand groups matching current path
  const isCollapsed = useCallback((label: string) => {
    if (!collapsed.has(label)) return false
    // Auto-expand if current path matches a child of this group
    const group = TENANT_NAV_GROUPS.find(g => g.label === label)
    if (group?.collapsible && group.items.some(item => currentPath === item.href)) {
      return false
    }
    return true
  }, [collapsed, currentPath])

  return { toggle, isCollapsed }
}

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
  const { toggle, isCollapsed } = useCollapsedGroups(currentPath)
  const sidebarRef = useRef<HTMLElement>(null)

  // Only trap focus when sidebar is acting as a mobile overlay (isOpen + onClose present)
  useFocusTrap(sidebarRef, isOpen && !!onClose)

  useEffect(() => {
    if (!isOpen || !onClose) return
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose!()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, onClose])

  function isItemVisible(item: NavItem): boolean {
    if (!item.feature) return true
    if (isPlatformAdmin) return true
    return isFeatureEnabled(item.feature)
  }

  function isGroupVisible(group: NavGroup): boolean {
    if (group.feature) {
      if (!isPlatformAdmin && !isFeatureEnabled(group.feature)) return false
    }
    return group.items.some(isItemVisible)
  }

  return (
    <>
      {isOpen && onClose && (
        <div
          className="fixed inset-0 z-30 bg-overlay md:hidden"
          aria-hidden="true"
          role="presentation"
          onClick={onClose}
        />
      )}
      <aside
      ref={sidebarRef}
      id={id}
      data-open={String(isOpen)}
      className={cn(
        'flex h-full w-64 flex-col bg-sidebar text-sidebar-foreground transition-transform duration-200',
        'max-md:fixed max-md:inset-y-0 max-md:left-0 max-md:z-40',
        !isOpen && 'max-md:-translate-x-full',
      )}
    >
      <nav aria-label="Main navigation" className="min-h-0 flex-1 overflow-y-auto py-4">
        <ul role="list" className="px-2">
          {TENANT_NAV_GROUPS.reduce<{ elements: React.ReactNode[]; visibleIndex: number }>((acc, group) => {
            if (!isGroupVisible(group)) return acc

            const visibleItems = group.items.filter(isItemVisible)
            if (visibleItems.length === 0) return acc

            const groupCollapsed = group.collapsible && isCollapsed(group.label)

            acc.elements.push(
              <li key={group.label}>
                {group.collapsible ? (
                  <button
                    type="button"
                    onClick={() => toggle(group.label)}
                    aria-expanded={!groupCollapsed}
                    className={cn(
                      'flex w-full items-center justify-between px-3 text-[10px] font-semibold uppercase tracking-wider text-sidebar-foreground/50',
                      'mb-1 hover:text-sidebar-foreground/70',
                      acc.visibleIndex > 0 && 'mt-4',
                    )}
                  >
                    {group.label}
                    {groupCollapsed
                      ? <ChevronRight className="size-3" />
                      : <ChevronDown className="size-3" />
                    }
                  </button>
                ) : (
                  <div className={cn('mb-1 px-3 text-[10px] font-semibold uppercase tracking-wider text-sidebar-foreground/50', acc.visibleIndex > 0 && 'mt-4')}>
                    {group.label}
                  </div>
                )}
                {!groupCollapsed && (
                  <ul role="list" className={cn('space-y-0.5', group.collapsible && 'pl-2')}>
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
                                ? 'bg-sidebar-accent text-sidebar-foreground'
                                : 'text-sidebar-foreground/70 hover:bg-sidebar-accent hover:text-sidebar-foreground',
                            )}
                          >
                            <Icon className="size-4 shrink-0" />
                            {item.label}
                          </Link>
                        </li>
                      )
                    })}
                  </ul>
                )}
              </li>
            )
            acc.visibleIndex++
            return acc
          }, { elements: [], visibleIndex: 0 }).elements}

          {showPlatformItems && (
            <>
              <li role="separator" className="my-3 border-t border-sidebar-border" />
              <li>
                <div className="mb-1 px-3 text-[10px] font-semibold uppercase tracking-wider text-sidebar-foreground/50">
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
                              ? 'bg-sidebar-accent text-sidebar-foreground'
                              : 'text-sidebar-foreground/70 hover:bg-sidebar-accent hover:text-sidebar-foreground',
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
