# PRD 044: Manifest as Sole Source of Truth

## Problem Statement

Meridian currently has two paths to define an economy: the manifest
(declarative, versioned in DB) and direct gRPC calls (imperative,
untracked). In practice, tenants like Volt Energy use both: the
manifest declares instruments and account types, but seed-demo then
re-registers them via gRPC because "the manifest executor is not
wired yet." Market data definitions, data sources, and organizational
parties are created entirely outside the manifest.

This dual-path architecture undermines Meridian's core value
proposition: **declare your economy in a manifest and the platform
operates it**. If half the economy lives in imperative gRPC calls,
it isn't versioned, isn't reproducible, and can't be diffed or
audited through the manifest pipeline. Tenants are created
dynamically - each tenant's economy must be fully defined and
versioned through their manifest, stored in the DB.

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

### R8: Manifest Persistence, Export, and Reconciliation

**The database is the sole source of truth for every tenant's
economy definition.** Tenants are created dynamically (via API,
UI, onboarding flows). Each tenant's manifest is submitted via
`ApplyManifest` and versioned in the DB. There is no assumption
of a git workflow - the platform owns the version history.

**Manifest version store**: Every `ApplyManifest` call persists
the full manifest document to a `manifest_versions` table:

- `tenant_id`, `version` (monotonically increasing),
  `manifest_document` (full JSON), `applied_by`,
  `applied_at`, `diff_summary`, `checksum` (SHA-256)
- The current active manifest is always queryable via
  `GetManifest` RPC
- Previous versions are retained for audit and rollback
- The diff between any two versions is computable via
  `DiffManifestVersions` RPC
- Rollback to a previous version via
  `ApplyManifest` with a prior version's document

**Manifest lifecycle**:

```text
Tenant created -> First ApplyManifest (v1) -> economy provisioned
             -> ApplyManifest (v2) -> diff computed, changes applied
             -> ApplyManifest (v3) -> ...
             -> GetManifest -> returns current active manifest
             -> ListManifestVersions -> full version history
             -> DiffManifestVersions(v2, v3) -> what changed
```

**Export** (`ExportManifest` RPC): Reconstructs a manifest from
the current live state. Used for:

- Migrating existing tenants that were set up via direct gRPC
  before this feature existed
- Bootstrapping the first manifest version for a tenant

**Reconciliation** (`ReconcileManifest` RPC): Compares the
last-applied manifest (from DB) against live resource state.
Reports any drift. This is a safety net, not the primary
enforcement (R9 removes the APIs that could cause drift).

### R9: Remove Public Structural Mutation APIs

**Don't guard the APIs - remove them from the public surface.**
If the endpoints don't exist publicly, drift is impossible by
construction rather than by runtime checks.

The following RPCs are removed from the public gateway / HTTP
routes and become **internal-only** (callable only by the
manifest executor via internal service mesh):

- `ReferenceDataService`: `RegisterInstrument`,
  `UpdateInstrument`, `DeprecateInstrument`
- `AccountTypeRegistryService`: `CreateDraft`,
  `ActivateAccountType`
- `MarketInformationService`: `RegisterDataSet`,
  `ActivateDataSet`, `DeprecateDataSet`,
  `RegisterDataSource`, `DeactivateDataSource`
- `PartyService`: `RegisterParty` (organizational types only),
  `UpdateSchema`
- `InternalAccountService`: `InitiateAccount` (structural)
- `SagaService`: `CreateSagaDraft`, `ActivateSaga`,
  `DeprecateSaga`
- `MappingService`: `CreateMapping`, `UpdateMapping`,
  `DeprecateMapping`
- `OperationalGatewayService`: `UpsertProviderConnection`,
  `UpsertInstructionRoute`

**What remains public** (runtime/operational endpoints):

- `MarketInformationService`: `RecordObservation`,
  `RecordObservationBatch`, `RetrieveObservation`,
  `ListObservations`, `RetrieveDataSet`, `ListDataSets`
  (read-only for data sets)
- `PartyService`: `RegisterParty` (customer types),
  all read/query endpoints
- `CurrentAccountService`: all endpoints (customer accounts)
- `InternalAccountService`: read/query endpoints
- All saga execution endpoints
- All read/query endpoints on every service

**Implementation approach**:

- Remove structural mutation routes from the gRPC gateway
  HTTP annotations and public proto surface
- Structural mutation RPCs become internal service methods
  callable only via the manifest executor's internal gRPC
  connection (no external exposure)
- The manifest executor calls them on the internal service
  mesh during `ApplyManifest`
- Operational gateway changes (new ingest/egress routes,
  provider connections) follow the same rule: update the
  manifest, re-apply

**Migration path**: Existing tenants using direct gRPC calls
run `ExportManifest` to generate their initial manifest, then
switch to manifest-only. A deprecation period warns on direct
calls before removal.

**How tenants update their economy**: Submit a new manifest via
`ApplyManifest`. The control plane diffs it against the current
version, computes the execution plan, and applies changes. The
DB stores every version with full audit trail. Tenants can
query their manifest history, diff versions, and rollback.

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
7. Structural mutation APIs are not exposed on the public gateway;
   only the manifest executor can call them internally
8. `ReconcileManifest` confirms zero drift between DB-stored
   manifest and live resource state
9. Every structural change is traceable to a manifest version
   stored in the DB with full audit metadata
10. Full manifest version history is queryable per tenant
    (`GetManifest`, `ListManifestVersions`,
    `DiffManifestVersions`)

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
| Removing public APIs breaks existing integrations | Deprecation period with warnings before removal; `ExportManifest` for migration |
| Emergency changes blocked by manifest flow | Escape hatch: admin `--force` flag on `ApplyManifest` with audit log entry |

## Implementation Notes

- The `differ` package already supports resource types and action types;
  extend with `ResourceMarketDataSource`, `ResourceMarketDataSet`,
  `ResourceOrganization`, `ResourceInternalAccount`
- The `planner` already maps resource types to phases and gRPC methods;
  extend the maps
- The executor dispatches planned calls to gRPC services; new resource
  types follow the same pattern
- Proto changes are additive (new repeated fields on Manifest message)
