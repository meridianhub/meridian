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

Meridian is evolving from a "Production-Grade Open Banking Ledger" to a **Universal Asset Bank** - a
high-integrity transaction engine capable of tracking any quantifiable, time-sensitive asset with
complex valuation requirements.

### The RBC Pattern: Read, Bill, Collect

Across multiple industries, we observe the same fundamental pattern:

| Phase | Description | Challenge |
|-------|-------------|-----------|
| **Read** | Capture usage/consumption from edge telemetry | Out-of-order delivery, duplicates, gaps, estimated vs actual |
| **Bill** | Apply time-varying rates to produce valuations | Dynamic pricing, complex tariffs, multiple rate structures |
| **Collect** | Settle in fiat currency with full audit trail | Reconciliation, disputes, regulatory compliance |

This pattern appears in:

**Energy & Utilities**
- Smart meter readings (half-hourly kWh) with estimated vs actual reconciliation
- Time-of-use tariffs varying by 30-minute settlement period
- Carbon intensity multipliers for green energy tracking

**Cloud & Compute**
- GPU-hour consumption from distributed clusters
- Spot vs reserved instance pricing (like energy day-ahead vs intraday markets)
- Per-tenant resource attribution across shared infrastructure

**Advertising & Attention**
- Real-time bidding (RTB) for ad impressions - auction-based pricing
- Click/conversion attribution with complex multi-touch models
- Budget pacing across time zones and campaigns

**Logistics & Capacity**
- Container shipping (TEU) with spot rate volatility
- Warehouse slot reservations with seasonal pricing
- Last-mile delivery surge pricing

**Financial Instruments**
- Derivative valuations requiring "the Greeks" (Delta, Theta, Vega)
- Bond pricing with yield curve dependencies
- Options with Black-Scholes or Monte Carlo valuations

### The Common Challenge

All these domains share critical complexity:

1. **Unreliable Telemetry**: Data arrives out-of-order, with gaps, duplicates, or corrections
2. **Temporal Pricing**: The same unit has different values at different times
3. **Estimated vs Actual**: Initial estimates must reconcile against final actuals
4. **Complex Valuation**: Simple multiplication isn't enough - need pluggable pricing engines
5. **Regulatory Audit**: Every calculation must be reproducible and explainable

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
3. **Fiat-only**: Cannot represent kWh, GPU-hours, or ad impressions
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
   │  Currency   │         │ EnergyUnit  │          │ ComputeUnit │
   │   (fiat)    │         │  (metered)  │          │   (cloud)   │
   └──────┬──────┘         └──────┬──────┘          └──────┬──────┘
          │                       │                        │
          ▼                       ▼                        ▼
   ┌─────────────┐         ┌─────────────┐          ┌─────────────┐
   │  Quantity   │         │  Quantity   │          │  Quantity   │
   │ [Currency]  │         │[EnergyUnit] │          │[ComputeUnit]│
   │  = Money    │         │  = Energy   │          │ = Compute   │
   └─────────────┘         └─────────────┘          └─────────────┘
```

### Target Use Cases

This type system is designed to support:

| Domain | Unit Type | Example Units | Valuation Complexity |
|--------|-----------|---------------|---------------------|
| **Banking** | Currency | GBP, USD, EUR | FX rates, interest curves |
| **Energy** | EnergyUnit | kWh, MWh, Therm | Half-hourly tariffs, carbon intensity |
| **Compute** | ComputeUnit | GPU-Hour, CPU-Hour | Spot pricing, reserved capacity |
| **Advertising** | ImpressionUnit | CPM, CPC, CPA | Real-time bidding, attribution |
| **Carbon** | CarbonUnit | tCO2e, kgCO2e | Market-traded allowances |
| **Logistics** | CapacityUnit | TEU, Pallet-Day | Seasonal rates, surge pricing |
| **Crypto** | TokenUnit | BTC, ETH, USDC | Exchange rates, gas fees |
| **Loyalty** | LoyaltyUnit | Points, Miles | Redemption rates, tier multipliers |
| **Derivatives** | ContractUnit | Option, Future | Greeks (Δ, Θ, Γ, ν, ρ) |

Each domain has unique valuation requirements, but all share the same fundamental ledger operations:
track quantities, apply rates, produce settlements.

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

// ComputeUnit implements Unit for cloud resource consumption.
type ComputeUnit struct {
    symbol    string // "GPU-hr", "CPU-hr", "vCPU-sec"
    precision int    // typically 6 for fine-grained billing
}

// Type aliases for common domain usage
type Money = Quantity[Currency]
type Energy = Quantity[EnergyUnit]
type Compute = Quantity[ComputeUnit]
```

### Compile-Time Safety

```go
// COMPILES: Same unit type
gbp := quantity.New(decimal.NewFromInt(100), currency.GBP)
usd := quantity.New(decimal.NewFromInt(50), currency.USD)
sum, err := gbp.Add(gbp)  // OK: Quantity[Currency] + Quantity[Currency]

// COMPILE ERROR: Different unit types
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
├── currency/         // Fiat currency (ISO 4217)
│   ├── currency.go
│   └── codes.go
│
├── energy/           // Energy metering (kWh, MWh, Therm)
│   ├── energy.go
│   └── units.go
│
├── compute/          // Cloud resources (GPU-hr, CPU-hr)
│   ├── compute.go
│   └── units.go
│
└── examples/         // Reference implementations
    ├── carbon/       // Emissions trading
    ├── loyalty/      // Points programs
    └── advertising/  // Impression tracking
```

### Rate Type for Temporal Pricing

The `Rate` type models time-varying conversion factors - the heart of the "Bill" phase.

```go
// Rate represents a conversion factor between two unit types.
// Supports temporal pricing: the rate at 14:00 differs from 03:00.
type Rate[From, To Unit] struct {
    from      From
    to        To
    factor    decimal.Decimal
    validFrom time.Time         // Rate effective period
    validTo   time.Time
}

// Example: Half-hourly electricity tariff
peakRate := rate.New(
    energy.KWH,
    currency.GBP,
    decimal.NewFromFloat(0.35),    // £0.35/kWh
    time.Date(2025, 1, 15, 16, 0, 0, 0, time.UTC),  // 16:00
    time.Date(2025, 1, 15, 16, 30, 0, 0, time.UTC), // 16:30
)

// Example: GPU spot pricing
spotRate := rate.New(
    compute.GPUHour,
    currency.USD,
    decimal.NewFromFloat(2.50),    // $2.50/GPU-hr
    time.Now(),
    time.Now().Add(5 * time.Minute), // Valid for 5 minutes
)
```

### Pluggable Valuation Architecture

The ledger doesn't implement valuation math. It routes to specialized engines.

```
┌─────────────────────────────────────────────────────────────────┐
│                    Valuation Orchestrator                        │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │
│  │   Router    │──│ Market Data │──│   Cache     │              │
│  └──────┬──────┘  └─────────────┘  └─────────────┘              │
│         │                                                        │
│    ┌────┴────┬────────────┬────────────┬────────────┐           │
│    ▼         ▼            ▼            ▼            ▼           │
│ ┌──────┐ ┌──────┐   ┌──────────┐ ┌──────────┐ ┌──────────┐     │
│ │Energy│ │Compute│  │Derivatives│ │  Carbon  │ │  Custom  │     │
│ │Pricer│ │Pricer │  │(QuantLib) │ │ (Market) │ │ (Tenant) │     │
│ └──────┘ └──────┘   └──────────┘ └──────────┘ └──────────┘     │
└─────────────────────────────────────────────────────────────────┘
```

Each valuation provider implements a common gRPC interface:

```protobuf
service ValuationProvider {
  rpc Valuate(ValuateRequest) returns (ValuateResponse);
}

message ValuateRequest {
  string asset_id = 1;
  string asset_class = 2;           // "ENERGY", "COMPUTE", "DERIVATIVE"
  string quantity = 3;              // Decimal as string
  string unit = 4;                  // "kWh", "GPU-hr"
  google.protobuf.Timestamp as_of = 5;
  map<string, string> market_inputs = 6;  // Spot price, volatility, etc.
}

message ValuateResponse {
  MoneyAmount value = 1;            // The calculated value
  map<string, double> risk_metrics = 2;  // Greeks, sensitivities
  string valuation_model = 3;       // "BLACK_SCHOLES", "MONTE_CARLO"
}
```

This enables:
- **Energy tenant**: Routes to tariff engine with half-hourly rates
- **Compute tenant**: Routes to spot pricing engine with availability curves
- **Derivatives tenant**: Routes to QuantLib wrapper for Greeks calculation
- **Custom tenant**: Routes to tenant-provided valuation service

### Handling Unreliable Telemetry

The type system supports the complexity of real-world data ingestion:

```go
// TimestampedQuantity wraps Quantity with measurement metadata
type TimestampedQuantity[U Unit] struct {
    Quantity[U]
    MeasuredAt   time.Time     // When the meter reading was taken
    ReceivedAt   time.Time     // When we received it (may differ)
    SourceID     string        // Meter/sensor identifier
    IsEstimate   bool          // Estimated vs actual reading
    SupersedesID *string       // If this corrects a previous reading
}

// Position Keeping handles:
// - Out-of-order: Sort by MeasuredAt, not ReceivedAt
// - Duplicates: Dedupe by SourceID + MeasuredAt
// - Gaps: Detect missing settlement periods, flag for estimation
// - Corrections: SupersedesID links actual to estimated readings
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

Phase 4: Remove legacy types once all services migrated

## Positive Consequences

* **Compile-time unit safety**: Cannot add GBP to kWh - caught by compiler
* **Single source of truth**: One Quantity implementation across all services
* **Extensible**: New asset types add Unit implementation only - no core changes
* **Pluggable valuation**: Complex pricing logic isolated in specialized engines
* **Temporal pricing**: Rate type models time-varying valuations naturally
* **Telemetry-ready**: TimestampedQuantity handles real-world data complexity
* **Zero runtime overhead**: Go monomorphizes generics - no interface dispatch

## Negative Consequences

* **Learning curve**: Team must understand Go generics (introduced Go 1.18)
* **Migration effort**: Three existing Money implementations to converge
* **Proto complexity**: Protocol Buffers don't support generics - need separate messages
* **Valuation complexity**: Each asset class needs its own pricing engine

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
* Task Master: `go-compile-time-safety` tag, Task #6

## Notes

### Protocol Buffer Representation

Since Protocol Buffers don't support generics, the API layer uses explicit message types:

```protobuf
message MoneyAmount {
  string amount = 1;      // Decimal as string for precision
  string currency = 2;    // ISO 4217 code
}

message EnergyAmount {
  string amount = 1;
  string unit = 2;        // "kWh", "MWh"
  google.protobuf.Timestamp measured_at = 3;
  bool is_estimate = 4;
}

message ComputeAmount {
  string amount = 1;
  string unit = 2;        // "GPU-hr", "CPU-hr"
  string resource_id = 3; // Which GPU/cluster
}
```

### Future: The Valuation Engine (Task 10)

This ADR enables the pluggable valuation architecture:

1. **Position Keeping** tracks `Quantity[U]` - usage in native units
2. **Market Information** provides `Rate[U, Currency]` - time-varying prices
3. **Valuation Engine** routes to appropriate provider based on asset class
4. **Financial Accounting** records `Quantity[Currency]` - settled values

The valuation engine doesn't know Black-Scholes or tariff curves. It knows routing.
Each asset class plugs in its own pricing logic via the `ValuationProvider` interface.

### Design Considerations for Complex Domains

**Energy**: Half-hourly settlement periods, estimated vs actual readings, carbon intensity
multipliers, reactive power charges, grid balancing costs.

**Compute**: Spot vs reserved pricing, preemption credits, multi-region arbitrage,
sustained use discounts, committed use contracts.

**Derivatives**: Greeks calculation (Δ, Θ, Γ, ν, ρ), implied volatility surfaces,
Monte Carlo simulations, regulatory capital requirements.

The type system provides the foundation. Domain-specific complexity lives in the
valuation providers, not the ledger.

### Reconsidering This Decision

Revisit if:
- Go generics prove to have unexpected performance issues in hot paths
- Team struggles with generic type signatures after reasonable learning period
- A superior approach emerges in the Go ecosystem for domain modeling
- Valuation provider interface proves too constraining for complex asset classes
