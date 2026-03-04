import { z } from "zod"

// Feature list — single source of truth for valid feature identifiers

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

export type FeatureId = (typeof ALL_FEATURES)[number]

// Zod validation schemas

export const TenantThemeConfigSchema = z.object({
  primaryColor: z.string(),
  logoUrl: z.string().url().optional(),
  faviconUrl: z.string().url().optional(),
  fontFamily: z.string().optional(),
})

export const TenantFeaturesConfigSchema = z.object({
  enabled: z.array(z.enum(ALL_FEATURES)),
  defaultFeature: z.enum(ALL_FEATURES).optional(),
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

// TypeScript types derived from Zod schemas

export type TenantThemeConfig = z.infer<typeof TenantThemeConfigSchema>
export type TenantFeaturesConfig = z.infer<typeof TenantFeaturesConfigSchema>
export type DashboardWidget = z.infer<typeof DashboardWidgetSchema>
export type TableDefaults = z.infer<typeof TableDefaultsSchema>
export type TenantLayoutConfig = z.infer<typeof TenantLayoutConfigSchema>
export type TenantUIConfig = z.infer<typeof TenantUIConfigSchema>

// Default configuration with all features enabled

export const DEFAULT_UI_CONFIG: TenantUIConfig = {
  features: {
    enabled: [...ALL_FEATURES],
    defaultFeature: "dashboard",
  },
}

export function validateTenantUIConfig(data: unknown): TenantUIConfig {
  return TenantUIConfigSchema.parse(data)
}
