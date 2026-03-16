# PRD 046: Economy Visualization Completeness

## Problem Statement

The Operations Console's economy visualization is incomplete and
partially broken. The economy graph only renders 4 of 12 manifest
resource types. Saga navigation is broken (404 on detail links,
empty list page). The 4 new resource types added in PRD 045
(market data sources/sets, organizations, internal accounts) don't
appear anywhere in the UI. Gateway mappings exist as a standalone
page but aren't connected to the manifest or economy graph. And
the new control-plane RPCs (ApplyResource, ExportManifest,
ReconcileManifest, DiffManifestVersions) have no frontend surface.

**The core user story**: A human designs an economy via AI
conversation (MCP). The AI drafts or applies a manifest. The human
needs to see the complete picture — every resource, every
relationship — to verify the AI did what they intended. Today the
graph shows instruments, account types, valuation rules, and sagas.
It's missing market data, organizations, internal accounts,
mappings, payment rails, and operational gateway config. The human
can't verify what they can't see.

## Current State

### What works

| Resource Type | In Graph | Detail Page | Manifest-Aware |
|---------------|----------|-------------|----------------|
| Instruments | Yes | /reference-data/instruments | Yes |
| Account Types | Yes | /reference-data/account-types | Yes |
| Valuation Rules | Yes | /reference-data/valuation-rules | Yes |
| Sagas | Yes | /starlark-config (broken) | Yes |

### What's broken

1. **Saga list page empty**: `/starlark-config` calls
   `sagaRegistry.listSagas()` which returns nothing. The economy
   overview reads sagas from the manifest via
   `manifestHistory.getCurrentManifest()`. Two different data
   sources, out of sync.

2. **Saga detail 404**: Economy graph links to
   `/starlark-config/{sagaName}` (e.g.,
   `energy_purchase_settlement`). The detail page calls
   `sagaRegistry.getSaga({ id: definitionId })` which expects a
   UUID, not a name. Always 404.

3. **Valuation rules have no click navigation**: Double-clicking a
   valuation rule node in the graph does nothing — no case in the
   navigation handler.

### What's missing from the graph

| Resource Type | In Manifest | In Graph | Detail Page |
|---------------|-------------|----------|-------------|
| Market Data Sources | Yes (PRD 045) | No | /market-data (not manifest-aware) |
| Market Data Sets | Yes (PRD 045) | No | /market-data (not manifest-aware) |
| Organizations | Yes (PRD 045) | No | /parties (not manifest-aware) |
| Internal Accounts | Yes (PRD 045) | No | /internal-accounts |
| Mappings | Yes | No | /gateway-mappings (not manifest-aware) |
| Operational Gateway | Yes | No | None |
| Payment Rails | Yes | No | None |
| Party Types | Yes | No | None |

### What's missing from the platform

| Capability | Backend RPC | Frontend Surface |
|------------|-------------|-----------------|
| Granular resource mutation | ApplyResource | None |
| Drift detection | ReconcileManifest | None |
| Manifest reconstruction | ExportManifest | None |
| Version comparison | DiffManifestVersions | Exists (client-side diff) — should use backend |
| Per-phase execution status | phase_status field | None |
| Optimistic locking feedback | sequence_number | None |

## Solution

### Phase 1: Fix Broken Pages

Fix the three broken navigation/data issues so existing
functionality works.

**1a. Saga list page**: Change data source from
`sagaRegistry.listSagas()` to
`manifestHistory.getCurrentManifest()` → extract `sagas[]`. This
aligns with how the economy overview already works. The saga
registry service may not have sagas if they were provisioned via
manifest (which is now the primary path per PRD 045).

**1b. Saga detail page**: Support lookup by both UUID and name.
When `definitionId` doesn't look like a UUID, resolve it by
fetching the current manifest and finding the saga by name. Update
the manifest graph's double-click handler to use a consistent
identifier.

**1c. Valuation rule navigation**: Add a `valuation_rule` case to
the graph's double-click handler, navigating to
`/reference-data/valuation-rules`.

### Phase 2: Complete the Economy Graph

Add the 8 missing resource types to the graph model, renderer,
and diff algorithm.

**Graph model changes** (`manifest-graph-model.ts`):

Add node types:
- `market_data_source` — leaf node (no edges to other types)
- `market_data_set` — edge to its source via `sourceCode`
- `organization` — leaf node (hierarchical via `parent_dno`
  attribute if present)
- `internal_account` — edges to account_type, instrument, and
  owner organization
- `mapping` — edges to target service/RPC
- `operational_gateway_connection` — leaf node (provider endpoint)
- `instruction_route` — edges to connection and mapping(s)
- `payment_rail` — leaf node (provider config)

Add edge types:
- `sourced_by` — market_data_set → market_data_source
- `typed_as` — internal_account → account_type
- `denominated_in` — internal_account → instrument
- `owned_by` — internal_account → organization
- `maps_to` — mapping → target service (conceptual)
- `routes_via` — instruction_route → connection
- `transforms_with` — instruction_route → mapping

**Graph renderer changes** (`manifest-graph.tsx`):

Add node components with distinct colors:
- Market Data: teal (`oklch(0.59 0.15 185)`)
- Organizations: amber (`oklch(0.75 0.15 70)`)
- Internal Accounts: cyan (`oklch(0.65 0.15 210)`)
- Mappings: rose (`oklch(0.65 0.15 350)`)
- Gateway/Routes: slate (`oklch(0.55 0.10 260)`)
- Payment Rails: emerald (`oklch(0.65 0.15 160)`)

Add double-click navigation for all new types:
- market_data_source/set → `/market-data`
- organization → `/parties`
- internal_account → `/internal-accounts`
- mapping → `/gateway-mappings`
- operational_gateway → new page or section
- payment_rail → new page or section

Add type visibility toggles in the filter sidebar.

**Diff algorithm changes** (`manifest-diff.ts`):

Extend `computeManifestDiff` to handle all new node/edge types
with the same added/removed/modified semantics.

**Economy overview changes** (`economy-overview-page.tsx`):

Add stat chips for all new resource types with counts and links.

### Phase 3: Connect Existing Pages to Manifest

The `/market-data`, `/parties`, `/gateway-mappings`, and
`/internal-accounts` pages exist but show data from their
respective services, not from the manifest. Add manifest context.

**3a. Market data page**: Show which datasets/sources are
manifest-declared vs ad-hoc. Badge or filter: "Manifest-managed"
vs "Runtime". Link back to economy graph.

**3b. Parties page**: Filter or section for manifest-declared
organizations (structural) vs customer parties (operational).

**3c. Gateway mappings page**: Show which mappings are declared
in the manifest. Show which instruction routes reference each
mapping. Badge: "Manifest-managed".

**3d. Internal accounts page**: Show manifest-declared accounts
vs runtime accounts. Show the account type and instrument
relationships from the manifest.

### Phase 4: Surface New Control-Plane RPCs

**4a. ApplyResource UI**: On each resource detail page (or via
the economy graph), add an "Edit Resource" action that opens a
form/editor for that single resource. Submits via
`ApplyResource` RPC. Shows structured validation errors with
fuzzy-match suggestions inline. Supports `dry_run` preview.

**4b. Reconcile dashboard**: New page or section under Economy
showing the output of `ReconcileManifest`. Drift items displayed
as a table: resource type, code, drift type (MISSING, MODIFIED,
EXTRA), with expand to show expected vs actual values. "Run
Reconciliation" button triggers the RPC.

**4c. Export manifest**: Button on economy overview: "Export from
Live State". Calls `ExportManifest`, shows the reconstructed
manifest in the YAML editor. User can diff against the current
stored manifest. Useful for tenants migrating to manifest-first.

**4d. Phase execution status**: On the manifest version history
table, add expandable row detail showing per-phase execution
status (COMPLETED, FAILED, SKIPPED) with timestamps and error
messages. Highlight PARTIAL status versions.

**4e. Optimistic locking UX**: When `ApplyManifest` returns
ABORTED (concurrent modification), show a conflict dialog:
"Another change was applied while you were editing. Current
version: N. Your base version: M." Options: "Reload and retry"
or "Force apply".

**4f. Backend diff for version comparison**: Replace the
client-side `computeManifestDiff` with the backend
`DiffManifestVersions` RPC in the history table's compare
feature. The backend diff is authoritative and handles all
resource types including the new ones.

## Economy Graph: Full Resource Map

```text
                    ┌─────────────────┐
                    │   Instruments    │
                    │  (GBP, KWH...)  │
                    └────────┬────────┘
          ┌─────────────────┼──────────────────┐
          │                 │                  │
          ▼                 ▼                  ▼
┌─────────────────┐ ┌──────────────┐  ┌───────────────┐
│  Account Types  │ │  Valuation   │  │  Market Data  │
│ (SETTLEMENT...) │ │    Rules     │  │   Sources     │
└────────┬────────┘ │ (KWH→GBP)   │  └───────┬───────┘
         │          └──────────────┘          │
         │                                    ▼
         │                           ┌───────────────┐
         │                           │  Market Data  │
         │                           │    Sets       │
         │                           └───────────────┘
         ▼
┌─────────────────┐      ┌──────────────────┐
│   Internal      │─────▶│  Organizations   │
│   Accounts      │      │ (DNOs, GSPs)     │
│ (GSP_KWH_INV)  │      └──────────────────┘
└─────────────────┘
         │
         ▼
┌─────────────────┐      ┌──────────────────┐
│     Sagas       │─────▶│    Mappings      │
│ (settlement...) │      │ (stripe_req...)  │
└─────────────────┘      └────────┬─────────┘
                                  │
                    ┌─────────────┼──────────────┐
                    ▼                            ▼
           ┌──────────────┐            ┌──────────────┐
           │  Instruction │            │   Provider   │
           │   Routes     │            │ Connections  │
           └──────────────┘            └──────────────┘
                    │
                    ▼
           ┌──────────────┐
           │ Payment Rails│
           └──────────────┘
```

## Success Criteria

1. Economy graph renders all 12 manifest resource types with
   correct relationships
2. Saga navigation works end-to-end (list, detail, graph click)
3. All resource types have click-through navigation from graph
   to their detail page
4. Existing pages (/market-data, /parties, /gateway-mappings)
   show manifest-managed vs runtime resources
5. ApplyResource available from resource detail pages with
   validation feedback
6. ReconcileManifest accessible from economy dashboard
7. Manifest version history shows per-phase execution status
8. Backend DiffManifestVersions used for version comparison

## Non-Goals

- Redesigning the economy graph layout algorithm (ELK works)
- Adding new backend RPCs (all exist from PRD 045)
- Manifest YAML editor changes (already functional)
- Real-time manifest change notifications (polling is sufficient)
- Mobile-responsive graph (desktop-only is acceptable)

## Risks

| Risk | Mitigation |
|------|------------|
| Graph becomes visually cluttered with 12 types | Type visibility toggles already exist; extend to new types. Default to showing core types (instruments, accounts, sagas) with others opt-in. |
| Performance with large manifests (many nodes) | ELK handles hundreds of nodes. Lazy-load edge detail on hover. |
| Proto types not generated for new fields | Run `buf generate` — types already exist from PRD 045 proto changes |
| Market data / parties pages show mixed data | Clear badge/filter distinguishing manifest-declared vs runtime |

## Implementation Notes

- Proto-generated TypeScript types for the 4 new resource types
  already exist in `api/gen/` from PRD 045's proto changes and
  `buf generate`.
- The `manifestHistory` and `manifestApplier` Connect RPC clients
  are already wired in `api/clients.ts`.
- The `ManifestGraph` component uses React Flow + ELK — adding
  node types follows the existing `InstrumentNode`,
  `AccountTypeNode` pattern.
- The graph model (`manifest-graph-model.ts`) extracts nodes from
  the manifest proto. Adding new types means adding extraction
  logic for the new repeated fields.
- The diff graph (`manifest-diff-graph.tsx`) reuses the same node
  components with status-based coloring.
- Phase 1 (bug fixes) and Phase 2 (graph completeness) can be
  developed in parallel with Phase 3 (page connections) and
  Phase 4 (new RPC surfaces).
