# PRD: Starlark Typed Service Clients

**Status:** Implemented
**Task Master Tag:** `starlark-typed-clients` (10/10 tasks done)

## Table of Contents

- [Problem Statement](#problem-statement)
- [Current Architecture](#current-architecture)
- [Research: Approaches Used by Other Systems](#research-approaches-used-by-other-systems)
- [Proposed Solution: Starlark Service Modules](#proposed-solution-starlark-service-modules)
- [Implementation Options](#implementation-options)
- [Bazel Idiomatic Alignment](#bazel-idiomatic-alignment)
- [Recommended Approach](#recommended-approach)
- [Schema Definition Location](#schema-definition-location)
- [Migration Path](#migration-path)
- [Success Criteria](#success-criteria)
- [Platform Default Saga Inheritance](#platform-default-saga-inheritance)
- [Implementation Tasks](#implementation-tasks)
- [Task Dependencies and Complexity](#task-dependencies-and-complexity)
- [References](#references)

---

## Problem Statement

Currently, Meridian's saga definitions use magic strings to reference service handlers:

```starlark
# Current: Magic strings with no compile-time validation
invoke_handler(
    handler="current_account.position_keeping.initiate_log",  # Typo? Will fail at runtime
    params={...}
)
```

**Pain Points:**

1. **No IDE Support**: No autocomplete, no inline documentation, no type hints
2. **Runtime Failures**: Typos in handler names only discovered at saga validation/execution
3. **Brittle Refactoring**: Renaming a handler requires grep-and-replace across all `.star` files
4. **Missing Schema Validation**: No way to validate parameters match handler expectations
5. **Documentation Gap**: Handler contracts (inputs/outputs) aren't discoverable from Starlark
6. **Platform Default Copy Semantics**: Platform sagas are copied to each tenant, preventing automatic propagation of updates

## Problem: Platform Default Saga Inheritance

The current `SagaSeeder` copies platform default `.star` files into each tenant's `saga_definition` table:

```go
// services/reference-data/saga/seeder.go - Current approach
query := `
    INSERT INTO saga_definition (id, name, version, script, status, is_system, ...)
    VALUES ($1, $2, $3, $4, ...)
    ON CONFLICT (name, version) DO NOTHING`
```

**Consequences:**

| Issue | Impact |
|-------|--------|
| **No Update Propagation** | Bug fixes to `deposit.star` don't reach existing tenants |
| **Storage Duplication** | Same script content stored N times (once per tenant) |
| **Inconsistent Behaviour** | Older tenants run different code than newer tenants |
| **Manual Migration Required** | Platform updates require explicit migration across all tenants |

**Desired Behaviour:**

```text
┌─────────────────────────────────────────────────────────────────┐
│                    Platform Defaults (public schema)            │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ platform_saga_definition                                  │  │
│  │   - current_account_deposit (v1.2.0)                     │  │
│  │   - current_account_withdrawal (v1.2.0)                  │  │
│  │   - payment_execution (v1.1.0)                           │  │
│  └──────────────────────────────────────────────────────────┘  │
└────────────────────────────────┬────────────────────────────────┘
                                 │ REFERENCE (not copy)
        ┌────────────────────────┼────────────────────────┐
        │                        │                        │
        ▼                        ▼                        ▼
┌───────────────┐      ┌───────────────┐      ┌───────────────┐
│   Tenant A    │      │   Tenant B    │      │   Tenant C    │
│ (uses default)│      │ (uses default)│      │ (custom)      │
│               │      │               │      │               │
│ saga_def:     │      │ saga_def:     │      │ saga_def:     │
│ - (empty or   │      │ - (empty or   │      │ - deposit     │
│    ref only)  │      │    ref only)  │      │   (override)  │
└───────────────┘      └───────────────┘      └───────────────┘
        │                        │                        │
        └────────────────────────┼────────────────────────┘
                                 ▼
                    When platform updates deposit.star:
                    - Tenant A & B: automatic update
                    - Tenant C: keeps custom override
```

## Current Architecture

### Handler Registration (Go Side)

```go
// services/current-account/service/saga_handlers.go
func RegisterCurrentAccountHandlers(registry *saga.DomainHandlerRegistry) error {
    handlers := []struct {
        name    string
        handler saga.DomainHandler
    }{
        {"current_account.position_keeping.initiate_log", currentAccountPositionKeepingInitiateLog},
        {"current_account.financial_accounting.capture_posting", currentAccountFinAcctCapturePosting},
        // ...
    }
    for _, h := range handlers {
        registry.Register(h.name, h.handler)
    }
}
```

### Handler Invocation (Starlark Side)

```starlark
# services/current-account/sagas/deposit.star
log_position_result = invoke_handler(
    handler="current_account.position_keeping.initiate_log",
    params={
        "account_id": account_identification,
        "amount": amount,
        "currency": currency,
        "direction": "CREDIT",
        "transaction_id": transaction_id,
    },
    compensate_handler="current_account.position_keeping.cancel_log",
    ...
)
```

### Existing Validation

The `ReferenceValidator` (`services/reference-data/saga/reference_validator.go`) already:

- Extracts handler references from Starlark AST
- Validates handlers exist in registry at DRAFT save time
- Provides "Did you mean...?" suggestions for typos
- Persists references for deprecation impact analysis

However, this validation happens **after** the script is written, not during authoring.

## Research: Approaches Used by Other Systems

### 1. Bazel Type Annotations (Experimental)

From [Starlark Types | Buck2](https://buck2.build/docs/developers/starlark/types/) and [Bazel Issue #22935](https://github.com/bazelbuild/bazel/issues/22935):

- As of Bazel 9.0 (Jan 2026), experimental type annotations available via
  `--experimental_starlark_type_checking`
- Syntax inspired by PEP 484: `def foo(x: int) -> str:`
- Bazel 10 roadmap targets stabilization of the typing system
- Currently useful for IDE tooling and static analysis

**Relevance**: Could annotate saga DSL functions with type hints, but doesn't solve the service discovery problem.

### 2. Buck2 Providers

From [Buck2 Glossary](https://buck2.build/docs/concepts/glossary/):

- Rules return **Providers** - typed data structures
- Providers flow through the dependency graph
- Enables static analysis of what data is available

**Relevance**: Could model services as providers, but Buck2's approach is build-system-specific.

### 3. Go Starlark Structs

From [starlarkstruct package](https://pkg.go.dev/go.starlark.net/starlarkstruct):

- `starlarkstruct.Make` creates immutable structs with named fields
- Can create "branded" structs with custom constructors
- Fields accessed via dot notation: `service.handler_name`

**Relevance**: Core mechanism for implementing typed service modules.

### 4. LUCI GenStruct

From [LUCI builtins package](https://pkg.go.dev/go.chromium.org/luci/starlark/builtins):

- `GenStruct(name)` returns a callable constructor for branded struct instances
- Used for creating domain-specific typed objects

**Relevance**: Pattern for generating type-safe constructors.

## Proposed Solution: Starlark Service Modules

### Vision: Typed Service Clients

```starlark
# FUTURE: Typed service clients with IDE support

# Service modules are pre-declared as frozen structs
result = current_account.position_keeping.initiate_log(
    account_id=account_identification,
    amount=amount,
    currency=currency,
    direction=current_account.Direction.CREDIT,  # Enum!
    transaction_id=transaction_id,
)
```

### Architecture

```text
┌─────────────────────────────────────────────────────────────────┐
│                    Handler Schema Registry                       │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ services:                                                │    │
│  │   current_account:                                       │    │
│  │     position_keeping:                                    │    │
│  │       initiate_log:                                      │    │
│  │         params:                                          │    │
│  │           account_id: {type: string, required: true}     │    │
│  │           amount: {type: Decimal, required: true}        │    │
│  │           direction: {type: enum, values: [DEBIT,CREDIT]}│    │
│  │         returns:                                         │    │
│  │           log_id: string                                 │    │
│  │           version: int64                                 │    │
│  └─────────────────────────────────────────────────────────┘    │
└────────────────────┬────────────────────────┬───────────────────┘
                     │                        │
          ┌──────────▼──────────┐   ┌────────▼────────┐
          │ Go Code Generator   │   │ Starlark Module │
          │                     │   │ Generator       │
          │ - Handler constants │   │                 │
          │ - Param validators  │   │ - Service structs│
          │ - Type assertions   │   │ - Param schemas  │
          └──────────┬──────────┘   └────────┬────────┘
                     │                        │
          ┌──────────▼──────────┐   ┌────────▼────────┐
          │ handlers_gen.go     │   │ services.star   │
          │                     │   │ (embedded/loaded)│
          │ const (             │   │                 │
          │   HandlerCAInitLog  │   │ current_account=│
          │     = "current_acc.."│   │   struct(...)   │
          │ )                   │   │                 │
          └─────────────────────┘   └─────────────────┘
```

## Implementation Options

### Option A: Pre-declared Service Modules (Recommended)

Generate Go code that creates Starlark struct hierarchies from handler registrations.

**Starlark Usage:**

```starlark
# Services are pre-declared immutable structs
current_account.position_keeping.initiate_log(
    account_id="...",
    amount=Decimal("100.00"),
    direction="CREDIT"  # or: Direction.CREDIT
)
```

**Go Implementation:**

```go
// Auto-generated from handler registry
func BuildServiceModules(registry *DomainHandlerRegistry) starlark.StringDict {
    return starlark.StringDict{
        "current_account": &starlark.Module{
            Name: "current_account",
            Members: starlark.StringDict{
                "position_keeping": buildPositionKeepingModule(registry),
                "financial_accounting": buildFinancialAccountingModule(registry),
            },
        },
    }
}
```

**Pros:**

- Natural dot notation: `current_account.position_keeping.initiate_log()`
- IDE-friendly (LSP can introspect modules)
- Compile-time validation in Go, runtime validation in Starlark
- Mirrors protobuf/gRPC patterns

**Cons:**

- Requires code generation pipeline
- Module hierarchy must match handler naming convention

### Option B: Enhanced Linter with Handler Metadata

Extend existing `SemanticLinter` with handler schemas.

**Schema Definition:**

```go
type HandlerSchema struct {
    Name        string
    Params      map[string]ParamSchema
    Returns     map[string]ReturnSchema
    Description string
}

type ParamSchema struct {
    Type     string   // "string", "Decimal", "int64", "enum", "map"
    Required bool
    Enum     []string // For enum types
    Default  any      // For optional params
}
```

**Enhanced Linter:**

```go
func (l *SemanticLinter) CheckHandlerParams(handlerName string, params map[string]any) []LintIssue {
    schema := l.handlerSchemas[handlerName]
    // Validate required params present
    // Validate param types match schema
    // Warn on unknown params
}
```

**Pros:**

- Builds on existing infrastructure
- Incremental adoption (doesn't change script syntax)
- Can generate IDE hints from schema

**Cons:**

- Still uses magic strings in scripts
- Validation at lint time, not write time

### ~~Option C: Constants + Validation~~ (REJECTED)

> **Not Bazel-Idiomatic.** In the Starlark/Bazel philosophy, if you have to use a constant to
> look up a function, you've failed to build a proper API.
>
> ❌ `Handlers.CURRENT_ACCOUNT_POSITION_INITIATE_LOG`
> ✅ `current_account.position_keeping.initiate_log()`
>
> Constants are useful internally in Go for type safety, but should never be exposed to
> Starlark scripts. The service module pattern (Option A) provides the same benefits with
> a proper API.

## Bazel Idiomatic Alignment

This PRD independently rediscovered the **Standard Distributed Logic Pattern** used by Bazel.
The following table shows alignment:

| PRD Feature | Bazel Equivalent | Alignment |
|-------------|------------------|-----------|
| Option A: Pre-declared Structs | `native.cc_binary` | ✅ Perfect |
| Dot Notation | `proto.common_v1` | ✅ Perfect |
| Reference-based Inheritance | Bazel "Toolchains" | ✅ Strong |
| YAML Schema → Code Gen | Bazel internal rule definitions | ✅ Correct |

### Implementation Directives (Bazel-Idiomatic)

To ensure idiomatic alignment, implementers MUST follow these directives:

#### 1. Native Builtins, Not Shims

Don't build Starlark functions that call Go functions. Build Go functions and bind them
directly to Starlark struct attributes:

```go
// ✅ CORRECT: Go handler bound directly as Starlark builtin
func buildPositionKeepingModule(registry *DomainHandlerRegistry) *starlarkstruct.Struct {
    return starlarkstruct.FromStringDict(starlark.String("position_keeping"), starlark.StringDict{
        "initiate_log": starlark.NewBuiltin("initiate_log", func(
            thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple,
        ) (starlark.Value, error) {
            // Direct handler implementation - no shim layer
            return registry.Call("current_account.position_keeping.initiate_log", args, kwargs)
        }),
    })
}

// ❌ WRONG: Shim function that looks up handler by string
// invoke_handler(handler="...", params={...})
```

#### 2. Branded Structs (Providers) for Returns

Handler returns MUST be typed `starlarkstruct` instances, not `map[string]any`:

```go
// ✅ CORRECT: Return branded struct with named fields
type PositionLogResult struct {
    LogID   string
    Version int64
    Status  string
}

func (r *PositionLogResult) ToStarlark() *starlarkstruct.Struct {
    return starlarkstruct.FromStringDict(starlark.String("PositionLogResult"), starlark.StringDict{
        "log_id":  starlark.String(r.LogID),
        "version": starlark.MakeInt64(r.Version),
        "status":  starlark.String(r.Status),
    })
}

// Script can now use: result.log_id (not result["log_id"])
// And: type(result) == "PositionLogResult"
```

#### 3. Universe vs Predeclared Scope

Platform defaults go in `starlark.Universe` (global). Tenant overrides go in `predeclared`:

```go
// Platform services available to ALL threads (Universe)
func BuildPlatformUniverse() starlark.StringDict {
    return starlark.StringDict{
        "current_account": buildCurrentAccountModule(),
        "payment_order":   buildPaymentOrderModule(),
        "Decimal":         DecimalBuiltin(),
        // ... platform-provided services
    }
}

// Tenant-specific overrides (Predeclared, per-thread)
func BuildTenantPredeclared(tenantID TenantID) starlark.StringDict {
    predeclared := make(starlark.StringDict)
    // Override platform services with tenant-specific implementations
    if override := getTenantOverride(tenantID, "current_account"); override != nil {
        predeclared["current_account"] = override
    }
    return predeclared
}

// Thread initialisation
thread := &starlark.Thread{Name: sagaName}
globals, err := starlark.ExecFileOptions(
    &syntax.FileOptions{},
    thread,
    "saga.star",
    script,
    BuildTenantPredeclared(tenantID),  // Tenant overrides shadow Universe
)
```

This makes the "Hierarchy of Truth" (Platform → Tenant) a feature of the **Environment
Loader**, not script logic.

#### 4. No load() Required for Core Services

Service objects are injected into global scope during thread initialisation. Scripts access
them directly without imports:

```starlark
# ✅ CORRECT: Services available immediately
result = current_account.position_keeping.initiate_log(
    account_id=input_data["account_id"],
    amount=input_data["amount"],
)

# ❌ WRONG: Requiring load() for core services
# load("@meridian//services:current_account.star", "current_account")
```

### Implementation Constraints (Task 2)

These constraints ensure "Train Track Precision" in the generated runtime:

#### Return Provider Pattern

Every handler MUST return a branded struct. The Go generator (Task 2.2) creates these
automatically from the YAML `returns:` block:

```yaml
# handlers.yaml
returns:
  log_id: {type: string}
  version: {type: int64}
```

Generates:

```go
// Auto-generated return provider
type PositionLogResult struct { ... }
func (r *PositionLogResult) ToStarlark() *starlarkstruct.Struct { ... }
```

Scripts use `result.log_id` (not `result["log_id"]`), enabling load-time field validation.

#### Proto-YAML Build Guard

CI MUST fail if gRPC field names change in `.proto` but not in `handlers.yaml`:

```yaml
# .github/workflows/ci.yaml
- name: Validate Saga Schemas
  run: |
    go run ./tools/validate-saga-schemas \
      --yaml=services/*/saga/handlers.yaml \
      --proto=api/proto/
```

This eliminates the "Triple Entry" liability (Proto + Go + YAML divergence).

#### Strict Determinism Guard

When binding Go functions as `starlark.Builtin`, the runtime MUST verify no hidden
non-deterministic dependencies:

```go
// ❌ FORBIDDEN inside handler
time.Now()           // Use ctx.KnowledgeAt instead
rand.Int()           // Use ctx.DeterministicRandom(seed)
uuid.New()           // Use ctx.GenerateUUID()

// ✅ REQUIRED: All context from SagaContext
func handler(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
    timestamp := ctx.KnowledgeAt  // Deterministic bi-temporal timestamp
    // ...
}
```

The semantic linter should flag any handler using `time.Now()` or similar.

## Recommended Approach

**Phase 1: Schema Registry + Enhanced Validation** (Low effort, high value)

1. Define handler schemas in YAML/JSON alongside handler registration
2. Enhance `SemanticLinter` to validate parameters against schemas
3. Generate Go constants from schemas for internal type safety (not exposed to Starlark)
4. Add validation at `ValidateDraft` and `ValidateActivation`

**Phase 2: Starlark Service Modules** (Medium effort, high value)

1. Build service tree using `starlarkstruct.Make` per Bazel-idiomatic directives
2. Bind Go handlers directly as `starlark.NewBuiltin` (no shim layer)
3. Return `starlarkstruct` instances from all handlers (branded returns)
4. Inject into Universe (platform) and Predeclared (tenant overrides)

**Phase 3: IDE Integration** (Optional, high polish)

1. Implement Starlark LSP server with schema awareness
2. Provide autocomplete, hover docs, go-to-definition
3. Integrate with VS Code / IDE extensions

## Schema Definition Location

### Option 1: Inline in Go (annotations)

```go
// Using struct tags or doc comments parsed by generator
type InitiateLogParams struct {
    AccountID     string          `saga:"required,type=string"`
    Amount        decimal.Decimal `saga:"required,type=Decimal"`
    Currency      string          `saga:"required,type=string"`
    Direction     string          `saga:"required,type=enum,values=DEBIT|CREDIT"`
    TransactionID string          `saga:"required,type=string"`
}
```

### Option 2: YAML Schema Files (Recommended)

```yaml
# services/current-account/saga/handlers.yaml
service: current_account
handlers:
  position_keeping.initiate_log:
    description: "Create DEBIT/CREDIT entry in PositionKeeping service"
    params:
      account_id: {type: string, required: true, description: "Account identifier"}
      amount: {type: Decimal, required: true}
      currency: {type: string, required: true}
      direction: {type: enum, values: [DEBIT, CREDIT], required: true}
      transaction_id: {type: string, required: true}
    returns:
      log_id: {type: string}
      version: {type: int64}
      status: {type: string}
    compensate: position_keeping.cancel_log
```

**Build-Time Proto Validation:**

To prevent the "Triple Entry" problem (changes requiring updates in Proto, Go, AND YAML),
add a build step that validates YAML schemas against `.proto` definitions:

```bash
# In CI pipeline
go run ./tools/validate-saga-schemas --yaml=services/*/saga/handlers.yaml --proto=api/proto/
```

This ensures handler parameter types in YAML match the corresponding gRPC request messages.

### Option 3: Proto Extensions

Since Meridian uses protobuf, could extend service definitions:

```protobuf
service PositionKeepingHandlers {
  option (saga.handler_prefix) = "current_account.position_keeping";

  rpc InitiateLog(InitiateLogRequest) returns (InitiateLogResponse) {
    option (saga.handler_name) = "initiate_log";
    option (saga.compensate) = "cancel_log";
  }
}
```

## Migration Path

### Compatibility Layer

Support both old and new syntax during transition:

```starlark
# OLD (deprecated but supported)
invoke_handler(handler="current_account.position_keeping.initiate_log", ...)

# NEW (recommended)
current_account.position_keeping.initiate_log(...)
```

The new syntax would internally call the same handler registry, just with compile-time validation of the handler name.

### Validation Levels

1. **DRAFT**: Warn on unrecognized handlers, suggest corrections
2. **ACTIVATION**: Error on unrecognized handlers
3. **RUNTIME**: Fast-path validation (handler exists check only)

## Success Criteria

1. **Zero typo-related runtime failures**: All handler references validated before execution
2. **IDE autocomplete**: Service/handler names discoverable in editors
3. **Self-documenting**: Handler parameters and return types visible in scripts
4. **Refactoring-safe**: Renaming handlers updates all references (via constants)
5. **Backward compatible**: Existing scripts continue to work during migration
6. **Automatic platform updates**: Platform saga improvements propagate to all tenants using defaults
7. **Override transparency**: Clear visibility into which tenants have custom overrides vs defaults

## Platform Default Saga Inheritance

To address the copy-vs-reference problem, we propose a **reference-based inheritance model** for platform default sagas.

### Option A: Platform Schema with Fallback Resolution (Recommended)

Store platform defaults in `public.platform_saga_definition`. Tenant saga resolution falls back to
platform when no tenant override exists.

**Schema Changes:**

```sql
-- public schema (shared across all tenants)
CREATE TABLE public.platform_saga_definition (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    version SEMVER NOT NULL,  -- e.g., '1.2.0'
    script TEXT NOT NULL,
    display_name TEXT,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- Tenant schema saga_definition gains optional reference
ALTER TABLE saga_definition ADD COLUMN platform_ref UUID REFERENCES public.platform_saga_definition(id);
ALTER TABLE saga_definition ADD COLUMN override_reason TEXT;  -- Why tenant overrode default
```

**Resolution Logic:**

```go
func (r *Registry) GetSaga(ctx context.Context, name string) (*Definition, error) {
    // 1. Check tenant-specific override first
    def, err := r.getTenantSaga(ctx, name)
    if err == nil && def != nil {
        return def, nil
    }

    // 2. Fall back to platform default
    return r.getPlatformDefault(ctx, name)
}
```

**Pros:**

- Clean separation between platform and tenant sagas
- Automatic propagation of platform updates
- Tenants can override when needed with audit trail
- No migration of existing data required (existing copies become overrides)

**Cons:**

- Cross-schema queries (minor performance impact)
- Need to handle platform version upgrades carefully

**Override Similarity Warning:**

When a tenant creates an override, the `ReferenceValidator` should run a diff analysis. If the
override is >90% identical to the platform default, prompt:

> "You are overriding a system saga with nearly identical logic. Consider using parameters
> instead to stay on the platform update path."

This prevents unnecessary overrides that block platform maintenance updates.

### Option B: Symbolic Reference in Tenant Table

Store only a reference marker in tenant `saga_definition`, not the script content.

```sql
-- Tenant saga_definition for platform defaults
INSERT INTO saga_definition (name, is_platform_ref, platform_version)
VALUES ('current_account_deposit', true, '1.2.0');

-- Tenant saga_definition for overrides (existing behaviour)
INSERT INTO saga_definition (name, script, override_reason)
VALUES ('current_account_deposit', '...custom script...', 'Custom settlement timing');
```

**Resolution:**

```go
if def.IsPlatformRef {
    return r.loadPlatformSaga(ctx, def.Name, def.PlatformVersion)
}
return def
```

### Option C: View-Based Unification

Create a view that unions platform and tenant sagas with tenant taking precedence.

```sql
CREATE VIEW effective_saga_definition AS
SELECT
    COALESCE(t.id, p.id) AS id,
    COALESCE(t.name, p.name) AS name,
    COALESCE(t.script, p.script) AS script,
    t.id IS NOT NULL AS is_tenant_override,
    p.version AS platform_version
FROM public.platform_saga_definition p
LEFT JOIN saga_definition t ON t.name = p.name AND t.is_system = false
UNION ALL
SELECT id, name, script, true, NULL
FROM saga_definition
WHERE name NOT IN (SELECT name FROM public.platform_saga_definition);
```

### Platform Version Management

Platform sagas should be versioned independently of tenant overrides:

```yaml
# Platform saga manifest (deployed with application)
platform_sagas:
  current_account_deposit:
    version: 1.2.0
    changelog:
      - "1.2.0: Added bucket-aware solvency validation"
      - "1.1.0: Fixed compensation ordering"
      - "1.0.0: Initial release"

  payment_execution:
    version: 1.1.0
    breaking_changes:
      - version: 2.0.0
        migration: "Requires handler registry v3"
```

### Bi-Temporal Integrity for In-Flight Sagas

**Critical Requirement:** A running `SagaInstance` MUST be pinned to the exact
`platform_saga_definition.id` (and version) that was active when it started.

```go
// When starting a saga, capture the current platform version
type SagaInstance struct {
    // ... existing fields
    PlatformSagaVersionID uuid.UUID  // Pinned at saga start
    ScriptHashAtStart     string     // SHA256 of script content
}

// During replay after pod restart, use pinned version
func (e *Engine) ReplaySaga(ctx context.Context, instance *SagaInstance) error {
    // Load the EXACT script version used when saga started
    def, err := e.registry.GetSagaByVersionID(ctx, instance.PlatformSagaVersionID)
    // ...
}
```

**Rationale:**

- Prevents "hot-swap" of scripts mid-replay
- Ensures deterministic replay after pod restarts
- Platform updates only affect NEW saga instances, not in-flight ones

### Migration Strategy

1. **Phase 1**: Create `public.platform_saga_definition` and populate from embedded files
2. **Phase 2**: Add `platform_ref` column to tenant `saga_definition`
3. **Phase 3**: Update seeder to create references instead of copies
4. **Phase 4**: Existing tenant copies become "frozen overrides" (opt-in to platform updates)

## Implementation Tasks

### Task 1: Handler Schema Registry

Define handler schemas that serve as single source of truth for both Go and Starlark.

1.1. **Create handler schema YAML format** - Define schema structure for handler metadata
     (params, returns, description, compensate handler)

1.2. **Add schema files for existing handlers** - Create YAML schemas for `current_account`,
     `payment_order`, `position_keeping`, `financial_accounting` handlers

1.3. **Build schema loader in Go** - Parse YAML schemas at startup, validate against
     registered handlers

1.4. **Enhance SemanticLinter with schema validation** - Validate handler params in Starlark
     scripts against schema definitions

1.5. **Add schema validation to ValidateDraft/ValidateActivation** - Integrate schema
     checking into existing validation pipeline

1.6. **Automated documentation generator** - Generate Markdown "Service Catalogue" from YAML
     schemas. Ensures up-to-date reference of every `service.method()` and required params.

### Task 2: Starlark Service Modules

Generate typed service modules that replace magic string handler references.

2.1. **Design service module structure** - Define how `current_account.position_keeping.initiate_log()`
     maps to handler registry

2.2. **Implement service module generator** - Generate Starlark structs from handler schemas

2.3. **Add service modules to NewRestrictedBuiltins** - Pre-declare generated modules in
     Starlark runtime

2.4. **Create invoke shim for backward compatibility** - Support both old `invoke_handler()`
     and new `service.handler()` syntax

2.5. **Add parameter type coercion** ⚠️ HIGH RISK - Starlark `int` vs Go `int64`/`uint32`
     causes frequent type mismatches. Coercion layer MUST:
     - Handle overflow checks for numeric types
     - Convert Starlark `number` to schema-specified Go type (`int32`, `int64`, `uint32`)
     - Reject out-of-range values with clear error messages
     - Support `Decimal` → `string` for gRPC Money types

2.6. **Write migration guide** - Document how to update existing scripts to new syntax

### Task 3: Platform Default Saga Inheritance

Replace copy-based seeding with reference-based inheritance.

3.1. **Create platform_saga_definition table** - Add Flyway migration for
     `public.platform_saga_definition` table with versioning support

3.2. **Add platform sync mechanism** - Sync embedded `.star` files to platform table on
     application startup

3.3. **Modify saga_definition schema** - Add `platform_ref`, `override_reason`, and
     `platform_version_at_override` columns

3.4. **Implement fallback resolution in Registry** - Update `GetSaga` to check tenant first,
     then fall back to platform defaults

3.5. **Update SagaSeeder for reference-based seeding** - New tenants get references to
     platform defaults, not copies

3.6. **Create tenant override API** - Endpoint for tenants to create custom override of
     platform saga with reason

3.7. **Add platform update notification** - Notify tenants when platform default they
     override has been updated

3.8. **Build override audit view** - Query to show which tenants have overrides vs defaults

### Task 4: Migrate Existing Tenants

Handle existing tenants that have copied platform defaults.

4.1. **Create migration analysis query** - Identify tenants with platform default copies
     vs custom modifications

4.2. **Implement diff tool** - Compare tenant saga script against current platform default
     to detect modifications

4.3. **Build bulk migration script** - For tenants with unmodified copies, convert to
     platform references

4.4. **Add opt-in upgrade endpoint** - Allow tenants with frozen overrides to adopt latest
     platform version

4.5. **Create rollback mechanism** - Allow tenants to revert from platform default to
     previous override

### Task 5: IDE Integration (Optional)

Provide IDE support for Starlark saga development.

5.1. **Generate JSON schema for handlers** - Export handler schemas in JSON Schema format
     for IDE consumption

5.2. **Create VS Code extension scaffold** - Basic extension structure with Starlark
     language support

5.3. **Implement autocomplete provider** - Suggest service/handler names based on schemas

5.4. **Add hover documentation** - Show handler description, params, returns on hover

5.5. **Implement go-to-definition** - Navigate from handler call to schema definition

## Task Dependencies and Complexity

### Dependency Graph

```text
                    ┌─────────────────────────────────────────────────┐
                    │              Can Run Concurrently               │
                    └─────────────────────────────────────────────────┘

    ┌──────────────────────┐              ┌──────────────────────┐
    │  Task 1: Schema      │              │  Task 3: Platform    │
    │  Registry (8 pts)    │              │  Inheritance (8 pts) │
    │                      │              │                      │
    │  No dependencies     │              │  No dependencies     │
    └──────────┬───────────┘              └──────────┬───────────┘
               │                                     │
               ▼                                     ▼
    ┌──────────────────────┐              ┌──────────────────────┐
    │  Task 2: Starlark    │              │  Task 4: Migrate     │
    │  Modules (10 pts)    │              │  Existing (5 pts)    │
    │                      │              │                      │
    │  Depends on: Task 1  │              │  Depends on: Task 3  │
    └──────────┬───────────┘              └──────────────────────┘
               │
               ▼
    ┌──────────────────────┐
    │  Task 5: IDE         │
    │  Integration (13 pts)│
    │  [Optional]          │
    │                      │
    │  Depends on: Task 1  │
    └──────────────────────┘
```

### Complexity Estimates (Story Points)

| Task | Scope | Points | Dependencies | Concurrency |
|------|-------|--------|--------------|-------------|
| **1** | Handler Schema Registry | 8 | None | ✅ Can start immediately |
| **2** | Starlark Service Modules | 10 | Task 1 | ⏳ Blocked by Task 1 |
| **3** | Platform Default Inheritance | 8 | None | ✅ Can start immediately |
| **4** | Migrate Existing Tenants | 5 | Task 3 | ⏳ Blocked by Task 3 |
| **5** | IDE Integration (Optional) | 13 | Task 1 | ⏳ Blocked by Task 1 |

**Critical Path**: Task 1 → Task 2 (18 points) or Task 3 → Task 4 (13 points)

**Parallel Execution Strategy**:

- **Sprint 1**: Task 1 (8 pts) + Task 3 (8 pts) concurrent - 8 pts elapsed
- **Sprint 2**: Task 2 (10 pts) + Task 4 (5 pts) concurrent - 10 pts elapsed
- **Sprint 3**: Task 5 if desired (13 pts, optional)

### Subtask Complexity Breakdown

| Subtask | Points | Dependencies |
|---------|--------|--------------|
| 1.1 Schema YAML format | 2 | None |
| 1.2 Add schema files | 2 | 1.1 |
| 1.3 Build schema loader | 3 | 1.1 |
| 1.4 Enhance linter | 3 | 1.3 |
| 1.5 Integrate validation | 2 | 1.4 |
| 1.6 Doc generator | 3 | 1.3 |
| | | |
| 2.1 Design module structure | 2 | 1.3 |
| 2.2 Implement generator | 5 | 2.1 |
| 2.3 Add to builtins | 2 | 2.2 |
| 2.4 Backward compat shim | 3 | 2.3 |
| 2.5 Type coercion ⚠️ | 5 | 2.3 |
| 2.6 Migration guide | 1 | 2.4 |
| | | |
| 3.1 Platform table migration | 3 | None |
| 3.2 Platform sync mechanism | 3 | 3.1 |
| 3.3 Modify saga_definition | 2 | 3.1 |
| 3.4 Fallback resolution | 3 | 3.3 |
| 3.5 Update seeder | 3 | 3.4 |
| 3.6 Override API | 2 | 3.5 |
| 3.7 Update notification | 2 | 3.6 |
| 3.8 Audit view | 1 | 3.3 |
| | | |
| 4.1 Migration analysis | 2 | 3.4 |
| 4.2 Diff tool | 3 | 3.4 |
| 4.3 Bulk migration | 3 | 4.2 |
| 4.4 Opt-in upgrade | 2 | 4.3 |
| 4.5 Rollback mechanism | 2 | 4.4 |
| | | |
| 5.1 JSON schema export | 2 | 1.3 |
| 5.2 VS Code extension | 3 | None |
| 5.3 Autocomplete | 5 | 5.1, 5.2 |
| 5.4 Hover docs | 3 | 5.1, 5.2 |
| 5.5 Go-to-definition | 3 | 5.1, 5.2 |

## References

- [Starlark Types | Buck2](https://buck2.build/docs/developers/starlark/types/)
- [Bazel Type Annotations Issue #22935](https://github.com/bazelbuild/bazel/issues/22935)
- [starlarkstruct package](https://pkg.go.dev/go.starlark.net/starlarkstruct)
- [LUCI Starlark builtins](https://pkg.go.dev/go.chromium.org/luci/starlark/builtins)
- [Starlark-Go Implementation](https://chromium.googlesource.com/external/github.com/google/starlark-go/+/master/doc/impl.md)
- [BazelCon 2025 Type Checking](https://blog.jetbrains.com/clion/2025/11/bazelcon-2025/)

## Appendix: Current Meridian Services

Services that would need handler schemas defined:

| Service | Handlers | Description |
|---------|----------|-------------|
| `current_account` | `position_keeping.*`, `financial_accounting.*`, `repository.*` | Core banking |
| `payment_order` | `create_lien`, `send_to_gateway`, `post_ledger_entries`, `execute_lien`, `terminate_lien` | Payment processing |
| `position_keeping` | `initiate_log`, `update_log`, `cancel_log` | Position tracking |
| `financial_accounting` | `post_entries`, `reverse_entries`, `create_booking` | Ledger operations |
| `valuation_engine` | `valuate` | Instrument valuation |
| `notification` | `send` | Notifications |
| `repository` | `save` | Data persistence |
