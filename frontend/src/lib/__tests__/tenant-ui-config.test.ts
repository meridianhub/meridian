import { describe, it, expect } from "vitest"
import {
  validateTenantUIConfig,
  DEFAULT_UI_CONFIG,
  TenantUIConfigSchema,
  ALL_FEATURES,
} from "../tenant-ui-config"

describe("tenant-ui-config", () => {
  describe("validateTenantUIConfig", () => {
    it("accepts a valid full config", () => {
      const config = {
        theme: {
          primaryColor: "#007bff",
          logoUrl: "https://example.com/logo.png",
          faviconUrl: "https://example.com/favicon.ico",
          fontFamily: "Inter",
        },
        features: {
          enabled: ["dashboard", "accounts", "payments"],
          defaultFeature: "dashboard",
        },
        layout: {
          dashboard: {
            widgets: [
              { feature: "accounts", component: "AccountSummaryWidget", position: 0 },
              { feature: "payments", component: "RecentPaymentsWidget", position: 1 },
            ],
          },
          tableDefaults: {
            accounts: {
              visibleColumns: ["id", "name", "balance"],
              defaultSort: "name",
            },
          },
        },
      }
      const result = validateTenantUIConfig(config)
      expect(result).toEqual(config)
    })

    it("accepts a partial config with only theme", () => {
      const config = {
        theme: { primaryColor: "#ff0000" },
      }
      const result = validateTenantUIConfig(config)
      expect(result.theme?.primaryColor).toBe("#ff0000")
      expect(result.features).toBeUndefined()
      expect(result.layout).toBeUndefined()
    })

    it("accepts an empty config (all fields optional)", () => {
      const result = validateTenantUIConfig({})
      expect(result).toEqual({})
    })

    it("accepts features config without defaultFeature", () => {
      const config = {
        features: {
          enabled: ["dashboard", "accounts"],
        },
      }
      const result = validateTenantUIConfig(config)
      expect(result.features?.enabled).toEqual(["dashboard", "accounts"])
      expect(result.features?.defaultFeature).toBeUndefined()
    })

    it("rejects invalid theme logoUrl (not a URL)", () => {
      const config = {
        theme: {
          primaryColor: "#007bff",
          logoUrl: "not-a-url",
        },
      }
      expect(() => validateTenantUIConfig(config)).toThrow()
    })

    it("rejects theme missing primaryColor", () => {
      const config = {
        theme: {
          logoUrl: "https://example.com/logo.png",
        },
      }
      expect(() => validateTenantUIConfig(config)).toThrow()
    })

    it("rejects non-integer widget position", () => {
      const config = {
        layout: {
          dashboard: {
            widgets: [
              { feature: "accounts", component: "AccountWidget", position: 1.5 },
            ],
          },
          tableDefaults: {},
        },
      }
      expect(() => validateTenantUIConfig(config)).toThrow()
    })

    it("rejects non-object input", () => {
      expect(() => validateTenantUIConfig("invalid")).toThrow()
      expect(() => validateTenantUIConfig(null)).toThrow()
      expect(() => validateTenantUIConfig(42)).toThrow()
    })

    it("rejects unknown feature identifiers in enabled list", () => {
      const config = {
        features: {
          enabled: ["dashboard", "acount"],  // typo: 'acount'
        },
      }
      expect(() => validateTenantUIConfig(config)).toThrow()
    })

    it("rejects unknown feature identifier as defaultFeature", () => {
      const config = {
        features: {
          enabled: ["dashboard"],
          defaultFeature: "not-a-feature",
        },
      }
      expect(() => validateTenantUIConfig(config)).toThrow()
    })
  })

  describe("DEFAULT_UI_CONFIG", () => {
    it("passes schema validation", () => {
      const result = TenantUIConfigSchema.safeParse(DEFAULT_UI_CONFIG)
      expect(result.success).toBe(true)
    })

    it("includes all expected features", () => {
      expect(DEFAULT_UI_CONFIG.features?.enabled).toEqual(expect.arrayContaining([...ALL_FEATURES]))
      expect(DEFAULT_UI_CONFIG.features?.enabled).toHaveLength(ALL_FEATURES.length)
    })

    it("has dashboard as default feature", () => {
      expect(DEFAULT_UI_CONFIG.features?.defaultFeature).toBe("dashboard")
    })

    it("contains all features", () => {
      expect(DEFAULT_UI_CONFIG.features?.enabled).toHaveLength(ALL_FEATURES.length)
    })
  })

  describe("ALL_FEATURES", () => {
    it("contains expected feature identifiers", () => {
      expect(ALL_FEATURES).toContain("dashboard")
      expect(ALL_FEATURES).toContain("accounts")
      expect(ALL_FEATURES).toContain("payments")
      expect(ALL_FEATURES).toContain("ledger")
      expect(ALL_FEATURES).toContain("positions")
      expect(ALL_FEATURES).toContain("reconciliation")
      expect(ALL_FEATURES).toContain("parties")
      expect(ALL_FEATURES).toContain("tenants")
      expect(ALL_FEATURES).toContain("reference-data")
      expect(ALL_FEATURES).toContain("internal-accounts")
      expect(ALL_FEATURES).toContain("market-data")
      expect(ALL_FEATURES).toContain("forecasting")
      expect(ALL_FEATURES).toContain("sagas")
      expect(ALL_FEATURES).toContain("mappings")
      expect(ALL_FEATURES).toContain("audit")
      expect(ALL_FEATURES).toContain("mcp-config")
    })
  })

  describe("TenantLayoutConfig", () => {
    it("accepts layout with empty widgets and tableDefaults", () => {
      const config = {
        layout: {
          dashboard: { widgets: [] },
          tableDefaults: {},
        },
      }
      const result = validateTenantUIConfig(config)
      expect(result.layout?.dashboard.widgets).toEqual([])
      expect(result.layout?.tableDefaults).toEqual({})
    })

    it("accepts tableDefaults without defaultSort", () => {
      const config = {
        layout: {
          dashboard: { widgets: [] },
          tableDefaults: {
            accounts: {
              visibleColumns: ["id", "name"],
            },
          },
        },
      }
      const result = validateTenantUIConfig(config)
      expect(result.layout?.tableDefaults["accounts"].visibleColumns).toEqual(["id", "name"])
      expect(result.layout?.tableDefaults["accounts"].defaultSort).toBeUndefined()
    })
  })
})
