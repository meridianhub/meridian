# PRD-057: Convergent Manifest Apply

## Problem Statement

Applying a manifest should be a convergent operation - like
`kubectl apply`, it should make reality match the declaration
regardless of current state. Today, `ApplyManifest` fails when
re-applied to an environment with existing data because:

1. The diff compares stored manifest history (last-applied vs new)
   instead of querying live service state. If a previous apply
   partially succeeded but wasn't recorded, the diff has no
   awareness of what actually exists.

2. The saga receives ALL manifest resources regardless of what the
   diff computed. The diff/plan output is effectively unused -
   `buildExecutorInput` passes the entire manifest to the
   Starlark script.

3. Handler-level idempotency fallbacks exist but are fragile.
   For example, `ActivateInstrument` catches `FailedPrecondition`
   and checks if the instrument is already ACTIVE, but if the gRPC
   error code differs or the lookup fails, the error propagates
   and the saga fails.

4. Resource removal means hard deletion, not deprecation. There is
   no audit trail of what was removed or ability to roll back.

This surfaced in CI (develop and demo deploy failures) but
represents a production risk: any tenant re-applying a manifest,
applying after a partial failure, or applying a manifest with a
small delta from the previous version will hit the same issues.

### Observed Failure

```text
activate instrument: rpc error: code = FailedPrecondition
  desc = instrument must be in DRAFT status: GBP
```

GBP was created and activated by a previous partial apply. The
stored manifest history was never updated (overall apply failed).
On retry, the diff sees everything as CREATE, the saga tries to
register+activate GBP again, and the handler's idempotency
fallback fails to recover.

## Prior Art: kubectl apply

Kubernetes `kubectl apply` is the proven model:

- **Three-way diff**: Compares last-applied state, current live
  state, and desired state
- **Per-resource convergence**: Each resource is independently
  assessed (create, update, no-op, or remove)
- **Idempotency is non-negotiable**: Applying the same manifest
  twice is always safe. Applying after a partial failure
  completes the convergence.
- **Removal is graceful**: Resources support finalizers, grace
  periods, and remain queryable after deletion

Meridian should follow this model. The manifest declares desired
state. Apply converges the live system to match. Always.

## Relationship to PRD-045

PRD-045 (Manifest as Sole Source of Truth) establishes that
the control plane owns all structural economy declarations and
services own runtime state. This PRD builds on that model:

- **The manifest remains the source of truth for desired state.**
  What the tenant declares is authoritative. PRD-045's versioned
  manifest store, audit trail, and diff capabilities are preserved.
- **Services are the source of truth for current state.** When
  computing what actions to take, the diff queries services for
  what actually exists rather than relying solely on stored
  manifest history. This handles partial failures, out-of-band
  changes, and stale history.
- **This matches PRD-045's own separation**: "Control Plane owns
  declarations, Services own runtime." The diff reads runtime
  state from services (their domain) and compares it against the
  declaration (control plane's domain).

In Kubernetes terms: the manifest is the desired state (like a
YAML file), the stored history is the last-applied annotation,
and the service query is the API server read. All three inform
the convergence decision.

## Shared Assumptions

_These principles are shared with PRD-058 (Full Economy
Visibility) and were independently validated by both
workstreams._

1. **The manifest stays pure.** It is a tenant's declaration of
   desired state. Platform defaults are not mixed into the
   manifest.

2. **`is_system` is the discriminator.** Platform-provided
   resources are tagged `is_system=true` in the database and
   proto responses. This flag already exists on saga definitions,
   instruments, and account types.

3. **Platform defaults are excluded from tenant diff.** A tenant
   manifest apply only converges tenant-owned resources. Platform
   defaults are visible in live state but read-only from the
   tenant's perspective.

4. **Override asymmetry exists and is intentional.** Sagas have a
   full override model (`override_api.go` with similarity checking
   and audit trail). Account types and instruments are strictly
   read-only (`ErrSystemAccountTypeReadOnly`). The diff logic must
   handle these differently.

5. **Services report current state for diffing.** The diff queries
   each service for what resources actually exist rather than
   relying solely on stored manifest history. The manifest remains
   the authoritative declaration of desired state (per PRD-045).

## Scope

### In Scope

#### Phase 1: Fix handler idempotency (bug fix)

- Reproduce the exact `ActivateInstrument` failure with a test
- Fix the fallback logic to handle all "already exists in target
  state" scenarios
- Extend the same pattern to all handlers (RegisterInstrument,
  account types, data sets, sagas, etc.)
- Add test coverage for every handler's idempotency path:
  resource already exists in target state, resource exists in
  intermediate state, resource doesn't exist

#### Phase 2: Live-state diff

- Replace the two-way diff (last-applied manifest vs new
  manifest) with a live-state diff (current service state vs
  desired manifest)
- The diff step queries each service via existing list endpoints
  to build a picture of current state
- Compare live state against desired manifest to produce precise
  actions per the action enum defined in the Diff Logic section
- Filter `is_system` resources before planning (pre-plan filter,
  not a plan action) - recognize saga overrides as intentional
- If a service list endpoint fails, the apply fails with a clear
  error ("cannot determine current state for reference-data").
  Partial live state would produce incorrect diffs. The manifest
  is not applied until full current state is known.

#### Phase 3: Plan-driven saga execution

- Pass only the diff-computed actions to the saga, not the
  entire manifest
- The saga becomes an executor of a precise plan rather than
  re-discovering what to do
- Each action carries its resource data and the intended
  operation (create, update, deprecate)
- Compensation logic adjusts accordingly (only compensate
  actions that were attempted)

#### Phase 4: Deprecation instead of deletion

- When a resource exists in live state but not in the manifest,
  the action is DEPRECATE (or the equivalent terminal state),
  not DELETE
- Deprecated resources remain queryable for audit
- Requires lifecycle states on config-only resources that
  currently lack them:
  - Market Data Sources: boolean `is_active` needs proper
    lifecycle
  - OG Connections: health status only, needs lifecycle
  - OG Routes: no status at all, needs lifecycle

### Out of Scope

- Platform default override workflows for non-saga resource
  types (future work)
- Explicit exclusion of platform defaults by tenants (deferred)
- UI changes for economy visibility (separate PRD-058)
- `ExportManifest` collector wiring (PRD-058 V1-backend)
- New `GetEconomySummary` endpoint
- Origin enum on manifest protos

## Technical Design

### Current Flow

```text
ApplyManifest(manifest)
  1. validate(manifest)
  2. diff(lastApplied, newManifest)      <- stored history
  3. plan(diffActions)                    <- phased plan
  4. execute(entireManifest)              <- ignores diff
  5. saga iterates ALL resources          <- unconditional
  6. handlers have fragile fallbacks
  7. recordHistory(manifest)              <- only on success
```

### Target Flow

```text
ApplyManifest(manifest)
  1. validate(manifest)
  2. queryLiveState(tenantID)             <- query services
  3. diff(liveState, desiredManifest)     <- reality vs intent
  4. filterPlatformDefaults(diffActions)   <- exclude is_system
  5. plan(filteredActions)                 <- phased plan
  6. execute(plannedActions)               <- only what's needed
  7. saga executes precise plan            <- action-aware
  8. handlers are robustly idempotent      <- proactive checks
  9. recordHistory(manifest, diffSummary)  <- even on partial
```

### Live State Query

Each service is queried for its managed resource types:

| Service | Endpoint | Resources |
|---------|----------|-----------|
| reference-data | `ListInstruments` | Instruments |
| reference-data | `ListAccountTypes` (gap: all statuses) | Account Types |
| reference-data | `ListSagas` | Saga Definitions |
| market-information | `ListDataSources` | Market Data Sources |
| market-information | `ListDataSets` | Market Data Sets |
| party | `ListParties` | Organizations |
| internal-account | `ListInternalAccounts` | Internal Accounts |
| internal-account | `ListValuationFeatures` | Valuation Features |
| operational-gateway | `ListConnections` | OG Connections |
| operational-gateway | `ListRoutes` | OG Routes |

**Gap**: `ListAccountTypes` currently returns only active types
(`ListActive`). Needs an "all statuses" variant for the diff to
detect draft/deprecated types.

**Valuation Rules**: No standalone list endpoint. These are
control-plane-internal, only created via manifest. Phase 2
should add a `ListValuationRules` query against the
control-plane database to read live state, keeping the diff
consistent across all resource types. Until then, valuation
rules use stored manifest history as a fallback - this is a
known limitation, not a target state.

### Diff Logic

#### Action Enum

The diff produces one action per resource. The full set:

- **CREATE** - Resource in manifest but not in live state.
  Register and activate.
- **NO_CHANGE** - Resource in both, fields and status match.
  No-op.
- **UPDATE** - Resource in both, fields differ. Semantics
  depend on resource type: for resources that support in-place
  mutation (OG connections/routes via upsert), apply directly.
  For lifecycle-managed resources (instruments, account types),
  UPDATE means deprecate the existing version and create a new
  one in the target state (deprecate-and-recreate). The apply
  succeeds only when desired state is fully converged - field
  drift is not left unresolved.
- **DEPRECATE** - Resource in live state but not in manifest,
  tenant-owned. Move to terminal state.

#### Pre-plan filtering (not an action)

Before actions are planned, `is_system=true` resources are
filtered out entirely. They are not included in the action
list. Saga overrides (tenant saga with same code as platform
default) are identified by checking `is_system=false` with a
non-nil `platform_ref` - these are tenant-owned and included
in the diff normally.

#### Resource matching

For each resource code in the desired manifest:

- **Not in live state** - Action: CREATE
- **In live state, matching fields and status** - NO_CHANGE
- **In live state, different fields** - UPDATE

For each tenant-owned resource in live state but NOT in the
desired manifest:

- Action: DEPRECATE

### Handler Idempotency Contract

Every handler must follow this contract:

```text
Given: handler is called with (action, resource_data)

When: resource already exists in the target state
Then: return success (no-op)

When: resource exists in a compatible intermediate state
Then: complete the transition (e.g., DRAFT -> ACTIVE)

When: resource doesn't exist
Then: create it and transition to target state

When: resource exists in an incompatible state
Then: return a clear error
```

This is proactive idempotency (check state first, then act)
rather than reactive (try and catch errors).

### Partial Success Handling

If a saga partially succeeds:

- Record the manifest version with `apply_status = 'PARTIAL'`
  and per-phase status
- On retry, the live-state diff will see what was already applied
  and compute only remaining actions
- No special "resume" logic needed - convergent diff handles
  this naturally

## Dependencies

- `ListAccountTypes` needs an all-statuses variant (or rename
  existing to clarify)
- Config-only resources (OG connections, routes, data sources)
  need lifecycle states for deprecation support (Phase 4)
- `is_system` flag must be consistently present in list endpoint
  responses (verify across all services)

## Success Criteria

1. Applying the same manifest twice in succession always succeeds
   (no errors, no side effects)
2. Applying a manifest after a partial failure completes the
   convergence (no manual intervention)
3. Removing a resource from a manifest results in deprecation,
   not deletion
4. Platform defaults are never modified or removed by tenant
   manifest apply
5. The develop and demo CI deploy pipelines succeed on every push
   without seed failures

## Complexity Estimate

| Phase | Estimate | Parallelizable |
|-------|----------|----------------|
| Phase 1: Handler idempotency fix | 3 pts | Independent |
| Phase 2: Live-state diff | 8 pts | Needs ListAccountTypes |
| Phase 3: Plan-driven saga | 5 pts | After Phase 2 |
| Phase 4: Deprecation lifecycle | 5 pts | Parallel to 2/3 |

**Total: 21 pts.** Critical path: Phase 1 (3) then Phase 2 (8)
then Phase 3 (5) = 16 pts. Phase 4 parallelizes.

Phase 1 can ship independently and unblocks the CI deploy
failures immediately.

## Related Workstreams

- **PRD-045: Manifest as Sole Source of Truth** - Establishes the
  control plane as owner of structural declarations. This PRD
  extends that model by querying services for current state
  during diff computation.
- **PRD-058: Full Economy Visibility** - Surfaces platform
  defaults in the UI. V0.5/V1 are frontend-only (no dependency
  on this work). V1-backend and V2 share the `is_system` field
  and list endpoint surface.
- **Deploy workflow fixes** (#2063, #2068, #2069) -
  Infrastructure fixes for database provisioning in CI. These
  are operational fixes; this PRD addresses the underlying
  architectural gap they exposed.
