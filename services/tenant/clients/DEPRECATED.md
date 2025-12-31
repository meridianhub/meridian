# Deprecated: Client Implementations

This directory contains deprecated client implementations. **Do not use these for new code.**

## Migration Guide

The client implementations in this directory have been superseded by service-owned client packages:

| Old (deprecated)              | New (use this)                  |
|-------------------------------|--------------------------------|
| `clients.NewPartyClient()`    | `services/party/client.New()`  |

## What Remains Useful

The following are still in use:

1. **PartyClientAdapter** (`party_client_adapter.go`): Wraps the service-owned party client
   with tenant-specific convenience methods

## Why This Change?

1. **Single Source of Truth**: Each service owns its client, reducing duplication
2. **Built-in Resilience**: Circuit breaker and retry are configured in one place
3. **Cleaner API**: `New()` returns a cleanup function for proper resource management
4. **Consistent Patterns**: All service clients follow the same structure

## Example Migration

### Before (deprecated pattern)

```go
import "github.com/meridianhub/meridian/services/tenant/clients"

client, err := clients.NewPartyClient(&clients.PartyClientConfig{
    ServiceName: "party",
    Namespace:   namespace,
    Port:        50055,
})
```

### After (recommended pattern)

```go
import partyclient "github.com/meridianhub/meridian/services/party/client"
import "github.com/meridianhub/meridian/services/tenant/clients"

baseClient, cleanup, err := partyclient.New(partyclient.Config{
    ServiceName: partyclient.ServiceName,
    Namespace:   namespace,
    Port:        partyclient.DefaultPort,
    Resilience: &sharedclients.ResilientClientConfig{
        Logger:             logger,
        CircuitBreakerName: "party",
    },
})
if err != nil { return err }
defer cleanup()

// Use adapter for tenant-specific methods
adapter := clients.NewPartyClientAdapter(baseClient)
```

## Removal Timeline

These deprecated implementations will be removed in a future release once all
consumers have migrated to the service-owned client packages.
