import { useMemo } from 'react'
import { useTenantContext } from '@/contexts/tenant-context'
import {
  DEFAULT_UI_CONFIG,
  type DashboardWidget,
  type TableDefaults,
} from '@/lib/tenant-ui-config'

const DEFAULT_WIDGETS: DashboardWidget[] = []

const DEFAULT_TABLE_DEFAULTS: Readonly<Partial<Record<string, TableDefaults>>> = {}

export interface TenantLayoutResult {
  widgets: readonly DashboardWidget[]
  tableDefaults: Readonly<Partial<Record<string, TableDefaults>>>
  getTableDefaults: (tableKey: string) => TableDefaults | undefined
}

export function useTenantLayout(): TenantLayoutResult {
  const { tenantConfig } = useTenantContext()

  return useMemo(() => {
    const config = tenantConfig ?? DEFAULT_UI_CONFIG
    const layout = config.layout

    const widgets: readonly DashboardWidget[] =
      layout?.dashboard?.widgets ?? DEFAULT_WIDGETS

    const tableDefaults: Readonly<Partial<Record<string, TableDefaults>>> =
      layout?.tableDefaults ?? DEFAULT_TABLE_DEFAULTS

    return {
      widgets,
      tableDefaults,
      getTableDefaults: (tableKey: string) =>
        Object.hasOwn(tableDefaults, tableKey) ? tableDefaults[tableKey] : undefined,
    }
  }, [tenantConfig])
}
