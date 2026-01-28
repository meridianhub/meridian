# Payment Order Saga Handler Architecture

## Decision: Handlers Remain in Service Package

Unlike other services (position-keeping, current-account, financial-accounting), payment-order saga handlers
**remain in `service/saga_handlers.go`** rather than being moved to a `client/starlark.go` file.

## Rationale

### The "Client Pattern" Applies to Simple Wrappers

Services like position-keeping, current-account, and financial-accounting have **thin wrapper handlers** that:

- Adapt Starlark `map[string]any` parameters to strongly-typed proto messages
- Make a single gRPC client call
- Return results with minimal business logic
- Enable external services to call these operations via saga steps

**Example** (from `services/position-keeping/client/starlark.go`):

```go
func initiateLogHandler(client *Client) saga.Handler {
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        accountID, _ := saga.RequireStringParam(params, "account_id")
        clientCtx := prepareClientContext(ctx)
        req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
            AccountId: accountID,
        }
        resp, err := client.InitiateFinancialPositionLog(clientCtx, req)
        // Simple error handling and response mapping...
    }
}
```

### Payment Order is Fundamentally Different

Payment-order saga handlers contain **complex orchestration logic**:

1. **Multiple Service Coordination**: Handlers call Current Account, Financial Accounting, Reference Data, and
   Payment Gateway services
2. **Business Rule Evaluation**: Bucket evaluation for non-fungible instruments, retry logic with exponential
   backoff
3. **Service-Internal Logic**: These handlers implement payment-order's internal saga orchestration, not
   external-facing operations
4. **Complex Dependencies**: `BucketEvaluator`, `PaymentOrchestrator`, `PaymentGateway` - not simple gRPC clients

**Example** (from `services/payment-order/service/saga_handlers.go`):

```go
func createPaymentOrderLienHandler(deps *PaymentOrderHandlerDeps, logger *slog.Logger) saga.Handler {
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        // 1. Extract parameters
        // 2. Evaluate bucket ID using BucketEvaluator and ReferenceDataClient
        // 3. Build lien request with bucket awareness
        // 4. Call CurrentAccountClient.InitiateLien
        // 5. Handle bucket-specific response logic
        // ... ~100 lines of business logic
    }
}
```

### Migration Status

| Service | Handler Location | Pattern | Complexity |
|---------|-----------------|---------|------------|
| position-keeping | `client/starlark.go` | Thin wrapper | Simple gRPC calls |
| current-account | `client/starlark.go` | Thin wrapper | Simple gRPC calls |
| financial-accounting | `client/starlark.go` | Thin wrapper | Simple gRPC calls |
| **payment-order** | **`service/saga_handlers.go`** | **Orchestration** | **Complex business logic** |

## Platform Utilities

The following handlers remain in `shared/pkg/saga/` as generic platform utilities:

- `repository.save` - Generic persistence operation, no service-specific logic
- `notification.send` - Generic notification operation, no service-specific logic

These are used across multiple sagas and have no gRPC client dependencies.

## Conclusion

This architecture decision maintains proper separation of concerns:

- **Simple adapters** live in `client/starlark.go` for external consumption
- **Complex orchestration** lives in `service/saga_handlers.go` for service-internal logic
- **Platform utilities** live in `shared/pkg/saga/` for cross-cutting concerns

Moving payment-order handlers to a client package would be architecturally incorrect and would obscure their
true nature as orchestration logic.
