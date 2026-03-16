# PRD 046: Economy Visualization Completeness

## Problem Statement

The Operations Console's economy visualization is incomplete and
partially broken. The economy graph renders 4 of 13 manifest
resource types. Saga navigation is broken (404 on detail links,
empty list page). The Economy Explorer page (`/economy/explore`)
is similarly incomplete — showing only instruments, account
types, sagas, and mappings out of 13 types. Gateway mappings
exist as a standalone page but aren't connected to the manifest
or economy graph. The new control-plane RPCs from PRD 045
(ApplyResource, ExportManifest, ReconcileManifest,
DiffManifestVersions) have no frontend surface.

**The core user story**: A human designs an economy via AI
conversation (MCP). The AI drafts or applies a manifest. The
human needs to see the complete picture — every resource, every
relationship — to verify the AI did what they intended. Today
the graph shows instruments, account types, valuation rules, and
sagas. It's missing market data, organizations, internal
accounts, mappings, payment rails, and operational gateway
config. The human can't verify what they can't see.

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

2. **Saga detail 404**: Economy graph navigates to
   `/starlark-config/{sagaName}` (e.g.,
   `energy_purchase_settlement`). The detail page calls
   `sagaRegistry.getSaga({ id: definitionId })` expecting a
   UUID. The manifest doesn't store saga UUIDs. An existing
   `getActiveSaga({ name })` hook resolves by name — use this.

3. **Valuation rules have no click navigation**: Double-clicking
   a valuation rule node in the graph does nothing — no case in
   the `onNodeDoubleClick` handler.

4. **Economy Explorer incomplete**: `/economy/explore` reads
   from the manifest but only extracts instruments, account
   types, sagas, and mappings. The Resources tab ignores market
   data, organizations, internal accounts, payment rails, party
   types, and operational gateway config.

### What's missing from the graph

| Resource Type | Manifest Field | In Graph | In Explorer |
|---------------|----------------|----------|-------------|
| Market Data Sources | `market_data.sources[]` (nested) | No | No |
| Market Data Sets | `market_data.datasets[]` (nested) | No | No |
| Organizations | `organizations[]` | No | No |
| Internal Accounts | `internal_accounts[]` | No | No |
| Mappings | `mappings[]` | No | Partial |
| Provider Connections | `operational_gateway.provider_connections[]` (nested) | No | No |
| Instruction Routes | `operational_gateway.instruction_routes[]` (nested) | No | No |
| Payment Rails | `payment_rails[]` | No | No |
| Party Types | `party_types[]` | No | No |

Note: `market_data` and `operational_gateway` are wrapper
messages containing sub-arrays, not flat repeated fields. The
graph model extraction must navigate this nesting.

### What's missing from the platform

| Capability | Backend RPC | Frontend Surface |
|------------|-------------|-----------------|
| Granular resource mutation | ApplyResource | None |
| Drift detection | ReconcileManifest | None |
| Manifest reconstruction | ExportManifest | None |
| Version comparison | DiffManifestVersions | Client-side only |
| Per-phase execution status | phase_status field | None (PARTIAL status unlabeled) |
| Optimistic locking feedback | sequence_number | None |

### Two visualization surfaces

The frontend has **two** economy visualization surfaces that
both need updating:

1. **Economy Overview** (`/economy`): Graph + stat chips +
   version history. The graph is the primary AI verification
   surface. Currently shows 4 types.

2. **Economy Explorer** (`/economy/explore`): Tab-based detailed
   browse with event channels, sagas, mappings, resources. Reads
   from the manifest. Currently shows 4 types in Resources tab.

These serve complementary roles:
- **Overview** = visual map + high-level summary (graph-first)
- **Explorer** = detailed browse by resource type (list-first)

Both must show all 13 resource types.

## Solution

### Phase 0: Prerequisites

Before any frontend work begins:

1. **Regenerate TypeScript types**: Run `buf generate api/proto`
   from repo root. The proto source has all new fields
   (manifest.proto fields 12-14: `market_data`,
   `organizations`, `internal_accounts`) and all new RPCs
   (`ApplyResource`, `DiffManifestVersions`, `ExportManifest`,
   `ReconcileManifest`), but the generated TypeScript in
   `frontend/src/api/gen/` is stale. Verify the new fields and
   RPC methods appear in the regenerated output.

2. **Backend: Add `force` to ApplyResourceRequest**: The
   `ApplyManifestRequest` has `force: bool` for bypassing
   breaking-change checks, but `ApplyResourceRequest` does not.
   Without it, deletions and breaking changes via single-resource
   UI are impossible. Add the field to the proto and regenerate.

### Phase 1: Fix Broken Pages

Ship independently — no dependencies on other phases.

**1a. Saga list page**: Change data source from
`sagaRegistry.listSagas()` to
`manifestHistory.getCurrentManifest()` → extract `sagas[]`.
Aligns with how the economy overview already works.

**1b. Saga detail page**: Use the existing `getActiveSaga({
name })` hook (at `use-sagas.ts:54-73`) when `definitionId`
doesn't look like a UUID. This resolves by name without
requiring a backend change. Update the manifest graph's
double-click handler to use a consistent approach.

**1c. Valuation rule navigation**: Add a `valuation_rule` case
to the graph's `onNodeDoubleClick` handler, navigating to
`/reference-data/valuation-rules`.

**1d. PARTIAL status label**: Add `PARTIAL` to the
`ManifestHistoryTable` status label map (currently only maps
APPLIED, FAILED, ROLLED_BACK).

### Phase 2: Complete the Economy Graph

#### 2a. Data-driven node type registry (prerequisite refactor)

Before adding new types, consolidate the 10+ scattered
`Record<ManifestNodeType, ...>` locations into a single
`NODE_TYPE_REGISTRY` configuration object. Currently, adding one
node type requires updates in:

- `ManifestNodeType` union (`manifest-graph-model.ts`)
- `ManifestInput` interface (remove — use proto type directly)
- `NODE_THEMES` record (`manifest-graph.tsx`)
- `LAYER_PRIORITY` record (`manifest-graph.tsx`)
- `LAYER_PRIORITY` duplicate (`manifest-diff-graph.tsx`)
- `nodeCountByType` initializer
- `visibleTypes` default set
- `nodeTypes` registration
- CSS variables (`index.css`, both light and dark themes)
- `execution-subgraph.tsx` theme map

Create a single registry that drives all of these. This makes
adding the 8 new types a one-place-per-type change and
eliminates the duplicate `LAYER_PRIORITY` between the main
graph and diff graph. TypeScript's `Record` exhaustiveness
checking is preserved.

#### 2b. Color palette (accessibility-first)

The existing 4 types use hues spaced ~55-70 degrees apart in
oklch. With 10 distinct hues needed, minimum 36-degree
separation is required. Proposed palette (verify with
colorblind simulator before implementing):

| Type Group | Hue | Existing? |
|------------|-----|-----------|
| Instruments | 250 (blue) | Existing |
| Account Types | 145 (green) | Existing |
| Valuation Rules | 85 (amber) | Existing |
| Sagas | 295 (purple) | Existing |
| Market Data | 180 (teal) | New |
| Organizations | 35 (orange) | New |
| Internal Accounts | 215 (steel blue) | New |
| Mappings | 340 (rose) | New |
| Gateway/Routes | 115 (lime) | New |
| Payment Rails | 325 (magenta) | New |

All colors need CSS custom properties for both light and dark
themes. Do not use inline oklch values.

#### 2c. Graph model extension

Add node types to `manifest-graph-model.ts`. Note the nested
extraction paths:

- `market_data_source` — from `manifest.marketData.sources[]`
- `market_data_set` — from `manifest.marketData.datasets[]`,
  edge to source via `sourceCode`
- `organization` — from `manifest.organizations[]`
- `internal_account` — from `manifest.internalAccounts[]`,
  edges to account_type, instrument, and owner organization
- `mapping` — from `manifest.mappings[]`
- `provider_connection` — from
  `manifest.operationalGateway.providerConnections[]`
- `instruction_route` — from
  `manifest.operationalGateway.instructionRoutes[]`, edges to
  connection and mapping(s) via `outboundMappingId`/
  `inboundMappingId`
- `payment_rail` — from `manifest.paymentRails[]`, use
  composite key `provider:account_id` as node ID (no `code`
  field exists; `provider` alone is not unique if multiple
  accounts exist for the same provider)
- `party_type` — from `manifest.partyTypes[]`

Edge types:
- `sourced_by` — market_data_set → market_data_source
- `typed_as` — internal_account → account_type
- `denominated_in` — internal_account → instrument
- `owned_by` — internal_account → organization
- `routes_via` — instruction_route → provider_connection
- `transforms_with` — instruction_route → mapping
- `defines_schema` — party_type → (no target — leaf node,
  but used as a schema definition for parties)

Note: `market_data_source`, `organization`, `payment_rail`,
and `party_type` are leaf nodes with no outbound edges. They
are included in the graph model for completeness (propagates
to all 22 graph consumers) but are **hidden by default** in
the graph view. They appear prominently in the Economy
Explorer's list-based tabs where lack of edges is natural.

#### 2d. Graph renderer and layout

Add node components following the existing `memo()` pattern
(`InstrumentNode`, `AccountTypeNode`, etc.).

**ELK layer priorities** (lower = higher in graph):
- Instruments: 50
- Market Data Sources: 45
- Market Data Sets: 40
- Account Types: 35
- Valuation Rules: 30
- Organizations: 25
- Internal Accounts: 20
- Sagas: 15
- Mappings: 10
- Provider Connections: 8
- Instruction Routes: 5
- Payment Rails: 3
- Party Types: 48 (near instruments — schema definitions)

**Default visibility**: Core types (instruments, account_types,
valuation_rules, sagas, internal_accounts) visible by default.
Infrastructure types (mappings, gateway, payment rails, party
types, market data) hidden by default — toggled on via filter.

**Grouped filter panel**: Group toggles into 6 categories
instead of 13 individual checkboxes:
- Financial Core (instruments, account types, valuation rules)
- Workflows (sagas)
- Market Data (sources, sets)
- Structure (organizations, internal accounts)
- Integration (mappings, gateway connections, routes)
- Config (payment rails, party types)

**Disconnected subgraph handling (DECISION)**: Market data
sources, organizations, payment rails, and party types are
leaf nodes with no outbound edges. Floating isolated boxes in
a relationship graph look broken, not intentional. Decision:
**leaf-only types are hidden by default in the graph view but
included in the graph model** (so all 22 consumers benefit).
They appear in the Economy Explorer's list-based tabs where
lack of edges is natural. Users can toggle them on in the
graph filter panel if they want the complete picture.

#### 2e. Economy overview stat chips

Keep stat chips to 4-6 key metrics (instruments, account types,
sagas, internal accounts, organizations, market data sets).
Don't add 13 chips — that's a wall of numbers. Link "View all"
to the Explorer page for the full breakdown.

#### 2f. Double-click navigation

Add navigation targets for all new types:
- market_data_source/set → `/market-data`
- organization → `/parties`
- internal_account → `/internal-accounts`
- mapping → `/gateway-mappings`
- provider_connection/instruction_route → Economy Explorer
  (Gateway tab)
- payment_rail → Economy Explorer (Config tab)
- party_type → Economy Explorer (Config tab)

#### 2g. Diff algorithm

The client-side `computeManifestDiff` in `manifest-diff.ts` is
already generic — it diffs by node ID and edge ID. New types
added to `buildManifestGraph` are automatically diffed. No
algorithm changes needed, just the model extension.

#### 2h. Event chain updates

Update `canShowEventChain` guard to include
`internal_account` nodes (they connect to instruments and
account types). Update `analyzeSagaOutputs` if saga scripts
reference new handler types.

#### 2i. Tests

- Node extraction tests for each new type
- Edge creation tests (especially nested
  operationalGateway → instructionRoutes → mappings)
- Filter toggle behavior with grouped categories
- Double-click navigation for each new type
- ELK layout stability with 40-60 nodes

### Phase 3: Complete Economy Explorer

Replace the original Phase 3 (manifest-managed badges on
service pages) with extending the Economy Explorer as the
unified manifest-first browse view.

**Rationale**: Adding manifest-managed badges to `/market-data`,
`/parties`, `/gateway-mappings` creates a dual-source-of-truth
problem. The manifest says resource X exists; the service might
disagree (apply in progress, drift, etc.). Badges pretend the
two sources agree, which they may not. The Explorer reads only
from the manifest — clean, consistent, no dual-source confusion.

Service pages remain as operational views (live service state).
Explorer is the manifest view (declared state).

**3a. Extend Resources tab**: Show all 13 manifest types with
expandable sections per type. Each section shows a table of
resources with key fields (code, name, status, relationships).

**3b. Add Gateway tab**: Show provider connections, instruction
routes, and their relationships to mappings. This replaces the
need for a separate gateway page in the economy context.

**3c. Add Config tab**: Show payment rails, party types, and
seed data summary.

**3d. Link from Overview**: The "View all" link from stat chips
navigates to the Explorer.

### Phase 4: Surface New Control-Plane RPCs

**4a. ApplyResource UI**: Add "Edit Resource" as a YAML editor
with `dry_run` preview — not 13 typed forms. The YAML editor
already exists. On submission, call `ApplyResource` RPC. Show
structured `ValidationError` responses with path, code, message,
and fuzzy-match suggestions inline. Typed forms per resource
type can follow in later PRDs.

**4b. Reconcile dashboard**: New section under Economy showing
`ReconcileManifest` output. Drift items as a DataTable:
resource_type, code, drift_type (MISSING, MODIFIED, EXTRA),
description. "Run Reconciliation" button with:
- Loading state during RPC (can take 5-30 seconds)
- Warning display when services are unreachable
- "No manifest applied" message for pre-045 tenants
- Auto-refresh option

**4c. Export manifest**: Button on economy overview: "Export from
Live State". Calls `ExportManifest`, shows reconstructed
manifest in YAML editor. Diff against stored manifest. Show
`section_sources` (which service provided each section) and
`warnings` for partial failures.

**4d. Phase execution status**: On manifest version history
table, add expandable row detail showing per-phase execution
status (COMPLETED, FAILED, SKIPPED) with timestamps and error
messages. The `phase_status` JSONB field is already stored on
every `ManifestVersion`.

**4e. Optimistic locking UX**: When `ApplyManifest` or
`ApplyResource` returns ABORTED (concurrent modification), show
a conflict dialog that includes the **diff between current and
base version** (via `DiffManifestVersions`). Options: "Reload
and merge" or "Cancel". Do NOT offer blind "Force apply" — show
what changed first.

**4f. Backend diff (tabular view)**: Add a tabular diff view
using the `DiffManifestVersions` RPC alongside the existing
visual graph diff. The backend returns `DiffAction[]`
(resource_type, action, resource_code, description, breaking)
which maps to a DataTable with status badges. Keep the existing
graph diff for visual comparison — these serve different
purposes. Do NOT replace the graph diff.

### Investigating: relationship_graph field

`ManifestVersion.relationship_graph` (field 10) stores a
pre-computed JSON graph during validation. If populated, the
frontend could render it directly instead of re-extracting from
manifest fields — reducing Phase 2 scope. Investigate whether
this field is populated, what its schema is, and whether it
contains enough information (node types, edges, labels) for
graph rendering. If viable, this replaces 2c (model extension)
with "render backend graph."

## Success Criteria

1. Economy graph renders all manifest resource types with
   correct relationships and accessible color palette
2. Economy Explorer shows all 13 manifest types in browsable
   tabs
3. Saga navigation works end-to-end (list, detail, graph click)
4. All resource types have click-through navigation from graph
5. ReconcileManifest accessible from economy dashboard with
   drift items table
6. ApplyResource available via YAML editor with dry_run and
   validation feedback
7. Manifest version history shows per-phase execution status
   and PARTIAL status
8. Backend DiffManifestVersions available as tabular diff
   alongside existing graph diff
9. Optimistic locking conflicts show diff before allowing
   action

## Non-Goals

- Typed forms for all 13 resource types in ApplyResource
  (YAML editor is sufficient for v1)
- Manifest-managed badges on service pages (Explorer replaces
  this — clean separation of manifest vs operational views)
- Redesigning the economy graph layout algorithm (ELK works)
- Adding new backend RPCs beyond the `force` field on
  `ApplyResourceRequest`
- Manifest YAML editor changes (already functional)
- Real-time manifest change notifications (polling is
  sufficient)
- Mobile-responsive graph (desktop-only is acceptable)
- `inbound_routes` visualization (Phase 5 placeholder in proto)

## Risks

| Risk | Mitigation |
|------|------------|
| Graph cluttered with 13 types (40-60 nodes) | Grouped visibility toggles; default infrastructure types to hidden; keep overview graph for core types, Explorer for full detail |
| Color palette inaccessible | Minimum 36-degree hue separation; verify with colorblind simulator; CSS custom properties for light/dark themes |
| Disconnected leaf nodes look like bugs | Group leaf nodes in designated region, or show only in Explorer. Explicit design decision before implementation. |
| DiffManifestVersions format mismatch | Keep existing graph diff + add tabular diff. Two complementary views, not a replacement. |
| ApplyResource can't handle breaking changes | Phase 0 adds `force` to proto. Until then, YAML editor fallback to full ApplyManifest. |
| Generated TS stale | Phase 0 runs `buf generate`. Verify output before Phase 2 begins. |
| 10+ location maintenance burden per type | Phase 2a data-driven registry refactor. One config object drives all locations. |
| Navigation scatter from graph clicks | Use Explorer as target for types without dedicated pages. Breadcrumb back to economy. |
| Event chain breaks with new types | Phase 2h explicitly updates event chain guards. |
| Pre-045 tenants hit errors on reconcile/export | Phase 4b specifies "no manifest" graceful handling. |
| `payment_rails` has no stable code field | Use `provider` as node ID. Document convention. |

## Implementation Notes

- The graph model (`manifest-graph-model.ts`) currently casts
  the manifest to a local `ManifestInput` interface that only
  declares 4 fields. This cast should be removed in Phase 2a —
  use the proto type directly so missing fields are caught at
  compile time.
- The `manifestHistory` and `manifestApplier` Connect RPC
  clients are already wired in `api/clients.ts`.
- The `ManifestGraph` component uses React Flow + ELK. Adding
  node types follows the existing `InstrumentNode`,
  `AccountTypeNode` `memo()` component pattern.
- The diff graph (`manifest-diff-graph.tsx`) has its own
  `LAYER_PRIORITY` duplicate — Phase 2a consolidates this.
- The client-side diff algorithm (`manifest-diff.ts`) is
  already generic. New types are automatically diffed once
  added to the graph model.
- Phase 1 (bug fixes) can ship independently and immediately.
- Phase 2a (registry refactor) must complete before 2c-2i.
- Phases 2 and 3 (graph + Explorer) can develop in parallel
  with Phase 4 (RPC surfaces).
