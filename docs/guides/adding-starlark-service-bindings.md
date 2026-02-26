# Adding Starlark Service Bindings

This guide explains how to add Starlark handlers for a new service, enabling saga orchestration to call your service's operations.

## Table of Contents

1. [Overview](#overview)
2. [Step 1: Create starlark.go](#step-1-create-starlarkgo)
3. [Step 2: Implement RegisterStarlarkHandlers](#step-2-implement-registerstarlarkhandlers)
4. [Step 3: Implement Handler Functions](#step-3-implement-handler-functions)
5. [Step 4: Write Comprehensive Tests](#step-4-write-comprehensive-tests)
6. [Step 5: Wire into Saga Executor](#step-5-wire-into-saga-executor)
7. [Conservation of Dimension Rule](#conservation-of-dimension-rule)
8. [Examples](#examples)

## Overview

Starlark service bindings bridge saga orchestration scripts with real service implementations. They adapt the
Starlark interface (using `map[string]any`) to strongly-typed gRPC client calls, while propagating saga metadata
for idempotency, tracing, and bi-temporal queries.

### Architecture Pattern

```text
Saga Script (.star)
       ↓
Handler Registry
       ↓
Service Binding (starlark.go)
       ↓
gRPC Client (client.go)
       ↓
Service Implementation
```

Each service binding:

- Lives in `services/{service-name}/client/starlark.go`
- Depends on the service's existing gRPC client
- Registers handlers with metadata (category, instruments produced)
- Follows a consistent 5-step handler pattern

## Step 1: Create starlark.go

Create `services/{service-name}/client/starlark.go` alongside the existing `client.go`:

```go
// Package client provides Starlark service bindings for {ServiceName}.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga step execution with real {ServiceName} service integration.
package client

import (
    "context"
    "fmt"

    // Import your service's proto package
    {servicename}v1 "github.com/meridianhub/meridian/api/proto/meridian/{service_name}/v1"
    "github.com/meridianhub/meridian/shared/pkg/clients"
    "github.com/meridianhub/meridian/shared/pkg/saga"
)
```

## Step 2: Implement RegisterStarlarkHandlers

This function registers all handlers for your service with the saga registry:

```go
// RegisterStarlarkHandlers registers all Starlark service bindings for {ServiceName}.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
//
// This function is called during service initialisation to register {ServiceName} handlers
// with the saga execution engine. Each handler includes metadata for conservation rule
// enforcement and operational categorization.
//
// Example usage:
//
// registry := saga.NewHandlerRegistry()
// client, cleanup, _ := client.New(client.Config{...})
// defer cleanup()
// err := RegisterStarlarkHandlers(registry, client)
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, client *Client) error {
    handlers := map[string]struct {
        handler  saga.Handler
        metadata saga.HandlerMetadata
    }{
        "service_name.operation": {
            handler:  operationHandler(client),
            metadata: saga.HandlerMetadata{
                Category:            saga.HandlerCategorySettlement, // or HandlerCategoryIngestion, HandlerCategoryValuation
                ProducesInstruments: []string{"USD"},                 // Currencies or assets this handler produces
            },
        },
        "service_name.another_operation": {
            handler: anotherOperationHandler(client),
            metadata: saga.HandlerMetadata{
                Category:            saga.HandlerCategorySettlement,
                ProducesInstruments: []string{}, // Empty if operation doesn't produce instruments
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
```

### Handler Categories

Use the appropriate category for your handler:

- `saga.HandlerCategoryIngestion` - Imports external data (meter readings, market prices)
- `saga.HandlerCategoryValuation` - Computes derived values (mark-to-market, accruals)
- `saga.HandlerCategorySettlement` - Executes financial operations (debits, credits, transfers)

### ProducesInstruments Field

The `ProducesInstruments` field declares what financial instruments (currencies, assets) this handler creates
positions for. This enables the **Conservation of Dimension Rule** enforcement (see section below).

## Step 3: Implement Handler Functions

Each handler follows a consistent 5-step pattern:

```go
// operationHandler creates a new {operation} via gRPC.
// This handler adapts Starlark parameters to the {Operation} RPC call,
// propagating saga metadata for idempotency, tracing, and bi-temporal queries.
//
// Parameters:
//   - param1 (string): Description of param1
//   - param2 (decimal): Description of param2
//   - param3 (string): Description of param3 (optional)
//
// Returns a map containing:
//   - field1: Description of returned field1
//   - field2: Description of returned field2
//   - status: Description of status field
func operationHandler(client *Client) saga.Handler {
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        // Step 1: Parse Starlark params using saga.Require* helpers
        param1, err := saga.RequireStringParam(params, "param1")
        if err != nil {
            return nil, err
        }

        param2, err := saga.RequireDecimalParam(params, "param2")
        if err != nil {
            return nil, err
        }

        // Optional parameters use saga.GetXParam (returns default if missing)
        param3 := saga.GetStringParam(params, "param3", "")

        // Step 2: Prepare client context with saga metadata
        // This propagates idempotency keys, knowledge_at timestamps, and correlation IDs
        clientCtx := prepareClientContext(ctx)

        // Step 3: Build the gRPC request from Starlark params
        req := &{servicename}v1.{Operation}Request{
            Field1: param1,
            Field2: proto.String(param2.String()), // Convert decimal to proto string
            Field3: param3,
        }

        // Step 4: Call REAL gRPC client (not a mock!)
        resp, err := client.{Operation}(clientCtx, req)
        if err != nil {
            return nil, fmt.Errorf("service_name.operation: %w", err)
        }

        // Step 5: Convert protobuf response to map[string]any for Starlark
        result := resp.Get{ResultObject}()
        return map[string]any{
            "field1": result.GetField1(),
            "field2": result.GetField2(),
            "status": result.GetStatus(),
        }, nil
    }
}
```

### Helper Functions for Parameter Parsing

The `saga` package provides helpers for extracting typed parameters:

```go
// Required parameters (return error if missing or wrong type)
saga.RequireStringParam(params, "key")    // string
saga.RequireDecimalParam(params, "key")   // decimal.Decimal
saga.RequireIntParam(params, "key")       // int64
saga.RequireBoolParam(params, "key")      // bool

// Optional parameters (return default if missing)
saga.GetStringParam(params, "key", "default")       // string with default
saga.GetDecimalParam(params, "key", decimal.Zero)   // decimal with default
saga.GetIntParam(params, "key", 0)                  // int64 with default
saga.GetBoolParam(params, "key", false)             // bool with default
```

### Context Preparation Pattern

The `prepareClientContext` function propagates saga metadata to the downstream service:

```go
// prepareClientContext extracts saga metadata from Starlark context and
// propagates it to the gRPC client context for tracing and bi-temporal queries.
func prepareClientContext(ctx *saga.StarlarkContext) context.Context {
    clientCtx := context.Background()

    // Propagate correlation ID for distributed tracing
    if correlationID := ctx.CorrelationID(); correlationID != "" {
        clientCtx = clients.WithCorrelationID(clientCtx, correlationID)
    }

    // Propagate knowledge_at timestamp for bi-temporal queries
    if knowledgeAt := ctx.KnowledgeAt(); !knowledgeAt.IsZero() {
        clientCtx = clients.WithKnowledgeAt(clientCtx, knowledgeAt)
    }

    // Propagate idempotency key if present
    if idempotencyKey := ctx.IdempotencyKey(); idempotencyKey != "" {
        clientCtx = clients.WithIdempotencyKey(clientCtx, idempotencyKey)
    }

    return clientCtx
}
```

This ensures saga operations are:

- **Traceable**: Correlation IDs link distributed operations
- **Bi-temporal**: Knowledge_at enables consistent reads across services
- **Idempotent**: Retries don't create duplicate operations

## Step 4: Write Comprehensive Tests

Create `services/{service-name}/client/starlark_test.go`:

```go
package client

import (
    "testing"

    "github.com/meridianhub/meridian/shared/pkg/saga"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// TestRegisterStarlarkHandlers verifies all handlers are registered
func TestRegisterStarlarkHandlers(t *testing.T) {
    registry := saga.NewHandlerRegistry()

    // Create test client (use mock or testcontainer)
    client := &Client{
        // Initialize with test config
    }

    err := RegisterStarlarkHandlers(registry, client)
    require.NoError(t, err)

    // Verify expected handlers exist
    expectedHandlers := []string{
        "service_name.operation",
        "service_name.another_operation",
    }

    for _, name := range expectedHandlers {
        handler := registry.Get(name)
        assert.NotNil(t, handler, "handler %s should be registered", name)
    }
}

// TestOperationHandler tests the operation handler with real client
func TestOperationHandler(t *testing.T) {
    // Setup: Create client with testcontainers or mock server
    client, cleanup := setupTestClient(t)
    defer cleanup()

    registry := saga.NewHandlerRegistry()
    err := RegisterStarlarkHandlers(registry, client)
    require.NoError(t, err)

    // Execute handler
    handler := registry.Get("service_name.operation")
    require.NotNil(t, handler)

    ctx := &saga.StarlarkContext{
        // Initialize with test values
    }

    params := map[string]any{
        "param1": "test-value",
        "param2": decimal.NewFromFloat(100.50),
    }

    result, err := handler(ctx, params)
    require.NoError(t, err)

    // Verify result structure
    resultMap, ok := result.(map[string]any)
    require.True(t, ok, "result should be map[string]any")
    assert.NotEmpty(t, resultMap["field1"])
    assert.Equal(t, "EXPECTED_STATUS", resultMap["status"])
}

// TestOperationHandler_MissingRequiredParam tests error handling
func TestOperationHandler_MissingRequiredParam(t *testing.T) {
    client := &Client{}
    registry := saga.NewHandlerRegistry()
    RegisterStarlarkHandlers(registry, client)

    handler := registry.Get("service_name.operation")
    ctx := &saga.StarlarkContext{}

    // Missing required parameter
    params := map[string]any{}

    _, err := handler(ctx, params)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "param1")
}
```

### Testing Best Practices

1. **Test handler registration** - Verify all expected handlers exist
2. **Test happy path** - Successful operation with valid params
3. **Test error cases** - Missing params, invalid types, gRPC errors
4. **Test metadata propagation** - Verify correlation IDs, idempotency keys flow through
5. **Use real services when possible** - Testcontainers > mocks for integration tests

## Step 5: Wire into Saga Executor

Update the service's `cmd/main.go` to register handlers during initialisation:

```go
package main

import (
    "github.com/meridianhub/meridian/services/{service-name}/client"
    "github.com/meridianhub/meridian/shared/pkg/saga"
)

func main() {
    // ... existing service initialisation ...

    // Create handler registry for saga orchestration
    handlerRegistry := saga.NewHandlerRegistry()

    // Register handlers for all services this service orchestrates
    // Use concrete *Client types, not interfaces

    if err := currentaccountclient.RegisterStarlarkHandlers(handlerRegistry, currentAccountClient); err != nil {
        logger.Warn("failed to register current-account handlers", "error", err)
    }

    if err := financialaccountingclient.RegisterStarlarkHandlers(handlerRegistry, finAcctClient); err != nil {
        logger.Warn("failed to register financial-accounting handlers", "error", err)
    }

    if err := positionkeepingclient.RegisterStarlarkHandlers(handlerRegistry, posKeepingClient); err != nil {
        logger.Warn("failed to register position-keeping handlers", "error", err)
    }

    // Initialize saga executor with handler registry
    sagaRunner := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
        Handlers: handlerRegistry,
        Logger:   logger,
    })

    // ... wire sagaRunner into orchestrator ...
}
```

**Important Notes:**

- Use concrete `*Client` types, not interface types (e.g., `service.XxxClient`)
- The `RegisterStarlarkHandlers` functions need access to the gRPC connection
- Log warnings but don't fail if optional service handlers fail to register
- This pattern decouples service registration from saga execution

## Conservation of Dimension Rule

The **Conservation of Dimension Rule** enforces type safety for financial instruments:

> Handlers must declare ProducesInstruments metadata matching the instrument types they actually create in
> position-keeping (e.g., USD handler cannot produce EUR positions)

### Why This Matters

Without this rule, a bug could cause incorrect instrument creation:

```go
// BAD - Handler declares it produces USD but actually creates EUR
metadata: saga.HandlerMetadata{
    Category:            saga.CategorySettlement,
    ProducesInstruments: []string{"USD"},  // WRONG!
}

// In handler implementation:
req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
    Currency: "EUR",  // MISMATCH! Creates EUR but declared USD
}
```

The saga validator catches this at handler registration time, preventing runtime errors.

### How to Set ProducesInstruments

1. **For ingestion handlers** (creating positions from external data):

   ```go
   ProducesInstruments: []string{"KWH", "GAS", "WATER"}  // Physics instruments
   ```

2. **For settlement handlers** (financial operations):

   ```go
   ProducesInstruments: []string{"USD", "EUR", "GBP"}  // Currencies
   ```

3. **For update/cancel operations** (don't create new instruments):

   ```go
   ProducesInstruments: []string{}  // Empty - no new instruments
   ```

4. **For multi-currency handlers** (can produce any currency):

   ```go
   ProducesInstruments: []string{"USD", "EUR", "GBP", "NZD"}  // All supported
   ```

The validator will:

- Check that declared instruments match what's actually created
- Prevent typos (e.g., "USDD" instead of "USD")
- Enforce consistency across saga steps

## Examples

### Example 1: Current Account Lien Handler

From `services/current-account/client/starlark.go`:

```go
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, client *Client) error {
    handlers := map[string]struct {
        handler  saga.Handler
        metadata saga.HandlerMetadata
    }{
        "current_account.create_lien": {
            handler: createLienHandler(client),
            metadata: saga.HandlerMetadata{
                Category: saga.HandlerCategorySettlement,
                // Liens reserve funds in specific currencies
                ProducesInstruments: []string{"USD", "EUR", "GBP", "NZD"},
            },
        },
    }
    // ... registration loop ...
}

func createLienHandler(client *Client) saga.Handler {
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        // Step 1: Parse params
        accountID, err := saga.RequireStringParam(params, "account_id")
        if err != nil {
            return nil, err
        }
        amount, err := saga.RequireDecimalParam(params, "amount")
        if err != nil {
            return nil, err
        }
        currency, err := saga.RequireStringParam(params, "currency")
        if err != nil {
            return nil, err
        }

        // Step 2: Prepare context
        clientCtx := prepareClientContext(ctx)

        // Step 3: Build request
        req := &currentaccountv1.InitiateLienRequest{
            AccountId: accountID,
            Money: &money.Money{
                CurrencyCode: currency,
                Units:        amount.IntPart(),
                Nanos:        int32(amount.Sub(decimal.NewFromInt(amount.IntPart())).Mul(decimal.NewFromInt(1e9)).IntPart()),
            },
        }

        // Step 4: Call gRPC
        resp, err := client.InitiateLien(clientCtx, req)
        if err != nil {
            return nil, fmt.Errorf("current_account.create_lien: %w", err)
        }

        // Step 5: Convert response
        lien := resp.GetLien()
        return map[string]any{
            "lien_id":    lien.GetLienId(),
            "account_id": lien.GetAccountId(),
            "amount":     amount,
            "currency":   currency,
            "status":     "ACTIVE",
        }, nil
    }
}
```

### Example 2: Position Keeping Ingestion Handler

From `services/position-keeping/client/starlark.go`:

```go
"position_keeping.initiate_log": {
    handler: initiateLogHandler(client),
    metadata: saga.HandlerMetadata{
        Category: saga.HandlerCategoryIngestion,
        // Position Keeping ingests physical measurements
        ProducesInstruments: []string{"KWH", "GAS", "WATER"},
    },
},

func initiateLogHandler(client *Client) saga.Handler {
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        accountID, err := saga.RequireStringParam(params, "account_id")
        if err != nil {
            return nil, err
        }

        clientCtx := prepareClientContext(ctx)

        req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
            AccountId: accountID,
        }

        resp, err := client.InitiateFinancialPositionLog(clientCtx, req)
        if err != nil {
            return nil, fmt.Errorf("position_keeping.initiate_log: %w", err)
        }

        log := resp.GetLog()
        return map[string]any{
            "log_id":     log.GetLogId(),
            "account_id": log.GetAccountId(),
            "status":     "INITIATED",
        }, nil
    }
}
```

### Example 3: Financial Accounting Settlement Handler

From `services/financial-accounting/client/starlark.go`:

```go
"financial_accounting.capture_posting": {
    handler: capturePostingHandler(client),
    metadata: saga.HandlerMetadata{
        Category: saga.HandlerCategorySettlement,
        // Postings create GL entries in multiple currencies
        ProducesInstruments: []string{"USD", "EUR", "GBP", "NZD"},
    },
},

func capturePostingHandler(client *Client) saga.Handler {
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        // Parse all required params
        accountID, err := saga.RequireStringParam(params, "account_id")
        if err != nil {
            return nil, err
        }
        amount, err := saga.RequireDecimalParam(params, "amount")
        if err != nil {
            return nil, err
        }
        currency, err := saga.RequireStringParam(params, "currency")
        if err != nil {
            return nil, err
        }
        entryType, err := saga.RequireStringParam(params, "entry_type")
        if err != nil {
            return nil, err
        }

        clientCtx := prepareClientContext(ctx)

        req := &financialaccountingv1.CaptureLedgerPostingRequest{
            AccountId: accountID,
            Money: &money.Money{
                CurrencyCode: currency,
                Units:        amount.IntPart(),
                Nanos:        int32(amount.Sub(decimal.NewFromInt(amount.IntPart())).Mul(decimal.NewFromInt(1e9)).IntPart()),
            },
            EntryType: entryType, // "DEBIT" or "CREDIT"
        }

        resp, err := client.CaptureLedgerPosting(clientCtx, req)
        if err != nil {
            return nil, fmt.Errorf("financial_accounting.capture_posting: %w", err)
        }

        posting := resp.GetPosting()
        return map[string]any{
            "posting_id": posting.GetPostingId(),
            "account_id": posting.GetAccountId(),
            "amount":     amount,
            "currency":   currency,
            "entry_type": entryType,
            "status":     "CAPTURED",
        }, nil
    }
}
```

## Summary Checklist

When adding Starlark service bindings:

- [ ] Create `services/{service-name}/client/starlark.go`
- [ ] Implement `RegisterStarlarkHandlers(registry, client)` function
- [ ] For each operation, create handler function following 5-step pattern:
  1. Parse parameters with `saga.Require*Param` helpers
  2. Prepare client context with `prepareClientContext(ctx)`
  3. Build gRPC request from parameters
  4. Call real gRPC client method
  5. Convert protobuf response to `map[string]any`
- [ ] Set correct handler metadata:
  - `Category`: Ingestion, Valuation, or Settlement
  - `ProducesInstruments`: List instruments this handler creates
- [ ] Write comprehensive tests in `starlark_test.go`
- [ ] Wire handlers into saga executor in `cmd/main.go`
- [ ] Verify Conservation of Dimension Rule compliance

## See Also

- [Starlark Saga Architecture](../architecture/starlark-saga-architecture.md) - Overall architecture
- [ADR-028: Starlark Saga Orchestration](../adr/0028-starlark-saga-cel-valuation.md) - Design decisions
- [Troubleshooting Saga Handlers](../runbooks/troubleshooting-saga-handlers.md) - Common issues and solutions
- [Saga Handler Schema](../saga-handlers.schema.json) - JSON schema for handler validation

---

**Success Metric:** A new team member should be able to add a service binding in < 2 hours following this guide.
