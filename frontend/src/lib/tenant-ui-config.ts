import { z } from "zod"

// TypeScript interfaces

export interface TenantThemeConfig {
  primaryColor: string
  logoUrl?: string
  faviconUrl?: string
  fontFamily?: string
}

export interface TenantFeaturesConfig {
  enabled: string[]
  defaultFeature?: string
}

export interface DashboardWidget {
  feature: string
  component: string
  position: number
}

export interface TableDefaults {
  visibleColumns: string[]
  defaultSort?: string
}

export interface TenantLayoutConfig {
  dashboard: {
    widgets: DashboardWidget[]
  }
  tableDefaults: Record<string, TableDefaults>
}

export interface TenantUIConfig {
  theme?: TenantThemeConfig
  features?: TenantFeaturesConfig
  layout?: TenantLayoutConfig
}

// Default configuration with all features enabled

export const ALL_FEATURES = [
  "dashboard",
  "accounts",
  "payments",
  "ledger",
  "positions",
  "reconciliation",
  "parties",
  "tenants",
  "reference-data",
  "internal-accounts",
  "market-data",
  "forecasting",
  "sagas",
  "manifests",
  "mappings",
  "audit",
  "mcp-config",
] as const

export const DEFAULT_UI_CONFIG: TenantUIConfig = {
  features: {
    enabled: [...ALL_FEATURES],
    defaultFeature: "dashboard",
  },
}

// Zod validation schemas

export const TenantThemeConfigSchema = z.object({
  primaryColor: z.string(),
  logoUrl: z.string().url().optional(),
  faviconUrl: z.string().url().optional(),
  fontFamily: z.string().optional(),
})

export const TenantFeaturesConfigSchema = z.object({
  enabled: z.array(z.string()),
  defaultFeature: z.string().optional(),
})

export const DashboardWidgetSchema = z.object({
  feature: z.string(),
  component: z.string(),
  position: z.number().int(),
})

export const TableDefaultsSchema = z.object({
  visibleColumns: z.array(z.string()),
  defaultSort: z.string().optional(),
})

export const TenantLayoutConfigSchema = z.object({
  dashboard: z.object({
    widgets: z.array(DashboardWidgetSchema),
  }),
  tableDefaults: z.record(z.string(), TableDefaultsSchema),
})

export const TenantUIConfigSchema = z.object({
  theme: TenantThemeConfigSchema.optional(),
  features: TenantFeaturesConfigSchema.optional(),
  layout: TenantLayoutConfigSchema.optional(),
})

export function validateTenantUIConfig(data: unknown): TenantUIConfig {
  return TenantUIConfigSchema.parse(data)
}
