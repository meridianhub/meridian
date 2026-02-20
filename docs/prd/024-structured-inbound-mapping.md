# PRD: Structured Mapping Layer

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
(HTTP/JSON to gRPC). This PRD addresses three layers above that:

1. **Consistent property model** — bring Party in line with Reference
   Data and Market Data
2. **Inbound mapping** — transform external JSON to internal proto
3. **Outbound mapping** — transform internal proto to external JSON

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

## Solution: Three-Phase Mapping Layer

### Phase 1: Unified Property Model

Bring Party into consistency with Reference Data and Market Data by
adding structured attribute support and CEL validation.

### Phase 2: Inbound Mapping

Tenant-configurable definitions that transform external JSON into
Meridian's internal proto structures before Vanguard processes them.

### Phase 3: Outbound Mapping

Tenant-configurable definitions that transform internal proto responses
back into partner-specific JSON formats after Vanguard processes them.

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
3. Wire CEL compiler (import from reference-data or extract to shared)
4. Validate attributes against schema at `RegisterParty` /
   `UpdateParty` time
5. Keep existing `Association.metadata` (Struct) for backwards
   compatibility — deprecate over time

### Phase 1 Scope

| Item | Points |
|------|--------|
| PartyTypeDefinition proto + CRUD | 3 |
| Party attributes field + migration | 3 |
| CEL compiler integration in Party service | 2 |
| Schema validation at registration | 3 |
| Tests + reference party type definitions | 2 |
| **Total** | **13** |

---

## Phase 2: Inbound Mapping

### Core Concept

An **Inbound Mapping** is a tenant-configurable definition that
describes how to transform external JSON into Meridian's internal proto
structure. Each mapping targets a specific RPC and defines:

1. **Field mappings**: External field paths to internal proto field paths
2. **Type coercions**: String to enum, string to decimal, date format
   normalization
3. **Validation rules**: CEL expressions for cross-field validation
4. **Computed fields**: CEL expressions that derive values from input
5. **Attribute flattening**: Map flat external JSON keys into
   `repeated AttributeEntry` or `google.protobuf.Struct`

### Routing Strategy

Mapped requests use a **dedicated ingress path**:

```text
POST /inbound/{mapping_name}
```

The gateway:

1. Extracts `mapping_name` from the URL path
2. Resolves the mapping: look up by name (tenant-scoped), always use
   the **latest ACTIVE version**. If no ACTIVE version exists, return
   404. Version override via `X-Mapping-Version` header is supported
   but optional.
3. Transforms the request body using the mapping rules
4. Rewrites the request internally to the `target_service`/`target_rpc`
5. Forwards the proto-conformant JSON to Vanguard

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

The **Mapping Engine middleware** lives in `services/gateway/` — it
intercepts `/inbound/` requests, resolves the mapping definition,
applies transforms, and forwards to Vanguard.

### Data Model

```protobuf
// api/proto/meridian/mapping/v1/mapping.proto

enum MappingStatus {
  MAPPING_STATUS_UNSPECIFIED = 0;
  MAPPING_STATUS_DRAFT = 1;
  MAPPING_STATUS_ACTIVE = 2;
  MAPPING_STATUS_DEPRECATED = 3;
}

enum MappingDirection {
  MAPPING_DIRECTION_UNSPECIFIED = 0;
  MAPPING_DIRECTION_INBOUND = 1;   // Phase 2
  MAPPING_DIRECTION_OUTBOUND = 2;  // Phase 3
}

message MappingDefinition {
  string id = 1;
  string tenant_id = 2;
  string name = 3;              // "bank-x-party-onboarding"
  MappingDirection direction = 4;
  string target_service = 5;    // "meridian.party.v1.PartyService"
  string target_rpc = 6;        // "RegisterParty"
  string version = 7;           // Semver for safe evolution
  MappingStatus status = 8;

  // Schema of expected external JSON (for discovery/docs)
  string external_schema = 9;   // JSON Schema

  // Transformation pipeline (executed in order below)
  repeated FieldMapping field_mappings = 10;
  repeated ComputedField computed_fields = 11;
  string validation_cel = 12;   // CEL: post-mapping validation

  // Metadata
  google.protobuf.Timestamp created_at = 13;
  google.protobuf.Timestamp updated_at = 14;

  // Batch support
  bool is_batch = 15;           // Input is JSON array
  string batch_target_path = 16; // Wrap array at this path
}
```

**Execution order**: `field_mappings` (sequentially) then
`computed_fields` (sequentially, on post-mapped state) then
`validation_cel` (on final state, must return `true`).

```protobuf
message FieldMapping {
  string source_path = 1;       // gjson path: "govt_id"
  string target_path = 2;       // Proto path: "reference.government_id"
  FieldTransform transform = 3; // Optional transformation
}

message FieldTransform {
  oneof transform {
    string cel_expression = 1;
    EnumMapping enum_mapping = 2;
    string date_format = 3;       // "2006-01-02" to RFC3339
    string default_value = 4;     // Fallback if source missing
    AttributeFlatten attribute_flatten = 5;
  }
}

message EnumMapping {
  map<string, string> values = 1; // External to internal
  string fallback = 2;            // Default if no match
}

message AttributeFlatten {
  repeated string source_keys = 1; // External keys to collect
  string target_field = 2;         // "attributes"
}

message ComputedField {
  string target_path = 1;         // Proto field to populate
  string cel_expression = 2;      // CEL using mapped fields
}
```

#### Path Syntax

Source paths use **gjson syntax** (no `$` prefix):

- Object fields: `govt_id`, `reference.government_id`
- Array index: `items.0`
- Array length: `items.#`
- Nested array map: `items.#.name`

Target paths use **proto field path notation** (dot-separated field
names matching the proto message structure).

#### CEL Expression Context

| Location | Variables | Return Type |
|----------|-----------|-------------|
| `FieldTransform.cel_expression` | `source` (extracted value) | Target field type |
| `ComputedField.cel_expression` | `input` (original JSON), `mapped` (post-mapping state) | Target field type |
| `validation_cel` | `input`, `mapped` | `bool` (false = reject with 400) |

All CEL expressions are subject to existing compiler constraints:
max 4096 bytes, max 10 nesting depth, cost limit 10,000.

### Batch Support

External feeds often send JSON arrays (e.g., `[{obs1}, {obs2}]`) while
Vanguard expects a wrapper object (e.g., `{"observations": [...]}`).

When `is_batch = true`:

1. The engine validates the input is a JSON array
2. Applies field_mappings and computed_fields to each element
3. Wraps the transformed array at `batch_target_path`

### Example: Bank Party Onboarding

**Request**: `POST /inbound/bank-x-party-onboarding`

**External JSON**:

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

**Mapping Definition**:

```yaml
name: bank-x-party-onboarding
direction: INBOUND
target_service: meridian.party.v1.PartyService
target_rpc: RegisterParty

field_mappings:
  - source_path: "type"
    target_path: "party_type"
    transform:
      enum_mapping:
        values:
          individual: "PARTY_TYPE_PERSON"
          corporate: "PARTY_TYPE_ORGANIZATION"

  - source_path: "full_name"
    target_path: "legal_name"

  - source_path: "govt_id"
    target_path: "reference.government_id"

  - source_path: "govt_id_type"
    target_path: "reference.issuing_authority"

  - source_path: "account_officer"
    target_path: "bank_relations.account_officer_id"

  - source_path: "branch"
    target_path: "bank_relations.assigned_branch"

computed_fields:
  - target_path: "display_name"
    cel_expression: "input.full_name.split(' ')[0]"

validation_cel: "has(input.full_name) && size(input.full_name) > 0"
```

**Output** (proto-conformant JSON forwarded to Vanguard):

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

### Example: Energy Retailer Market Data (Batch)

**Request**: `POST /inbound/energy-metering-feed`

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
direction: INBOUND
target_service: meridian.market_information.v1.MarketInformationService
target_rpc: RecordObservationBatch
is_batch: true
batch_target_path: "observations"

field_mappings:
  - source_path: "meter_id"
    target_path: "instrument_code"

  - source_path: "reading"
    target_path: "value"
    transform:
      cel_expression: "string(source)"

  - source_path: "timestamp"
    target_path: "observed_at"

  - source_path: "quality"
    target_path: "quality_level"
    transform:
      enum_mapping:
        values:
          estimated: "QUALITY_LEVEL_ESTIMATE"
          actual: "QUALITY_LEVEL_ACTUAL"
          verified: "QUALITY_LEVEL_VERIFIED"

  - source_path: "tenor"
    target_path: "attributes"
    transform:
      attribute_flatten:
        source_keys: ["tenor", "settlement"]
        target_field: "attributes"

computed_fields:
  - target_path: "resolution_key_value"
    cel_expression: >
      mapped.attributes.tenor + ':' + mapped.attributes.settlement
```

**Output** (wrapped batch forwarded to Vanguard):

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

**Request**: `POST /inbound/verra-carbon-credit`

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
direction: INBOUND
target_service: meridian.reference_data.v1.ReferenceDataService
target_rpc: RegisterInstrument

field_mappings:
  - source_path: "asset_type"
    target_path: "instrument_code"

  - source_path: "unit"
    target_path: "unit_of_measure"

  - source_path: "precision"
    target_path: "decimal_precision"
    transform:
      cel_expression: "int(source)"

  - source_path: "registry"
    target_path: "attributes"
    transform:
      attribute_flatten:
        source_keys: ["registry", "vintage_year", "methodology"]
        target_field: "attributes"

  - source_path: "min_amount"
    target_path: "minimum_amount"
    transform:
      cel_expression: "string(source)"

computed_fields:
  - target_path: "dimension"
    cel_expression: "'DIMENSION_CARBON'"

validation_cel: >
  has(input.registry) && has(input.unit) && input.min_amount > 0
```

**Output** (proto-conformant JSON forwarded to Vanguard):

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
| Mapping engine core (path extraction, CEL, flattening) | 8 |
| Gateway `/inbound/` routing handler | 3 |
| DryRunMapping RPC + transformation trace | 3 |
| Batch support (`is_batch` + `batch_target_path`) | 2 |
| Reference mappings (Party, Market Data, Reference Data) | 5 |
| **Total** | **26** |

---

## Phase 3: Outbound Mapping

### Core Concept

An **Outbound Mapping** transforms Meridian's internal proto-JSON
responses back into partner-specific formats. This is the reverse of
Phase 2 — same engine, opposite direction.

### Routing and Version Selection

Outbound mappings are activated by the **same ingress path**:

```text
POST /inbound/{mapping_name}
```

Outbound mappings are linked to inbound mappings via
`outbound_mapping_id`:

```protobuf
message MappingDefinition {
  // ... existing fields ...
  string outbound_mapping_id = 17; // Paired outbound mapping
}
```

**Version selection rules:**

1. When resolving the inbound mapping, the gateway also resolves its
   paired outbound mapping via `outbound_mapping_id`
2. If the inbound mapping has no `outbound_mapping_id`, the gateway
   looks up the latest ACTIVE outbound `MappingDefinition` with the
   same `name` and `direction = OUTBOUND`
3. If no ACTIVE outbound mapping exists, the gateway returns
   Vanguard's raw proto-JSON response unchanged (no transformation)
   and logs a debug-level missing-outbound notice
4. The resolved outbound mapping version is included in the
   `X-Outbound-Mapping-Version` response header for traceability

### Architecture

```text
POST /inbound/{mapping_name}
  |
  v
Gateway --> Inbound Engine --> Proto JSON --> Vanguard --> gRPC Service
                                                |
                                           Proto JSON Response
                                                |
                                                v
                               Outbound Engine --> Partner JSON Response
                                    |
                          OutboundMappingDefinition
```

### Data Model

Outbound mappings reuse the same `MappingDefinition` proto with
`direction = OUTBOUND`. The field semantics reverse:

- `source_path` refers to proto response fields (gjson on proto-JSON)
- `target_path` refers to the partner's expected output structure
- `computed_fields` derive values from the response
- `validation_cel` validates the output before returning

### Example: Bank Party Response

**Internal proto-JSON** (from Vanguard):

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

**Outbound Mapping** (`bank-x-party-response`):

```yaml
name: bank-x-party-response
direction: OUTBOUND
target_service: meridian.party.v1.PartyService
target_rpc: RegisterParty

field_mappings:
  - source_path: "partyId"
    target_path: "id"

  - source_path: "partyType"
    target_path: "type"
    transform:
      enum_mapping:
        values:
          PARTY_TYPE_PERSON: "individual"
          PARTY_TYPE_ORGANIZATION: "corporate"

  - source_path: "legalName"
    target_path: "full_name"

  - source_path: "status"
    target_path: "status"
    transform:
      enum_mapping:
        values:
          PARTY_STATUS_ACTIVE: "active"
          PARTY_STATUS_SUSPENDED: "suspended"

  - source_path: "createdAt"
    target_path: "created_date"
    transform:
      date_format: "2006-01-02"
```

**Partner receives**:

```json
{
  "id": "p-123",
  "type": "individual",
  "full_name": "Alice Smith",
  "status": "active",
  "created_date": "2026-02-20"
}
```

### Phase 3 Scope

| Item | Points |
|------|--------|
| Outbound engine (reverse field mapping) | 5 |
| Gateway response interception middleware | 3 |
| Inbound/outbound pairing logic | 2 |
| DryRunMapping extension for outbound | 2 |
| Reference outbound mappings + tests | 3 |
| **Total** | **15** |

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
   another mapping. The `/inbound/` routing strategy prevents this
   naturally since Vanguard forwards to gRPC, not back to `/inbound/`.
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
5. **Lazy JSON path extraction** — use `tidwall/gjson` for source_path
   extraction to avoid full unmarshaling where possible

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
    "source_path": "govt_id_type",
    "target_path": "reference.issuing_authority",
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
2. (Phase 2) An external system can `POST /inbound/{mapping_name}`
   with JSON in its own format and have it correctly mapped to a
   Meridian RPC
3. (Phase 2) A tenant can register a mapping definition via API and
   see it take effect immediately
4. (Phase 2) Mapping validation rejects invalid definitions at
   registration time (not at request time)
5. (Phase 2) Schema discovery API returns the expected input JSON shape
6. (Phase 2) DryRunMapping shows transformed output or detailed errors
7. (Phase 2) Batch array inputs are correctly wrapped and forwarded
8. (Phase 2) Error responses include field path, transform type, error
   message, and trace ID
9. (Phase 2) Performance overhead < 5ms p99 for complex mappings
10. (Phase 3) Outbound mappings transform proto-JSON responses back to
    partner-specific formats
11. (Phase 3) Inbound/outbound mapping pairs work end-to-end on a
    single `/inbound/` request

## Complexity Estimate

| Phase | Points | Description |
|-------|--------|-------------|
| Phase 1 | 13 | Unified property model (Party consistency) |
| Phase 2 | 26 | Inbound mapping (engine, gateway, DryRun, batch) |
| Phase 3 | 15 | Outbound mapping (reverse engine, response middleware) |
| **Total** | **54** | |

### Phase 2 Build Order

Implement **DryRunMapping before the live middleware**. DryRun shares
~90% of the transformation logic (path extraction, CEL evaluation,
enum mapping, attribute flattening) but is easier to unit test because
it's a simple RPC with no routing or Vanguard integration.

Recommended sequence:

1. Proto + CRUD (foundation)
2. Mapping engine core (pure transform logic, no HTTP)
3. DryRunMapping RPC (engine + RPC wrapper, full unit test coverage)
4. Gateway `/inbound/` middleware (engine + HTTP routing + Vanguard)
5. Reference mappings + integration tests

### Phase Dependencies

```text
Phase 1 ──┐
           ├──> Phase 2 ──> Phase 3
           │
(Phase 1 is recommended before Phase 2 for full consistency,
 but Phase 2 can start in parallel if Party attributes are
 not in the initial reference mappings)
```

## Implementation Advisory

### Dependencies

- **`tidwall/gjson`**: Add to `go.mod` for `source_path` extraction.
  Not currently in the module. gjson syntax uses dot notation without
  `$` prefix (e.g., `govt_id` not `$.govt_id`).
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
- **CEL Compiler Location**: If Phase 1 proceeds first, extract the
  CEL compiler from `services/reference-data/cel/` to
  `shared/pkg/cel/` so both Party and the mapping engine can use it
  without importing the reference-data service.

## Open Questions

1. **Mapping inheritance?** Should tenants inherit from a base mapping
   and override specific fields? Useful for "bank template" + tenant
   customizations.

2. **Versioned RPCs?** If a proto message evolves (new fields), should
   mappings auto-adapt or require explicit versioning?
