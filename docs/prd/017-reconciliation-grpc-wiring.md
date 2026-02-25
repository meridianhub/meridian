# PRD: Wire Reconciliation gRPC Handlers to Service Layer

## Problem Statement

The reconciliation service has 9 RPCs defined in
`reconciliation.proto` but only 4 are wired to actual service-layer
implementations. The remaining 5 return `codes.Unimplemented` stubs
despite having fully functional service-layer logic available. This
gap was identified during e2e testing (PR #846), where tests had to
bypass gRPC transport entirely and call the service layer directly
because "many RPCs return UNIMPLEMENTED."

The service cannot be exercised through its gRPC API for core
settlement lifecycle operations (create, execute, retrieve, control,
list results), which means:

- gRPC gateway REST endpoints return 501 for core operations
- Integration tests cannot validate the full transport stack
- The e2e test suite tests service logic but not the actual API
  contract

## RPC Status Audit

### Proto-defined RPCs (9 total)

<!-- markdownlint-disable MD013 -->

| # | RPC | Handler Status | Service Logic? | Notes |
|---|-----|---------------|----------------|-------|
| 1 | `InitiateAccountReconciliation` | UNIMPLEMENTED | Partial | `domain.NewSettlementRun()` + repo exist. Need proto mapping. |
| 2 | `ExecuteAccountReconciliation` | UNIMPLEMENTED | Yes | Capturer, Detector, Valuator all exist. Need orchestration. |
| 3 | `RetrieveAccountReconciliation` | UNIMPLEMENTED | Yes | `FindByID()` exists. Proto mapping only. |
| 4 | `ControlAccountReconciliation` | UNIMPLEMENTED | Partial | `Cancel()` exists. Pause/Resume not in domain. |
| 5 | `ListReconciliationResults` | UNIMPLEMENTED | Yes | `VarianceRepository.List()` exists. Need mapping + pagination. |
| 6 | `AssertBalance` | WIRED | Yes | Fully implemented via `BalanceAssertor`. |
| 7 | `InitiateDispute` | WIRED (cond.) | Yes | Returns UNIMPLEMENTED only if `disputeRepo == nil`. |
| 8 | `ControlDispute` | WIRED (cond.) | Yes | Returns UNIMPLEMENTED only if `disputeRepo == nil`. |
| 9 | `RetrieveDispute` | WIRED (cond.) | Yes | Returns UNIMPLEMENTED only if `disputeRepo == nil`. |

<!-- markdownlint-enable MD013 -->

### Summary

- **Fully wired**: 4 (AssertBalance, InitiateDispute,
  ControlDispute, RetrieveDispute)
- **UNIMPLEMENTED with service logic available**: 3
  (RetrieveAccountReconciliation, ListReconciliationResults,
  ExecuteAccountReconciliation)
- **UNIMPLEMENTED with partial service logic**: 2
  (InitiateAccountReconciliation, ControlAccountReconciliation)
- **Total handlers to wire**: 5

## Detailed Analysis Per Handler

### 1. InitiateAccountReconciliation (service.go:107-112)

**Current**: Returns `codes.Unimplemented`

**What exists**:

- `domain.NewSettlementRun(accountID, scope, settlementType,
  periodStart, periodEnd, initiatedBy)` creates a validated domain
  entity
- `domain.SettlementRunRepository.Create(ctx, run)` persists it
- Domain enums: `ReconciliationScope`, `SettlementType` with parsers

**What needs to be done**:

- Map proto request fields to domain types (proto enums -> domain
  enums)
- Call `domain.NewSettlementRun()` + `runRepo.Create()`
- Map domain `SettlementRun` back to proto `SettlementRunSummary`
- Handle idempotency key (if present)

**Dependencies needed on `AccountReconciliationService` struct**:

- `runRepo domain.SettlementRunRepository` (NEW field)

**Complexity**: 3 SP - Straightforward mapping, no business logic.

**Closed Loop consideration**: This handler must support
high-frequency automated invocation from upstream services (e.g.,
Payment Order triggering reconciliation on `payout.paid` webhook).
The `SettlementRunRepository` should support idempotency on
`payment_reference` or equivalent external identifier to prevent
duplicate runs from webhook retries.

### 2. ExecuteAccountReconciliation (service.go:115-120)

**Current**: Returns `codes.Unimplemented`

**What exists**:

- `SnapshotCapturer.CaptureSnapshots(ctx, runID)` - captures PK
  positions
- `VarianceDetector.DetectVariances(ctx, runID)` - compares
  snapshots
- `VarianceValuator.ValueVariances(ctx, runID)` - values variances
- `SettlementRunRepository.FindByID()` - loads run for status
  validation

**What needs to be done**:

- Parse `run_id` from request
- Orchestrate the pipeline: capture -> detect -> value (or delegate
  to background worker)
- Decision: synchronous vs async execution. The current
  `SnapshotCapturer` has a 10-minute timeout, suggesting this is
  designed for async. For gRPC, we should either:
  - (a) Start async and return immediately with RUNNING status, OR
  - (b) Run synchronously if the operation is expected to be fast

**Dependencies needed on `AccountReconciliationService` struct**:

- `runRepo domain.SettlementRunRepository` (shared with #1)
- `snapshotCapturer *SnapshotCapturer` (NEW field)
- `varianceDetector *VarianceDetector` (NEW field)
- `varianceValuator *VarianceValuator` (NEW field)

**Complexity**: 5 SP - Orchestration logic + async execution
decision.

**Closed Loop consideration**: For external reconciliation (e.g.,
Stripe Nostro/Vostro), the `SnapshotCapturer` currently only looks
at internal Position Keeping data. When `scope == NOSTRO_VOSTRO`,
the Execute pipeline may need to trigger an adapter to fetch
external settlement reports or accept a reference to an
already-ingested report. Consider adding an optional
`external_reference_id` field to `ExecuteAccountReconciliationRequest`
to link a specific external report (e.g., Stripe Payout ID) to the
run. This is a follow-up concern and not blocking for the initial
wiring.

### 3. RetrieveAccountReconciliation (service.go:123-128)

**Current**: Returns `codes.Unimplemented`

**What exists**:

- `SettlementRunRepository.FindByID(ctx, runID)` returns
  `*domain.SettlementRun`

**What needs to be done**:

- Parse `run_id` UUID from request
- Call `runRepo.FindByID()`
- Map `*domain.SettlementRun` to proto `SettlementRunSummary`
- Handle not-found -> `codes.NotFound`

**Dependencies needed**: `runRepo` (shared)

**Complexity**: 2 SP - Pure mapping, simplest handler.

### 4. ControlAccountReconciliation (service.go:131-136)

**Current**: Returns `codes.Unimplemented`

**What exists**:

- `SettlementRun.Cancel()` domain method
- `SettlementRunRepository.Update()` for persistence
- Proto `ControlAction` enum: CANCEL, PAUSE, RESUME

**What needs to be done**:

- Wire CANCEL action to `run.Cancel()` + `runRepo.Update()`
- PAUSE and RESUME: Domain model doesn't support these transitions.
  Run status FSM only allows: PENDING->RUNNING,
  RUNNING->COMPLETED/FAILED/CANCELLED, COMPLETED->FINALIZED
- Options for PAUSE/RESUME:
  - (a) Return `codes.Unimplemented` for PAUSE/RESUME specifically
  - (b) Add PAUSED state to domain model (scope creep)

**Dependencies needed**: `runRepo` (shared)

**Complexity**: 3 SP - Cancel is straightforward; PAUSE/RESUME need
a design decision.

### 5. ListReconciliationResults (service.go:139-144)

**Current**: Returns `codes.Unimplemented`

**What exists**:

- `VarianceRepository.List(ctx, VarianceFilter)` with `RunID`,
  `Status`, `Reason`, `Limit`, `Offset` fields
- Domain variance model with all required fields

**What needs to be done**:

- Parse request: `run_id`, `page_size`, `page_token`,
  `filter_status`, `filter_reason`
- Convert `page_token` to offset (encode/decode opaque cursor)
- Build `domain.VarianceFilter` from proto request
- Call `varianceRepo.List()`
- Map `[]*domain.Variance` to proto `[]VarianceDetail`
- Build response with `next_page_token` and `total_count`

**Dependencies needed on `AccountReconciliationService` struct**:

- `varianceListRepo domain.VarianceRepository` (NEW field, different
  from `VarianceFinder` interface which is scoped to dispute
  validation)

**Complexity**: 3 SP - Mapping + pagination logic.

**Closed Loop consideration**: When reconciling external sources
(e.g., Stripe), a variance carries structured context beyond "amount
mismatch" -- fee differences, timing gaps, unexpected refunds.
Ensure the `Variance` entity supports structured metadata (JSONB) so
downstream consumers can understand the specific reason a loop
didn't close. The proto mapping should expose this metadata field.
This may already be supported via the existing `Metadata` field on
the domain model; verify during implementation.

## Missing Infrastructure

### Persistence Repositories

Currently only `DisputeRepository` has a persistence adapter
(`adapters/persistence/dispute_repository.go`). The following
repository implementations are **missing**:

<!-- markdownlint-disable MD013 -->

| Repository Interface | Persistence Impl | Needed For |
|---------------------|-------------------|------------|
| `SettlementRunRepository` | **MISSING** | Initiate, Execute, Retrieve, Control |
| `SettlementSnapshotRepository` | **MISSING** | Execute (via SnapshotCapturer) |
| `VarianceRepository` | **MISSING** | Execute (via VarianceDetector), ListResults |
| `BalanceAssertionRepository` | **MISSING** | AssertBalance (wired but nil guard) |
| `ImbalanceTrendRepository` | **MISSING** | AssertBalance trend tracking |

<!-- markdownlint-enable MD013 -->

**CRITICAL FINDING**: The gRPC handlers cannot be wired without
first implementing the persistence layer for
`SettlementRunRepository` and `VarianceRepository` at minimum. The
database schema (migrations) exists for all tables, but the Go
GORM/SQL adapters haven't been written.

### Main Wiring (cmd/main.go)

The `cmd/main.go` currently:

- Creates the gRPC server
- Registers Health and Reflection services
- Does NOT register `AccountReconciliationService` at all
- Does NOT instantiate any repositories or service-layer components
- Does NOT create the `AccountReconciliationService` instance

This means even the already-wired handlers (AssertBalance, dispute
RPCs) are not accessible in production because the service is never
registered with the gRPC server.

## Proposed Solution

### Phase 1: Persistence Layer (prerequisite) - 8 SP

Implement GORM-based persistence adapters for:

1. `SettlementRunRepository` (3 SP) - CRUD + List with filtering
2. `VarianceRepository` (3 SP) - CRUD + List with filtering + batch
   operations
3. `SettlementSnapshotRepository` (2 SP) - CRUD + batch operations

These follow the pattern established in `dispute_repository.go`.

### Phase 2: Wire gRPC Handlers - 8 SP

1. Add new dependencies to `AccountReconciliationService` struct
   (1 SP)
   - `runRepo`, `snapshotCapturer`, `varianceDetector`,
     `varianceValuator`, `varianceListRepo`
   - Add corresponding `With*` option functions

2. Implement handler bodies (5 SP):
   - `RetrieveAccountReconciliation` (2 SP)
   - `InitiateAccountReconciliation` (3 SP)
   - `ListReconciliationResults` (3 SP)
   - `ControlAccountReconciliation` (3 SP, CANCEL only;
     PAUSE/RESUME return Unimplemented)
   - `ExecuteAccountReconciliation` (5 SP, async orchestration)

3. Proto-to-domain mapping helpers (2 SP):
   - `toProtoRunSummary`
   - `toProtoVarianceDetail`
   - `toDomainScope`
   - `toDomainSettlementType`
   - Pagination cursor encode/decode

**Async execution state machine for
ExecuteAccountReconciliation**:

1. Validate run exists and is in PENDING status
2. Transition run status to RUNNING
3. Acknowledge the gRPC request immediately (return run summary
   with RUNNING status)
4. Spawn background goroutine: capture -> detect -> value
5. On success: transition to COMPLETED (client discovers via
   RetrieveAccountReconciliation)
6. On failure: transition to FAILED with error details persisted
   (since the gRPC client has already disconnected, the DB is the
   only reliable status channel)

### Phase 3: Main Wiring - 3 SP

Update `cmd/main.go` to:

1. Instantiate persistence repositories
2. Create service-layer components (SnapshotCapturer,
   VarianceDetector, VarianceValuator, BalanceAssertor)
3. Create `AccountReconciliationService` with all options
4. Register with gRPC server:
   `reconciliationv1.RegisterAccountReconciliationServiceServer(
   grpcServer, svc)`

### Phase 4: Tests - 5 SP

1. Unit tests for each wired handler (mock repositories)
2. Integration tests for persistence repositories (CockroachDB
   testcontainers)

### Phase 5: Migrate E2E Tests to gRPC Transport - 5 SP

This phase addresses the root cause that triggered this PRD: the
e2e test suite (PR #846) bypasses gRPC transport entirely, calling
the service layer directly. Once handlers are wired, the e2e tests
must be migrated to exercise the full gRPC stack.

**Current state** (`tests/reconciliation-e2e/`):

- 25 tests across 5 files call service-layer methods directly
  (e.g., `infra.capturer.CaptureSnapshots()`,
  `infra.grpcService.InitiateDispute()`)
- The `e2eTestInfra` struct instantiates service components
  in-process without gRPC transport
- Dispute tests already use gRPC (via `infra.grpcService`) but
  settlement lifecycle tests do not

**What needs to change**:

1. Update `e2eTestInfra` to start an in-process gRPC server with
   `AccountReconciliationService` registered (using
   `bufconn` or `grpc.NewServer()` with a test listener)
2. Create a gRPC client connected to the test server
3. Migrate settlement lifecycle tests to use gRPC client:
   - `TestSettlement_FullLifecycle` -> call
     `InitiateAccountReconciliation`, `ExecuteAccountReconciliation`,
     `RetrieveAccountReconciliation`
   - `TestSettlement_CancelRun` -> call
     `ControlAccountReconciliation` with CANCEL
   - `TestSettlement_ListVariances` -> call
     `ListReconciliationResults`
4. Migrate snapshot/variance tests to use Execute RPC instead
   of direct capturer/detector calls
5. Verify proto request/response mapping round-trips correctly
6. Keep existing direct service-layer tests as a fallback
   regression suite (rename with `_direct` suffix) or remove
   if redundant

**Acceptance criteria**:

- All 25 e2e tests pass through gRPC transport
- No test calls service-layer methods directly for operations
  that have a corresponding gRPC handler
- Proto enum mapping is validated end-to-end
- Error codes (NotFound, InvalidArgument, PermissionDenied)
  are verified at the gRPC level

## Total Scope Assessment

| Phase | Story Points | PR Strategy |
|-------|-------------|-------------|
| Phase 1: Persistence | 8 | PR #1 |
| Phase 2: Wire handlers | 8 | PR #2 |
| Phase 3: Main wiring | 3 | PR #2 (same) |
| Phase 4: Unit + integration tests | 5 | PR #2 (same) |
| Phase 5: E2E migration to gRPC | 5 | PR #3 |
| **Total** | **29** | **3 PRs** |

**Recommended PR strategy**: 3 PRs for manageable review units:

- PR 1 (8 SP): Persistence repositories with integration tests
- PR 2 (16 SP): Handler wiring + main.go + unit/integration tests
- PR 3 (5 SP): Migrate e2e tests to gRPC transport (closes the
  loop from PR #846)

## Design Decisions

<!-- markdownlint-disable MD013 -->

| # | Decision | Resolution | Rationale |
|---|----------|-----------|-----------|
| 1 | Execute: sync vs async? | **Async** | 10-min snapshot timeout means sync gRPC will hit deadline. Fire-and-forget with status polling via Retrieve. |
| 2 | Control: PAUSE/RESUME? | **CANCEL only** | Domain FSM has no PAUSED state. Return Unimplemented for PAUSE/RESUME. |
| 3 | List: total_count? | **Return -1** | Repo doesn't return count. Add COUNT query later. |
| 4 | ExternalDataIngester? | **Follow-up** | Placeholder interface only. Implementation out of scope. |

<!-- markdownlint-enable MD013 -->

## Closed Loop Integration Considerations

The following considerations ensure this wiring work is
forward-compatible with the Closed Loop vision (e.g., Stripe
Connect reconciliation). These are **not blocking** for the initial
implementation but should inform design choices.

### External Data Injection (ExecuteAccountReconciliation)

When `scope == NOSTRO_VOSTRO`, the reconciliation pipeline needs to
compare internal positions against external settlement reports
(e.g., Stripe Payout Reports). The current `SnapshotCapturer` only
queries internal Position Keeping.

**Action for this PRD**: Add an optional `external_reference_id`
field to the Execute request proto mapping (if the proto supports
it, otherwise note as a follow-up proto change). The service layer
should accept and persist this reference on the `SettlementRun`
even if the external fetch adapter is not yet implemented.

### Automated Invocation (InitiateAccountReconciliation)

In the Closed Loop, upstream services (Payment Order) will
programmatically trigger reconciliation runs on events like
`payout.paid`. The Initiate handler must tolerate high-frequency
automated calls.

**Action for this PRD**: Ensure
`SettlementRunRepository.Create()` supports idempotency checking
on an external reference field. If a run with the same reference
already exists, return the existing run rather than creating a
duplicate.

### Structured Variance Metadata (ListReconciliationResults)

External reconciliation variances carry structured context beyond
"amount mismatch" -- e.g., fee differences, timing gaps, unexpected
refunds. The proto mapping should expose any existing metadata
field on the Variance domain model.

**Action for this PRD**: During proto mapping implementation, check
if `domain.Variance` has a metadata/context field and map it
through to the proto response. If the domain model lacks this,
note it as a follow-up.

### Reliable Async Status Updates (ExecuteAccountReconciliation)

Since Execute returns immediately (async), the background goroutine
must reliably update DB status to FAILED on error. Without this,
the Closed Loop automation cannot detect failures and trigger
retries.

**Action for this PRD**: The async goroutine must wrap the entire
pipeline in a recover/defer that transitions the run to FAILED
status on any panic or error. Use a separate context (not the gRPC
request context, which is cancelled after response) for the
background work.

## Dependencies and Risks

### Dependencies

- Database schema already exists (migrations present)
- All domain models and repository interfaces are defined
- `DisputeRepository` persistence adapter provides a clear
  implementation pattern

### Risks

- **Low**: Proto-to-domain enum mapping may have gaps (proto has
  more values than domain in some cases)
- **Low**: Pagination cursor encoding needs to be consistent for
  clients
- **Medium**: ExecuteAccountReconciliation async execution needs
  careful error handling and goroutine lifecycle management
- **Low**: CockroachDB-specific SQL considerations for new
  repositories

## Files to Modify

### New Files

- `services/reconciliation/adapters/persistence/settlement_run_repository.go`
- `services/reconciliation/adapters/persistence/settlement_run_repository_test.go`
- `services/reconciliation/adapters/persistence/variance_repository.go`
- `services/reconciliation/adapters/persistence/variance_repository_test.go`
- `services/reconciliation/adapters/persistence/settlement_snapshot_repository.go`
- `services/reconciliation/adapters/persistence/settlement_snapshot_repository_test.go`

### Modified Files

- `services/reconciliation/service/service.go` - Add dependencies,
  implement 5 handlers, add mapping helpers
- `services/reconciliation/cmd/main.go` - Wire everything together,
  register gRPC service

## Appendix: Cross-Service Unimplemented RPC Audit

A scan of all services identified additional unimplemented gRPC
handlers beyond the reconciliation service. These are documented
here for visibility and future planning. They are **out of scope**
for this PRD but represent the full picture of gRPC wiring gaps
across Meridian.

### current-account: Withdrawal Persistence Missing

3 handlers return `codes.Unimplemented` when
`withdrawalRepo == nil`:

- **UpdateWithdrawal** (`grpc_withdrawal_manage.go:173`) -
  "withdrawal persistence not configured"
- **RetrieveWithdrawal** (`grpc_withdrawal_manage.go:262`) -
  Returns empty list for account queries but errors on direct
  withdrawal-by-ID lookup
- **ExecuteWithdrawal** (`grpc_withdrawal_execute.go:54`) -
  Direct withdrawal by account_id/amount works; by withdrawal_id
  fails

**Root cause**: `WithdrawalRepository` domain interface exists but
no persistence adapter is implemented. Service is registered in
`cmd/main.go` and gracefully degrades for account-based operations.

**Severity**: Medium - Withdrawal-by-ID operations fail in
production.

### internal-account: Position Keeping Client Not Wired

- **RetrieveBalances** (`service/server.go:596`) - Returns
  `codes.Unimplemented` when `positionKeepingClient == nil`:
  "position keeping service not configured"

**Root cause**: Position Keeping client interface exists but is not
wired in `cmd/main.go`. Balance operations require PK as source of
truth.

**Severity**: Medium - Balance retrieval non-functional without
PK integration.

### party: KYC/AML Provider Missing

- **ExchangeDemographics** (`grpc_service.go:712`) - Returns
  `codes.Unimplemented` in production mode without external
  provider: "KYC/AML verification not implemented - cannot operate
  in production without external provider integration"
  - Development mode: Returns VERIFIED (stub)
  - `KYC_STUB_ENABLED=true`: Returns VERIFIED (stub)
  - Production without flag: Returns Unimplemented

**Root cause**: External KYC provider integration (Onfido, Jumio,
etc.) not implemented. Intentional production guard.

**Severity**: Medium - By design; requires vendor selection.

### Services With Proto Definitions But No Deployment

The following services have proto-defined RPCs but no `cmd/main.go`
(no deployable binary). These may be planned future services or
library/support code:

<!-- markdownlint-disable MD013 -->

| Service | Proto | Status |
|---------|-------|--------|
| control-plane (AuthService) | `control_plane/v1/auth.proto` | Validation utility only (`cmd/validate/`) |
| control-plane (ManifestHistoryService) | `control_plane/v1/manifest_history_service.proto` | No server binary |
| reference-data (ReferenceDataService) | `reference_data/v1/instrument.proto` | Library service, handler exists |
| saga (SagaAdminService) | `saga/v1/saga_admin.proto` | No server binary |
| saga (SagaRegistryService) | `saga/v1/saga_registry.proto` | No server binary |

<!-- markdownlint-enable MD013 -->

**Severity**: Low - These services are either not yet deployed or
serve as libraries consumed by other services.
