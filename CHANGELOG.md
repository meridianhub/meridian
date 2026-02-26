# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### BREAKING CHANGE: Starlark Handlers Moved to Service Clients

**Migration Required**: If you directly import `shared/pkg/saga.DefaultRegistry()`, you must now explicitly
register service handlers.

#### What Changed

Starlark service handlers have been moved from a global registry in `shared/pkg/saga/handlers.go` to
service-specific client packages (`services/{service}/client/starlark.go`). Handlers now call real gRPC services
instead of returning mock data.

#### Migration Path

**Before (Old Pattern - No Longer Supported):**

```go
// ❌ This no longer works
executor := saga.NewExecutor(saga.ExecutorConfig{
    Handlers: saga.DefaultRegistry(), // Global registry removed
})
```

**After (New Pattern - Required):**

```go
// ✅ Explicit handler registration
func main() {
    // 1. Initialize service clients
    currentAccountClient, cleanup1, _ := currentaccountclient.New(currentaccountclient.Config{
        Address: os.Getenv("CURRENT_ACCOUNT_ADDR"),
    })
    defer cleanup1()

    positionKeepingClient, cleanup2, _ := positionkeepingclient.New(positionkeepingclient.Config{
        Address: os.Getenv("POSITION_KEEPING_ADDR"),
    })
    defer cleanup2()

    financialAcctClient, cleanup3, _ := financialaccountingclient.New(financialaccountingclient.Config{
        Address: os.Getenv("FINANCIAL_ACCOUNTING_ADDR"),
    })
    defer cleanup3()

    // 2. Create handler registry
    handlerRegistry := saga.NewHandlerRegistry()

    // 3. Register service handlers explicitly
    if err := currentaccountclient.RegisterStarlarkHandlers(handlerRegistry, currentAccountClient); err != nil {
        logger.Warn("failed to register current-account handlers", "error", err)
    }

    if err := positionkeepingclient.RegisterStarlarkHandlers(handlerRegistry, positionKeepingClient); err != nil {
        logger.Warn("failed to register position-keeping handlers", "error", err)
    }

    if err := financialaccountingclient.RegisterStarlarkHandlers(handlerRegistry, financialAcctClient); err != nil {
        logger.Warn("failed to register financial-accounting handlers", "error", err)
    }

    // 4. Create saga runner with explicit dependencies
    sagaRunner := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
        Handlers: handlerRegistry,
        Logger:   logger,
    })
}
```

#### Services Affected

The following services have handler registration functions you need to call:

- **Current Account**: `currentaccountclient.RegisterStarlarkHandlers`
  - Handlers: `create_lien`, `execute_lien`, `terminate_lien`, `save`
- **Position Keeping**: `positionkeepingclient.RegisterStarlarkHandlers`
  - Handlers: `initiate_log`, `update_log`, `cancel_log`
- **Financial Accounting**: `financialaccountingclient.RegisterStarlarkHandlers`
  - Handlers: `capture_posting`, `reverse_posting`

#### Benefits of New Pattern

- **Clear Dependencies**: Explicit registration shows what services your saga orchestrates
- **Better Testing**: Inject mock clients for unit testing
- **No Global State**: Each service initialisation creates its own registry
- **Service Ownership**: Service teams own their Starlark bindings
- **Real Service Integration**: Handlers now call actual gRPC services with validation, resilience, and
  observability

#### Backward Compatibility

**Starlark scripts are unchanged.** Handler names and signatures remain the same:

```starlark
# This script works with both old and new patterns
step(name="create_lien")
lien_result = current_account.create_lien(
    account_id=account_id,
    amount=amount,
    currency=currency,
)
```

Only the Go initialisation code in `cmd/main.go` needs to change.

#### Timeline for Migration

- **Removed in this release**: `saga.DefaultRegistry()` global registry
- **Added in this release**: `{service}client.RegisterStarlarkHandlers` functions
- **Required action**: Update service initialisation code in `cmd/main.go`

#### Documentation

For detailed implementation guide, see:

- [Adding Starlark Service Bindings Guide](docs/guides/adding-starlark-service-bindings.md)
- [Starlark Saga Architecture](docs/architecture/starlark-saga-architecture.md)
- [Troubleshooting Saga Handlers](docs/runbooks/troubleshooting-saga-handlers.md)
- [ADR-028: Starlark Saga Orchestration](docs/adr/0028-starlark-saga-cel-valuation.md)

#### Need Help?

If you encounter issues during migration:

1. Check the [troubleshooting runbook](docs/runbooks/troubleshooting-saga-handlers.md)
2. Review example implementations in `services/payment-order/cmd/main.go`
3. Contact the platform team via Slack #saga-support

---

## [Previous Releases]

<!-- Add previous release notes here as they occur -->
