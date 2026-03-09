---
name: prd-040-handler-schema-alignment
description: Eliminate handlers.yaml by deriving handler schemas from proto definitions via annotated Go handler registrations
triggers:
  - Adding or modifying saga handler parameters
  - Adding or modifying proto service definitions
  - Adding new enum values to proto or handler schemas
  - Working on Starlark service module generation
  - Reviewing handler registration in services/*/client/starlark.go

instructions: |
  Meridian currently maintains three representations of handler type information:
  1. Proto definitions (api/proto/) - typed enums, message fields, RPC signatures
  2. handlers.yaml (shared/pkg/saga/schema/) - Starlark schema with string-based enums
  3. Go handler implementations (services/*/client/starlark.go) - manual string-to-proto conversion

  This PRD eliminates handlers.yaml by enriching Go handler registrations with proto type
  references, allowing the schema to be derived at runtime via proto reflection. The handler
  registry becomes the single source of truth for both saga metadata and type contracts.
---

# PRD-040: Handler Schema Alignment — Proto-Derived Handler Contracts

**Author:** Meridian Platform Team
**Status:** Draft
**Date:** 2026-03-09

---

## 1. Problem Statement

Meridian has a **three-way type duplication problem** across its saga handler system:

| Component | Location | Defines | Enum representation |
|-----------|----------|---------|---------------------|
| Proto definitions | `api/proto/meridian/*/v1/*.proto` | RPC signatures, message fields, typed enums | `POSTING_DIRECTION_DEBIT = 1` |
| handlers.yaml | `shared/pkg/saga/schema/handlers.yaml` | Starlark param schemas, string enums, saga metadata | `values: [DEBIT, CREDIT]` |
| Go handlers | `services/*/client/starlark.go` | Manual param extraction, string-to-proto enum conversion | `switch "DEBIT": return POSTING_DIRECTION_DEBIT` |

**The core issue:** handlers.yaml is a manually-maintained shadow of proto definitions,
but weaker — it uses strings where proto has typed enums. Every proto change requires a
corresponding handlers.yaml update with no compiler or test to catch drift.

### 1.1 Specific Risks

1. **Silent enum drift**: Proto adds `POSTING_DIRECTION_REVERSAL`;
   handlers.yaml still says `values: [DEBIT, CREDIT]`;
   Starlark scripts can never use the new value; no error is raised
2. **Naming inconsistency**: Proto uses `BEHAVIOR_CLASS_CUSTOMER`,
   handlers.yaml uses `CUSTOMER`, Go switch statements bridge them —
   three places to update for one change
3. **No contract test**: Nothing verifies that the 11 handler registration files,
   1377-line handlers.yaml, and dozens of proto files stay in sync
4. **String-typed enums**: handlers.yaml `type: enum` with `values: [...]`
   provides no link to the proto enum type it represents,
   making automated validation impossible

### 1.2 Current Handler Metadata

`saga.HandlerMetadata` (in `shared/pkg/saga/linter.go`) already carries:

- `Category` (ingestion, settlement, valuation)
- `ProducesInstruments` (instrument codes for conservation rules)
- `CompensationStrategy` ("auto", "saga_managed", "none")
- `HasAutoCompensation` (bool)
- `IsExternal` / `RequiresPreCheck` (external system flags)

**Missing from metadata:**

- `Compensate` — the compensation handler name
  (currently only in handlers.yaml `compensate:` field)
- Proto type references — which proto request/response messages
  this handler maps to
- Description — currently only in handlers.yaml
- `Version` / `Conversions` / `Deprecated` — handler evolution
  rules for backward-compatible saga script migration
  (currently only in handlers.yaml, never used in production)

## 2. Proposed Solution

### 2.1 Design Principle

**The Go handler registration becomes the single source of truth.**
Proto types provide the structural contract (params, returns, enums).
Saga metadata (compensation, strategy, category) lives alongside the handler
implementation. handlers.yaml is eliminated.

### 2.2 Enriched Handler Metadata

Extend `saga.HandlerMetadata` with proto type references and the compensation handler name:

```go
type HandlerMetadata struct {
    // Existing fields (unchanged)
    IsExternal           bool
    RequiresPreCheck     bool
    Category             HandlerCategory
    ProducesInstruments  []string
    CompensationStrategy string
    HasAutoCompensation  bool

    // New: compensation handler name (was handlers.yaml compensate: field)
    Compensate string

    // New: proto type references for schema derivation
    ProtoRequestType  proto.Message  // e.g., (*positionkeepingv1.InitiateFinancialPositionLogRequest)(nil)
    ProtoResponseType proto.Message  // e.g., (*positionkeepingv1.InitiateFinancialPositionLogResponse)(nil)

    // New: handler description (was handlers.yaml description: field)
    Description string

    // New: parameter overrides for Starlark-specific behaviour
    // Covers cases where Starlark params don't map 1:1 to proto fields
    // (e.g., alias params, derived params, params not in proto)
    ParamOverrides map[string]ParamOverride

    // New: handler evolution (was handlers.yaml version/conversions/deprecated)
    // Version of this handler definition. Defaults to 1.
    Version int

    // Conversions defines backward-compatible mappings from old handler
    // names/params to the current definition. When a tenant's stored
    // Starlark script calls an old handler name with old param names,
    // the registry rewrites the call using these rules.
    Conversions []HandlerConversion

    // Deprecated marks this handler as superseded.
    // Scripts calling it receive a warning at validation time.
    Deprecated bool
}

// ParamOverride allows handler authors to declare Starlark-specific
// parameter behaviour that can't be derived from proto alone.
type ParamOverride struct {
    // Type overrides the proto-derived FieldType.
    // Required for types proto can't express (e.g., TypeDecimal).
    Type FieldType

    // Alias maps this Starlark param name to a different proto field name.
    // Example: Starlark "account_id" -> proto "position_id"
    Alias string

    // Derived marks this param as computed by the handler, not passed to proto.
    // Example: "valuation_analysis" is built by the saga, not a proto field.
    Derived bool

    // Deprecated marks this param as deprecated in favour of another.
    // Example: "currency" deprecated in favour of "instrument_code"
    Deprecated string

    // Required overrides proto's required/optional for Starlark context.
    // Proto may mark a field optional but the saga always requires it.
    Required *bool
}

// HandlerConversion defines how to migrate calls from old handler
// versions to the current definition. This enables stored Starlark
// scripts to continue working after handler renames or param changes.
type HandlerConversion struct {
    // FromVersion is the old handler version being migrated from.
    FromVersion int

    // FromName is the old handler name (if renamed).
    // The registry registers this as a DeprecatedMapping so old scripts
    // resolve to the current handler transparently.
    FromName string

    // ParamMapping maps new param names to old param names.
    // Key: current param name, Value: old param name the script uses.
    ParamMapping map[string]string

    // Defaults provides values for new required params that old scripts
    // won't supply. Key: param name, Value: default value expression.
    Defaults map[string]string

    // Sunset is the version at which the old name/mapping is removed.
    Sunset string
}
```

### 2.3 Updated Handler Registration

Each `services/*/client/starlark.go` registration becomes self-describing:

```go
// Current (handlers.yaml carries the type contract)
"position_keeping.initiate_log": {
    handler:  initiateLogHandler(client),
    metadata: saga.HandlerMetadata{
        Category: saga.HandlerCategoryIngestion,
    },
},

// Proposed (handler registration IS the type contract)
"position_keeping.initiate_log": {
    handler: initiateLogHandler(client),
    metadata: saga.HandlerMetadata{
        Description:          "Initiate a position log entry for a DEBIT or CREDIT transaction",
        Category:             saga.HandlerCategoryIngestion,
        Compensate:           "position_keeping.cancel_log",
        CompensationStrategy: "auto",
        HasAutoCompensation:  true,
        ProtoRequestType:     (*positionkeepingv1.InitiateFinancialPositionLogRequest)(nil),
        ProtoResponseType:    (*positionkeepingv1.InitiateFinancialPositionLogResponse)(nil),
        ParamOverrides: map[string]saga.ParamOverride{
            "account_id":         {Alias: "position_id"},
            "amount":             {Type: schema.TypeDecimal},
            "currency":           {Deprecated: "instrument_code"},
            "valuation_analysis": {Derived: true},
        },
        // Handler evolution (no conversions needed if never renamed)
        Version: 1,
    },
},

// Example with handler evolution (hypothetical rename + param change)
"test.record_entry": {
    handler: recordEntryHandler(client),
    metadata: saga.HandlerMetadata{
        Version: 2,
        Conversions: []saga.HandlerConversion{{
            FromVersion:  1,
            FromName:     "test.initiate_log",
            ParamMapping: map[string]string{"quantity": "amount"},
            Defaults:     map[string]string{"entry_type": "'STANDARD'"},
            Sunset:       "3.0",
        }},
        // ... proto types, compensation, etc.
    },
},
```

### 2.4 Schema Derivation at Runtime

Replace `schema.Parse(embeddedPlatformHandlers)` with a function that builds the schema from the handler registry:

```go
// DeriveSchema walks the handler registry and uses proto reflection
// to build HandlerDef entries equivalent to what handlers.yaml provided.
func DeriveSchema(registry *saga.HandlerRegistry) (*Schema, error) {
    schema := &Schema{Handlers: make(map[string]*HandlerDef)}

    for name, metadata := range registry.AllWithMetadata() {
        if metadata.ProtoRequestType == nil {
            return nil, fmt.Errorf("handler %s: missing ProtoRequestType", name)
        }

        def := &HandlerDef{
            Description:          metadata.Description,
            Compensate:           metadata.Compensate,
            CompensationStrategy: metadata.CompensationStrategy,
            HasAutoCompensation:  metadata.HasAutoCompensation,
        }

        // Derive params from proto request message descriptor
        reqDesc := metadata.ProtoRequestType.ProtoReflect().Descriptor()
        def.Params = deriveParams(reqDesc, metadata.ParamOverrides)

        // Derive returns from proto response message descriptor
        respDesc := metadata.ProtoResponseType.ProtoReflect().Descriptor()
        def.Returns = deriveReturns(respDesc)

        schema.Handlers[name] = def
    }
    return schema, nil
}
```

Proto field type mapping:

| Proto type | Derived FieldType | Notes |
|------------|-------------------|-------|
| `string` | `TypeString` | |
| `int32` | `TypeInt32` | |
| `int64` | `TypeInt64` | |
| `uint32` | `TypeUint32` | |
| `bool` | `TypeBool` | |
| `bytes` | `TypeString` | base64 encoded |
| `enum` | `TypeEnum` | Values derived from enum descriptor, stripping common prefix |
| `message` (nested) | `TypeMap` | |
| `repeated` | `TypeArray` | |
| Field with `(buf.validate.field).string.uuid = true` | `TypeUUID` | |
| `string` with `ParamOverride{Type: TypeDecimal}` | `TypeDecimal` | Explicit override required |

Note: Proto has no native Decimal type. All Decimal params (e.g., `amount`)
must be declared via `ParamOverride` with an explicit type override.
This avoids fragile naming conventions and makes the mapping explicit.

Enum value derivation strips the proto prefix to match Starlark conventions:

- `POSTING_DIRECTION_DEBIT` -> `DEBIT`
- `BEHAVIOR_CLASS_CUSTOMER` -> `CUSTOMER`
- Rule: remove the `ENUM_NAME_` prefix (the common prefix convention in protobuf)

### 2.5 Contract Test

A test that validates alignment across all three layers:

```go
func TestHandlerProtoAlignment(t *testing.T) {
    registry := buildFullHandlerRegistry()

    for name, metadata := range registry.AllWithMetadata() {
        t.Run(name, func(t *testing.T) {
            // 1. Proto type must be set
            require.NotNil(t, metadata.ProtoRequestType,
                "handler %s missing ProtoRequestType", name)

            // 2. Derive schema from proto
            derived := deriveHandlerDef(metadata)

            // 3. Every proto enum value must be reachable
            for paramName, paramDef := range derived.Params {
                if paramDef.Type == TypeEnum {
                    assert.NotEmpty(t, paramDef.Values,
                        "handler %s param %s: enum has no values",
                        name, paramName)
                }
            }

            // 4. Compensation handler must exist in registry
            if metadata.Compensate != "" {
                _, err := registry.Get(metadata.Compensate)
                assert.NoError(t, err,
                    "handler %s compensation %s not registered",
                    name, metadata.Compensate)
            }

            // 5. Param parity: derived schema must match
            // handlers.yaml (Phase 2 regression safety net)
            yamlDef := yamlRegistry.GetHandler(name)
            if yamlDef != nil {
                for pName, pDef := range yamlDef.Params {
                    derivedParam, ok := derived.Params[pName]
                    assert.True(t, ok,
                        "handler %s: param %s in YAML but not derived",
                        name, pName)
                    if ok {
                        assert.Equal(t, pDef.Type, derivedParam.Type,
                            "handler %s param %s: type mismatch",
                            name, pName)
                    }
                }
            }

            // 6. Conversion targets must exist
            for _, conv := range metadata.Conversions {
                if conv.FromName != "" {
                    // FromName should NOT collide with existing
                    _, err := registry.Get(conv.FromName)
                    assert.Error(t, err,
                        "conversion from_name %s collides",
                        conv.FromName)
                }
            }
        })
    }
}
```

### 2.6 Backward Compatibility: Transitional Period

The migration from handlers.yaml to proto-derived schema should be incremental:

1. **Phase 1**: Add proto type fields to `HandlerMetadata`, keep handlers.yaml as-is
2. **Phase 2**: Add contract test that compares handlers.yaml against proto-derived schema (catches existing drift)
3. **Phase 3**: Annotate all 11 handler registration files with proto types + saga metadata
4. **Phase 4**: Switch `BuildServiceModules()` to use `DeriveSchema()` instead of `schema.Parse()`
5. **Phase 5**: Delete handlers.yaml, remove `//go:embed handlers.yaml`

At each phase, existing behaviour is preserved. The contract test in Phase 2
may surface existing drift that needs fixing before Phase 4.

## 3. Scope

### 3.1 In Scope

- Extend `saga.HandlerMetadata` with `Compensate`, `Description`,
  `ProtoRequestType`, `ProtoResponseType`, `ParamOverrides`,
  `Version`, `Conversions`, `Deprecated`
- Add `HandlerConversion` struct for handler evolution rules
- Implement `DeriveSchema()` using proto reflection (`protoreflect` package)
- Annotate all 11 handler registration files with proto type references
- Move all handlers.yaml fields to Go registrations: `compensate`,
  `compensation_strategy`, `version`, `conversions`, `deprecated`
- Contract test asserting proto-handler alignment
- Enum prefix stripping logic for Starlark-friendly enum values
- Handle `ParamOverride` cases: aliases, derived params, deprecated params
- Migrate `DeprecatedMapping` registry to build from `HandlerConversion`
  metadata instead of parsing YAML
- Delete both `handlers.yaml` files (platform + control-plane) and
  their `//go:embed` directives
- Update `BuildServiceModules()` to use derived schema

### 3.2 Out of Scope

- Saga handler RBAC (which handlers a saga can invoke) —
  separate concern, separate PRD
- Proto-to-Starlark code generation (full codegen approach) —
  the runtime reflection approach is simpler and sufficient
- Changes to Starlark script syntax — scripts continue calling
  `position_keeping.initiate_log(direction="DEBIT")`

### 3.3 Services to Annotate

| Service | File | Estimated Handlers |
|---------|------|--------------------|
| position-keeping | `services/position-keeping/client/starlark.go` | 3 |
| financial-accounting | `services/financial-accounting/client/starlark.go` | 7 |
| financial-gateway | `services/financial-gateway/client/starlark.go` | 4 |
| current-account | `services/current-account/client/starlark.go` | 5 |
| reconciliation | `services/reconciliation/client/starlark.go` | 5 |
| operational-gateway | `services/operational-gateway/client/starlark.go` | 3 |
| party | `services/party/client/starlark.go` | 4 |
| market-information | `services/market-information/client/starlark.go` | 3 |
| reference-data | `services/reference-data/client/starlark.go` | 5 |
| internal-account | `services/internal-account/client/starlark.go` | 3 |
| control-plane (manifest) | `services/control-plane/internal/applier/handlers.go` | 7 |

## 4. Non-Functional Requirements

### 4.1 Zero Starlark Script Changes

Existing Starlark scripts must continue working without modification.
The derived schema must produce the same Starlark service modules with
the same parameter names, types, and enum values.

### 4.2 Startup Performance

Schema derivation via proto reflection must complete in < 100ms at startup.
Proto descriptors are already loaded; reflection is metadata-only
(no serialization).

### 4.3 Test Coverage

- Contract test must cover all registered handlers (fail if a handler lacks proto annotations)
- Enum alignment test must verify every proto enum value is reachable from Starlark
- Compensation chain test must verify all `Compensate` references point to registered handlers
- Regression test must verify derived schema matches current handlers.yaml output (Phase 2 safety net)

## 5. Risks and Mitigations

**Proto field names don't match Starlark param names:**
Schema derivation produces wrong param names.
`ParamOverride.Alias` handles mismatches; contract test catches them.

**Some handlers have params not in proto (derived values):**
Missing params in derived schema.
`ParamOverride.Derived` marks these; they're added to schema explicitly.

**Decimal type has no proto equivalent:**
Can't derive `TypeDecimal` from proto.
Handled via `ParamOverride{Type: TypeDecimal}` — explicit, no naming conventions.

**Enum prefix stripping is ambiguous:**
Wrong Starlark enum values.
Prefix stripping uses proto's common prefix convention;
override via `ParamOverride` if needed.

**Handler evolution is implemented but never used in production:**
`version`/`conversions`/`deprecated` exist in code and tests but
no production handler has ever used them. Migration to Go metadata
is low-risk since there are no existing conversion rules to preserve.

## 6. Inspiration: cadence-workflow/starlark-worker

The [cadence-workflow/starlark-worker](https://github.com/cadence-workflow/starlark-worker) project uses a pattern where:

- Plugins declare `builtins` maps that **are** the schema — no separate YAML
- Each plugin's `Register()` method validates handler signatures at startup
- `Create()` produces fresh Starlark modules per execution
- Type bridging uses a `__codec__` marker for custom types

Meridian's approach follows this philosophy: the handler registration
**is** the schema, validated at test time via proto reflection rather
than maintained as a separate YAML file.

## 7. Success Criteria

1. handlers.yaml is deleted from the codebase
2. All 11 handler registration files carry proto type annotations
3. Contract test runs in CI and fails on proto-handler drift
4. All existing Starlark scripts pass without modification
5. `BuildServiceModules()` produces identical service modules from derived schema
6. No manual enum string lists remain — all enum values derived from proto descriptors

## 8. Implementation Order

```text
Phase 1: Extend HandlerMetadata (foundation)
    └── Phase 2: Contract test comparing handlers.yaml vs proto (safety net)
        ├── Phase 3: Annotate handler files (11 services, parallelisable)
        └── Phase 4: Implement DeriveSchema() + switch BuildServiceModules()
            └── Phase 5: Delete handlers.yaml
```

Phases 1-2 are the critical path. Phase 3 can be done service-by-service. Phase 4-5 are the payoff.
