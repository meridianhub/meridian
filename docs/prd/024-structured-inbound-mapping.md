---
name: prd-structured-mapping-layer
description: Bidirectional JSON mapping engine with unified property model across Party, Reference Data, and Market Data
triggers:
  - Implementing external JSON format mapping or transformation
  - Building tenant-configurable field mappings or enum translations
  - Working on inbound or outbound data transformation at the gateway
  - Adding structured attributes or CEL validation to Party service
  - Integrating external systems that send non-proto JSON formats
  - Working on mapping definitions in the tenant manifest
  - Questions about FieldCorrespondence, MappingDefinition, or DryRunMapping
  - Batch JSON array ingestion for market data or observations
instructions: |
  Two phases. Phase 1: Unified Property Model — extract CEL compiler
  to shared/pkg/cel/, add repeated AttributeEntry + JSON Schema +
  CEL validation to Party (matching Reference Data and Market Data
  patterns), add party_types to tenant manifest. Phase 2: Bidirectional
  Mapping — single MappingDefinition with FieldCorrespondence
  (external_path/internal_path), auto-reversible transforms (enum,
  date, attribute flatten), explicit CelTransform (inbound_cel +
  outbound_cel), IdempotencyConfig for dedup, DryRunMapping RPC,
  gateway middleware at /mapping/{name}, manifest integration. CRUD
  in services/reference-data/, engine middleware in services/api-gateway/.
  Key deps: tidwall/gjson (add to go.mod), hashicorp/golang-lru/v2
  (already present). All CEL expressions bounded: max 4096 bytes,
  cost limit 10,000, guaranteed termination.
---

# PRD: Structured Mapping Layer

**Status:** Implemented
**Version:** 1.0
**Date:** 2026-02-21
**Author:** Architecture Team
**Task Master Tag:** `structured-mapping-layer`

## Problem Statement

External systems send JSON in their own formats — a bank's party payload
looks different from an energy retailer's, which looks different from a
carbon registry's. Today, every integration must manually conform to
Meridian's exact proto-derived JSON structure (`camelCase` field names,
specific enum strings, nested message shapes). This creates friction:
integrators must read proto definitions to understand the expected
format, and any structural mismatch causes opaque validation failures.

Meanwhile, Meridian's three domains handle flexibility **inconsistently**:

| Domain | Attributes | Schema | Validation | Computed |
|--------|-----------|--------|------------|----------|
| Reference Data | `repeated AttributeEntry` | JSON Schema (string) | CEL | Fungibility keys |
| Market Data | `repeated AttributeEntry` | JSON Schema (Struct) | CEL | Resolution keys |
| Party | None (fixed fields only) | None | buf.validate only | None |

Party is the outlier. Reference Data and Market Data both support
tenant-configurable schemas, CEL-based validation, and computed keys.
Party has none of this — custom data lives in unvalidated
`google.protobuf.Struct` metadata on associations only.

The Vanguard transcoder (tasks 1-10) solved **protocol transcoding**
(HTTP/JSON to gRPC). This PRD addresses two layers above that:

1. **Consistent property model** — bring Party in line with Reference
   Data and Market Data
2. **Bidirectional mapping** — a single definition that transforms
   external JSON to internal proto (inbound) and internal proto back
   to external JSON (outbound), without duplicating logic

## Technical Context

### What Exists Today

**Protocol Layer** (solved by gateway-json-transcoding):

- Vanguard transcodes REST/JSON to gRPC proto using `google.api.http`
  annotations
- Handles Content-Type negotiation (JSON, Connect, gRPC-Web)
- Identity headers propagate as gRPC metadata

**Schema Infrastructure** (Reference Data + Market Data):

- `attribute_schema` (JSON Schema, max 16KB) — defines valid attributes
  per entity type
- CEL compiler (`services/reference-data/cel/compiler.go`) with:
  - Validation environment: `attributes`, `amount`, `valid_from`,
    `valid_to`, `source`
  - Bucket key environment: `attributes` + `bucket_key()` function
  - Eligibility environment: `party`, `attributes`
  - Custom functions: `parse_iso_date`, `parse_int`, `parse_decimal`,
    `parse_bool`, `bucket_key`
  - Security: max 4096 bytes, max 10 depth, cost limit 10,000
- `repeated AttributeEntry` for deterministic attribute ordering
- Schema discovery RPCs (`GetAttributeSchema`)

**Party Service** (the gap):

- Fixed proto fields: `legal_name`, `display_name`, `party_type`,
  `status`, `external_reference`
- `google.protobuf.Struct metadata` on `Association` only — unvalidated,
  no schema, no CEL
- No `AttributeEntry`, no `attribute_schema`, no computed fields
- Persistence: simple GORM entity with string columns, no JSON column

### What's Missing

1. **Party extensibility**: No way to define tenant-specific party
   attributes with validation (e.g., "tier", "risk_rating", "kyc_level")
2. **Consistent schema validation**: Party has none; Reference Data and
   Market Data have CEL but with slightly different patterns
3. **Inbound transformation**: No way to map external JSON formats to
   internal proto structures
4. **Outbound transformation**: No way to map internal proto responses
   back to partner-specific JSON formats
5. **Format discovery**: No way for integrators to ask "what JSON shape
   does tenant X expect?"

## Solution: Two-Phase Mapping Layer

### Phase 1: Unified Property Model

Bring Party into consistency with Reference Data and Market Data by
adding structured attribute support and CEL validation.

### Phase 2: Bidirectional Mapping

A single `MappingDefinition` describes the correspondence between
external and internal fields. The engine runs it **forward** for
inbound requests and **backward** for outbound responses — no
duplicate definitions needed.

---

## Phase 1: Unified Property Model

### Goal

Eliminate the inconsistency table above. After Phase 1:

| Domain | Attributes | Schema | Validation | Computed |
|--------|-----------|--------|------------|----------|
| Reference Data | `repeated AttributeEntry` | JSON Schema | CEL | Fungibility keys |
| Market Data | `repeated AttributeEntry` | JSON Schema | CEL | Resolution keys |
| Party | `repeated AttributeEntry` | JSON Schema | CEL | Eligibility keys |

### Changes to Party Proto

Add to `Party` message in `api/proto/meridian/party/v1/party.proto`:

```protobuf
// Tenant-configurable structured attributes
repeated meridian.quantity.v1.AttributeEntry attributes = N;
```

Add to `PartyType` definition (or a new `PartyTypeDefinition` message):

```protobuf
message PartyTypeDefinition {
  string party_type = 1;          // e.g., "PERSON", "ORGANIZATION"
  string tenant_id = 2;
  string attribute_schema = 3;    // JSON Schema (max 16KB)
  string validation_cel = 4;      // CEL: cross-field validation
  string eligibility_cel = 5;     // CEL: account type eligibility
  string error_message_cel = 6;   // CEL: custom error messages
}
```

### Changes to Party Persistence

Add a JSONB column to the party entity for attributes:

```sql
ALTER TABLE party ADD COLUMN attributes JSONB DEFAULT '[]';
```

Store `repeated AttributeEntry` as a JSON array, consistent with how
Reference Data and Market Data persist attributes.

### CEL Compiler Reuse

The existing CEL compiler in `services/reference-data/cel/compiler.go`
already supports three environments (validation, bucket key,
eligibility). Party reuses the **eligibility environment** which already
exposes `party` and `attributes` variables.

No new CEL functions needed — `parse_iso_date`, `parse_int`,
`parse_decimal`, `parse_bool` cover Party's needs.

### Migration Path

1. Add `PartyTypeDefinition` proto + CRUD RPCs to Party service
2. Add `attributes` field to `Party` proto and persistence
3. Extract CEL compiler to `shared/pkg/cel/` (hard prerequisite)
4. Validate attributes against schema at `RegisterParty` /
   `UpdateParty` time
5. Keep existing `Association.metadata` (Struct) for backwards
   compatibility — deprecate over time

### Phase 1 Scope

| Item | Points |
|------|--------|
| PartyTypeDefinition proto + CRUD | 3 |
| Party attributes field + migration | 3 |
| Extract CEL compiler to `shared/pkg/cel/` + Party integration | 3 |
| Schema validation at registration | 3 |
| Manifest: add `party_types` + differ/planner | 3 |
| Tests + reference party type definitions | 2 |
| **Total** | **17** |

---

## Phase 2: Bidirectional Mapping

### Core Concept

A **MappingDefinition** is a single, bidirectional definition that
describes the correspondence between external and internal fields.
The mapping engine runs it **forward** (external to proto) for inbound
requests and **backward** (proto to external) for outbound responses.

Most transforms are naturally reversible:

| Transform | Inbound | Outbound |
|-----------|---------|----------|
| Field rename | Read external, write internal | Read internal, write external |
| Enum mapping | Look up external key | Invert: look up internal key |
| Date format | Parse external format to RFC3339 | Format RFC3339 to external |
| Attribute flatten | Collect keys into `AttributeEntry[]` | Unflatten to flat keys |
| Default value | Apply if source missing | Skip (inbound-only) |
| CEL expression | **Not reversible** — needs explicit pair | See `CelTransform` below |

For the one non-reversible transform (CEL), we provide explicit
`inbound_cel` and `outbound_cel` fields. This means **one mapping
definition handles both directions** — no duplicate definitions, no
pairing logic, no version synchronization problem.

### Routing Strategy

Mapped requests use a **dedicated path**:

```text
POST /mapping/{mapping_name}
```

The gateway:

1. Extracts `mapping_name` from the URL path
2. Resolves the mapping: look up by name (tenant-scoped), always use
   the **latest ACTIVE version**. If no ACTIVE version exists, return
   404. Version override via `X-Mapping-Version` header is supported
   but optional.
3. **Inbound**: transforms the request body (external to proto)
4. Rewrites the request internally to the `target_service`/`target_rpc`
5. Forwards the proto-conformant JSON to Vanguard
6. **Outbound**: transforms the response body (proto to external)
7. Returns the partner-format JSON to the caller

This avoids intercepting standard gRPC paths (e.g., `/v1/party`) and
supports multiple mappings per RPC — a tenant can have
`bank-x-party-onboarding` and `bank-y-party-onboarding` targeting the
same `RegisterParty` with different field layouts.

**Cache key**: `{tenant_id}:{mapping_name}:{version}`. When no explicit
version is provided, the gateway resolves latest ACTIVE and caches
under the resolved version.

### Service Ownership

Mapping CRUD lives in **`services/reference-data/`**. Mappings are
metadata about how to interpret data, analogous to Instrument
Definitions and Attribute Schemas.

The **Mapping Engine middleware** lives in `services/api-gateway/` — it
intercepts `/mapping/` requests, resolves the mapping definition,
applies transforms in both directions, and forwards to Vanguard.

**Hard prerequisite (Phase 1)**: The CEL compiler must be extracted
from `services/reference-data/cel/` to **`shared/pkg/cel/`** before
Phase 2 begins. Both the Party service (Phase 1) and the mapping
engine in the gateway (Phase 2) need CEL compilation. Without
extraction, the gateway would import the reference-data service
package — creating a circular dependency. This extraction is
scoped as part of Phase 1's CEL compiler integration work.

### Data Model

```protobuf
// api/proto/meridian/mapping/v1/mapping.proto

enum MappingStatus {
  MAPPING_STATUS_UNSPECIFIED = 0;
  MAPPING_STATUS_DRAFT = 1;
  MAPPING_STATUS_ACTIVE = 2;
  MAPPING_STATUS_DEPRECATED = 3;
}

message MappingDefinition {
  string id = 1;
  string tenant_id = 2;
  string name = 3;              // "bank-x-party-onboarding"
  string target_service = 4;    // "meridian.party.v1.PartyService"
  string target_rpc = 5;        // "RegisterParty"
  string version = 6;           // Semver for safe evolution
  MappingStatus status = 7;

  // Schema of expected external JSON (for discovery/docs)
  string external_schema = 8;   // JSON Schema

  // Bidirectional field correspondences
  repeated FieldCorrespondence fields = 9;

  // Direction-specific computed fields
  repeated ComputedField inbound_computed_fields = 10;
  repeated ComputedField outbound_computed_fields = 11;

  // Direction-specific validation
  string inbound_validation_cel = 12;
  string outbound_validation_cel = 13;

  // Metadata
  google.protobuf.Timestamp created_at = 14;
  google.protobuf.Timestamp updated_at = 15;

  // Batch support (inbound only)
  bool is_batch = 16;           // Input is JSON array
  string batch_target_path = 17; // Wrap array at this path

  // Idempotency derivation rules
  IdempotencyConfig idempotency = 18;
}
```

#### Idempotency

External systems (dumb pipes) often retry requests without internal
request IDs, risking duplicate records. The `IdempotencyConfig` lets
the mapping derive an idempotency key from the request itself, so the
gateway can deduplicate retries before they reach the gRPC service.

```protobuf
message IdempotencyConfig {
  // Extract from header or body path
  // e.g., "header:X-Request-ID" or "body:transaction_ref"
  string source_selector = 1;

  // Derive key from content hash (e.g., hash of meter_id + timestamp)
  bool use_content_hash = 2;
  repeated string content_hash_fields = 3;
}
```

**Resolution order**:

1. If `source_selector` is set and the value exists in the request,
   use it as the idempotency key
2. If `use_content_hash = true`, hash the specified
   `content_hash_fields` (post-mapping values) to derive the key
3. If neither is configured, no deduplication is applied (passthrough)

The derived key is passed as gRPC metadata to the target service,
which uses its existing idempotency infrastructure to detect
duplicates. For batch requests (`is_batch = true`), the key is
derived **per element** using the element's field values.

**Inbound execution order**: `fields` (forward, sequentially) then
`inbound_computed_fields` (sequentially) then `inbound_validation_cel`
(must return `true`) then `idempotency` (derive key from mapped
values).

**Outbound execution order**: `fields` (reverse, sequentially) then
`outbound_computed_fields` (sequentially) then
`outbound_validation_cel` (must return `true`).

```protobuf
message FieldCorrespondence {
  string external_path = 1;       // gjson path: "govt_id"
  string internal_path = 2;       // Proto path: "reference.government_id"
  FieldTransform transform = 3;   // Optional transformation
}

message FieldTransform {
  oneof transform {
    CelTransform cel_transform = 1;  // Explicit inbound + outbound
    EnumMapping enum_mapping = 2;    // Auto-reversible
    string date_format = 3;          // Auto-reversible
    string default_value = 4;        // Inbound only
    AttributeFlatten attribute_flatten = 5; // Auto-reversible
  }
}

// For transforms that aren't naturally reversible
message CelTransform {
  string inbound_cel = 1;   // external value -> internal value
  string outbound_cel = 2;  // internal value -> external value
}

message EnumMapping {
  map<string, string> values = 1; // External to internal
  string fallback = 2;            // Default if no match (inbound)
  string outbound_fallback = 3;   // Default if no match (outbound)
}

message AttributeFlatten {
  repeated string source_keys = 1; // External keys to collect
  string target_field = 2;         // "attributes"
}

message ComputedField {
  string target_path = 1;         // Field to populate
  string cel_expression = 2;      // CEL expression
}
```

#### Path Syntax

`external_path` uses **gjson syntax** (no `$` prefix):

- Object fields: `govt_id`, `reference.government_id`
- Array index: `items.0`
- Array length: `items.#`
- Nested array map: `items.#.name`

`internal_path` uses **proto field path notation** (dot-separated field
names matching the proto message structure).

#### CEL Expression Context

**Inbound:**

| Location | Variables | Return |
|----------|-----------|--------|
| `CelTransform.inbound_cel` | `source` (extracted external value) | Internal field type |
| `inbound_computed_fields` | `input` (external JSON), `mapped` (post-mapping) | Target field type |
| `inbound_validation_cel` | `input`, `mapped` | `bool` |

**Outbound:**

| Location | Variables | Return |
|----------|-----------|--------|
| `CelTransform.outbound_cel` | `source` (internal proto value) | External field type |
| `outbound_computed_fields` | `input` (proto JSON), `mapped` (post-mapping) | Target field type |
| `outbound_validation_cel` | `input`, `mapped` | `bool` |

All CEL expressions are subject to existing compiler constraints:
max 4096 bytes, max 10 nesting depth, cost limit 10,000.

### Batch Support

External feeds often send JSON arrays (e.g., `[{obs1}, {obs2}]`) while
Vanguard expects a wrapper object (e.g., `{"observations": [...]}`).

When `is_batch = true`:

1. The engine validates the input is a JSON array
2. Applies field_mappings and computed_fields to each element
3. Wraps the transformed array at `batch_target_path`

### Example: Bank Party Onboarding (Bidirectional)

**Inbound request**: `POST /mapping/bank-x-party-onboarding`

**External JSON** (from bank's system):

```json
{
  "type": "individual",
  "full_name": "Alice Smith",
  "govt_id": "AB123456C",
  "govt_id_type": "national_insurance",
  "dob": "1990-05-15",
  "account_officer": "AO-42",
  "branch": "LONDON-01"
}
```

**Mapping Definition** (single definition, works both directions):

```yaml
name: bank-x-party-onboarding
target_service: meridian.party.v1.PartyService
target_rpc: RegisterParty

fields:
  - external_path: "type"
    internal_path: "party_type"
    transform:
      enum_mapping:
        values:
          individual: "PARTY_TYPE_PERSON"
          corporate: "PARTY_TYPE_ORGANIZATION"

  - external_path: "full_name"
    internal_path: "legal_name"

  - external_path: "govt_id"
    internal_path: "reference.government_id"

  - external_path: "govt_id_type"
    internal_path: "reference.issuing_authority"

  - external_path: "account_officer"
    internal_path: "bank_relations.account_officer_id"

  - external_path: "branch"
    internal_path: "bank_relations.assigned_branch"

inbound_computed_fields:
  - target_path: "display_name"
    cel_expression: "input.full_name.split(' ')[0]"

inbound_validation_cel: >
  has(input.full_name) && size(input.full_name) > 0
```

**Inbound output** (proto-conformant JSON forwarded to Vanguard):

```json
{
  "partyType": "PARTY_TYPE_PERSON",
  "legalName": "Alice Smith",
  "displayName": "Alice",
  "reference": {
    "governmentId": "AB123456C",
    "issuingAuthority": "national_insurance"
  },
  "bankRelations": {
    "accountOfficerId": "AO-42",
    "assignedBranch": "LONDON-01"
  }
}
```

**Outbound** — the same mapping auto-reverses the response. The gRPC
service returns proto-JSON:

```json
{
  "partyId": "p-123",
  "partyType": "PARTY_TYPE_PERSON",
  "legalName": "Alice Smith",
  "displayName": "Alice",
  "status": "PARTY_STATUS_ACTIVE",
  "createdAt": "2026-02-20T10:30:00Z"
}
```

The engine reverses `fields` — enum mappings invert automatically
(`PARTY_TYPE_PERSON` to `individual`), field renames reverse
(`legal_name` to `full_name`). Fields not in the mapping
(`partyId`, `status`, `createdAt`) pass through unchanged:

```json
{
  "partyId": "p-123",
  "type": "individual",
  "full_name": "Alice Smith",
  "displayName": "Alice",
  "status": "PARTY_STATUS_ACTIVE",
  "createdAt": "2026-02-20T10:30:00Z"
}
```

To suppress or rename unmapped response fields, add them to `fields`:

```yaml
  # Pass through with rename
  - external_path: "id"
    internal_path: "party_id"
  - external_path: "status"
    internal_path: "status"
    transform:
      enum_mapping:
        values:
          active: "PARTY_STATUS_ACTIVE"
          suspended: "PARTY_STATUS_SUSPENDED"
  - external_path: "created_date"
    internal_path: "created_at"
    transform:
      date_format: "2006-01-02"
```

### Example: Energy Retailer Market Data (Batch)

**Request**: `POST /mapping/energy-metering-feed`

**External JSON** (batch of readings):

```json
[
  {
    "meter_id": "MPAN-1234567890",
    "reading": 42.5,
    "unit": "kWh",
    "timestamp": "2026-02-20T10:30:00Z",
    "quality": "estimated",
    "tenor": "HH",
    "settlement": "D+1"
  },
  {
    "meter_id": "MPAN-1234567890",
    "reading": 43.1,
    "unit": "kWh",
    "timestamp": "2026-02-20T11:00:00Z",
    "quality": "estimated",
    "tenor": "HH",
    "settlement": "D+1"
  }
]
```

**Mapping Definition**:

```yaml
name: energy-metering-feed
target_service: meridian.market_information.v1.MarketInformationService
target_rpc: RecordObservationBatch
is_batch: true
batch_target_path: "observations"

fields:
  - external_path: "meter_id"
    internal_path: "instrument_code"

  - external_path: "reading"
    internal_path: "value"
    transform:
      cel_transform:
        inbound_cel: "string(source)"
        outbound_cel: "double(source)"

  - external_path: "timestamp"
    internal_path: "observed_at"

  - external_path: "quality"
    internal_path: "quality_level"
    transform:
      enum_mapping:
        values:
          estimated: "QUALITY_LEVEL_ESTIMATE"
          actual: "QUALITY_LEVEL_ACTUAL"
          verified: "QUALITY_LEVEL_VERIFIED"

  - external_path: "tenor"
    internal_path: "attributes"
    transform:
      attribute_flatten:
        source_keys: ["tenor", "settlement"]
        target_field: "attributes"

inbound_computed_fields:
  - target_path: "resolution_key_value"
    cel_expression: >
      mapped.attributes.tenor + ':' + mapped.attributes.settlement
```

**Inbound output** (wrapped batch forwarded to Vanguard):

```json
{
  "observations": [
    {
      "instrumentCode": "MPAN-1234567890",
      "value": "42.5",
      "observedAt": "2026-02-20T10:30:00Z",
      "qualityLevel": "QUALITY_LEVEL_ESTIMATE",
      "attributes": [
        {"key": "tenor", "value": "HH"},
        {"key": "settlement", "value": "D+1"}
      ],
      "resolutionKeyValue": "HH:D+1"
    },
    {
      "instrumentCode": "MPAN-1234567890",
      "value": "43.1",
      "observedAt": "2026-02-20T11:00:00Z",
      "qualityLevel": "QUALITY_LEVEL_ESTIMATE",
      "attributes": [
        {"key": "tenor", "value": "HH"},
        {"key": "settlement", "value": "D+1"}
      ],
      "resolutionKeyValue": "HH:D+1"
    }
  ]
}
```

### Example: Carbon Registry Asset Class

**Request**: `POST /mapping/verra-carbon-credit`

**External JSON**:

```json
{
  "asset_type": "voluntary_carbon_credit",
  "registry": "verra",
  "vintage_year": 2025,
  "methodology": "VM0007",
  "unit": "tCO2e",
  "min_amount": 0.001,
  "precision": 3
}
```

**Mapping Definition**:

```yaml
name: verra-carbon-credit
target_service: meridian.reference_data.v1.ReferenceDataService
target_rpc: RegisterInstrument

fields:
  - external_path: "asset_type"
    internal_path: "instrument_code"

  - external_path: "unit"
    internal_path: "unit_of_measure"

  - external_path: "precision"
    internal_path: "decimal_precision"
    transform:
      cel_transform:
        inbound_cel: "int(source)"
        outbound_cel: "int(source)"

  - external_path: "registry"
    internal_path: "attributes"
    transform:
      attribute_flatten:
        source_keys: ["registry", "vintage_year", "methodology"]
        target_field: "attributes"

  - external_path: "min_amount"
    internal_path: "minimum_amount"
    transform:
      cel_transform:
        inbound_cel: "string(source)"
        outbound_cel: "double(source)"

inbound_computed_fields:
  - target_path: "dimension"
    cel_expression: "'DIMENSION_CARBON'"

inbound_validation_cel: >
  has(input.registry) && has(input.unit) && input.min_amount > 0
```

**Inbound output** (proto-conformant JSON forwarded to Vanguard):

```json
{
  "instrumentCode": "voluntary_carbon_credit",
  "unitOfMeasure": "tCO2e",
  "decimalPrecision": 3,
  "minimumAmount": "0.001",
  "dimension": "DIMENSION_CARBON",
  "attributes": [
    {"key": "registry", "value": "verra"},
    {"key": "vintage_year", "value": "2025"},
    {"key": "methodology", "value": "VM0007"}
  ]
}
```

### Phase 2 Scope

| Item | Points |
|------|--------|
| MappingDefinition proto + CRUD service | 5 |
| Bidirectional engine core (path extraction, CEL, flattening) | 8 |
| Outbound reverse engine (auto-invert transforms) | 3 |
| Gateway `/mapping/` routing + response interception | 4 |
| DryRunMapping RPC (inbound + outbound) + trace | 3 |
| Batch support (`is_batch` + `batch_target_path`) | 2 |
| Manifest integration (differ, planner, validator) | 3 |
| Reference mappings (Party, Market Data, Ref Data) | 5 |
| **Total** | **33** |

---

## Design Constraints

### Safety (Non-negotiable)

All mapping definitions must be **safe by construction**:

1. **CEL only** for expressions — guaranteed termination, bounded cost,
   no side effects
2. **Schema validation** — input validated against `external_schema`
   (JSON Schema), output validated against the target proto message
   descriptor via proto descriptor reflection
3. **Mapping compilation** at registration — parse all paths, compile
   all CEL, validate all enum mappings, verify `batch_target_path`
   (if specified) points to a valid repeated field. Reject invalid
   mappings immediately.
4. **Cost limits** — reuse existing CEL compiler constraints (max 4096
   bytes, cost limit 10,000)
5. **Immutable versions** — once active, a mapping version is frozen.
   Changes create new versions.
6. **Recursion guard** — a mapping cannot target an RPC that triggers
   another mapping. The `/mapping/` routing strategy prevents this
   naturally since Vanguard forwards to gRPC, not back to `/mapping/`.
7. **Service validation** — at registration, `target_service` is
   validated against the proto descriptor set (compiled into the
   gateway) to ensure it refers to a known gRPC service.

### Performance

1. **Compiled mappings cached** — cache compiled CEL programs and path
   extractors per tenant+mapping using `hashicorp/golang-lru/v2`.
2. **Sub-millisecond overhead** for simple field mappings (rename +
   type coercion)
3. **< 5ms overhead** for complex mappings with CEL evaluation
4. **Zero overhead** when no mapping defined — passthrough to Vanguard
   unchanged
5. **Lazy JSON path extraction** — use `tidwall/gjson` for
   external_path/internal_path extraction to avoid full unmarshaling

### Consistency

1. **Unified pattern** — all three domains use the same attribute,
   schema, and validation infrastructure (Phase 1 prerequisite)
2. **Deterministic** — same input + same mapping = same output, always
3. **Bi-temporal aware** — mappings can set temporal fields
   (valid_from/valid_to) from external formats

## Debugging and Visibility

### Transformation Trace

Every mapping execution generates a trace ID logged by the gateway.
When a mapping fails, the external partner receives a structured error:

```json
{
  "code": 400,
  "message": "Mapping transform failed",
  "trace_id": "abc-123",
  "details": {
    "field": "govt_id_type",
    "external_path": "govt_id_type",
    "internal_path": "reference.issuing_authority",
    "transform": "enum_mapping",
    "error": "No mapping for value 'passport_number' and no fallback"
  }
}
```

Error responses include: the failed field path (source and target),
the transform type that failed, the specific error message, and the
trace ID for correlation.

### DryRunMapping RPC

A `DryRunMapping` RPC (analogous to `EvaluateAssetValuation` and
`ExecuteDryRun` in sagas) allows a tenant to:

1. Submit sample JSON and a mapping name
2. Receive the transformed output or detailed error diagnostics
3. See which fields mapped, which defaulted, which CEL expressions
   evaluated
4. Works for both inbound and outbound mappings

This is essential for integrator DX — partners iterate on their
payload format without sending live requests.

## Success Criteria

1. (Phase 1) Party supports `repeated AttributeEntry` with JSON Schema
   validation and CEL expressions, consistent with Reference Data and
   Market Data
2. (Phase 2) An external system can `POST /mapping/{mapping_name}`
   with JSON in its own format and have it correctly mapped to a
   Meridian RPC
3. (Phase 2) The same mapping definition auto-reverses responses back
   to the partner's JSON format (bidirectional)
4. (Phase 2) A tenant can register a mapping definition via API and
   see it take effect immediately
5. (Phase 2) Mapping validation rejects invalid definitions at
   registration time (not at request time)
6. (Phase 2) Schema discovery API returns the expected external JSON
   shape
7. (Phase 2) DryRunMapping shows transformed output (both directions)
   or detailed errors
8. (Phase 2) Batch array inputs are correctly wrapped and forwarded
9. (Phase 2) Error responses include field path, transform type, error
   message, and trace ID
10. (Phase 2) Performance overhead < 5ms p99 for complex mappings

## Complexity Estimate

| Phase | Points | Description |
|-------|--------|-------------|
| Phase 1 | 17 | Unified property model + CEL extraction + manifest |
| Phase 2 | 33 | Bidirectional mapping + manifest integration |
| **Total** | **50** | |

### Phase 2 Build Order

Implement **DryRunMapping before the live middleware**. DryRun shares
~90% of the transformation logic (path extraction, CEL evaluation,
enum mapping, attribute flattening) but is easier to unit test because
it's a simple RPC with no routing or Vanguard integration.

Recommended sequence:

1. Proto + CRUD (foundation)
2. Mapping engine core — inbound transforms (pure logic, no HTTP)
3. Reverse engine — outbound auto-inversion of transforms
4. DryRunMapping RPC (engine + RPC, inbound + outbound, unit tests)
5. Gateway `/mapping/` middleware (routing + response interception)
6. Reference mappings + integration tests

### Phase Dependencies

```text
Phase 1 ──┐
           ├──> Phase 2
           │
(Phase 1 is recommended before Phase 2 for full consistency,
 but Phase 2 can start in parallel if Party attributes are
 not in the initial reference mappings)
```

## Implementation Advisory

### Dependencies

- **`tidwall/gjson`**: Add to `go.mod` for path extraction. Not
  currently in the module. gjson syntax uses dot notation without `$`
  prefix (e.g., `govt_id` not `$.govt_id`).
- **`hashicorp/golang-lru/v2`**: Already in `go.mod` (v2.0.7). Use
  for compiled mapping cache in the gateway middleware.

### Caching Strategy

The gateway must cache compiled mapping definitions.

- **Cache implementation**: `hashicorp/golang-lru/v2` with bounded
  size per tenant. Key: `{tenant_id}:{mapping_name}:{version}`.
- **Invalidation (V1)**: Short TTL of 1-5 minutes. Acceptable because
  mapping definitions change infrequently.
- **Invalidation (V2)**: Hook into Event Bus via
  `reference-data.mapping.updated` events for near-instant
  invalidation.

### Other Notes

- **CEL Compilation**: Pre-compile all CEL expressions at mapping
  registration time. Store compiled programs alongside the mapping
  definition in the cache.
- **Recursion Guard**: Validated at registration — `target_service`
  checked against proto descriptor set.
- **CEL Compiler Location**: Hard requirement — extract the CEL
  compiler from `services/reference-data/cel/` to `shared/pkg/cel/`
  in Phase 1. See Service Ownership section.

## Manifest Integration

The tenant manifest
(`api/proto/meridian/control_plane/v1/manifest.proto`) is the atomic
configuration snapshot for a Meridian tenant. It currently declares
instruments, account types, valuation rules, sagas, payment rails,
and seed data. Both phases of this PRD require manifest extensions.

### Phase 1: Add `party_types` to Manifest

```protobuf
message Manifest {
  // ... existing fields ...
  repeated PartyTypeDefinition party_types = 9;
}
```

This lets tenants declare party type schemas, validation rules, and
eligibility checks alongside their other business model definitions.
The control-plane differ and planner must be extended to handle
`party_types` — diff changes, plan additions/removals, and apply
them via the Party service CRUD RPCs.

### Phase 2: Add `mappings` to Manifest

```protobuf
message Manifest {
  // ... existing fields ...
  repeated MappingDefinition mappings = 10;
}
```

This lets tenants declare their bidirectional mapping definitions
as part of the manifest. The control-plane must diff mapping changes
and apply them via the Reference Data mapping CRUD RPCs.

### Files Requiring Updates

| File | Change |
|------|--------|
| `api/proto/meridian/control_plane/v1/manifest.proto` | Add fields |
| `api/jsonschema/manifest.v1.schema.json` | Regenerate |
| `api/proto/meridian/control_plane/v1/examples/*.manifest.json` | Add examples |
| `services/control-plane/internal/differ/` | Diff party_types + mappings |
| `services/control-plane/internal/planner/` | Plan apply operations |
| `services/control-plane/internal/validator/` | Validate references |
| `scripts/validate-manifest-jsonschema.sh` | No change (auto) |

### Example: Energy Trading Manifest with Mappings

```json
{
  "version": "1.0",
  "metadata": {
    "name": "Nordic Energy Trading",
    "industry": "energy"
  },
  "instruments": [ ... ],
  "accountTypes": [ ... ],
  "mappings": [
    {
      "name": "energy-metering-feed",
      "targetService": "meridian.market_information.v1.MarketInformationService",
      "targetRpc": "RecordObservationBatch",
      "isBatch": true,
      "batchTargetPath": "observations",
      "fields": [
        {
          "externalPath": "meter_id",
          "internalPath": "instrument_code"
        },
        {
          "externalPath": "quality",
          "internalPath": "quality_level",
          "transform": {
            "enumMapping": {
              "values": {
                "estimated": "QUALITY_LEVEL_ESTIMATE",
                "actual": "QUALITY_LEVEL_ACTUAL"
              }
            }
          }
        }
      ]
    }
  ]
}
```

## Open Questions

1. **Mapping inheritance?** Should tenants inherit from a base mapping
   and override specific fields? Useful for "bank template" + tenant
   customizations.

2. **Versioned RPCs?** If a proto message evolves (new fields), should
   mappings auto-adapt or require explicit versioning?
