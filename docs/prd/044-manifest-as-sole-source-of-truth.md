# PRD 044: Manifest as Sole Source of Truth

## Problem Statement

Meridian has two paths to define an economy: the manifest
(declarative) and direct gRPC calls (imperative). In practice,
tenants like Volt Energy use both: the manifest declares
instruments and account types, but seed-demo re-registers them
via gRPC because the manifest executor doesn't fully activate
them. Market data definitions, data sources, and organizational
parties are created entirely outside the manifest.

This means economy state is scattered across services with no
unified versioning, no diffs, and no audit trail. A tenant
cannot answer "what changed in my economy last week?" or
"roll back to yesterday's configuration."

**The goal**: One declarative control plane that owns all
structural economy state, versions every change, supports
both full-manifest and granular resource mutations, and
enables AI/cookbook-driven economy composition.

## Architecture: Control Plane Owns Declarations, Services Own Runtime

### The separation

```text
┌─────────────────────────────────────────────┐
│              Control Plane                   │
│                                              │
│  Owns: All structural resource declarations  │
│  Stores: Composite manifest (versioned in DB)│
│  Validates: Cross-service references         │
│  Executes: Phased dispatch to services       │
│                                              │
│  Public APIs:                                │
│    ApplyManifest  (full economy)             │
│    ApplyResource  (single resource change)   │
│    GetManifest    (current composite state)  │
│    DiffManifest   (compare versions)         │
│    ListVersions   (audit trail)              │
│                                              │
├──────────┬──────────┬──────────┬─────────────┤
│ ref-data │ market   │ party    │ saga        │
│          │ info     │          │             │
│ Owns:    │ Owns:    │ Owns:    │ Owns:       │
│ runtime  │ runtime  │ runtime  │ runtime     │
│ state    │ state    │ state    │ state       │
│          │          │          │             │
│ Public:  │ Public:  │ Public:  │ Public:     │
│ read/    │ read/    │ read/    │ read/       │
│ query    │ query +  │ query +  │ query +     │
│ only     │ observe  │ customer │ execute     │
│          │          │ parties  │             │
│ Internal:│ Internal:│ Internal:│ Internal:   │
│ register │ register │ register │ create/     │
│ activate │ activate │ org      │ activate    │
└──────────┴──────────┴──────────┴─────────────┘
```

**Control plane owns declarations** because an economy is a
coherent dependency graph. Instruments, account types, market
data, sagas form cross-service references that require
whole-graph validation:

- Account type `ENERGY_TRADING` references instruments
  `[GBP, KWH]` - must validate they exist
- Valuation rule `KWH→GBP` references two instruments and
  a market data source - must validate all three
- Saga `process_settlement` references account types and
  instruments - must validate the full chain

No single service can validate these. The control plane can.

**Services own runtime** - the actual registered instruments,
active account types, recorded observations, customer accounts,
running sagas. Services receive provisioning instructions from
the control plane executor via internal APIs.

### Structural vs operational

| Structural (control plane) | Operational (services) |
|---------------------------|----------------------|
| Instrument definitions | Registered instrument state |
| Account type definitions | Active account types, customer accounts |
| Market data set/source definitions | Price observations |
| Organizational parties (DNOs, GSPs) | Customer parties |
| Internal accounts (chart of accounts) | Account balances, transactions |
| Saga definitions | Saga executions |
| Valuation rules | Computed valuations |
| Mappings, operational gateway config | Dispatched instructions |
| Payment rail config | Payment transactions |

## Two Write Paths, One Source of Truth

### Path 1: ApplyManifest (full economy)

For provisioning new tenants, cookbook-generated economies,
or wholesale reconfiguration.

```text
Cookbook/AI/UI generates manifest
  → ApplyManifest(manifest)
  → Control plane diffs against current version
  → Validates full dependency graph
  → Persists new manifest version to DB
  → Executes changes in dependency order (phases)
  → Returns diff summary
```

### Path 2: ApplyResource (granular change)

For adding one account type, updating a saga, adding a
market data source. This is the shadcn-style "just add
this one thing" path.

```text
Tenant/AI/UI submits one resource change
  → ApplyResource(instrument_definition)
  → Control plane reads current manifest from DB
  → Patches the resource into the composite document
  → Validates full dependency graph (cross-service refs)
  → Computes diff (shown to caller if dry_run)
  → Persists new manifest version to DB
  → Executes only the affected phases
  → Returns diff summary
```

Both paths produce the same result: a new manifest version
in the DB with a computable diff. The composite manifest is
always the full picture regardless of how it was assembled.

Note: until `ApplyResource` ships (Phase 2), clients achieve
the same result via `GetManifest` → modify → `ApplyManifest`.
The same validation, diffing, and versioning pipeline runs
either way.

### Manifest versioning in DB

Every change (whether via `ApplyManifest` or `ApplyResource`)
creates a new version:

```text
manifest_versions table:
  tenant_id        — which tenant
  sequence_number  — monotonically increasing BIGINT (1, 2, 3...)
  manifest         — full composite JSON document
  diff             — what changed from previous version
  phase_status     — per-phase execution status (JSON)
  applied_by       — who/what made the change (user, AI, cookbook)
  applied_at       — timestamp
  apply_status     — APPLIED | PARTIAL | FAILED | ROLLED_BACK
  checksum         — SHA-256 of the manifest document
  source           — "apply_manifest" or "apply_resource"
  resource_path    — if apply_resource: "instruments/KWH"
```

**Concurrency control**: Writes use optimistic locking via
`sequence_number`. Every write includes
`WHERE sequence_number = $expected`. Concurrent writes fail
with a conflict error and the caller retries with the latest
version. This prevents the lost-update problem where two
concurrent changes silently overwrite each other.

**Partial execution**: The `phase_status` field records which
phases completed, which failed, and which were skipped. If
phase 7 fails after phases 1-6 succeeded, the manifest
version is persisted with `apply_status = PARTIAL` and the
phase-level detail. Callers can inspect what succeeded and
either fix-and-retry or rollback.

### Control plane RPCs

```text
ApplyManifest         — submit full manifest, diff + apply
ApplyResource         — submit single resource, patch + diff + apply
GetManifest           — current active manifest (full composite)
GetManifestVersion    — specific historical version
ListManifestVersions  — version history with metadata
DiffManifestVersions  — diff between any two versions
ExportManifest        — reconstruct manifest from live state
ReconcileManifest     — compare DB manifest vs live state
```

## Current Gaps

### Resources missing from the manifest

| Resource | Current Path | Action |
|----------|-------------|--------|
| Market data sets | `RegisterDataSet` | Add to manifest |
| Market data sources | `RegisterDataSource` | Add to manifest |
| Organizational parties | `RegisterParty` | Add to manifest |
| Internal accounts | `InitiateAccount` | Add to manifest |

### Executor gaps

| Resource | Issue | Action |
|----------|-------|--------|
| Instruments | Executor doesn't fully activate | Fix lifecycle |
| Account types | Executor doesn't fully activate | Fix lifecycle |
| Payment rails | Proto defined, no planner mapping | Wire executor |

## New Manifest Sections

### Market data

```json
{
  "marketData": {
    "sources": [
      {
        "code": "NORDPOOL",
        "name": "Nord Pool Spot Market",
        "description": "Nordic/Baltic electricity exchange",
        "trustLevel": 90
      }
    ],
    "dataSets": [
      {
        "code": "WHOLESALE_ENERGY_GBP_KWH",
        "category": "DATA_CATEGORY_ENERGY_PRICE",
        "unit": "GBP/kWh",
        "displayName": "Wholesale Energy Price",
        "resolutionKeyExpression": "'spot'",
        "validationExpression": "value > 0 && value < 10.0",
        "sourceCode": "NORDPOOL"
      }
    ]
  }
}
```

Observations (`RecordObservation`) remain operational - the
manifest declares *what* you track, not the prices themselves.

### Organizations

```json
{
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
  ]
}
```

Customer parties remain operational - organizations are the
economy's topology, customers are runtime data.

### Internal accounts (chart of accounts)

```json
{
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

Customer accounts remain operational - internal accounts are
the economy's plumbing.

## Execution Phases

The control plane executor dispatches changes in dependency
order. Each phase completes before the next begins:

```text
Phase 1:  Instruments          (register + activate)
Phase 2:  Account Types        (draft + activate)
Phase 3:  Valuation Rules
Phase 4:  Market Data Sources  (register)
Phase 5:  Market Data Sets     (register + activate)
Phase 6:  Party Types          (schema definitions)
Phase 7:  Organizations        (register structural parties)
Phase 8:  Internal Accounts    (initiate)
Phase 9:  Sagas                (create + activate)
Phase 10: Mappings
Phase 11: Operational Gateway
Phase 12: Payment Rails
```

`ApplyResource` skips phases that are unaffected by the change.

## Service API Surface

### Structural mutation APIs become internal-only

These RPCs are removed from the public gateway. The control
plane executor calls them on the internal service mesh:

- **reference-data**: `RegisterInstrument`, `UpdateInstrument`,
  `DeprecateInstrument`, `CreateDraft`, `ActivateAccountType`
- **market-information**: `RegisterDataSet`, `ActivateDataSet`,
  `DeprecateDataSet`, `RegisterDataSource`,
  `DeactivateDataSource`
- **party**: `RegisterParty` (organizational), `UpdateSchema`
- **internal-account**: `InitiateAccount` (structural)
- **saga**: `CreateSagaDraft`, `ActivateSaga`, `DeprecateSaga`
- **mapping**: `CreateMapping`, `UpdateMapping`,
  `DeprecateMapping`
- **operational-gateway**: `UpsertProviderConnection`,
  `UpsertInstructionRoute`

### Public APIs per service (read + operational)

Each service retains its own independent API surface for
reads and operational data. No single monolithic swagger -
the composite API is assembled from per-service specs:

- **reference-data**: Read/query instruments, account types
- **market-information**: `RecordObservation`,
  `RecordObservationBatch`, read/query observations and
  data sets
- **party**: `RegisterParty` (customer types), read/query
- **current-account**: All endpoints (customer accounts)
- **internal-account**: Read/query endpoints
- **saga**: Execution endpoints, read/query
- **position-keeping**: All endpoints (operational)
- **control-plane**: All manifest management RPCs

### Composite API specification

The platform API is assembled from per-service specs, not
maintained as a single monolithic document. Each service
publishes its own OpenAPI/AsyncAPI spec. The gateway
composes them under service-scoped paths:

```text
/v1/reference-data/...      → reference-data spec
/v1/market-information/...   → market-information spec
/v1/parties/...              → party spec
/v1/control-plane/...        → manifest management spec
/v1/accounts/...             → current-account spec
```

## AI and Cookbook Integration

### Economy creation (cookbook → ApplyManifest)

```text
AI: "What does your business do?"
  → Selects cookbook recipes (energy trading, carbon, etc.)
  → Composes recipes into a complete manifest
  → ApplyManifest(manifest, dry_run=true)
  → Shows diff to user: "This will create 3 instruments,
    2 account types, 1 market data source..."
  → User confirms
  → ApplyManifest(manifest, dry_run=false)
  → Economy provisioned
```

### Economy modification (AI → ApplyResource)

```text
AI: "Add a carbon credit instrument"
  → GetManifest (or ApplyResource when available)
  → Modifies instrument section
  → ApplyManifest(manifest, dry_run=true)
  → Validates: no broken references
  → Shows diff: "+1 instrument: CARBON_CREDIT"
  → User confirms
  → ApplyManifest(manifest, dry_run=false)
  → New manifest version (v4)
```

The AI modifies only the section it cares about and
resubmits. The control plane handles validation, diffing,
and versioning. When `ApplyResource` ships in Phase 2, this
becomes a single RPC without needing the full manifest.

### Structured validation errors

Both `ApplyManifest` and `ApplyResource` return structured
validation errors that humans and AI can act on:

```json
{
  "validationErrors": [
    {
      "path": "accountTypes[0].allowedInstruments[1]",
      "code": "REFERENCE_NOT_FOUND",
      "message": "Instrument 'CARBON' not found. Did you mean 'CARBON_CREDIT'?",
      "suggestion": "CARBON_CREDIT"
    },
    {
      "path": "marketData.dataSets[0].sourceCode",
      "code": "REFERENCE_NOT_FOUND",
      "message": "Data source 'NORDPOOL' not defined in marketData.sources",
      "suggestion": null
    },
    {
      "path": "sagas[2].script",
      "code": "STARLARK_SYNTAX_ERROR",
      "message": "Line 14: undefined: position_keepng (did you mean position_keeping?)",
      "line": 14,
      "suggestion": "position_keeping"
    }
  ]
}
```

Error categories:

- **REFERENCE_NOT_FOUND**: Cross-resource reference to
  something that doesn't exist (with fuzzy-match suggestions)
- **DUPLICATE_CODE**: Two resources with the same code
- **STARLARK_SYNTAX_ERROR**: Saga script compilation failure
  (with line number)
- **CEL_SYNTAX_ERROR**: Validation/bucketing expression error
- **INVALID_VALUE**: Field value fails proto validation
- **CIRCULAR_DEPENDENCY**: Resource dependency cycle detected

AI receives these errors, fixes the manifest/resource, and
resubmits. The dry_run flag lets AI validate before applying,
creating a tight feedback loop without side effects.

### Diff visibility

Every change is diffable:

```text
DiffManifestVersions(v3, v4):
  + instruments[2]: CARBON_CREDIT (COMMODITY, 4 decimals)

DiffManifestVersions(v1, v4):
  + instruments[2]: CARBON_CREDIT
  ~ accountTypes[0].allowedInstruments: [GBP, KWH] → [GBP, KWH, CARBON_CREDIT]
  + marketData.dataSets[1]: CARBON_PRICE_GBP
```

## Reconciliation and Export

**ReconcileManifest**: Compares the DB-stored manifest against
live resource state across services. Reports drift. With
structural APIs removed from the public surface, drift should
be impossible - reconciliation is a safety net.

**ExportManifest**: Reconstructs a manifest from live state.
Used to migrate existing tenants (set up via direct gRPC
before this feature) to the manifest-first model.

## Implementation Phases

### Phase 1: The Manifest Actually Works

Fix the existing pipeline so `ApplyManifest` provisions a
fully operational economy with no workarounds.

**Scope**:

1. Fix executor lifecycle for instruments (register +
   activate) and account types (draft + activate). This is
   the bug at seed-demo line 132: "manifest executor not
   wired yet."
2. Add 4 new resource types to manifest proto, differ,
   planner, and executor: market data sources, market data
   sets, organizations, internal accounts.
3. Add concurrency control to `manifest_versions`:
   `sequence_number BIGINT` with optimistic locking on write.
4. Deepen diff to field-level comparison. Current
   `CompareVersions` only checks top-level key
   additions/removals; must detect field-level changes
   within a resource (e.g., precision 2 → 4).
5. Expose `DiffManifestVersions` as public RPC. The
   internal `CompareVersions` logic already exists in
   `HistoryService`; wire it to a proto RPC.
6. Add partial execution tracking: `phase_status` and
   `apply_status` fields on manifest versions so callers
   can see what succeeded when a multi-phase apply fails.
7. Rewrite seed-demo to use only `ApplyManifest` for
   structural provisioning. Operational data (customer
   parties, accounts, observations) via service APIs.

**Success criteria**:

- `ApplyManifest` provisions Volt Energy from a single
  manifest document
- seed-demo has zero direct structural gRPC calls
- `DiffManifestVersions` shows field-level changes
- Concurrent `ApplyManifest` calls fail safely with a
  conflict error (no silent data loss)
- Partial execution failures are recorded with per-phase
  status

### Phase 2: Granular Mutations

Add `ApplyResource` for single-resource changes, enabling
AI/cookbook-driven granular economy modification.

**Scope**:

1. `ApplyResource` RPC: read current manifest from DB,
   patch single resource, validate full dependency graph,
   diff, execute affected phases only. Uses same
   differ/planner/executor pipeline as `ApplyManifest`.
2. Enhanced structured validation errors with fuzzy-match
   suggestions across all resource types.
3. `dry_run` mode improvements for both `ApplyManifest`
   and `ApplyResource` (preview diff without applying).
4. Wire payment rails into planner (proto defined, no
   executor mapping yet).

**Success criteria**:

- `ApplyResource` modifies a single resource with full
  validation, diff, and versioning
- AI can add an instrument via `ApplyResource` and receive
  structured errors if references are broken
- dry_run returns the exact diff that would be applied

### Phase 3: Governance

Lock down the structural mutation APIs and add tooling for
compliance, drift detection, and tenant migration.

**Scope**:

1. Structural mutation APIs become internal-only. Remove
   HTTP annotations from public gateway for all structural
   RPCs listed in "Service API Surface" section.
2. `ExportManifest` RPC: reconstruct a manifest from live
   service state. Used to bootstrap the first manifest
   version for existing tenants.
3. `ReconcileManifest` RPC: compare DB-stored manifest
   against live resource state across services. Report
   drift as structured output.
4. Migration tooling: deprecation warnings on direct
   structural API calls during transition period. Tenants
   run `ExportManifest` to generate their initial manifest,
   then switch to manifest-only.

**Success criteria**:

- Structural mutation RPCs are not accessible via public
  gateway
- `ReconcileManifest` confirms zero drift between DB
  manifest and live state
- Existing tenants can migrate via `ExportManifest` →
  `ApplyManifest`

## Success Criteria (All Phases)

1. `ApplyManifest` provisions a fully operational economy
   from a single document (no seed-demo workarounds)
2. `ApplyResource` modifies a single resource with full
   validation, diff, and versioning
3. Every structural change produces a new manifest version
   in the DB with computable diff
4. Structural mutation APIs are internal-only; not exposed
   on the public gateway
5. Market data definitions, organizations, and internal
   accounts are part of the manifest
6. AI/cookbook can create economies via `ApplyManifest` and
   modify them via `ApplyResource`
7. `ReconcileManifest` confirms zero drift
8. Each service retains its own independent API spec for
   reads and operational endpoints
9. `DiffManifestVersions` shows exactly what changed between
   any two versions
10. Existing v1.0 manifests continue to work

## Non-Goals

- Runtime/operational data in the manifest (observations,
  customer accounts, transactions)
- Manifest inheritance or overlay merging at runtime
- Scheduled data ingestion configuration
- Multi-manifest per tenant
- Event-sourced manifest storage (snapshot + diff is
  sufficient at this scale)
- Terraform-style plan/apply separation (`dry_run` provides
  the same preview capability)
- Runtime-state-aware `BREAKING_CHANGE` detection (requires
  cross-service runtime queries; defer to service-level
  rejection during execution)

## Risks

| Risk | Mitigation |
|------|------------|
| Cross-service validation complexity | Control plane already has the differ/planner; extend it |
| Concurrent manifest writes | Optimistic locking via `sequence_number` with CAS on write |
| Partial execution failure | Per-phase status tracking; `apply_status = PARTIAL` with detail |
| Large manifests degrade diff perf | Diff operates on structured sections, not raw JSON |
| Removing public APIs breaks integrations | Phase 3 deprecation period; `ExportManifest` for migration |
| Shallow diff misses field changes | Deepen `CompareVersions` to use `proto.Equal` per resource |

## Implementation Notes

- The differ, planner, executor, and history service already
  exist and are working. This PRD extends them, not replaces.
- `manifest_versions` table already exists with
  `HistoryService` providing store, retrieve, list, compare,
  rollback. Needs `sequence_number` column and `phase_status`.
- Extend `differ` with `ResourceMarketDataSource`,
  `ResourceMarketDataSet`, `ResourceOrganization`,
  `ResourceInternalAccount`
- Extend `planner` phase mapping and gRPC method mapping
- `ApplyResource` reuses the same differ/planner/executor
  pipeline as `ApplyManifest` - it just patches one resource
  into the current manifest first
- Proto changes are additive (new repeated fields on the
  Manifest message)
- Service structural mutation RPCs move from public to
  internal proto packages (or remove HTTP annotations)
- Reuse the existing `differ.ManifestDiffer` output as the
  user-facing diff rather than building a second diff engine
