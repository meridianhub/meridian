# PRD: Meridian Cookbook - Unified Pattern Registry

## Problem Statement

Meridian has two categories of reusable building blocks
with no unified discovery mechanism:

**Economy patterns** - manifest configurations, saga
scripts, and design patterns for business models (energy
settlement, SaaS billing, carbon credits). These exist as
example manifests, test fixtures, and a design patterns
guide - scattered across multiple directories and formats.

**UI components** - shared React components, feature
module widgets, and tenant-configurable layout elements.
These exist as TypeScript files with no structured
metadata describing what's available, what props they
accept, or which feature modules use them.

Dropping Storybook (see PRD-034 architectural decision)
leaves a gap: there is no single catalogue where
developers and AI assistants can discover both the UI
building blocks for the console and the economy building
blocks for tenant configuration. These are two sides of
the same coin - a tenant's economy manifest drives what
the UI displays, and the UI components determine how that
economy is presented.

This creates four problems:

1. **Discovery**: No single index of available building
   blocks across UI and economy configuration. AI must
   search multiple directories and file formats.
2. **Composition**: Economy patterns that work well
   together (energy settlement + carbon offset) have no
   formal declaration of compatibility. UI components
   have no structured metadata linking them to the
   features they serve.
3. **Navigation**: Given a tenant's current state, there
   is no way to ask "what can I add next?" for either
   economy extensions or UI customisations.
4. **Consistency**: UI components and economy patterns
   use different discovery mechanisms despite both being
   consumable through the same shadcn registry model.

## Vision

A **Meridian Cookbook** - a unified, AI-navigable registry
of composable patterns covering both UI components and
economy configurations. Following the shadcn/ui registry
model for static discoverability, and HATEOAS principles
for state-aware navigation.

The cookbook is to Meridian what shadcn/ui is to React: a
catalogue of building blocks that AI assistants and
developers can browse, compose, and apply - without
parsing unstructured documentation and without guessing
what works together. The difference is scope: our
cookbook covers both the visual layer (React components,
dashboard widgets) and the business layer (instruments,
account types, sagas, valuation rules).

**One registry, two pattern types:**

| Type | Examples | Consumer |
|------|----------|----------|
| `registry:ui` | DataTable, MoneyDisplay, StatusBadge | Frontend dev, tenant UI config |
| `registry:pattern` | Energy Settlement, SaaS Billing | AI manifest config, MCP tools |

Both types share the same registry format, discovery
tools, and composition model. An AI configuring a new
tenant can browse economy patterns to build the manifest
AND browse UI components to understand how it renders.

### Two Modes of Operation

**Offline (static registry browsing)**: The registry layer
is static JSON files. AI assistants can read `registry.json`
and individual pattern/component files directly from the
filesystem or a CDN. No running server needed. This covers
browsing, reading metadata, and manually composing
configurations.

**Online (state-aware HATEOAS discovery)**: The
`meridian_cookbook_discover` MCP tool inspects a tenant's
current manifest and UI config, returning compatible
patterns with reasons. This requires the MCP server to be
running. Discovery adds intelligence on top of the static
registry - it is not required for basic browsing.

### Three Layers

| Layer | Model | Requires server? |
|-------|-------|------------------|
| **Registry** | shadcn/ui | No - static JSON files |
| **Discovery** | HATEOAS | Yes - MCP server computes compatibility |
| **Execution** | Existing MCP | Yes - validate, plan, apply |

## Goals

1. Unify UI component metadata and economy configuration
   patterns into a single machine-readable registry
2. Structure existing economy patterns (manifests, sagas,
   design patterns) into the registry format
3. Describe all shared and feature UI components with
   structured metadata (props, dependencies, feature
   module, tenant-configurable properties)
4. Declare composability between economy patterns (what
   works together, what conflicts)
5. Provide state-aware discovery: given a tenant's current
   manifest and UI config, surface compatible patterns
6. Expose the cookbook through MCP tools for AI-assisted
   configuration of both economy and UI
7. Maintain the cookbook as living documentation that
   evolves with the platform

## Non-Goals

- Replacing the manifest apply workflow (the cookbook
  produces manifests that go through the existing
  validate/plan/apply pipeline)
- Building a visual component browser to replace
  Storybook (the registry serves AI and CLI consumers;
  visual preview comes from running the app itself)
- Auto-generating saga scripts from natural language (the
  cookbook provides tested reference scripts, not generated
  ones)
- Tenant-specific pattern storage (the cookbook is platform
  content, not per-tenant)

## Architecture

### Registry Entry Types

The cookbook uses two `type` values within the same
shadcn/ui registry schema:

**`registry:ui`** - UI components (React)

```json
{
  "$schema": "https://cookbook.meridianhub.org/schema/registry-item.json",
  "name": "data-table",
  "type": "registry:ui",
  "title": "Data Table",
  "description": "Sortable, filterable table with pagination.",
  "registryDependencies": ["status-badge", "entity-link"],
  "categories": ["shared", "layout"],
  "meta": {
    "feature_module": "shared",
    "tenant_configurable": true,
    "configurable_props": [
      "visible_columns",
      "default_sort"
    ],
    "used_by": [
      "accounts", "payments", "ledger",
      "positions", "reconciliation"
    ]
  },
  "files": [
    {
      "path": "shared/data-table.tsx",
      "type": "registry:ui"
    }
  ]
}
```

**`registry:pattern`** - Economy configuration patterns

```json
{
  "$schema": "https://cookbook.meridianhub.org/schema/registry-item.json",
  "name": "energy-settlement",
  "type": "registry:pattern",
  "title": "Energy Usage-to-Value Settlement",
  "description": "Converts metered kWh into monetary value using market rates.",
  "categories": ["energy", "utilities", "cross-instrument"],
  "meta": {
    "complexity": 3,
    "design_pattern": "cross-instrument-valuation",
    "industries": ["energy", "utilities"],
    "provides": {
      "instruments": ["KWH"],
      "account_types": [
        "energy_metered",
        "energy_revenue_retail",
        "energy_revenue_wholesale"
      ],
      "sagas": ["usage_to_value"],
      "valuation_rules": [
        "kwh_to_gbp_retail",
        "kwh_to_gbp_wholesale"
      ],
      "triggers": [
        "event:position-keeping.transaction-captured.v1"
      ]
    },
    "requires": {
      "instruments": ["GBP"],
      "market_data": [
        "KWH_GBP_RETAIL",
        "KWH_GBP_WHOLESALE"
      ]
    },
    "composes_with": [
      "carbon-offset",
      "time-of-use-pricing",
      "payment-gateway-stripe"
    ],
    "conflicts_with": ["flat-rate-billing"],
    "extends": []
  },
  "registryDependencies": ["base-fiat-gbp"],
  "files": [
    {
      "path": "patterns/energy-settlement/manifest-fragment.yaml",
      "type": "registry:file",
      "target": "~/manifest-fragment.yaml"
    },
    {
      "path": "patterns/energy-settlement/usage_to_value.star",
      "type": "registry:file",
      "target": "~/sagas/usage_to_value.star"
    }
  ]
}
```

Both types use the same `$schema`, the same
`registryDependencies` mechanism, and the same `meta`
object (with type-specific fields). The `type` field
determines which metadata fields are relevant.

### Metadata Schema by Type

**Common fields (all types):**

| Field | Type | Purpose |
|-------|------|---------|
| `name` | string | Kebab-case canonical identifier |
| `type` | string | `registry:ui` or `registry:pattern` |
| `title` | string | Human-readable display name |
| `description` | string | Brief summary |
| `categories` | string[] | Tags for filtering |
| `registryDependencies` | string[] | Other registry items this depends on |
| `files` | object[] | Associated source files |

**UI-specific metadata (`registry:ui`):**

| Field | Type | Purpose |
|-------|------|---------|
| `meta.feature_module` | string | Which feature module owns this component |
| `meta.tenant_configurable` | bool | Whether tenants can configure this component |
| `meta.configurable_props` | string[] | Props exposed to tenant config |
| `meta.used_by` | string[] | Feature modules that use this component |

**Pattern-specific metadata (`registry:pattern`):**

| Field | Type | Purpose |
|-------|------|---------|
| `meta.complexity` | int (1-8) | Story points estimate |
| `meta.design_pattern` | string | Reference to design patterns guide |
| `meta.industries` | string[] | Industry tags |
| `meta.provides.instruments` | string[] | Instrument codes defined |
| `meta.provides.account_types` | string[] | Account types defined |
| `meta.provides.sagas` | string[] | Saga names included |
| `meta.provides.valuation_rules` | string[] | Valuation rules defined |
| `meta.provides.triggers` | string[] | Event triggers wired |
| `meta.requires.instruments` | string[] | Instruments that must exist |
| `meta.requires.market_data` | string[] | Market data datasets required |
| `meta.composes_with` | string[] | Patterns known to work together |
| `meta.conflicts_with` | string[] | Mutually exclusive patterns |
| `meta.extends` | string[] | Patterns this builds upon |

### Registry Index

`cookbook/registry.json` lists all entries across both
types:

```json
{
  "$schema": "https://cookbook.meridianhub.org/schema/registry.json",
  "name": "meridian-cookbook",
  "homepage": "https://github.com/meridianhub/meridian",
  "items": [
    {
      "name": "data-table",
      "type": "registry:ui",
      "title": "Data Table",
      "categories": ["shared", "layout"]
    },
    {
      "name": "money-display",
      "type": "registry:ui",
      "title": "Money Display",
      "categories": ["shared", "formatting"]
    },
    {
      "name": "account-summary-card",
      "type": "registry:ui",
      "title": "Account Summary Card",
      "categories": ["accounts", "dashboard"]
    },
    {
      "name": "base-fiat-gbp",
      "type": "registry:pattern",
      "title": "Base Fiat Currency (GBP)",
      "categories": ["foundation", "fiat"]
    },
    {
      "name": "energy-settlement",
      "type": "registry:pattern",
      "title": "Energy Usage-to-Value Settlement",
      "categories": ["energy", "cross-instrument"]
    },
    {
      "name": "saas-billing",
      "type": "registry:pattern",
      "title": "SaaS Usage Billing",
      "categories": ["technology", "billing"]
    }
  ]
}
```

The `type` field enables filtering: `type:registry:ui`
for UI components, `type:registry:pattern` for economy
patterns, or unfiltered for the full catalogue.

### Directory Structure

```text
cookbook/
├── registry.json                    # Unified index (UI + economy)
├── schema/
│   ├── registry.json                # JSON Schema for registry index
│   └── registry-item.json           # JSON Schema for all entry types
├── ui/                              # UI component entries
│   ├── data-table/
│   │   └── component.json           # UI component metadata
│   ├── money-display/
│   │   └── component.json
│   ├── status-badge/
│   │   └── component.json
│   ├── entity-link/
│   │   └── component.json
│   ├── direction-badge/
│   │   └── component.json
│   ├── detail-skeleton/
│   │   └── component.json
│   ├── breadcrumbs/
│   │   └── component.json
│   ├── time-display/
│   │   └── component.json
│   ├── account-summary-card/
│   │   └── component.json
│   └── recent-payments/
│       └── component.json
├── patterns/                        # Economy pattern entries
│   ├── base-fiat-gbp/
│   │   ├── pattern.json
│   │   └── manifest-fragment.yaml
│   ├── energy-settlement/
│   │   ├── pattern.json
│   │   ├── manifest-fragment.yaml
│   │   └── usage_to_value.star
│   ├── saas-billing/
│   │   ├── pattern.json
│   │   ├── manifest-fragment.yaml
│   │   └── compute_billing.star
│   ├── carbon-offset/
│   │   ├── pattern.json
│   │   ├── manifest-fragment.yaml
│   │   └── carbon_retirement.star
│   └── ...                          # See inventory tables below
└── docs/
    ├── authoring-patterns.md        # Guide for economy patterns
    └── authoring-components.md      # Guide for UI components
```

Note: the directory listing above is illustrative, not
exhaustive. See the Initial Inventory sections for the
full lists.

### HATEOAS Discovery

The discovery layer inspects a tenant's current manifest
and UI config, returning compatible patterns with
navigation links. This is the key difference from a
static catalogue: the response is state-aware.

**MCP tool: `meridian_cookbook_discover`**

Input: current manifest (or tenant_id to fetch it),
optional `type` filter (`registry:ui`, `registry:pattern`,
or omit for both)

Output:

```json
{
  "economy_state": {
    "instruments": ["GBP", "KWH"],
    "account_types": [
      "energy_metered",
      "energy_revenue_retail"
    ],
    "sagas": ["usage_to_value"],
    "market_data": ["KWH_GBP_RETAIL"]
  },
  "ui_state": {
    "enabled_features": [
      "accounts", "positions", "ledger"
    ],
    "dashboard_widgets": ["account-summary-card"]
  },
  "compatible_patterns": [
    {
      "name": "carbon-offset",
      "type": "registry:pattern",
      "rel": "extend",
      "title": "Add Carbon Credit Tracking",
      "reason": "You have KWH instruments - most energy tenants pair this with carbon accounting",
      "complexity": 3,
      "_links": {
        "detail": "/cookbook/patterns/carbon-offset",
        "compose": "/cookbook/compose?patterns=carbon-offset"
      }
    },
    {
      "name": "time-of-use-pricing",
      "type": "registry:pattern",
      "rel": "enhance",
      "title": "Add Time-of-Use Rate Curves",
      "reason": "Your usage_to_value saga uses flat rates - this adds temporal pricing",
      "complexity": 2,
      "_links": {
        "detail": "/cookbook/patterns/time-of-use-pricing",
        "compose": "/cookbook/compose?patterns=time-of-use-pricing"
      }
    }
  ],
  "compatible_components": [
    {
      "name": "quality-ladder-badge",
      "type": "registry:ui",
      "rel": "enhance",
      "title": "Quality Ladder Badge",
      "reason": "You have position-keeping enabled but no quality indicator in your dashboard",
      "_links": {
        "detail": "/cookbook/ui/quality-ladder-badge"
      }
    }
  ],
  "conflicting_patterns": [
    {
      "name": "flat-rate-billing",
      "reason": "Conflicts with existing usage_to_value saga"
    }
  ],
  "_links": {
    "self": "/cookbook/discover",
    "validate": "/manifest/validate",
    "plan": "/manifest/plan"
  }
}
```

**Why HATEOAS matters for AI agents:**

Traditional pattern catalogue: AI must know all patterns,
understand all composition rules, and reason about
compatibility from metadata. Works, but the AI carries the
cognitive load.

HATEOAS discovery: AI asks "what's possible from here?" and
the system tells it. The AI follows links, not logic.
Compatibility rules are computed by the server (via
`meridian_cookbook_discover`), while the actual manifest
merge is done client-side by the AI. Validation remains
server-side (`meridian_manifest_validate`). New patterns
become available to AI the moment they're added to the
registry, with zero prompt engineering.

### MCP Tools

The cookbook adds three MCP tools alongside the existing
manifest tools:

| Tool | Layer | Purpose |
|------|-------|---------|
| `meridian_cookbook_list` | Registry | Browse all entries, filter by type/category/industry |
| `meridian_cookbook_describe` | Registry | Get full entry detail with associated files |
| `meridian_cookbook_discover` | Discovery | Given current tenant state, return compatible entries |

All three tools support both `registry:ui` and
`registry:pattern` types. The `type` parameter filters
results when a consumer only wants one category.

**Composition is done client-side.** The AI reads pattern
manifest fragments and merges them into a complete manifest.
The existing `meridian_manifest_validate` tool catches any
conflicts or missing dependencies. This keeps the cookbook
stateless and the validation authoritative.

**Merge semantics** (economy patterns only): Manifest
fragments are merged using these deterministic rules:

- **Lists of scalars** (e.g., `instruments`,
  `account_types`, `sagas`, `requires.market_data`):
  append in pattern dependency order, then deduplicate
  by exact string value (first occurrence wins)
- **Scalars** (metadata.name, metadata.description):
  later pattern overrides earlier
- **Nested objects** (valuation_rules, seed_data):
  recursive merge, with later pattern values overriding
  on key collision
- **Post-merge validation**:
  `meridian_manifest_validate` runs on the merged result
  and rejects structurally invalid manifests (e.g.,
  duplicate instrument codes with conflicting
  definitions, missing required references). The merge
  itself is permissive; validation is authoritative

The merge order is deterministic: resolve
`registryDependencies` depth-first, then append the
requested patterns in the order specified by the user.
The resulting manifest is passed to
`meridian_manifest_validate` which is the authoritative
arbiter of correctness.

**Full conversation flow:**

```text
User: "We're an energy retailer billing residential customers"

AI: meridian_cookbook_list(type: "registry:pattern", category: "energy")
    → energy-settlement, time-of-use-pricing, carbon-offset

AI: meridian_cookbook_describe("energy-settlement")
    → full pattern with manifest fragment + saga script

AI: Merges energy-settlement fragment into manifest

AI: meridian_cookbook_discover(current_manifest)
    → compatible patterns: time-of-use-pricing, carbon-offset
    → compatible components: quality-ladder-badge
    → reason: "Your usage_to_value uses flat rates - TOU adds temporal pricing"

User: "Add time-of-use pricing"

AI: meridian_cookbook_describe("time-of-use-pricing")
    → manifest fragment + tou_energy_valuation.star

AI: Merges TOU fragment into manifest

AI: meridian_manifest_validate(merged_manifest)
    → valid

AI: meridian_manifest_plan(merged_manifest)
    → shows diff

User: "Looks good"

AI: meridian_manifest_apply(merged_manifest, plan_hash)

User: "What UI components are available for energy?"

AI: meridian_cookbook_list(type: "registry:ui", category: "positions")
    → quality-ladder-badge, data-table, money-display
```

## Initial Inventory

### Economy Patterns

Migrated from existing content:

| Pattern | Source | Industry |
|---------|--------|----------|
| `base-fiat-gbp` | New (foundation) | All |
| `base-fiat-usd` | New (foundation) | All |
| `energy-settlement` | `usage_to_value.star` + `energy_trading.manifest.json` | Energy |
| `time-of-use-pricing` | `tou_energy_valuation.star` | Energy |
| `saas-billing` | `compute_billing.star` + `saas_billing.manifest.json` | Technology |
| `dynamic-capacity-pricing` | `dynamic_capacity_billing.star` | Technology |
| `carbon-offset` | `carbon_credits.manifest.json` | Carbon/ESG |
| `precious-metals` | `valuation_on_capture.star` + `precious_metals.manifest.json` | Wealth |
| `entity-distribution` | `race_result_distribution.star` | Betting/Finance |
| `phantom-cost-basis` | `corporate_action_cost_adjustment.star` | Wealth |
| `payment-gateway-stripe` | `stripe_operational_gateway.manifest.json` | Payments |
| `kyc-compliance` | `kyc_on_party.star` | Compliance |

### UI Components

Extracted from existing `shared/` and `features/`
components (see PRD-034 for component relocation plan):

| Component | Feature Module | Tenant Configurable |
|-----------|---------------|---------------------|
| `data-table` | shared | Yes (columns, sort) |
| `money-display` | shared | No |
| `direction-badge` | shared | No |
| `status-badge` | shared | No |
| `entity-link` | shared | No |
| `detail-skeleton` | shared | No |
| `breadcrumbs` | shared | No |
| `time-display` | shared | No |
| `handler-reference` | shared | No |
| `audit-trail` | shared | No |
| `account-summary-card` | accounts | Yes (visible fields) |
| `recent-payments` | payments | Yes (row count) |
| `cel-editor` | sagas | No |
| `starlark-editor` | sagas | No |
| `saga-timeline` | sagas | No |
| `quality-ladder-badge` | positions | No |

## Implementation Phases

### Phase 1: Schema and Directory Structure

- Define `registry-item.json` JSON Schema supporting
  both `registry:ui` and `registry:pattern` types
- Define `registry.json` JSON Schema for the unified
  index
- Create `cookbook/` directory structure with `ui/`,
  `patterns/`, `schema/`, and `docs/` subdirectories
- Create the initial `registry.json` index file

### Phase 2: Populate UI Component Entries

- Create `component.json` metadata for each shared
  component listed in the UI inventory
- Declare `feature_module`, `tenant_configurable`,
  `configurable_props`, and `used_by` for each
- Validate that `registryDependencies` between
  components are accurate
- Cross-reference with PRD-034's component registry
  section for consistency

### Phase 3: Migrate Economy Patterns

- Extract manifest fragments from existing example
  manifests in `api/proto/.../examples/`
- Extract saga scripts from `tenant-saga-examples/`
- Create `pattern.json` metadata for each, declaring
  provides/requires/composes_with/conflicts_with
- Validate all fragments with `meridian_manifest_validate`
- Cross-reference with
  `docs/guides/manifest-design-patterns.md` to ensure
  coverage

### Phase 4: MCP Discovery Tools

- Implement `meridian_cookbook_list` (reads registry.json,
  supports type/category/industry filtering)
- Implement `meridian_cookbook_describe` (reads individual
  entry files, returns full detail with associated files)
- Implement `meridian_cookbook_discover` (inspects current
  manifest and UI config, matches against
  provides/requires, returns compatible entries with
  HATEOAS links)

### Phase 5: Authoring Guides and CI Validation

- Write `cookbook/docs/authoring-patterns.md` guide for
  economy patterns
- Write `cookbook/docs/authoring-components.md` guide for
  UI component entries
- Add CI validation: all entry files conform to schema,
  all manifest fragments pass validation, all saga
  scripts pass Starlark syntax validation
- Add test: composition of all `composes_with` pairs
  produces valid manifests

## Open Questions

1. **Pattern granularity**: Should `energy-settlement`
   include GBP as a provided instrument, or should there
   be a separate `base-fiat-gbp` pattern that energy
   depends on? Finer granularity enables more composition
   but increases the number of patterns.

2. **Versioning**: Should entries have versions? A pattern
   may evolve as the platform adds capabilities. Options:
   - Unversioned (entries always reflect current platform)
   - Semver (breaking changes get major version bumps)
   - Platform version pinning (entry declares minimum
     platform version)

3. **Manifest fragment format**: Should fragments be YAML
   (human-readable, matches manifest files) or JSON
   (consistent with registry format, easier to merge
   programmatically)?

4. **Discovery implementation**: Should
   `meridian_cookbook_discover` live in the MCP server
   (Go, alongside existing manifest tools) or as a
   separate lightweight service? The logic is simple set
   intersection - probably belongs in the existing MCP
   server.

5. **Community patterns**: Should the registry support
   third-party pattern sources (like shadcn's namespace
   system)? This enables an ecosystem but adds trust and
   validation complexity.

6. **UI component auto-generation**: Should
   `component.json` files be generated from TypeScript
   source (parsing exports and prop types) or maintained
   manually? Auto-generation ensures accuracy but adds
   build tooling. Manual authoring is simpler but risks
   drift.

## Success Criteria

1. A single `cookbook/registry.json` index contains both
   UI components and economy patterns
2. All existing example manifests and saga scripts are
   represented as cookbook patterns
3. All shared and feature UI components have structured
   `component.json` metadata
4. Each economy pattern declares its
   provides/requires/composes_with metadata
5. `meridian_cookbook_list` returns entries filtered by
   type, category, or industry
6. `meridian_cookbook_describe` returns full entry detail
   including associated files
7. `meridian_cookbook_discover` returns compatible entries
   given a current tenant state
8. Composing any declared `composes_with` pair produces a
   manifest that passes `meridian_manifest_validate`
9. CI validates all entry schemas, manifest fragments,
   and saga syntax
