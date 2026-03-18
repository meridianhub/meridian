---
name: value-types
description: Relationship between Qty[D], Money, Asset, Amount, and shared/pkg/money types, with a decision guide
triggers:
  - Representing a monetary amount in Go
  - Representing energy, carbon, or compute quantities
  - Choosing between Money and Amount types
  - Cross-service communication with non-currency instruments
  - What type should I use for a financial field?
---

# Value Types: Qty, Money, Asset, and Amount

Meridian uses a layered type system for representing quantities of any asset class—currency,
energy, carbon credits, compute hours, and more. This guide explains each type, how they
relate, and when to use each.

## Type Hierarchy

```
shared/platform/quantity/
└── Qty[D Dimension]          ← generic base type (phantom-typed)
    ├── type Money = Qty[Monetary]    ← currency quantities (GBP, USD, EUR)
    └── type Asset = Qty[Commodity]   ← non-currency assets (KWH, GPU_HOUR, TONNE_CO2E)

shared/pkg/money/
└── Money                     ← thin wrapper around quantity.Money
                                 adds minor-unit constructors and Currency type
                                 (preferred for legacy/inter-service money operations)

shared/pkg/amount/
└── Amount                    ← dimension-agnostic value type
                                 accepts any valid dimension at runtime
                                 used for cross-service communication and persistence
```

See [ADR-013](../adr/0013-generic-asset-quantity-types.md) for the architectural rationale.

## The Phantom Type Pattern

`Qty[D]` uses Go generics with phantom types to enforce dimension safety at compile time.
`Monetary` and `Commodity` are empty structs—they carry zero runtime overhead but prevent
accidental mixing in the type system:

```go
var money quantity.Money   // Qty[Monetary]
var energy quantity.Asset  // Qty[Commodity]

money = energy  // compile error: cannot use Qty[Commodity] as Qty[Monetary]
```

This eliminates a category of bugs (mixing kilowatt-hours with British pounds) that JSON
or `decimal.Decimal` fields cannot prevent.

## Quick Decision Guide

```
Is the value always a fiat currency (USD, GBP, EUR...)?
├── Yes → Does this code interact with legacy APIs or cross-service protos?
│         ├── Yes → shared/pkg/money.Money
│         └── No  → quantity.Money (Qty[Monetary])
└── No  → Is the dimension known at compile time?
           ├── Yes, commodity (KWH, GPU_HOUR, TONNE_CO2E) → quantity.Asset (Qty[Commodity])
           └── No (any dimension, runtime) → shared/pkg/amount.Amount
```

## Type Reference

### `quantity.Qty[D]` — Generic Base Type

**Package**: `shared/platform/quantity`

The foundational type. All quantity arithmetic in Meridian ultimately operates on `Qty[D]`.
It holds a `decimal.Decimal` amount and an `Instrument` (code, version, precision, dimension).

```go
import "github.com/meridianhub/meridian/shared/platform/quantity"

// Direct use (uncommon—prefer the named aliases)
qty := quantity.New[quantity.Monetary](decimal.NewFromInt(100), instrument)
```

Use `Qty[D]` directly when writing generic functions that must work across dimensions:

```go
func Sum[D quantity.Dimension](items []quantity.Qty[D]) (quantity.Qty[D], error) {
    // ...
}
```

### `quantity.Money` — Currency Quantities

**Type alias**: `type Money = Qty[Monetary]`

**Package**: `shared/platform/quantity`

Use `quantity.Money` for currency values inside domain packages that already import
`shared/platform/quantity`. Most services re-export this as a local type alias:

```go
// services/financial-accounting/domain/quantity.go
type Money = quantity.Money  // re-exported for use within this service's domain
```

**Factory functions** (from `shared/platform/quantity`):

```go
m := quantity.NewMoney(decimal.NewFromInt(100), instrument)  // from decimal
m := quantity.NewMoneyFromInt(10000, instrument)             // from minor units
m := quantity.ZeroMoney(instrument)                          // zero value
```

### `quantity.Asset` — Commodity Quantities

**Type alias**: `type Asset = Qty[Commodity]`

**Package**: `shared/platform/quantity`

Use `quantity.Asset` for non-monetary instruments: energy (KWH), compute (GPU_HOUR),
carbon credits (TONNE_CO2E), and other physical or digital commodities.

```go
// services/position-keeping/domain/quantity.go re-exports this as:
type Asset = quantity.Asset

// Usage in domain code:
assetQty := quantity.NewAsset(decimal.NewFromFloat(1.5), kwhInstrument)
```

Compile-time safety prevents mixing `Asset` with `Money`:

```go
var energy quantity.Asset
var funds  quantity.Money
total := energy.Add(funds)  // compile error
```

### `shared/pkg/money.Money` — Cross-Service Currency Type

**Package**: `shared/pkg/money`

A thin wrapper around `quantity.Money` that adds:

- Named `Currency` type (`money.CurrencyGBP`, `money.CurrencyUSD`, ...)
- Minor-unit constructors (`money.New("GBP", 10000)` → £100.00)
- Overflow-safe `ToMinorUnits()` with explicit error
- `AmountCents()` for backward compatibility (deprecated, use `ToMinorUnits()`)

```go
import "github.com/meridianhub/meridian/shared/pkg/money"

// From minor units (cents, pence)
m, err := money.New("GBP", 10000)    // £100.00

// From decimal major units
m, err := money.NewFromDecimal(decimal.NewFromInt(100), money.CurrencyGBP)

// Arithmetic
sum, err := m1.Add(m2)          // returns ErrCurrencyMismatch if currencies differ
diff, err := m1.Subtract(m2)
product := m.Multiply(factor)

// Conversion
cents, err := m.ToMinorUnits()  // returns ErrAmountOverflow if out of int64 range
```

**When to use `shared/pkg/money.Money` over `quantity.Money`:**

- Payment gateways and external APIs that speak in minor units (Stripe, Adyen)
- Services that predate the `quantity` package and use the minor-unit API
- Any code that needs the named `Currency` type with `IsValid()`, `ParseCurrency()`, etc.

### `shared/pkg/amount.Amount` — Dimension-Agnostic Type

**Package**: `shared/pkg/amount`

`Amount` is the bridge type for contexts where the instrument dimension is not known at
compile time—typically persistence and cross-service communication.

Unlike `Money` (currency-only) and `Asset` (commodity-only), `Amount` accepts any
dimension in `quantity.ValidDimensions`: `CURRENCY`, `ENERGY`, `CARBON`, `COMPUTE`, etc.

```go
import "github.com/meridianhub/meridian/shared/pkg/amount"

// From persisted columns (code, dimension, precision, minor units)
a, err := amount.NewFromInstrument("GBP",  "CURRENCY", 2, 10000)  // £100.00
a, err := amount.NewFromInstrument("KWH",  "ENERGY",   3, 1500)   // 1.500 KWH
a, err := amount.NewFromInstrument("GPU_HOUR", "COMPUTE", 4, 5000) // 0.5000 GPU_HOUR

// From a resolved instrument
a := amount.New(instrument, minorUnits)
a := amount.NewFromDecimal(instrument, majorUnits)

// Arithmetic
sum, err := a1.Add(a2)          // ErrInstrumentMismatch if instruments differ
diff, err := a1.Subtract(a2)
product := a.Multiply(factor)

// Accessors
a.InstrumentCode() // "KWH"
a.Dimension()      // "ENERGY"
a.Precision()      // 3
a.Amount()         // decimal.Decimal
```

**Use `Amount` when:**

- Reading from or writing to a database table that stores generic instrument quantities
- Building gRPC proto messages that must represent any asset class
- A function receives quantities from reference data without compile-time dimension knowledge
- Implementing position-keeping logic that handles both currency and commodity positions

## Persistence Mapping

### Storing Quantity Values

Both `Money` and `Asset` must be decomposed for storage. Store three columns:

| Column | Type | Example |
|--------|------|---------|
| `instrument_code` | `VARCHAR(20)` | `"GBP"`, `"KWH"` |
| `dimension` | `VARCHAR(20)` | `"CURRENCY"`, `"ENERGY"` |
| `amount` | `DECIMAL(38,18)` | `100.000000000000000000` |

Or store minor units as `BIGINT` for currencies (more compact, avoids floating-point):

| Column | Type | Example |
|--------|------|---------|
| `instrument_code` | `VARCHAR(20)` | `"GBP"` |
| `amount_minor` | `BIGINT` | `10000` (= £100.00) |

### Loading from Persistence

Use `amount.NewFromInstrument` to reconstruct from persisted columns:

```go
// From GORM entity
func toDomain(e *PositionEntity) (*domain.Position, error) {
    qty, err := amount.NewFromInstrument(
        e.InstrumentCode,
        e.Dimension,
        e.Precision,
        e.AmountMinor,
    )
    if err != nil {
        return nil, fmt.Errorf("reconstruct position amount: %w", err)
    }
    // ...
}
```

For currency-only services using minor-unit storage:

```go
m, err := money.New(e.CurrencyCode, e.AmountMinor)
```

## Proto Representation

In protobuf messages, represent quantities as structured fields (not raw `double`):

```protobuf
message Quantity {
  string instrument_code = 1;  // "GBP", "KWH"
  string dimension       = 2;  // "CURRENCY", "ENERGY"
  int64  amount_minor    = 3;  // in smallest unit (pence, watt-hours)
  int32  precision       = 4;  // decimal places (2 for GBP, 3 for KWH)
}
```

In the service layer, map proto quantities to `Amount`:

```go
func protoToAmount(q *pb.Quantity) (sharedamount.Amount, error) {
    return sharedamount.NewFromInstrument(
        q.InstrumentCode,
        q.Dimension,
        int(q.Precision),
        q.AmountMinor,
    )
}
```

## Service Domain Re-exports

Each service's `domain/quantity.go` re-exports the types it needs, providing a stable
local name that doesn't require services to import `shared/platform/quantity` directly:

```go
// services/position-keeping/domain/quantity.go
package domain

import (
    sharedamount "github.com/meridianhub/meridian/shared/pkg/amount"
    "github.com/meridianhub/meridian/shared/platform/quantity"
)

type (
    Qty[D quantity.Dimension] = quantity.Qty[D]
    Money                     = quantity.Money
    Asset                     = quantity.Asset
    Amount                    = sharedamount.Amount
    Monetary                  = quantity.Monetary
    Commodity                 = quantity.Commodity
    Instrument                = quantity.Instrument
)
```

This pattern lets domain code write `Money` rather than `quantity.Money`, and avoids
leaking the shared package path into every domain file.

## Summary Table

| Type | Package | Dimension | Use For |
|------|---------|-----------|---------|
| `Qty[Monetary]` / `Money` | `shared/platform/quantity` | Currency only | Domain arithmetic with currencies |
| `Qty[Commodity]` / `Asset` | `shared/platform/quantity` | Commodity only | Domain arithmetic with non-currency assets |
| `money.Money` | `shared/pkg/money` | Currency only | Minor-unit APIs, payment gateways, legacy code |
| `amount.Amount` | `shared/pkg/amount` | Any dimension | Persistence, protos, dimension-agnostic functions |
