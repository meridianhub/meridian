# PRD: Economy Cookbook - AI-Navigable Business Pattern Registry

## Problem Statement

Meridian has accumulated a rich set of economy
configuration patterns across multiple industries: energy
settlement, SaaS billing, carbon credit tracking, precious
metals, betting distribution. These patterns exist as
example manifests, saga scripts, and a design patterns
guide - but they are scattered across documentation,
test fixtures, and proto example directories.

This creates three problems:

1. **Discovery**: An AI assistant configuring a new tenant
   economy must search across multiple directories and file
   formats to find relevant patterns. There is no single
   index of what's available.
2. **Composition**: Patterns that work well together (energy
   settlement + carbon offset) have no formal declaration of
   compatibility. An AI must infer composition rules from
   reading documentation.
3. **Navigation**: Given a tenant's current economy state,
   there is no way to ask "what can I add next?" The AI must
   reason about the full pattern space rather than following
   available transitions.

## Vision

An **Economy Cookbook** - a structured, AI-navigable registry
of composable business configuration patterns. Following the
shadcn/ui registry model for static discoverability, and
HATEOAS principles for state-aware navigation.

The cookbook is to Meridian manifests what shadcn/ui is to
React components: a catalogue of building blocks that AI
assistants and developers can browse, compose, and apply -
without parsing unstructured documentation and without
guessing what works together.

### Two Modes of Operation

**Offline (static registry browsing)**: The registry layer
is static JSON files. AI assistants can read `registry.json`
and individual `pattern.json` files directly from the
filesystem or a CDN. No running server needed. This covers
browsing, reading pattern metadata, and manually composing
manifest fragments.

**Online (state-aware HATEOAS discovery)**: The
`meridian_cookbook_discover` MCP tool inspects a tenant's
current manifest and returns compatible patterns with
reasons. This requires the MCP server to be running (same
as existing `meridian_manifest_validate`). Discovery adds
intelligence on top of the static registry - it is not
required for basic pattern browsing.

### Three Layers

| Layer | Model | Requires server? |
|-------|-------|------------------|
| **Registry** | shadcn/ui | No - static JSON files |
| **Discovery** | HATEOAS | Yes - MCP server computes compatibility |
| **Execution** | Existing MCP | Yes - validate, plan, apply |

## Goals

1. Structure existing economy patterns (manifests, sagas,
   design patterns) into a machine-readable registry format
2. Declare composability between patterns (what works
   together, what conflicts)
3. Provide state-aware discovery: given a tenant's current
   manifest, surface compatible patterns with reasons
4. Expose the cookbook through MCP tools for AI-assisted
   economy configuration
5. Maintain the cookbook as living documentation that evolves
   with the platform

## Non-Goals

- Replacing the manifest apply workflow (the cookbook
  produces manifests that go through the existing
  validate/plan/apply pipeline)
- Building a visual economy designer UI (the cookbook serves
  AI and CLI consumers; a UI could consume it later)
- Auto-generating saga scripts from natural language (the
  cookbook provides tested reference scripts, not generated
  ones)
- Tenant-specific pattern storage (the cookbook is platform
  content, not per-tenant)

## Architecture

### Pattern Format

Each pattern is a JSON file following the shadcn/ui
`registry-item.json` schema, extended with economy-specific
metadata. Patterns live in `cookbook/patterns/`.

```json
{
  "$schema": "https://cookbook.meridian.dev/schema/pattern.json",
  "name": "energy-settlement",
  "type": "registry:pattern",
  "title": "Energy Usage-to-Value Settlement",
  "description": "Converts metered kWh consumption into monetary value using market rates.",
  "categories": ["energy", "utilities", "cross-instrument"],
  "meta": {
    "complexity": 3,
    "design_pattern": "cross-instrument-valuation",
    "industries": ["energy", "utilities"],
    "provides": {
      "instruments": ["KWH"],
      "account_types": ["energy_metered", "energy_revenue_retail", "energy_revenue_wholesale"],
      "sagas": ["usage_to_value"],
      "valuation_rules": ["kwh_to_gbp_retail", "kwh_to_gbp_wholesale"],
      "triggers": ["event:position-keeping.transaction-captured.v1"]
    },
    "requires": {
      "instruments": ["GBP"],
      "market_data": ["KWH_GBP_RETAIL", "KWH_GBP_WHOLESALE"]
    },
    "composes_with": ["carbon-offset", "time-of-use-pricing", "payment-gateway-stripe"],
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

### Pattern Metadata Schema

The `meta` object carries economy-specific semantics:

| Field | Type | Purpose |
|-------|------|---------|
| `complexity` | int (1-8) | Story points estimate for implementation |
| `design_pattern` | string | Reference to design patterns guide |
| `industries` | string[] | Industry tags for filtering |
| `provides.instruments` | string[] | Instrument codes this pattern defines |
| `provides.account_types` | string[] | Account type codes this pattern defines |
| `provides.sagas` | string[] | Saga names this pattern includes |
| `provides.valuation_rules` | string[] | Valuation rules this pattern defines |
| `provides.triggers` | string[] | Event triggers wired |
| `requires.instruments` | string[] | Instruments that must exist |
| `requires.market_data` | string[] | Market data datasets required |
| `composes_with` | string[] | Patterns known to work together |
| `conflicts_with` | string[] | Patterns that are mutually exclusive |
| `extends` | string[] | Patterns this one builds upon |

### Registry Index

`cookbook/registry.json` lists all patterns:

```json
{
  "$schema": "https://cookbook.meridian.dev/schema/registry.json",
  "name": "meridian-economy-cookbook",
  "homepage": "https://github.com/meridianhub/meridian",
  "items": [
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
    },
    {
      "name": "carbon-offset",
      "type": "registry:pattern",
      "title": "Carbon Credit Tracking and Retirement",
      "categories": ["carbon", "esg"]
    }
  ]
}
```

### Directory Structure

```text
cookbook/
├── registry.json                    # Pattern index
├── schema/
│   ├── pattern.json                 # JSON Schema for pattern entries
│   └── registry.json                # JSON Schema for registry index
├── patterns/
│   ├── base-fiat-gbp/
│   │   ├── pattern.json             # Pattern metadata
│   │   └── manifest-fragment.yaml   # Manifest fragment
│   ├── energy-settlement/
│   │   ├── pattern.json
│   │   ├── manifest-fragment.yaml
│   │   └── usage_to_value.star
│   ├── time-of-use-pricing/
│   │   ├── pattern.json
│   │   ├── manifest-fragment.yaml
│   │   └── tou_energy_valuation.star
│   ├── saas-billing/
│   │   ├── pattern.json
│   │   ├── manifest-fragment.yaml
│   │   └── compute_billing.star
│   ├── carbon-offset/
│   │   ├── pattern.json
│   │   ├── manifest-fragment.yaml
│   │   └── carbon_retirement.star
│   ├── precious-metals/
│   │   ├── pattern.json
│   │   ├── manifest-fragment.yaml
│   │   └── valuation_on_capture.star
│   ├── entity-distribution/
│   │   ├── pattern.json
│   │   ├── manifest-fragment.yaml
│   │   └── race_result_distribution.star
│   ├── dynamic-capacity-pricing/
│   │   ├── pattern.json
│   │   ├── manifest-fragment.yaml
│   │   └── dynamic_capacity_billing.star
│   ├── payment-gateway-stripe/
│   │   ├── pattern.json
│   │   └── manifest-fragment.yaml
│   └── kyc-compliance/
│       ├── pattern.json
│       ├── manifest-fragment.yaml
│       └── kyc_on_party.star
├── ...                              # Additional patterns added over time
└── docs/
    └── authoring-patterns.md        # Guide for adding new patterns
```

Note: the directory listing above is illustrative, not
exhaustive. See the Initial Pattern Inventory section for
the full list of patterns to migrate.

### HATEOAS Discovery

The discovery layer inspects a tenant's current manifest
and returns compatible patterns with navigation links.
This is the key difference from a static catalogue: the
response is state-aware.

**MCP tool: `meridian_cookbook_discover`**

Input: current manifest (or tenant_id to fetch it)

Output:

```json
{
  "economy_state": {
    "instruments": ["GBP", "KWH"],
    "account_types": ["energy_metered", "energy_revenue_retail"],
    "sagas": ["usage_to_value"],
    "market_data": ["KWH_GBP_RETAIL"]
  },
  "compatible_patterns": [
    {
      "name": "carbon-offset",
      "rel": "extend",
      "title": "Add Carbon Credit Tracking",
      "reason": "You have KWH instruments - most energy tenants pair this with carbon accounting for ESG compliance",
      "complexity": 3,
      "_links": {
        "detail": "/cookbook/patterns/carbon-offset",
        "compose": "/cookbook/compose?patterns=carbon-offset"
      }
    },
    {
      "name": "time-of-use-pricing",
      "rel": "enhance",
      "title": "Add Time-of-Use Rate Curves",
      "reason": "Your usage_to_value saga uses flat rates - this adds temporal pricing from forecast curves",
      "complexity": 2,
      "_links": {
        "detail": "/cookbook/patterns/time-of-use-pricing",
        "compose": "/cookbook/compose?patterns=time-of-use-pricing"
      }
    },
    {
      "name": "payment-gateway-stripe",
      "rel": "connect",
      "title": "Connect Stripe for Collections",
      "reason": "You have GBP revenue accounts but no payment rail configured",
      "complexity": 5,
      "_links": {
        "detail": "/cookbook/patterns/payment-gateway-stripe",
        "compose": "/cookbook/compose?patterns=payment-gateway-stripe"
      }
    }
  ],
  "conflicting_patterns": [
    {
      "name": "flat-rate-billing",
      "reason": "Conflicts with existing usage_to_value saga which uses cross-instrument valuation"
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
| `meridian_cookbook_list` | Registry | Browse all patterns, filter by category/industry |
| `meridian_cookbook_describe` | Registry | Get full pattern detail with manifest fragment and saga files |
| `meridian_cookbook_discover` | Discovery | Given current manifest, return compatible patterns with reasons |

**Composition is done client-side.** The AI reads pattern
manifest fragments and merges them into a complete manifest.
The existing `meridian_manifest_validate` tool catches any
conflicts or missing dependencies. This keeps the cookbook
stateless and the validation authoritative.

**Merge semantics**: Manifest fragments are merged using
these deterministic rules:

- **Lists** (instruments, account_types, sagas): append in
  pattern dependency order, deduplicate by `code`/`name`
  field (first occurrence wins)
- **Scalars** (metadata.name, metadata.description): later
  pattern overrides earlier
- **Nested objects** (valuation_rules, seed_data): recursive
  merge, with later pattern values overriding on key
  collision
- **Conflict detection**: if two fragments define the same
  `code`/`name` with different content,
  `meridian_manifest_validate` rejects with a descriptive
  error listing both sources

The merge order is deterministic: resolve
`registryDependencies` depth-first, then append the
requested patterns in the order specified by the user. The
resulting manifest is passed to `meridian_manifest_validate`
which is the authoritative arbiter of correctness.

**Full conversation flow:**

```text
User: "We're an energy retailer billing residential customers"

AI: meridian_cookbook_list(category: "energy")
    → energy-settlement, time-of-use-pricing, carbon-offset

AI: meridian_cookbook_describe("energy-settlement")
    → full pattern with manifest fragment + saga script

AI: Merges energy-settlement fragment into manifest

AI: meridian_cookbook_discover(current_manifest)
    → compatible: time-of-use-pricing, carbon-offset, payment-gateway-stripe
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
```

## Initial Pattern Inventory

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

## Implementation Phases

### Phase 1: Schema and Directory Structure

- Define `pattern.json` and `registry.json` JSON Schemas
- Create `cookbook/` directory structure
- Create the registry index file

### Phase 2: Migrate Existing Patterns

- Extract manifest fragments from existing example
  manifests in `api/proto/.../examples/`
- Extract saga scripts from `tenant-saga-examples/`
- Create pattern.json metadata for each, declaring
  provides/requires/composes_with/conflicts_with
- Validate all fragments with `meridian_manifest_validate`
- Cross-reference with `docs/guides/manifest-design-patterns.md`
  to ensure coverage

### Phase 3: MCP Discovery Tools

- Implement `meridian_cookbook_list` (reads registry.json,
  supports category/industry filtering)
- Implement `meridian_cookbook_describe` (reads individual
  pattern.json, returns full detail with file contents)
- Implement `meridian_cookbook_discover` (inspects current
  manifest, matches against pattern provides/requires,
  returns compatible patterns with HATEOAS links)

### Phase 4: Authoring Guide and Validation

- Write `cookbook/docs/authoring-patterns.md` guide for
  adding new patterns
- Add CI validation: all pattern.json files conform to
  schema, all manifest fragments pass validation, all
  saga scripts pass Starlark syntax validation
- Add test: composition of all `composes_with` pairs
  produces valid manifests

## Open Questions

1. **Pattern granularity**: Should `energy-settlement`
   include GBP as a provided instrument, or should there
   be a separate `base-fiat-gbp` pattern that energy
   depends on? Finer granularity enables more composition
   but increases the number of patterns.

2. **Versioning**: Should patterns have versions? A pattern
   may evolve as the platform adds capabilities. Options:
   - Unversioned (patterns always reflect current platform)
   - Semver (breaking changes get major version bumps)
   - Platform version pinning (pattern declares minimum
     platform version)

3. **Manifest fragment format**: Should fragments be YAML
   (human-readable, matches manifest files) or JSON
   (consistent with registry format, easier to merge
   programmatically)?

4. **Discovery implementation**: Should
   `meridian_cookbook_discover` live in the MCP server (Go,
   alongside existing manifest tools) or as a separate
   lightweight service? The logic is simple set
   intersection - probably belongs in the existing MCP
   server.

5. **Community patterns**: Should the registry support
   third-party pattern sources (like shadcn's namespace
   system)? This enables an ecosystem but adds trust and
   validation complexity.

## Success Criteria

1. All existing example manifests and saga scripts are
   represented as cookbook patterns
2. Each pattern declares its provides/requires/composes_with
   metadata
3. `meridian_cookbook_list` returns all patterns with
   category filtering
4. `meridian_cookbook_describe` returns full pattern detail
   including manifest fragment and saga files
5. `meridian_cookbook_discover` returns compatible patterns
   given a current manifest
6. Composing any declared `composes_with` pair produces a
   manifest that passes `meridian_manifest_validate`
7. CI validates all pattern schemas, manifest fragments,
   and saga syntax
