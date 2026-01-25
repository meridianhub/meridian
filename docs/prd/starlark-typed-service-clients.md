# PRD: Starlark Typed Service Clients

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
| **Inconsistent Behavior** | Older tenants run different code than newer tenants |
| **Manual Migration Required** | Platform updates require explicit migration across all tenants |

**Desired Behavior:**

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

- Bazel 9 introduces experimental type annotations via `--experimental_starlark_type_checking`
- Syntax inspired by PEP 484: `def foo(x: int) -> str:`
- Goal for Bazel 10 is full type checking
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

### Option C: Constants + Validation (Hybrid)

Generate Go constants and use them in both handler registration and Starlark validation.

**Generated Go:**

```go
// shared/pkg/saga/handlers_gen.go
package saga

// Handler name constants (single source of truth)
const (
    HandlerCurrentAccountPositionInitLog = "current_account.position_keeping.initiate_log"
    HandlerCurrentAccountPositionCancelLog = "current_account.position_keeping.cancel_log"
    // ...
)

// Handler registry validation
var AllHandlers = []string{
    HandlerCurrentAccountPositionInitLog,
    HandlerCurrentAccountPositionCancelLog,
}
```

**Starlark Validation (enhanced):**

```go
func NewRestrictedBuiltins() starlark.StringDict {
    builtins["Handlers"] = buildHandlersEnum()  // Exposes constants to Starlark
    // ...
}
```

**Starlark Usage:**

```starlark
# Constants exposed to scripts
invoke_handler(
    handler=Handlers.CURRENT_ACCOUNT_POSITION_INITIATE_LOG,
    params={...}
)
```

**Pros:**

- Single source of truth in Go
- IDE can autocomplete constant names
- Easy to add parameter validation incrementally

**Cons:**

- Ugly constant names in Starlark
- Doesn't provide parameter schema validation

## Recommended Approach

**Phase 1: Schema Registry + Enhanced Validation** (Low effort, high value)

1. Define handler schemas in YAML/JSON alongside handler registration
2. Enhance `SemanticLinter` to validate parameters against schemas
3. Generate Go constants from schemas for type safety
4. Add validation at `ValidateDraft` and `ValidateActivation`

**Phase 2: Starlark Service Modules** (Medium effort, high value)

1. Generate Starlark module definitions from handler schemas
2. Pre-declare service modules in `NewRestrictedBuiltins()`
3. Handlers become callable methods on frozen structs
4. Migrate existing scripts incrementally (support both syntaxes)

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

### Option B: Symbolic Reference in Tenant Table

Store only a reference marker in tenant `saga_definition`, not the script content.

```sql
-- Tenant saga_definition for platform defaults
INSERT INTO saga_definition (name, is_platform_ref, platform_version)
VALUES ('current_account_deposit', true, '1.2.0');

-- Tenant saga_definition for overrides (existing behavior)
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

### Migration Strategy

1. **Phase 1**: Create `public.platform_saga_definition` and populate from embedded files
2. **Phase 2**: Add `platform_ref` column to tenant `saga_definition`
3. **Phase 3**: Update seeder to create references instead of copies
4. **Phase 4**: Existing tenant copies become "frozen overrides" (opt-in to platform updates)

## Implementation Effort Estimates

| Phase | Scope | Estimate |
|-------|-------|----------|
| Phase 1 | Schema YAML + Enhanced Linter | 2-3 days |
| Phase 2 | Starlark Service Modules | 3-5 days |
| Phase 3 | LSP/IDE Integration | 5-8 days |
| Phase 4 | Platform Default Inheritance | 3-4 days |

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
