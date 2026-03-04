# Mapping Layer

The Mapping Layer provides a configuration-driven JSON transformation engine that bridges
external partner schemas to Meridian's internal proto-based service contracts. It handles
field renaming, type coercion, enum mapping, CEL-based validation, computed fields, and
attribute flattening — all defined in declarative JSON files with no code changes required.

## Quick Start

1. Create a mapping definition JSON file in `services/reference-data/examples/`.
2. Register it via the Reference Data Service API.
3. Post payloads to `POST /mapping/{name}` on the Gateway.

## Architecture

```text
External Partner
       |
       v
  Gateway HTTP
       |
  MappingMiddleware  <- reads MappingDefinition from Reference Data Service
       |
  shared/pkg/mapping.Engine
       |
  TransformInbound / TransformOutbound / TransformInboundBatch
       |
  Downstream gRPC Service
```

### Components

| Component | Location | Responsibility |
|-----------|----------|----------------|
| Mapping Engine | `shared/pkg/mapping/` | JSON transform via gjson/sjson + CEL |
| Gateway Middleware | `services/api-gateway/internal/middleware/` | HTTP integration |
| Reference Data Service | `services/reference-data/` | Stores MappingDefinition records |
| Proto Definition | `api/proto/meridian/mapping/v1/mapping.proto` | Wire contract |
| Example Definitions | `services/reference-data/examples/` | Reference JSON files |

## Mapping Definition Schema

A mapping definition is a JSON document with the following top-level fields:

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique identifier — used as the URL path segment |
| `target_service` | string | Fully-qualified gRPC service name |
| `target_rpc` | string | RPC method name |
| `version` | int | Definition schema version |
| `inbound_validation_cel` | string | CEL expression evaluated before transform |
| `outbound_validation_cel` | string | CEL expression evaluated on outbound response |
| `is_batch` | bool | If `true`, expects a JSON array as input |
| `batch_target_path` | string | Target path for the batch array in the output |
| `fields` | array | Field correspondence rules |
| `inbound_computed_fields` | array | CEL-computed fields added to inbound output |
| `outbound_computed_fields` | array | CEL-computed fields added to outbound output |
| `idempotency` | object | Idempotency key derivation config |

### Field Correspondence

```json
{
  "external_path": "full_name",
  "internal_path": "legal_name",
  "transform": { }
}
```

A `transform` block supports exactly one of:

| Transform | Description |
|-----------|-------------|
| `enum_mapping` | Map string values; optional `fallback` |
| `date_format` | Parse/format dates using Go time layout strings |
| `attribute_flatten` | Collect named fields into `[{key, value}]` list |

### Computed Fields

```json
{
  "target_path": "display_name",
  "cel_expression": "has(input.first_name) ? input.first_name : input.full_name"
}
```

CEL variables available during inbound transform:

| Variable | Description |
|----------|-------------|
| `input` | The raw external JSON as a `map<string, dyn>` |
| `mapped` | Fields already mapped by the field rules |
| `payload` | Same as `input` (available in validation expressions) |

### Idempotency

```json
{
  "idempotency": {
    "source_selector": "govt_id"
  }
}
```

Or use a content hash over specific fields:

```json
{
  "idempotency": {
    "use_content_hash": true,
    "content_hash_fields": ["meter_id", "timestamp"]
  }
}
```

## CEL Type Safety

CEL is strongly typed. When calling `double()` on a numeric field, always compare
against float literals:

```cel
# CORRECT
double(payload.min_amount) > 0.0

# WRONG - CEL has no overload for double > int
double(payload.min_amount) > 0
```

## DryRun API

The engine supports dry-run execution that returns field-level traces without side effects:

```go
result := engine.DryRunInbound(def, inputJSON)
// result.TransformedJSON  - the output
// result.ValidationPassed - true/false
// result.FieldTraces      - per-field transformation details
// result.ExecutionTimeMs  - wall-clock transform time
```

See `services/reference-data/e2e/mapping_dry_run_test.go` for usage examples.

## Reference Examples

See [examples.md](./examples.md) for annotated walkthroughs of each reference definition.
