# Saga Handler Loading

## Why this document exists

Starlark saga scripts and their handler bindings are invisible to standard Go import-graph tools. There are
no `import` statements to follow: saga scripts live in the database (per-tenant `saga_definition` table),
handler registries are built at startup by explicit registration calls, and the connection between a script
name and its Go implementation is established through thread-local storage at execution time.

This document maps that loading flow so that contributors can reason about the full chain from a `.star` file
to an executed Go handler.

---

## Loading Flow

The flow has two phases: startup (handler registration + service module construction) and per-request
(saga script retrieval + execution).

### Startup Phase

```mermaid
sequenceDiagram
    participant Main as cmd/meridian
    participant HR as HandlerRegistry
    participant BSM as schema.BuildServiceModules
    participant DR as schema.DeriveSchema
    participant RT as saga.Runtime

    Main->>HR: NewHandlerRegistry()
    Main->>HR: Register{Service}Handlers(registry, deps)
    Note over HR: Handlers stored with proto metadata<br/>(ProtoRequestType, ProtoResponseType,<br/>Compensate, ParamOverrides)

    Main->>BSM: BuildServiceModules(registry)
    BSM->>DR: DeriveSchema(registry)
    Note over DR: Reflects over proto descriptors<br/>to derive FieldDefs for each handler
    DR-->>BSM: *Schema (params/returns per handler)
    BSM-->>Main: starlark.StringDict<br/>{service_name -> starlarkstruct}

    Main->>RT: NewRuntime(logger, opts...)
    Main->>saga: NewStarlarkSagaRunner(Runtime, Registry, ServiceModules)
```

### Per-Request Execution Phase

Two patterns exist depending on the calling service.

#### Pattern A - current-account (script loaded from filesystem at startup)

```mermaid
sequenceDiagram
    participant Orch as DepositOrchestrator
    participant SR as StarlarkSagaRunner
    participant RT as saga.Runtime
    participant HW as HandlerWrapper (Starlark builtin)
    participant GH as Go Handler

    Orch->>SR: ExecuteSaga(ctx, "current_account_deposit", script, input)
    SR->>SR: buildStarlarkContext(ctx, input)
    SR->>SR: buildPredeclaredModules() - injects serviceModules
    SR->>RT: ExecuteSagaWithInput(ctx, name, script, ExecutionInput)
    Note over RT: Validates script size (<=64 KB)<br/>5s timeout<br/>Predeclared: input_data, builtins, service modules

    RT->>RT: starlark.ExecFile(thread, script, predeclared)
    Note over RT: Script body runs; calls service module methods

    RT->>HW: position_keeping.initiate_log(account_id=..., amount=...)
    HW->>HW: authorizeHandlerInvocation(RBAC check)
    HW->>HW: CoerceParams + ValidateParams (schema check)
    HW->>GH: handler(StarlarkContext, params)
    GH-->>HW: result map[string]any
    HW->>HW: trackStepResult (for compensation)
    HW-->>RT: starlarkstruct.Struct (typed result)

    RT-->>SR: ExecutionResult{Globals}
    SR-->>Orch: *RunnerOutput{Success, Output, StepResults}
```

#### Pattern B - payment-order (script fetched from reference-data gRPC at request time)

```mermaid
sequenceDiagram
    participant Orch as PaymentOrchestrator
    participant RD as ReferenceDataClientWrapper
    participant RPC as SagaRegistryService (gRPC)
    participant DB as saga_definition table
    participant SR as StarlarkSagaRunner

    Orch->>RD: GetSaga(ctx, sagaName, version=0)
    RD->>RPC: GetSagaRequest{Name, Version=0}
    RPC->>DB: SELECT ... WHERE name=$1 AND status=ACTIVE<br/>ORDER BY version DESC (tenant override first)
    DB-->>RPC: saga_definition row (script content)
    RPC-->>RD: GetSagaResponse{Saga{Script, ...}}
    RD-->>Orch: *SagaDefinition{Script}

    Orch->>SR: ExecuteSaga(ctx, sagaName, sagaDef.Script, runnerInput)
    Note over SR: Same execution path as Pattern A
```

---

## Validation Rules

### Compile-time (Starlark parser, applied at `starlark.ExecFile`)

| Rule | Enforced By | Detail |
|------|-------------|--------|
| Starlark syntax | Starlark parser | Standard Starlark grammar; syntax errors produce `ErrSyntax` |
| No `while` loops | Starlark language | Starlark has no `while` statement; only `for` over finite iterables |
| No recursion | Starlark language | Starlark does not permit recursive function calls |
| No `import` | Starlark language | Starlark has no module import mechanism; service modules are pre-injected as predeclared globals |
| Script size | `sandbox.ValidateScript` | Maximum 64 KB; returns `ErrScriptTooLarge` before execution begins |

### Runtime (applied per handler call during script execution)

| Rule | Enforced By | Location |
|------|-------------|----------|
| Execution timeout | `context.WithTimeout` in `Runtime` | 5 s default; `ErrTimeout` on breach |
| Positional args rejected | `wrapHandler` | Handler calls must use keyword arguments only |
| Required params present | `HandlerDef.ValidateParams` | `ErrMissingParam` for absent required fields |
| Type coercion | `schema.CoerceParams` | Converts Starlark strings to `Decimal`, enums validated against schema |
| RBAC authorization | `authorizeHandlerInvocation` | Checks `Claims.HasScope` / `HasRole` for handlers with `resource_type` + `required_permission` declared; system sagas (no Claims on context) bypass this check |
| CEL preconditions | `saga.Definition.PreconditionsExpression` | Evaluated before script execution by callers that set this field on the definition |

### Schema build-time (applied once at startup in `BuildServiceModules`)

| Rule | Enforced By | Detail |
|------|-------------|--------|
| Handler present in registry | `buildServiceStruct` | `ErrHandlerMissingFromRegistry` if schema names a handler not in `HandlerRegistry` |
| No naming conflicts | `handlerTree.validate` | `ErrNamingConflict` if a name is used as both a handler and a namespace |
| Complete RBAC metadata | `BuildServiceModulesFromSchema` | `ErrPartialRBACMetadata` if only one of `resource_type` / `required_permission` is set |

---

## Storage Locations

| Artifact | Location | Loaded By |
|----------|----------|-----------|
| Platform default saga scripts | `services/reference-data/saga/defaults/<name>/v<semver>.star` | `Seeder.SeedTenant` via `embed.FS` at tenant provisioning |
| Tenant saga scripts | `saga_definition` table (per-tenant schema) | `SagaRegistryService.GetSaga` gRPC RPC |
| Handler schemas | `shared/pkg/saga/schema/handlers.yaml` (embedded) | `schema.BuildServiceModules` via `DeriveSchema` (proto reflection) |
| current-account scripts at startup | Filesystem path under `SAGA_ASSET_DIR` or executable dir | `loadSagaAsset` in `services/current-account/service/server.go` |

---

## Bootstrap Flow - Platform Default Sagas

Platform default sagas are seeded into every tenant schema during provisioning, not at binary startup.

```mermaid
sequenceDiagram
    participant PW as ProvisioningWorker
    participant Seeder as refsaga.Seeder
    participant EFS as embed.FS (defaults/)
    participant DB as tenant schema saga_definition

    PW->>Seeder: SeedTenant(ctx, tenantID)
    Seeder->>EFS: ReadDir("defaults") - discover saga directories
    Seeder->>EFS: ReadFile for highest semver .star in each dir
    Note over Seeder: "deposit"->"current_account_deposit"<br/>"withdrawal"->"current_account_withdrawal"

    loop for each platform default saga
        Seeder->>DB: INSERT saga_definition<br/>(is_system=true, ACTIVE, v1)<br/>ON CONFLICT DO NOTHING
    end

    Note over DB: Idempotent - safe to call multiple times<br/>UUIDv5 gives deterministic IDs per saga name
```

The `ProvisioningWorker` registers this as hook `"saga-definitions"` (step 3 of 6 in
`startProvisioningWorker`), running after instrument seeding but before account-type blueprints.

---

## Tenant Override Flow

When a tenant creates a custom saga with the same name as a platform default, the registry applies tenant
resolution at retrieval time.

```mermaid
sequenceDiagram
    participant Caller as payment-order / event-router
    participant Registry as PostgresRegistry (reference-data)
    participant DB as tenant schema saga_definition

    Caller->>Registry: GetActive(ctx, name)
    Registry->>DB: Step 1 - query WHERE name=$1<br/>AND is_system=false AND status='ACTIVE'
    alt tenant override found
        DB-->>Registry: *Definition (tenant saga)
    else no tenant override
        Registry->>DB: Step 2 - query WHERE name=$1<br/>AND is_system=true AND status='ACTIVE'
        DB-->>Registry: *Definition (platform default) or ErrNotFound
    end
    Registry-->>Caller: *Definition
```

Tenant sagas have `is_system=false`. System sagas have `is_system=true` and are read-only - the registry
returns `ErrSystemSagaReadOnly` for any mutation attempt on them.

---

## Key Types and Entry Points

| Symbol | File | Role |
|--------|------|------|
| `StarlarkSagaRunner` | `shared/pkg/saga/starlark_runner.go` | Orchestrates execution: builds context, injects modules, runs compensation |
| `Runtime` | `shared/pkg/saga/runtime.go` | Low-level Starlark execution with timeout, size limits, predeclared builtins |
| `HandlerRegistry` | `shared/pkg/saga/handlers.go` | In-memory map of handler name to Go `Handler` func + `HandlerMetadata` |
| `BuildServiceModules` | `shared/pkg/saga/schema/service_modules.go` | Converts registry + proto schema into `starlark.StringDict` of typed service structs |
| `DeriveSchema` | `shared/pkg/saga/schema/derive.go` | Reflects proto descriptors to build `FieldDef` maps for params/returns |
| `Seeder` | `services/reference-data/saga/seeder.go` | Copies embedded `.star` files into tenant `saga_definition` rows at provisioning |
| `Registry` interface | `services/reference-data/saga/registry.go` | Defines `GetActive`, `CreateDraft`, `ActivateSaga`, etc. for the reference-data service |
| `handlers.yaml` | `shared/pkg/saga/schema/handlers.yaml` | Canonical handler schema: descriptions, compensation relationships, proto references |
