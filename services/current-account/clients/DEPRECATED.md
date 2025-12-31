# Deprecated: Client Implementations

This directory contains deprecated client implementations. **Do not use these for new code.**

## Migration Guide

The client implementations in this directory have been superseded by service-owned client packages:

| Old (deprecated)                     | New (use this)                                     |
|--------------------------------------|---------------------------------------------------|
| `clients.NewPositionKeepingClient()` | `services/position-keeping/client.New()`          |
| `clients.NewFinancialAccountingClient()` | `services/financial-accounting/client.New()` |
| `clients.NewPartyClient()`           | `services/party/client.New()`                     |
| `clients.NewResilientPositionKeepingClient()` | Use `Config.Resilience` in new client |
| `clients.NewResilientFinancialAccountingClient()` | Use `Config.Resilience` in new client |
| `clients.NewResilientPartyClient()` | Use `Config.Resilience` in new client |

## What Remains Useful

The following are still in use and exported from this package:

1. **Interfaces** (`interfaces.go`): Consumer-specific interfaces for testing/mocking
   - `PositionKeepingClient`
   - `FinancialAccountingClient`
   - `PartyClient`

2. **Errors** (`party_client.go`): Party validation errors
   - `ErrPartyNotFound`
   - `ErrPartyNotActive`

## Example Migration

### Before (deprecated pattern)

```go
import "github.com/meridianhub/meridian/services/current-account/clients"

// Create raw client
client, err := clients.NewPositionKeepingClient(&clients.PositionKeepingClientConfig{
    ServiceName: "position-keeping",
    Namespace:   namespace,
    Port:        50053,
    Tracer:      tracer,
})
if err != nil { return err }

// Wrap with resilience
resilient := clients.NewResilientPositionKeepingClient(
    client,
    sharedclients.ResilientClientConfig{Logger: logger},
)
```

### After (recommended pattern)

```go
import poskeepingclient "github.com/meridianhub/meridian/services/position-keeping/client"

// Single step: create client with built-in resilience
client, cleanup, err := poskeepingclient.New(poskeepingclient.Config{
    ServiceName: poskeepingclient.ServiceName,
    Namespace:   namespace,
    Port:        poskeepingclient.DefaultPort,
    Tracer:      tracer,
    Resilience: &sharedclients.ResilientClientConfig{
        Logger:             logger,
        CircuitBreakerName: "position-keeping",
    },
})
if err != nil { return err }
defer cleanup()
```

## Why This Change?

1. **Single Source of Truth**: Each service owns its client, reducing duplication
2. **Built-in Resilience**: Circuit breaker and retry are configured in one place
3. **Cleaner API**: `New()` returns a cleanup function for proper resource management
4. **Consistent Patterns**: All service clients follow the same structure

## Removal Timeline

These deprecated implementations will be removed in a future release once all
consumers have migrated to the service-owned client packages.
