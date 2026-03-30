---
name: saga-contract-specification
description: Formal specification for how distributed services transact together using declarative saga patterns
triggers:
  - Designing new saga workflows or cookbook patterns
  - Defining handler schemas or compensation strategies
  - Evaluating Meridian's saga system against alternatives (Temporal, Step Functions)
  - Understanding the Saga Contract model for sales or technical positioning
instructions: |
  The Saga Contract defines how services declare capabilities,
  compose into transactions, and recover from failure.
  handlers.yaml is the contract. Starlark scripts express the
  happy path. Compensation is a service schema property, not a
  workflow concern. All execution is forward-only and append-only.
  See docs/spec/saga-contract.md for the full specification.
---

# The Saga Contract Specification

**Version:** 0.1.0-draft
**Status:** Draft
**Authors:** Meridian Platform Team
**Date:** 2026-03-30

## Abstract

The Saga Contract defines a declarative model for expressing distributed
transactions across service boundaries. It specifies how services declare
their capabilities, how workflows compose those capabilities into
transactional sequences, and how failure recovery is handled automatically
through bounded, deterministic, forward-only execution.

The Saga Contract occupies a layer above API specifications (OpenAPI,
AsyncAPI) and below application business logic. Where OpenAPI describes
*how to call a service* and AsyncAPI describes *how to listen to events*,
the Saga Contract describes *how services transact together* - including
forward execution, failure classification, compensation, and composition
of reusable patterns.

**The Saga Contract is what makes an Economy Runtime possible.** By
defining a formal agreement between services about how they participate
in distributed transactions, the contract enables entire economic models
- billing, settlement, reconciliation, multi-asset accounting - to be
expressed declaratively and operated continuously, rather than hand-wired
in imperative code.

For implementation details, see:
- [Starlark Saga Architecture](../architecture/starlark-saga-architecture.md) - component diagrams, data flow, dependency injection
- [Starlark Saga Skill](../skills/starlark-saga.md) - development guide and operational reference

## 1. Design Principles

### 1.1 Forward-Only Immutable Execution

All state changes in a Saga Contract system are append-only. Transactions
are never mutated or deleted - they are compensated by appending new
records that represent the reversal. This is not a design choice for
auditability alone; it is the mechanism that makes data consistency
achievable in high-latency, distributed environments where traditional
locking is impossible.

The orchestration language is constrained to Starlark (a non-Turing-complete
subset of Python) and the expression language to CEL (a side-effect-free
expression evaluator), guaranteeing that every workflow terminates:

- No `while` loops (only `for` over finite iterables)
- No recursion
- No unbounded I/O
- Configurable execution timeout (default: 5 seconds)

**Implication:** Every saga has a provable upper bound on execution time. SLAs can be stated as guarantees, not aspirations.

### 1.2 Schema-Driven Safety

Workflows can only invoke operations declared in the Handler Schema.
The schema is the single source of truth for what operations exist,
what parameters they accept, what they return, and how they compensate.
Code generation from the schema produces type-safe clients that make
invalid calls unrepresentable.

### 1.3 Separation of Concerns in Failure Recovery

Compensation is a property of the **service schema**, not the
**workflow**. Service owners declare how their operations are undone.
Saga authors write only the happy path. The runtime assembles
compensation automatically from schema declarations. This separation
eliminates the class of bugs where a developer forgets to register a
compensation step.

### 1.4 Fail-Safe by Default

Unknown errors are classified as FATAL (non-retryable). The system
compensates rather than retrying indefinitely. This is a deliberate
choice: retrying an unknown error wastes resources and delays recovery.
Known transient conditions (timeouts, connection resets) are explicitly
enumerated for retry.

## 2. Handler Schema

The Handler Schema is the contract itself - the formal agreement between services about their transactional capabilities. Each handler represents a single operation that a service can perform.

### 2.1 Schema Format

```yaml
service: <service-name>
version: "<semver>"

handlers:
  <namespace>.<operation>:
    description: "<human-readable purpose>"
    compensate: "<namespace>.<compensation-operation>"  # OR
    compensation_strategy: auto | none | saga_managed
    external: true | false  # default: false
    proto_ref:
      proto_rpc: "<package>.<Service>/<Method>"
      exposed_params: [<field-names>]
      exposed_returns: [<field-names>]
      param_aliases: {<proto-field>: <starlark-name>}
    params:  # inline format, for composite handlers or services without proto
      <param-name>:
        type: string | int | decimal | bool | enum | map | uuid
        required: true | false
        description: "<purpose>"
        values: [<enum-values>]  # when type is enum
    returns:
      <field-name>:
        type: string | int | decimal | bool | map | uuid
        description: "<purpose>"
```

### 2.2 Handler Naming

Handlers follow the convention `<namespace>.<operation>` where:

- `namespace` identifies the service domain (e.g., `position_keeping`, `current_account`, `financial_gateway`)
- `operation` identifies the specific action (e.g., `initiate_log`, `create_lien`, `dispatch_payment`)

Namespaces map to gRPC service packages. Operations map to RPC methods.

### 2.3 Compensation Declaration

Every handler MUST declare one of:

| Declaration | Meaning |
|---|---|
| `compensate: <handler>` | Named handler is invoked automatically on rollback. Implicitly sets `compensation_strategy: auto`. |
| `compensation_strategy: auto` | Compensation is wired automatically via the `compensate` field. Implicit default when `compensate` is present. |
| `compensation_strategy: none` | No compensation needed. Read-only, idempotent, or itself a compensation handler. |
| `compensation_strategy: saga_managed` | Compensation defined in the saga script, not the schema. For rollback requiring workflow context. |

### 2.4 External Handlers

Handlers marked `external: true` cross the system boundary (e.g., payment gateways, meter communication networks). External handlers:

- Have higher latency expectations
- May not support synchronous compensation
- Require explicit idempotency keys
- Are subject to provider-specific retry policies

### 2.5 Proto-Referenced Type Resolution

When `proto_ref` is present, parameter and return types are resolved from the referenced protobuf RPC at schema load time. This eliminates duplication between the schema and the proto definitions.

- `exposed_params` controls which proto request fields are visible to Starlark (whitelist)
- `exposed_returns` controls which proto response fields are returned to Starlark
- `param_aliases` remap proto field names to saga-friendly names (e.g., `position_id` -> `account_id`)
- Fields not in `exposed_params` receive default values or are set by the runtime (e.g., `tenant_id`, `correlation_id`)

### 2.6 Handler Return Contract

Every handler invocation returns a `map[string]any` to the saga script. The keys available in this map MUST be documented in either:

- `exposed_returns` in the proto_ref (resolved from proto response message), or
- `returns` in the inline format

**This is a formal contract.** Saga scripts depend on specific return keys (e.g., `lien_id`, `log_id`, `booking_log_id`). Removing or renaming a return field is a breaking change to all sagas that consume it.

## 3. Trigger Grammar

Sagas bind to execution triggers. The trigger determines *when* a saga runs.

### 3.1 Trigger Syntax

```ebnf
trigger := trigger-type ":" trigger-value
trigger-type := "scheduled" | "event" | "api" | "webhook"
```

### 3.2 Trigger Types

| Type | Format | Example | Semantics |
|---|---|---|---|
| `scheduled` | `scheduled:<schedule-name>` | `scheduled:payg_topup` | Invoked by the saga scheduler on a configured cadence (cron or interval). Schedule configuration is external to the saga definition. |
| `event` | `event:<event-type>` | `event:position-keeping.transaction-captured.v1` | Invoked when a matching domain event is published. Event type follows `<service>.<event-name>.v<version>` convention. |
| `api` | `api:<path>` | `api:/v1/payments/stripe` | Invoked by an inbound HTTP/gRPC request. Path is relative to the tenant's API namespace. |
| `webhook` | `webhook:<provider>` | `webhook:stripe` | Invoked by an external webhook callback. Provider name maps to a registered webhook adapter. |

### 3.3 Event Filters

Event-triggered sagas MAY include a CEL filter expression that gates execution:

```yaml
sagas:
  - name: consumption_block_tariff
    trigger: "event:position-keeping.transaction-captured.v1"
    filter: "(event.instrument_code == 'KWH_ELEC' || event.instrument_code == 'KWH_GAS') && event.direction == 'DEBIT'"
```

The filter evaluates against the event payload. Only events where the filter returns `true` trigger the saga. Filters MUST be valid CEL expressions and MUST evaluate in < 10ms.

## 4. Orchestration Language

Sagas are expressed in Starlark - a deterministic, non-Turing-complete dialect of Python developed by Google for Bazel build configurations.

### 4.1 Saga Structure

```python
# Header metadata (comments, parsed by tooling)
# Saga: <name>
# Version: <semver>
# Previous: <previous-version> | none
# Changed: <changelog entry>

<name>_saga = saga(name="<name>")

def execute_<name>():
    ctx = input_data          # dict - runtime-injected input parameters

    step(name="<step-name>")  # checkpoint declaration
    result = <namespace>.<operation>(param=value, ...)

    # ... subsequent steps ...

    return {<output-dict>}

output = execute_<name>()
```

### 4.2 DSL Builtins

The following functions are available in the saga execution environment:

| Function | Signature | Purpose |
|---|---|---|
| `saga()` | `saga(name: str) -> Saga` | Declare a saga definition. Must be called exactly once per script. |
| `step()` | `step(name: str) -> None` | Declare a named execution checkpoint. Steps are recorded for compensation ordering (LIFO). |
| `posting()` | `posting(account_id, amount, direction, instrument_code, ...) -> dict` | Composite handler that creates a ledger posting (position + accounting entries). |
| `cel_eval()` | `cel_eval(expression: str, variables: dict) -> any` | Evaluate a CEL expression with the given variable bindings. |
| `resolve_account()` | `resolve_account(reference: str) -> str` | Resolve an account reference to an account ID. |
| `resolve_instrument()` | `resolve_instrument(code: str) -> dict` | Resolve an instrument code to its full definition. |
| `build_org_account_ref()` | `build_org_account_ref(party_id, org_id, currency) -> str` | Build an organization-scoped account reference from party, org, and currency. |
| `invoke_saga()` | `invoke_saga(name: str, input: dict) -> dict` | Invoke a child saga. Circular invocation is detected and rejected. |
| `fail()` | `fail(reason: str) -> never` | Explicitly fail the saga with a reason. Triggers compensation. |
| `log()` | `log(message: str) -> None` | Emit an audit log entry (routed to the audit trail, not stdout). |
| `Decimal()` | `Decimal(value: str) -> Decimal` | Create an arbitrary-precision decimal value. |

### 4.3 Blocked Operations

The following are explicitly unavailable in the saga environment:

- `load()` - no module imports (all capabilities provided via service modules)
- `print()` - redirected to audit logger via `log()`
- `time.now()` - non-deterministic; timestamps injected via `input_data` or `knowledge_at`
- `random()` - non-deterministic
- `exec()`, `compile()`, `open()` - arbitrary code execution / filesystem access
- `while` loops - unbounded iteration
- Recursive function definitions - unbounded call depth

### 4.4 Service Module Injection

Handler namespaces are injected as Starlark modules at runtime. These modules are auto-generated from the Handler Schema (Section 2), providing type-safe method calls:

```python
# Auto-generated from handlers.yaml
# position_keeping module exposes:
#   .initiate_log(account_id, amount, instrument_code, direction, ...)
#   .update_log(log_id, ...)
#   .cancel_log(log_id, ...)

result = position_keeping.initiate_log(
    account_id="acc-123",
    amount=Decimal("100.00"),
    instrument_code="GBP",
    direction="CREDIT",
)
# result is a dict with keys defined by exposed_returns
```

Parameters are coerced from Starlark types to Go/proto types by the service module runtime:

| Starlark Type | Go Type | Proto Type |
|---|---|---|
| `str` | `string` | `string` |
| `int` | `int64` | `int64` |
| `Decimal(...)` | `shopspring/decimal.Decimal` | `string` (decimal encoding) |
| `bool` | `bool` | `bool` |
| `str` (enum) | `string` | validated against proto enum |
| `str` (uuid) | `uuid.UUID` | `string` (UUID encoding) |

### 4.5 Input and Output

- **Input:** The `input_data` global is a `dict` injected by the runtime containing the saga's input parameters. The shape of `input_data` is defined by the saga's trigger context.
- **Output:** The saga script MUST assign its return value to the `output` global. The output is a `dict` containing the saga's result.

### 4.6 Deterministic Execution

Saga scripts MUST be deterministic - the same `input_data` MUST produce the same sequence of handler calls with the same parameters (modulo handler return values). This enables:

- **Replay:** Failed sagas can be replayed from any checkpoint
- **Audit:** The execution trace is fully reproducible
- **Testing:** Sagas can be validated with recorded handler responses

## 5. Error Classification and Recovery

### 5.1 Error Categories

All errors from handler invocations are classified into exactly one of two categories:

| Category | Meaning | Action |
|---|---|---|
| **FATAL** | Non-retryable. Business rule violation, validation failure, authorization denied, resource not found. | Compensate immediately (LIFO). |
| **TRANSIENT** | Retryable. Network timeout, connection reset, service unavailable, rate limiting, deadlock. | Retry with backoff, then compensate if retries exhausted. |

### 5.2 Classification Rules

Classification follows a priority order:

1. **Explicit wrapper** - Error wrapped as `TransientError` or `FatalError` by the handler
2. **Sentinel match** - Error matches a known sentinel (e.g., `ErrInsufficientFunds`, `ErrAccountClosed`)
3. **FATAL pattern match** - Error message contains a fatal pattern (e.g., "insufficient funds", "validation failed", "not found", "unauthorized")
4. **TRANSIENT pattern match** - Error message contains a transient pattern (e.g., "timeout", "connection refused", "rate limit", "deadlock")
5. **Default: FATAL** - Unknown errors are treated as fatal. Fail-safe: don't retry what you don't understand.

### 5.3 FATAL Sentinel Errors

The following conditions are always classified as FATAL:

- `insufficient funds` / `insufficient balance`
- `account closed` / `account frozen`
- `invalid amount`
- `duplicate transaction`
- `business rule violation`
- `validation failed`
- `not found` / `does not exist`
- `unauthorized` / `forbidden` / `permission denied`
- `constraint violation` (unique, foreign key, check, null)

### 5.4 TRANSIENT Patterns

The following conditions are always classified as TRANSIENT:

- Network: `timeout`, `connection refused/reset/closed`, `network error`, `dns lookup failed`, `eof`, `broken pipe`
- Availability: `service unavailable`, `temporarily unavailable`, `circuit breaker`
- Throttling: `too many requests`, `rate limit`, `throttle`
- Concurrency: `deadlock`, `lock timeout`, `could not serialize`, `serialization failure`
- Context: `deadline exceeded`, `context canceled`

### 5.5 Saga State Machine

```text
PENDING --> RUNNING --> COMPLETED
                |
                +--> WAITING_FOR_EVENT --> RUNNING (on resume)
                |         |
                |         +--> FAILED (on timeout)
                |
                +--> SUSPENDED --> RUNNING (on resume)
                |
                +--> COMPENSATING --> COMPENSATED
                |         |
                |         +--> FAILED (unrecoverable)
                |
                +--> FAILED_MANUAL_INTERVENTION (max retries exceeded)
```

| State | Description | Transitions |
|---|---|---|
| `PENDING` | Created, not yet executing | -> `RUNNING` |
| `RUNNING` | Executing forward steps | -> `COMPLETED`, `WAITING_FOR_EVENT`, `SUSPENDED`, `COMPENSATING`, `FAILED_MANUAL_INTERVENTION` |
| `WAITING_FOR_EVENT` | Suspended awaiting external callback (Section 6.5) | -> `RUNNING` (on resume), `FAILED` (on timeout) |
| `SUSPENDED` | Temporarily paused for async processing | -> `RUNNING` (on resume), `FAILED` (on timeout) |
| `COMPLETED` | All steps succeeded | Terminal |
| `COMPENSATING` | Executing compensation (LIFO order) | -> `COMPENSATED`, `FAILED` |
| `COMPENSATED` | All compensation steps succeeded | Terminal |
| `FAILED` | Compensation itself failed or suspended saga timed out | Terminal (requires manual intervention) |
| `FAILED_MANUAL_INTERVENTION` | Max retries exceeded on transient errors | Terminal (requires operator review) |

### 5.6 Compensation Order

Compensation executes in **LIFO** (Last-In-First-Out) order. Only steps that completed successfully are compensated. Steps that failed do not require compensation (the operation did not take effect).

For each completed step, the compensation handler is determined by:

1. The `compensate` field in the Handler Schema (automatic)
2. The `compensation_strategy: saga_managed` declaration (saga script defines inline)
3. `compensation_strategy: none` (no compensation needed)

### 5.7 Compensation Failure

If a compensation step fails:

1. The error is classified using the same rules (Section 5.2)
2. TRANSIENT errors are retried (up to max compensation retries)
3. FATAL errors in compensation transition the saga to `FAILED`
4. A `FAILED` saga requires manual operator intervention - the system cannot automatically recover

## 6. Durable Execution Model

The Saga Contract requires that saga execution survives process failures, network partitions, and infrastructure restarts. This section specifies the durability mechanisms that make forward-only execution reliable.

### 6.1 Saga Persistence

Every saga instance is persisted with sufficient state to resume execution from any checkpoint:

| Field | Purpose |
|---|---|
| `saga_instance_id` | Unique execution identifier |
| `saga_definition_id` | Reference to the saga version being executed |
| `status` | Current state machine position (Section 5.5) |
| `current_step_index` | Last completed step (resume point) |
| `input_snapshot` | Immutable copy of input_data at saga creation |
| `replay_count` | Number of recovery attempts |
| `correlation_id` | Distributed tracing linkage |
| `causation_id` | Parent-child saga linkage |
| `claimed_by_pod` | Which process owns this execution |
| `lease_expires_at` | When the ownership lease expires |

### 6.2 Step Result Caching

Each completed step's result is persisted with an idempotency key (`saga_{instance_id}_step_{index}`). On replay, the runtime returns cached results for previously completed steps rather than re-executing them. This guarantees exactly-once semantics for handler invocations, even across process restarts.

### 6.3 Lease-Based Ownership

Saga execution is owned by a single process at a time, enforced through database-level leases:

- A process claims a saga by atomically setting `claimed_by_pod` and `lease_expires_at`
- Claims use `FOR UPDATE` row locking to prevent race conditions
- If a process crashes, its lease expires and the saga becomes claimable by another process
- Jitter (0-500ms) on claim timing prevents thundering herd when multiple processes recover simultaneously

### 6.4 Orphan Detection and Recovery

A background watcher periodically scans for orphaned sagas - executions whose owning process has died (lease expired, `claimed_by_pod` is NULL). Orphaned sagas are re-claimed and resumed from their last completed step. Sagas that exceed a maximum replay count transition to `FAILED_MANUAL_INTERVENTION` rather than retrying indefinitely.

### 6.5 Suspend and Resume

Sagas can suspend execution while waiting for external events (e.g., payment confirmations, meter readings, webhook callbacks):

- The saga transitions to `SUSPENDED` with a reason and context data
- A configurable timeout auto-fails suspended sagas that are never resumed
- External systems resume the saga by matching the idempotency key
- The timeout worker polls for expired suspensions (default: 1 minute interval)

### 6.6 Transactional Outbox

Saga state changes and domain events are published atomically using the transactional outbox pattern:

- Events are written to an outbox table within the same database transaction as the saga state update
- A background worker reads pending outbox entries and publishes them to the event bus (Kafka)
- `SELECT FOR UPDATE SKIP LOCKED` prevents concurrent workers from processing the same entry
- Failed publications are retried with a maximum retry count before marking as failed
- Stuck entries (processing state exceeding a timeout) are reset for reprocessing

This ensures that saga state and published events are always consistent - no saga completes without its events being published, and no events are published without the saga state being updated.

### 6.7 Lookup Result Caching for Deterministic Replay

External lookups (`resolve_account`, `resolve_instrument`, `market_information.get_rate`) return values that may change between the original execution and a replay. To maintain deterministic execution, lookup results are captured during the first execution and replayed from cache on subsequent attempts.

## 7. Expression Language (CEL)

CEL (Common Expression Language) is used for stateless evaluation in two contexts:

### 7.1 Preconditions

Saga definitions MAY include a `preconditions_expression` that is evaluated before execution begins. If the expression returns `false`, the saga is not executed.

```yaml
preconditions_expression: "input.amount > 0 && input.currency in ['GBP', 'USD', 'EUR']"
```

### 7.2 Inline Evaluation

Saga scripts can evaluate CEL expressions via the `cel_eval()` builtin:

```python
is_eligible = cel_eval(
    "account.balance > threshold && account.status == 'ACTIVE'",
    {"account": account_data, "threshold": Decimal("100.00")}
)
```

### 7.3 CEL Environment

CEL expressions have access to:

| Variable | Type | Source |
|---|---|---|
| `input` | `map` | Saga input parameters |
| `ctx` | `map` | Saga context (execution_id, correlation_id, tenant_id) |
| `event` | `map` | Event payload (for event-triggered sagas) |

### 7.4 CEL Constraints

- Expressions MUST be stateless (no side effects)
- Expressions MUST evaluate in < 10ms
- Expressions cannot invoke handlers or modify saga state
- Only standard CEL functions are available (no custom extensions)

## 8. Metadata Propagation

Every handler invocation carries metadata through the execution context:

| Field | Source | Purpose |
|---|---|---|
| `correlation_id` | Generated at saga creation | Links all operations in a saga for distributed tracing |
| `idempotency_key` | `saga_{instance_id}_step_{index}` | Prevents duplicate operations on retry. Key is stable across retries to ensure exactly-once semantics. |
| `knowledge_at` | Set at saga start | Bi-temporal timestamp ensuring consistent reads across all steps |
| `tenant_id` | Extracted from request context | Multi-tenant isolation |
| `party_id` | Extracted from input_data or request context | Party-level visibility scope |

Metadata is propagated automatically by the service module runtime. Saga scripts do not need to manage metadata explicitly.

## 9. Composition Model

### 9.1 Child Sagas

Sagas can invoke other sagas via `invoke_saga()`:

```python
step(name="run_settlement")
settlement_result = invoke_saga(
    name="energy_settlement",
    input={"account_id": account_id, "period": period}
)
```

Child sagas:

- Execute within the parent's correlation context
- Are compensated if the parent compensates (cascading compensation)
- Cannot create circular invocation chains (detected at invocation time)
- Track parent relationship via `parent_saga_id` and `parent_step_index`

### 9.2 Cookbook Patterns

Patterns are reusable, composable saga templates with declared dependencies and compatibility:

```json
{
    "name": "<pattern-name>",
    "meta": {
        "provides": {
            "instruments": ["..."],
            "account_types": ["..."],
            "sagas": ["..."],
            "valuation_rules": ["..."],
            "triggers": ["..."]
        },
        "requires": {
            "instruments": ["..."],
            "market_data": ["..."]
        },
        "composes_with": ["<compatible-patterns>"],
        "conflicts_with": ["<incompatible-patterns>"],
        "extends": ["<base-patterns>"]
    }
}
```

### 9.3 Pattern Composition Rules

- A pattern's `requires` MUST be satisfied by either the platform defaults or another pattern's `provides`
- Patterns listed in `conflicts_with` MUST NOT be active in the same tenant simultaneously
- Patterns listed in `extends` MUST be active - they provide base capabilities the pattern depends on
- `composes_with` is advisory - indicates tested compatibility but does not enforce it

### 9.4 Manifest Fragments

Each pattern contributes a manifest fragment that declares:

- **Instruments** - asset types the pattern introduces
- **Account types** - ledger account categories with normal balance direction and allowed instruments
- **Valuation rules** - how instruments are valued (fixed rate, spot rate, formula)
- **Saga bindings** - which sagas are active and what triggers them
- **Policies** - validation rules and bucketing expressions (CEL)

Fragments are merged into the tenant's complete manifest at deployment time. The assembled manifest is what the Economy Runtime continuously operates.

## 10. Saga Definition Lifecycle

Saga definitions are versioned and follow a managed lifecycle:

| Status | Meaning |
|---|---|
| `DRAFT` | Editable, not executable. Used during development and testing. |
| `ACTIVE` | Live for execution. Immutable - changes require a new version. |
| `DEPRECATED` | Phased out. Running instances complete, no new executions. May specify a `successor_id` pointing to the replacement version. |

### 10.1 Versioning

Saga scripts carry version metadata in their header:

```python
# Saga: topup_waterfall
# Version: 1.1.0
# Previous: 1.0.0
# Changed: Added debt recovery rate clamping (Ofgem compliance)
```

Version transitions:

- `DRAFT` -> `ACTIVE`: Requires passing schema validation (all handler calls match Handler Schema)
- `ACTIVE` -> `DEPRECATED`: Sets `successor_id` to the new version
- `DRAFT` -> `DRAFT`: Editable, version bump not required
- `ACTIVE` -> `ACTIVE`: Not allowed (immutable; create new version)

## 11. Safety Guarantees

A conforming implementation provides the following guarantees:

| Guarantee | Mechanism | Section |
|---|---|---|
| **Termination** | Starlark (no while/recursion) + execution timeout | 1.1, 4.3 |
| **Type safety** | Handler Schema + service module code generation | 2, 4.4 |
| **Compensation completeness** | Every handler declares its compensation strategy | 2.3 |
| **Exactly-once execution** | Step result caching with idempotency keys | 6.2 |
| **Crash recovery** | Lease-based ownership + orphan detection | 6.3, 6.4 |
| **Event consistency** | Transactional outbox pattern | 6.6 |
| **Deterministic replay** | Lookup result caching + blocked non-determinism | 4.6, 6.7 |
| **Idempotency** | Deterministic key generation per step/retry | 8 |
| **Tenant isolation** | Metadata propagation of tenant_id and party_id | 8 |
| **Audit trail** | All handler invocations recorded with correlation context | 8 |
| **Bounded expression evaluation** | CEL < 10ms, no side effects | 7.4 |
| **Composition safety** | Circular saga detection, pattern conflict detection | 9.1, 9.3 |

## 12. Relationship to Other Specifications

| Specification | What it defines | Saga Contract's relationship |
|---|---|---|
| **OpenAPI** | How to call a service (HTTP) | Saga Contract handlers MAY reference OpenAPI operations for external services |
| **AsyncAPI** | How to listen to events | Saga Contract event triggers consume events described by AsyncAPI |
| **Protobuf/gRPC** | Service interface contracts | Saga Contract handlers reference proto RPCs for type resolution |
| **CloudEvents** | Event envelope format | Saga Contract event triggers consume CloudEvents payloads |
| **Saga Contract** | **How services transact together** | Orchestration, failure recovery, composition, durable execution |

## 13. Conformance

An implementation conforms to this specification if it:

1. Implements the Handler Schema format (Section 2) as the single source of truth for handler capabilities
2. Generates type-safe service modules from the schema (Section 4.4)
3. Executes saga scripts in a sandboxed Starlark environment with only the declared builtins (Section 4.2, 4.3)
4. Classifies errors according to the priority rules (Section 5.2)
5. Compensates in LIFO order using declared compensation handlers (Section 5.6)
6. Persists saga state and step results for crash recovery (Section 6)
7. Guarantees exactly-once handler execution via idempotency keys (Section 6.2)
8. Publishes events atomically with state changes via transactional outbox (Section 6.6)
9. Propagates metadata on every handler invocation (Section 8)
10. Detects circular saga invocations (Section 9.1)
11. Enforces the saga lifecycle state machine (Section 10)

---

## Appendix A: Prior Art

### A.1 The Saga Pattern (Garcia-Molina & Salem, 1987)

The original saga pattern proposed decomposing long-lived transactions
into sequences of sub-transactions, each with a compensating
transaction. The Saga Contract builds on this foundation but addresses
gaps that the original paper and subsequent implementations left open:
how to *declare* compensation rather than code it, how to *guarantee
termination* rather than hope for it, and how to *compose* saga
patterns rather than writing each from scratch.

### A.2 Comparison with Existing Frameworks

| Dimension | Temporal IO | AWS Step Functions | Eventuate / Cadence | **Saga Contract** |
|---|---|---|---|---|
| **Workflow definition** | Imperative code (Go/Java/TS/Python) | JSON state machine | Imperative code (Java/Go) | **Declarative (Starlark)** |
| **Termination guarantee** | No - Turing-complete, relies on timeouts | Yes - finite state machine | No - Turing-complete | **Yes - language-level (non-Turing-complete)** |
| **Compensation model** | Manual code pattern; developer writes rollback in try/catch | Catch blocks in state machine | Manual code pattern | **Schema-declared; service owners define once, saga authors write happy path only** |
| **Service contract** | None native; community `temporal-contract` library exists | IAM-based permissions | None | **Handler Schema (handlers.yaml) - params, returns, compensation pairs, triggers** |
| **Determinism enforcement** | Runtime (replay fails if non-deterministic) | N/A (no replay) | Runtime | **Compile-time (the language cannot express non-determinism)** |
| **AI code generation safety** | Unconstrained - AI generates arbitrary code | JSON only - limited expressiveness | Unconstrained | **Schema-constrained - AI can only call declared handlers with validated types** |
| **Static visualization** | Impossible - execution graph only known at runtime | Yes - state machine is static | Impossible | **Yes - declarative manifest renders as graph before execution** |
| **Trigger model** | SDK call, Schedules, Cron. No native event/webhook triggers | EventBridge, API Gateway, S3 | SDK call | **Scheduled, event (Kafka + CEL filter), API, webhook - declared in manifest** |
| **Durable execution** | Yes - event-sourced replay | Yes - managed state machine | Yes - event-sourced replay | **Yes - lease-based ownership, step caching, transactional outbox** |
| **Pattern library** | No | No (Workflow Studio templates are UI-only) | No | **Yes - Cookbook with composable, dependency-aware patterns** |
| **Expression language** | None built-in | JSONPath (limited) | None | **CEL (< 10ms, stateless, used for preconditions, filters, validation)** |

### A.3 Temporal IO - Detailed Comparison

Temporal is the closest prior art. Both systems provide durable
execution of distributed workflows with failure recovery. The
architectural difference is philosophical:

**Temporal gives developers full Turing-complete power and enforces
correctness at runtime.** If a workflow is non-deterministic, replay
fails. If a developer forgets to register a compensation step, the
saga is incomplete. If a workflow enters an infinite loop, it eventually
hits a timeout. The developer is responsible for correctness; the
platform provides durability.

**The Saga Contract constrains the language to make incorrect programs
inexpressible.** Non-determinism is blocked at the language level, not
detected at runtime. Compensation is declared in the schema, not coded
in the workflow. Termination is guaranteed by the grammar, not enforced
by timeouts. The schema is responsible for correctness; the developer
is responsible for business logic.

This is the difference between a runtime safety net and compile-time
correctness. Temporal catches mistakes. The Saga Contract prevents them.

**Where Temporal is stronger:**
- Language ecosystem - developers use their existing language (Go, Java, TypeScript)
- Maturity - production-proven at scale across many organizations
- Flexibility - Turing-complete workflows can express anything
- Community - large ecosystem of tooling, examples, and support

**Where the Saga Contract is stronger:**
- Compensation by declaration, not by discipline
- Termination by construction, not by timeout
- AI-safe code generation constrained by schema
- Static analyzability - the workflow graph is known before execution
- Pattern composition - reusable, tested patterns with dependency tracking
- Happy-path authoring - saga writers never think about failure recovery

## Appendix B: Complete Handler Catalog

*To be generated from `handlers.yaml` - each handler with its full parameter and return type documentation.*

## Appendix C: Cookbook Pattern Schema

*To be published at a stable URI. Currently defined by the pattern.json structure in Section 9.2.*
