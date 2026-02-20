# PRD: Structured Inbound Mapping Layer

## Problem Statement

External systems send JSON in their own formats — a bank's party payload
looks different from an energy retailer's, which looks different from a
carbon registry's. Today, every integration must manually conform to
Meridian's exact proto-derived JSON structure (`camelCase` field names,
specific enum strings, nested message shapes). This creates friction:
integrators must read proto definitions to understand the expected format,
and any structural mismatch causes opaque validation failures.

Meanwhile, Meridian's three domains handle flexibility inconsistently:

| Domain | Attributes | Validation | Computed | Customization |
|--------|-----------|------------|----------|---------------|
| Reference Data | JSON Schema + `AttributeEntry` | CEL | Fungibility keys | Full |
| Party | Fixed + `Struct` metadata | None | None | None |
| Market Data | JSON Schema + `AttributeEntry` + `Struct` | CEL | Resolution keys | Full |

The Vanguard transcoder (tasks 1-10) solved **protocol transcoding**
(HTTP/JSON to gRPC). This PRD addresses the next layer: **semantic
transcoding** — mapping external data formats to internal proto structures
with validation, transformation, and computed field generation.

## Technical Context

### What Exists Today

**Protocol Layer** (solved by gateway-json-transcoding):

- Vanguard transcodes REST/JSON to gRPC proto using `google.api.http`
  annotations
- Handles Content-Type negotiation (JSON, Connect, gRPC-Web)
- Identity headers propagate as gRPC metadata

**Schema Infrastructure** (exists in reference-data and
market-information):

- `attribute_schema` (JSON Schema, max 16KB) — defines valid attributes
  per entity type
- CEL compiler (`services/reference-data/cel/compiler.go`) with security
  constraints:
  - Max 4096 bytes, max 10 nesting depth, cost limit 10,000
  - Guaranteed termination (no while loops, no recursion)
- `repeated AttributeEntry` for deterministic attribute ordering
- Schema discovery RPCs (`GetAttributeSchema`)

**Handler Schema** (exists for saga orchestration):

- `handlers.yaml` defines typed handler signatures
- Auto-generates type-safe Starlark clients
- Compile-time validation of saga workflows

### What's Missing

1. **Inbound mapping definitions**: No way to say "when tenant X sends
   `{"govt_id": "..."}`, map it to `government_id` in the Party
   Reference qualifier"
2. **Consistent schema validation across domains**: Party has none;
   reference data and market data have CEL but with different patterns
3. **Gateway-level transformation**: Vanguard handles 1:1 proto JSON
   mapping; no tenant-aware transformations
4. **Computed field generation at ingress**: Resolution keys, fungibility
   keys computed deep in service layers — could be computed earlier
5. **External format discovery**: No way for integrators to ask "what
   JSON shape does tenant X expect for party onboarding?"

## Solution: Inbound Mapping Definitions

### Core Concept

An **Inbound Mapping** is a tenant-configurable definition that describes
how to transform external JSON into Meridian's internal proto structure.
Each mapping targets a specific RPC (e.g., `RegisterParty`,
`CreateInstrument`, `RecordObservation`) and defines:

1. **Field mappings**: External field paths to internal proto field paths
2. **Type coercions**: String to enum, string to decimal, date format
   normalization
3. **Validation rules**: CEL expressions for cross-field validation
4. **Computed fields**: CEL expressions that derive values from input
   (resolution keys, default values)
5. **Attribute flattening**: Map flat external JSON keys into
   `repeated AttributeEntry` or `google.protobuf.Struct`

### Routing Strategy

Mapped requests use a **dedicated ingress path** to disambiguate from
direct API calls:

```text
POST /inbound/{mapping_name}
```

The gateway:

1. Extracts `mapping_name` from the URL path
2. Looks up the mapping definition by name (tenant-scoped via identity
   headers)
3. Transforms the request body using the mapping rules
4. Rewrites the request internally to the `target_service`/`target_rpc`
5. Forwards the proto-conformant JSON to Vanguard

This avoids intercepting standard gRPC paths (e.g., `/v1/party`), which
would risk breaking clients that already send valid proto-JSON. It also
supports multiple mappings per RPC — a tenant can have
`bank-x-party-onboarding` and `bank-y-party-onboarding` targeting the
same `RegisterParty` RPC with different field layouts.

### Architecture

```text
POST /inbound/{mapping_name}
  |
  v
Gateway --> Mapping Engine --> Proto-conformant JSON --> Vanguard --> gRPC
                 |
       InboundMappingDefinition
       (tenant-configured, per-mapping-name)
```

### Service Ownership

Mapping CRUD lives in **`services/reference-data/`**. Mappings are
metadata about how to interpret data, analogous to Instrument Definitions
and Attribute Schemas. They reuse the CEL compiler infrastructure already
present in Reference Data (`services/reference-data/cel/compiler.go`).

The **Mapping Engine middleware** lives in `services/gateway/` — it
intercepts `/inbound/` requests, calls Reference Data to resolve the
mapping definition, applies transforms, and forwards to Vanguard.

### Data Model

```protobuf
// api/proto/meridian/mapping/v1/mapping.proto

enum MappingStatus {
  MAPPING_STATUS_UNSPECIFIED = 0;
  MAPPING_STATUS_DRAFT = 1;
  MAPPING_STATUS_ACTIVE = 2;
  MAPPING_STATUS_DEPRECATED = 3;
}

message InboundMappingDefinition {
  string id = 1;
  string tenant_id = 2;
  string name = 3;              // "bank-x-party-onboarding"
  string target_service = 4;    // "meridian.party.v1.PartyService"
  string target_rpc = 5;        // "RegisterParty"
  string version = 6;           // Semver for safe evolution
  MappingStatus status = 7;

  // Schema of expected inbound JSON (for discovery/docs)
  string input_schema = 8;      // JSON Schema

  // Transformation rules (ordered, applied sequentially)
  repeated FieldMapping field_mappings = 9;
  repeated ComputedField computed_fields = 10;
  string validation_expression = 11;  // CEL: post-mapping validation

  // Metadata
  google.protobuf.Timestamp created_at = 12;
  google.protobuf.Timestamp updated_at = 13;

  // Batch support
  bool is_batch = 14;           // Input is JSON array
  string batch_target_path = 15; // Wrap array at this path
}

message FieldMapping {
  string source_path = 1;       // JSONPath: "$.govt_id"
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

#### CEL Expression Context

CEL expressions have access to different variables depending on where
they appear:

- **`FieldTransform.cel_expression`**: `source` (the extracted source
  field value). Return type must match the target field type.
- **`ComputedField.cel_expression`**: `input` (original external JSON
  as `map<string, any>`) and `mapped` (intermediate state after
  field_mappings applied as `map<string, any>`).
- **`validation_expression`**: `input` and `mapped`. Must return
  `bool` — `false` rejects the request with a 400 error.

All CEL expressions are subject to the existing compiler constraints:
max 4096 bytes, max 10 nesting depth, cost limit 10,000.

### Batch Support

External feeds often send JSON arrays (e.g., `[{obs1}, {obs2}]`) while
Vanguard expects a wrapper object (e.g., `{"observations": [...]}`).

When `is_batch = true`:

1. The engine validates the input is a JSON array
2. Applies field_mappings and computed_fields to each element
3. Wraps the transformed array at `batch_target_path`

Example: An energy feed sends `[{meter, reading}, {meter, reading}]`.
With `batch_target_path = "observations"`, the engine produces
`{"observations": [{mapped1}, {mapped2}]}` for
`RecordObservationBatch`.

### Example: Bank Party Onboarding Mapping

**External JSON** (from a bank's system):

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

**Request**: `POST /inbound/bank-x-party-onboarding`

**Mapping Definition**:

```yaml
name: bank-x-party-onboarding
target_service: meridian.party.v1.PartyService
target_rpc: RegisterParty

field_mappings:
  - source: "$.type"
    target: "party_type"
    transform:
      enum_mapping:
        values:
          individual: "PARTY_TYPE_PERSON"
          corporate: "PARTY_TYPE_ORGANIZATION"

  - source: "$.full_name"
    target: "legal_name"

  - source: "$.govt_id"
    target: "reference.government_id"

  - source: "$.govt_id_type"
    target: "reference.issuing_authority"

  - source: "$.account_officer"
    target: "bank_relations.account_officer_id"

  - source: "$.branch"
    target: "bank_relations.assigned_branch"

computed_fields:
  - target: "display_name"
    cel: "input.full_name.split(' ')[0]"

validation:
  cel: "has(input.full_name) && size(input.full_name) > 0"
```

**Result** (proto-conformant JSON forwarded to Vanguard):

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

### Example: Energy Retailer Market Data Mapping

**External JSON** (from a metering system):

```json
{
  "meter_id": "MPAN-1234567890",
  "reading": 42.5,
  "unit": "kWh",
  "timestamp": "2026-02-20T10:30:00Z",
  "quality": "estimated",
  "tenor": "HH",
  "settlement": "D+1"
}
```

**Mapping Definition** transforms to `RecordObservation` with:

- `quality: "estimated"` mapped to `QUALITY_ESTIMATE` via enum_mapping
- Flat `tenor` + `settlement` collected into
  `repeated AttributeEntry` via attribute_flatten
- Computed `resolution_key_value` from attributes via CEL

### Example: Asset Class Definition Mapping

**External JSON** (from a carbon registry):

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

**Mapping Definition** transforms to `RegisterInstrument` with:

- `unit: "tCO2e"` mapped to instrument code lookup/creation
- Flat `registry`, `vintage_year`, `methodology` collected into
  `repeated AttributeEntry`
- `validation_expression`: `"value >= 0.001"`
- `dimension` derived from unit type

## Scope

### In Scope (This PRD)

1. **InboundMappingDefinition proto** — data model for mapping
   definitions
2. **Mapping CRUD service** — create, update, version,
   activate/deprecate mappings (in `services/reference-data/`)
3. **Mapping Engine middleware** — gateway component that intercepts
   `/inbound/{mapping_name}` and applies mappings before Vanguard
4. **CEL-based transforms** — reuse existing CEL compiler with
   mapping-specific extensions
5. **Schema discovery API** — "what JSON shape does this mapping
   expect?"
6. **Mapping validation** — compile-time verification that mappings
   produce valid proto JSON
7. **DryRunMapping RPC** — paste JSON, see transformed output or errors
8. **Batch support** — `is_batch` + `batch_target_path` for array
   inputs
9. **Three reference mappings** — one each for Party, Reference Data,
   and Market Data

### Out of Scope

- **Outbound mapping** (response transformation) — separate PRD
- **Streaming mapping** — this covers request/response only
- **UI for mapping creation** — API-first, UI later
- **Mapping marketplace** — sharing mappings between tenants
- **AI-assisted mapping generation** — future enhancement

## Design Constraints

### Safety (Non-negotiable)

All mapping definitions must be **safe by construction**:

1. **CEL only** for expressions — guaranteed termination, bounded cost,
   no side effects
2. **JSON Schema validation** of both input and output — catch errors at
   definition time
3. **Mapping compilation** at registration — parse all paths, compile
   all CEL, validate all enum mappings. Reject invalid mappings
   immediately.
4. **Cost limits** — reuse existing CEL compiler constraints (max 4096
   bytes, cost limit 10,000)
5. **Immutable versions** — once active, a mapping version is frozen.
   Changes create new versions.
6. **Recursion guard** — a mapping cannot target an RPC that triggers
   another mapping. The `/inbound/` routing strategy prevents this
   naturally since Vanguard forwards to gRPC, not back to `/inbound/`.

### Performance

1. **Compiled mappings cached** — don't re-parse on every request. Cache
   compiled CEL programs and path extractors per tenant+mapping using
   `hashicorp/golang-lru` (same pattern as Reference Data).
2. **Sub-millisecond overhead** for simple field mappings (rename + type
   coercion)
3. **< 5ms overhead** for complex mappings with CEL evaluation
4. **Zero overhead** when no mapping defined — passthrough to Vanguard
   unchanged
5. **Lazy JSON path extraction** — use `tidwall/gjson` for source_path
   extraction to avoid full unmarshaling where possible

### Consistency

1. **Unified pattern** — Party, Reference Data, and Market Data all use
   the same mapping infrastructure
2. **Deterministic** — same input + same mapping = same output, always
3. **Bi-temporal aware** — mappings can set temporal fields
   (valid_from/valid_to) from external formats

## Debugging and Visibility

### Transformation Trace

Every mapping execution generates a trace ID logged by the gateway.
When a mapping fails (CEL error, missing required field, schema
violation), the external partner receives a 400 with the trace ID for
correlation.

### DryRunMapping RPC

A `DryRunMapping` RPC (analogous to `EvaluateAssetValuation` and
`ExecuteDryRun` in sagas) allows a tenant to:

1. Submit sample JSON and a mapping name
2. Receive the transformed output or detailed error diagnostics
3. See which fields mapped, which defaulted, which CEL expressions
   evaluated

This is essential for integrator DX — partners can iterate on their
payload format against a mapping definition without sending live
requests.

## Success Criteria

1. An external system can `POST /inbound/{mapping_name}` with JSON in
   its own format and have it correctly mapped to a Meridian RPC
2. A tenant can register a mapping definition via API and see it take
   effect immediately
3. Mapping validation rejects invalid definitions at registration time
   (not at request time)
4. Schema discovery API returns the expected input JSON shape for a
   given mapping
5. DryRunMapping shows transformed output or detailed errors for sample
   input
6. Performance overhead < 5ms p99 for complex mappings
7. All three domains (Party, Reference Data, Market Data) have working
   reference mappings
8. Batch array inputs are correctly wrapped and forwarded

## Complexity Estimate

**Total: 26 points** (decomposition needed)

- Proto definition + CRUD service: 5 points
- Mapping engine middleware: 8 points (path extraction, CEL evaluation,
  attribute flattening, batch wrapping)
- Gateway routing (`/inbound/` handler): 3 points
- DryRunMapping RPC + trace logging: 3 points
- Batch support: 2 points
- Reference mappings + tests: 5 points

**Critical path**: Proto (5) then Engine+DryRun (11) then Gateway (3)
= 19 points sequential

**Parallelizable**: Batch support (2) and reference mappings (5) can
run alongside gateway integration

## Implementation Advisory

### Dependencies

- **`tidwall/gjson`**: Add to `go.mod` for `source_path` extraction.
  Not currently in the module — must be added. Significantly faster
  than `encoding/json` for path-based extraction (no full unmarshal).
  Fall back to `encoding/json` only for complex nested writes.
- **`hashicorp/golang-lru/v2`**: Already in `go.mod` (v2.0.7). Use
  for compiled mapping cache in the gateway middleware.

### Build Order

Implement **DryRunMapping before the live middleware**. DryRun shares
~90% of the transformation logic (path extraction, CEL evaluation,
enum mapping, attribute flattening) but is easier to unit test because
it's a simple RPC with no routing or Vanguard integration. Once DryRun
works, the live middleware wraps the same engine with HTTP routing and
request forwarding.

Recommended sequence:

1. Proto + CRUD (foundation)
2. Mapping engine core (pure transform logic, no HTTP)
3. DryRunMapping RPC (engine + RPC wrapper, full unit test coverage)
4. Gateway `/inbound/` middleware (engine + HTTP routing + Vanguard)
5. Reference mappings + integration tests

### Caching Strategy

The gateway must cache compiled mapping definitions to avoid
re-fetching and re-compiling on every request.

- **Cache implementation**: `hashicorp/golang-lru/v2` with a bounded
  size per tenant. Key: `{tenant_id}:{mapping_name}:{version}`.
- **Invalidation (V1)**: Short TTL of 1-5 minutes. Simple, no
  infrastructure dependency. Acceptable because mapping definitions
  change infrequently (registration/activation, not per-request).
- **Invalidation (V2)**: Hook into the Event Bus via
  `reference-data.mapping.updated` events for near-instant
  invalidation. This aligns with the existing event-driven patterns
  but is not required for the initial implementation.

### Other Notes

- **CEL Compilation**: Pre-compile all CEL expressions at mapping
  registration time. Store compiled programs alongside the mapping
  definition in the cache. Reuse the existing CEL compiler constraints
  from `services/reference-data/cel/`.
- **Recursion Guard**: The `/inbound/` routing strategy naturally
  prevents recursion — Vanguard forwards to gRPC backends, not back
  to the gateway's `/inbound/` handler. Validate at registration time
  that `target_service` is a known gRPC service, not a mapping
  endpoint.

## Open Questions

1. **Mapping inheritance?** Should tenants inherit from a base mapping
   and override specific fields? Useful for "bank template" + tenant
   customizations.

2. **Versioned RPCs?** If a proto message evolves (new fields), should
   mappings auto-adapt or require explicit versioning?

3. **Error granularity?** When mapping fails, should the error indicate
   which field/transform failed? (Yes, almost certainly — but how
   detailed?)
