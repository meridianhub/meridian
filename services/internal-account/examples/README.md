# Internal Account Service Examples

This directory contains runnable Go examples demonstrating how to use the Internal Account service API.

## Prerequisites

1. **Service running**: Start the Internal Account service on `localhost:50057`
2. **Dependencies**: For balance queries, Position Keeping service should be running on `localhost:50053`

### Quick Start with Tilt

```bash
# Start all services
tilt up

# Wait for internal-account to be green in the Tilt UI
open http://localhost:10350
```

### Quick Start without Tilt

```bash
# Start just the Internal Account service
cd services/internal-account
go run ./cmd
```

## Running Examples

Each example is a standalone Go program that can be run independently:

```bash
# From repository root
go run ./services/internal-account/examples/create_clearing_account.go
go run ./services/internal-account/examples/query_balance.go
go run ./services/internal-account/examples/counterparty_account.go
go run ./services/internal-account/examples/multi_asset.go
go run ./services/internal-account/examples/account_lifecycle.go
```

## Example Descriptions

### 1. create_clearing_account.go

Basic account creation with tenant context. Demonstrates:

- Connecting to the gRPC service
- Setting tenant context via metadata
- Creating a simple CLEARING account
- Using idempotency keys for exactly-once semantics

```bash
go run ./services/internal-account/examples/create_clearing_account.go
```

### 2. query_balance.go

Balance retrieval via Position Keeping delegation. Demonstrates:

- Listing accounts with filters
- Querying account balance (delegated to Position Keeping)
- Handling service unavailability gracefully
- Interpreting InstrumentAmount responses

```bash
go run ./services/internal-account/examples/query_balance.go
```

### 3. counterparty_account.go

NOSTRO/VOSTRO account setup for counterparty banking. Demonstrates:

- Creating NOSTRO accounts (our account at another bank)
- Creating VOSTRO accounts (their account at our bank)
- Required CounterpartyDetails structure
- Counterparty attributes (e.g., SWIFT/BIC codes)
- Updating counterparty details
- Optimistic locking with expected_version

```bash
go run ./services/internal-account/examples/counterparty_account.go
```

### 4. multi_asset.go

Creating non-currency internal accounts. Demonstrates:

- Energy accounts (KWH - kilowatt-hours)
- Compute accounts (GPU_HOUR - GPU compute allocation)
- Carbon accounts (TONNE_CO2E - carbon credits)
- Different account types (INVENTORY, SUSPENSE, REVENUE)
- Supported instrument dimensions

```bash
go run ./services/internal-account/examples/multi_asset.go
```

### 5. account_lifecycle.go

Full account lifecycle management. Demonstrates:

- State transitions: ACTIVE -> SUSPENDED -> ACTIVE -> CLOSED
- Control actions: SUSPEND, ACTIVATE, CLOSE
- Reason requirements for audit trail
- Terminal state behavior (CLOSED cannot be reopened)
- Error handling for invalid transitions

```bash
go run ./services/internal-account/examples/account_lifecycle.go
```

## Common Patterns

### Tenant Context

All examples require tenant context via gRPC metadata:

```go
ctx = metadata.AppendToOutgoingContext(ctx,
    "x-organization", "your-tenant-id",
)
```

### Error Handling

Examples demonstrate proper gRPC error handling:

```go
if err != nil {
    st, ok := status.FromError(err)
    if ok {
        switch st.Code() {
        case codes.NotFound:
            // Handle not found
        case codes.AlreadyExists:
            // Handle duplicate
        case codes.FailedPrecondition:
            // Handle invalid state transition
        case codes.Aborted:
            // Handle optimistic lock conflict
        }
    }
}
```

### Idempotency

Create operations should use idempotency keys:

```go
IdempotencyKey: &commonv1.IdempotencyKey{
    Key: "unique-operation-id-" + time.Now().Format("20060102"),
}
```

## Account Types Reference

| Type | Purpose | Requires Counterparty |
|------|---------|----------------------|
| `CLEARING` | Settlement and clearing operations | No |
| `NOSTRO` | Our account at another bank | Yes |
| `VOSTRO` | Their account at our bank | Yes |
| `HOLDING` | Temporary fund holding | No |
| `SUSPENSE` | Unidentified/pending transactions | No |
| `REVENUE` | Income tracking | No |
| `EXPENSE` | Cost tracking | No |
| `INVENTORY` | Non-cash asset tracking | No |

## Instrument Dimensions Reference

| Dimension | Example Codes | Use Case |
|-----------|--------------|----------|
| CURRENCY | USD, EUR, GBP | Fiat and crypto currencies |
| ENERGY | KWH, MWH, THERM | Energy trading, utilities |
| COMPUTE | GPU_HOUR, CPU_SECOND | Cloud compute allocation |
| CARBON | TONNE_CO2E | Carbon credits, emissions |
| MASS | KG, TON, LB | Commodity inventory |
| VOLUME | LITRE, GALLON, BARREL | Liquid commodities |
| TIME | HOUR, DAY | Time-based billing |
| DATA | GB, TB | Data transfer/storage |
| COUNT | UNIT, TOKEN, VOUCHER | Countable items |

## Troubleshooting

### "connection refused"

Service not running. Start it with:

```bash
go run ./services/internal-account/cmd
```

### "tenant not found"

Missing `x-organization` header. Add to context:

```go
ctx = metadata.AppendToOutgoingContext(ctx, "x-organization", "your-tenant")
```

### "Position Keeping unavailable"

For balance queries, Position Keeping must be running. Start with Tilt or:

```bash
go run ./services/position-keeping/cmd
```

### "counterparty details required"

NOSTRO and VOSTRO accounts require CounterpartyDetails. See `counterparty_account.go`.
