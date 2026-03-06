---
name: prd-manifest-business-model-visualization
description: >
  Visualize the live tenant manifest as an interactive business model graph
  with transitive event chain resolution
triggers:
  - User navigates to the Manifest page and sees only flat expandable lists
  - User views a reference data detail page with no visibility into attached sagas
  - Business analyst asks "what happens when a kWh position is captured?"
  - Solutions architect needs to understand event fan-out across configured sagas
instructions: |
  This PRD transforms the manifest page from a flat configuration viewer into
  an interactive business model graph. It reuses the visualization primitives
  built by PRD-036 (star-parser, saga-flow, composition-graph, ReactFlow/ELK
  layout) and applies them to the live tenant manifest rather than the static
  cookbook catalogue.
  Key principle: the manifest IS the business model. Every instrument, account
  type, valuation rule, and saga is a node in a connected graph. The
  visualization reveals the relationships and event-driven fan-out that are
  implicit in the YAML/Starlark but invisible in a flat list.
  The transitive closure engine is the core new capability: given a starting
  node (e.g., an instrument or account type), trace the chain of events and
  saga executions across multiple hops, up to the runtime chain depth limit
  (currently 10). This is static analysis — no saga execution required.
---

# PRD 038: Manifest Business Model Visualization

**Status:** Draft
**Parent:** [PRD 036 - Cookbook Browser](036-cookbook-browser.md) (visualization
primitives)
**Related:**

- [PRD 014 - Control Plane](014-control-plane.md) (manifest lifecycle,
  versioning)
- [PRD 035 - Economy Cookbook](035-economy-cookbook.md) (pattern registry,
  star-parser)

## Problem Statement

The manifest page (`/manifests`) currently shows the tenant's
configuration as expandable lists: instruments, account types,
valuation rules, sagas. Each section is a flat enumeration with no
visual connection between elements.

This representation hides the most important property of the manifest:
**how its elements interact at runtime**.

Consider a tenant with the energy-settlement pattern applied. Their
manifest contains:

- Instruments: KWH, GBP
- Account types: ENERGY_INVENTORY (allows KWH), SETTLEMENT (allows
  GBP), REVENUE (allows GBP)
- Valuation rules: KWH->GBP retail, KWH->GBP wholesale
- Saga: `usage_to_value` (trigger:
  `event:position-keeping.transaction-captured.v1`, filter:
  `event.instrument_code != 'GBP'`)

From the flat list, a business user cannot see that:

1. A kWh position on an ENERGY_INVENTORY account emits a
   `transaction-captured` event
2. The `usage_to_value` saga catches that event (because
   `instrument_code != 'GBP'` passes)
3. The saga computes retail and wholesale valuations using the two
   KWH->GBP rules
4. The saga books GBP positions on SETTLEMENT and REVENUE accounts
5. Those GBP positions emit their own `transaction-captured` events,
   but the CEL filter rejects them (`instrument_code == 'GBP'`),
   terminating the chain

This is a **two-hop fan-out** that is invisible in the current UI.
The business user sees five disconnected configuration items. They
should see a connected flow showing cause and effect.

**Three gaps:**

1. **No manifest graph.** The manifest's elements are related
   (instruments allowed by account types, valuation rules bridging
   instruments, sagas triggered by events on account types) but
   presented as independent lists.

2. **No event chain visibility.** Sagas produce downstream events
   that may trigger other sagas. The full transitive closure — what
   the event-router resolves at runtime using CEL filters and chain
   depth tracking — is invisible to the person configuring the
   business model.

3. **No contextual executions on reference data pages.** When
   viewing an instrument or account type in the reference data
   section, there is no tab showing "these sagas execute when events
   involve this entity." The reference data and manifest
   configuration are disconnected surfaces.

## Vision

Transform the manifest page into an **interactive business model
graph** and extend reference data detail pages with **execution
context tabs**.

### Manifest Graph View

The manifest page gains a third tab alongside "Current Manifest"
and "Version History": a **Graph** tab showing the complete business
model as an interactive node graph.

Nodes represent the four manifest element types. Edges represent the
relationships between them:

```text
[KWH] ──allowed_by──> [ENERGY_INVENTORY]
  |                           |
  |──from_instrument──> [kwh_to_gbp_retail]
  |                           |        event:transaction-captured
  |──from_instrument──> [kwh_to_gbp_wholesale]       |
  |                                                   v
  v                                       [usage_to_value saga]
[GBP] <──to_instrument── [kwh_to_gbp_*]    |          |
  |                                         v          v
  |──allowed_by──> [SETTLEMENT] <── retail leg
  |──allowed_by──> [REVENUE] <───── wholesale leg
```

The graph is interactive:

- Click a node to navigate to its detail (instrument detail, account
  type detail, saga flow view)
- Hover a node to highlight all connected elements and dim unrelated
  ones
- Filter by element type (show only instruments + sagas, hide
  valuation rules)
- Zoom and pan for complex manifests

### Transitive Closure View

Select any node and activate "Show Event Chain" to see the full
fan-out:

Starting from ENERGY_INVENTORY:

```text
Hop 0: Position entry on ENERGY_INVENTORY (KWH)
  |
  | emits: event:position-keeping.transaction-captured.v1
  | filter passes: instrument_code != 'GBP' (KWH -> true)
  |
Hop 1: usage_to_value saga executes
  |-- compute_retail_valuation (kwh_to_gbp_retail)
  |-- compute_wholesale_valuation (kwh_to_gbp_wholesale)
  |-- book_retail_position -> SETTLEMENT (GBP, DEBIT)
  |-- book_wholesale_position -> REVENUE (GBP, CREDIT)
       |
       | emits: event:position-keeping.transaction-captured.v1
       | filter fails: instrument_code != 'GBP' (GBP -> false)
       |
       x Chain terminates (CEL filter rejection)
```

This is computed entirely by static analysis:

1. Parse each saga's Starlark script (reuse `star-parser.ts`) to
   find `position_keeping.initiate_log()` calls
2. Extract the `instrument_code` parameter from those calls
3. Match the produced event against other sagas' trigger channels
   and CEL filters
4. Repeat until no new sagas match or the configured chain depth
   limit is reached

### Versioned Manifest Diffing

When viewing version history, select two versions to see a visual
diff:

- Green nodes/edges: added in the newer version
- Red nodes/edges: removed in the newer version
- Amber nodes/edges: modified between versions

### Reference Data Execution Context

Each reference data detail page gains an "Executions" tab:

**Instrument detail (e.g., KWH):**

- Sagas triggered by events involving this instrument (direct match
  on trigger/filter)
- Valuation rules that convert from/to this instrument
- The transitive fan-out subgraph starting from this instrument

**Account type detail (e.g., ENERGY_INVENTORY):**

- Sagas that read from or write to accounts of this type (from
  star-parser analysis)
- Instruments allowed on this account type
- The transitive fan-out subgraph starting from this account type

This turns reference data pages from "what is this thing" into
"what does this thing do in the business model."

## Architecture

### Data Source

All data comes from the existing `GetCurrentManifest` gRPC call
(already used by `ManifestCurrentView`). The manifest proto contains
instruments, account types, valuation rules, and sagas with inline
scripts. No new backend endpoints required for the core
visualization.

The transitive closure is computed client-side from the manifest
data. The star-parser extracts service calls from saga scripts. CEL
filter analysis uses string matching on the filter expressions (the
same expressions the event-router compiles at runtime).

### Manifest Graph Model

A TypeScript module that transforms a `Manifest` proto response into
a typed graph:

```typescript
interface ManifestGraph {
  nodes: ManifestNode[]
  edges: ManifestEdge[]
}

interface ManifestNode {
  id: string // e.g., "instrument:KWH", "saga:usage_to_value"
  type: 'instrument' | 'account_type' | 'valuation_rule' | 'saga'
  label: string
  data:
    | InstrumentDefinition
    | AccountTypeDefinition
    | ValuationRule
    | SagaDefinition
}

// Saga nodes carry trigger metadata directly (channel, filter)
// rather than modeling event channels as separate graph nodes.
// This avoids introducing a node type that has no manifest-level
// definition — event channels are implicit, not configured.

interface ManifestEdge {
  id: string
  source: string
  target: string
  relationship: ManifestRelationship
}

type ManifestRelationship =
  | 'allowed_by' // instrument -> account_type
  | 'converts_from' // valuation_rule -> instrument
  | 'converts_to' // valuation_rule -> instrument
  | 'reads_from' // saga -> account_type (star-parser)
  | 'writes_to' // saga -> account_type (star-parser)
  | 'uses_valuation' // saga -> valuation_rule (star-parser)
```

The `reads_from`, `writes_to`, and `uses_valuation` edges are
derived by running `star-parser.ts` on each saga's inline script
and matching service call parameters against manifest elements.

### Transitive Closure Engine

```typescript
interface EventChain {
  hops: EventHop[]
  terminationReason:
    | 'filter_rejection'
    | 'chain_depth_limit'
    | 'no_matching_sagas'
}

interface EventHop {
  depth: number
  trigger: string // event channel name
  saga: SagaDefinition
  filterExpression: string | null
  filterResult: 'pass' | 'fail' | 'indeterminate'
  producedEvents: ProducedEvent[]
}

interface ProducedEvent {
  channel: string // e.g., "position-keeping.transaction-captured.v1"
  instrumentCode: string | null // from initiate_log params
  accountType: string | null // from initiate_log params
}
```

**Static CEL filter analysis:** For simple filters like
`event.instrument_code != 'GBP'`, the engine can determine
pass/fail when the produced event's instrument code is known. For
complex filters, the result is `indeterminate` and the UI shows the
filter expression with a "may or may not match" indicator.

**Chain depth limit:** The engine defaults to 10 (matching the
event-router's `defaultMaxChainDepth`). If a future configuration
endpoint exposes the effective chain depth per tenant, the UI should
read it from there. Until then, the frontend uses a shared constant
that must be kept in sync with the event-router's default. The
current depth is displayed in the UI so users understand the runtime
safety boundary.

### Component Architecture

```text
frontend/src/features/manifests/
  components/
    manifest-graph.tsx            # Full business model graph
    manifest-graph-node.tsx       # Custom nodes per element type
    manifest-graph-legend.tsx     # Edge type legend
    event-chain-panel.tsx         # Transitive closure detail panel
    manifest-diff-graph.tsx       # Version diff overlay
  lib/
    manifest-graph-model.ts       # Manifest proto -> graph model
    transitive-closure.ts         # Event chain resolution engine
    cel-filter-analyzer.ts        # Static CEL filter analysis
    saga-output-analyzer.ts       # star-parser extension
  hooks/
    use-manifest-graph.ts         # Graph model from manifest
    use-event-chain.ts            # Transitive closure from node
  pages/
    index.tsx                     # Add Graph tab
    manifest-current-view.tsx     # Existing (unchanged)
    manifest-history-table.tsx    # Existing (unchanged)

frontend/src/features/reference-data/
  components/
    execution-context-tab.tsx     # Executions tab for details
    execution-subgraph.tsx        # Filtered manifest graph
```

### Reused from PRD-036

| PRD-036 component | Reuse in PRD-038 |
|---|---|
| `star-parser.ts` | Parse saga scripts, extract `initiate_log` params |
| `saga-flow.tsx` | Embed in event chain panel for per-saga flows |
| `composition-graph.tsx` | ReactFlow + ELK layout patterns |
| `manifest-viewer.tsx` | Existing manifest YAML view (unchanged) |
| `@xyflow/react` + `elkjs` | Same layout engines, different nodes |

**Reuse mechanism:** The shared visualization primitives
(`star-parser.ts`, `SagaFlowDiagram`, graph layout utilities) live
in `frontend/src/features/cookbook/` today. Phase 1 of this PRD
should extract the reusable modules (`star-parser.ts`,
`saga-mermaid.ts`, and the ReactFlow layout helpers) to
`frontend/src/lib/visualization/` so both `features/cookbook/` and
`features/manifests/` import from a shared location. This avoids
cross-feature imports and establishes `lib/` as the home for
reusable visualization utilities.

### Relationship to Event-Router Runtime

The visualization mirrors what the event-router does at runtime:

| Runtime (event-router) | Visualization (frontend) |
|---|---|
| `SagaRegistry.Reload()` indexes by channel | `manifest-graph-model.ts` builds trigger edges |
| `SagaDispatchHandler.Handle()` evaluates CEL | `cel-filter-analyzer.ts` static filter analysis |
| `x-meridian-chain-depth` limits depth | `transitive-closure.ts` stops at `maxChainDepth` |
| `saga.EventToInputData()` converts events | `saga-output-analyzer.ts` extracts outputs |

The visualization is an approximation of runtime behavior through
static analysis. It cannot capture dynamic values (e.g., account IDs
resolved at runtime via `reference_data.get_account()`), but it
captures the structural topology: which sagas can trigger, what they
produce, and where the chain terminates.

## Implementation Phases

### Phase 1: Manifest Graph Model

- `manifest-graph-model.ts`: transform `Manifest` proto into typed
  graph
- Build nodes from instruments, account types, valuation rules, sagas
- Build edges from `allowed_instruments`, `from/to_instrument`, saga
  `trigger` fields
- Unit tests with the energy-settlement manifest fragment as fixture

### Phase 2: Manifest Graph View

- `manifest-graph.tsx`: ReactFlow component with custom nodes per
  element type
- ELK layout (layered, top-down: instruments at top, sagas at bottom)
- Custom node components: instrument (blue), account type (green),
  valuation rule (amber), saga (purple)
- Hover highlighting (dim unrelated nodes, animate connected edges)
- Click to navigate to detail pages
- Add "Graph" tab to the manifests page alongside "Current Manifest"
  and "Version History"
- Edge type legend

### Phase 3: Saga Output Analysis

- `saga-output-analyzer.ts`: extend star-parser to extract produced
  events from saga scripts
- Identify `position_keeping.initiate_log()` calls and extract
  `instrument_code`, `account_id` params
- Identify `valuation_engine.compute()` calls and extract `method_id`,
  `from/to_instrument` params
- Add `reads_from` and `writes_to` edges to the manifest graph model
  when the target account type is statically determinable (e.g.,
  `instrument_code="GBP"` as a literal). When the target is a
  runtime variable, emit a `writes_to_dynamic` marker on the saga
  node with the variable name, rendered in the UI as "writes to
  (resolved at runtime)" with the relevant code snippet as tooltip
- Unit tests against all cookbook saga patterns

### Phase 4: Transitive Closure Engine

- `transitive-closure.ts`: given a starting node, walk the event
  chain
- `cel-filter-analyzer.ts`: static analysis of CEL filter expressions
  for pass/fail/indeterminate
- Trace: starting event -> matching sagas -> produced events ->
  matching sagas -> ...
- Stop conditions: CEL filter rejection, chain depth limit, no
  matching sagas
- `event-chain-panel.tsx`: panel showing the full chain with
  hop-by-hop detail
- Each hop shows: saga name, filter expression + result, produced
  events, target accounts
- Embed `SagaFlowDiagram` for each saga in the chain (expandable)

### Phase 5: Reference Data Execution Context

- `execution-context-tab.tsx`: new tab on instrument and account type
  detail pages
- Shows sagas attached to this entity (direct triggers + transitive)
- Shows valuation rules involving this instrument
- Embedded subgraph (filtered manifest graph showing only related
  nodes)
- Wire into existing reference data detail page routing

### Phase 6: Manifest Version Diff (Optional)

- `manifest-diff-graph.tsx`: overlay diff between two manifest
  versions
- Added/removed/modified nodes and edges
- Color coding: green (added), red (removed), amber (modified)
- Accessible from the version history tab: select two versions to
  compare

## Technology Choices

### Graph Layout

**Decision: ELK (layered algorithm)** for the manifest graph.

The manifest has a natural hierarchy: instruments -> account types ->
valuation rules -> sagas. A layered (Sugiyama) layout respects this
hierarchy and produces readable top-to-bottom flows. This is the same
layout engine used by the cookbook composition graph.

Force-directed layout was considered but rejected: it produces
arbitrary positioning that obscures the hierarchical relationships
between element types.

### CEL Filter Static Analysis

**Decision: Pattern matching on filter expression strings**, not CEL
compilation in the browser.

The browser does not need a full CEL evaluator. Most saga filters
follow predictable patterns:

```cel
event.instrument_code != 'GBP'          // extractable: field comparison
event.direction == 'DEBIT'              // extractable: field comparison
event.amount > 0 && event.currency == 'GBP'  // extractable: compound
has(event.metadata.some_field)          // indeterminate (runtime only)
```

A regex-based pattern matcher handles the common cases. Complex
filters fall back to `indeterminate`, which the UI displays honestly:
"this filter may or may not match — view the expression."

This avoids bundling the cel-go WASM build or a JavaScript CEL
evaluator (both add significant bundle size for marginal value).

### Star-Parser Extension

**Decision: Extend the existing `star-parser.ts`** with output
analysis, not create a separate parser.

The star-parser already extracts `serviceCalls` from each step. The
extension adds semantic interpretation: when the service is
`position_keeping` and the method is `initiate_log`, extract the
`instrument_code` and `account_id` parameters. This is a thin layer
on top of existing parsing.

## Open Questions

1. **Dynamic parameters in saga scripts.** When a saga calls
   `position_keeping.initiate_log(instrument_code="GBP")`, the
   instrument code is statically extractable. But when it calls
   `initiate_log(instrument_code=computed_value)`, the code is
   determined at runtime. How should the visualization handle this?
   Options: show as "dynamic" with a tooltip explaining the
   variable, or trace the variable assignment backward through
   the script.

2. **Account type resolution.** Sagas often resolve account IDs at
   runtime via `reference_data.get_account()`. The visualization
   can show that a saga *reads from* reference data, but cannot
   determine *which specific account type* without runtime data.
   Should Phase 5 call the reference data gRPC service to resolve
   account type mappings, or accept the limitation of static
   analysis?

3. **Filter complexity threshold.** At what point should the CEL
   analyzer give up and return `indeterminate`? Current proposal:
   handle field comparisons (`==`, `!=`, `>`, `<`), boolean
   operators (`&&`, `||`), and `has()` checks. Anything else is
   indeterminate.

4. **Manifest size scaling.** A complex tenant might have 20+
   instruments, 30+ account types, and 50+ sagas. The graph could
   become visually overwhelming. Options: progressive disclosure
   (show only one element type at a time), semantic zoom (zoom out
   shows clusters, zoom in shows individual nodes), or fixed
   maximum node count with a "simplified view" toggle.

5. **Relationship to MCP tools.** The existing
   `meridian_causation_tree` MCP tool provides runtime causation
   analysis. Should the frontend visualization call this tool (via
   a new gRPC endpoint wrapping the same logic), or remain purely
   client-side static analysis? Client-side is simpler but less
   accurate; server-side is more accurate but adds a backend
   dependency.

## Non-Goals

- Editing the manifest from the graph view (read-only visualization;
  editing uses the existing apply dialog or future phases)
- Executing sagas from the visualization (use existing saga
  management)
- Real-time event streaming or live saga execution monitoring (this
  is static analysis of the manifest configuration, not runtime
  observation)
- CEL compilation in the browser (pattern matching is sufficient for
  the common cases)
- Mobile-responsive layout (staff-only, desktop assumed)

## Success Criteria

1. A business user can open the Manifest Graph tab and see all
   manifest elements as an interactive node graph with typed edges
   showing relationships
2. Clicking "Show Event Chain" on any node displays the full
   transitive fan-out with hop-by-hop detail, including CEL filter
   pass/fail analysis
3. The transitive closure correctly identifies chain termination
   points (filter rejection, chain depth limit) for all cookbook
   patterns
4. Reference data detail pages (instruments, account types) show an
   "Executions" tab listing attached sagas and the relevant subgraph
5. The graph handles manifests with up to 50 instruments, 50 account
   types, and 50 sagas without layout degradation (< 2s render)
6. Version diff view highlights added, removed, and modified elements
   between two manifest versions
7. All visualization is derived from the existing `GetCurrentManifest`
   gRPC response and client-side static analysis — no new backend
   endpoints required for Phases 1-5. Phase 5 may additionally call
   existing reference data gRPC endpoints (e.g., to resolve account
   type mappings) depending on the resolution of open question 2;
   the constraint is against creating new backend services, not
   against using existing ones

## Relationship to Previous PRDs

| PRD | What it built | What this PRD adds |
|---|---|---|
| 014 | Control plane: manifest lifecycle, validation, versioning | Visual manifest as interactive business model graph |
| 035 | Economy cookbook: pattern registry, Starlark sagas | Runtime visualization of configured patterns |
| 036 | Cookbook browser: star-parser, saga-flow, ReactFlow/ELK | Reuses primitives for live manifest data |
| **038** | | **Business model visualization for non-engineers** |

PRD-036 visualizes what Meridian *offers* (the cookbook catalogue).
PRD-038 visualizes what a tenant *has configured* (their live
business model). Together they answer: "what can I build?" and
"what have I built?"
