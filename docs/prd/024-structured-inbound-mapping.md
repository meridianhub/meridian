# PRD: Structured Inbound Mapping Layer

## Problem Statement

External systems send JSON in their own formats — a bank's party payload looks
different from an energy retailer's, which looks different from a carbon registry's.
Today, every integration must manually conform to Meridian's exact proto-derived
JSON structure (`camelCase` field names, specific enum strings, nested message
shapes). This creates friction: integrators must read proto definitions to
understand the expected format, and any structural mismatch causes opaque
validation failures.

Meanwhile, Meridian's three domains handle flexibility inconsistently:

| Domain | Attributes | Validation | Computed | Customization |
|--------|-----------|------------|----------|---------------|
| Reference Data | JSON Schema + `AttributeEntry` | CEL | Fungibility keys | Full |
| Party | Fixed + `Struct` metadata | None | None | None |
| Market Data | JSON Schema + `AttributeEntry` + `Struct` | CEL | Resolution keys | Full |

The Vanguard transcoder (tasks 1-10) solved **protocol transcoding**
(HTTP/JSON ↔ gRPC). This PRD addresses the next layer: **semantic
transcoding** — mapping external data formats to internal proto structures
with validation, transformation, and computed field generation.

## Technical Context

### What Exists Today

**Protocol Layer** (solved by gateway-json-transcoding):

- Vanguard transcodes REST/JSON → gRPC proto using `google.api.http` annotations
- Handles Content-Type negotiation (JSON, Connect, gRPC-Web)
- Identity headers propagate as gRPC metadata

**Schema Infrastructure** (exists in reference-data and market-information):

- `attribute_schema` (JSON Schema, max 16KB) — defines valid attributes per entity type
- CEL compiler (`services/reference-data/cel/compiler.go`) with security constraints:
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
5. **External format discovery**: No way for integrators to ask "what JSON
   shape does tenant X expect for party onboarding?"

## Solution: Inbound Mapping Definitions

### Core Concept

An **Inbound Mapping** is a tenant-configurable definition that describes how
to transform external JSON into Meridian's internal proto structure. Each
mapping targets a specific RPC (e.g., `RegisterParty`, `CreateInstrument`,
`RecordObservation`) and defines:

1. **Field mappings**: External field paths → internal proto field paths
2. **Type coercions**: String→enum, string→decimal, date format normalization
3. **Validation rules**: CEL expressions for cross-field validation
4. **Computed fields**: CEL expressions that derive values from input (resolution keys, default values)
5. **Attribute flattening**: Map flat external JSON keys into `repeated AttributeEntry` or `google.protobuf.Struct`

### Architecture

```text
External JSON → Gateway → Mapping Engine → Proto-conformant JSON → Vanguard → gRPC
                               ↑
                     InboundMappingDefinition
                     (tenant-configured, per-RPC)
```

The Mapping Engine sits as middleware **before** Vanguard in the gateway chain. It:

1. Identifies the target RPC from the URL path
2. Looks up the tenant's mapping definition for that RPC
3. Transforms the request body
4. Passes the conformant JSON to Vanguard for proto transcoding

### Data Model

```protobuf
// api/proto/meridian/mapping/v1/mapping.proto

message InboundMappingDefinition {
  string id = 1;
  string tenant_id = 2;
  string name = 3;                          // e.g., "bank-x-party-onboarding"
  string target_service = 4;                // e.g., "meridian.party.v1.PartyService"
  string target_rpc = 5;                    // e.g., "RegisterParty"
  string version = 6;                       // Semver for safe evolution
  MappingStatus status = 7;                 // DRAFT → ACTIVE → DEPRECATED

  // Schema of expected inbound JSON (for discovery/docs)
  string input_schema = 8;                  // JSON Schema describing external format

  // Transformation rules (ordered, applied sequentially)
  repeated FieldMapping field_mappings = 9;
  repeated ComputedField computed_fields = 10;
  string validation_expression = 11;        // CEL: cross-field validation (post-mapping)

  // Metadata
  google.protobuf.Timestamp created_at = 12;
  google.protobuf.Timestamp updated_at = 13;
}

message FieldMapping {
  string source_path = 1;                   // JSONPath in external JSON: "$.govt_id"
  string target_path = 2;                   // Proto field path: "reference.government_id"
  FieldTransform transform = 3;             // Optional transformation
}

message FieldTransform {
  oneof transform {
    string cel_expression = 1;              // CEL: "source.toUpper()"
    EnumMapping enum_mapping = 2;           // {"INDIVIDUAL": "PARTY_TYPE_PERSON", ...}
    string date_format = 3;                 // "2006-01-02" → RFC3339
    string default_value = 4;               // If source missing, use this
    AttributeFlatten attribute_flatten = 5;  // Map flat keys → AttributeEntry[]
  }
}

message EnumMapping {
  map<string, string> values = 1;           // External → internal enum string
  string fallback = 2;                      // Default if no match
}

message AttributeFlatten {
  repeated string source_keys = 1;          // External keys to collect
  string target_field = 2;                  // "attributes" (repeated AttributeEntry)
}

message ComputedField {
  string target_path = 1;                   // Proto field to populate
  string cel_expression = 2;               // CEL using mapped fields as input
}
```

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
        values: {"individual": "PARTY_TYPE_PERSON", "corporate": "PARTY_TYPE_ORGANIZATION"}

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
    cel: "input.full_name.split(' ')[0]"   # First name as display name

validation:
  cel: "has(input.full_name) && size(input.full_name) > 0"
```

**Result** (proto-conformant JSON passed to Vanguard):

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

- `quality: "estimated"` → `QUALITY_ESTIMATE`
- Flat `tenor` + `settlement` → `repeated AttributeEntry`
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

- `unit: "tCO2e"` → instrument code lookup/creation
- Flat `registry`, `vintage_year`, `methodology` → `repeated AttributeEntry`
- `validation_expression` computed: `"value >= 0.001"`
- `dimension` derived from unit type

## Scope

### In Scope (This PRD)

1. **InboundMappingDefinition proto** — data model for mapping definitions
2. **Mapping CRUD service** — create, update, version, activate/deprecate mappings
3. **Mapping Engine middleware** — gateway component that applies mappings before Vanguard
4. **CEL-based transforms** — reuse existing CEL compiler with mapping-specific extensions
5. **Schema discovery API** — "what JSON shape does this mapping expect?"
6. **Mapping validation** — compile-time verification that mappings produce valid proto JSON
7. **Three reference mappings** — one each for Party, Reference Data, and Market Data

### Out of Scope

- **Outbound mapping** (response transformation) — separate PRD
- **Streaming/batch mapping** — this covers request/response only
- **UI for mapping creation** — API-first, UI later
- **Mapping marketplace** — sharing mappings between tenants
- **AI-assisted mapping generation** — future enhancement (LLM generates mapping from sample JSON)

## Design Constraints

### Safety (Non-negotiable)

All mapping definitions must be **safe by construction**:

1. **CEL only** for expressions — guaranteed termination, bounded cost, no side effects
2. **JSON Schema validation** of both input and output — catch errors at definition time
3. **Mapping compilation** at registration — parse all paths, compile all
   CEL, validate all enum mappings. Reject invalid mappings immediately.
4. **Cost limits** — reuse existing CEL compiler constraints
   (max 4096 bytes, cost limit 10,000)
5. **Immutable versions** — once active, a mapping version is frozen.
   Changes create new versions.

### Performance

1. **Compiled mappings cached** — don't re-parse on every request. Cache
   compiled CEL programs and path extractors per tenant+mapping.
2. **Sub-millisecond overhead** for simple field mappings (rename + type coercion)
3. **< 5ms overhead** for complex mappings with CEL evaluation
4. **Zero overhead** when no mapping defined — passthrough to Vanguard unchanged

### Consistency

1. **Unified pattern** — Party, Reference Data, and Market Data all use the same mapping infrastructure
2. **Deterministic** — same input + same mapping = same output, always
3. **Bi-temporal aware** — mappings can set temporal fields (valid_from/valid_to) from external formats

## Success Criteria

1. An external system can send JSON in its own format and have it correctly mapped to a Meridian RPC
2. A tenant can register a mapping definition via API and see it take effect immediately
3. Mapping validation rejects invalid definitions at registration time (not at request time)
4. Schema discovery API returns the expected input JSON shape for a given mapping
5. Performance overhead < 5ms p99 for complex mappings
6. All three domains (Party, Reference Data, Market Data) have working reference mappings

## Complexity Estimate

**Total: 21 points** (decomposition needed)

- Proto definition + CRUD service: 5 points
- Mapping engine middleware: 8 points (core complexity — path extraction, CEL evaluation, attribute flattening)
- Gateway integration: 3 points
- Reference mappings + tests: 5 points

**Critical path**: Proto → Engine → Gateway integration (16 points sequential)
**Parallelizable**: Reference mappings can run alongside gateway integration

## Open Questions

1. **Where does the mapping engine live?** Gateway middleware (request-level)
   vs dedicated mapping service (RPC-level)? Gateway is simpler but couples
   mapping logic to the gateway. Dedicated service is cleaner but adds a
   network hop.

2. **Mapping inheritance?** Should tenants inherit from a base mapping and
   override specific fields? Useful for "bank template" + tenant
   customizations.

3. **Versioned RPCs?** If a proto message evolves (new fields), should
   mappings auto-adapt or require explicit versioning?

4. **Batch operations?** Some domains accept batch requests (bulk
   observations). Should mappings apply per-item within a batch?

5. **Error granularity?** When mapping fails, should the error indicate
   which field/transform failed? (Yes, almost certainly — but how
   detailed?)
