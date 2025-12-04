---
name: adr-013-generic-asset-quantity-types
description: Generic Quantity[U] type system enabling multi-asset ledger with compile-time unit safety
triggers:
  - Extending the ledger to support non-fiat assets (energy, compute, tokens)
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

Meridian is a **high-integrity transaction engine**. At its core, the system does two things:

1. **Position Keeping**: Track quantities in their native units
2. **Valuation**: Convert positions to settlement currency

### The Key Insight: Fiat is the Degenerate Case

Today, Meridian tracks fiat currency. The valuation function is trivial: £1 = £1.

But the architecture we've built - Position Keeping, Financial Accounting, Sagas, audit trails -
applies to *any* quantifiable asset. The only thing that changes is the **valuation function**:

| Asset Type | Position (Native Unit) | Valuation Function | Settlement |
|------------|----------------------|-------------------|------------|
| Fiat | £100.00 | Identity (1:1) | £100.00 |
| Energy | 150 kWh | Tariff × Time-of-Use | £52.50 |
| Compute | 10 GPU-hours | Spot Price × Region | $25.00 |
| Derivatives | 100 Options | Black-Scholes(Δ,Θ,Γ,ν,ρ) | £3,450.00 |
| Carbon | 50 tCO2e | Market Price | €2,750.00 |

**Position Keeping is asset-agnostic. Valuation is where complexity lives.**

This separation is powerful:
- The ledger doesn't need to understand tariff curves or option Greeks
- New asset classes plug in via valuation providers
- The core remains simple, auditable, and correct

### Why This Matters

The same challenges appear across industries:

**Telemetry Complexity**
- Data arrives out-of-order, with gaps, duplicates, or corrections
- Estimated readings must reconcile against actuals
- Meters/sensors may be unreliable or delayed

**Valuation Complexity**
- Time-varying rates (the same unit has different values at different times)
- Complex pricing models (spot vs reserved, tiered, auction-based)
- Risk metrics and sensitivities (Greeks for derivatives)

**Regulatory Requirements**
- Every calculation must be reproducible and explainable
- Full audit trail from position to settlement
- Dispute resolution requires historical rate lookup

These aren't energy-specific or compute-specific problems. They're **position keeping and
valuation problems** that happen to manifest in different domains.

### Current State: Fiat-Only Money Types

The current implementation has three independent `Money` structs:

| Service | Implementation | Precision | Currency Type |
|---------|---------------|-----------|---------------|
| Position Keeping | `decimal.Decimal` + `Currency` enum | Arbitrary | Enum (7 currencies) |
| Financial Accounting | `decimal.Decimal` + `Currency` enum | Arbitrary | Enum |
| Current Account | `int64` (cents) + `string` | Fixed (cents) | String |

This creates problems:

1. **Duplication**: Same logic repeated three times with subtle differences
2. **Type safety gaps**: Nothing prevents adding GBP to USD at compile time
3. **Fiat-only**: Cannot represent kWh, GPU-hours, or other units
4. **No temporal awareness**: No concept of "when" a quantity was measured

## Decision Drivers

* **Compile-time safety**: Catch unit mixing errors at build time, not runtime
* **Asset agnosticism**: Support arbitrary asset types without modifying core libraries
* **Temporal awareness**: Quantities must associate with measurement timestamps
* **Precision flexibility**: Different assets need different precision (fiat: 2, crypto: 8, energy: 3)
* **Pluggable valuation**: Ledger routes to valuation engines, doesn't implement the math
* **Migration path**: Existing Money usage must migrate incrementally
* **Performance**: Generic types should have zero runtime overhead

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

```
                         ┌─────────────────────┐
                         │    Unit interface   │
                         │  ─────────────────  │
                         │  Symbol() string    │
                         │  Precision() int    │
                         └─────────┬───────────┘
                                   │
          ┌────────────────────────┼────────────────────────┐
          │                        │                        │
          ▼                        ▼                        ▼
   ┌─────────────┐         ┌─────────────┐          ┌─────────────┐
   │  Currency   │         │     ...     │          │     ...     │
   │   (fiat)    │         │  (tenant)   │          │  (tenant)   │
   └──────┬──────┘         └──────┬──────┘          └──────┬──────┘
          │                       │                        │
          ▼                       ▼                        ▼
   ┌─────────────┐         ┌─────────────┐          ┌─────────────┐
   │  Quantity   │         │  Quantity   │          │  Quantity   │
   │ [Currency]  │         │   [...]     │          │   [...]     │
   │  = Money    │         │             │          │             │
   └─────────────┘         └─────────────┘          └─────────────┘
```

### The Position/Valuation Model

```
┌─────────────────────────────────────────────────────────────────────┐
│                         POSITION KEEPING                             │
│                                                                      │
│   Tracks: Quantity[U] - amounts in native units                     │
│   Handles: Telemetry ingestion, deduplication, gap detection        │
│   Output: Timestamped positions with full audit trail               │
│                                                                      │
└─────────────────────────────────┬───────────────────────────────────┘
                                  │
                                  │ Position + Rate
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                           VALUATION                                  │
│                                                                      │
│   Input: Quantity[U] + Rate[U, Currency] + MarketData               │
│   Routes to: Appropriate ValuationProvider for asset class          │
│   Output: Quantity[Currency] + RiskMetrics                          │
│                                                                      │
│   ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐              │
│   │ Identity │ │  Tariff  │ │  Spot    │ │QuantLib  │  ...         │
│   │  (fiat)  │ │ (energy) │ │(compute) │ │(derivs)  │              │
│   └──────────┘ └──────────┘ └──────────┘ └──────────┘              │
│                                                                      │
└─────────────────────────────────┬───────────────────────────────────┘
                                  │
                                  │ Valued Position
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      FINANCIAL ACCOUNTING                            │
│                                                                      │
│   Records: Quantity[Currency] - settlement amounts                  │
│   Provides: Double-entry ledger, audit trail, reconciliation        │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

**For fiat currency**: Valuation is the identity function. Position = Settlement.

**For everything else**: Valuation is where the domain complexity lives. The ledger
doesn't need to understand it - just route to the right provider.

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

// Type alias for common domain usage
type Money = Quantity[Currency]
```

### Compile-Time Safety

```go
// COMPILES: Same unit type
gbp := quantity.New(decimal.NewFromInt(100), currency.GBP)
usd := quantity.New(decimal.NewFromInt(50), currency.USD)
sum, err := gbp.Add(gbp)  // OK: Quantity[Currency] + Quantity[Currency]

// COMPILE ERROR: Different unit types
// (assuming tenant has defined an EnergyUnit)
kwh := quantity.New(decimal.NewFromFloat(150.5), energy.KWH)
invalid := gbp.Add(kwh)  // Error: cannot use Quantity[EnergyUnit] as Quantity[Currency]

// RUNTIME CHECK: Same type, different units (GBP vs USD)
mixed, err := gbp.Add(usd)  // Returns ErrUnitMismatch
```

The generic constraint catches type-level errors (adding money to energy) at compile time.
Same-type but different-unit errors (GBP vs USD) are caught at runtime, matching current behavior.

### Package Structure

```
pkg/platform/quantity/
├── quantity.go       // Quantity[U] generic type and operations
├── unit.go           // Unit interface definition
│
└── currency/         // Fiat currency (ISO 4217)
    ├── currency.go   // Currency type implementing Unit
    └── codes.go      // Standard currency codes

# Tenants define their own unit types:
# internal/tenant-acme/units/...
```

The core library provides `Quantity[U]` and `Currency`. Tenants extend with their own
unit types as needed. The ledger doesn't prescribe what assets you can track.

### Rate Type for Valuation

The `Rate` type models conversion factors between unit types.

```go
// Rate represents a conversion factor between two unit types.
// For temporal pricing, rates have validity periods.
type Rate[From, To Unit] struct {
    from      From
    to        To
    factor    decimal.Decimal
    validFrom time.Time         // Optional: rate effective period
    validTo   time.Time
}

// Identity rate for fiat (£1 = £1)
identityRate := rate.New(currency.GBP, currency.GBP, decimal.NewFromInt(1))

// FX rate
fxRate := rate.New(currency.USD, currency.GBP, decimal.NewFromFloat(0.79))
```

### Pluggable Valuation Architecture

The ledger doesn't implement valuation math. It routes to specialized providers.

```go
// ValuationProvider is implemented by each pricing engine
type ValuationProvider interface {
    Valuate(ctx context.Context, req ValuationRequest) (ValuationResponse, error)
    Supports(assetClass string) bool
}

// The orchestrator routes to the appropriate provider
type ValuationOrchestrator struct {
    providers []ValuationProvider
    marketData MarketDataService
}

func (v *ValuationOrchestrator) Valuate(ctx context.Context, position Position) (Money, error) {
    provider := v.findProvider(position.AssetClass)
    if provider == nil {
        // No valuation needed - already in settlement currency
        return position.Amount.(Money), nil
    }
    return provider.Valuate(ctx, position, v.marketData)
}
```

This enables:
- **Fiat**: Identity provider (or nil - no valuation needed)
- **Tenant A**: Custom tariff engine for their domain
- **Tenant B**: Integration with their existing pricing system
- **Complex assets**: QuantLib wrapper, Monte Carlo, etc.

### Handling Telemetry Complexity

For assets with complex ingestion requirements, `TimestampedQuantity` captures metadata:

```go
// TimestampedQuantity wraps Quantity with measurement metadata
type TimestampedQuantity[U Unit] struct {
    Quantity[U]
    MeasuredAt   time.Time     // When the reading was taken
    ReceivedAt   time.Time     // When we received it (may differ)
    SourceID     string        // Meter/sensor identifier
    IsEstimate   bool          // Estimated vs actual reading
    SupersedesID *string       // If this corrects a previous reading
}

// Position Keeping handles:
// - Out-of-order: Sort by MeasuredAt, not ReceivedAt
// - Duplicates: Dedupe by SourceID + MeasuredAt
// - Gaps: Detect missing periods, flag for estimation
// - Corrections: SupersedesID links actual to estimated readings
```

This is optional complexity - fiat positions don't need it.

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

Phase 4: Remove legacy types once all services migrated

## Positive Consequences

* **Compile-time unit safety**: Cannot add GBP to kWh - caught by compiler
* **Single source of truth**: One Quantity implementation across all services
* **Extensible**: New asset types add Unit implementation only - no core changes
* **Pluggable valuation**: Complex pricing logic isolated in tenant-specific providers
* **Clean abstraction**: Position Keeping is asset-agnostic; Valuation is the plugin point
* **Zero runtime overhead**: Go monomorphizes generics - no interface dispatch

## Negative Consequences

* **Learning curve**: Team must understand Go generics (introduced Go 1.18)
* **Migration effort**: Three existing Money implementations to converge
* **Proto complexity**: Protocol Buffers don't support generics - need separate messages

## Pros and Cons of the Options

### String-Based Units

`Amount{Value: 100, Unit: "kWh"}`

* Good, because simple to implement
* Good, because works with any unit without code changes
* Bad, because no compile-time checking - "kwh" vs "kWh" vs "KWH" errors
* Bad, because arithmetic operations need runtime unit validation
* Bad, because precision not tied to unit

### Interface-Based Polymorphism

`type Asset interface { Unit() string; Amount() decimal.Decimal }`

* Good, because follows traditional Go patterns
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
* Bad, because massive code duplication (Add, Subtract, etc. for each type)
* Bad, because no shared abstraction for cross-asset operations
* Bad, because new asset types require full implementation

## Links

* [ADR-0003: Database Schema Migrations](0003-database-schema-migrations.md) - Money struct examples
* [ADR-0005: Adapter Pattern](0005-adapter-pattern-layer-translation.md) - Layer translation patterns
* [Go Generics Tutorial](https://go.dev/doc/tutorial/generics) - Official documentation
* [shopspring/decimal](https://github.com/shopspring/decimal) - Precise decimal arithmetic
* [samber/mo](https://github.com/samber/mo) - Option/Result types (PR #189)

## Notes

### Protocol Buffer Representation (Wire Format)

Protocol Buffers don't support generics. Two approaches:

**Option A: Asset-specific messages** (current fiat approach)
```protobuf
message MoneyAmount {
  string amount = 1;
  string currency = 2;  // ISO 4217
}
```
*Risk: Requires recompile/redeploy for every new asset type.*

**Option B: Generic asset message** (recommended for extensibility)
```protobuf
message AssetAmount {
  string amount = 1;       // Decimal as string
  string asset_code = 2;   // "GBP", "KWH", "GPU-A100"
}
```

The adapter layer (per [ADR-0005](0005-adapter-pattern-layer-translation.md)) hydrates the generic
proto into a specific `Quantity[U]` based on context. If a service expects `Quantity[EnergyUnit]`
and receives `asset_code: "GBP"`, the adapter returns an error.

This allows new asset types without API recompilation - the adapter validates at runtime.

### Database Persistence (GORM)

Go generics and ORMs often conflict. Strategy:

**Storage**: Persist `Quantity[U]` as composite columns:
```sql
CREATE TABLE positions (
    id UUID PRIMARY KEY,
    amount DECIMAL(38, 18) NOT NULL,  -- Precise decimal
    unit_code VARCHAR(16) NOT NULL,    -- "GBP", "KWH", etc.
    -- ...
);
```

**Why composite columns over JSONB:**
- SQL-level aggregation (`SUM(amount) WHERE unit_code = 'KWH'`)
- Indexing on `unit_code` for tenant queries
- Schema enforcement at database level

**Go implementation**: Custom `Scanner`/`Valuer` for GORM:
```go
func (q *Quantity[U]) Scan(value interface{}) error {
    // Hydrate from (amount, unit_code) composite
}

func (q Quantity[U]) Value() (driver.Value, error) {
    // Serialize to composite columns
}
```

**Consistency enforcement**: The type parameter `U` is enforced at the application layer.
The database stores the generic `unit_code` string; the adapter validates it matches
the expected `U` when reading.

### The Valuation Engine

This ADR enables the pluggable valuation architecture:

1. **Position Keeping** tracks `Quantity[U]` - amounts in native units
2. **Valuation Engine** routes to appropriate provider based on asset class
3. **Financial Accounting** records `Quantity[Currency]` - settled values

For fiat, step 2 is identity (or skipped entirely). For everything else, it's where
domain complexity lives - but that complexity is encapsulated in valuation providers,
not spread through the ledger.

### Reconsidering This Decision

Revisit if:
- Go generics prove to have unexpected performance issues in hot paths
- Team struggles with generic type signatures after reasonable learning period
- A superior approach emerges in the Go ecosystem for domain modeling
- Valuation provider interface proves too constraining for complex asset classes
