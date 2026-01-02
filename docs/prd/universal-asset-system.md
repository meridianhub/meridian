# PRD: Universal Asset System

**Status:** Draft
**ADRs:** [0013 - Universal Quantity Type System](../adr/0013-generic-asset-quantity-types.md), [0014 - Dynamic Asset Registry](../adr/0014-dynamic-asset-registry.md)
**Target Task Master Tag:** `universal-asset-system`

## Overview

Extend Meridian's ledger from fiat-only to multi-asset support. Enable tenants to define
custom assets (energy, commodities, vouchers) without code deployment while maintaining
compile-time dimensional safety.

### Goals

1. **Compile-time safety**: Prevent physics errors (money + rice) at build time
2. **Runtime flexibility**: New assets via database configuration, not code
3. **Tenant isolation**: Each tenant has their own asset catalog
4. **Valuation foundation**: Rate type for asset-to-currency conversion (providers in future PRD)

### Non-Goals (Simplified Scope)

- ~~Migration from legacy `Money` types~~ - clean implementation, no backwards compatibility
- ~~Migration-as-Trade pattern~~ - no existing positions to migrate
- ~~Version deprecation lifecycle~~ - not needed pre-production
- ~~Distributed cache invalidation~~ - simple caching sufficient for now

---

## CEL Validation Pattern

We use **Google CEL (Common Expression Language)** for attribute validation instead of JSON Schema.
CEL is non-Turing complete, compiles to bytecode, executes in nanoseconds, and can express
cross-field validation that JSON Schema cannot.

### Why CEL over JSON Schema

| Aspect | JSON Schema | CEL |
|--------|-------------|-----|
| **Performance** | ~1ms (parse + validate) | ~100ns (execute compiled) |
| **Cross-field validation** | Limited | Native: `a.x > a.y` |
| **Type coercion** | Strict | Explicit: `int(attrs['expiry'])` |
| **Ecosystem** | Web-standard | Google/Proto-native |
| **Safety** | Schema validation | Guaranteed termination |

### Example: Defining a Custom Instrument with CEL

A tenant registers "GPU-H100-SPOT" with this validation expression:

```javascript
// Rule: Must have region, and if US region, zone must be 1 or 2
has(attributes.region) &&
(attributes.region != 'us-east' || attributes.zone in ['1', '2'])
```

### Example: Validation at Ingestion

When `RecordMeasurement` receives:

```json
{
  "instrument": "GPU-H100-SPOT",
  "attributes": { "region": "us-east", "zone": "5" }
}
```

The cached CEL program executes → returns `false` → measurement rejected **before** domain layer.

### CEL Expression Examples

| Use Case | CEL Expression |
|----------|----------------|
| No constraints | `true` |
| Require field | `has(attributes.region)` |
| Enum validation | `attributes.type in ['spot', 'reserved', 'committed']` |
| Numeric check | `int(attributes.expiry) > 1700000000` |
| Cross-field | `attributes.tier == 'premium' \|\| !has(attributes.sla)` |

---

## Fungibility Resolution

Beyond ingestion validation, CEL handles **operational predicates** that determine position
behavior. This extends CEL from "is this data valid?" to "can these positions be combined?"

### The Performance Guardrail

```text
┌─────────────────────────────────────────────────────────────────────────┐
│  NATIVE GO (Hot Path)              │  CEL (Policy Decisions)           │
├─────────────────────────────────────────────────────────────────────────┤
│  Quantity.Add()                    │  validation_expression            │
│  Quantity.Sub()                    │  fungibility_expression           │
│  decimal.Decimal arithmetic        │  AreFungible(ctx_a, ctx_b)        │
│  Position aggregation loop         │  Temporal overlap policy          │
└─────────────────────────────────────────────────────────────────────────┘
```

**Rule**: CEL evaluates predicates (boolean decisions). Native Go performs arithmetic.
We never offload `Add`/`Sub` to CEL in the hot loop.

### Fungibility Expression

Each instrument defines a CEL expression that determines whether two positions can be merged:

```javascript
// Input: 'a' and 'b' are attribute maps from two positions
// Output: Boolean - true if positions can be combined into one row

// Example 1: Same expiry = fungible
a.expiry == b.expiry

// Example 2: Same region AND same tier = fungible
a.region == b.region && a.tier == b.tier

// Example 3: Temporal overlap allowed if same contract
a.contract_id == b.contract_id &&
(int(a.valid_to) >= int(b.valid_from) || int(b.valid_to) >= int(a.valid_from))
```

### How Fungibility Affects the Ledger

```mermaid
flowchart TB
    subgraph Input["Incoming Position"]
        NEW["Amount: 100 KWH<br/>attrs: {period: '14:00-15:00'}"]
    end

    subgraph Existing["Existing Positions"]
        E1["Amount: 50 KWH<br/>attrs: {period: '14:00-15:00'}"]
        E2["Amount: 75 KWH<br/>attrs: {period: '15:00-16:00'}"]
    end

    subgraph CEL["CEL Predicate: AreFungible(new, existing)"]
        C1["new vs E1: true<br/>(same period)"]
        C2["new vs E2: false<br/>(different period)"]
    end

    subgraph Result["Ledger State"]
        R1["Amount: 150 KWH ✓<br/>attrs: {period: '14:00-15:00'}"]
        R2["Amount: 75 KWH<br/>attrs: {period: '15:00-16:00'}"]
    end

    NEW --> CEL
    E1 --> C1
    E2 --> C2
    C1 -->|"MERGE"| R1
    C2 -->|"KEEP SEPARATE"| R2
```

### The Aggregation Contract

When Position Keeping aggregates positions:

1. **Dimension check** (compile-time): `Quantity[Monetary]` cannot combine with `Quantity[Commodity]`
2. **Instrument check** (runtime): USD cannot combine with EUR
3. **Version check** (runtime): USD(v1) cannot combine with USD(v2)
4. **Fungibility check** (CEL): `fungibility_expression.Eval({a: pos1.attrs, b: pos2.attrs})`

If all checks pass, positions are merged (`SUM(amount)`). Otherwise, they remain distinct rows.

### Temporal Logic

Time-bound assets (energy, licenses, subscriptions) need temporal overlap policy:

```javascript
// Can merge if periods are adjacent or overlapping AND same tariff
(int(a.period_end) >= int(b.period_start) ||
 int(b.period_end) >= int(a.period_start)) &&
a.tariff_code == b.tariff_code
```

**Edge case**: If CEL returns `false` for a merge attempt, the operation either:

- Creates a new distinct position (accumulation), OR
- Fails with `ErrPositionsNotFungible` (strict mode)

The behavior is configurable per instrument.

---

## Service Impact Matrix

Overview of all services and the changes required for Universal Asset System.

| Service | Impact | Change Type | Description |
|---------|--------|-------------|-------------|
| `reference-data` | **NEW** | Full service | New BIAN service for instrument definitions |
| `position-keeping` | **HIGH** | Domain + Adapter | Core asset tracking, CEL validation, fungibility |
| `current-account` | **HIGH** | Domain + Adapter | Multi-asset balance support |
| `financial-accounting` | **HIGH** | Domain + Adapter | Multi-dimensional ledger entries |
| `payment-order` | **MODERATE** | Adapter | Multi-asset payment instructions |
| `utilization-metering-consumer` | **MODERATE** | Domain + Adapter | Native fit for `Quantity[Commodity]` |
| `gateway` | **LOW** | Pass-through | Route new Reference Data API |
| `tenant` | **NONE** | No changes | Tenant context unchanged |
| `party` | **NONE** | No changes | Party management independent |
| `audit-worker` | **NONE** | No changes | Consumes events, no money logic |

### Shared Package Migration

The existing `shared/domain/money` package is re-exported by all services. Migration path:

```text
BEFORE                              AFTER
──────                              ─────
shared/domain/money/                pkg/platform/quantity/
├── money.go (Money struct)         ├── quantity.go (Quantity[D])
├── currency.go                     ├── dimension.go (Monetary, Commodity)
└── errors.go                       ├── instrument.go (UnitDef)
                                    └── currency/ (predefined fiat)

services/*/domain/money.go          services/*/domain/quantity.go
└── re-exports shared/domain/money  └── re-exports pkg/platform/quantity
```

**Migration sequence:**

1. Stream A creates `pkg/platform/quantity` (new, no breaking changes)
2. Per-service streams (I.1-I.4) migrate from `shared/domain/money` → `pkg/platform/quantity`
3. After all services migrated, deprecate `shared/domain/money`

---

## Work Streams

Designed for parallel execution across multiple developers. Dependencies shown in diagram below.

```mermaid
flowchart TB
    subgraph Foundation["Phase 1: Foundation (Parallel)"]
        A["Stream A<br/>Core Types<br/>(quantity pkg)"]
        D["Stream D<br/>Protobuf<br/>Definitions"]
        E["Stream E<br/>DB Schema"]
    end

    subgraph TypeExt["Phase 2: Type Extensions"]
        B["Stream B<br/>Currency<br/>Definitions"]
        C["Stream C<br/>Rate Type"]
    end

    subgraph Service["Phase 3: Service Layer"]
        F["Stream F<br/>Reference Data<br/>Service"]
    end

    subgraph API["Phase 4: API & Caching (Parallel)"]
        G["Stream G<br/>gRPC API<br/>Handlers"]
        H["Stream H<br/>Caching<br/>Layer"]
    end

    subgraph Integration["Phase 5: Service Integration (Parallel)"]
        I1["Stream I.1<br/>position-keeping"]
        I2["Stream I.2<br/>current-account"]
        I3["Stream I.3<br/>financial-accounting"]
        I4["Stream I.4<br/>payment-order &<br/>utilization-metering"]
    end

    subgraph Final["Phase 6: Verification"]
        J["Stream J<br/>Integration<br/>Tests"]
    end

    A --> B
    A --> C
    A --> F
    D --> G
    E --> F
    F --> G
    F --> H
    G --> I1
    G --> I2
    G --> I3
    G --> I4
    H --> I1
    H --> I2
    H --> I3
    H --> I4
    I1 --> J
    I2 --> J
    I3 --> J
    I4 --> J
```

---

## Stream A: Core Types Package

**Location:** `pkg/platform/quantity/`
**Dependencies:** None (foundational)

### Deliverables

1. **Dimensions** (`dimension.go`)

   ```go
   type Monetary struct{}
   type Commodity struct{}
   ```

2. **UnitDef** (`unit.go`)

   ```go
   type UnitDef struct {
       Code      string    // "USD", "KWH", "GPU-H100"
       Version   uint32    // Schema version
       Dimension string    // "Monetary" or "Commodity" - required for deserialization
       Precision int       // Decimal places
   }
   ```

   > **Serialization note**: `Dimension` is stored as a string (not type parameter) because
   > Go generics are erased at runtime. When deserializing from DB/proto, we use `Dimension`
   > to reconstruct the correct `Quantity[Monetary]` or `Quantity[Commodity]` at the boundary.

3. **Quantity[D]** (`quantity.go`)

   ```go
   type Quantity[D any] struct {
       Amount decimal.Decimal
       Unit   UnitDef
   }

   // Type aliases
   type Money = Quantity[Monetary]
   type Asset = Quantity[Commodity]
   ```

4. **Operations**: `Add`, `Subtract`, `Multiply`, `Divide`, `Neg`, `IsZero`, `Compare`
   - Same-dimension operations: compile-time safe
   - Same-unit validation: runtime check returning error

5. **PositionKey** (`position.go`)

   ```go
   type PositionKey struct {
       AccountID  string
       AssetCode  string
       Version    uint32
       Attributes map[string]string
   }
   ```

6. **Generic Bridge Factory** (`factory.go`)

   > **The Problem**: Go generics are erased at runtime, but DB/Proto use string dimensions.
   > This factory is the **only** boundary where runtime strings become compile-time types.

   ```go
   // ParseQuantity converts raw data into a typed Quantity.
   // This is the boundary between runtime (Proto/DB) and compile-time (Go generics).
   func ParseQuantity(amount decimal.Decimal, def UnitDef) (any, error) {
       switch def.Dimension {
       case "Monetary":
           return Quantity[Monetary]{Amount: amount, Unit: def}, nil
       case "Commodity":
           return Quantity[Commodity]{Amount: amount, Unit: def}, nil
       default:
           return nil, ErrUnknownDimension
       }
   }
   ```

### Acceptance Criteria

- [ ] `Quantity[Monetary].Add(Quantity[Commodity])` fails at compile time
- [ ] `USD.Add(EUR)` returns `ErrUnitMismatch` at runtime
- [ ] `USD(v1).Add(USD(v2))` returns `ErrVersionMismatch` at runtime
- [ ] `ParseQuantity` correctly bridges runtime strings to compile-time types
- [ ] 100% test coverage on arithmetic operations

---

## Stream B: Currency Definitions

**Location:** `pkg/platform/quantity/currency/`
**Dependencies:** Stream A (UnitDef type)

### Deliverables

1. **Predefined UnitDefs** for major currencies (ISO 4217):
   - USD, EUR, GBP, JPY, CHF, AUD, CAD, NZD
   - Precision: 2 for most, 0 for JPY

2. **Lookup function**:

   ```go
   func ByCode(code string) (UnitDef, bool)
   ```

3. **Constructor helpers**:

   ```go
   func USD(amount decimal.Decimal) Money
   func EUR(amount decimal.Decimal) Money
   // etc.
   ```

### Acceptance Criteria

- [ ] All major fiat currencies defined with correct precision
- [ ] `currency.USD(decimal.NewFromInt(100))` creates valid Money

---

## Stream C: Rate Type

**Location:** `pkg/platform/quantity/`
**Dependencies:** Stream A (Quantity, UnitDef types)

> **Scope boundary**: This stream covers the Rate data structure and basic conversion math only.
> ValuationProvider interface and orchestration belongs in a future Valuation Engine PRD (ADR-019).

### Deliverables

1. **Rate type** (`rate.go`)

   ```go
   type Rate struct {
       From      UnitDef
       To        UnitDef
       Factor    decimal.Decimal
       ValidFrom time.Time
       ValidTo   time.Time
   }

   // Convert applies the rate to a quantity, returning the converted amount.
   // Returns error if quantity's unit doesn't match Rate.From.
   func (r Rate) Convert(q Quantity[Monetary]) (Quantity[Monetary], error)
   ```

2. **Identity rate helper**:

   ```go
   // IdentityRate returns a 1:1 rate for same-currency operations
   func IdentityRate(unit UnitDef) Rate
   ```

3. **Rate validation**: Ensure `From != To` unless identity, validate temporal bounds

### Acceptance Criteria

- [ ] `Rate.Convert()` correctly multiplies amount by factor
- [ ] `Rate.Convert()` returns error if unit mismatch
- [ ] `IdentityRate()` returns factor of 1.0
- [ ] Rate with `ValidFrom > ValidTo` rejected

---

## Stream D: Protobuf Definitions

**Location:** `proto/platform/v1/`
**Dependencies:** Stream A (type design, can work from ADR spec)

> **Proto vs CEL**: Protobuf defines the **Container** (data structure). CEL is the **Gatekeeper**
> (validation logic). Proto messages are pure data carriers with no behavior. CEL expressions
> are compiled and executed by the service layer to validate attribute payloads.

### Deliverables

1. **InstrumentAmount message** (`quantity.proto`) - *The Data Carrier*

   ```protobuf
   message InstrumentAmount {
       string amount = 1;              // Decimal as string for precision
       string instrument_code = 2;     // "USD", "KWH", "GPU-H100"
       uint32 version = 3;             // Schema version

       // The "Payload": Raw key-value pairs.
       // Checked against validation_expression at ingestion time.
       // All values are strings; CEL handles type coercion.
       map<string, string> attributes = 4;
   }
   ```

   > **Why `map<string, string>`**: Using `google.protobuf.Struct` adds marshalling overhead
   > and CEL environment complexity. String maps are fast, simple, and CEL can coerce types
   > explicitly: `int(attributes['expiry']) > 1700000000`.

2. **InstrumentDefinition message** (`reference_data.proto`) - *Structure + Rules*

   ```protobuf
   message InstrumentDefinition {
       string id = 1;
       string tenant_id = 2;
       string code = 3;
       uint32 version = 4;
       string dimension = 5;           // "Monetary" or "Commodity"
       int32 precision = 6;

       // The "Gatekeeper": Validates if attributes are allowed for this instrument.
       // Compiled and cached. Input: attributes map. Output: bool.
       // Example: "has(attributes.region) && int(attributes.expiry) > 0"
       string validation_expression = 7;

       // The "Arbiter": Determines if two positions can be merged (fungible).
       // Compiled and cached. Input: 'a' and 'b' attribute maps. Output: bool.
       // Example: "a.expiry == b.expiry && a.region == b.region"
       // Default: "a == b" (exact attribute match required)
       string fungibility_expression = 8;

       string display_name = 9;
       string description = 10;
   }
   ```

3. **Rate message**

   ```protobuf
   message Rate {
       string from_code = 1;
       string to_code = 2;
       string factor = 3;
       google.protobuf.Timestamp valid_from = 4;
       google.protobuf.Timestamp valid_to = 5;
   }
   ```

4. **Buf breaking change detection** configured

### Acceptance Criteria

- [ ] Proto compiles without errors
- [ ] Generated Go code matches domain types
- [ ] Buf lint passes
- [ ] `validation_expression` field documented with CEL examples

---

## Stream E: Database Schema

**Location:** `services/reference-data/migrations/`
**Dependencies:** Stream A (UnitDef field design)

> **BIAN alignment**: This service maps to the BIAN `FinancialInstrumentReferenceDataManagement`
> service domain, which maintains a directory of financial instrument reference data including
> currencies, equities, debt instruments, and commodities.

### Deliverables

1. **Instrument definitions table**

   ```sql
   CREATE TABLE instrument_definitions (
       id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
       tenant_id UUID NOT NULL,
       code VARCHAR(32) NOT NULL,
       version INTEGER NOT NULL DEFAULT 1,
       dimension VARCHAR(32) NOT NULL,
       precision INTEGER NOT NULL,

       -- CEL Expressions (compiled and cached by service layer)
       validation_expression TEXT NOT NULL DEFAULT 'true',  -- Ingestion gatekeeper
       fungibility_expression TEXT NOT NULL DEFAULT 'a == b',  -- Position merge arbiter

       display_name VARCHAR(128),
       description TEXT,
       created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

       UNIQUE(tenant_id, code, version),
       CHECK (precision >= 0 AND precision <= 18),
       CHECK (dimension IN ('Monetary', 'Commodity')),
       CHECK (validation_expression <> ''),
       CHECK (fungibility_expression <> '')
   );

   CREATE INDEX idx_instrument_definitions_lookup
       ON instrument_definitions(tenant_id, code, version);
   ```

   > **Two CEL expressions per instrument**:
   > - `validation_expression`: "Is this data valid?" (ingestion)
   > - `fungibility_expression`: "Can these positions merge?" (aggregation)

2. **System tenant seed data**:

   ```sql
   -- System tenant ID: 00000000-0000-0000-0000-000000000000
   -- Platform-wide instruments accessible to ALL tenants
   -- validation_expression='true' means no attribute constraints
   INSERT INTO instrument_definitions
       (tenant_id, code, version, dimension, precision, validation_expression, display_name)
   VALUES
       ('00000000-0000-0000-0000-000000000000', 'USD', 1, 'Monetary', 2, 'true', 'US Dollar'),
       ('00000000-0000-0000-0000-000000000000', 'EUR', 1, 'Monetary', 2, 'true', 'Euro'),
       ('00000000-0000-0000-0000-000000000000', 'GBP', 1, 'Monetary', 2, 'true', 'British Pound');
   ```

### Acceptance Criteria

- [ ] Migration applies cleanly
- [ ] Unique constraint prevents duplicate code+version per tenant
- [ ] Index supports efficient lookups
- [ ] System tenant seed data inserted
- [ ] `validation_expression` column stores valid CEL expressions
- [ ] Default `'true'` allows permissive instruments (no attribute constraints)

---

## Stream F: Reference Data Service

**Location:** `services/reference-data/`
**Dependencies:** Stream A, Stream E

### Deliverables

1. **InstrumentRegistry interface** (`registry.go`)

   ```go
   // SystemTenantID is the well-known UUID for platform-wide instruments
   var SystemTenantID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

   type InstrumentRegistry interface {
       // GetDefinition looks up instrument by tenant, falling back to SystemTenant if not found.
       // Lookup order: tenant_id → SystemTenantID
       GetDefinition(ctx context.Context, tenantID uuid.UUID, code string, version uint32) (InstrumentDefinition, error)

       // GetLatestDefinition returns highest version, with same fallback logic
       GetLatestDefinition(ctx context.Context, tenantID uuid.UUID, code string) (InstrumentDefinition, error)

       // CreateDefinition creates tenant-specific instrument (cannot create in SystemTenant via API)
       // Compiles CEL expression at creation time - fails fast on syntax errors
       CreateDefinition(ctx context.Context, def InstrumentDefinition) (InstrumentDefinition, error)

       // ListDefinitions returns tenant instruments + all SystemTenant instruments
       ListDefinitions(ctx context.Context, tenantID uuid.UUID) ([]InstrumentDefinition, error)

       // ValidateAttributes executes compiled CEL program against attribute map
       ValidateAttributes(ctx context.Context, def InstrumentDefinition, attrs map[string]string) error
   }
   ```

2. **System Tenant Inheritance Logic**:

   ```go
   func (r *PostgresRegistry) GetDefinition(
       ctx context.Context, tenantID uuid.UUID, code string, version uint32,
   ) (InstrumentDefinition, error) {
       // 1. Try tenant-specific lookup
       def, err := r.queries.GetInstrumentDefinition(ctx, tenantID, code, version)
       if err == nil {
           return def, nil
       }
       if !errors.Is(err, sql.ErrNoRows) {
           return InstrumentDefinition{}, err
       }

       // 2. Fallback to System Tenant
       return r.queries.GetInstrumentDefinition(ctx, SystemTenantID, code, version)
   }
   ```

3. **System Tenant Write Protection**:

   ```go
   var ErrSystemTenantReadOnly = errors.New("system tenant instruments are read-only")

   func (r *PostgresRegistry) CreateDefinition(
       ctx context.Context, def InstrumentDefinition,
   ) (InstrumentDefinition, error) {
       // Enforce: System Tenant instruments are admin-only (seeded via migrations)
       if def.TenantID == SystemTenantID {
           return InstrumentDefinition{}, ErrSystemTenantReadOnly
       }

       // Compile CEL expressions before persisting (fail fast on syntax errors)
       if _, err := r.compiler.CompileValidation(def.ValidationExpression); err != nil {
           return InstrumentDefinition{}, fmt.Errorf("%w: %v", ErrCELCompileError, err)
       }
       if _, err := r.compiler.CompileFungibility(def.FungibilityExpression); err != nil {
           return InstrumentDefinition{}, fmt.Errorf("%w: %v", ErrCELCompileError, err)
       }

       return r.queries.CreateInstrumentDefinition(ctx, def)
   }
   ```

4. **CEL Compiler** (`cel.go`) using `github.com/google/cel-go`:

   ```go
   type CELCompiler struct {
       validationEnv  *cel.Env  // For validation_expression (single attrs map)
       fungibilityEnv *cel.Env  // For fungibility_expression (two attrs maps: a, b)
   }

   func NewCELCompiler() (*CELCompiler, error) {
       // Environment for ingestion validation: attributes → bool
       valEnv, err := cel.NewEnv(
           cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
       )
       if err != nil {
           return nil, err
       }

       // Environment for fungibility check: a, b → bool
       fungEnv, err := cel.NewEnv(
           cel.Variable("a", cel.MapType(cel.StringType, cel.StringType)),
           cel.Variable("b", cel.MapType(cel.StringType, cel.StringType)),
       )
       if err != nil {
           return nil, err
       }

       return &CELCompiler{validationEnv: valEnv, fungibilityEnv: fungEnv}, nil
   }

   // CompileValidation compiles ingestion validation expression.
   func (c *CELCompiler) CompileValidation(expr string) (cel.Program, error) {
       return c.compile(c.validationEnv, expr)
   }

   // CompileFungibility compiles position merge predicate.
   func (c *CELCompiler) CompileFungibility(expr string) (cel.Program, error) {
       return c.compile(c.fungibilityEnv, expr)
   }

   // AreFungible evaluates whether two positions can be merged.
   func (c *CELCompiler) AreFungible(
       prog cel.Program, attrsA, attrsB map[string]string,
   ) (bool, error) {
       out, _, err := prog.Eval(map[string]interface{}{"a": attrsA, "b": attrsB})
       if err != nil {
           return false, err
       }
       result, ok := out.Value().(bool)
       if !ok {
           return false, fmt.Errorf("CEL expression must return bool, got %T", out.Value())
       }
       return result, nil
   }
   ```

5. **PostgreSQL implementation** with sqlc-generated queries

6. **Error types**:
   - `ErrInstrumentNotFound`
   - `ErrDuplicateInstrument`
   - `ErrCELCompileError` - syntax/semantic error in validation expression
   - `ErrAttributeValidationFailed` - CEL evaluated to `false`
   - `ErrSystemTenantReadOnly` - attempt to create/modify System Tenant instrument

### Acceptance Criteria

- [ ] CRUD operations work correctly
- [ ] Tenant lookup falls back to System Tenant when not found
- [ ] `ListDefinitions` includes both tenant and System Tenant instruments
- [ ] Cannot create instruments in System Tenant via API (admin-only seed data)
- [ ] CEL expression compiled at `CreateDefinition` - syntax errors rejected immediately
- [ ] `ValidateAttributes` executes compiled CEL and returns clear error on `false`

---

## Stream G: gRPC API Handlers

**Location:** `services/reference-data/handler/`
**Dependencies:** Stream D, Stream F

### Deliverables

1. **ReferenceDataService** proto definition:

   ```protobuf
   service ReferenceDataService {
       rpc RegisterInstrument(RegisterInstrumentRequest) returns (InstrumentDefinition);
       rpc RetrieveInstrument(RetrieveInstrumentRequest) returns (InstrumentDefinition);
       rpc ListInstruments(ListInstrumentsRequest) returns (ListInstrumentsResponse);
   }
   ```

2. **Handler implementation** with:
   - Tenant extraction from context
   - Input validation
   - Error mapping to gRPC codes

3. **Adapter layer** for proto ↔ domain conversion

### Acceptance Criteria

- [ ] All endpoints functional
- [ ] Proper gRPC error codes returned
- [ ] Tenant context required and enforced

---

## Stream H: Caching Layer

**Location:** `services/reference-data/cache/`
**Dependencies:** Stream F (registry interface)

### Deliverables

1. **CachedInstrumentRegistry** wrapper with **compiled CEL programs**:

   ```go
   // CachedInstrument holds the definition and BOTH pre-compiled CEL programs
   type CachedInstrument struct {
       Definition         InstrumentDefinition
       ValidationProgram  cel.Program  // For ingestion: attributes → bool
       FungibilityProgram cel.Program  // For aggregation: (a, b) → bool
   }

   // cachedEntry wraps CachedInstrument with timestamp for TTL enforcement
   type cachedEntry struct {
       instrument CachedInstrument
       cachedAt   time.Time
   }

   type CachedInstrumentRegistry struct {
       delegate InstrumentRegistry
       compiler *CELCompiler
       cache    *lru.Cache[string, cachedEntry]  // Bounded LRU with TTL
       ttl      time.Duration
   }
   ```

   > **Why cache compiled programs**: CEL parsing/compilation is ~100μs. CEL execution is ~100ns.
   > By caching both `cel.Program` instances alongside the definition, we pay compilation once.

2. **Read-through caching with TTL enforcement**:

   ```go
   func (c *CachedInstrumentRegistry) GetDefinition(
       ctx context.Context, tenantID uuid.UUID, code string, version uint32,
   ) (CachedInstrument, error) {
       key := fmt.Sprintf("%s:%s:%d", tenantID, code, version)

       // Check cache with TTL validation
       if entry, ok := c.cache.Get(key); ok {
           if time.Since(entry.cachedAt) < c.ttl {
               return entry.instrument, nil  // Cache hit, still valid
           }
           c.cache.Remove(key)  // Expired, remove stale entry
       }

       // Cache miss or expired: fetch from delegate, compile, cache
       def, err := c.delegate.GetDefinition(ctx, tenantID, code, version)
       if err != nil {
           return CachedInstrument{}, err
       }

       inst, err := c.compileAndCache(key, def)
       if err != nil {
           return CachedInstrument{}, err
       }
       return inst, nil
   }
   ```

3. **Cache invalidation** on `CreateDefinition` (local only, no distributed)

### Acceptance Criteria

- [ ] Cache hit returns pre-compiled CEL program
- [ ] TTL-based expiration works
- [ ] Creation invalidates relevant cache entries
- [ ] `ValidateAttributes` uses cached `cel.Program` (no re-compilation)

---

## Stream I: Service Integration (Per-Service Sub-Streams)

**Dependencies:** Stream F, Stream G, Stream H
**Parallel execution:** All I.x streams can run in parallel after Phase 4 completes.

> **Performance critical**: Position Keeping may process 100k+ TPS. Every `RecordMeasurement`
> call must NOT make a synchronous gRPC call to Reference Data. Instrument definitions AND
> compiled CEL programs must be cached aggressively in-process.

### Shared Infrastructure (All Services)

Before per-service work, establish shared caching infrastructure:

```go
// LocalInstrumentCache provides sub-microsecond lookups for hot-path operations.
// Used by all services that handle quantities.
type LocalInstrumentCache struct {
    registry InstrumentRegistry              // Remote client (fallback)
    compiler *CELCompiler                    // For compiling on cache miss
    cache    *lru.Cache[string, CachedInstrument]  // Bounded: max 10,000 entries
    ttl      time.Duration                   // Refresh interval (e.g., 5 minutes)
}
```

---

### Stream I.1: Position Keeping Integration

**Location:** `services/position-keeping/`
**Developer allocation:** 2

#### Scope

Position Keeping is the **primary consumer** of multi-asset quantities. Changes span:

| Layer | Files | Changes |
|-------|-------|---------|
| Domain | `domain/money.go` | Replace with `domain/quantity.go` re-exporting `pkg/platform/quantity` |
| Domain | `domain/measurement.go` | Use `Quantity[D]` instead of `Money` |
| Domain | `domain/events.go` | Update event payloads to use `InstrumentAmount` |
| Adapter | `adapters/grpc/*.go` | Add CEL validation, `ParseQuantity` bridge |
| Adapter | `adapters/persistence/*.go` | Update SQL to store instrument_code + version + attributes |
| Service | `service/*.go` | Inject `LocalInstrumentCache`, use `AreFungible` for position merge |

#### Deliverables

1. **Domain migration**: Replace `Money` type with `Quantity[D]`

   ```go
   // BEFORE
   type Position struct {
       Amount   Money
       Currency Currency
   }

   // AFTER
   type Position struct {
       Amount     Quantity[D]  // D is Monetary or Commodity
       Attributes map[string]string
   }
   ```

2. **Fungibility integration** in position aggregation:

   ```go
   func (s *Service) canMergePositions(
       ctx context.Context, existing, incoming Position,
   ) (bool, error) {
       // Same dimension, same instrument, same version already checked
       cached, _ := s.cache.Get(ctx, tenantID, existing.InstrumentCode, existing.Version)
       return s.celCompiler.AreFungible(
           cached.FungibilityProgram,
           existing.Attributes,
           incoming.Attributes,
       )
   }
   ```

3. **Validation checkpoint** in `RecordMeasurement`:

   ```go
   func (a *Adapter) RecordMeasurement(ctx context.Context, req *pb.RecordMeasurementRequest) error {
       // 1. Cache lookup
       cached, err := a.cache.Get(ctx, tenantID, req.Amount.InstrumentCode, req.Amount.Version)
       if err != nil {
           return status.Errorf(codes.NotFound, "unknown instrument: %s", req.Amount.InstrumentCode)
       }

       // 2. CEL validation (~100ns)
       valid, err := a.cel.Validate(cached.ValidationProgram, req.Amount.Attributes)
       if !valid {
           return status.Errorf(codes.InvalidArgument, "attributes failed validation")
       }

       // 3. Type bridge
       qty, err := quantity.ParseQuantity(amount, cached.Definition.ToUnitDef())
       if err != nil {
           return status.Errorf(codes.Internal, "type bridge failed: %v", err)
       }

       // 4. Domain entry
       return a.service.RecordMeasurement(ctx, qty, req.Amount.Attributes)
   }
   ```

4. **Database migration**: Add `attributes JSONB` column to positions table

#### Acceptance Criteria

- [ ] `RecordMeasurement` accepts multi-asset instruments
- [ ] CEL validation rejects invalid attributes before domain entry
- [ ] Fungibility expression controls position merging
- [ ] Existing fiat-only tests still pass (backwards compatible)
- [ ] No gRPC calls on hot path (cache hit rate > 99%)

---

### Stream I.2: Current Account Integration

**Location:** `services/current-account/`
**Developer allocation:** 1

#### Scope

Current Account manages account balances. Changes:

| Layer | Files | Changes |
|-------|-------|---------|
| Domain | `domain/money.go` | Replace with `domain/quantity.go` |
| Domain | `domain/account.go` | Balance as `Quantity[Monetary]` (fiat accounts) |
| Domain | `domain/lien.go` | Lien amounts as `Quantity[Monetary]` |
| Adapter | `adapters/persistence/*.go` | Update balance storage |
| Service | `service/*.go` | Inject cache, validate on deposit/withdrawal |

#### Deliverables

1. **Domain migration**: Update `Account.Balance` to use `Quantity[Monetary]`

   ```go
   type Account struct {
       ID        uuid.UUID
       Balance   Quantity[Monetary]  // Was: Money
       Currency  string              // Instrument code (e.g., "GBP")
   }
   ```

2. **Deposit/Withdrawal validation**: Validate instrument exists before accepting

3. **Multi-currency foundation**: Structure supports future multi-currency accounts

#### Acceptance Criteria

- [ ] Account creation accepts instrument code (not just currency string)
- [ ] Deposits/withdrawals validated against Reference Data
- [ ] Balance queries return `InstrumentAmount` in proto responses
- [ ] Existing account tests pass

---

### Stream I.3: Financial Accounting Integration

**Location:** `services/financial-accounting/`
**Developer allocation:** 1

#### Scope

Financial Accounting maintains the double-entry ledger. Changes:

| Layer | Files | Changes |
|-------|-------|---------|
| Domain | `domain/money.go` | Replace with `domain/quantity.go` |
| Domain | `domain/ledger_posting.go` | Postings use `Quantity[D]` |
| Adapter | `adapters/persistence/*.go` | Ledger entries store instrument + attributes |
| Service | `service/*.go` | Validate instruments on posting |

#### Deliverables

1. **Domain migration**: Ledger postings support any dimension

   ```go
   type LedgerPosting struct {
       DebitAccount  uuid.UUID
       CreditAccount uuid.UUID
       Amount        Quantity[D]  // Generic: can be Monetary or Commodity
       Attributes    map[string]string
   }
   ```

2. **Dimension-aware validation**: Ensure debit/credit use same dimension

3. **Audit trail**: Attributes stored with each posting for full traceability

#### Acceptance Criteria

- [ ] Ledger accepts multi-asset postings
- [ ] Dimension mismatch in double-entry rejected at domain layer
- [ ] Existing fiat posting tests pass
- [ ] Audit queries return full attribute context

---

### Stream I.4: Secondary Services Integration

**Location:** `services/payment-order/`, `services/utilization-metering-consumer/`
**Developer allocation:** 1

#### Scope (Payment Order)

Payment Order orchestrates payment instructions:

| Layer | Files | Changes |
|-------|-------|---------|
| Domain | `domain/money.go` | Replace with `domain/quantity.go` |
| Domain | `domain/payment_order.go` | Amount as `Quantity[Monetary]` |
| Adapter | `adapters/persistence/*.go` | Store instrument code |

#### Scope (Utilization Metering Consumer)

This service already tracks non-fiat measurements - **native fit for `Quantity[Commodity]`**:

| Layer | Files | Changes |
|-------|-------|---------|
| Domain | `domain/measurement.go` | Replace `Quantity int64` + `UnitOfMeasure string` with `Quantity[Commodity]` |
| Adapter | `adapters/grpc/*.go` | Use `InstrumentAmount` for Position Keeping calls |

#### Deliverables

1. **Payment Order**: Update to use `Quantity[Monetary]` for payment amounts

2. **Utilization Metering**: Natural migration to typed quantities

   ```go
   // BEFORE
   type UtilizationMeasurement struct {
       Quantity      int64   // e.g., 1
       UnitOfMeasure string  // e.g., "transaction"
   }

   // AFTER
   type UtilizationMeasurement struct {
       Amount Quantity[Commodity]  // Instrument: "TRANSACTION", "API_CALL", etc.
   }
   ```

3. **Instrument definitions**: Create system-tenant instruments for utilization types

#### Acceptance Criteria

- [ ] Payment orders accept multi-asset amounts
- [ ] Utilization measurements use typed `Quantity[Commodity]`
- [ ] Position Keeping receives properly typed measurements
- [ ] Existing payment/metering tests pass

---

## Stream J: Integration Tests

**Location:** `services/reference-data/integration_test.go`
**Dependencies:** All streams

### Deliverables

1. **End-to-end tests** using Testcontainers:
   - Create custom instrument definition
   - Create position with instrument
   - Validate attribute rejection
   - Tenant isolation verification
   - System Tenant fallback verification

2. **Performance baseline**: Registry lookup latency under load

### Acceptance Criteria

- [ ] Full workflow tested
- [ ] Tenant isolation proven
- [ ] System Tenant inheritance works (tenant can use "USD" without defining it)
- [ ] No flaky tests (use `await` package, not `time.Sleep`)

---

## Parallel Execution Summary

| Stream | Can Start After | Developers | Service |
|--------|-----------------|------------|---------|
| A: Core Types | Immediately | 2 | `pkg/platform/quantity` |
| B: Currency | A | 1 | `pkg/platform/quantity/currency` |
| C: Rate Type | A | 1 | `pkg/platform/quantity` |
| D: Protobuf | Immediately (from ADR spec) | 1 | `proto/platform/v1` |
| E: DB Schema | Immediately (from ADR spec) | 1 | `services/reference-data` |
| F: Reference Data Service | A + E | 2 | `services/reference-data` |
| G: gRPC Handlers | D + F | 1 | `services/reference-data` |
| H: Caching | F | 1 | `services/reference-data` |
| **I.1: Position Keeping** | G + H | 2 | `services/position-keeping` |
| **I.2: Current Account** | G + H | 1 | `services/current-account` |
| **I.3: Financial Accounting** | G + H | 1 | `services/financial-accounting` |
| **I.4: Payment + Metering** | G + H | 1 | `services/payment-order`, `services/utilization-metering-consumer` |
| J: Integration Tests | All I.x | 1 | Cross-service |

**Critical path:** A → F → G → I.1 → J

**Maximum parallelism:**

- **Phase 1:** 4 streams (A, D, E, and B/C if working from ADR spec)
- **Phase 5:** 4 streams (I.1, I.2, I.3, I.4 all run in parallel)

**Total developer allocation:** 14 developer-streams across 10 developers

---

## Decisions Made

| Question | Decision |
|----------|----------|
| Service naming | `reference-data` (BIAN: FinancialInstrumentReferenceDataManagement) |
| System Tenant ID | `00000000-0000-0000-0000-000000000000` |
| Lookup inheritance | Tenant → System Tenant fallback |
| Valuation scope | Rate struct only; ValuationProvider deferred to future PRD |
| Attribute validation | CEL `validation_expression` - 100x faster than JSON Schema |
| Fungibility policy | CEL `fungibility_expression` - determines position merge eligibility |
| Arithmetic | Native Go `decimal.Decimal` - CEL never in hot loop |
| Local cache type | Bounded LRU (`hashicorp/golang-lru`) to prevent memory leaks |
| CEL caching | Cache both compiled programs (validation + fungibility) |
| Integration strategy | Per-service streams (I.1-I.4) for parallel execution |
| Shared package | New `pkg/platform/quantity`; deprecate `shared/domain/money` after migration |

---

## Open Questions

1. **Initial commodity catalog**: Which non-currency instruments should be seeded (if any)?
2. **Cache TTL**: What's the acceptable staleness window for instrument definitions? (Proposed: 5 min)

---

## Success Metrics

- [ ] All streams completed and merged
- [ ] Custom instrument creation works end-to-end
- [ ] System Tenant inheritance works (any tenant can use "USD")
- [ ] No compile-time dimensional safety regressions
- [ ] Reference Data lookup p99 < 10ms (with service-level caching)
- [ ] Position Keeping cache hit rate > 99% (with local caching)
