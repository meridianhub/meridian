---
name: adr-013-generic-asset-quantity-types
description: Generic Quantity[U] type system enabling multi-asset ledger with compile-time unit safety
triggers:
  - Extending the ledger to support non-fiat assets (kWh, GPU-hours, carbon credits)
  - Refactoring Money types across services
  - Implementing temporal pricing or valuation engines
  - Designing multi-asset portfolio or inventory systems
instructions: |
  Use Quantity[U] generic type where U is a unit type (Currency, EnergyUnit, ComputeUnit, etc.).
  All arithmetic operations enforce compile-time unit matching - you cannot add GBP to kWh.
  Migrate existing Money types to Quantity[Currency] as the fiat specialization.
  New asset types implement the Unit interface and get type-safe quantities automatically.
---

# 13. Generic Asset and Quantity Type System

Date: 2025-12-03

## Status

Proposed

## Context

Meridian is evolving from a "Production-Grade Open Banking Ledger" to a "Universal Asset Bank" - a platform
capable of tracking not just fiat currency, but any quantifiable asset: energy (kWh), compute (GPU-hours),
carbon credits, loyalty points, or cryptocurrency.

The current implementation has three independent `Money` structs across services:

| Service | Implementation | Precision | Currency Type |
|---------|---------------|-----------|---------------|
| Position Keeping | `decimal.Decimal` + `Currency` enum | Arbitrary | Enum (7 currencies) |
| Financial Accounting | `decimal.Decimal` + `Currency` enum | Arbitrary | Enum |
| Current Account | `int64` (cents) + `string` | Fixed (cents) | String |

This creates several problems:

1. **Duplication**: Same logic repeated three times with subtle differences
2. **Type safety gaps**: Nothing prevents adding GBP to USD at compile time - only runtime checks
3. **Fiat-only**: The `Currency` type cannot represent kWh or GPU-hours without awkward extensions
4. **Inconsistent precision**: Some services use decimal, others use fixed-point integers

The "Temporal Pricing Engine" roadmap requires:

- Tracking **usage** in native units (e.g., 150 kWh)
- Applying **tariffs** from market data (e.g., £0.28/kWh at 14:00)
- Producing **bills** in fiat currency (e.g., £42.00)

This demands a type system that can safely handle multiple asset types and prevent unit mixing.

## Decision Drivers

* **Compile-time safety**: Catch unit mixing errors at build time, not runtime
* **Asset agnosticism**: Support arbitrary asset types without modifying core libraries
* **Precision flexibility**: Different assets need different precision (fiat: 2-4 decimals, crypto: 8+, energy: 3)
* **Migration path**: Existing Money usage must migrate incrementally without big-bang refactor
* **Performance**: Generic types should have zero runtime overhead (monomorphization)
* **Go idioms**: Solution must feel natural to Go developers, not fight the language

## Considered Options

1. **String-based units**: `Amount{Value: 100, Unit: "kWh"}` - runtime validation only
2. **Interface-based polymorphism**: `type Asset interface { Unit() string }` - runtime dispatch
3. **Generic Quantity[U] with unit constraints**: `Quantity[Currency]`, `Quantity[EnergyUnit]` - compile-time
4. **Separate types per asset class**: `Money`, `Energy`, `Compute` - no shared abstraction

## Decision Outcome

Chosen option: **Generic Quantity[U] with unit constraints**, because it provides compile-time safety
while maintaining Go's simplicity. The generic approach allows new asset types to be added without
modifying the core quantity library, and produces zero-overhead code through Go's monomorphization.

### Type Hierarchy

The `Unit` interface is open for extension. Any type implementing `Unit` can be used with `Quantity[U]`.
The examples below (Currency, EnergyUnit, ComputeUnit) are illustrative - the system supports arbitrary
asset classes including loyalty points, air miles, water usage, bandwidth, storage, tokens, or any
other quantifiable unit a tenant may need.

```
                         ┌─────────────────────┐
                         │    Unit interface   │
                         │  ─────────────────  │
                         │  Symbol() string    │
                         │  Precision() int    │
                         └─────────┬───────────┘
                                   │
                    ┌──────────────┼──────────────┐
                    │              │              │
                    ▼              ▼              ▼
             ┌───────────┐  ┌───────────┐  ┌───────────┐
             │ Currency  │  │EnergyUnit │  │    ...    │
             │ (example) │  │ (example) │  │ (custom)  │
             └─────┬─────┘  └─────┬─────┘  └─────┬─────┘
                   │              │              │
                   ▼              ▼              ▼
             ┌───────────┐  ┌───────────┐  ┌───────────┐
             │ Quantity  │  │ Quantity  │  │ Quantity  │
             │[Currency] │  │[EnergyUnit│  │ [Custom]  │
             │ = Money   │  │ = Energy  │  │           │
             └───────────┘  └───────────┘  └───────────┘
```

**Example unit types** (not exhaustive):
- `Currency` - Fiat money (GBP, USD, EUR)
- `EnergyUnit` - Power consumption (kWh, MWh, Therm)
- `ComputeUnit` - Processing time (GPU-Hour, CPU-Hour)
- `CarbonUnit` - Emissions (tCO2e, kgCO2e)
- `LoyaltyUnit` - Points, miles, rewards
- `StorageUnit` - Data (GB, TB)
- `BandwidthUnit` - Transfer (Mbps, GB/month)
- `TokenUnit` - Crypto or internal tokens
- *...any custom unit a tenant defines*

### Core Types

```go
// Unit is the constraint interface for all quantity units.
// Any type implementing Unit can be used with Quantity[U].
type Unit interface {
    // Symbol returns the unit identifier (e.g., "GBP", "kWh", "GPU-hr")
    Symbol() string

    // Precision returns the number of decimal places for this unit.
    // GBP: 2, BTC: 8, kWh: 3
    Precision() int
}

// Quantity represents a precise amount of a specific unit type.
// The type parameter U ensures compile-time unit matching.
type Quantity[U Unit] struct {
    amount decimal.Decimal
    unit   U
}

// Currency implements Unit for fiat currencies (ISO 4217).
type Currency struct {
    code      string // "GBP", "USD", "EUR"
    precision int    // 2 for most, 0 for JPY
}

// EnergyUnit implements Unit for energy measurements.
type EnergyUnit struct {
    symbol    string // "kWh", "MWh", "Therm"
    precision int    // typically 3
}

// Type aliases for common domain usage
type Money = Quantity[Currency]
type Energy = Quantity[EnergyUnit]
```

### Compile-Time Safety

```go
// COMPILES: Same unit type
gbp := quantity.New(decimal.NewFromInt(100), currency.GBP)
usd := quantity.New(decimal.NewFromInt(50), currency.USD)
sum, err := gbp.Add(gbp)  // OK: Quantity[Currency] + Quantity[Currency]

// COMPILE ERROR: Different unit types
kwh := quantity.New(decimal.NewFromInt(150), energy.KWH)
invalid := gbp.Add(kwh)  // Error: cannot use Quantity[EnergyUnit] as Quantity[Currency]

// RUNTIME CHECK: Same type, different units (GBP vs USD)
mixed, err := gbp.Add(usd)  // Returns ErrUnitMismatch
```

The generic constraint catches type-level errors (adding money to energy) at compile time.
Same-type but different-unit errors (GBP + USD) are caught at runtime, matching current behavior.

### Package Structure

The core library provides the generic `Quantity[U]` type and `Unit` interface. Example unit
implementations are provided for common use cases, but tenants can define custom units in their
own packages without modifying the core library.

```
pkg/platform/quantity/
├── quantity.go       // Quantity[U] generic type and operations
├── unit.go           // Unit interface definition
│
├── currency/         // Example: fiat currency
│   ├── currency.go   // Currency type implementing Unit
│   └── codes.go      // ISO 4217 currency codes
│
└── examples/         // Reference implementations for other asset classes
    ├── energy/       // kWh, MWh, Therm, BTU
    ├── compute/      // GPU-Hour, CPU-Hour
    └── carbon/       // tCO2e, kgCO2e

# Tenants define custom units in their own packages:
# internal/tenant-acme/units/airmiles/
# internal/tenant-xyz/units/waterusage/
```

### Migration Strategy

Phase 1: Introduce shared types (non-breaking)
```go
// pkg/platform/quantity/currency/currency.go
// New shared Currency type with full ISO 4217 support
```

Phase 2: Create type alias in domain packages
```go
// internal/position-keeping/domain/money.go
import "github.com/meridianhub/meridian/pkg/platform/quantity"

// Money is now an alias for the shared generic type
type Money = quantity.Quantity[currency.Currency]
```

Phase 3: Migrate service internals incrementally
```go
// Each service updates to use shared type
// Old constructors wrap new ones for compatibility
func NewMoney(amount decimal.Decimal, curr Currency) (Money, error) {
    return quantity.New(amount, currency.FromLegacy(curr))
}
```

Phase 4: Remove legacy types once all services migrated

### Arithmetic Operations

All operations preserve compile-time type safety:

```go
func (q Quantity[U]) Add(other Quantity[U]) (Quantity[U], error)
func (q Quantity[U]) Subtract(other Quantity[U]) (Quantity[U], error)
func (q Quantity[U]) Multiply(scalar decimal.Decimal) Quantity[U]
func (q Quantity[U]) Divide(scalar decimal.Decimal) (Quantity[U], error)

// Cross-unit operations return a different type
func (q Quantity[U]) ConvertTo(target U, rate decimal.Decimal) Quantity[U]

// Valuation: Quantity[A] × Rate[A→B] = Quantity[B]
func Value[A, B Unit](qty Quantity[A], rate Rate[A, B]) Quantity[B]
```

### Rate Type for Conversions

```go
// Rate represents a conversion factor between two unit types.
// Used for pricing (kWh → GBP) and currency exchange (USD → EUR).
type Rate[From, To Unit] struct {
    from   From
    to     To
    factor decimal.Decimal
}

// Example: Electricity tariff
tariff := rate.New(energy.KWH, currency.GBP, decimal.NewFromFloat(0.28))
// 150 kWh × £0.28/kWh = £42.00
bill := quantity.Value(usage, tariff)  // Returns Quantity[Currency]
```

## Positive Consequences

* **Compile-time unit safety**: Cannot add GBP to kWh - caught by compiler
* **Single source of truth**: One Quantity implementation across all services
* **Extensible**: New asset types (carbon credits, loyalty points) add Unit implementation only
* **Zero runtime overhead**: Go monomorphizes generics - no interface dispatch
* **Temporal pricing enabled**: Rate[Energy, Currency] type models tariffs naturally
* **Precision per unit**: Each unit type defines appropriate decimal places

## Negative Consequences

* **Learning curve**: Team must understand Go generics (introduced Go 1.18)
* **Migration effort**: Three existing Money implementations to converge
* **Proto complexity**: Protocol Buffers don't support generics - need oneof or separate messages
* **IDE support**: Some Go IDEs still have incomplete generics support (improving)

## Pros and Cons of the Options

### String-Based Units

`Amount{Value: 100, Unit: "kWh"}`

* Good, because simple to implement
* Good, because works with any unit without code changes
* Bad, because no compile-time checking - "kwh" vs "kWh" vs "KWH" errors
* Bad, because arithmetic operations need runtime unit validation
* Bad, because precision not tied to unit - must be specified separately

### Interface-Based Polymorphism

`type Asset interface { Unit() string; Amount() decimal.Decimal }`

* Good, because follows traditional Go patterns
* Good, because works with existing codebases
* Bad, because runtime dispatch overhead
* Bad, because no compile-time prevention of unit mixing
* Bad, because requires type assertions for concrete operations

### Generic Quantity[U] (Chosen)

* Good, because compile-time unit type safety
* Good, because zero runtime overhead (monomorphization)
* Good, because extensible without modifying core library
* Good, because precision tied to unit type
* Bad, because requires Go 1.18+ (not an issue - already on 1.25)
* Bad, because more complex type signatures in function parameters

### Separate Types Per Asset Class

`type Money struct{}`, `type Energy struct{}`, `type Compute struct{}`

* Good, because explicit and simple
* Good, because no generics knowledge required
* Bad, because massive code duplication (Add, Subtract, etc. for each type)
* Bad, because no shared abstraction for cross-asset operations
* Bad, because new asset types require full implementation

## Links

* [ADR-0003: Database Schema Migrations](0003-database-schema-migrations.md) - Money struct examples
* [ADR-0005: Adapter Pattern](0005-adapter-pattern-layer-translation.md) - Layer translation will need Quantity adapters
* [Go Generics Tutorial](https://go.dev/doc/tutorial/generics) - Official Go generics documentation
* [shopspring/decimal](https://github.com/shopspring/decimal) - Decimal library used for precise arithmetic
* [samber/mo](https://github.com/samber/mo) - Option/Result types already adopted (PR #189)
* Task Master: `go-compile-time-safety` tag, Task #6

## Notes

### Protocol Buffer Representation

Since Protocol Buffers don't support generics, the API layer will use explicit message types:

```protobuf
message MoneyAmount {
  string amount = 1;      // Decimal as string for precision
  string currency = 2;    // ISO 4217 code
}

message EnergyAmount {
  string amount = 1;
  string unit = 2;        // "kWh", "MWh", etc.
}

// Adapters convert between proto and domain types
func MoneyFromProto(pb *pb.MoneyAmount) (Money, error)
func MoneyToProto(m Money) *pb.MoneyAmount
```

### Future: Valuation Engine

This ADR enables the "Temporal Pricing Engine" by providing:

1. `Quantity[EnergyUnit]` - Usage tracking in native units
2. `Rate[EnergyUnit, Currency]` - Time-varying tariffs from market data
3. `Quantity[Currency]` - Calculated bills

The valuation engine (roadmap Task 10) will subscribe to Position Keeping events and apply
rates from Market Information service to produce bills.

### Reconsidering This Decision

Revisit if:
- Go generics prove to have unexpected performance issues in hot paths
- Team struggles with generic type signatures after reasonable learning period
- A superior approach emerges in the Go ecosystem for domain modeling
