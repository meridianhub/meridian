# Full Economy Visibility

## Problem Statement

Meridian tenants cannot see what capabilities the platform
provides out of the box. Every tenant receives 28 platform
default resources via PostProvisioningHooks - 8 system sagas,
12 account type blueprints, 5 valuation methods, and 3
valuation policies - all tagged with `is_system=true` in the
database. These resources are actively running but invisible
in the UI because the Economy Explorer and Overview pages read
exclusively from `GetCurrentManifest`, which only contains
tenant-authored manifest state.

A freshly provisioned tenant sees "No economy configured"
despite having a functioning economy with deposit/withdrawal
sagas, multi-currency account types, and identity valuation
methods already running. This creates blank canvas anxiety
during onboarding and obscures the platform's value
proposition.

The problem is not "tenants can't see platform defaults." The
problem is "tenants don't understand what capabilities the
platform provides out of the box." This distinction drives the
solution toward a capabilities guide rather than a mixed
inventory view.

## Technical Context

### What exists today

**Platform defaults seeded per tenant
(PostProvisioningHooks):**

| Resource Type | Count | Seeder Location | Tagged |
|---|---|---|---|
| Saga definitions | 8 | `saga/seeder.go` | `is_system=true` |
| Account type blueprints | 12 | `accounttype/seeder.go` | `is_system=true` |
| Valuation methods | 5 | `valuation/seeder.go` | `is_system=true` |
| Valuation policies | 3 | `valuation/seeder.go` | `is_system=true` |

All seeders are under `services/reference-data/`.

**System sagas:** deposit, withdrawal, payment_execution,
reconciliation_adjustment, dividend_distribution,
dunning_escalation, dunning_unfreeze, stripe_payment. Seeded
with `platform_ref` pointing to
`public.platform_saga_definition`, script resolved at query
time.

**Account type blueprints:**
CURRENT_ACCOUNT_GBP/USD/EUR, CLEARING_GBP, NOSTRO_GBP,
VOSTRO_GBP, HOLDING_ESCROW, SUSPENSE_UNALLOCATED,
REVENUE_FEES, EXPENSE_OPERATIONS, CARBON_CREDIT_HOLDING,
INVENTORY_KWH.

**Valuation methods:**
SYSTEM_IDENTITY_USD/GBP/EUR (same-currency identity),
SYSTEM_RETAIL_ENERGY (KWH to GBP),
SYSTEM_CARBON_CREDIT (TONNE_CO2E to GBP).

**Valuation policies:**
SYSTEM_IDENTITY (pass-through),
SYSTEM_POSITIVE_AMOUNT (validates > 0),
SYSTEM_AMOUNT_UNDER_LIMIT (validates < 1,000,000).

**Override resolution (sagas only):**

The saga registry's `GetActive` checks tenant overrides first
(`is_system=false`, ACTIVE, highest version), then falls back
to platform defaults (`is_system=true`, ACTIVE, highest
version). A tenant can override a system saga by creating one
with the same name. Account types and instruments have no
override mechanism - system resources reject all modifications
via `ErrSystemAccountTypeReadOnly`.

**Data flow gap:**

- `GetCurrentManifest` returns stored manifest JSON snapshots
  (tenant-authored only)
- `ExportManifest` has collector interfaces but collectors are
  not wired in production (`wire_grpc.go` passes nil
  `ExportCollectors`)
- Reference-data List RPCs already return `is_system` on
  proto responses
- The frontend has no rendering of the `is_system` flag
  anywhere

**Key architectural principle:**

The manifest is a "desired state declaration" - it represents
what the tenant authored. Platform defaults are not
tenant-declared and should not be mixed into the manifest.
Both this workstream and PRD-057 (Convergent Manifest Apply)
independently reached this conclusion.

### Platform defaults are static per release

Platform defaults are hardcoded in Go seeders as embedded
files and Go constants. They change when Meridian code ships,
not at runtime. The frontend also ships when code ships -
they're in the same release artifact. This means V0.5 and V1
can use static frontend data with zero backend dependency.

## Phased Delivery

### V0.5: Empty State Reframe (1 point, frontend only)

**Goal:** Eliminate blank canvas anxiety. Communicate platform
value to fresh tenants.

**Changes:**

- Update `EmptyState` in `economy-overview-page.tsx` from
  "No economy configured" to "No custom economy configured.
  Your tenant includes 28 platform capabilities out of the
  box - 8 sagas, 12 account types, 5 valuation methods, and
  3 policies. Apply a manifest to customize or extend them."
- Update `EmptyState` in `economy-explore-page.tsx` with
  equivalent copy
- Add links to existing reference data list pages (sagas,
  account types, valuation rules) where platform defaults
  are already visible

**Why ship separately:** This is a copy change with zero
regression risk that immediately improves onboarding. No new
components, no new data sources.

### V1: Platform Badges on Existing Pages (2-3 points, frontend only)

**Goal:** Make platform vs tenant distinction visible where
tenants already browse resources.

**Changes:**

- Add "Platform" badge to saga list page for
  `is_system=true` entries
- Add "Platform" badge to account type list page for
  `is_system=true` entries
- Add "Platform" badge to valuation rules list page for
  `is_system=true` entries
- Add secondary stat line on Economy Overview:
  "Running on 28 platform capabilities"
- Analytics instrumentation (4 track calls):
  - `economy.platform_badge_visible` - page load with
    platform resources present
  - `economy.platform_resource_clicked` - user clicks a
    platform resource
  - `economy.override_intent` - user navigates from
    platform resource to saga creation
  - `economy.empty_state_shown` - empty state displayed
    with `hasManifest: false`

**`is_system` prerequisite checklist:**

Before V1 implementation, verify each List RPC returns
`is_system` and that the frontend renders those items:

| Entity | Proto field | List RPC | Status |
|---|---|---|---|
| `SagaDefinition` | `bool is_system` | `ListSagas` | Verified in proto |
| `AccountType` | `bool is_system` | `ListAccountTypes` | Verified in proto |
| `Instrument` | `bool is_system` | `ListInstruments` | Verified in proto |
| `ValuationMethod` | `bool is_system` | `ListValuationMethods` | To verify |
| `ValuationPolicy` | `bool is_system` | `ListValuationPolicies` | To verify |

If any entity's List RPC does not include `is_system`, that
becomes a small backend prerequisite (adding the field to the
proto response). The "no backend work" statement is
conditional on all five passing verification.

Additionally, confirm list pages render `is_system=true`
items (not filtering them out). If they already show
everything, badges are pure additive. If they filter, that's
a prerequisite fix.

**Data source:** The reference-data List RPCs return
`is_system` on proto responses. The badge is:

```tsx
{item.isSystem && <Badge variant="outline">Platform</Badge>}
```

**Why no backend work:** `is_system` is already present in
the proto responses the frontend consumes on list pages. No
new endpoints or proto changes needed.

### V1-backend: Wire ExportManifest Collectors (3 points, independent)

**Goal:** Complete the unfinished ExportManifest bridge.
Standalone value independent of economy visibility.

**Changes:**

- Wire existing collector implementations in `wire_grpc.go`
  (SagaCollector, AccountTypeCollector, InstrumentCollector)
- Implement `ValuationCollector` interface (simple table
  query, maps to `ValuationRule` proto)
- Enable parallel collection (current `collectAllSections`
  is sequential)

**Standalone value:**

- Drift detection: `ReconcileManifest` RPC becomes functional
  (currently `DriftWarningBanner` hardcodes
  `driftDetected: false`)
- CI validation: `meridian-cli manifest diff --live` becomes
  possible
- Manifest-from-state: tenants who configured resources via
  direct API can export a valid manifest
- Reconciliation assertions as a balance assertion type

**Relationship to this PRD:** This is a prerequisite for V2
but ships on its own merits. Track separately.

### V2: Composite Economy View (13+ points, conditional on V1 signals)

**Goal:** Fully integrated economy visualization with
provenance, graph integration, and override workflows.

**Scope (to be refined based on V1 analytics):**

- `origin` enum on manifest proto types
  (`ORIGIN_PLATFORM`, `ORIGIN_MANIFEST`,
  `ORIGIN_TENANT_OVERRIDE`) - use `origin` not `source`
  to avoid collision with `ValuationRule.source` field
- Composite Economy Explorer merging manifest + live state
  with provenance badges
- ManifestGraph integration with origin-based node styling
  (dashed borders, muted colors for platform)
- Override workflow: "Create Override" action on platform
  saga nodes
- Override count display:
  "3 of 8 platform sagas customized"
- Drift detection exclusion logic: `is_system=true`
  resources excluded from tenant drift calculations
  (prerequisite - platform defaults must not trigger drift
  warnings)
- Graph filter dimension for platform/manifest/override

**Override asymmetry to handle:**

- Sagas: full override model exists (`override_api.go`,
  similarity checking, audit trail)
- Account types: strictly read-only
  (`ErrSystemAccountTypeReadOnly`), no override mechanism
- Instruments: strictly read-only, no override mechanism
- UI must communicate this asymmetry: "Override" affordance
  on sagas, read-only lock on account types/instruments

**V2 trigger criteria (measure from V1 analytics):**

Evaluate after a 30-day observation window post-V1 launch.
Baseline = first 7 days of V1 data. Source of truth =
analytics events for quantitative signals, support ticket
system for qualitative signals.

| Signal | Threshold | Denominator |
|---|---|---|
| Badge click-through | >15% of sessions | Sessions with badges visible |
| Override intent | >5% | Users viewing platform resources |
| Explicit request | 3+ distinct tenants | Support tickets in window |
| Support deflection | 50% reduction | "What's included" questions vs baseline |

## Explicit Out of Scope for V0.5/V1

- No new backend endpoints or proto changes
- No ManifestGraph integration
- No override/customize workflow
- No drift detection changes
- No composite Economy Explorer view
- No `GetEconomySummary` endpoint
- No `origin` enum on manifest protos
- No functional grouping of platform defaults

## Related Workstreams

### PRD-046: Economy Visualization Completeness

PRD-046 covers economy graph integration, drift detection,
and provenance display from the manifest perspective. It
rejected manifest-managed badges on service pages as a
non-goal, preferring the Explorer as the unified manifest
view.

PRD-058 V1 badges are architecturally different - they use
the operational `is_system` flag from reference-data services,
not manifest state. However, V2 scope (composite Economy
Explorer, graph integration with origin-based styling, drift
detection exclusion) overlaps with PRD-046 Phases 2-4.

**Coordination:** PRD-046 owns the manifest view. PRD-058
owns the platform/tenant provenance layer. V2 implementation
should coordinate to avoid duplicate work in graph
integration and drift detection.

### PRD-057: Convergent Manifest Apply

A parallel workstream addressing how manifests are applied -
diffing against live state, handler idempotency, deprecation
semantics, and platform-aware diffing. Four phases, 21 points
total. Phase 1 (handler idempotency fix, 3 pts) ships
independently and unblocks CI.

**Shared assumptions (mirrored in both PRDs):**

1. **The manifest stays pure.** It represents the tenant's
   declaration of intent. Platform defaults are not mixed in.
2. **`is_system` is the discriminator.** The boolean flag on
   reference-data entities distinguishes platform from tenant
   resources. Both workstreams rely on this existing field.
3. **Platform defaults are excluded from tenant diff/drift.**
   Resources with `is_system=true` should not appear as drift
   from the tenant's manifest, and should not be included in
   convergent apply diffs.

**Coordination points:**

- PRD-058 V1-backend (ExportManifest collectors) and PRD-057
  Phase 2 (live-state diff) both need the same List endpoints
  - natural coordination point but neither blocks the other
- PRD-058 V0.5/V1 has zero dependency on PRD-057 - ships
  immediately
- The override asymmetry flagged here (sagas overridable,
  account types read-only) is captured in PRD-057's diff
  logic design

**Shared dependency:** Both workstreams need `is_system`
consistently present in List endpoint proto responses across
all services. PRD-058 V1 relies on this for badges. PRD-057
relies on this to filter platform defaults from tenant diffs.

## Success Criteria

### V0.5

- Fresh tenants see platform capability count instead of
  "No economy configured"
- Links navigate to reference data pages where platform
  resources are visible

### V1

- Platform resources visually distinguished from tenant
  resources on all list pages
- Analytics baseline established for V2 go/no-go decision
- Zero regressions on existing Economy Explorer/Overview
  pages

### V2 (if triggered)

- Tenant can see complete economy (platform + custom) in
  one view with clear provenance
- Override workflow enables saga customization from the UI
- Drift detection excludes platform defaults from tenant
  drift calculations
