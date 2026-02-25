# Saga Handler Audit Report

**Date**: 2026-02-03
**Scope**: All 13 Meridian services
**Objective**: Identify which services require `client/starlark.go` saga handler implementations

---

## Executive Summary

This audit systematically examines all 13 Meridian services to determine which require Starlark saga handler
implementations (`client/starlark.go`). The analysis is based on:

1. Cross-service client references in Go code
2. Existing saga script usage patterns
3. Service architectural roles (settlement, ingestion, valuation)
4. gRPC method signatures and idempotency characteristics

**Findings**:

- **3 services** require saga handler implementation (HIGH/MEDIUM priority)
- **3 services** already have handlers implemented (no action needed)
- **7 services** do not require handlers (infrastructure/orchestrators/workers/deferred)

---

## Services Requiring client/starlark.go

### 1. internal-account (HIGH PRIORITY)

**Status**: Has `client/client.go` but **missing `client/starlark.go`**

**Evidence**:

- **14 cross-service references** found in:
  - `services/payment-order/service/` (7 files - account resolution, orchestrator, tests)
  - `services/financial-accounting/service/` (2 files - account resolver)
  - `services/current-account/service/` (3 files - account resolver)
  - `services/position-keeping/service/` (2 files - account validator)
- Currently used for **internal clearing account resolution** in payment flows
- **Future saga use case**: Nostro/vostro account management in cross-border payment sagas

**Category**: `HandlerCategorySettlement`
**ProducesInstruments**: `["USD", "EUR", "GBP", "NZD"]` (for nostro/vostro accounts)

**Handlers to Implement**:

| Handler Name | gRPC Method | Category | Idempotent | ProducesInstruments |
|-------------|-------------|----------|------------|-------------------|
| `internal_account.initiate` | `InitiateInternalAccount` | Settlement | No (uses circuit breaker only) | YES - currencies |
| `internal_account.retrieve` | `RetrieveInternalAccount` | Settlement | Yes (with retry) | NO - read-only |
| `internal_account.get_balance` | `GetBalance` | Settlement | Yes (with retry) | NO - read-only |
| `internal_account.list_accounts` | `ListInternalAccounts` | Settlement | Yes (with retry) | NO - read-only |

**Implementation Notes**:

- `initiate` handler creates nostro/vostro accounts for cross-border settlements
- `retrieve` and `get_balance` are read-only queries for saga decision points
- All handlers should use `prepareClientContext()` for saga metadata propagation
- Account types: CLEARING, NOSTRO, VOSTRO, HOLDING, SUSPENSE, REVENUE, EXPENSE, INVENTORY

---

### 2. reference-data (HIGH PRIORITY)

**Status**: Has `client/client.go` but **missing `client/starlark.go`**

**Evidence**:

- **10 cross-service references** found in:
  - `services/payment-order/service/` (4 files - bucket solvency evaluation, saga handlers)
  - `services/internal-account/service/` (3 files - account validation)
  - `services/financial-accounting/service/` (1 file)
  - `services/financial-accounting/cmd/` (1 file)
- **Currently used** in payment-order saga handlers for bucket-aware solvency checks
- **Critical for**: Non-fungible instrument bucket evaluation (vouchers, tickets, time-limited instruments)

**Category**: `HandlerCategoryValuation`
**ProducesInstruments**: `[]` (read-only - provides instrument metadata, doesn't create instruments)

**Handlers to Implement**:

| Handler Name | gRPC Method | Category | Idempotent | ProducesInstruments |
|-------------|-------------|----------|------------|-------------------|
| `reference_data.retrieve_instrument` | `RetrieveInstrument` | Valuation | Yes (with retry) | NO - read-only |
| `reference_data.list_instruments` | `ListInstruments` | Valuation | Yes (with retry) | NO - read-only |
| `reference_data.evaluate_instrument` | `EvaluateInstrument` | Valuation | Yes (with retry) | NO - CEL evaluation |

**Implementation Notes**:

- Handlers provide instrument metadata for bucket solvency checks
- `evaluate_instrument` runs CEL expressions against payment attributes
- All are read-only operations with caching (L1/L2 via Redis)
- Used in saga decision points, not for state mutation

---

### 3. market-information (MEDIUM PRIORITY)

**Status**: Has `client/client.go` but **missing `client/starlark.go`**

**Evidence**:

- **3 cross-service references** found (all in `services/market-information/adapters/external/ecb/`)
- Currently used for **FX rate lookups** in ECB worker (background ingestion)
- **Future saga use case**: Real-time FX rate resolution in cross-currency payment sagas

**Category**: `HandlerCategoryValuation`
**ProducesInstruments**: `[]` (read-only - provides market data, doesn't create instruments)

**Handlers to Implement**:

| Handler Name | gRPC Method | Category | Idempotent | ProducesInstruments |
|-------------|-------------|----------|------------|-------------------|
| `market_information.retrieve_observation` | `RetrieveObservation` | Valuation | Yes (with retry) | NO - read-only |
| `market_information.list_observations` | `ListObservations` | Valuation | Yes (with retry) | NO - read-only |

**Implementation Notes**:

- Handlers provide point-in-time FX rate lookups for multi-currency transactions
- Supports bi-temporal queries (`knowledge_time` for audit replay)
- Read-only operations with Time-Bound Quality Ladder (ACTUAL → ESTIMATED → MARKET)
- Used for currency conversion in cross-border payment sagas

---

### 4. party (DEFERRED - No Saga Integration Required)

**Status**: Has `client/client.go` but **starlark.go NOT needed**

**Analysis Date**: 2026-02-04

**Evidence**:

- **11 cross-service references** found in:
  - `services/current-account/` (5 files - KYC/party validation during account creation)
  - `services/tenant/service/` (4 files - party client adapter)
  - `services/current-account/cmd/` (2 files - party wrapper)
- **No saga script usage**: Search for `party.` in all `.star` files returned zero results
- **Pre-saga validation pattern**: Party validation occurs during account creation via direct gRPC calls
  (see `grpc_service_party_integration_test.go`)
- **Async verification architecture**: KYC/AML checks handled via `VerificationService` with webhook-based
  async processing (see `verification_service.go`)

**Current Usage Pattern**:

Party service is called in **two distinct contexts**, neither requiring saga integration:

1. **Account Opening (Pre-Saga)**:
   - Direct gRPC call from `current-account.InitiateCurrentAccount()`
   - Validates party exists and has ACTIVE status before account creation
   - Fails fast with InvalidArgument or FailedPrecondition gRPC codes
   - Happens **before** any saga orchestration begins

2. **KYC/AML Verification (Background Async)**:
   - Initiated via `VerificationService.InitiateVerification()`
   - Creates PENDING verification record immediately
   - External KYC provider processes asynchronously
   - Results delivered via webhook to `verification_webhook.go`
   - Publishes `VerificationCompletedEvent` to event stream
   - **Not in critical path** of payment execution

**Decision Rationale**:

Party operations are **orthogonal to saga orchestration** for these reasons:

1. **Validation is Pre-Saga**: Party checks are gating conditions before saga initiation, not orchestrated steps
2. **No Compensation Needed**: Party lookups don't create state requiring rollback
3. **Async Verification Model**: KYC checks use event-driven architecture (webhook → event publisher),
   not synchronous saga steps
4. **No Instrument Production**: Party service doesn't create/consume financial instruments

**Future Consideration**:

If payment approval workflows require **synchronous party validation during saga execution**
(e.g., real-time sanction screening), revisit this decision. Current architecture delegates these checks to:

- Pre-saga validation (account opening)
- Post-saga compliance monitoring (background workers)

**Category**: N/A (Deferred)
**ProducesInstruments**: N/A (Deferred)

---

## Services NOT Requiring client/starlark.go

### 1. current-account ✓

**Status**: Already has `client/starlark.go` (✓ COMPLETE)
**Reason**: Service with saga scripts (`deposit.star`, `withdrawal.star`). Handlers already implemented.

---

### 2. financial-accounting ✓

**Status**: Already has `client/starlark.go` (✓ COMPLETE)
**Reason**: Service with saga integration for booking logs and postings. Handlers already implemented.

---

### 3. position-keeping ✓

**Status**: Already has `client/starlark.go` (✓ COMPLETE)
**Reason**: Service with saga integration for position logging. Handlers already implemented.

---

### 4. payment-order ✗

**Status**: No `client/` directory

**Reason**: **Orchestrator service** - initiates sagas but doesn't expose handlers to other sagas.
Contains saga scripts but doesn't need to be called by other services' sagas.

---

### 5. gateway ✗

**Status**: No `client/` directory

**Reason**: **External gateway wrapper** - called directly by payment-order orchestrator, not used in saga scripts.
Handles external payment rail communication.

---

### 6. tenant ✗

**Status**: Has `client/client.go` but **saga handlers not needed**

**Reason**: **Infrastructure service** - provides tenant context/configuration.
Not used in transactional saga flows. Read-only lookups happen outside saga execution.

---

### 7. audit-worker ✗

**Status**: No `client/` directory
**Reason**: **Background worker** - consumes saga events for audit logging. No cross-service calls. Pure event consumer.

---

### 8. utilization-metering-consumer ✗

**Status**: No `client/` directory
**Reason**: **Background worker** - processes utilization data asynchronously. No cross-service calls. Pure event consumer.

---

### 9. party ✗

**Status**: Has `client/client.go` but **saga handlers not needed**

**Reason**: Party operations occur **outside saga orchestration**:

- **Pre-saga validation**: Party checks are gating conditions during account creation (before saga initiation)
- **Async verification**: KYC/AML verification uses webhook-based event architecture, not synchronous saga steps
- **No compensation needed**: Party lookups are read-only and don't create state requiring rollback
- **No instrument production**: Party service doesn't create/consume financial instruments

See detailed analysis in "Services Requiring client/starlark.go → Section 4. party (DEFERRED)".

---

## Handler Implementation Priority Matrix

| Service | Priority | Cross-Service Refs | Saga Use Case | Complexity |
|---------|----------|-------------------|---------------|-----------|
| **internal-account** | **HIGH** | 14 | Nostro/vostro account creation in cross-border payments | 5 points |
| **reference-data** | **HIGH** | 10 | Bucket solvency checks (already used in payment-order) | 3 points |
| **market-information** | **MEDIUM** | 3 | FX rate lookups in multi-currency transactions | 3 points |
| **party** | **DEFERRED** | 11 | No saga integration needed (pre-saga + async patterns) | N/A |

**Total**: 11 story points across 3 services

---

## Handler Implementation Specifications

### Common Patterns (All Services)

All handlers must follow this structure:

```go
// RegisterStarlarkHandlers registers all Starlark service bindings for [ServiceName].
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, client *Client) error {
    handlers := map[string]struct {
        handler  saga.Handler
        metadata saga.HandlerMetadata
    }{
        "service_name.handler_name": {
            handler: handlerNameHandler(client),
            metadata: saga.HandlerMetadata{
                Category:            saga.HandlerCategorySettlement, // or Ingestion/Valuation
                ProducesInstruments: []string{"USD", "EUR"}, // or empty for read-only
            },
        },
    }

    for name, h := range handlers {
        if err := registry.RegisterWithMetadata(name, h.handler, &h.metadata); err != nil {
            return fmt.Errorf("failed to register %s: %w", name, err)
        }
    }
    return nil
}

func handlerNameHandler(client *Client) saga.Handler {
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        // 1. Parse Starlark params using saga.RequireStringParam, saga.GetOptionalStringParam, etc.
        param1, err := saga.RequireStringParam(params, "param1")
        if err != nil {
            return nil, err
        }

        // 2. Prepare client context with saga metadata propagation
        clientCtx := prepareClientContext(ctx)

        // 3. Build the gRPC request
        req := &servicev1.MethodRequest{
            Param1: param1,
        }

        // 4. Call the client method
        resp, err := client.Method(clientCtx, req)
        if err != nil {
            return nil, fmt.Errorf("failed to call method: %w", err)
        }

        // 5. Return result as map[string]any for Starlark
        return map[string]any{
            "result_field": resp.ResultField,
        }, nil
    }
}

// prepareClientContext propagates saga metadata to downstream gRPC calls
func prepareClientContext(ctx *saga.StarlarkContext) context.Context {
    clientCtx := context.Background()
    clientCtx = clients.WithCorrelationID(clientCtx, ctx.CorrelationID)
    clientCtx = clients.WithKnowledgeTime(clientCtx, ctx.KnowledgeTime)
    clientCtx = clients.WithIdempotencyKey(clientCtx, ctx.IdempotencyKey)
    return clientCtx
}
```

---

### internal-account Handler Specifications

**File**: `services/internal-account/client/starlark.go`

#### Handler: `internal_account.initiate`

**Purpose**: Create nostro/vostro accounts for cross-border settlement

**Input Parameters**:

```starlark
internal_account.initiate(
    account_type="NOSTRO",           # NOSTRO, VOSTRO, CLEARING, etc.
    currency="USD",
    account_name="USD Nostro Account",
    idempotency_key="unique-key"
)
```

**Output**:

```starlark
{
    "account_id": "acc_...",
    "account_type": "NOSTRO",
    "currency": "USD",
    "status": "ACTIVE"
}
```

**gRPC Method**: `InitiateInternalAccount`

---

#### Handler: `internal_account.retrieve`

**Purpose**: Fetch account details for saga decision points

**Input Parameters**:

```starlark
internal_account.retrieve(
    account_id="acc_..."
)
```

**Output**:

```starlark
{
    "account_id": "acc_...",
    "account_type": "NOSTRO",
    "currency": "USD",
    "status": "ACTIVE",
    "current_balance": "1000.00"
}
```

**gRPC Method**: `RetrieveInternalAccount`

---

#### Handler: `internal_account.get_balance`

**Purpose**: Query account balance for solvency checks

**Input Parameters**:

```starlark
internal_account.get_balance(
    account_id="acc_..."
)
```

**Output**:

```starlark
{
    "account_id": "acc_...",
    "available_balance_cents": 100000,
    "currency": "USD"
}
```

**gRPC Method**: `GetBalance`

---

#### Handler: `internal_account.list_accounts`

**Purpose**: List internal accounts with optional filtering

**Input Parameters**:

```starlark
internal_account.list_accounts(
    account_type="NOSTRO",  # Optional filter by type
    currency="USD",         # Optional filter by currency
    status="ACTIVE"         # Optional filter by status
)
```

**Output**:

```starlark
{
    "accounts": [
        {
            "account_id": "acc_...",
            "account_type": "NOSTRO",
            "currency": "USD",
            "status": "ACTIVE",
            "current_balance": "1000.00"
        },
        {
            "account_id": "acc_...",
            "account_type": "NOSTRO",
            "currency": "USD",
            "status": "ACTIVE",
            "current_balance": "2500.00"
        }
    ]
}
```

**gRPC Method**: `ListInternalAccounts`

**Error Cases**:

- Invalid filter values return empty list
- gRPC errors propagate to saga

**Idempotency**: Yes - read-only operation with retry

**Category**: `HandlerCategorySettlement`

**ProducesInstruments**: `[]` (read-only)

---

### reference-data Handler Specifications

**File**: `services/reference-data/client/starlark.go`

#### Handler: `reference_data.retrieve_instrument`

**Purpose**: Fetch instrument definition for bucket evaluation

**Input Parameters**:

```starlark
reference_data.retrieve_instrument(
    code="VOUCHER_MEAL",
    version=1
)
```

**Output**:

```starlark
{
    "code": "VOUCHER_MEAL",
    "version": 1,
    "display_name": "Meal Voucher",
    "instrument_type": "VOUCHER",
    "fungibility": "NON_FUNGIBLE",
    "bucket_expression": "attr.restaurant_type",
    "status": "ACTIVE"
}
```

**gRPC Method**: `RetrieveInstrument`

---

#### Handler: `reference_data.evaluate_instrument`

**Purpose**: Run CEL expression against payment attributes

**Input Parameters**:

```starlark
reference_data.evaluate_instrument(
    code="VOUCHER_MEAL",
    version=1,
    attributes={
        "restaurant_type": "italian",
        "amount_cents": 5000
    }
)
```

**Output**:

```starlark
{
    "code": "VOUCHER_MEAL",
    "evaluation_result": "italian",
    "is_valid": True
}
```

**gRPC Method**: `EvaluateInstrument`

---

#### Handler: `reference_data.list_instruments`

**Purpose**: List instruments with optional filtering for bulk operations

**Input Parameters**:

```starlark
reference_data.list_instruments(
    instrument_type="VOUCHER",  # Optional filter by type
    status="ACTIVE",            # Optional filter by status
    fungibility="NON_FUNGIBLE"  # Optional filter by fungibility
)
```

**Output**:

```starlark
{
    "instruments": [
        {
            "code": "VOUCHER_MEAL",
            "version": 1,
            "display_name": "Meal Voucher",
            "instrument_type": "VOUCHER",
            "fungibility": "NON_FUNGIBLE",
            "bucket_expression": "attr.restaurant_type",
            "status": "ACTIVE"
        },
        {
            "code": "VOUCHER_TRANSPORT",
            "version": 1,
            "display_name": "Transport Voucher",
            "instrument_type": "VOUCHER",
            "fungibility": "NON_FUNGIBLE",
            "bucket_expression": "attr.transport_mode",
            "status": "ACTIVE"
        }
    ]
}
```

**gRPC Method**: `ListInstruments`

**Error Cases**:

- Invalid filter values return empty list
- gRPC errors propagate to saga

**Idempotency**: Yes - read-only operation with retry

**Category**: `HandlerCategoryValuation`

**ProducesInstruments**: `[]` (read-only)

---

### market-information Handler Specifications

**File**: `services/market-information/client/starlark.go`

#### Handler: `market_information.retrieve_observation`

**Purpose**: Fetch FX rate for currency conversion

**Input Parameters**:

```starlark
market_information.retrieve_observation(
    data_set_id="USD_EUR_FX",
    data_source_id="ECB",
    as_of_time="2026-02-03T10:00:00Z",
    quality_level="ACTUAL"
)
```

**Output**:

```starlark
{
    "data_set_id": "USD_EUR_FX",
    "value": "0.92",
    "quality_level": "ACTUAL",
    "observed_at": "2026-02-03T10:00:00Z"
}
```

**gRPC Method**: `RetrieveObservation`

---

#### Handler: `market_information.list_observations`

**Purpose**: List market observations with filtering for bulk FX rate lookups

**Input Parameters**:

```starlark
market_information.list_observations(
    data_set_id="USD_EUR_FX",           # Optional filter by data set
    data_source_id="ECB",               # Optional filter by data source
    as_of_time="2026-02-03T10:00:00Z",  # Optional time filter
    quality_level="ACTUAL"              # Optional quality level filter
)
```

**Output**:

```starlark
{
    "observations": [
        {
            "data_set_id": "USD_EUR_FX",
            "value": "0.92",
            "quality_level": "ACTUAL",
            "observed_at": "2026-02-03T10:00:00Z"
        },
        {
            "data_set_id": "USD_EUR_FX",
            "value": "0.91",
            "quality_level": "ACTUAL",
            "observed_at": "2026-02-03T09:00:00Z"
        }
    ]
}
```

**gRPC Method**: `ListObservations`

**Error Cases**:

- Invalid filter values return empty list
- gRPC errors propagate to saga

**Idempotency**: Yes - read-only operation with retry

**Category**: `HandlerCategoryValuation`

**ProducesInstruments**: `[]` (read-only)

---

## Conservation Rule Impact

Each handler's `ProducesInstruments` metadata enables **Conservation Rule** enforcement:

| Service | Handlers | ProducesInstruments | Conservation Rule Impact |
|---------|----------|-------------------|--------------------------|
| internal-account | `initiate` | Currencies | Creates Money instruments (nostro/vostro) |
| reference-data | All | `[]` | Read-only - no instrument creation |
| market-information | All | `[]` | Read-only - no instrument creation |

**Key Insight**: Only `internal-account.initiate` produces instruments. All other handlers are read-only
and support saga decision logic without creating/destroying value. Party service handlers are deferred as
party operations occur outside saga orchestration (pre-saga validation + async verification).

---

## Testing Strategy

### Unit Tests (Per Handler)

Each handler must have tests covering:

1. **Happy path**: Valid parameters → successful gRPC call → correct Starlark result
2. **Missing required params**: `saga.RequireStringParam` returns error
3. **gRPC error handling**: Downstream error propagates correctly
4. **Idempotency**: Duplicate calls with same idempotency key return identical results
   (only for handlers marked "Idempotent: Yes")
5. **Context propagation**: CorrelationID, KnowledgeTime, IdempotencyKey passed correctly

**Note**: Idempotency tests (item 4) only apply to handlers with `Idempotent: Yes (with retry)`.
Handlers using circuit breaker patterns (`Idempotent: No`) rely on downstream service guarantees
and do not require idempotency key-based duplicate detection tests.

### Integration Tests (Per Service)

1. **End-to-end saga execution**: Create saga script → register handlers → execute saga → verify results
2. **Cross-service integration**: Test handler calling real service via testcontainers
3. **Conservation rule validation**: Verify `ProducesInstruments` metadata enforced

---

## Next Steps (Subtasks 19.2-19.4)

| Subtask | Service | Story Points | Dependencies |
|---------|---------|--------------|--------------|
| **19.2** | internal-account | 5 | None (parallel) |
| **19.3** | reference-data | 3 | None (parallel) |
| **19.4** | market-information | 3 | None (parallel) |
| **19.5** | party | N/A | Deferred (documented in audit) |

Subtasks 19.2-19.4 can execute **in parallel** after this audit report is reviewed.

---

## Appendix: Cross-Service Reference Details

### internal-account (14 references)

```text
services/payment-order/service/payment_orchestrator.go:70
services/payment-order/service/account_resolver.go:64
services/payment-order/service/grpc_service.go:102
services/payment-order/cmd/main.go
services/financial-accounting/service/account_resolver.go
services/financial-accounting/service/client_interfaces.go
services/current-account/service/account_resolver.go
services/current-account/service/client_interfaces.go
services/current-account/cmd/main.go
services/position-keeping/service/account_validator.go
services/position-keeping/cmd/main.go
```

### reference-data (10 references)

```text
services/payment-order/service/saga_handlers.go:44
services/payment-order/service/bucket_solvency_test.go:25
services/payment-order/service/grpc_service.go:120
services/internal-account/service/server.go
services/internal-account/service/client_interfaces.go
services/financial-accounting/service/financial_accounting_service.go
services/financial-accounting/cmd/main.go
```

### market-information (3 references)

```text
services/market-information/cmd/main.go
services/market-information/adapters/external/ecb/ecb_worker.go
services/market-information/adapters/external/ecb/ecb_worker_test.go
```

### party (11 references)

```text
services/current-account/service/grpc_service.go
services/current-account/service/client_interfaces.go
services/current-account/cmd/main.go
services/current-account/cmd/party_wrapper.go
services/tenant/service/client_interfaces.go
services/tenant/service/grpc_service.go
services/tenant/service/party_client_adapter.go
services/tenant/cmd/main.go
```

---

## Conclusion

This audit provides a complete roadmap for saga handler implementation across Meridian's service architecture.
The three services requiring handlers represent **11 story points** of work that can be parallelized across
subtasks 19.2-19.4, enabling future saga patterns including:

1. **Cross-border payments** (internal-account + market-information)
2. **Bucket-aware solvency** (reference-data - already in use)

The party service (subtask 19.5) has been **deferred** after analysis showing that party operations occur
outside saga orchestration flows (pre-saga validation during account opening + async KYC verification via webhooks).

All handler specifications include detailed parameter mappings, conservation rule metadata,
and testing requirements for immediate implementation.
