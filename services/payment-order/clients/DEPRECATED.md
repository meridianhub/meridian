# Deprecated: Client Implementations

This directory contains deprecated client implementations. **Do not use these for new code.**

## Migration Guide

The client implementations in this directory have been superseded by service-owned client packages:

| Old (deprecated)                          | New (use this)                               |
|-------------------------------------------|----------------------------------------------|
| `clients.NewCurrentAccountClient()`       | `services/current-account/client.New()`      |
| `clients.NewFinancialAccountingClient()`  | `services/financial-accounting/client.New()` |

## Why This Change?

1. **Single Source of Truth**: Each service owns its client, reducing duplication
2. **Built-in Resilience**: Circuit breaker and retry are configured in one place
3. **Cleaner API**: `New()` returns a cleanup function for proper resource management
4. **Consistent Patterns**: All service clients follow the same structure

## Example Migration

### Before (deprecated pattern)

```go
import "github.com/meridianhub/meridian/services/payment-order/clients"

client, err := clients.NewCurrentAccountClient(&clients.CurrentAccountClientConfig{
    ServiceName: "current-account",
    Namespace:   namespace,
    Port:        50051,
})
```

### After (recommended pattern)

```go
import currentacctclient "github.com/meridianhub/meridian/services/current-account/client"

client, cleanup, err := currentacctclient.New(currentacctclient.Config{
    ServiceName: currentacctclient.ServiceName,
    Namespace:   namespace,
    Port:        currentacctclient.DefaultPort,
    Resilience: &sharedclients.ResilientClientConfig{
        Logger:             logger,
        CircuitBreakerName: "current-account",
    },
})
if err != nil { return err }
defer cleanup()
```

## Removal Timeline

These deprecated implementations will be removed in a future release once all
consumers have migrated to the service-owned client packages.
