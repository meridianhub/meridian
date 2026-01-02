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

    subgraph Integration["Phase 5: Integration"]
        I["Stream I<br/>Adapter<br/>Integration"]
        J["Stream J<br/>Integration<br/>Tests"]
    end

    A --> B
    A --> C
    A --> F
    D --> G
    E --> F
    F --> G
    F --> H
    G --> I
    H --> I
    I --> J
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
       Code      string
       Version   uint32
       Precision int
       Schema    AttributeSchema
   }
   ```

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

### Acceptance Criteria

- [ ] `Quantity[Monetary].Add(Quantity[Commodity])` fails at compile time
- [ ] `USD.Add(EUR)` returns `ErrUnitMismatch` at runtime
- [ ] `USD(v1).Add(USD(v2))` returns `ErrVersionMismatch` at runtime
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

**Location:** `proto/platform/quantity/v1/`
**Dependencies:** Stream A (type design, can work from ADR spec)

### Deliverables

1. **AssetAmount message** (`quantity.proto`)

   ```protobuf
   message AssetAmount {
       string amount = 1;
       string asset_code = 2;
       uint32 version = 3;
       map<string, string> attributes = 4;
   }
   ```

2. **Rate message**

   ```protobuf
   message Rate {
       string from_code = 1;
       string to_code = 2;
       string factor = 3;
       google.protobuf.Timestamp valid_from = 4;
       google.protobuf.Timestamp valid_to = 5;
   }
   ```

3. **Buf breaking change detection** configured

### Acceptance Criteria

- [ ] Proto compiles without errors
- [ ] Generated Go code matches domain types
- [ ] Buf lint passes

---

## Stream E: Database Schema

**Location:** `services/reference-data/migrations/`
**Dependencies:** Stream A (UnitDef field design)

> **BIAN alignment**: This service maps to the BIAN `FinancialInstrumentReferenceDataManagement`
> service domain, which maintains a directory of financial instrument reference data including
> currencies, equities, debt instruments, and commodities.

### Deliverables

1. **Asset definitions table**

   ```sql
   CREATE TABLE instrument_definitions (
       id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
       tenant_id UUID NOT NULL,
       code VARCHAR(32) NOT NULL,
       version INTEGER NOT NULL DEFAULT 1,
       dimension VARCHAR(32) NOT NULL,
       precision INTEGER NOT NULL,
       attribute_schema JSONB NOT NULL DEFAULT '{}',
       display_name VARCHAR(128),
       description TEXT,
       created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

       UNIQUE(tenant_id, code, version),
       CHECK (precision >= 0 AND precision <= 18),
       CHECK (dimension IN ('Monetary', 'Commodity'))
   );

   CREATE INDEX idx_instrument_definitions_lookup
       ON instrument_definitions(tenant_id, code, version);
   ```

2. **System tenant seed data**:

   ```sql
   -- System tenant ID: 00000000-0000-0000-0000-000000000000
   -- Platform-wide instruments accessible to ALL tenants
   INSERT INTO instrument_definitions (tenant_id, code, version, dimension, precision, display_name)
   VALUES
       ('00000000-0000-0000-0000-000000000000', 'USD', 1, 'Monetary', 2, 'US Dollar'),
       ('00000000-0000-0000-0000-000000000000', 'EUR', 1, 'Monetary', 2, 'Euro'),
       ('00000000-0000-0000-0000-000000000000', 'GBP', 1, 'Monetary', 2, 'British Pound');
   ```

### Acceptance Criteria

- [ ] Migration applies cleanly
- [ ] Unique constraint prevents duplicate code+version per tenant
- [ ] Index supports efficient lookups
- [ ] System tenant seed data inserted

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
       CreateDefinition(ctx context.Context, def InstrumentDefinition) (InstrumentDefinition, error)

       // ListDefinitions returns tenant instruments + all SystemTenant instruments
       ListDefinitions(ctx context.Context, tenantID uuid.UUID) ([]InstrumentDefinition, error)

       // ValidateAttributes checks attributes against instrument's JSON Schema
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

3. **PostgreSQL implementation** with sqlc-generated queries

4. **JSON Schema validation** using `github.com/santhosh-tekuri/jsonschema`

5. **Error types**:
   - `ErrInstrumentNotFound`
   - `ErrDuplicateInstrument`
   - `ErrInvalidSchema`
   - `ErrAttributeValidationFailed`

### Acceptance Criteria

- [ ] CRUD operations work correctly
- [ ] Tenant lookup falls back to System Tenant when not found
- [ ] `ListDefinitions` includes both tenant and System Tenant instruments
- [ ] Cannot create instruments in System Tenant via API (admin-only seed data)
- [ ] Invalid attributes rejected with clear error messages

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

1. **CachedInstrumentRegistry** wrapper:

   ```go
   type CachedInstrumentRegistry struct {
       delegate InstrumentRegistry
       cache    *cache.Cache
       ttl      time.Duration
   }
   ```

2. **Read-through caching** on `GetDefinition` and `GetLatestDefinition`

3. **Cache invalidation** on `CreateDefinition` (local only, no distributed)

### Acceptance Criteria

- [ ] Cache hit reduces database queries
- [ ] TTL-based expiration works
- [ ] Creation invalidates relevant cache entries

---

## Stream I: Adapter Integration

**Location:** Existing service adapters (Position Keeping, Current Account, etc.)
**Dependencies:** Stream F, Stream G

> **Performance critical**: Position Keeping may process 100k+ TPS. Every `RecordMeasurement`
> call must NOT make a synchronous gRPC call to Reference Data. Instrument definitions must
> be cached aggressively in-process.

### Deliverables

1. **Reference Data client** injected into transaction adapters

2. **Aggressive in-memory caching** within Position Keeping:

   ```go
   // LocalInstrumentCache provides sub-microsecond lookups for hot-path operations.
   // Refreshed asynchronously; stale reads acceptable for short windows.
   type LocalInstrumentCache struct {
       registry InstrumentRegistry      // Remote client (fallback)
       cache    sync.Map                // instrument_code:version → InstrumentDefinition
       ttl      time.Duration           // Refresh interval (e.g., 5 minutes)
   }

   func (c *LocalInstrumentCache) Get(
       ctx context.Context, tenantID uuid.UUID, code string, version uint32,
   ) (InstrumentDefinition, error) {
       // 1. Check local cache (sync.Map - lock-free reads)
       key := fmt.Sprintf("%s:%s:%d", tenantID, code, version)
       if cached, ok := c.cache.Load(key); ok {
           return cached.(InstrumentDefinition), nil
       }

       // 2. Cache miss: fetch from Reference Data service
       def, err := c.registry.GetDefinition(ctx, tenantID, code, version)
       if err != nil {
           return InstrumentDefinition{}, err
       }

       // 3. Populate cache
       c.cache.Store(key, def)
       return def, nil
   }
   ```

3. **Background refresh goroutine**: Periodically refresh cached definitions to pick up new
   instruments without requiring restarts

4. **Schema-on-Write validation** at ingestion:

   ```go
   func (a *TransactionAdapter) validateInstrument(ctx context.Context, req *pb.Request) error {
       def, err := a.localCache.Get(ctx, tenantID, req.InstrumentCode, req.Version)
       if err != nil {
           return status.Errorf(codes.NotFound, "unknown instrument")
       }
       return a.registry.ValidateAttributes(ctx, def, req.Attributes)
   }
   ```

5. **Position creation** using validated `PositionKey`

### Acceptance Criteria

- [ ] Invalid instruments rejected at adapter layer
- [ ] Invalid attributes rejected with clear errors
- [ ] Cache hit rate > 99% after warm-up
- [ ] No gRPC calls on hot path (cache hit)
- [ ] New instruments visible within TTL window (e.g., 5 minutes)

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

| Stream | Can Start After | Developers |
|--------|-----------------|------------|
| A: Core Types | Immediately | 2 |
| B: Currency | A | 1 |
| C: Rate Type | A | 1 |
| D: Protobuf | Immediately (from ADR spec) | 1 |
| E: DB Schema | Immediately (from ADR spec) | 1 |
| F: Reference Data Service | A + E | 2 |
| G: gRPC Handlers | D + F | 1 |
| H: Caching | F | 1 |
| I: Adapter Integration | F + G | 2 |
| J: Integration Tests | All | 1 |

**Critical path:** A → F → G → I → J

**Maximum parallelism at start:** 4 streams (A, D, E, and potentially B/C if working from ADR spec)

---

## Decisions Made

| Question | Decision |
|----------|----------|
| Service naming | `reference-data` (BIAN: FinancialInstrumentReferenceDataManagement) |
| System Tenant ID | `00000000-0000-0000-0000-000000000000` |
| Lookup inheritance | Tenant → System Tenant fallback |
| Valuation scope | Rate struct only; ValuationProvider deferred to future PRD |

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
