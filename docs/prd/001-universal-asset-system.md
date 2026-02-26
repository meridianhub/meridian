---
name: prd-universal-asset-system
description: Extend Meridian's ledger from fiat-only to multi-asset support with dimensional safety
triggers:
  - Implementing multi-asset or universal asset support
  - Working on InstrumentType, Quantity, or asset definitions
  - Adding new asset types (commodities, energy, vouchers)
  - Designing tenant-specific asset catalogues
  - Implementing dimensional safety or asset quantity types
  - Working on reference data service
instructions: |
  This PRD defines the Universal Asset System for multi-asset ledger support.
  Key patterns: Use Go generics for dimensional safety (Monetary vs Commodity).
  Assets are configured via database, not code. Each tenant has isolated catalogue.
  Refer to ADR-0013 (Quantity Types) and ADR-0014 (Reference Data) for implementation.
  Immutable proto contracts are defined in Zero-State Contract section - never modify.
---

# PRD: Universal Asset System

**Status:** Implemented
**Task Master Tag:** `universal-asset-system` (36/36 tasks done)
**ADRs:**

- [0013 - Universal Quantity Type System](../adr/0013-generic-asset-quantity-types.md)
- [0014 - Financial Instrument Reference Data](../adr/0014-financial-instrument-reference-data.md)

## Overview

Extend Meridian's ledger from fiat-only to multi-asset support. Enable tenants to define
custom assets (energy, commodities, vouchers) without code deployment while maintaining
dimensional safety.

### Goals

1. **Dimensional safety**: Prevent physics errors (money + rice) via Go generics
   > *Clarification*: In a distributed system with dynamic schemas, you cannot have true "compile-time"
   > safety for tenant-defined assets (the compiler doesn't know "Rice" exists). What we have is
   > **dimensional safety**: the platform knows at compile time that `Monetary` maths is distinct from
   > `Commodity` maths, preventing accidental treatment of commodity balances as cash during settlement.
2. **Runtime flexibility**: New assets via database configuration, not code
3. **Tenant isolation**: Each tenant has their own asset catalogue
4. **Valuation foundation**: Rate type for asset-to-currency conversion (providers in future PRD)

### Non-Goals (Simplified Scope)

- ~~Migration from legacy `Money` types~~ - clean implementation, no backwards compatibility
- ~~Migration-as-Trade pattern~~ - no existing positions to migrate
- ~~Version deprecation lifecycle~~ - not needed pre-production
- ~~Distributed cache invalidation~~ - **INCLUDED** (event-driven via `instrument.updated`)
  > Each service subscribes to instrument update events for cross-node cache invalidation.
  > This enables near-real-time consistency (<100ms) with TTL as the safety net.

---

## Table of Contents

- [Zero-State Contract (IMMUTABLE)](#zero-state-contract-immutable)
- [CEL Validation Pattern](#cel-validation-pattern)
- [Data Contract: Proto + CEL + Go](#data-contract-proto--cel--go)
- [Asset Catalogue (Open Source Library)](#asset-catalogue-open-source-library)
- [Fungibility Resolution](#fungibility-resolution)
- [Service Impact Matrix](#service-impact-matrix)
- [Work Streams](#work-streams)
  - [Stream A: Core Types Package](#stream-a-core-types-package)
  - [Stream B: Currency Definitions](#stream-b-currency-definitions)
  - [Stream C: Rate Type](#stream-c-rate-type)
  - [Stream D: Protobuf Definitions](#stream-d-protobuf-definitions)
  - [Stream E: Database Schema](#stream-e-database-schema)
  - [Stream F: Reference Data Service](#stream-f-reference-data-service)
  - [Stream G: gRPC API Handlers](#stream-g-grpc-api-handlers)
  - [Stream H: Caching Layer](#stream-h-caching-layer)
  - [Stream I: Service Integration](#stream-i-service-integration-per-service-sub-streams)
  - [Stream J: Integration Tests](#stream-j-integration-tests)
- [Parallel Execution Summary](#parallel-execution-summary)
- [Integration Coordination Strategy](#integration-coordination-strategy)
- [Decisions Made](#decisions-made)
- [Open Questions](#open-questions)
- [Success Metrics](#success-metrics)

---

## Zero-State Contract (IMMUTABLE)

> **⚠️ AI AGENT INSTRUCTION**: This section defines the immutable contracts for this sprint.
> You **MUST NOT** modify these proto definitions or Go interfaces. If you need a change,
> you **MUST abort** and request a contract update from the human operator. These contracts
> are the coordination points between parallel work streams.

This section locks down the shared contracts **before** any parallel stream begins work.
Without this, agents working on Stream A (Core Types) and Stream F (Ref Data) will drift
in their understanding of `Instrument` by even one field, causing Stream I (Integration)
to fail catastrophically.

### Immutable Proto Definitions

These proto files are created in **Step 0** (before any stream executes) and committed
to the repository. Agents read but **never modify** these files.

#### `proto/quantity/v1/quantity.proto`

```protobuf
syntax = "proto3";
package quantity.v1;

import "google/protobuf/timestamp.proto";

option go_package = "meridian/gen/quantity/v1;quantityv1";

// AttributeEntry is a key-value pair for asset context.
// Using repeated structs instead of map<string,string> enables:
// 1. Pooling: Slices can be reset and reused via sync.Pool (maps cannot)
// 2. Determinism: Iteration order is stable (maps are random)
// 3. GC: At 100k TPS, pooled slices avoid 100k map allocations/sec
message AttributeEntry {
    string key = 1;    // Must be snake_case (^[a-z][a-z0-9_]*$)
    string value = 2;
}

// InstrumentAmount is the "Sealed Envelope" - the universal payload for all asset quantities.
// Go code handles the envelope (amount, code, version, temporal bounds, source).
// CEL handles the letter (attributes validation, bucket key generation).
message InstrumentAmount {
    // The quantity magnitude (decimal string for precision).
    // Native Go maths handles this. CEL accesses via 'amount' variable.
    string amount = 1;

    // The Identity of the asset.
    string instrument_code = 2;     // "USD", "KWH", "GPU-H100"
    uint32 version = 3;             // Schema version for evolution

    // The Context/Fungibility payload (tenant-defined attributes).
    // CEL is the SOLE accessor for business rules on this payload.
    // Keys MUST be snake_case (^[a-z][a-z0-9_]*$) for CEL dot-access.
    //
    // CRITICAL: Uses repeated structs, NOT map<string,string>.
    // - Slices are poolable via sync.Pool; maps force allocation per unmarshal
    // - At 100k TPS, this avoids 100k map allocations/second
    // - Go code converts to map only at CEL boundary (transient allocation)
    repeated AttributeEntry attributes = 4;

    // Temporal bounds (first-class citizens per ADR-0017).
    // Exposed to CEL as 'valid_from' and 'valid_to' variables.
    google.protobuf.Timestamp valid_from = 5;
    google.protobuf.Timestamp valid_to = 6;

    // Quality Ladder support (ADR-0017).
    // Lookup key for Source Authority Registry (e.g., "SMETS2_METER", "CUSTOMER_READ").
    // Exposed to CEL as 'source' variable for source-specific validation rules.
    string source = 7;
}
```

#### `proto/reference_data/v1/instrument.proto`

```protobuf
syntax = "proto3";
package reference_data.v1;

option go_package = "meridian/gen/reference_data/v1;referencedatav1";

// InstrumentDefinition is the schema + rules for an asset type.
// Stored in Reference Data service, cached in all consuming services.
message InstrumentDefinition {
    string id = 1;
    string tenant_id = 2;
    string code = 3;                        // "USD", "RICE", "KWH"
    uint32 version = 4;
    string dimension = 5;                   // "Monetary" or "Commodity"
    int32 precision = 6;                    // Decimal places (2 for USD, 0 for whole units)

    // CEL expression for attribute validation.
    // Input: {attributes, amount, valid_from, valid_to, source, instrument}
    // Output: bool (true = valid)
    string validation_expression = 7;

    // CEL expression for bucket key generation.
    // Input: {attributes}
    // Output: string (SHA256 hash via bucket_key() function)
    string fungibility_key_expression = 8;

    // Attribute schema with input hints for client guidance.
    // Maps attribute name → format hint for the expected string format.
    map<string, AttributeHint> attribute_hints = 9;

    string display_name = 10;
    string description = 11;
    string status = 12;                     // "DRAFT", "ACTIVE", "DEPRECATED"
}

// AttributeHint provides guidance on expected string format for an attribute.
// This bridges the gap between stringly-typed proto and CEL type coercion.
message AttributeHint {
    string type = 1;              // "string", "int", "decimal", "timestamp", "bool"
    string format = 2;            // Regex or format hint: "^\d+$", "ISO8601", "^[A-Z]{2}$"
    string description = 3;       // Human-readable description
    bool required = 4;            // Must be present
    string example = 5;           // Example value for documentation
}
```

### Immutable Go Interfaces

These interfaces are defined in **Step 0** and placed in `shared/platform/quantity/interfaces.go`.
All stream implementations **MUST** implement these interfaces exactly.

#### `shared/platform/quantity/interfaces.go`

```go
package quantity

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/shopspring/decimal"
)

// ===============================================================
// IMMUTABLE INTERFACES - DO NOT MODIFY DURING SPRINT
// If you need changes, ABORT and request contract update.
// ===============================================================

// Dimension represents the physics of an asset (compile-time safety).
type Dimension interface {
    Name() string  // "Monetary" or "Commodity"
}

// Quantity represents a dimensionally-safe amount of an asset.
// D is a phantom type for compile-time dimension checking.
type Quantity[D Dimension] struct {
    amount         decimal.Decimal
    instrumentCode string
    version        uint32
}

// QuantityValue is the closed interface for mixed-dimension handling.
// Used when dimension is known only at runtime (database reads).
type QuantityValue interface {
    Dimension() string
    Amount() decimal.Decimal
    InstrumentCode() string
    Version() uint32
}

// AttributeBag is a poolable container for attributes.
// MANDATORY for hot paths to avoid GC pressure at 100k TPS.
// Implementations MUST use sync.Pool for allocation.
type AttributeBag interface {
    Get(key string) (string, bool)
    Set(key, value string)
    Keys() []string
    Len() int
    Reset()  // Clears all entries for pool reuse
    ToMap() map[string]string  // For CEL evaluation (allocates)
}

// Pool-backed implementation (REQUIRED in Stream A, not deferred)
var attributeBagPool = sync.Pool{
    New: func() any { return &sliceAttributeBag{entries: make([]kv, 0, 16)} },
}

type kv struct{ key, value string }

type sliceAttributeBag struct {
    entries []kv
}

func AcquireAttributeBag() AttributeBag {
    return attributeBagPool.Get().(*sliceAttributeBag)
}

func ReleaseAttributeBag(bag AttributeBag) {
    if b, ok := bag.(*sliceAttributeBag); ok {
        b.Reset()
        attributeBagPool.Put(b)
    }
}

func (b *sliceAttributeBag) Get(key string) (string, bool) {
    for _, e := range b.entries {
        if e.key == key { return e.value, true }
    }
    return "", false
}

func (b *sliceAttributeBag) Set(key, value string) {
    for i := range b.entries {
        if b.entries[i].key == key { b.entries[i].value = value; return }
    }
    b.entries = append(b.entries, kv{key, value})
}

func (b *sliceAttributeBag) Keys() []string {
    keys := make([]string, len(b.entries))
    for i, e := range b.entries { keys[i] = e.key }
    return keys
}

func (b *sliceAttributeBag) Len() int { return len(b.entries) }

func (b *sliceAttributeBag) Reset() { b.entries = b.entries[:0] }

func (b *sliceAttributeBag) ToMap() map[string]string {
    m := make(map[string]string, len(b.entries))
    for _, e := range b.entries { m[e.key] = e.value }
    return m
}

// FromProto converts proto AttributeEntry slice to pooled AttributeBag.
// Caller MUST call ReleaseAttributeBag when done.
func FromProto(entries []*quantityv1.AttributeEntry) AttributeBag {
    bag := AcquireAttributeBag()
    for _, e := range entries {
        bag.Set(e.Key, e.Value)
    }
    return bag
}

// InstrumentRegistry is the contract between Reference Data and consumers.
type InstrumentRegistry interface {
    // GetDefinition retrieves an instrument, falling back to SystemTenant if not found.
    GetDefinition(ctx context.Context, tenantID uuid.UUID, code string, version uint32) (InstrumentDefinition, error)

    // GetLatestDefinition retrieves the latest ACTIVE version.
    GetLatestDefinition(ctx context.Context, tenantID uuid.UUID, code string) (InstrumentDefinition, error)

    // CreateDefinition creates a new DRAFT instrument.
    // Returns ErrSystemTenantReadOnly if tenantID is SystemTenantID.
    CreateDefinition(ctx context.Context, def InstrumentDefinition) (InstrumentDefinition, error)

    // ActivateInstrument transitions DRAFT → ACTIVE.
    ActivateInstrument(ctx context.Context, tenantID uuid.UUID, code string, version uint32) error

    // DeprecateInstrument transitions ACTIVE → DEPRECATED.
    DeprecateInstrument(ctx context.Context, tenantID uuid.UUID, code string, version uint32) error

    // ListDefinitions returns tenant instruments + SystemTenant instruments.
    ListDefinitions(ctx context.Context, tenantID uuid.UUID) ([]InstrumentDefinition, error)
}

// CachedInstrumentRegistry wraps InstrumentRegistry with caching.
// CRITICAL: Must implement singleflight for cold cache misses.
type CachedInstrumentRegistry interface {
    InstrumentRegistry

    // GetCached returns a cached instrument with compiled CEL programs.
    // On cache miss: uses singleflight to fetch once, cache, then return.
    // NEVER returns ErrInstrumentNotCached - always attempts recovery.
    GetCached(ctx context.Context, tenantID uuid.UUID, code string, version uint32) (CachedInstrument, error)

    // Invalidate removes an entry from cache (called on local writes).
    Invalidate(tenantID uuid.UUID, code string, version uint32)

    // InvalidateAll clears the entire cache (emergency recovery).
    InvalidateAll()
}

// CachedInstrument contains pre-compiled CEL programs for hot-path performance.
type CachedInstrument struct {
    Definition        InstrumentDefinition
    ValidationProgram interface{}  // *cel.Program - opaque to avoid import cycle
    BucketKeyProgram  interface{}  // *cel.Program
}

// InstrumentDefinition is the domain model (Go representation of proto).
type InstrumentDefinition struct {
    ID                       uuid.UUID
    TenantID                 uuid.UUID
    Code                     string
    Version                  uint32
    Dimension                string
    Precision                int32
    ValidationExpression     string
    FungibilityKeyExpression string
    AttributeHints           map[string]AttributeHint
    DisplayName              string
    Description              string
    Status                   string
}

// AttributeHint guides clients on expected attribute formats.
type AttributeHint struct {
    Type        string  // "string", "int", "decimal", "timestamp", "bool"
    Format      string  // Regex or format: "^\d+$", "ISO8601"
    Description string
    Required    bool
    Example     string
}

// CELEvaluator is the contract for CEL expression evaluation.
type CELEvaluator interface {
    // ValidateAttributes runs the validation expression.
    // Input: attributes, amount, valid_from, valid_to, source, instrument context.
    // Returns: (valid bool, error)
    ValidateAttributes(
        ctx context.Context,
        program interface{},  // *cel.Program
        attrs AttributeBag,
        amount string,
        validFrom, validTo time.Time,
        source string,
        instrument InstrumentContext,
    ) (bool, error)

    // GenerateBucketKey runs the fungibility expression.
    // Input: attributes map.
    // Returns: (bucketKey string, error)
    GenerateBucketKey(program interface{}, attrs AttributeBag) (string, error)
}

// InstrumentContext provides instrument metadata to CEL expressions.
// Enables rules like: "instrument.precision == 2" or "instrument.dimension == 'Monetary'"
type InstrumentContext struct {
    Code      string
    Version   uint32
    Dimension string
    Precision int32
}

// Well-known error types for contract enforcement.
var (
    ErrInstrumentNotFound    = errors.New("instrument not found")
    ErrSystemTenantReadOnly  = errors.New("system tenant instruments are read-only")
    ErrInstrumentLocked      = errors.New("instrument is ACTIVE or DEPRECATED; cannot modify")
    ErrCELCompileError       = errors.New("CEL expression compilation failed")
    ErrCELEvalError          = errors.New("CEL expression evaluation failed")
    ErrDimensionMismatch     = errors.New("dimension mismatch in quantity operation")
    ErrVersionMismatch       = errors.New("instrument version mismatch")
)
```

### Contract Rules for AI Agents

| Rule | Enforcement |
|------|-------------|
| **No proto modifications** | Agents abort if proto changes are needed |
| **No interface modifications** | Agents abort if interface changes are needed |
| **Implement exactly as specified** | Type signatures must match byte-for-byte |
| **Request contract update if blocked** | Human operator reviews and approves changes |
| **All streams depend on Step 0** | No stream starts until contracts are committed |

### Contract Verification Checklist (Stream J)

Integration tests **MUST** verify these contracts are honored:

```go
// contract_test.go - Run FIRST before any other integration tests
func TestContractsNotModified(t *testing.T) {
    // Verify proto files match expected SHA256 checksums
    // Verify interface file matches expected SHA256 checksum
    // If mismatch: FAIL LOUDLY with "CONTRACT VIOLATION" message
}
```

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

## Data Contract: Proto + CEL + Go

### The Sealed Envelope Pattern

This is a **schema-less schema** pattern. Protobuf defines the physical container; CEL defines the logical shape.

```text
┌─────────────────────────────────────────────────────────────────────────┐
│  InstrumentAmount (The Sealed Envelope)                                 │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────────────────────┐   ┌─────────────────────────────┐  │
│  │  ENVELOPE (Go handles)          │   │  LETTER (CEL handles)       │  │
│  │  ─────────────────────          │   │  ──────────────────         │  │
│  │  • amount: "100.50"             │   │  • attributes: {...}        │  │
│  │  • instrument_code: "KWH"       │   │                             │  │
│  │  • version: 1                   │   │  Go NEVER introspects this  │  │
│  │  • valid_from/valid_to          │   │  map for business logic.    │  │
│  └─────────────────────────────────┘   └─────────────────────────────┘  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

**The Contract:**

| Layer | Responsibility | Touches `attributes`? |
|-------|----------------|----------------------|
| **Protobuf** | Physical container, wire format | Serializes blindly |
| **Go** | Envelope handling (Amount, Code, Version) | **NEVER** introspects for business logic |
| **CEL** | Letter handling (Attributes validation, bucket key) | **SOLE** accessor for business rules |

> **Critical Rule**: Go code **blindly passes** the `attributes` map to CEL. It never reads attribute
> keys or values for business logic. This guarantees that adding a new asset type **never requires
> Go code changes** - only a new CEL expression in the database.

### How the Layers Connect

| Concept | Defined In (Static) | Instance Data (Runtime) | Connected By |
|---------|---------------------|-------------------------|--------------|
| **Structure** | **Protobuf** (`InstrumentAmount`) | `map<string,string> attributes` | The Wire Format |
| **Logic** | **CEL** (Reference Data) | `validation_expression`, `fungibility_key_expression` | The Validation Engine |
| **Bridge** | **Go** (`quantity.ParseQuantity`) | `env.Program.Eval(vars)` | Variable Injection |

**Runtime flow:**

```go
// 1. PROTO holds the payload (the Sealed Envelope)
protoMsg := &pb.InstrumentAmount{
    Amount:         "100.50",
    InstrumentCode: "KWH",
    Version:        1,
    Attributes:     map[string]string{"tou_period": "14", "region": "us-east"},
}

// 2. GO handles the Envelope (Amount, Code) - NEVER reads attribute keys
amount := decimal.RequireFromString(protoMsg.Amount)
cached := cache.Get(tenantID, protoMsg.InstrumentCode, protoMsg.Version)

// 3. CEL handles the Letter (Attributes) - Go passes map blindly
celVars := map[string]interface{}{
    "attributes": protoMsg.Attributes,  // Go doesn't know what's in here
}
result, _ := cached.ValidationProgram.Eval(celVars)
// CEL knows: "has(attributes.tou_period) && int(attributes.tou_period) >= 0"
```

**The Architectural Moat:**

- **Proto** provides infinite flexibility (any `map<string,string>`)
- **CEL** provides rigid safety (tenant-defined validation rules)
- **Go Generics** provide compile-time physics (`Quantity[Monetary]` vs `Quantity[Commodity]`)
- **Go blindness** ensures new asset types never require code deployment

---

## Asset Catalogue (Open Source Library)

The PRD defines a flexible system where tenants can create custom instruments with CEL expressions.
However, expecting every tenant to write CEL from scratch is operationally impractical. We need a
**Standard Library** of validated asset types that users can instantiate or extend.

### Concept: Asset Archetypes

An **Archetype** is a pre-built instrument template stored as YAML in the repository. Archetypes
provide battle-tested CEL logic for common asset classes, contributed by domain experts.

**Directory structure:**

```text
configs/archetypes/
├── commodity/
│   ├── perishable_goods.yaml
│   └── perishable_goods_test.yaml
├── energy/
│   ├── renewable_power.yaml
│   └── renewable_power_test.yaml
└── financial/
    ├── carbon_credit.yaml
    └── carbon_credit_test.yaml
```

### Example Archetype Definition

**File:** `configs/archetypes/commodity/perishable_goods.yaml`

```yaml
name: "Perishable Goods (Base)"
description: "Items that expire and cannot be traded after a specific date."
dimension: "Commodity"
attributes:
  - name: "expiry_date"
    type: "timestamp"
    required: true
validation_cel: |
  has(attributes.expiry_date) &&
  timestamp(attributes.expiry_date) > now
# Bucket key = sole determinant of fungibility
# Same expiry_date = same bucket = fungible
fungibility_key_cel: |
  attributes.expiry_date
```

**File:** `configs/archetypes/energy/renewable_power.yaml`

```yaml
name: "Renewable Energy Certificate"
dimension: "Commodity"
attributes:
  - name: "generation_source"
    enum: ["solar", "wind", "hydro"]
  - name: "region"
validation_cel: |
  has(attributes.generation_source) &&
  attributes.generation_source in ['solar', 'wind', 'hydro']
# Bucket key = sole determinant of fungibility
# Same source + region = same bucket = fungible
fungibility_key_cel: |
  attributes.generation_source + "|" + attributes.region
```

### Testing Archetypes (Community Contribution Model)

Contributors don't touch Go code. They submit a YAML archetype + test file:

**File:** `configs/archetypes/energy/renewable_power_test.yaml`

```yaml
archetype: "renewable_power.yaml"
tests:
  - name: "Valid Solar"
    input: { "generation_source": "solar", "region": "us-east" }
    expect: PASS

  - name: "Invalid Source"
    input: { "generation_source": "coal", "region": "us-east" }
    expect: FAIL

  - name: "Missing Required Field"
    input: { "region": "us-east" }
    expect: FAIL
```

**CI Pipeline:**

1. Developer submits PR with new YAML archetype + test YAML
2. GitHub Actions runs `go run cmd/archetype-tester`
3. Tester compiles CEL from YAML and runs all scenarios
4. Green build → PR merges

### Goal: Asset Marketplace

Enable a library of asset types (EU Carbon Credits, ISDA Swaps, US Treasuries, GPU Compute Hours)
contributed by domain experts. Tenants select from the catalogue rather than writing CEL from scratch.

> **Implementation tasks** are defined in Stream F (F.4 and F.5).

---

## Fungibility Resolution

Beyond ingestion validation, CEL handles **operational predicates** that determine position
behaviour. This extends CEL from "is this data valid?" to "can these positions be combined?"

### The Bucket Key Pattern (Sole Source of Truth)

**The Problem**: Naively checking `AreFungible(a, b)` for every existing position is O(N).
With millions of positions, this kills performance.

**The Solution**: Use a single CEL expression to generate a deterministic **bucket key**.
If two positions have the same `bucket_id`, they ARE fungible. Period.

```text
┌─────────────────────────────────────────────────────────────────────────┐
│  The Bucket Key Pattern for Position Aggregation                        │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  fungibility_key_expression (The SOLE determinant of fungibility)       │
│  ────────────────────────────────────────────────────────────────       │
│  Input: attributes map                                                  │
│  Output: string (bucket key - SHA256 hash)                              │
│  Example: bucket_key(['region', 'vintage'])                             │
│                                                                         │
│  Rule: Same bucket_id = fungible. Different bucket_id = NOT fungible.   │
│  SECURITY: Use bucket_key(), never string concatenation (see below)     │
│                                                                         │
│  Write Path: Calculate key → INSERT       Read Path: GROUP BY bucket_id │
│              with bucket_id column                   → SUM(amount)      │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

> **Why no pairwise `equals()` check?** A pairwise comparison inside the read path introduces
> O(N²) complexity. If you need complex fungibility rules (e.g., "Vintage 2023 matches 2024"),
> encode that logic in the key expression to output the **same string** for both. Push complexity
> to configuration, not runtime.

### The Performance Guardrail

```text
┌─────────────────────────────────────────────────────────────────────────┐
│  NATIVE GO + SQL (Hot Path)        │  CEL (Policy Decisions)           │
├─────────────────────────────────────────────────────────────────────────┤
│  Quantity.Add()                    │  validation_expression            │
│  Quantity.Sub()                    │  fungibility_key_expression       │
│  decimal.Decimal arithmetic        │                                   │
│  SQL SUM() + GROUP BY bucket_id    │  (bucket key on write only)       │
└─────────────────────────────────────────────────────────────────────────┘
```

**Rule**: CEL runs on the **write path only** (validation + bucket key generation).
The read path uses pure SQL: `SELECT SUM(amount) ... GROUP BY bucket_id`.
No CEL evaluation on reads.

### Bucket Key Expression

Each instrument defines a CEL expression that generates a deterministic bucket key:

```javascript
// Input: 'attributes' map from incoming position
// Output: String - the bucket key (SHA256 hash for collision resistance)

// Example 1: Empty key = all positions fungible (default)
""

// Example 2: Group by region and vintage (SECURE - uses hash)
bucket_key(['region', 'vintage'])

// Example 3: Group by contract and expiry month
bucket_key(['contract_id', 'expiry_month'])
// Note: Pre-compute expiry_month in validation, store as attribute

// Example 4: Complex matching (Vintage 2023 and 2024 are fungible)
// Use validation_expression to normalise vintage to "recent" before bucketing:
// validation: if int(attributes.vintage) >= 2023 then attributes.vintage = "recent"
bucket_key(['region', 'vintage'])
```

> **CRITICAL SECURITY**: Always use `bucket_key()` function, NEVER string concatenation.
>
> **Delimiter Injection Attack**: If you use `attributes.region + "|" + attributes.vintage`:
>
> - Position A: `region="US|East"`, `vintage="2024"` → Key: `US|East|2024`
> - Position B: `region="US"`, `vintage="East|2024"` → Key: `US|East|2024`
>
> These two chemically different positions become fungible and the ledger merges them.
> The `bucket_key()` function uses length-prefixed hashing to prevent this attack.
>
> **Key Design**: If you need "Vintage 2023 matches 2024", normalise in validation
> (set `vintage="recent"` for both), then use `bucket_key(['region', 'vintage'])`.

### How Bucket & Verify Affects the Ledger

```mermaid
flowchart TB
    subgraph Input["Incoming Position"]
        NEW["Amount: 100 KWH<br/>attrs: {region: 'us-east', vintage: '2025'}"]
    end

    subgraph CELKey["Step 1: Generate Bucket Key (SHA256 hash)"]
        KEY["bucket_key(['region', 'vintage'])<br/>→ 'a1b2c3d4e5f6...' (32 hex chars)"]
    end

    subgraph Database["Step 2: Index Lookup (O(log N))"]
        DB["SELECT * FROM positions<br/>WHERE bucket_id = 'a1b2c3d4e5f6...'"]
    end

    subgraph Result["Step 3: Aggregate by Bucket"]
        R1["SUM(amount) = 150 KWH<br/>bucket_id: 'a1b2c3d4...'"]
        R2["SUM(amount) = 75 KWH<br/>bucket_id: 'f7e8d9c0...'"]
    end

    NEW --> CELKey
    CELKey --> KEY
    KEY --> Database
    Database --> Result
```

### The Aggregation Contract

When Position Keeping aggregates positions:

1. **Dimension check** (compile-time): `Quantity[Monetary]` cannot combine with `Quantity[Commodity]`
2. **Instrument check** (runtime): USD cannot combine with EUR
3. **Version check** (runtime): USD(v1) cannot combine with USD(v2)
4. **Bucket check** (SQL): `GROUP BY bucket_id` - same bucket = fungible

**That's it.** No CEL on the read path. Pure SQL aggregation.

### Write Path: Append-Only with Bucket ID

```go
// Position Keeping: Append-Only with Bucket ID
func (s *Service) RecordMeasurement(ctx context.Context, tenantID uuid.UUID, new Position) error {
    // NO LOCKING. Constant time O(1) writes.

    // 1. Validate attributes via CEL (cached program, ~100ns)
    cached, err := s.cache.Get(ctx, tenantID, new.InstrumentCode, new.Version)
    if err != nil {
        return err
    }
    if valid, _ := s.cel.Validate(cached.ValidationProgram, new.Attributes); !valid {
        return ErrAttributeValidationFailed
    }

    // 2. Generate bucket ID (hashCode) via CEL (~100ns)
    bucketID, err := s.cel.GenerateBucketKey(cached.BucketKeyProgram, new.Attributes)
    if err != nil {
        return err
    }
    new.BucketID = bucketID

    // 3. Insert new row immediately - no read-modify-write
    return s.repo.Insert(ctx, new)
}
```

### Read Path: Pure SQL Aggregation

```go
// Aggregation is pure SQL - no CEL on reads
func (s *Service) GetAggregatedPosition(
    ctx context.Context, tenantID uuid.UUID, accountID, instrumentCode string,
) ([]AggregatedPosition, error) {
    // Database does ALL the work via indexed GROUP BY
    // SELECT bucket_id, SUM(amount) FROM positions
    // WHERE account_id = ? AND instrument_code = ?
    // GROUP BY bucket_id
    return s.repo.AggregateByBucket(ctx, accountID, instrumentCode)
}

// For detailed breakdown within a bucket
func (s *Service) GetBucketDetails(
    ctx context.Context, accountID, instrumentCode, bucketID string,
) ([]Position, error) {
    // Just return raw positions - no CEL processing
    return s.repo.FindByBucket(ctx, accountID, instrumentCode, bucketID)
}
```

**Why Bucket Key Only is better:**

- Write path is O(1) constant time - calculate bucket ID, INSERT
- Read path is pure SQL: `GROUP BY bucket_id` uses B-tree index
- **No CEL on reads** - aggregation happens entirely in the database
- Background compaction can merge rows within buckets during low-traffic windows

> **Phase 1 Decision**: Append-Only is the **ONLY** supported write mode.
> Write-time merging requires per-bucket locking which adds complexity.
> Defer to Phase 2 after validating Append-Only meets real-world needs.

### Temporal Logic

Time-bound assets (energy, licenses, subscriptions) need temporal overlap policy.
Encode time ranges in the bucket key:

```javascript
// Bucket by contract and hour
attributes.contract_id + "|" + string(int(attributes.period_start) / 3600)
```

**Edge case**: If CEL returns `false` for a merge attempt, the operation either:

- Creates a new distinct position (accumulation), OR
- Fails with `ErrPositionsNotFungible` (strict mode)

The behaviour is configurable per instrument.

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
shared/domain/money/                shared/platform/quantity/
├── money.go (Money struct)         ├── quantity.go (Quantity[D])
├── currency.go                     ├── dimension.go (Monetary, Commodity)
└── errors.go                       ├── instrument.go (Instrument)
                                    └── currency/ (predefined fiat)

services/*/domain/money.go          services/*/domain/quantity.go
└── re-exports shared/domain/money  └── re-exports shared/platform/quantity
```

**Migration sequence:**

1. Stream A creates `shared/platform/quantity` (new, no breaking changes)
2. Per-service streams (I.1-I.4) migrate from `shared/domain/money` → `shared/platform/quantity`
3. After all services migrated, deprecate `shared/domain/money`

---

## Work Streams

Designed for parallel execution across multiple AI agents or developers. **Step 0 must complete
before any stream begins** - it establishes the immutable contracts that all streams implement.

> **⚠️ AI AGENT COORDINATION**: Streams can run in parallel ONLY within the same phase.
> All streams depend on Step 0 contracts. If an agent needs a contract change, it MUST abort
> and request human approval. See [Zero-State Contract](#zero-state-contract-immutable).

### Step 0: Contract Lock (HUMAN ONLY)

**Before any stream begins**, a human operator commits the immutable contracts:

1. Create `proto/quantity/v1/quantity.proto` (from Zero-State Contract section)
2. Create `proto/reference_data/v1/instrument.proto` (from Zero-State Contract section)
3. Create `shared/platform/quantity/interfaces.go` (from Zero-State Contract section)
4. Run `buf generate` to create Go stubs
5. Commit all files with message: `feat: lock zero-state contracts for sprint`
6. Calculate SHA256 checksums for contract verification in Stream J

**Rule**: No agent may modify these files. If blocked, abort and request contract update.

### Dependency Diagram

```mermaid
flowchart TB
    subgraph Step0["Step 0: Contract Lock (Human)"]
        Z["Zero-State<br/>Contracts<br/>(Proto + Interfaces)"]
    end

    subgraph Foundation["Phase 1: Foundation (Parallel)"]
        A["Stream A<br/>Core Types<br/>(implement interfaces)"]
        D["Stream D<br/>Protobuf<br/>(extend, not modify)"]
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
        direction TB
        I1["Stream I.1<br/>Position Keeping<br/>(Write Path)"]
        I1R["Stream I.1R<br/>Position Keeping<br/>(Read Path)"]
        I2["Stream I.2<br/>current-account"]
        I3["Stream I.3<br/>financial-accounting"]
        I4["Stream I.4<br/>payment-order &<br/>utilization-metering"]
    end

    subgraph Final["Phase 6: Verification"]
        J["Stream J<br/>Integration<br/>Tests"]
    end

    Z --> A
    Z --> D
    Z --> E
    A --> B
    A --> C
    A --> F
    D --> G
    E --> F
    F --> G
    F --> H
    G --> I1
    G --> I1R
    G --> I2
    G --> I3
    G --> I4
    H --> I1
    H --> I1R
    H --> I2
    H --> I3
    H --> I4
    I1 --> J
    I1R --> J
    I2 --> J
    I3 --> J
    I4 --> J
```

### Stream I Split (AI Execution Safety)

Stream I.1 (Position Keeping) is split into **Write Path** and **Read Path** sub-streams:

| Sub-Stream | Focus | Agent Specialty |
|------------|-------|-----------------|
| **I.1 (Write)** | `RecordMeasurement`, CEL validation, bucket key | CEL-heavy, AttributeBag pooling |
| **I.1R (Read)** | `GetAggregatedPosition`, SQL GROUP BY | SQL-heavy, query optimisation |

**Rationale**: These are different problem domains. One agent focuses on CEL complexity,
the other on SQL query performance. This prevents context-switching overhead.

---

## Stream A: Core Types Package

**Location:** `shared/platform/quantity/`
**Dependencies:** None (foundational)

### Deliverables

1. **Dimensions** (`dimension.go`)

   ```go
   type Monetary struct{}
   type Commodity struct{}
   ```

2. **Instrument** (`instrument.go`)

   ```go
   // Instrument identifies an asset type for quantity operations.
   // Maps to InstrumentDefinition from Reference Data service.
   type Instrument struct {
       Code      string    // "USD", "KWH", "GPU-H100"
       Version   uint32    // Schema version
       Dimension string    // "Monetary" or "Commodity" - required for deserialisation
       Precision int       // Decimal places (for display formatting; maths uses arbitrary precision)
   }
   ```

   > **Serialisation note**: `Dimension` is stored as a string (not type parameter) because
   > Go generics are erased at runtime. When deserializing from DB/proto, we use `Dimension`
   > to reconstruct the correct `Quantity[Monetary]` or `Quantity[Commodity]` at the boundary.

3. **Quantity[D]** (`quantity.go`)

   ```go
   type Quantity[D any] struct {
       Amount     decimal.Decimal
       Instrument Instrument
   }

   // Type aliases for common use cases
   type Money = Quantity[Monetary]
   type Asset = Quantity[Commodity]
   ```

   > **Decimal Parsing Performance**: Proto uses `string amount` for compatibility and precision.
   > Parsing strings to `decimal.Decimal` happens on every ledger row read. Use a high-performance
   > parser like `shopspring/decimal` which benchmarks at ~500ns per parse. At 100k TPS reads,
   > this adds ~50ms/second overhead - acceptable but worth monitoring.
   >
   > **Alternative considered**: Google's `google.type.Money` (units + nanos) or custom proto
   > `Decimal` message (value + scale). Both require more complex marshalling. String parsing
   > with a fast library is simpler and sufficient for our scale.

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
   // QuantityValue is the closed interface for handling mixed dimensions.
   // Consumers use type-safe accessors instead of raw type assertions.
   type QuantityValue interface {
       Dimension() string
       GetAmount() decimal.Decimal
       GetInstrument() Instrument
       IsZero() bool

       // Type-safe accessors - use these instead of type assertions
       AsMonetary() (Quantity[Monetary], bool)
       AsCommodity() (Quantity[Commodity], bool)
   }

   // ParseQuantity converts raw data into a QuantityValue.
   // Returns the interface type - consumers must use AsMonetary()/AsCommodity()
   // for type-safe access. This avoids panic-prone type assertions.
   func ParseQuantity(amount decimal.Decimal, inst Instrument) (QuantityValue, error) {
       switch inst.Dimension {
       case "Monetary":
           return Quantity[Monetary]{Amount: amount, Instrument: inst}, nil
       case "Commodity":
           return Quantity[Commodity]{Amount: amount, Instrument: inst}, nil
       default:
           return nil, ErrUnknownDimension
       }
   }

   // Usage pattern - safe dimension handling:
   //
   // qv, err := ParseQuantity(amount, inst)
   // if m, ok := qv.AsMonetary(); ok {
   //     // Handle monetary quantity
   // } else if c, ok := qv.AsCommodity(); ok {
   //     // Handle commodity quantity
   // }
   ```

   > **Why QuantityValue instead of `any`?** Returning `any` forces raw type assertions
   > (`result.(Quantity[Monetary])`) which panic if wrong. The interface's `AsMonetary()`
   > and `AsCommodity()` methods return `(T, bool)`, matching Go's map/type-switch pattern.
   > This provides compile-time safety at the call site without requiring dimension knowledge.

   **Interface Implementation** (both Quantity types implement QuantityValue):

   ```go
   func (q Quantity[Monetary]) AsMonetary() (Quantity[Monetary], bool) { return q, true }
   func (q Quantity[Monetary]) AsCommodity() (Quantity[Commodity], bool) { return Quantity[Commodity]{}, false }
   func (q Quantity[Commodity]) AsMonetary() (Quantity[Monetary], bool) { return Quantity[Monetary]{}, false }
   func (q Quantity[Commodity]) AsCommodity() (Quantity[Commodity], bool) { return q, true }
   ```

   For code paths where dimension is known at compile time, prefer direct construction:

   ```go
   // Known dimension at compile time - use NewQuantity[D]
   money := NewQuantity[Monetary](decimal.NewFromInt(100), usdInstrument)

   // Unknown dimension at runtime - use ParseQuantity + interface
   qv, _ := ParseQuantity(amountFromProto, instFromDB)
   if m, ok := qv.AsMonetary(); ok {
       processMonetaryPayment(m)
   } else if c, ok := qv.AsCommodity(); ok {
       processCommodityTransfer(c)
   }
   ```

   **When to use which:**

   | Scenario | Use | Returns |
   |----------|-----|---------|
   | Unknown dimension at compile time | `ParseQuantity()` | `any` → use `AsMonetary()`/`AsCommodity()` |
   | Known dimension (e.g., Current Account = Monetary) | `NewQuantity[Monetary]()` | `Quantity[Monetary]` directly |
   | Generic operations on any quantity | `QuantityValue` interface | Type-safe, no panic risk |

7. **Typed Rehydration Constructor** (`quantity.go`)

   > **The Rehydration Problem**: When loading from DB/Proto, we need to validate that
   > the stored dimension matches the expected compile-time type. This constructor
   > provides a type-safe bridge with explicit dimension validation.

   ```go
   // NewQuantity creates a quantity from raw data, validating the dimension.
   // Use this when you KNOW the expected dimension at compile time.
   func NewQuantity[D Dimension](amount decimal.Decimal, inst Instrument) (Quantity[D], error) {
       // Runtime check: does D match inst.Dimension?
       var zero D
       if inst.Dimension != zero.Name() {
           return Quantity[D]{}, ErrDimensionMismatch
       }
       return Quantity[D]{Amount: amount, Instrument: inst}, nil
   }

   // Dimension interface for type-safe rehydration
   type Dimension interface {
       Name() string
   }

   func (Monetary) Name() string  { return "Monetary" }
   func (Commodity) Name() string { return "Commodity" }
   ```

### Acceptance Criteria

- [ ] `Quantity[Monetary].Add(Quantity[Commodity])` fails at compile time
- [ ] `USD.Add(EUR)` returns `ErrInstrumentMismatch` at runtime
- [ ] `USD(v1).Add(USD(v2))` returns `ErrVersionMismatch` at runtime
- [ ] `ParseQuantity` correctly bridges runtime strings to compile-time types
- [ ] `NewQuantity[Monetary]` with `Commodity` instrument returns `ErrDimensionMismatch`
- [ ] 100% test coverage on arithmetic operations

---

## Stream B: Currency Definitions

**Location:** `shared/platform/quantity/currency/`
**Dependencies:** Stream A (Instrument type)

### Deliverables

1. **Predefined Instruments** for major currencies (ISO 4217):
   - USD, EUR, GBP, JPY, CHF, AUD, CAD, NZD
   - Precision: 2 for most, 0 for JPY

2. **Lookup function**:

   ```go
   func ByCode(code string) (Instrument, bool)
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

**Location:** `shared/platform/quantity/`
**Dependencies:** Stream A (Quantity, Instrument types)

> **Scope boundary**: This stream covers the Rate data structure and basic conversion maths only.
> ValuationProvider interface and orchestration belongs in a future Valuation Engine PRD (ADR-019).

### Deliverables

1. **Rate type** (`rate.go`)

   ```go
   type Rate struct {
       From      Instrument
       To        Instrument
       Factor    decimal.Decimal
       ValidFrom time.Time
       ValidTo   time.Time
   }

   // Convert applies the rate to a quantity, returning the converted amount.
   // Returns error if quantity's instrument doesn't match Rate.From.
   func (r Rate) Convert(q Quantity[Monetary]) (Quantity[Monetary], error)
   ```

2. **Identity rate helper**:

   ```go
   // IdentityRate returns a 1:1 rate for same-currency operations
   func IdentityRate(inst Instrument) Rate
   ```

3. **Rate validation**: Ensure `From != To` unless identity, validate temporal bounds

4. **Precision Handling**:

   > **Rule**: `Rate.Convert()` outputs a result with the **target instrument's precision**.

   ```go
   func (r Rate) Convert(q Quantity[Monetary]) (Quantity[Monetary], error) {
       if q.Instrument.Code != r.From.Code {
           return Quantity[Monetary]{}, ErrInstrumentMismatch
       }

       // Multiply by factor
       rawResult := q.Amount.Mul(r.Factor)

       // Round to target instrument's precision using Banker's rounding
       // (round half to even) for financial compliance
       roundedResult := rawResult.RoundBank(int32(r.To.Precision))

       return Quantity[Monetary]{
           Amount:     roundedResult,
           Instrument: r.To,
       }, nil
   }
   ```

   > **Rounding Mode**: Banker's rounding (round half to even) is required for financial
   > compliance. It eliminates systematic bias that occurs with round-half-up.
   >
   > **Performance Note**: `shopspring/decimal.RoundBank()` is ~100-200ns per call. At 100k TPS
   > with valuation loops, verify this doesn't become a bottleneck. The operation is CPU-bound
   > (no allocations), so it scales with core count. If profiling shows hotspot, consider
   > batching conversions or caching common rate+precision combinations.

   **Example**: Converting 1 Gold Bar (precision=4) to USD (precision=2) at rate 2,847.5350:
   - Raw: `1 * 2847.5350 = 2847.5350`
   - Banker's rounding: `2847.54` (0.535 → 0.54 because 4 is even)

### Acceptance Criteria

- [ ] `Rate.Convert()` correctly multiplies amount by factor
- [ ] `Rate.Convert()` uses Banker's rounding (round half to even)
- [ ] `Rate.Convert()` returns error if source instrument mismatch
- [ ] `IdentityRate()` returns factor of 1.0
- [ ] Rate with `ValidFrom > ValidTo` rejected

---

## Stream D: Protobuf Definitions

**Location:** `proto/platform/v1/`
**Dependencies:** Stream A (type design, can work from ADR spec)

> **Proto vs CEL**: Protobuf defines the **Container** (data structure). CEL is the **Gatekeeper**
> (validation logic). Proto messages are pure data carriers with no behaviour. CEL expressions
> are compiled and executed by the service layer to validate attribute payloads.

### Deliverables

1. **InstrumentAmount message** (`quantity.proto`) - *The Data Carrier*

   > **See Zero-State Contract** for canonical proto definition. Key design points below.

   ```protobuf
   message AttributeEntry {
       string key = 1;    // Must be snake_case
       string value = 2;
   }

   message InstrumentAmount {
       string amount = 1;
       string instrument_code = 2;
       uint32 version = 3;
       repeated AttributeEntry attributes = 4;  // NOT map - enables pooling
       google.protobuf.Timestamp valid_from = 5;
       google.protobuf.Timestamp valid_to = 6;
       string source = 7;  // Quality Ladder (ADR-0017)
   }
   ```

   > **Why `repeated AttributeEntry` instead of `map<string, string>`**:
   >
   > - **Poolability**: Slices can be reset and reused via sync.Pool. Maps cannot.
   > - **Determinism**: Slice iteration order is stable. Map iteration is random.
   > - **GC at scale**: At 100k TPS, map allocations cause 100k heap allocations/sec.
   >   Pooled slices eliminate this entirely.
   >
   > **CEL Bridge**: Go code converts `[]AttributeEntry` to `map[string]string` only at the
   > CEL evaluation boundary. This transient map is short-lived (per-request) and acceptable.
   >
   > **Why explicit time fields**: Per ADR-0017 (Temporal Quality), time is a first-class citizen.
   > Storing `valid_from`/`valid_to` as top-level fields avoids parsing from attributes.

   **CEL Type Coercion Table**: Force developers to rely on CEL's casting, not custom Go parsing:

   | User Intent | Attribute Value (Proto) | CEL Expression |
   |-------------|-------------------------|----------------|
   | **Integer** | `"100"` | `int(attributes['val'])` |
   | **Float** | `"99.5"` | `double(attributes['val'])` |
   | **Boolean** | `"true"` | `bool(attributes['val'])` |
   | **Timestamp** | `"2025-01-01T00:00:00Z"` | `timestamp(attributes['val'])` |
   | **String** | `"us-east"` | `attributes['val']` (no coercion) |

   > **Rule**: All type conversion happens in CEL expressions. Go code only sees `map[string]string`.

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

       // The "Bucketer": Generates deterministic key for database grouping.
       // Compiled and cached. Input: attributes map. Output: string (SHA256 hash).
       // Example: "bucket_key(['region', 'vintage'])"
       // SECURITY: Always use bucket_key(), never string concatenation (delimiter injection)
       // Default: "" (all positions in same bucket - fungible by instrument alone)
       // CRITICAL: This key is stored as `bucket_id` column and indexed for O(log N) lookups.
       // RULE: Same bucket_id = fungible. Different bucket_id = NOT fungible.
       string fungibility_key_expression = 8;

       string display_name = 9;
       string description = 10;
   }
   ```

   > **Sole Source of Truth**: `fungibility_key_expression` is the **only** determinant of fungibility.
   > There is no pairwise `equals()` check. If two positions have the same `bucket_id`, they are
   > fungible and will be aggregated together via `GROUP BY bucket_id`. Complex fungibility rules
   > must be encoded in the key expression to output the same string for fungible positions.
   >
   > **Immutability of Bucket Key Logic (CRITICAL)**:
   > The `fungibility_key_expression` is **immutable** for a given Instrument Version. Changing the
   > bucketing logic fundamentally changes what the asset *is*. Historical rows have the old `bucket_id`;
   > SQL `GROUP BY` will treat old and new buckets as separate positions.
   >
   > **To change logic, the Tenant must create Version N+1.** Positions in Version N remain in their
   > old buckets until explicitly traded/migrated to Version N+1 via a "Wash/Reload" trade pattern.

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
**Dependencies:** Stream A (Instrument field design)

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

       -- Lifecycle status: DRAFT allows editing, ACTIVE locks expressions
       status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',

       -- CEL Expressions (compiled and cached by service layer)
       validation_expression TEXT NOT NULL DEFAULT 'true',  -- Ingestion gatekeeper

       -- Bucket key for O(log N) position aggregation
       -- Empty string = all positions fungible by instrument alone
       -- Same bucket_id = fungible. Different bucket_id = NOT fungible.
       fungibility_key_expression TEXT NOT NULL DEFAULT '',

       -- Optional user-friendly error message expression (CEL)
       error_message_expression TEXT,

       -- Client-facing schema for attribute discovery (JSON Schema draft-07)
       -- Enables frontends to generate forms and validate input client-side
       attribute_schema JSONB,

       display_name VARCHAR(128),
       description TEXT,
       created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       activated_at TIMESTAMPTZ,  -- When status changed to ACTIVE

       UNIQUE(tenant_id, code, version),
       CHECK (precision >= 0 AND precision <= 18),
       CHECK (dimension IN ('Monetary', 'Commodity')),
       CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
       CHECK (length(trim(validation_expression)) > 0),
       CHECK (length(validation_expression) <= 4096),
       CHECK (length(fungibility_key_expression) <= 4096)
   );

   CREATE INDEX idx_instrument_definitions_lookup
       ON instrument_definitions(tenant_id, code, version);

   -- LIFECYCLE AND IMMUTABILITY ENFORCEMENT
   -- CockroachDB does not support PL/pgSQL triggers.
   -- Enforce lifecycle rules at the Go application layer (repository/service).
   --
   -- Rules enforced by InstrumentDefinitionRepository.Update():
   -- DRAFT: Free editing, no transactions allowed
   --   - When activating (DRAFT -> ACTIVE), set activated_at = NOW()
   -- ACTIVE: Expressions locked, transactions allowed
   --   - Reject updates to fungibility_key_expression or validation_expression
   --   - Reject revert to DRAFT (would orphan bucket calculations)
   --   - Allow ACTIVE -> DEPRECATED (soft deprecation)
   -- DEPRECATED: Read-only, no new transactions
   ```

   > **Two CEL expressions per instrument**:
   >
   > - `validation_expression`: "Is this data valid?" (ingestion gatekeeper)
   > - `fungibility_key_expression`: "What bucket does this belong to?" (sole determinant of fungibility)
   >
   > **Instrument Lifecycle**:
   >
   > ```text
   > ┌─────────┐      Activate()      ┌─────────┐     Deprecate()    ┌────────────┐
   > │  DRAFT  │ ──────────────────▶  │ ACTIVE  │ ─────────────────▶ │ DEPRECATED │
   > └─────────┘                      └─────────┘                    └────────────┘
   >     │                                │                               │
   >     │ • Expressions editable         │ • Expressions LOCKED          │ • Read-only
   >     │ • No transactions allowed      │ • Transactions allowed        │ • No new txns
   >     │ • CEL Playground testing       │ • activated_at set            │ • Soft sunset
   >     └────────────────────────────────┴───────────────────────────────┘
   >                                    No revert from ACTIVE → DRAFT
   > ```

2. **Position table columns** (added by Stream I.1 migration)

   ```sql
   -- Migration 1: Add columns
   ALTER TABLE positions ADD COLUMN bucket_id VARCHAR(256);
   ALTER TABLE positions ADD COLUMN dimension VARCHAR(32) NOT NULL DEFAULT 'Monetary';
   ALTER TABLE positions ADD COLUMN attributes JSONB;
   ```

   ```sql
   -- Migration 2 (separate file): Add index after columns are public
   -- CockroachDB requires columns to be committed before referencing in indexes
   CREATE INDEX idx_positions_bucket ON positions(tenant_id, instrument_code, bucket_id);
   ```

   > **Write path**: Calculate bucket_id from `fungibility_key_expression`, store with position.
   > **Read path**: `SELECT SUM(amount) FROM positions WHERE bucket_id = ?` uses index.
   >
   > ⚠️ **QUERY CONSTRAINT (CRITICAL)**: The index does NOT include `attributes` JSONB column.
   >
   > **ALLOWED**: Queries using `bucket_id` or `bucket_id IN (...)`:
   >
   > ```sql
   > SELECT SUM(amount) FROM positions WHERE bucket_id = 'a1b2c3d4...'
   > SELECT * FROM positions WHERE bucket_id IN ('a1b2c3d4...', 'e5f6g7h8...')
   > ```
   >
   > **FORBIDDEN**: Queries filtering by raw attributes (causes full table scan):
   >
   > ```sql
   > -- DO NOT DO THIS - will scan ALL positions for the instrument
   > SELECT * FROM positions WHERE attributes->>'grade' = 'A'
   > ```
   >
   > If a tenant needs "How much Grade A Rice do I have?", the application must
   > calculate the bucket_id for `{grade: "A"}` using `bucket_key()`, then query by
   > that bucket_id.
   >
   > **Read Availability**: The `dimension` column is **intentionally redundant**. It allows
   > Position Keeping to instantiate `Quantity[D]` structs and perform basic aggregations
   > **even if the Reference Data service is offline**. Without this, a Reference Data outage
   > would make the Ledger unreadable (Tier 0 availability coupled to Tier 1 service).
   >
   > The tradeoff: ~32 bytes per row vs. service independence on the read path.

3. **System tenant seed data** (App Bootstrap - NOT SQL Migration):

   > **The Chicken-and-Egg Problem**: To create a Tenant, you need to bill them. To bill them,
   > you need an Instrument (`USD`). To define `USD`, you need Reference Data Service running.
   > The Reference Data Service needs the database schema.
   >
   > **Solution**: Seed base instruments via **App Bootstrap**, not SQL migration. This keeps
   > migrations purely structural (schema changes only) while application code handles data seeding.
   > The service startup sequence ensures instruments exist before accepting traffic.

   ```go
   // bootstrap.go - Runs on service startup, AFTER migrations, BEFORE accepting traffic
   func (s *Service) Bootstrap(ctx context.Context) error {
       // Idempotent: Only insert if not already present
       systemInstruments := []InstrumentDefinition{
           {
               TenantID:   SystemTenantID,
               Code:       "USD",
               Version:    1,
               Dimension:  "Monetary",
               Precision:  2,
               Status:     "ACTIVE",
               ValidationExpression: "true",
               DisplayName: "US Dollar",
           },
           {
               TenantID:   SystemTenantID,
               Code:       "EUR",
               Version:    1,
               Dimension:  "Monetary",
               Precision:  2,
               Status:     "ACTIVE",
               ValidationExpression: "true",
               DisplayName: "Euro",
           },
           {
               TenantID:   SystemTenantID,
               Code:       "GBP",
               Version:    1,
               Dimension:  "Monetary",
               Precision:  2,
               Status:     "ACTIVE",
               ValidationExpression: "true",
               DisplayName: "British Pound",
           },
       }

       for _, inst := range systemInstruments {
           // CRITICAL: Use ON CONFLICT DO NOTHING, not UPSERT
           // In distributed deployment (5 replicas starting simultaneously),
           // all will race to insert. Only one wins; others hit conflict.
           // DO NOT log conflict as error - it's expected behaviour.
           if err := s.repo.InsertIfNotExists(ctx, inst); err != nil {
               return fmt.Errorf("bootstrap instrument %s: %w", inst.Code, err)
           }
       }
       return nil
   }

   // InsertIfNotExists uses ON CONFLICT DO NOTHING.
   // Returns nil on both success AND conflict (idempotent).
   func (r *PostgresRepo) InsertIfNotExists(ctx context.Context, inst InstrumentDefinition) error {
       _, err := r.db.ExecContext(ctx, `
           INSERT INTO instrument_definitions (
               id, tenant_id, code, version, dimension, precision,
               status, validation_expression, display_name
           ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
           ON CONFLICT (tenant_id, code, version) DO NOTHING
       `, uuid.New(), inst.TenantID, inst.Code, inst.Version, inst.Dimension,
          inst.Precision, inst.Status, inst.ValidationExpression, inst.DisplayName)

       // DO NOT check RowsAffected() == 0 as an error.
       // Zero rows affected means another replica already inserted - that's fine.
       return err
   }
   ```

   > **Distributed Deployment Race Condition**:
   >
   > When 5 Kubernetes replicas start simultaneously, they all race to INSERT system instruments.
   > Using `ON CONFLICT DO NOTHING` ensures only one wins, others silently succeed (no error).
   >
   > **CRITICAL**: Do NOT log "unique constraint violation" as an error. It's expected behaviour
   > in distributed deployments. The InsertIfNotExists pattern treats conflict as success.
   >
   > **Alternative (Kubernetes Job)**: For stricter control, run seeding as a Helm pre-install hook:
>
   > ```yaml
   > # helm/templates/seed-job.yaml
   > apiVersion: batch/v1
   > kind: Job
   > metadata:
   >   name: {{ .Release.Name }}-seed
   >   annotations:
   >     "helm.sh/hook": pre-install,pre-upgrade
   >     "helm.sh/hook-weight": "5"  # After migrations (weight 0)
   > spec:
   >   template:
   >     spec:
   >       containers:
   >       - name: seed
   >         image: {{ .Values.image }}
   >         command: ["./reference-data", "seed"]
   >       restartPolicy: Never
   > ```
   >
   > **Why App Bootstrap over SQL Migration?**
   >
   > - Migrations stay **pure DDL** (CREATE TABLE, ALTER, etc.) - easier to review and audit
   > - Seed data lives in **Go code** - type-safe, testable, version-controlled alongside service
   > - **Idempotent by design** - uses ON CONFLICT DO NOTHING, safe to run multiple times
   > - **Environment-specific** - dev/staging/prod can have different seed sets if needed

### Acceptance Criteria

- [ ] Migration applies cleanly (pure DDL, no INSERT statements)
- [ ] Unique constraint prevents duplicate code+version per tenant
- [ ] Index supports efficient lookups
- [ ] App Bootstrap seeds system instruments on startup (idempotent)
- [ ] System instruments have `status='ACTIVE'` after bootstrap
- [ ] `validation_expression` column stores valid CEL expressions
- [ ] Default `'true'` allows permissive instruments (no attribute constraints)
- [ ] DRAFT instruments allow expression edits via trigger
- [ ] ACTIVE instruments reject expression updates (trigger raises exception)
- [ ] ACTIVE → DRAFT revert is blocked by trigger
- [ ] ACTIVE → DEPRECATED transition allowed

---

## Stream F: Reference Data Service

**Location:** `services/reference-data/`
**Dependencies:** Stream A, Stream E

### Deliverables

1. **InstrumentRegistry interface** (`registry.go`)

   ```go
   // SystemTenantID is the well-known UUID for platform-wide instruments.
   // NOTE: We use a NON-ZERO UUID to distinguish "System Tenant" from "Uninitialized".
   // Many ORMs and Go's uuid.Nil treat all-zeros as "nil/unset", causing subtle bugs.
   // This UUID is valid v4 format (positions 13=4, 17=8) and clearly intentional.
   var SystemTenantID = uuid.MustParse("00000000-0000-4000-8000-000000000001")

   type InstrumentRegistry interface {
       // GetDefinition looks up instrument by tenant, falling back to SystemTenant if not found.
       // Lookup order: tenant_id → SystemTenantID
       GetDefinition(ctx context.Context, tenantID uuid.UUID, code string, version uint32) (InstrumentDefinition, error)

       // GetLatestDefinition returns highest version, with same fallback logic.
       // NOTE: When callers pass version=0, it means "use latest version".
       // Implementation: SELECT ... WHERE version = COALESCE(NULLIF($version, 0), (SELECT MAX(version) ...))
       // Or: if version == 0 { return GetLatestDefinition(...) }
       GetLatestDefinition(ctx context.Context, tenantID uuid.UUID, code string) (InstrumentDefinition, error)

       // CreateDefinition creates a DRAFT instrument (cannot create in SystemTenant via API)
       // Compiles CEL expression at creation time - fails fast on syntax errors
       // New instruments start in DRAFT status, allowing expression edits before activation.
       CreateDefinition(ctx context.Context, def InstrumentDefinition) (InstrumentDefinition, error)

       // UpdateDefinition updates a DRAFT instrument's expressions.
       // Returns ErrInstrumentLocked if instrument is ACTIVE or DEPRECATED.
       UpdateDefinition(ctx context.Context, def InstrumentDefinition) (InstrumentDefinition, error)

       // ActivateInstrument transitions DRAFT → ACTIVE, locking expressions permanently.
       // After activation, only DEPRECATED transition is allowed.
       ActivateInstrument(ctx context.Context, tenantID uuid.UUID, code string, version uint32) error

       // DeprecateInstrument transitions ACTIVE → DEPRECATED for soft sunset.
       // Deprecated instruments reject new transactions but allow reads.
       DeprecateInstrument(ctx context.Context, tenantID uuid.UUID, code string, version uint32) error

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

3. **System Tenant Write Protection and Lifecycle Errors**:

   ```go
   var (
       ErrSystemTenantReadOnly = errors.New("system tenant instruments are read-only")
       ErrInstrumentLocked     = errors.New("instrument is ACTIVE or DEPRECATED; expressions cannot be modified")
       ErrInstrumentNotDraft   = errors.New("instrument must be DRAFT to activate")
       ErrInstrumentNotActive  = errors.New("instrument must be ACTIVE to deprecate")
   )

   func (r *PostgresRegistry) CreateDefinition(
       ctx context.Context, def InstrumentDefinition,
   ) (InstrumentDefinition, error) {
       // Enforce: System Tenant instruments are admin-only (seeded via migrations)
       if def.TenantID == SystemTenantID {
           return InstrumentDefinition{}, ErrSystemTenantReadOnly
       }

       // New instruments always start as DRAFT (DB default, enforced here too)
       def.Status = "DRAFT"

       // Compile CEL expressions before persisting (fail fast on syntax errors)
       if _, err := r.compiler.CompileValidation(def.ValidationExpression); err != nil {
           return InstrumentDefinition{}, fmt.Errorf("%w: %v", ErrCELCompileError, err)
       }
       if _, err := r.compiler.CompileFungibility(def.FungibilityExpression); err != nil {
           return InstrumentDefinition{}, fmt.Errorf("%w: %v", ErrCELCompileError, err)
       }

       return r.queries.CreateInstrumentDefinition(ctx, def)
   }

   func (r *PostgresRegistry) ActivateInstrument(
       ctx context.Context, tenantID uuid.UUID, code string, version uint32,
   ) error {
       def, err := r.GetDefinition(ctx, tenantID, code, version)
       if err != nil {
           return err
       }
       // Enforce: System Tenant instruments are admin-only (cannot modify via API)
       if def.TenantID == SystemTenantID {
           return ErrSystemTenantReadOnly
       }
       if def.Status != "DRAFT" {
           return ErrInstrumentNotDraft
       }
       // DB trigger handles setting activated_at
       return r.queries.UpdateInstrumentStatus(ctx, def.ID, "ACTIVE")
   }

   func (r *PostgresRegistry) DeprecateInstrument(
       ctx context.Context, tenantID uuid.UUID, code string, version uint32,
   ) error {
       def, err := r.GetDefinition(ctx, tenantID, code, version)
       if err != nil {
           return err
       }
       // Enforce: System Tenant instruments are admin-only (cannot modify via API)
       if def.TenantID == SystemTenantID {
           return ErrSystemTenantReadOnly
       }
       if def.Status != "ACTIVE" {
           return ErrInstrumentNotActive
       }
       return r.queries.UpdateInstrumentStatus(ctx, def.ID, "DEPRECATED")
   }
   ```

   > **Bootstrap Clarification**: `CreateDefinition` validates **CEL syntax** (does it compile?),
   > NOT position attributes. This avoids a chicken-and-egg problem - we can't validate attributes
   > because no positions exist yet. The `ValidateAttributes` method is called by **Position Keeping**
   > at ingestion time, not by Reference Data during definition creation.

4. **CEL Security Constraints**:

   | Constraint | Limit | Enforcement Layer | Rationale |
   |------------|-------|-------------------|-----------|
   | Expression length | 4KB max | Stream F (Registry) | Prevent storage abuse |
   | Cost limit | 10,000 | Stream F (CEL env) | Prevent expensive evaluations |
   | Registration rate | 10/min/tenant | **Stream G (gRPC interceptor)** | Prevent DoS via compilation spam |
   | Expression depth | 10 levels max | Stream F (Registry) | Prevent stack overflow in evaluation |

   > **Rate Limiting Note**: Registration rate limiting is enforced as a **gRPC interceptor**
   > (Stream G, see `RateLimitInterceptor`), not in the registry service (Stream F). This ensures:
   > - Registry remains a pure domain service without transport concerns
   > - Rate limiting middleware is shared across all tenant-facing endpoints
   > - Metrics and rate-limit headers are handled at the transport layer
   >
   > **Preferred**: API Gateway rate limiting (Envoy, Kong). **Fallback**: gRPC interceptor.

   ```go
   const MaxExpressionLength = 4096  // 4KB
   const MaxExpressionDepth = 10     // Nesting levels

   func (r *PostgresRegistry) CreateDefinition(ctx context.Context, def InstrumentDefinition) error {
       // Length check before compilation (quick reject)
       if len(def.ValidationExpression) > MaxExpressionLength {
           return ErrExpressionTooLong
       }
       if len(def.FungibilityKeyExpression) > MaxExpressionLength {
           return ErrExpressionTooLong
       }

       // Parse to AST (we're compiling anyway - reuse the parse result)
       ast, issues := r.compiler.validationEnv.Parse(def.ValidationExpression)
       if issues != nil && issues.Err() != nil {
           return fmt.Errorf("%w: %v", ErrInvalidValidationExpression, issues.Err())
       }

       // Depth check on AST (not raw string - accurate measurement)
       if depth := measureASTDepth(ast.Expr()); depth > MaxExpressionDepth {
           return ErrExpressionTooDeep
       }

       // Continue with type-check and program compilation...
   }

   // measureASTDepth walks the CEL AST to find maximum nesting depth
   func measureASTDepth(expr *exprpb.Expr) int {
       // Traverse children recursively, return max depth
       // ...
   }
   ```

   > **Security Review**: Tenant-provided CEL expressions are validated at compile-time
   > by cel-go. Runtime execution uses `CostLimit(10000)` to abort expensive evaluations.
   > Expression depth limits (measured on AST, not raw string) prevent stack overflow.
   > CEL's non-Turing-complete nature guarantees termination.

5. **Attribute Key Validation** (CEL Compatibility):

   > **Rule**: Attribute keys referenced in CEL expressions MUST be `snake_case`
   > (alphanumeric + underscores only, starting with a letter).

   ```go
   var validAttributeKeyRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

   // validateAttributeKeys extracts attribute references from CEL and validates format
   func validateAttributeKeys(expr string) error {
       // Extract keys referenced as attributes.KEY or attributes['KEY']
       keys := extractAttributeKeys(expr)
       for _, key := range keys {
           if !validAttributeKeyRegex.MatchString(key) {
               return fmt.Errorf("%w: '%s' must be snake_case (e.g., 'expiry_date' not 'expiry-date')",
                   ErrInvalidAttributeKey, key)
           }
       }
       return nil
   }
   ```

   **Why**: CEL interprets `attributes.user-id` as subtraction (`attributes.user` minus `id`).
   Forcing `snake_case` ensures `attributes.user_id` works without bracket syntax `attributes['user-id']`.

6. **CEL Environment Variables** (Explicit Contract):

   The CEL environments are strictly typed. Variable names are fixed contracts:

   **Validation Expression Environment:**

   | Variable | Type | Description |
   |----------|------|-------------|
   | `attributes` | `Map<String, String>` | The attributes map from InstrumentAmount proto |
   | `amount` | `String` | The quantity magnitude (for min/max checks) |
   | `valid_from` | `Timestamp` | Period start (if provided) |
   | `valid_to` | `Timestamp` | Period end (if provided) |

   **Bucket Key Expression Environment:**

   | Variable | Type | Description |
   |----------|------|-------------|
   | `attributes` | `Map<String, String>` | The attributes map from InstrumentAmount proto |
   | Returns | `String` | The bucket key for database grouping (index lookup) |

   > **Note**: There is no pairwise "fungibility expression" environment. The bucket key is the
   > **sole determinant** of fungibility. Same key = fungible. Different key = not fungible.

7. **CEL Compiler** (`cel.go`) using `github.com/google/cel-go`:

   > **CEL Version Pinning (CRITICAL for Determinism)**
   >
   > CEL expressions generate `bucket_id` values stored permanently in the database.
   > If cel-go library behaviour changes (function semantics, type coercion), calculated
   > keys may shift, causing position splits or merges.
   >
   > **Requirements:**
   > - Pin `github.com/google/cel-go` to exact version in `go.mod` (e.g., `v0.20.1`)
   > - Never upgrade cel-go without running full bucket key regression tests
   > - Document cel-go version in instrument definition metadata for future audits
   > - BucketKeyLib MUST sort map keys before hashing (Go map iteration is random)
   >
   > ```go
   > // go.mod - pin exact version
   > require github.com/google/cel-go v0.20.1
   > ```

   ```go
   // CELVersion documents the pinned cel-go version for determinism audits.
   // If upgraded, run full bucket key regression tests before deployment.
   const CELVersion = "v0.20.1"

   type CELCompiler struct {
       validationEnv *cel.Env  // For validation_expression: attributes → bool
       bucketKeyEnv  *cel.Env  // For fungibility_key_expression: attributes → string
   }

   func NewCELCompiler() (*CELCompiler, error) {
       // Custom functions for safe string parsing (avoid "Date Format Hell")
       safeParseFuncs := cel.Lib(&SafeParseLib{})

       // Environment for ingestion validation: attributes → bool
       valEnv, err := cel.NewEnv(
           cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
           cel.Variable("amount", cel.StringType),
           cel.Variable("valid_from", cel.TimestampType),
           cel.Variable("valid_to", cel.TimestampType),
           cel.CostLimit(10000),
           safeParseFuncs,
       )
       if err != nil {
           return nil, err
       }

       // Custom function for hash-based bucket keys (avoid delimiter injection)
       bucketKeyFunc := cel.Lib(&BucketKeyLib{})

       // Environment for bucket key generation: attributes → string
       bucketEnv, err := cel.NewEnv(
           cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
           cel.CostLimit(10000),
           safeParseFuncs,
           bucketKeyFunc,
       )
       if err != nil {
           return nil, err
       }

       return &CELCompiler{
           validationEnv: valEnv,
           bucketKeyEnv:  bucketEnv,
       }, nil
   }

   // SafeParseLib provides safe string parsing functions to avoid "Date Format Hell".
   // Tenants MUST use these instead of raw CEL type coercion for consistent behaviour.
   //
   // Functions:
   //   parse_iso_date(string) -> timestamp  // Strict ISO 8601: "2025-01-15T00:00:00Z"
   //   parse_int(string) -> int             // Explicit error on non-numeric
   //   parse_decimal(string) -> double      // For amounts, explicit error on invalid
   //   parse_bool(string) -> bool           // Only "true"/"false", not "1"/"0"
   //
   // Example: parse_iso_date(attributes.expiry) > now
   // Instead of: timestamp(attributes.expiry) > now  // UNSAFE: format unknown
   type SafeParseLib struct{}

   func (SafeParseLib) CompileOptions() []cel.EnvOption {
       return []cel.EnvOption{
           cel.Function("parse_iso_date",
               cel.Overload("parse_iso_date_string",
                   []*cel.Type{cel.StringType}, cel.TimestampType,
                   cel.UnaryBinding(func(val ref.Val) ref.Val {
                       s := val.Value().(string)
                       t, err := time.Parse(time.RFC3339, s)
                       if err != nil {
                           return types.NewErr("parse_iso_date: invalid ISO 8601 format: %s", s)
                       }
                       return types.Timestamp{Time: t}
                   }),
               ),
           ),
           cel.Function("parse_int",
               cel.Overload("parse_int_string",
                   []*cel.Type{cel.StringType}, cel.IntType,
                   cel.UnaryBinding(func(val ref.Val) ref.Val {
                       s := val.Value().(string)
                       i, err := strconv.ParseInt(s, 10, 64)
                       if err != nil {
                           return types.NewErr("parse_int: invalid integer: %s", s)
                       }
                       return types.Int(i)
                   }),
               ),
           ),
           // parse_decimal and parse_bool follow same pattern...
       }
   }

   func (SafeParseLib) ProgramOptions() []cel.ProgramOption { return nil }

   // BucketKeyLib provides the bucket_key() function for SAFE bucket key generation.
   // This MUST be used instead of string concatenation to avoid delimiter injection.
   //
   // Example: bucket_key(['region', 'vintage'])
   // Output: SHA256 hash of sorted, length-prefixed key-value pairs
   //
   // SECURITY: Prevents delimiter injection attacks where:
   //   Region="US|East", Vintage="2024" would otherwise collide with
   //   Region="US", Vintage="East|2024"
   type BucketKeyLib struct{}

   func (BucketKeyLib) CompileOptions() []cel.EnvOption {
       return []cel.EnvOption{
           cel.Function("bucket_key",
               cel.Overload("bucket_key_list",
                   []*cel.Type{cel.ListType(cel.StringType)}, cel.StringType,
                   cel.FunctionBinding(func(args ...ref.Val) ref.Val {
                       // Extract attribute keys from CEL list
                       keys := args[0].(traits.Lister)
                       attrs := args[1].(traits.Mapper)  // attributes variable

                       // Sort keys for deterministic output
                       sortedKeys := extractAndSort(keys)

                       // Build length-prefixed concatenation, then hash
                       var buf bytes.Buffer
                       for _, k := range sortedKeys {
                           v := attrs.Get(types.String(k))
                           if types.IsError(v) {
                               return types.NewErr("bucket_key: missing attribute '%s'", k)
                           }
                           // Safe type assertion - attributes must be strings
                           strVal, ok := v.Value().(string)
                           if !ok {
                               return types.NewErr("bucket_key: attribute '%s' must be string, got %T", k, v.Value())
                           }
                           // Length prefix prevents delimiter injection
                           buf.WriteString(fmt.Sprintf("%d:%s=%d:%s;",
                               len(k), k, len(strVal), strVal))
                       }

                       // SHA256 for fixed-length, collision-resistant key
                       hash := sha256.Sum256(buf.Bytes())
                       return types.String(hex.EncodeToString(hash[:16])) // 32 hex chars
                   }),
               ),
           ),
       }
   }

   func (BucketKeyLib) ProgramOptions() []cel.ProgramOption { return nil }

   // CompileValidation compiles ingestion validation expression.
   func (c *CELCompiler) CompileValidation(expr string) (cel.Program, error) {
       return c.compile(c.validationEnv, expr)
   }

   // CompileBucketKey compiles bucket key generation expression.
   func (c *CELCompiler) CompileBucketKey(expr string) (cel.Program, error) {
       if expr == "" {
           return nil, nil  // Empty expression = all positions in same bucket
       }
       return c.compile(c.bucketKeyEnv, expr)
   }

   // GenerateBucketKey evaluates the bucket key for database grouping.
   // This is the SOLE determinant of fungibility.
   //
   // FAIL-FAST BEHAVIOR: If the CEL expression references a missing attribute,
   // this returns an error and the transaction MUST be rejected. We do not
   // support "partial" bucketing or "default" buckets.
   func (c *CELCompiler) GenerateBucketKey(prog cel.Program, attrs map[string]string) (string, error) {
       if prog == nil {
           return "", nil  // No key expression = empty bucket (all fungible by instrument)
       }
       out, _, err := prog.Eval(map[string]interface{}{"attributes": attrs})
       if err != nil {
           // CEL error = missing attribute or type mismatch
           // REJECT the transaction - do not fall back to "default" bucket
           return "", fmt.Errorf("bucket key generation failed: %w", err)
       }
       key, ok := out.Value().(string)
       if !ok {
           return "", fmt.Errorf("bucket key expression must return string, got %T", out.Value())
       }
       return key, nil
   }
   ```

   > **No pairwise comparison**: There is no `AreFungible(a, b)` function. Fungibility is
   > determined entirely by the bucket key. Same key = fungible. Different key = not fungible.
   >
   > **Fail-Fast on Missing Attributes**: If the bucket key expression references an attribute
   > that is missing (e.g., `attributes['vintage']` when only `region` is provided), the CEL
   > evaluation returns an error. The transaction is **rejected**, not placed in a "default" bucket.
   > This enforces data quality at the gate.

8. **PostgreSQL implementation** with sqlc-generated queries

9. **Error types**:

   ```go
   var (
       // Lookup errors
       ErrInstrumentNotFound  = errors.New("instrument not found")
       ErrDuplicateInstrument = errors.New("instrument already exists")

       // CEL compilation errors (fail at registration time)
       ErrInvalidValidationExpression = errors.New("validation expression failed to compile")
       ErrInvalidBucketKeyExpression  = errors.New("bucket key expression failed to compile")
       ErrExpressionTooLong           = errors.New("expression exceeds 4KB limit")
       ErrExpressionTooDeep           = errors.New("expression exceeds max nesting depth")

       // CEL runtime errors (fail at validation/key generation time)
       ErrAttributeValidationFailed = errors.New("attributes failed CEL validation")
       ErrBucketKeyGenerationFailed = errors.New("bucket key generation failed")
       ErrCELRuntimeError           = errors.New("CEL expression runtime error")

       // Access control errors
       ErrSystemTenantReadOnly = errors.New("system tenant instruments are read-only")
   )
   ```

   > **Error wrapping**: Use `fmt.Errorf("%w: %v", ErrInvalidValidationExpression, celErr)`
   > to preserve both the sentinel error and the underlying CEL parser message.

10. **CEL Error Message Surfacing** (Structured ValidationReport):

    CEL validation failures should provide **actionable, user-friendly** error messages:

    ```go
    // ValidationReport provides structured feedback for CEL failures
    type ValidationReport struct {
        InstrumentCode string              `json:"instrument_code"`
        Valid          bool                `json:"valid"`
        Errors         []ValidationError   `json:"errors,omitempty"`
        UserMessage    string              `json:"user_message,omitempty"`  // Human-readable
    }

    type ValidationError struct {
        Field   string `json:"field,omitempty"`   // e.g., "region"
        Code    string `json:"code"`              // e.g., "MISSING_REQUIRED_FIELD"
        Message string `json:"message"`           // e.g., "Missing required field: region"
    }
    ```

    **User-Friendly Error Generation:**

    InstrumentDefinition supports an optional `error_message_expression` (CEL) that constructs
    human-readable errors based on input attributes:

    ```protobuf
    message InstrumentDefinition {
        // ... existing fields ...

        // Optional: CEL expression to generate user-friendly error messages.
        // Input: attributes map. Output: string (error message, or "" if valid).
        // Example: "!has(attributes.region) ? 'Missing required field: region' : ''"
        string error_message_expression = 12;
    }
    ```

    ```go
    // Usage in ValidateAttributes - returns structured report, not just error
    func (r *Registry) ValidateAttributes(
        ctx context.Context, def InstrumentDefinition, attrs map[string]string,
    ) ValidationReport {
        report := ValidationReport{InstrumentCode: def.Code, Valid: true}

        // 1. Run validation expression
        result, _, err := r.validationProgram.Eval(map[string]any{"attributes": attrs})
        if err != nil {
            report.Valid = false
            report.Errors = append(report.Errors, ValidationError{
                Code:    "CEL_RUNTIME_ERROR",
                Message: err.Error(),
            })
            return report
        }

        // Safe type assertion - CEL expressions MUST return bool
        passed, ok := result.Value().(bool)
        if !ok {
            report.Valid = false
            report.Errors = append(report.Errors, ValidationError{
                Code:    "CEL_TYPE_ERROR",
                Message: fmt.Sprintf("validation expression must return bool, got %T", result.Value()),
            })
            return report
        }

        if !passed {
            report.Valid = false

            // 2. Generate user-friendly message via error_message_expression
            if def.ErrorMessageExpression != "" {
                msg, _ := r.errorMsgProgram.Eval(map[string]any{"attributes": attrs})
                if s, ok := msg.Value().(string); ok && s != "" {
                    report.UserMessage = s
                }
            }
            if report.UserMessage == "" {
                report.UserMessage = "Attributes failed validation"
            }
            report.Errors = append(report.Errors, ValidationError{
                Code:    "VALIDATION_FAILED",
                Message: report.UserMessage,
            })
        }
        return report
    }
    ```

    > **Why structured reports?** A generic "InvalidArgument: attributes failed validation"
    > frustrates developers. With `error_message_expression`, tenants can define:
    > `"!has(attributes.region) ? 'Missing required field: region' : ''"` to provide
    > actionable feedback like "Missing required field: region".

### Acceptance Criteria

- [ ] CRUD operations work correctly
- [ ] Tenant lookup falls back to System Tenant when not found
- [ ] `ListDefinitions` includes both tenant and System Tenant instruments
- [ ] Cannot create instruments in System Tenant via API (admin-only seed data)
- [ ] CEL expression compiled at `CreateDefinition` - syntax errors rejected immediately
- [ ] `ValidateAttributes` executes compiled CEL and returns clear error on `false`

### Phase 1.5: Asset Catalogue (Deferred)

> **Scope Decision**: The following deliverables are moved to Phase 1.5. For the "Frugal Path,"
> we only need to prove we can define *one* custom asset manually via API/CLI. Building a
> library system now distracts from the core ledger physics.

1. **Archetype Loader** (`cmd/archetype-loader`) - *Phase 1.5*

    Reads YAML archetype definitions from `configs/archetypes/` and seeds them into the System Tenant:

    ```go
    // ArchetypeLoader reads YAML files and creates InstrumentDefinitions
    type ArchetypeLoader struct {
        registry InstrumentRegistry
        compiler *CELCompiler
    }

    func (l *ArchetypeLoader) LoadAll(ctx context.Context, dir string) error {
        files, _ := filepath.Glob(filepath.Join(dir, "**/*.yaml"))
        for _, f := range files {
            if strings.HasSuffix(f, "_test.yaml") {
                continue // Skip test files
            }
            if err := l.loadArchetype(ctx, f); err != nil {
                return fmt.Errorf("failed to load %s: %w", f, err)
            }
        }
        return nil
    }

    func (l *ArchetypeLoader) loadArchetype(ctx context.Context, path string) error {
        var arch Archetype
        data, _ := os.ReadFile(path)
        yaml.Unmarshal(data, &arch)

        def := InstrumentDefinition{
            TenantID:              SystemTenantID,
            Code:                  arch.Name,
            Dimension:             arch.Dimension,
            ValidationExpression:  arch.ValidationCEL,
            FungibilityExpression: arch.FungibilityCEL,
        }
        _, err := l.registry.CreateDefinition(ctx, def)
        return err
    }
    ```

    > **Usage**: Run during deployment or as init container to seed archetypes.

2. **Archetype Tester** (`cmd/archetype-tester`) - *Phase 1.5*

    CI tool that validates archetype CEL logic against test scenarios:

    ```go
    // ArchetypeTester runs test cases against archetype definitions
    func (t *ArchetypeTester) RunTests(archetypePath, testPath string) error {
        arch := loadArchetype(archetypePath)
        tests := loadTests(testPath)

        program, err := t.compiler.CompileValidation(arch.ValidationCEL)
        if err != nil {
            return fmt.Errorf("failed to compile archetype %s: %w", archetypePath, err)
        }

        for _, tc := range tests.Tests {
            // Evaluate CEL expression with test input
            out, _, err := program.Eval(map[string]any{"attributes": tc.Input})
            if err != nil {
                // CEL runtime error - treat as validation failure
                if tc.Expect == "PASS" {
                    return fmt.Errorf("test %q: expected PASS, got CEL error: %v", tc.Name, err)
                }
                continue  // Expected FAIL, CEL error counts as failure
            }

            // Safe type assertion (mirrors AreFungible pattern)
            passed, ok := out.Value().(bool)
            if !ok {
                return fmt.Errorf("test %q: CEL expression must return bool, got %T", tc.Name, out.Value())
            }

            if tc.Expect == "PASS" && !passed {
                return fmt.Errorf("test %q: expected PASS, got FAIL", tc.Name)
            }
            if tc.Expect == "FAIL" && passed {
                return fmt.Errorf("test %q: expected FAIL, got PASS", tc.Name)
            }
        }
        return nil
    }
    ```

    > **CI Integration**: Add GitHub Action that runs `go run cmd/archetype-tester` on PRs
    > touching `configs/archetypes/`.

#### Phase 1.5 Acceptance Criteria

- [ ] Archetype loader seeds YAML definitions into System Tenant
- [ ] Archetype tester validates CEL logic against test scenarios in CI

---

## Stream G: gRPC API Handlers

**Location:** `services/reference-data/handler/`
**Dependencies:** Stream D, Stream F

### Deliverables

1. **ReferenceDataService** proto definition:

   ```protobuf
   service ReferenceDataService {
       // CRUD operations
       rpc RegisterInstrument(RegisterInstrumentRequest) returns (InstrumentDefinition);
       rpc UpdateInstrument(UpdateInstrumentRequest) returns (InstrumentDefinition);
       rpc RetrieveInstrument(RetrieveInstrumentRequest) returns (InstrumentDefinition);
       rpc ListInstruments(ListInstrumentsRequest) returns (ListInstrumentsResponse);

       // Lifecycle transitions (see Stream E/F for status semantics)
       rpc ActivateInstrument(ActivateInstrumentRequest) returns (InstrumentDefinition);
       rpc DeprecateInstrument(DeprecateInstrumentRequest) returns (InstrumentDefinition);

       // CEL Playground - Test expressions without persisting
       rpc EvaluateInstrument(EvaluateInstrumentRequest) returns (EvaluateInstrumentResponse);

       // Client Discovery - Get attribute schema for form generation
       rpc GetAttributeSchema(GetAttributeSchemaRequest) returns (GetAttributeSchemaResponse);
   }

   // InstrumentDefinition includes attribute_schema for client discovery
   message InstrumentDefinition {
       string code = 1;
       uint32 version = 2;
       string dimension = 3;
       uint32 precision = 4;
       string status = 5;  // DRAFT, ACTIVE, DEPRECATED
       string validation_expression = 6;
       string fungibility_key_expression = 7;
       string error_message_expression = 8;

       // CLIENT DISCOVERY: JSON Schema (draft-07) describing expected attributes.
       // Frontends use this to generate input forms and validate client-side.
       // Example: {"type":"object","properties":{"region":{"type":"string","enum":["US","EU"]}}}
       string attribute_schema_json = 9;

       string display_name = 10;
       string description = 11;
   }

   message GetAttributeSchemaRequest {
       string instrument_code = 1;
       uint32 version = 2;  // 0 = latest
   }

   message GetAttributeSchemaResponse {
       string instrument_code = 1;
       uint32 version = 2;

       // JSON Schema for client-side validation and form generation
       string attribute_schema_json = 3;

       // Extracted required fields (convenience for simple clients)
       repeated string required_attributes = 4;

       // Example valid attributes (for documentation/testing)
       map<string, string> example_attributes = 5;
   }

   message EvaluateInstrumentRequest {
       // CEL expressions to evaluate (same fields as InstrumentDefinition)
       string validation_expression = 1;
       string fungibility_key_expression = 2;
       string error_message_expression = 3;

       // Test data: attributes to evaluate against
       map<string, string> test_attributes = 4;
   }

   message EvaluateInstrumentResponse {
       // Did the expressions compile successfully?
       bool compile_success = 1;
       repeated string compile_errors = 2;

       // Validation result (if compiled successfully)
       bool validation_passed = 3;
       ValidationReport validation_report = 4;

       // Bucket key result (if compiled successfully)
       string bucket_key = 5;
   }
   ```

   > **Client Discovery Pattern**: The "Sealed Envelope" pattern solves the backend problem
   > (Go passes attributes blindly), but API consumers need to know what keys to include.
   >
   > **Solution**: Every `InstrumentDefinition` includes `attribute_schema_json` (JSON Schema).
   > Clients call `GetAttributeSchema` or inspect `RetrieveInstrument` response to:
   >
   > 1. Generate input forms dynamically (JSON Schema → UI components)
   > 2. Validate attributes client-side before submission
   > 3. Display helpful error messages (e.g., "region must be US or EU")
   >
   > **Example flow**:
   >
   > ```mermaid
   > sequenceDiagram
   >     participant C as Client
   >     participant R as Reference Data
   >     C->>R: GetAttributeSchema("RICE")
   >     R-->>C: {schema: {properties: {grade, origin}}}
   >     Note over C: Generate form from schema
   >     Note over C: Validate input client-side
   >     C->>R: RecordMeasurement(attrs)
   >     R-->>C: ✓ Valid
   > ```

2. **Handler implementation** with:
   - Tenant extraction from context
   - Input validation
   - Error mapping to gRPC codes

3. **Adapter layer** for proto ↔ domain conversion

4. **CEL Playground** (`EvaluateInstrument` handler):

   ```go
   // EvaluateInstrument provides a "dry run" mode for testing CEL expressions.
   // Use this to validate expressions BEFORE activating an instrument.
   // The endpoint does NOT persist anything - purely for testing.
   func (h *Handler) EvaluateInstrument(
       ctx context.Context,
       req *pb.EvaluateInstrumentRequest,
   ) (*pb.EvaluateInstrumentResponse, error) {
       resp := &pb.EvaluateInstrumentResponse{}

       // 1. Compile validation expression
       valProg, err := h.compiler.CompileValidation(req.ValidationExpression)
       if err != nil {
           resp.CompileSuccess = false
           resp.CompileErrors = append(resp.CompileErrors,
               fmt.Sprintf("validation_expression: %v", err))
       }

       // 2. Compile bucket key expression (if provided)
       var bucketProg cel.Program
       if req.FungibilityKeyExpression != "" {
           bucketProg, err = h.compiler.CompileFungibility(req.FungibilityKeyExpression)
           if err != nil {
               resp.CompileSuccess = false
               resp.CompileErrors = append(resp.CompileErrors,
                   fmt.Sprintf("fungibility_key_expression: %v", err))
           }
       }

       // 3. Compile error message expression (if provided)
       var errMsgProg cel.Program
       if req.ErrorMessageExpression != "" {
           errMsgProg, err = h.compiler.CompileErrorMessage(req.ErrorMessageExpression)
           if err != nil {
               resp.CompileSuccess = false
               resp.CompileErrors = append(resp.CompileErrors,
                   fmt.Sprintf("error_message_expression: %v", err))
           }
       }

       // If any compilation failed, return early
       if len(resp.CompileErrors) > 0 {
           return resp, nil
       }
       resp.CompileSuccess = true

       // 4. Evaluate against test attributes
       if len(req.TestAttributes) > 0 {
           // Run validation
           passed, report := h.validator.Evaluate(valProg, errMsgProg, req.TestAttributes)
           resp.ValidationPassed = passed
           resp.ValidationReport = report.ToProto()

           // Generate bucket key
           if bucketProg != nil {
               bucketKey, err := h.evaluator.GenerateBucketKey(bucketProg, req.TestAttributes)
               if err != nil {
                   resp.CompileErrors = append(resp.CompileErrors,
                       fmt.Sprintf("bucket_key evaluation failed: %v", err))
               } else {
                   resp.BucketKey = bucketKey
               }
           }
       }

       return resp, nil
   }
   ```

   > **Workflow**: Create DRAFT instrument → Test expressions via `EvaluateInstrument` →
   > Iterate on expressions using `UpdateInstrument` → When satisfied, call `ActivateInstrument`.
   > This prevents "lock-in" of untested expressions.

5. **Rate Limiting** for `RegisterInstrument`:

   > **Preferred location**: API Gateway (Envoy, Kong, Nginx) using external rate limit service.
   > Gateway-level rate limiting keeps operational concerns separate from business logic.
   >
   > **Fallback**: If Gateway rate limiting is not available, implement as gRPC interceptor:

   ```go
   // RateLimitInterceptor enforces per-tenant rate limits on instrument registration.
   // Uses token bucket algorithm: 10 tokens, refill 1 token per 6 seconds.
   //
   // NOTE: Prefer Gateway-level rate limiting (Envoy ratelimit service).
   // This interceptor is a fallback for environments without Gateway support.
   type RateLimitInterceptor struct {
       limiters sync.Map  // map[tenantID]*rate.Limiter
   }

   func (r *RateLimitInterceptor) UnaryInterceptor(
       ctx context.Context,
       req interface{},
       info *grpc.UnaryServerInfo,
       handler grpc.UnaryHandler,
   ) (interface{}, error) {
       // Only rate-limit RegisterInstrument, not reads
       if info.FullMethod != "/platform.v1.ReferenceDataService/RegisterInstrument" {
           return handler(ctx, req)
       }

       tenantID := tenant.FromContext(ctx)
       limiter := r.getLimiter(tenantID)  // 10/min token bucket

       if !limiter.Allow() {
           // Return gRPC RESOURCE_EXHAUSTED with Retry-After header
           return nil, status.Errorf(codes.ResourceExhausted,
               "rate limit exceeded: max 10 instrument registrations per minute")
       }

       return handler(ctx, req)
   }
   ```

   > **Implementation**: Use `golang.org/x/time/rate` for token bucket. Each tenant gets
   > independent limiter with burst=10, rate=10/minute. Limiters are lazily created and
   > stored in sync.Map for thread safety.

6. **Simulation Mode** (CLI/MCP - Full Transaction Dry Run):

   > Beyond `EvaluateInstrument` (which tests expressions only), users need to simulate
   > a complete transaction: "If I deposit 100 units with these attributes, what happens?"

   ```go
   // SimulateTransaction provides a full dry-run of a transaction.
   // Shows validation result, bucket assignment, and what the position would look like.
   type SimulateTransactionRequest struct {
       InstrumentCode string
       Version        uint32
       Amount         string
       Attributes     map[string]string
       ValidFrom      time.Time
       ValidTo        time.Time
       Source         string  // Quality Ladder source
   }

   type SimulateTransactionResponse struct {
       // Would validation pass?
       ValidationPassed bool
       ValidationErrors []string

       // What bucket would this land in?
       BucketID string

       // Preview of the position record
       PositionPreview struct {
           InstrumentCode string
           Amount         string
           BucketID       string
           Dimension      string
           Attributes     map[string]string
       }

       // If bucket already exists, what's the current balance?
       // (Optional - requires DB lookup, may return nil if not available)
       ExistingBucketBalance *string
   }
   ```

   **CLI Tool** (`cmd/instrument-cli`):

   ```bash
   # Simulate a deposit
   $ instrument-cli simulate --tenant abc123 \
       --instrument RICE --version 1 \
       --amount 100 \
       --attr grade=A --attr region=US \
       --valid-from 2025-01-01 --valid-to 2025-12-31

   Simulation Result:
   ─────────────────────────────────────────────────
   Instrument:        RICE (v1)
   Amount:            100
   Attributes:        {grade: "A", region: "US"}

   Validation:        ✓ PASSED
   Bucket ID:         a1b2c3d4e5f6g7h8...

   Position Preview:
     - instrument_code: RICE
     - bucket_id:       a1b2c3d4e5f6g7h8...
     - amount:          100
     - dimension:       Commodity

   Existing Bucket:   250 units (would become 350 after deposit)
   ─────────────────────────────────────────────────

   # Simulate validation failure
   $ instrument-cli simulate --tenant abc123 \
       --instrument RICE --version 1 \
       --amount 100 \
       --attr grade=X \  # Invalid grade

   Simulation Result:
   ─────────────────────────────────────────────────
   Validation:        ✗ FAILED
   Errors:
     - Grade must be A, B, or C (got: X)
   ─────────────────────────────────────────────────
   ```

   > **Why CLI over just API**: The API tests expressions in isolation. The CLI simulates
   > the full pipeline: load instrument from cache, run validation, generate bucket key,
   > optionally lookup existing bucket balance. Users see exactly what would happen before
   > calling `RegisterInstrument`.

### Acceptance Criteria

- [ ] All CRUD endpoints functional (`Register`, `Update`, `Retrieve`, `List`)
- [ ] Lifecycle endpoints functional (`Activate`, `Deprecate`)
- [ ] `EvaluateInstrument` compiles and evaluates CEL expressions without persisting
- [ ] `EvaluateInstrument` returns compile errors, validation report, and bucket key
- [ ] `GetAttributeSchema` returns JSON Schema for client-side validation
- [ ] `RetrieveInstrument` includes `attribute_schema_json` in response
- [ ] Proper gRPC error codes returned
- [ ] Tenant context required and enforced
- [ ] Rate limiting implemented as `RateLimitInterceptor` (middleware, not handler)
- [ ] `RegisterInstrument` rate-limited to 10 requests/minute per tenant
- [ ] Rate limit exceeded returns `RESOURCE_EXHAUSTED` (gRPC code 8) with clear message
- [ ] Rate limiting does NOT apply to read-only endpoints
- [ ] **Simulation Mode**: `instrument-cli simulate` shows validation, bucket ID, and position preview
- [ ] **Simulation Mode**: Optionally shows existing bucket balance (requires DB access)

---

## Stream H: Caching Layer

**Location:** `services/reference-data/cache/`
**Dependencies:** Stream F (registry interface)

### Deliverables

1. **CachedInstrumentRegistry** wrapper with **compiled CEL programs**:

   ```go
   // CachedInstrument holds the definition and pre-compiled CEL programs
   type CachedInstrument struct {
       Definition        InstrumentDefinition
       ValidationProgram cel.Program  // For ingestion: attributes → bool
       BucketKeyProgram  cel.Program  // For bucketing: attributes → string (may be nil)
   }
   // Note: No FungibilityProgram - bucket key is sole determinant of fungibility

   // cachedEntry wraps CachedInstrument with timestamp for TTL enforcement
   type cachedEntry struct {
       instrument CachedInstrument
       cachedAt   time.Time
   }

   // cacheKey is a struct-based key to avoid delimiter collision risks.
   // Using a struct instead of fmt.Sprintf("%s:%s:%d") prevents edge cases
   // where tenant IDs or codes might contain the delimiter character.
   type cacheKey struct {
       TenantID uuid.UUID
       Code     string
       Version  uint32
   }

   type CachedInstrumentRegistry struct {
       delegate InstrumentRegistry
       compiler *CELCompiler
       cache    *lru.Cache[cacheKey, cachedEntry]  // Bounded LRU with struct key
       ttl      time.Duration
   }
   ```

   > **Why cache compiled programs**: CEL parsing/compilation is ~100μs. CEL execution is ~100ns.
   > By caching both `cel.Program` instances alongside the definition, we pay compilation once.
   >
   > **Why struct keys**: Avoids delimiter collision risks with string-based keys.

2. **Read-through caching with TTL enforcement**:

   ```go
   func (c *CachedInstrumentRegistry) GetDefinition(
       ctx context.Context, tenantID uuid.UUID, code string, version uint32,
   ) (CachedInstrument, error) {
       key := cacheKey{TenantID: tenantID, Code: code, Version: version}

       // Check cache with TTL validation (jitter applied here for thundering herd prevention)
       if entry, ok := c.cache.Get(key); ok {
           if time.Since(entry.cachedAt) < c.jitteredTTL() {
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

   // compileAndCache compiles CEL programs and stores with jittered TTL
   func (c *CachedInstrumentRegistry) compileAndCache(
       key cacheKey, def InstrumentDefinition,
   ) (CachedInstrument, error) {
       validationProg, err := c.compiler.CompileValidation(def.ValidationExpression)
       if err != nil {
           return CachedInstrument{}, fmt.Errorf("compile validation: %w", err)
       }
       bucketKeyProg, err := c.compiler.CompileBucketKey(def.FungibilityKeyExpression)
       if err != nil {
           return CachedInstrument{}, fmt.Errorf("compile bucket key: %w", err)
       }

       inst := CachedInstrument{
           Definition:        def,
           ValidationProgram: validationProg,
           BucketKeyProgram:  bucketKeyProg,
       }

       // Store with current timestamp - TTL checked on read via time.Since()
       // Jitter applied at validation time to prevent thundering herd
       c.cache.Add(key, cachedEntry{
           instrument: inst,
           cachedAt:   time.Now(),
       })

       return inst, nil
   }
   ```

   > **TTL Enforcement**: Cache entries store `cachedAt` timestamp. On read, we check
   > `time.Since(entry.cachedAt) < c.jitteredTTL()` - the jitter is applied at validation
   > time, not storage time, so each node's "view" of expiration differs slightly.

3. **Cache invalidation** (local + event-driven):

   ```go
   // Local invalidation: Clear cache on writes from this node
   func (c *CachedInstrumentRegistry) InvalidateOnWrite(
       tenantID uuid.UUID, code string, version uint32,
   ) {
       key := cacheKey{TenantID: tenantID, Code: code, Version: version}
       c.cache.Remove(key)
   }

   // Event-driven invalidation: Subscribe to instrument.updated events
   // This handles invalidation when OTHER nodes modify instruments
   func (c *CachedInstrumentRegistry) SubscribeToUpdates(ctx context.Context, consumer EventConsumer) {
       consumer.Subscribe("instrument.updated", func(event InstrumentUpdatedEvent) {
           c.cache.Remove(cacheKey{
               TenantID: event.TenantID,
               Code:     event.Code,
               Version:  event.Version,
           })
           log.Info().
               Str("code", event.Code).
               Uint32("version", event.Version).
               Msg("cache invalidated via event")
       })
   }
   ```

   > **Dual invalidation strategy**: Local writes invalidate immediately (synchronous).
   > Remote writes invalidate via event stream (eventual consistency, typically <100ms).
   > TTL provides the safety net for missed events.

4. **TTL Jitter** (Thundering Herd Prevention):

   ```go
   func (c *CachedInstrumentRegistry) jitteredTTL() time.Duration {
       // Base TTL: 5 minutes, Jitter: ±30 seconds
       // Prevents all nodes from refreshing cache simultaneously
       jitter := time.Duration(rand.Int63n(int64(60 * time.Second))) - 30*time.Second
       return c.ttl + jitter
   }
   ```

5. **Emergency Cache Purge** (Safety Valve):

   ```go
   // PurgeAll clears the entire cache - use for emergency recovery.
   // Exposed via admin API endpoint: POST /admin/cache/purge
   // Or trigger via pod restart (cache is in-memory only).
   func (c *CachedInstrumentRegistry) PurgeAll() {
       c.cache.Purge()
   }

   // PurgeInstrument clears a specific instrument from cache.
   // Use when a bad definition got cached and needs immediate eviction.
   func (c *CachedInstrumentRegistry) PurgeInstrument(tenantID uuid.UUID, code string, version uint32) {
       key := cacheKey{TenantID: tenantID, Code: code, Version: version}
       c.cache.Remove(key)
   }
   ```

   > **Recovery scenario**: If a malformed CEL expression passes compile-time checks but causes
   > runtime errors, operators can purge the bad definition from cache while fixing the DB record.

6. **Redis L2 Cache** (Cold Start Resilience):

   ```go
   // TieredCache implements Memory (L1) → Redis (L2) → gRPC (Source) lookup.
   // L2 provides resilience: if Reference Data is down AND pod restarts (cold L1),
   // Position Keeping can still process transactions from L2.
   type TieredCache struct {
       l1        *lru.Cache[cacheKey, cachedEntry]  // In-memory, fastest
       l2        *redis.Client                       // Persists across restarts
       source    InstrumentRegistry                  // Reference Data gRPC
       compiler  *CELCompiler
       l1TTL     time.Duration  // 5 minutes
       l2TTL     time.Duration  // 1 hour (survive Reference Data outages)
   }

   func (c *TieredCache) Get(
       ctx context.Context, tenantID uuid.UUID, code string, version uint32,
   ) (CachedInstrument, error) {
       key := cacheKey{TenantID: tenantID, Code: code, Version: version}
       redisKey := fmt.Sprintf("instrument:%s:%s:%d", tenantID, code, version)

       // L1: Memory (sub-microsecond)
       if entry, ok := c.l1.Get(key); ok {
           if time.Since(entry.cachedAt) < c.jitteredL1TTL() {
               return entry.instrument, nil
           }
           c.l1.Remove(key)
       }

       // L2: Redis (millisecond, survives pod restart)
       if data, err := c.l2.Get(ctx, redisKey).Bytes(); err == nil {
           var def InstrumentDefinition
           if proto.Unmarshal(data, &def) == nil {
               inst, _ := c.compileAndCacheL1(key, def)
               return inst, nil  // Skip source, use L2 data
           }
       }

       // Source: gRPC to Reference Data (singleflight)
       def, err := c.source.GetDefinition(ctx, tenantID, code, version)
       if err != nil {
           return CachedInstrument{}, err
       }

       // Populate both L1 and L2
       inst, _ := c.compileAndCacheL1(key, def)
       c.cacheL2(ctx, redisKey, def)
       return inst, nil
   }

   func (c *TieredCache) cacheL2(ctx context.Context, key string, def InstrumentDefinition) {
       data, _ := proto.Marshal(&def)
       c.l2.Set(ctx, key, data, c.l2TTL)
   }
   ```

   > **Why Redis L2**: Without L2, a Position Keeping pod restart during a Reference Data
   > outage would fail all transactions (cold L1, source unavailable). Redis survives pod
   > restarts, providing 1-hour resilience window. Uses existing Redis infrastructure
   > (already deployed for idempotency keys).
   >
   > **Invalidation**: L2 entries invalidated via same `instrument.updated` event stream.
   > TTL provides safety net. Stale L2 is acceptable: worst case is using old CEL rules
   > for up to 1 hour during an outage - better than transaction failures.

### Acceptance Criteria

- [ ] Cache hit returns pre-compiled CEL program
- [ ] TTL-based expiration works with jitter (4m30s - 5m30s range)
- [ ] Local writes invalidate cache entries synchronously
- [ ] Event subscription invalidates cache on remote writes (`instrument.updated`)
- [ ] `ValidateAttributes` uses cached `cel.Program` (no re-compilation)
- [ ] Admin API exposes cache purge endpoints
- [ ] Pod restart clears L1 cache (in-memory)
- [ ] **Redis L2**: Cold-start pod can serve transactions from L2 when Reference Data is unavailable
- [ ] **Redis L2**: Invalidation events propagate to both L1 and L2

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
//
// COLD CACHE STRATEGY: Uses singleflight to handle cache misses gracefully.
// 1. Prefetch on startup: Load all active instruments before accepting traffic
// 2. Background refresh: Subscribe to instrument updates via Kafka/gRPC stream
// 3. Cache miss = singleflight fallback: One request fetches, others wait
type LocalInstrumentCache struct {
    registry InstrumentRegistry              // Remote client (for fetch on miss)
    compiler *CELCompiler                    // For compiling fetched definitions
    cache    *lru.Cache[string, CachedInstrument]  // Bounded: max 10,000 entries
    ttl      time.Duration                   // Refresh interval (e.g., 5 minutes)
    sflight  singleflight.Group              // Deduplicates concurrent cache misses
}

// STARTUP SEQUENCE:
// 1. Connect to Reference Data service
// 2. Prefetch ALL instruments for this tenant (blocking)
// 3. Subscribe to instrument update stream (background)
// 4. Only then: start accepting traffic
//
// If prefetch fails, service should NOT start (fail-fast).

func (c *LocalInstrumentCache) Get(
    ctx context.Context, tenantID uuid.UUID, code string, version uint32,
) (CachedInstrument, error) {
    key := fmt.Sprintf("%s:%s:%d", tenantID, code, version)

    // Fast path: cache hit
    if cached, ok := c.cache.Get(key); ok {
        return cached, nil
    }

    // Slow path: singleflight fetch (one request fetches, others wait)
    // This solves the "cold cache rejection" problem for new instruments.
    result, err, _ := c.sflight.Do(key, func() (interface{}, error) {
        // Double-check: another goroutine may have populated cache
        if cached, ok := c.cache.Get(key); ok {
            return cached, nil
        }

        // Fetch from Reference Data service (gRPC call)
        def, err := c.registry.GetDefinition(ctx, tenantID, code, version)
        if err != nil {
            return nil, err  // Instrument truly doesn't exist
        }

        // Compile CEL programs
        cached, err := c.compileAndCache(key, def)
        if err != nil {
            return nil, err
        }

        return cached, nil
    })

    if err != nil {
        return CachedInstrument{}, err
    }
    return result.(CachedInstrument), nil
}
```

> **Singleflight Pattern**: When you deploy a new instrument "RICE-V2" and immediately
> send 10k transactions, the first request fetches from Reference Data (gRPC) while
> all others wait. Once cached, all 10k requests proceed. This eliminates the "flaky
> API" feeling without opening a DDoS vector (singleflight deduplicates concurrent requests).
>
> **Scenario Analysis**:
>
> - Cache hit: ~100ns (normal operation)
> - Cache miss (first request): ~5ms (gRPC round-trip)
> - Cache miss (concurrent requests): Wait for first request, then ~100ns
> - Unknown instrument: Returns error after gRPC confirms non-existence

### The Rehydration Pattern (Critical)

**Problem**: Database rows and Proto messages store instrument codes as strings. How do we safely
reconstruct `Quantity[D]` when loading, ensuring the dimension matches?

**Solution**: The Rehydration Pattern is a 4-step process at every persistence adapter boundary:

```go
// Persistence Adapter: Loading a position from the database
func (a *PostgresAdapter) LoadPosition(ctx context.Context, id uuid.UUID) (any, error) {
    // 1. Read row: Amount (decimal) + InstrumentCode + Version + Attributes
    row, err := a.queries.GetPosition(ctx, id)
    if err != nil {
        return nil, err
    }

    // 2. Hot path lookup: Get cached instrument (includes dimension)
    cached, err := a.cache.Get(ctx, row.TenantID, row.InstrumentCode, row.Version)
    if err != nil {
        return nil, fmt.Errorf("unknown instrument %s (v%d): %w", row.InstrumentCode, row.Version, err)
    }

    // 3. Dimension check: Validate dimension BEFORE instantiating generic type
    // This prevents type confusion attacks and data corruption
    inst := cached.Definition.ToInstrument()

    // 4. Instantiate via bridge: Runtime string → Compile-time type
    return quantity.ParseQuantity(row.Amount, inst)
}
```

**Type-Safe Alternative** (when caller knows expected dimension):

```go
// When the calling code KNOWS it expects Monetary (e.g., Current Account balance)
func (a *PostgresAdapter) LoadMonetaryPosition(ctx context.Context, id uuid.UUID) (quantity.Money, error) {
    row, err := a.queries.GetPosition(ctx, id)
    if err != nil {
        return quantity.Money{}, err
    }

    cached, err := a.cache.Get(ctx, row.TenantID, row.InstrumentCode, row.Version)
    if err != nil {
        return quantity.Money{}, err
    }

    inst := cached.Definition.ToInstrument()

    // NewQuantity validates dimension matches type parameter
    return quantity.NewQuantity[quantity.Monetary](row.Amount, inst)
}
```

**Key Insight**: The rehydration boundary is the **adapter layer**, not the domain layer.
Domain code receives fully-typed `Quantity[D]` values. Adapters handle the runtime→compile-time bridge.

---

### Stream I.1: Position Keeping Integration

**Location:** `services/position-keeping/`
**Developer allocation:** 2

#### Scope

Position Keeping is the **primary consumer** of multi-asset quantities. Changes span:

| Layer | Files | Changes |
|-------|-------|---------|
| Domain | `domain/money.go` | Replace with `domain/quantity.go` re-exporting `shared/platform/quantity` |
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

       // 3. Generate bucket key
       bucketID, err := a.cel.GenerateBucketKey(cached.BucketKeyProgram, req.Amount.Attributes)
       if err != nil {
           return status.Errorf(codes.InvalidArgument, "bucket key generation failed: %v", err)
       }

       // 4. CARDINALITY GUARD: Prevent "Infinite Buckets" DOS attack
       // If a tenant buckets by serial_number, every position gets unique bucket.
       // This defeats aggregation, bloats indexes, and creates O(N) reads.
       bucketCount, err := a.repo.CountDistinctBuckets(ctx, tenantID, req.AccountID, req.Amount.InstrumentCode)
       if err != nil {
           return status.Errorf(codes.Internal, "bucket count failed: %v", err)
       }
       if bucketCount >= MaxBucketsPerAccountInstrument {
           return status.Errorf(codes.ResourceExhausted,
               "bucket limit exceeded: account has %d buckets for instrument %s (max: %d)",
               bucketCount, req.Amount.InstrumentCode, MaxBucketsPerAccountInstrument)
       }

       // 5. Type bridge
       qty, err := quantity.ParseQuantity(amount, cached.Definition.ToInstrument())
       if err != nil {
           return status.Errorf(codes.Internal, "type bridge failed: %v", err)
       }

       // 6. Domain entry
       return a.service.RecordMeasurement(ctx, qty, req.Amount.Attributes, bucketID)
   }

   // Cardinality limits to prevent "Infinite Buckets" DOS
   const (
       MaxBucketsPerAccountInstrument = 10000  // Hard limit - reject above this
       BucketCardinalityAlertThreshold = 1000  // Soft limit - emit metric/alert
   )
   ```

4. **Database migration**: Add new columns to positions table:
   - `attributes JSONB`
   - `bucket_id VARCHAR(256)`
   - `dimension VARCHAR(32)`

   > The `dimension` column enables reading positions without Reference Data service dependency.
   > See Stream E.2 for rationale on read availability decoupling.

5. **Read-Side Aggregation** (Simple, No Side Effects):

   Since we use **Append-Only** writes, buckets accumulate fragments over time. Reads simply
   sum in memory - Go is fast enough to sum 1,000 decimals in microseconds.

   ```go
   // Read path: Just sum in memory. No side effects, no compaction triggers.
   func (s *Service) GetAggregatedPosition(
       ctx context.Context, tenantID uuid.UUID, accountID, instrumentCode, bucketID string,
   ) (AggregatedPosition, error) {
       rows, err := s.repo.FindByBucket(ctx, accountID, instrumentCode, bucketID)
       if err != nil {
           return AggregatedPosition{}, err
       }

       // Simple in-memory sum. Even 1,000 rows completes in < 1ms.
       // Do NOT trigger side effects (compaction) from reads - this couples
       // read availability to write load and creates DOS vectors.
       return s.sumPositions(rows), nil
   }
   ```

   > **Why no read-triggered compaction?** Triggering async work from reads is dangerous:
   >
   > - Couples read availability to write load (fragmented accounts slow down reads)
   > - A DOS attack that fragments buckets spawns runaway goroutines/kafka messages
   > - Creates unpredictable latency spikes on read path
   >
   > **Solution**: Compaction runs as a **standalone background worker** that scans for
   > fragmented buckets independently of read traffic. See `CompactionWorker` below.

6. **Background Compaction Worker** (Decoupled from Read Path):

   ```go
   // CompactionWorker runs on a schedule, NOT triggered by reads.
   // Scans for buckets with high fragment counts and compacts them.
   type CompactionWorker struct {
       repo      PositionRepository
       interval  time.Duration  // e.g., every 5 minutes
       threshold int            // e.g., 100 rows per bucket
   }

   func (w *CompactionWorker) Run(ctx context.Context) {
       ticker := time.NewTicker(w.interval)
       for {
           select {
           case <-ctx.Done():
               return
           case <-ticker.C:
               w.compactFragmentedBuckets(ctx)
           }
       }
   }

   func (w *CompactionWorker) compactFragmentedBuckets(ctx context.Context) {
       // Find buckets with > threshold rows
       fragmented, _ := w.repo.FindFragmentedBuckets(ctx, w.threshold)
       for _, bucket := range fragmented {
           w.compactBucket(ctx, bucket)
       }
   }
   ```

   > **Phase 1 Note**: Background worker can be a simple goroutine. Production deployments
   > should use a proper job scheduler with distributed locking.

7. **Re-bucketing Tool** (CATASTROPHIC RECOVERY ONLY):

   > ⚠️ **LEDGER INTEGRITY WARNING**: Rebucketing violates the immutable ledger principle.
   > Rewriting `bucket_id` on existing rows breaks the link between original Write and
   > current Read. If you have backups or CDC streams, they will diverge from live data.
   >
   > **Preferred Solution: Migration-as-Trade**
   > Instead of rebucketing, create a new instrument version and migrate:
   > 1. Create `Rice(v2)` with corrected `fungibility_key_expression`
   > 2. Debit from `Rice(v1)` positions (zeroing them out)
   > 3. Credit to `Rice(v2)` positions (with correct bucketing)
   > 4. This creates an auditable "trade" from v1 → v2
   >
   > **When to use Rebucketing** (catastrophic recovery only):
   > - Definition was wrong from day 0 AND no settlements have occurred
   > - Data corruption recovery where audit trail is already broken
   > - Development/staging environment reset
   >
   > **Do NOT use for**: Normal expression fixes, gradual migrations, production corrections.

   ```go
   // RebucketingTool rebuilds position bucket assignments for an instrument.
   // ⚠️ CATASTROPHIC RECOVERY ONLY - violates immutable ledger principle.
   // Prefer Migration-as-Trade pattern for normal corrections.
   // ADMIN-ONLY: Requires explicit authorisation and audit logging.
   type RebucketingTool struct {
       measurementStore MeasurementStore  // Raw measurements with original attributes
       positionStore    PositionStore
       settlementStore  SettlementStore   // To check settlement locks
       celCompiler      *CELCompiler
   }

   // Rebucket recalculates bucket_id for all positions of an instrument.
   // Prerequisites:
   // 1. Raw measurements with original attributes must be preserved (not compacted away)
   // 2. Instrument must be DEPRECATED (prevents new writes during rebucketing)
   // 3. New instrument version (N+1) must exist with corrected expression
   // 4. NO positions can be included in a FINALIZED settlement run (settlement lock)
   func (t *RebucketingTool) Rebucket(
       ctx context.Context,
       tenantID uuid.UUID,
       instrumentCode string,
       fromVersion, toVersion uint32,
   ) (*RebucketingReport, error) {
       // 0. SETTLEMENT LOCK CHECK: Cannot rebucket settled history
       // Positions included in finalised settlements are immutable for audit/regulatory reasons
       settledPositions, err := t.settlementStore.FindSettledPositions(
           ctx, tenantID, instrumentCode, fromVersion,
       )
       if err != nil {
           return nil, fmt.Errorf("settlement check failed: %w", err)
       }
       if len(settledPositions) > 0 {
           return nil, &SettlementLockError{
               InstrumentCode: instrumentCode,
               Version:        fromVersion,
               SettledCount:   len(settledPositions),
               Message: fmt.Sprintf(
                   "cannot rebucket: %d positions included in finalised settlements",
                   len(settledPositions),
               ),
           }
       }

       // 1. Load new expression from Version N+1
       newDef, err := t.registry.GetDefinition(ctx, tenantID, instrumentCode, toVersion)
       if err != nil {
           return nil, fmt.Errorf("target version not found: %w", err)
       }
       bucketProg, err := t.celCompiler.CompileFungibility(newDef.FungibilityKeyExpression)
       if err != nil {
           return nil, fmt.Errorf("failed to compile new expression: %w", err)
       }

       // 2. Stream all raw measurements for old version
       measurements, err := t.measurementStore.StreamByInstrumentVersion(
           ctx, tenantID, instrumentCode, fromVersion,
       )
       if err != nil {
           return nil, err
       }

       // 3. Rebuild positions with new bucket_id
       var rebuilt, failed int
       for m := range measurements {
           newBucketID, err := t.celCompiler.GenerateBucketKey(bucketProg, m.Attributes)
           if err != nil {
               failed++
               continue  // Log and continue, don't abort entire operation
           }

           err = t.positionStore.UpdateBucketID(ctx, m.PositionID, newBucketID, toVersion)
           if err != nil {
               failed++
               continue
           }
           rebuilt++
       }

       return &RebucketingReport{
           Rebuilt: rebuilt,
           Failed:  failed,
       }, nil
   }

   // SettlementLockError returned when attempting to rebucket settled positions.
   // Settled positions are immutable - you cannot rewrite financial history.
   type SettlementLockError struct {
       InstrumentCode string
       Version        uint32
       SettledCount   int
       Message        string
   }

   func (e *SettlementLockError) Error() string {
       return e.Message
   }
   ```

   > **Critical Requirement**: Raw measurements with original attributes **MUST** be preserved.
   > Compaction that discards attributes makes rebucketing impossible.
   >
   > **Workflow**:
   > 1. Deprecate `Rice(v1)` to stop new transactions
   > 2. Create `Rice(v2)` with corrected `fungibility_key_expression`
   > 3. Run `Rebucket(tenant, "RICE", v1, v2)` to migrate positions
   > 4. Activate `Rice(v2)` when rebucketing completes

#### Acceptance Criteria

- [ ] `RecordMeasurement` accepts multi-asset instruments
- [ ] CEL validation rejects invalid attributes before domain entry
- [ ] `SafeParseLib` macros available in CEL environment (`parse_iso_date`, `parse_int`, etc.)
- [ ] `bucket_key()` function generates hash-based keys (prevents delimiter injection)
- [ ] Bucket ID generated from `fungibility_key_expression` using `bucket_key()`, stored with position
- [ ] Cardinality guard rejects requests when bucket count exceeds limit (10,000 per account/instrument)
- [ ] `GetAggregatedPosition` uses `GROUP BY bucket_id` for O(log N) lookup
- [ ] Background `CompactionWorker` consolidates fragmented buckets (NOT on read path)
- [ ] Existing fiat-only tests still pass (backwards compatible)
- [ ] No gRPC calls on hot path (cache hit rate > 99%)
- [ ] Re-bucketing tool can rebuild bucket assignments from raw measurements
- [ ] Re-bucketing tool fails with `SettlementLockError` if positions are in finalised settlements
- [ ] Raw measurements preserve original attributes (not discarded during compaction)

**Phase 1 Append-Only Enforcement** (Critical):

- [ ] All write APIs (`RecordMeasurement`, `CreatePosition`) use INSERT only, never UPDATE
- [ ] No write-time position merging: requests that would merge positions create new rows instead
- [ ] `UpdatePosition` and `MergePositions` endpoints return `UNIMPLEMENTED` in Phase 1
- [ ] Database constraints prevent UPDATE on position amount columns (trigger or policy)
- [ ] Integration tests verify: 100 writes to same account = 100 rows (no consolidation)
- [ ] Position consolidation occurs ONLY via offline background job (not on hot write path)
- [ ] Background compaction job is documented but NOT required for Phase 1 MVP

**Append-Only Enforcement Mechanisms** (Code-level):

- [ ] `PositionRepository.Insert()` is the ONLY write method; no `Update()` or `Upsert()` exists
- [ ] Service layer has no `MergeOnWrite` option or similar configuration
- [ ] Application-layer enforcement rejects UPDATE on `amount` column:

  > **CockroachDB note:** PL/pgSQL triggers are not supported. Append-only enforcement
  > is implemented in `PositionRepository` which has no `Update()` method, and validated
  > via code review checklist.

- [ ] Code review checklist includes "No UPDATE on positions table" verification

> **Rationale**: Append-only writes achieve O(1) constant time without locks. Write-time merging
> requires per-bucket locking which creates bottlenecks on hot accounts at 100k+ TPS. Position
> aggregation happens at read-time (cacheable) or via background compaction during low-traffic windows.

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

4. **Dimensional Liens** (CRITICAL for bucket-aware accounts):

   The `Lien` entity **must** store `bucket_id` for commodity accounts with attributes.
   When `InitiateLien` is called, the service must:

   ```go
   func (s *Service) InitiateLien(ctx context.Context, req LienRequest) (*Lien, error) {
       // 1. Load the Instrument Definition
       cached, err := s.cache.Get(ctx, req.TenantID, req.InstrumentCode, req.Version)
       if err != nil {
           return nil, err
       }

       // 2. Execute fungibility_key_expression on the request attributes
       bucketID, err := s.cel.GenerateBucketKey(cached.BucketKeyProgram, req.Attributes)
       if err != nil {
           return nil, fmt.Errorf("cannot generate bucket key for lien: %w", err)
       }

       // 3. Store the resulting bucket_id on the Lien
       lien := &Lien{
           AccountID:      req.AccountID,
           InstrumentCode: req.InstrumentCode,
           BucketID:       bucketID,  // CRITICAL: Must match position bucket
           Amount:         req.Amount,
       }

       // 4. Validate solvency against the SPECIFIC bucket balance
       available := s.getAvailableBalance(ctx, req.AccountID, req.InstrumentCode, bucketID)
       // Available = Sum(Positions where bucket=X) - Sum(Liens where bucket=X)
       if available.LessThan(req.Amount) {
           return nil, ErrInsufficientFunds
       }

       return s.repo.CreateLien(ctx, lien)
   }
   ```

   > **Why bucket-aware liens?** A user with 100 units of `RICE` in `bucket_id="grade_a"` and
   > 50 units in `bucket_id="grade_b"` must not be able to lien 150 units of "Grade A Rice".
   > The lien must lock the *specific bucket*, not just the instrument total.
   >
   > **Phase 1 Scope**: Liens are strictly single-bucket. If a user wants to reserve "Any valid
   > electricity" (regardless of source=solar vs source=wind), the upstream Payment Order service
   > must query available buckets and issue specific Lien requests. Current Account does not
   > support "multi-bucket liens" - this avoids complexity in the solvency check.

#### Acceptance Criteria

- [ ] Account creation accepts instrument code (not just currency string)
- [ ] Deposits/withdrawals validated against Reference Data
- [ ] Balance queries return `InstrumentAmount` in proto responses
- [ ] **Lien entity stores `bucket_id`** (for commodity accounts with attributes)
- [ ] **Solvency check validates against specific bucket balance**, not total instrument balance
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
- [ ] **High-Cardinality Bucket Test**: Verify that CEL key generation + DB index lookup
      remains under 10ms even with 1 million distinct buckets (attribute combinations)
- [ ] **Bucket Aggregation Test**: Verify `GROUP BY bucket_id` aggregation with 10,000 positions
      across 100 buckets completes under 50ms
- [ ] **GC Pressure Load Test**: Sustained 100k TPS with pooled `AttributeBag` allocations.
      Measure P99 latency AND GC pause times (not just throughput). Target: <10ms P99, <50ms GC pauses.
      Verify pool reuse rate >95% (most requests should reuse, not allocate).
- [ ] **CEL Determinism Test**: Same attributes in different order must produce identical `bucket_id`.
      Run `bucket_key(['region', 'grade'])` with attributes `{region: "US", grade: "A"}` 10,000 times
      across multiple goroutines. Every result must be identical. Fail if ANY divergence detected.
- [ ] **CEL Version Regression Test**: Store expected `bucket_id` values for known attribute sets.
      After any cel-go upgrade, re-run and compare. Block deployment if hashes change.
- [ ] **Poison Pill CEL Test**: Tenant defines a valid but computationally expensive CEL expression
      (e.g., `size([1,2,3,4,5,6,7,8,9,10].map(x, [1,2,3,4,5,6,7,8,9,10].map(y, x*y)).flatten()) > 0`).
      Verify `CostLimit(10000)` aborts evaluation instantly and returns clear error to gRPC client.
      Must NOT hang the gRPC handler or leak goroutines.

---

## Parallel Execution Summary

| Stream | Can Start After | Developers | Service |
|--------|-----------------|------------|---------|
| A: Core Types | Immediately | 2 | `shared/platform/quantity` |
| B: Currency | A | 1 | `shared/platform/quantity/currency` |
| C: Rate Type | A | 1 | `shared/platform/quantity` |
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

## Integration Coordination Strategy

With 10 parallel streams touching 6+ services, late-discovery integration failures are a significant
risk. This section defines guardrails to catch misalignment early.

### Integration Coordinator Role

Assign one person as **Integration Coordinator** with responsibilities:

- Own cross-stream interface contracts (proto definitions, Go interfaces)
- Run integration smoke tests on each dependency resolution (not just unit tests)
- Triage integration failures with priority over feature work
- Approve any changes to shared contracts (proto, `shared/platform/quantity`)

### Dependency-Based Integration Gates

Integration validation occurs at dependency boundaries, not calendar dates:

| Gate | Trigger | Exit Criteria |
|------|---------|---------------|
| **Contract Freeze** | Before any dependent stream starts | All proto definitions and Go interfaces finalised |
| **Foundation** | Streams A, D, E all complete | Shared types compile together; no interface conflicts |
| **Service Layer** | Stream F complete | Registry passes tests with real DB; CEL compilation works |
| **API Layer** | Streams F, G, H complete | End-to-end flow works with mock tenant |
| **Integration** | All I.x streams complete | Cross-service calls validated |

### Interface Contract Rules

1. **Proto definitions are immutable after Contract Freeze** - use `reserved` fields, not modifications
2. **Go interfaces in `shared/platform/quantity` require coordinator approval** to change
3. **Database schemas require migration compatibility** - no breaking changes to existing columns
4. **Cache key formats are contracts** - changes require cache flush coordination

### Early Warning Signals

| Signal | Response |
|--------|----------|
| Stream blocked on another stream's API | Escalate to coordinator; consider interface stub |
| Integration test fails after dependency resolves | Stop downstream work; fix integration first |
| Contract change requested after freeze | Require written justification and impact analysis |
| >3 streams modifying same file | Architectural review needed |

### Escape Hatch

If API Layer gate shows >2x expected integration complexity:

1. Pause downstream streams (I.x)
2. Assess: Can we reduce to 5 streams (vertical slices)?
3. Consider: Defer Asset Catalogue (streams 11-12 in F) to Phase 2
4. Replan with reduced scope if necessary

> **Goal**: Catch integration issues at dependency boundaries, not during final Stream J.

---

## Decisions Made

| Question | Decision |
|----------|----------|
| Service naming | `reference-data` (BIAN: FinancialInstrumentReferenceDataManagement) |
| System Tenant ID | `00000000-0000-4000-8000-000000000001` |
| Lookup inheritance | Tenant → System Tenant fallback |
| Valuation scope | Rate struct only; ValuationProvider deferred to future PRD |
| Attribute validation | CEL `validation_expression` - 100x faster than JSON Schema |
| Fungibility pattern | **Bucket key only** - `fungibility_key_expression` is sole determinant (no pairwise check) |
| Position aggregation | Pure SQL: `GROUP BY bucket_id` - no CEL on read path |
| Arithmetic | Native Go `decimal.Decimal` - CEL never in hot loop |
| Local cache type | Bounded LRU (`hashicorp/golang-lru`) to prevent memory leaks |
| CEL caching | Cache compiled programs (validation + bucket key) |
| Cache miss strategy | Prefetch on startup; cache miss = singleflight fallback (one request fetches, others wait) |
| Read-side coalescing | Trigger compaction when bucket exceeds 100 rows |
| Integration strategy | Per-service streams (I.1-I.4) for parallel execution |
| Shared package | New `shared/platform/quantity`; deprecate `shared/domain/money` after migration |

---

## Open Questions

1. **Initial commodity catalogue**: Which non-currency instruments should be seeded (if any)?
2. **Cache TTL**: What's the acceptable staleness window for instrument definitions? (Proposed: 5 min)

---

## Success Metrics

- [ ] All streams completed and merged
- [ ] Custom instrument creation works end-to-end
- [ ] System Tenant inheritance works (any tenant can use "USD")
- [ ] No compile-time dimensional safety regressions
- [ ] Reference Data lookup p99 < 10ms (with service-level caching)
- [ ] Position Keeping cache hit rate > 99% (with local caching)
