# PRD: Durable Execution Engine

**Status:** Production-Ready
**Version:** 1.0
**Author:** Architecture Team
**ADR Reference:** [ADR-028](../adr/0028-starlark-saga-cel-valuation.md)
**Companion PRD:** [Starlark Saga Orchestration (Core)](./starlark-saga-orchestration-core.md)

---

## 1. Executive Summary

This PRD defines the **Durable Execution Engine** - the "Muscle" that ensures saga
workflows survive pod crashes, handle external events asynchronously, and maintain
perfect auditability. This transforms Meridian from a "Volatile Orchestrator" into
a "Mini-Temporal" system optimized for BIAN-compliant financial ledgers.

### Scope

| In Scope (This PRD) | Out of Scope (See Companion PRD) |
|---------------------|----------------------------------|
| Saga state persistence | Starlark runtime integration |
| Lease-based pod claiming | Step handler registry |
| Replay execution logic | Party isolation & composition |
| Async wait / external events | Reference validation & linting |
| Hot-fixing & zombie recovery | Decimal type for financial math |
| Causation/correlation propagation | CEL integration |

### The Problem Statement

Current saga execution is **volatile** - if a pod dies mid-saga, the transaction
is lost and must be manually recovered. For high-value energy settlements and
wealth management operations, this is unacceptable.

| Pain Point | Business Impact |
|------------|-----------------|
| **Pod crash loses saga state** | Manual intervention for every failure |
| **No replay capability** | Cannot resume from last known state |
| **External waits block pods** | Inefficient resource utilization |
| **No visibility into nested sagas** | Support cannot debug complex workflows |

### The Solution

A durable execution engine that provides:

- **Replay**: Re-execute saga from last completed step after pod restart
- **Lease-based claiming**: Prevent duplicate processing across pods
- **Async wait**: Suspend saga for external events without holding resources
- **Causation tree**: Full visibility into nested saga execution for debugging

**Key Insight**: Ken can trust the system to "finish the job" even if the entire
data center loses power mid-settlement.

---

## 2. Functional Requirements

### FR-1: Durable Execution via Replay

- **Requirement**: Saga execution MUST survive pod restarts through **Replay**
- **Mechanism**: Re-execute Starlark script, return cached results for completed steps
- **State location**: `saga_instances` and `saga_step_results` tables (service-local)
- **Guarantee**: If a pod dies at step N, another pod resumes at step N

### FR-2: Strict Determinism

- **Requirement**: Replayed sagas MUST produce identical results given identical inputs
- **Enforcement**:
  - No `time.now()` - use `ctx.knowledge_at`
  - No `random()` - use `ctx.new_uuid()` (deterministic)
  - No external I/O - use step handlers
- **Bi-temporal context**: All queries use `knowledge_at` from original execution

### FR-3: Step Handler Output Contracts

- **Requirement**: Step handlers MUST return structured, typed responses
- **Format**: Starlark Struct (immutable) - not Dict (mutable)
- **Validation**: Output schema validated at handler registration
- **Benefit**: Scripts can rely on consistent result structure across replays

### FR-4: Simulation Mode Boundary

- **Requirement**: `ctx.is_simulation` flag MUST prevent real side effects
- **Enforcement**: Step handlers check flag and skip external calls in simulation
- **Audit**: Simulated executions logged separately from production

### FR-5: Causation ID Propagation

- **Requirement**: Runtime MUST automatically inject `causation_id` into step handler calls
- **Compensation linking**: Compensation steps receive parent step's `causation_id`
- **Audit trail**: All "Do" and "Undo" actions linked via causation chain
- **See also**: FR-8 for `correlation_id` (groups entire business operation)

### FR-6: Side-Effect Idempotency Enforcement

- **Requirement**: Step handlers for external integrations MUST declare idempotency
- **Categories**:
  - `EXTERNAL_SUPPORTED`: External system accepts idempotency keys
  - `EXTERNAL_NOT_SUPPORTED`: Requires Pre-Step Check via `verify_external_state()`
- **ACTIVATION gate**: Non-idempotent external handler without Pre-Step Check blocks activation
- **Idempotency key format**: `saga_{instance_id}_step_{step_index}`

### FR-7: Zombie Saga Detection

- **Requirement**: Sagas that exceed `MAX_REPLAYS` (default: 5) MUST transition to
  `FAILED_MANUAL_INTERVENTION`
- **Alert**: Zombie detection triggers P1 alert to Incident Response Runbook
- **Admin action**: Operator uses Admin API to hot-fix or force-complete
- **Audit**: All zombie transitions and interventions logged

### FR-8: Correlation ID Propagation

- **Requirement**: Runtime MUST propagate `correlation_id` from trigger event through
  entire saga lifecycle
- **Distinction from causation_id**:
  - `causation_id`: Links cause→effect within saga
  - `correlation_id`: Groups ALL related actions across entire business operation
- **Use case**: Energy settlement splits into Retail, Wholesale, Tax entries grouped by `correlation_id`
- **Schema**: Add `correlation_id UUID NOT NULL` to `saga_instances`

### FR-9: Progress Emission for UI Integration

- **Requirement**: Starlark scripts MUST emit non-blocking progress updates via `ctx.emit_progress()`
- **Implementation**: Progress published to Kafka topic `saga.progress.{tenant_id}`
- **Consumer**: Mobile/Edge apps show real-time settlement status
- **Non-blocking**: No database write; fire-and-forget
- **Idempotency**: Safe to emit same progress on replay

### FR-10: Deterministic UUID Generator

- **Requirement**: Runtime MUST provide `ctx.new_uuid()` builtin
- **Implementation**: Version 5 UUIDs (RFC 4122)
  - Namespace: `SagaInstance.ID`
  - Name: `"{StepIndex}:{CallIndex}"`
- **Stability**: Same saga replaying same step produces identical UUIDs
- **See also**: FR-11 for seed reset on replay

### FR-11: Deterministic UUID Seed Reset on Replay

- **Requirement**: `ctx.new_uuid()` generator MUST reset to consistent seed at step start
- **Problem**: Mid-step crash must produce same UUIDs on retry
- **Implementation**: Seed = `SHA256(saga_instance_id + step_index)`, CallIndex reset to 0
- **Guarantee**: Same saga, same step, same call sequence = identical UUIDs

### FR-12: Saga Hot-Fixing for Zombie Recovery

- **Requirement**: Admin API MUST allow hot-fix of stuck sagas via definition re-pointing
- **Bi-temporal model**:
  1. Deploy fixed saga definition as new version
  2. Update stuck instance's `saga_definition_id` to new version
  3. Reset `replay_count`, set status to `PENDING`
  4. Resume: cached `saga_step_results` respected, failed step re-executes
- **Audit trail**: Captures operator, reason, old/new version
- **Guard rails**: Only available for `FAILED_MANUAL_INTERVENTION` status

### FR-13: Reactive Orphan Wake-up (Fast-Path Resumption)

- **Requirement**: System SHOULD use reactive event signal for immediate orphan detection
- **Problem**: 5-minute lease expiry too slow for high-volume transactions
- **Implementation options**: PostgreSQL `LISTEN/NOTIFY`, NATS, K8s pod termination webhook
- **Latency target**: Resume orphaned saga within 10 seconds of pod crash
- **Fallback**: Background scan at lease expiry interval

### FR-14: Step Error Severity Classification

- **Requirement**: Step handlers MUST return error category
- **Categories**:
  - `TRANSIENT` (network timeout): Increments `replay_count`, triggers retry
  - `FATAL` (insufficient funds): Transitions to `COMPENSATING` immediately
- **Benefit**: Prevents wasted retries on unrecoverable errors
- **Schema**: Add `error_category VARCHAR(16)` to `saga_step_results`

### FR-15: Valuation Snapshot Stability

- **Requirement**: `valuate_batch()` MUST be treated as side-effecting step for replay
- **Persistence**: Full valuation response (observation IDs, rates) stored in `saga_step_results`
- **Replay**: Return cached result instead of re-calling Valuation Engine
- **Audit continuity**: 7-year audit replay works even after MIM data purge

### FR-16: Async/Wait Pattern for External Events

- **Requirement**: Runtime MUST support `ctx.suspend(idempotency_key)` for long waits
- **Behavior**:
  1. Save current state to `saga_step_results`
  2. Release pod lease
  3. Transition status to `WAITING_FOR_EVENT`
- **Resume**: `CompleteSagaStep(saga_id, idempotency_key, result)` gRPC endpoint
- **Timeout**: Optional `ctx.suspend(key, timeout=duration)` auto-fails after deadline

### FR-17: Deep-Copy Serialization Boundary

- **Requirement**: When persisting `SagaStepResult.output_snapshot`, Go runtime MUST
  serialize to JSON and deserialize back
- **Problem**: Pointer to Go map modified by Starlark corrupts persisted state
- **Implementation**: `output_snapshot = json.Unmarshal(json.Marshal(starlarkValue))`
- **Guarantee**: Replayed steps receive fresh, immutable copy

### FR-18: Causation Tree Visualization API

- **Requirement**: `saga_execution_log` MUST support recursive query for causation tree
- **Use case**: Debug nested saga failures (parent → step → child → step)
- **API**: `GET /admin/sagas/{id}/causation-tree`
- **Schema**: Add `parent_saga_id UUID`, `parent_step_index INTEGER` to `saga_instances`

---

## 3. Technical Architecture

### 3.1 Ownership Model

| Component | Location | Rationale |
|-----------|----------|-----------|
| `saga_definitions` | Reference Data (shared) | Definitions are tenant config |
| `saga_instances` | Each service's schema | Execution state is service-local |
| `saga_step_results` | Each service's schema | Step results are service-local |

> **Pattern**: Common schema definition, service-local tables. Each service
> (Payment Order, Current Account, etc.) has its own saga execution state.

### 3.2 Service-Local Execution State Schema

```sql
CREATE TABLE saga_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Saga definition reference
    saga_definition_id UUID NOT NULL,
    saga_name VARCHAR(64) NOT NULL,
    saga_version INTEGER NOT NULL,

    -- Input and context (for replay)
    input_snapshot JSONB NOT NULL,
    party_id UUID NOT NULL,
    knowledge_at TIMESTAMPTZ,

    -- Tracing (FR-5, FR-8, FR-18)
    correlation_id UUID NOT NULL,
    causation_id UUID,
    parent_saga_id UUID,
    parent_step_index INTEGER,

    -- Async/Wait (FR-16)
    suspend_idempotency_key VARCHAR(128),
    suspend_timeout_at TIMESTAMPTZ,

    -- Ownership (race condition prevention)
    claimed_by_pod VARCHAR(128),
    claimed_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,

    -- Progress
    current_step_index INTEGER NOT NULL DEFAULT 0,
    replay_count INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    -- PENDING, RUNNING, WAITING_FOR_EVENT, COMPLETED, COMPENSATING, COMPENSATED, FAILED, FAILED_MANUAL_INTERVENTION

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,

    -- Error context
    error_message TEXT,
    failed_step_index INTEGER
);

CREATE TABLE saga_step_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    saga_instance_id UUID NOT NULL REFERENCES saga_instances(id),

    step_index INTEGER NOT NULL,
    step_name VARCHAR(64) NOT NULL,

    -- Result (for replay hydration)
    output_snapshot JSONB NOT NULL,
    error_category VARCHAR(16),  -- TRANSIENT, FATAL, NULL (success)

    -- Tracing
    idempotency_key VARCHAR(128) NOT NULL,
    causation_id UUID NOT NULL,

    -- Timestamps
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL,

    UNIQUE(saga_instance_id, step_index)
);

-- Index for efficient orphaned saga detection
CREATE INDEX idx_saga_instances_lease_expires
    ON saga_instances(lease_expires_at)
    WHERE status IN ('RUNNING', 'PENDING', 'COMPENSATING');

-- Partition-aware index for high-volume (100k TPS)
CREATE INDEX idx_saga_instances_orphan_by_tenant
    ON saga_instances(tenant_id, lease_expires_at)
    WHERE status IN ('RUNNING', 'PENDING', 'COMPENSATING');
```

### 3.3 Transaction Affinity (Critical)

The most important guarantee: **step result and index update are atomic**.

```go
func (r *Runtime) executeStep(ctx context.Context, saga *SagaInstance, step Step) error {
    // BEGIN TRANSACTION
    tx, _ := r.db.BeginTx(ctx, nil)
    defer tx.Rollback()

    // Execute step handler
    result, err := r.handlers.Execute(ctx, step, saga.InputSnapshot)
    if err != nil {
        // Determine error category
        category := classifyError(err)
        if category == FATAL {
            saga.Status = "COMPENSATING"
        } else {
            saga.ReplayCount++
        }
        // Persist error state atomically
        r.repo.UpdateInstance(tx, saga)
        return tx.Commit()
    }

    // ATOMIC: Insert result AND update index in same transaction
    r.repo.InsertStepResult(tx, saga.ID, step.Index, result)
    saga.CurrentStepIndex++
    r.repo.UpdateInstance(tx, saga)

    return tx.Commit()
    // END TRANSACTION
}
```

**Why**: Without this atomicity, a crash between inserting the result and updating
the index creates "Ghost Steps" - completed work with no record.

### 3.4 Replay Execution Flow

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                         REPLAY EXECUTION FLOW                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  1. LOAD SAGA INSTANCE                                                      │
│     - Load saga_instances row                                               │
│     - Load saga_definition (from Reference Data)                            │
│     - Load all saga_step_results for this instance                          │
│                                                                             │
│  2. HYDRATE STARLARK VM                                                     │
│     - For each completed step, inject cached result as Struct (immutable)   │
│     - Reset ctx.new_uuid() seed for current step                            │
│                                                                             │
│  3. EXECUTE FROM current_step_index                                         │
│     - Steps 0..N-1: Return cached results (skip handler calls)              │
│     - Step N: Execute handler, persist result atomically                    │
│     - Step N+1..end: Execute normally                                       │
│                                                                             │
│  4. HANDLE FAILURES                                                         │
│     - TRANSIENT: Increment replay_count, retry                              │
│     - FATAL: Transition to COMPENSATING                                     │
│     - replay_count > MAX: Transition to FAILED_MANUAL_INTERVENTION          │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 3.5 Recovery Worker (Staggered Lease Strategy)

```go
func (w *SagaWorker) claimOrphanedSagas(ctx context.Context) {
    // Jitter: 0-500ms random delay prevents thundering herd
    jitter := time.Duration(rand.Intn(500)) * time.Millisecond
    time.Sleep(jitter)

    // Partition-aware: only scan tenants this pod serves
    for _, tenantID := range w.assignedTenants {
        orphans := w.repo.FindOrphanedByTenant(ctx, tenantID, w.claimBatchSize)
        for _, saga := range orphans {
            w.attemptClaim(ctx, saga)
        }
    }
}
```

### 3.6 Async/Wait Flow

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                         ASYNC/WAIT PATTERN                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  SCRIPT:                                                                    │
│    def handle_payment(ctx):                                                 │
│        # ... do work ...                                                    │
│        ctx.suspend("payment_confirmation_123", timeout="24h")               │
│        # Execution pauses here until external callback                      │
│        confirmation = ctx.suspended_result                                  │
│        # ... continue processing ...                                        │
│                                                                             │
│  RUNTIME:                                                                   │
│    1. ctx.suspend() saves state, releases lease                             │
│    2. Status → WAITING_FOR_EVENT                                            │
│    3. External system calls CompleteSagaStep(saga_id, key, result)          │
│    4. Saga resumes with result injected into ctx.suspended_result           │
│                                                                             │
│  TIMEOUT:                                                                   │
│    - Background worker scans suspend_timeout_at                             │
│    - Expired sagas transition to FAILED                                     │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 4. Critical Implementation Directives

These directives are **mandatory** during implementation.

### A. Recovery Worker: Staggered Lease Strategy

Random jitter (0-500ms) prevents thundering herd on cluster-wide restart.

### B. Step Output Hydration: Immutable Structs

Step handlers MUST return `starlark.Struct` (immutable), not `starlark.Dict` (mutable).
This prevents Starlark scripts from accidentally modifying cached replay results.

### C. Compensation Context: Full Failure Information

Compensation logic receives `failed_step_index`, `error_message`, enabling decisions
between "undo everything" and "partial compensation".

### D. Zombie Alerting: Bi-Temporal Integrity

When hot-fixing, use the **original** `knowledge_at` timestamp, not current time.
This preserves bi-temporal audit trail integrity.

---

## 5. Parallel Work Streams

This PRD is designed for **3 parallel development streams** (2 primary + migration).

### Stream Dependency Graph

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                    COMPANION PRD (Core)                                      │
│  ┌─────────────────┐  ┌─────────────────┐                                   │
│  │  Stream 2       │  │  Stream 3       │                                   │
│  │  RUNTIME        │  │  REGISTRY       │                                   │
│  └────────┬────────┘  └────────┬────────┘                                   │
└───────────┼────────────────────┼────────────────────────────────────────────┘
            │                    │
            └─────────┬──────────┘
                      │
                      ▼
            ┌─────────────────┐
            │  Stream 7       │
            │  DURABLE        │
            │  EXECUTION      │
            │  (Persistence)  │
            └────────┬────────┘
                     │
                     ▼
            ┌─────────────────┐
            │  Stream 8       │
            │  EXTERNAL       │
            │  INTEGRATION    │
            │  (Async/Hot-fix)│
            └────────┬────────┘
                     │
                     ▼
            ┌─────────────────┐
            │  Stream 9       │
            │  MIGRATION      │
            │  (Go → Starlark)│
            └─────────────────┘
```

### Stream 7: Durable Execution (Persistence)

**Owner:** 2 developers
**Depends on:** Core PRD Streams 2, 3

| Task ID | Description | Priority |
|---------|-------------|----------|
| **SAGA-041** | Create `saga_instances` table (service-local) | P0 |
| **SAGA-042** | Create `saga_step_results` table | P0 |
| **SAGA-043** | Implement lease-based claiming (`claimed_by_pod`, `lease_expires_at`) | P0 |
| **SAGA-044** | Implement orphan detection and adoption on pod startup | P0 |
| **SAGA-045** | Implement replay execution (skip completed steps) | P0 |
| **SAGA-046** | Add lease renewal background worker | P1 |
| **SAGA-058** | Implement `ctx.new_uuid()` deterministic UUID generator | P0 |
| **SAGA-061** | Implement reactive orphan wake-up via LISTEN/NOTIFY (FR-13) | P1 |
| **SAGA-062** | Add `correlation_id` propagation (FR-8) | P0 |
| **SAGA-063** | Implement `ctx.emit_progress()` with Kafka publisher (FR-9) | P1 |
| **SAGA-064** | Implement step error severity classification (FR-14) | P0 |
| **SAGA-065** | Add partition-aware orphan scanning (100k TPS) | P2 |
| **SAGA-069** | Implement deep-copy serialization boundary (FR-17) | P0 |

### Stream 8: External Integration (Async/Hot-fix)

**Owner:** 1-2 developers
**Depends on:** Stream 7

| Task ID | Description | Priority |
|---------|-------------|----------|
| **SAGA-047** | Implement `valuate_batch()` with snapshot stability (FR-15) | P1 |
| **SAGA-049** | Define step handler output schemas (typed contracts) | P1 |
| **SAGA-050** | Validate script accesses against handler output schemas | P2 |
| **SAGA-051** | Add `ctx.is_simulation` flag and handler enforcement | P1 |
| **SAGA-052** | Implement automatic `causation_id` propagation | P1 |
| **SAGA-053** | Add idempotency declaration to step handler interface | P0 |
| **SAGA-054** | Implement ACTIVATION fail-fast for non-idempotent handlers | P0 |
| **SAGA-055** | Implement zombie saga detection (replay_count > MAX) | P0 |
| **SAGA-056** | Add high-severity alerting for zombie sagas | P1 |
| **SAGA-057** | Implement `ExecuteDryRun` RPC | P1 |
| **SAGA-059** | Implement `verify_external_state()` builtin | P0 |
| **SAGA-060** | Add Admin API for saga hot-fixing | P1 |
| **SAGA-066** | Implement `ctx.suspend()` and WAITING_FOR_EVENT (FR-16) | P1 |
| **SAGA-067** | Implement `CompleteSagaStep` gRPC endpoint | P1 |
| **SAGA-068** | Add suspend timeout worker | P2 |
| **SAGA-070** | Add causation tree recursive query and Admin API (FR-18) | P1 |

### Stream 9: Migration (Integration)

**Owner:** 1 developer
**Depends on:** All streams

| Task ID | Description | Priority |
|---------|-------------|----------|
| **SAGA-008** | Migrate `withdrawal_orchestrator.go` to Starlark | P0 |
| **SAGA-009** | Migrate `deposit_orchestrator.go` to Starlark | P1 |
| **SAGA-010** | Migrate `payment_orchestrator.go` to Starlark | P1 |
| **SAGA-011** | Implement simulation mode for DRAFT sagas | P1 |
| **SAGA-012** | Create saga execution audit logging | P1 |
| **SAGA-014** | Admin API for saga management | P2 |
| **SAGA-015** | Documentation and tenant onboarding guide | P2 |

---

## 6. Acceptance Criteria

### Durable Execution

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-DE-01** | Saga state persisted before each step execution | Unit test: verify row exists before handler returns |
| **AC-DE-02** | Pod restart resumes saga from last completed step | Integration test: kill pod, restart, verify completion |
| **AC-DE-03** | Replay returns cached results for completed steps | Unit test: verify handler not called |
| **AC-DE-04** | Lease-based claiming prevents duplicate processing | Concurrency test: two pods, only one succeeds |
| **AC-DE-05** | Orphan detection finds sagas with expired leases | Unit test: expire lease, verify orphan query |
| **AC-DE-06** | Idempotency keys prevent duplicate side effects | Integration test: replay, verify single external call |
| **AC-DE-07** | Lease renewal extends expiry while processing | Unit test: verify `lease_expires_at` updated |

### Determinism & Hardening

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-DH-01** | No `time.now()` access in Starlark | Unit test: call fails |
| **AC-DH-02** | All time logic uses `ctx.knowledge_at` | Code review |
| **AC-DH-03** | Step handler returns Struct (immutable) | Unit test: verify type |
| **AC-DH-04** | `ctx.is_simulation` prevents side effects | Integration test: no real transactions |
| **AC-DH-05** | `causation_id` propagated to compensation | Unit test: verify field |
| **AC-DH-06** | `ctx.new_uuid()` produces identical UUIDs on replay | Unit test: replay, compare |

### External Integration & Resilience

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-EI-01** | External handlers declare idempotency capability | Code review |
| **AC-EI-02** | ACTIVATION fails for non-idempotent without Pre-Step Check | Unit test |
| **AC-EI-03** | Zombie saga transitions to FAILED_MANUAL_INTERVENTION | Integration test |
| **AC-EI-04** | Zombie detection triggers P1 alert | Integration test |
| **AC-EI-05** | Transaction affinity: step result + index update atomic | Unit test: inject failure |
| **AC-EI-06** | `verify_external_state()` prevents duplicate calls | Integration test |
| **AC-EI-07** | Saga hot-fix re-points to new definition | Admin API test |
| **AC-EI-08** | Hot-fix preserves bi-temporal integrity | Query test: timestamps |

### Performance & Observability

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-PO-01** | Orphaned saga resumes within 10 seconds | Integration test |
| **AC-PO-02** | Fallback orphan scan works without reactive signal | Integration test |
| **AC-PO-03** | `correlation_id` propagated to all events | Integration test |
| **AC-PO-04** | `ctx.emit_progress()` publishes to Kafka | Integration test |
| **AC-PO-05** | Progress emission non-blocking | Unit test: no DB writes |
| **AC-PO-06** | UUID seed reset at step boundary | Unit test |

### Async Handling & Debugging

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-AD-01** | `ctx.suspend()` transitions to WAITING_FOR_EVENT | Unit test |
| **AC-AD-02** | `CompleteSagaStep` resumes suspended saga | Integration test |
| **AC-AD-03** | Duplicate callbacks deduplicated | Unit test |
| **AC-AD-04** | Suspended saga auto-fails after timeout | Integration test |
| **AC-AD-05** | Serialization boundary prevents memory leakage | Unit test |
| **AC-AD-06** | Causation tree API returns full hierarchy | Integration test |

### Type Safety & Resilience

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-TR-01** | `TRANSIENT` error increments replay_count | Unit test |
| **AC-TR-02** | `FATAL` error transitions to COMPENSATING | Unit test |
| **AC-TR-03** | `valuate_batch()` result cached in step_results | Unit test |
| **AC-TR-04** | Replay of `valuate_batch()` returns cached result | Integration test |

---

## 7. Risks and Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Transaction affinity logic incorrect | High | Medium | Extensive integration testing, chaos testing |
| Orphan detection misses sagas | High | Low | Multiple detection mechanisms, alerts |
| Hot-fix corrupts bi-temporal history | High | Low | Strict audit logging, knowledge_at preservation |
| Async wait leaks resources | Medium | Low | Timeout enforcement, monitoring |

---

## 8. Links

- [ADR-028: Starlark Saga Orchestration](../adr/0028-starlark-saga-cel-valuation.md)
- [Companion PRD: Starlark Saga Orchestration (Core)](./starlark-saga-orchestration-core.md)
- [Temporal.io](https://temporal.io/) - Industry reference for durable execution
- [Flink Stateful Functions](https://nightlies.apache.org/flink/flink-statefun-docs-stable/) - Stateful serverless
