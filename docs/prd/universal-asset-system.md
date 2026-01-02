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
4. **Valuation architecture**: Pluggable providers for asset-to-currency conversion

### Non-Goals (Simplified Scope)

- ~~Migration from legacy `Money` types~~ - clean implementation, no backwards compatibility
- ~~Migration-as-Trade pattern~~ - no existing positions to migrate
- ~~Version deprecation lifecycle~~ - not needed pre-production
- ~~Distributed cache invalidation~~ - simple caching sufficient for now

---

## Work Streams

Designed for parallel execution across multiple developers. Dependencies shown in diagram below.

```text
                                    ┌─────────────────────┐
                                    │   Stream A          │
                                    │   Core Types        │
                                    │   (quantity pkg)    │
                                    └──────────┬──────────┘
                                               │
                    ┌──────────────────────────┼──────────────────────────┐
                    │                          │                          │
                    ▼                          ▼                          ▼
          ┌─────────────────┐       ┌─────────────────┐       ┌─────────────────┐
          │   Stream B      │       │   Stream C      │       │   Stream D      │
          │   Currency      │       │   Rate &        │       │   Protobuf      │
          │   Definitions   │       │   Valuation     │       │   Definitions   │
          └────────┬────────┘       └────────┬────────┘       └────────┬────────┘
                   │                         │                         │
                   │                         │                         │
                   └─────────────────────────┼─────────────────────────┘
                                             │
          ┌─────────────────┐                │
          │   Stream E      │                │
          │   DB Schema     │◄───────────────┘
          │   (parallel)    │
          └────────┬────────┘
                   │
                   ▼
          ┌─────────────────┐
          │   Stream F      │
          │   Registry      │
          │   Service       │
          └────────┬────────┘
                   │
          ┌────────┴────────┐
          │                 │
          ▼                 ▼
┌─────────────────┐ ┌─────────────────┐
│   Stream G      │ │   Stream H      │
│   gRPC API      │ │   Caching       │
│   Handlers      │ │   Layer         │
└────────┬────────┘ └────────┬────────┘
         │                   │
         └─────────┬─────────┘
                   │
                   ▼
          ┌─────────────────┐
          │   Stream I      │
          │   Adapter       │
          │   Integration   │
          └────────┬────────┘
                   │
                   ▼
          ┌─────────────────┐
          │   Stream J      │
          │   Integration   │
          │   Tests         │
          └─────────────────┘
```

---

## Stream A: Core Types Package

**Location:** `pkg/platform/quantity/`
**Dependencies:** None (foundational)
**Estimated complexity:** 5 points

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
**Estimated complexity:** 2 points

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

## Stream C: Rate & Valuation

**Location:** `pkg/platform/quantity/`
**Dependencies:** Stream A (Quantity, UnitDef types)
**Estimated complexity:** 3 points

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
   ```

2. **ValuationProvider interface** (`valuation.go`)

   ```go
   type ValuationProvider interface {
       Valuate(ctx context.Context, req ValuationRequest) (ValuationResponse, error)
       Supports(dimension string, assetCode string) bool
   }

   type ValuationRequest struct {
       Position    Position
       TargetUnit  UnitDef
       AtTime      time.Time
   }

   type ValuationResponse struct {
       Value       Money
       Rate        Rate
       Provider    string
   }
   ```

3. **IdentityProvider**: Trivial implementation for same-currency valuation

### Acceptance Criteria

- [ ] Rate correctly converts between units
- [ ] IdentityProvider returns 1:1 for same currency
- [ ] ValuationProvider interface supports pluggable implementations

---

## Stream D: Protobuf Definitions

**Location:** `proto/platform/quantity/v1/`
**Dependencies:** Stream A (type design, can work from ADR spec)
**Estimated complexity:** 2 points

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

**Location:** `services/asset-registry/migrations/`
**Dependencies:** Stream A (UnitDef field design)
**Estimated complexity:** 2 points

### Deliverables

1. **Asset definitions table**

   ```sql
   CREATE TABLE asset_definitions (
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

   CREATE INDEX idx_asset_definitions_lookup
       ON asset_definitions(tenant_id, code, version);
   ```

2. **Seed data** for platform currencies (system tenant):
   - USD, EUR, GBP with precision 2

### Acceptance Criteria

- [ ] Migration applies cleanly
- [ ] Unique constraint prevents duplicate code+version per tenant
- [ ] Index supports efficient lookups

---

## Stream F: Registry Service

**Location:** `services/asset-registry/`
**Dependencies:** Stream A, Stream E
**Estimated complexity:** 5 points

### Deliverables

1. **AssetRegistry interface** (`registry.go`)

   ```go
   type AssetRegistry interface {
       GetDefinition(ctx context.Context, tenantID uuid.UUID, code string, version uint32) (AssetDefinition, error)
       GetLatestDefinition(ctx context.Context, tenantID uuid.UUID, code string) (AssetDefinition, error)
       CreateDefinition(ctx context.Context, def AssetDefinition) (AssetDefinition, error)
       ListDefinitions(ctx context.Context, tenantID uuid.UUID) ([]AssetDefinition, error)
       ValidateAttributes(ctx context.Context, def AssetDefinition, attrs map[string]string) error
   }
   ```

2. **PostgreSQL implementation** with sqlc-generated queries

3. **JSON Schema validation** using `github.com/santhosh-tekuri/jsonschema`

4. **Error types**:
   - `ErrAssetNotFound`
   - `ErrDuplicateAsset`
   - `ErrInvalidSchema`
   - `ErrAttributeValidationFailed`

### Acceptance Criteria

- [ ] CRUD operations work correctly
- [ ] Tenant isolation enforced (queries always filter by tenant_id)
- [ ] Invalid attributes rejected with clear error messages
- [ ] System tenant assets (currencies) accessible to all tenants

---

## Stream G: gRPC API Handlers

**Location:** `services/asset-registry/handler/`
**Dependencies:** Stream D, Stream F
**Estimated complexity:** 3 points

### Deliverables

1. **AssetRegistryService** proto definition:

   ```protobuf
   service AssetRegistryService {
       rpc CreateAsset(CreateAssetRequest) returns (AssetDefinition);
       rpc GetAsset(GetAssetRequest) returns (AssetDefinition);
       rpc ListAssets(ListAssetsRequest) returns (ListAssetsResponse);
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

**Location:** `services/asset-registry/cache/`
**Dependencies:** Stream F (registry interface)
**Estimated complexity:** 2 points

### Deliverables

1. **CachedAssetRegistry** wrapper:

   ```go
   type CachedAssetRegistry struct {
       delegate AssetRegistry
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

**Location:** Existing service adapters
**Dependencies:** Stream F, Stream G
**Estimated complexity:** 3 points

### Deliverables

1. **Asset Registry client** injected into transaction adapters

2. **Schema-on-Write validation** at ingestion:

   ```go
   func (a *TransactionAdapter) validateAsset(ctx context.Context, req *pb.Request) error {
       def, err := a.registry.GetDefinition(ctx, tenantID, req.AssetCode, req.Version)
       if err != nil {
           return status.Errorf(codes.NotFound, "unknown asset")
       }
       return a.registry.ValidateAttributes(ctx, def, req.Attributes)
   }
   ```

3. **Position creation** using validated `PositionKey`

### Acceptance Criteria

- [ ] Invalid assets rejected at adapter layer
- [ ] Invalid attributes rejected with clear errors
- [ ] Valid requests flow through to domain layer

---

## Stream J: Integration Tests

**Location:** `services/asset-registry/integration_test.go`
**Dependencies:** All streams
**Estimated complexity:** 3 points

### Deliverables

1. **End-to-end tests** using Testcontainers:
   - Create custom asset definition
   - Create position with asset
   - Validate attribute rejection
   - Tenant isolation verification

2. **Performance baseline**: Registry lookup latency under load

### Acceptance Criteria

- [ ] Full workflow tested
- [ ] Tenant isolation proven
- [ ] No flaky tests (use `await` package, not `time.Sleep`)

---

## Parallel Execution Summary

| Stream | Can Start After | Developers |
|--------|-----------------|------------|
| A: Core Types | Immediately | 2 |
| B: Currency | A | 1 |
| C: Rate/Valuation | A | 1 |
| D: Protobuf | Immediately (from ADR spec) | 1 |
| E: DB Schema | Immediately (from ADR spec) | 1 |
| F: Registry Service | A + E | 2 |
| G: gRPC Handlers | D + F | 1 |
| H: Caching | F | 1 |
| I: Adapter Integration | F + G | 1 |
| J: Integration Tests | All | 1 |

**Critical path:** A → F → G → I → J

**Maximum parallelism at start:** 4 streams (A, D, E, and potentially B/C if working from ADR spec)

---

## Open Questions

1. **Service ownership**: Should Asset Registry be a new service or part of an existing BIAN domain?
2. **System tenant ID**: Confirm UUID for platform-wide assets (suggest `00000000-0000-0000-0000-000000000000`)
3. **Initial asset catalog**: Which commodity assets should be seeded beyond fiat currencies?

---

## Success Metrics

- [ ] All streams completed and merged
- [ ] Custom asset creation works end-to-end
- [ ] No compile-time dimensional safety regressions
- [ ] Registry lookup p99 < 10ms (with caching)
