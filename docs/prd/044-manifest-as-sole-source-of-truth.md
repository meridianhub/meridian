# PRD 044: Manifest as Sole Source of Truth

## Problem Statement

Meridian currently has two paths to define an economy: the manifest
(version-controlled, declarative) and direct gRPC calls (imperative,
untracked). In practice, tenants like Volt Energy use both: the manifest
declares instruments and account types, but seed-demo then re-registers
them via gRPC because "the manifest executor is not wired yet." Market
data definitions, data sources, and organizational parties are created
entirely outside the manifest.

This dual-path architecture undermines Meridian's core value proposition:
**declare your economy in a manifest and the platform operates it**. If
half the economy lives in imperative gRPC calls, it isn't
version-controlled, isn't reproducible, and can't be diffed or audited
through the manifest pipeline.

## Current State

### What the manifest handles today

| Resource | Manifest Section | Executor Wired | Notes |
|----------|-----------------|---------------|-------|
| Instruments | `instruments[]` | Partially | Manifest stores definition, but seed-demo re-registers via `ReferenceDataService.RegisterInstrument` because executor doesn't activate |
| Account Types | `accountTypes[]` | Partially | Same issue - manifest stores, seed-demo activates via `AccountTypeRegistryService` |
| Valuation Rules | `valuationRules[]` | Yes | Mapped to `RegisterInstrument` in planner |
| Sagas | `sagas[]` | Yes | Full lifecycle support |
| Party Types | `partyTypes[]` | Yes | Schema-level definitions |
| Mappings | `mappings[]` | Yes | Bidirectional field mapping |
| Operational Gateway | `operationalGateway` | Yes | Provider connections + instruction routes |
| Payment Rails | `paymentRails[]` | No executor | Proto defined, no planner mapping |

### What is NOT in the manifest at all

| Resource | Current Path | Why It Matters |
|----------|-------------|---------------|
| **Market Data Set Definitions** | `MarketInformationService.RegisterDataSet` | Defines what market data a tenant tracks (WHOLESALE_ENERGY_GBP_KWH, USD_EUR_FX). These are structural, not operational. |
| **Market Data Sources** | `MarketInformationService.RegisterDataSource` | Defines where data comes from (NORDPOOL, BLOOMBERG). Structural reference data. |
| **Organizational Parties** | `PartyService.RegisterParty` | DNOs, grid supply points, clearinghouses - structural entities that define the economy's topology. |
| **Internal Accounts** | `InternalAccountService.InitiateAccount` | Inventory accounts, settlement accounts - the chart of accounts. |

### What should STAY outside the manifest

| Resource | Why | Path |
|----------|-----|------|
| **Customer parties** | Runtime data - customers sign up dynamically | gRPC / API |
| **Customer accounts** | Runtime data - opened per customer | gRPC / API |
| **Market data observations** | Operational data - prices arrive continuously | gRPC / feeds |
| **Transactions / ledger entries** | Operational data | Saga execution |
| **Account balances** | Derived from transactions | Read-only queries |

## Desired State

The manifest becomes the **sole source of truth** for everything that defines an economy's structure. The distinction is:

- **Manifest (structural)**: What *types* of things exist, how they relate, what rules govern them
- **Runtime (operational)**: Individual *instances* of those types, created by users/systems/sagas

### New manifest sections

```json
{
  "version": "1.1",
  "metadata": { ... },
  "instruments": [ ... ],
  "accountTypes": [ ... ],
  "valuationRules": [ ... ],
  "sagas": [ ... ],
  "partyTypes": [ ... ],
  "mappings": [ ... ],
  "operationalGateway": { ... },
  "paymentRails": [ ... ],

  "marketData": {
    "sources": [
      {
        "code": "NORDPOOL",
        "name": "Nord Pool Spot Market",
        "description": "Nordic/Baltic electricity exchange",
        "trustLevel": 90
      },
      {
        "code": "INTERNAL_TARIFF",
        "name": "Internal Tariff Engine",
        "description": "Internally computed retail tariffs",
        "trustLevel": 100
      }
    ],
    "dataSets": [
      {
        "code": "WHOLESALE_ENERGY_GBP_KWH",
        "category": "DATA_CATEGORY_ENERGY_PRICE",
        "unit": "GBP/kWh",
        "displayName": "Wholesale Energy Price (GBP per kWh)",
        "description": "Day-ahead wholesale electricity price",
        "resolutionKeyExpression": "'spot'",
        "validationExpression": "value > 0 && value < 10.0",
        "sourceCode": "NORDPOOL"
      }
    ]
  },

  "organizations": [
    {
      "code": "UK_POWER_NETWORKS",
      "name": "UK Power Networks",
      "partyType": "DISTRIBUTION_NETWORK_OPERATOR",
      "attributes": {
        "region": "South East England",
        "license_area": "SPN"
      }
    },
    {
      "code": "GSP_SOUTH_EAST",
      "name": "Grid Supply Point - South East",
      "partyType": "GRID_SUPPLY_POINT",
      "attributes": {
        "gsp_group": "_A",
        "parent_dno": "UK_POWER_NETWORKS"
      }
    }
  ],

  "internalAccounts": [
    {
      "code": "GSP_SOUTH_EAST_KWH_INVENTORY",
      "accountType": "INVENTORY_KWH",
      "ownerOrganization": "GSP_SOUTH_EAST",
      "instrument": "KWH",
      "description": "KWH inventory for South East GSP"
    },
    {
      "code": "SETTLEMENT_GBP",
      "accountType": "SETTLEMENT",
      "instrument": "GBP",
      "description": "Central GBP settlement account"
    }
  ]
}
```

### Execution phases (updated planner)

```text
Phase 1: Instruments          (register + activate)
Phase 2: Account Types        (draft + activate)
Phase 3: Valuation Rules
Phase 4: Market Data Sources  (register)
Phase 5: Market Data Sets     (register + activate)
Phase 6: Party Types          (schema definitions)
Phase 7: Organizations        (register structural parties)
Phase 8: Internal Accounts    (initiate)
Phase 9: Sagas                (create + activate)
Phase 10: Mappings
Phase 11: Operational Gateway
Phase 12: Payment Rails
```

## Requirements

### R1: Market Data Definitions in Manifest

Add `marketData` section to the Manifest proto with:

- `sources[]` - Data source definitions (code, name, trust level)
- `dataSets[]` - Data set definitions (code, category, unit, CEL expressions)

**Executor behavior**: On `ApplyManifest`, register data sources first,
then register and activate data sets. Diff against existing state -
only create/update what changed.

**What stays runtime**: Actual price observations (`RecordObservation`)
remain gRPC-only. The manifest declares *what* you track, not
*what the prices are*.

### R2: Organizational Parties in Manifest

Add `organizations[]` section for structural/institutional parties that define the economy's topology.

**Distinction from customer parties**: Organizations are part of the
economy's structure (a DNO, a clearinghouse, a grid supply point).
They exist before any customer signs up. Customer parties are
runtime data.

**Executor behavior**: Register parties via
`PartyService.RegisterParty` during manifest apply. Support UPDATE
for attribute changes. No DELETE (organizations can be deactivated
but not removed).

### R3: Internal Accounts (Chart of Accounts) in Manifest

Add `internalAccounts[]` section for the structural chart of accounts -
inventory accounts, settlement accounts, clearing accounts.

**Distinction from customer accounts**: Internal accounts are the
economy's plumbing. Customer accounts are opened dynamically per
customer.

**Executor behavior**: Create via
`InternalAccountService.InitiateAccount`. Support idempotent
re-creation. Reference organizations by code.

### R4: Fix Instrument and Account Type Executor Wiring

The manifest planner already maps instruments and account types to
gRPC methods, but the executor doesn't complete the full lifecycle
(draft -> active). Fix this so `ApplyManifest` fully activates
instruments and account types without needing seed-demo workarounds.

### R5: Payment Rails Executor

Payment rails are defined in the manifest proto but have no planner
mapping. Add the mapping so `ApplyManifest` processes them.

### R6: Manifest Version Bump

Bump manifest schema version to `1.1` to signal the expanded schema.
Maintain backwards compatibility - `1.0` manifests without the new
sections continue to work.

### R7: Update seed-demo to Manifest-Only

Rewrite `cmd/seed-demo/main.go` to:

1. Apply a single manifest that contains everything (instruments, account types, market data, organizations, internal accounts)
2. Use gRPC only for runtime operational data (customer parties, customer accounts, meter reads, price observations)

This serves as both validation and documentation of the manifest-first approach.

### R8: Manifest Export and Drift Reconciliation

Add `ExportManifest` RPC to the control plane that reconstructs a
manifest from the current tenant state. This serves two purposes:

**Export**: Generates a manifest from live state, enabling:

- Migrating existing tenants to manifest-first
- Backing up tenant configuration

**Reconciliation**: Compare the last-applied manifest against
live state to detect drift. Any resource that was created or
modified outside the manifest pipeline is flagged.

- `ReconcileManifest` RPC returns a drift report: resources
  present in live state but absent from manifest, resources
  whose live state differs from manifest-declared state
- Operators can choose to either update the manifest to match
  reality or re-apply the manifest to force convergence
- Drift detection runs on-demand (via RPC) and optionally on
  a schedule (e.g., daily reconciliation saga)

This is the enforcement mechanism for R9 below. Even if a direct
gRPC call somehow bypasses the write guard, reconciliation
catches it.

### R9: Manifest-Only Write Path (Economy Lockdown)

**All structural changes to a tenant's economy MUST go through
the manifest.** Direct gRPC mutations to structural resources
are rejected at the service layer when a manifest is active.

This means:

- `RegisterInstrument`, `CreateDraft` (account types),
  `RegisterDataSet`, `RegisterDataSource`,
  `RegisterParty` (organizational), `InitiateAccount` (internal),
  `CreateSagaDraft`, `CreateMapping`,
  `UpsertProviderConnection`, `UpsertInstructionRoute`
  all return `FAILED_PRECONDITION` with message
  "resource managed by manifest - update the manifest and
  re-apply" when called directly for a manifest-managed tenant.

- Runtime/operational endpoints remain open:
  `RecordObservation`, `RegisterParty` (customer type),
  `InitiateAccount` (customer accounts), saga execution, etc.

- Every `ApplyManifest` call increments the manifest version.
  The version history provides a full audit trail of every
  structural change to the economy.

**Implementation approach**:

- Each structural service checks a `manifest_managed` flag on
  the tenant. If true, reject direct writes for resource types
  the manifest owns.
- The manifest executor bypasses this guard via an internal
  service credential / context flag.
- Operational gateway changes (new ingest/egress routes,
  provider connections) follow the same rule: modify the
  manifest's `operationalGateway` section, re-apply.

**Version control integration**: Because the manifest is a JSON
file checked into the repo, every economy change flows through
the normal PR/review/merge cycle. The git history IS the audit
trail. No structural change happens without a commit.

## Success Criteria

1. A tenant's entire structural economy can be defined in a single
   manifest file
2. `ApplyManifest` with that file creates a fully operational economy
   (no seed-demo workarounds needed)
3. Market data set definitions are version-controlled in the manifest
4. Organizational parties and internal accounts are version-controlled
   in the manifest
5. The Volt Energy demo runs with manifest-only structural setup
6. Existing manifests (v1.0) continue to work without modification
7. Direct gRPC calls to structural endpoints return
   `FAILED_PRECONDITION` for manifest-managed tenants
8. `ReconcileManifest` detects resources created outside the manifest
9. Every structural change is traceable to a manifest version
   (and therefore a git commit)

## Non-Goals

- Migrating runtime/operational data into the manifest (observations, customer accounts, transactions)
- Scheduled data ingestion configuration (future: operational gateway can handle this)
- Multi-manifest composition (single manifest per tenant for now)
- Manifest inheritance or templates (future PRD)

## Risks

| Risk | Mitigation |
|------|------------|
| Proto changes could break existing clients | Additive-only changes, version field gates parsing |
| Market data sets have complex lifecycle | Manifest apply targets ACTIVE; deprecation stays manual |
| Organization references create ordering deps | Planner already handles phase ordering |
| Large manifests become unwieldy | Future PRD for manifest composition / includes |
| Write guards break existing integrations | Feature-flagged rollout per tenant; `manifest_managed` opt-in |
| Emergency changes blocked by manifest flow | Escape hatch: admin `--force` flag with audit log entry |

## Implementation Notes

- The `differ` package already supports resource types and action types;
  extend with `ResourceMarketDataSource`, `ResourceMarketDataSet`,
  `ResourceOrganization`, `ResourceInternalAccount`
- The `planner` already maps resource types to phases and gRPC methods;
  extend the maps
- The executor dispatches planned calls to gRPC services; new resource
  types follow the same pattern
- Proto changes are additive (new repeated fields on Manifest message)
