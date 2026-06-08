---
name: prd-meridian-economy-runtime
description: >
  Formalise Meridian as a programmable Economy Runtime with typed service modules,
  handler evolution, relationship graph, AI generator, and conversational IDE
triggers:
  - Working on manifest validation, typed service modules, or handler schemas
  - Designing AI-assisted economy configuration or manifest generation
  - Building the generator pipeline or IDE wizard for economy creation
  - Working on handler versioning, conversion rules, or ABI evolution
  - Implementing relationship graph extraction or impact analysis
  - Discussing Starlark/CEL bounded expressiveness or termination guarantees
  - Building economy simulation or what-if impact analysis features
  - Working on saga error taxonomy, DLQ, or operator dashboard
instructions: |
  This PRD formalises the existing Meridian architecture as an Economy Runtime and
  defines three new layers (Relationship Graph, Generator, IDE) plus three foundational
  improvements (Typed Service Modules, Handler Evolution, Trigger Validation).
  Key concepts: bounded expressiveness (Starlark + CEL), schema-driven programmability,
  K8s-style handler conversion rules with CEL default expressions, endpoint-binding API
  triggers, drain-based in-flight saga migration, FATAL-default error classification.
  Implementation is sequenced into Phases 0-5, with Phase 0 split into 5 independent PRs.
  Throughput target: 5-10k TPS sustainable (correctness over raw speed).
  Refer to handlers.yaml as the ABI, manifest.proto as the program format.
---

# The Meridian Economy Runtime: From Business Description to Running Economy

## Status: READY

## Executive Summary

Meridian is a transaction integrity engine that tenants configure through manifests
(YAML/JSON declarations), Starlark scripts (saga orchestration), and CEL expressions
(validation/pricing). Today these are authored manually and applied via the control plane.
This PRD formalises the system as an **Economy Runtime** and defines the layers needed to
close current gaps and unlock AI-assisted configuration.

**Three new layers:**

1. **Relationship Graph** -- runtime introspection of cross-resource dependencies
2. **Generator** -- AI-assisted generation of manifests from business descriptions
3. **IDE** -- conversational wizard UI for economy creation

**Three foundational improvements (identified during design review):**

1. **Typed Service Modules** -- close the validation gap between Starlark scripts and handler schemas
2. **Handler Evolution** -- K8s-style versioned handlers with inline conversion rules
3. **Trigger Validation** -- cross-reference all trigger types against their respective registries

The tagline: **"Describe your business. We build your economy."**

---

## 1. The Economy Runtime (What We Already Have, Formalised)

### 1.1 The Mental Model

The architecture maps cleanly to compiler and runtime concepts. We use this mapping
throughout as an explanatory framework -- not because Meridian is a traditional VM,
but because the concepts (instruction set, ABI, type checker, linker, loader) describe
exactly what each component does.

| Runtime Concept | Meridian Equivalent | Implementation |
|-----------------|---------------------|----------------|
| **Program** | Manifest (YAML/JSON) | `api/proto/meridian/control_plane/v1/manifest.proto` |
| **Instruction Set** | Starlark (control flow) + CEL (expressions) | Bounded, non-Turing-complete by design |
| **Registers** | Reference Data (instruments, account types, sagas, party types) | `services/reference-data/` tables |
| **Memory** | Operational Data (positions, postings, balances, parties) | Position keeping, financial accounting services |
| **Loader/Linker** | Control Plane (validate -> diff -> plan -> apply) | `services/control-plane/internal/applier/` |
| **Execution Engine** | Saga Orchestrator + Event Bus + Handler Registry | Kafka topics, gRPC handler calls |
| **I/O Ports** | API triggers, webhooks, scheduled jobs, gateway instructions | AsyncAPI channels, operational/financial gateways |
| **Type System** | Handler schemas (`handlers.yaml`), topic registry, instrument dimensions | Validation before deploy |
| **ABI** | Handler schemas (`handlers.yaml`) | Stable interface between scripts and services |

### 1.2 The Instruction Set Design

Meridian's key innovation is **bounded expressiveness** -- the instruction set is intentionally not Turing-complete:

- **Starlark**: No `while` loops, no recursion. All programs terminate. Used for saga orchestration.
- **CEL**: Expression-only, no side effects, sub-millisecond. Used for validation, pricing, eligibility, event filtering.

This is the same design choice Google made for Bazel (Starlark) and Kubernetes admission control (CEL). It means:

- Every program is guaranteed to terminate
- Execution cost is predictable and bounded
- AI can generate code reliably (simpler syntax, fewer failure modes)
- Programs can be statically analysed (relationship extraction)

### 1.3 Termination Guarantees (Two Levels)

**Script-level termination** (Starlark):

- No `while` loops, no recursion
- Runtime enforcement: 5s timeout, 64KB max script, max 3 nested loops
- Mathematical proof: all programs are finite

**System-level termination** (Chain Depth):

- The event router tracks `x-meridian-chain-depth` across saga chains
- When saga A produces an event that triggers saga B, chain depth increments
- At depth 10 (configurable), events are dropped with warning
- Prevents infinite saga chains even when individual sagas terminate

Propagation path: Kafka message header (`x-meridian-chain-depth`) → event router reads
and checks against max → saga executor injects into gRPC metadata → service binding
writes incremented value to outbound Kafka events. The chain depth is available in CEL
filter expressions as `chain_depth` for sagas that need depth-aware behaviour.

Together, these guarantee the runtime is **provably finite** at every level.

### 1.4 The Program Structure

A complete Meridian program (manifest) declares:

```text
Manifest
+-- metadata          (program name, industry, description)
+-- instruments[]     (unit types: GBP, KWH, TONNE_CO2E)
+-- account_types[]   (register types: CUSTOMER, CLEARING, REVENUE)
+-- valuation_rules[] (conversion functions: KWH -> GBP)
+-- sagas[]           (executable logic: Starlark + trigger + filter)
+-- party_types[]     (actor definitions with schema and validation)
+-- mappings[]        (field transformations for external data)
+-- payment_rails[]   (external payment configuration)
+-- operational_gateway (provider connections + instruction routes)
+-- seed_data         (initial reference data)
```

### 1.5 The Execution Model

```text
Event arrives (Kafka / API / Webhook / Schedule)
  -> Topic router matches trigger string
  -> CEL filter evaluates (sub-millisecond gate)
  -> Chain depth check (system-level termination guard)
  -> Idempotency check (deduplicate Kafka redelivery)
  -> Saga orchestrator loads Starlark script
  -> Script executes steps sequentially
    -> Each step calls a handler (gRPC to service modules)
    -> Handlers are type-checked against handlers.yaml schema
    -> Parameters validated and coerced at call boundary
    -> On failure: automatic compensation (LIFO rollback)
  -> Output recorded to event store
```

### 1.6 Script Storage

Saga scripts use the `.star` extension (the convention used by Google's reference
Starlark implementation, cadence-workflow/starlark-worker, and Meridian's existing
cookbook).

**Script storage model:**

All sagas use **inline `script:`** in the manifest today:

```yaml
sagas:
  - name: simple_deposit
    trigger: "api:/v1/deposit"
    script: |
      def execute():
          position_keeping.initiate_log(...)
```

**Platform saga seeding** (existing, well-implemented):

Platform default sagas are versioned `.star` files embedded in the Go binary:

```text
services/reference-data/saga/defaults/
├── deposit/v1.0.0.star          # Platform default deposit saga
├── withdrawal/v1.0.0.star       # Platform default withdrawal saga
├── stripe_payment/v1.0.0.star   # Stripe payment saga
└── payment_execution/v1.0.0.star
```

The seeding pipeline runs at startup and tenant provisioning:

1. **`PlatformSync.SyncPlatformDefaults()`** — loads embedded `.star` files into
   `public.platform_saga_definition` (INSERT-only, version history preserved)
2. **`Seeder.SeedTenant()`** — creates `saga_definition` rows in tenant schema with
   `platform_ref` pointing to platform table (no script copy). Runs as post-provisioning hook.
3. **`CreateTenantOverride`** — tenants replace `platform_ref` with custom script
   (similarity check prevents trivial overrides, creates as DRAFT requiring activation)

At **runtime**, scripts are loaded from `saga_definition` table (per-tenant schema).
Platform sagas resolve via `platform_ref` → `platform_saga_definition` (public schema).
Platform script updates automatically propagate to all tenants using references.

**`script_ref` (Phase 5, future):**

For tenant-authored sagas that are too complex for inline YAML, `script_ref` would allow
referencing a named script:

```yaml
  - name: monthly_fleet_invoice
    trigger: "scheduled:monthly_billing"
    script_ref: "monthly_fleet_invoice.star"  # resolved at apply-time
```

This requires a script upload/storage mechanism (per-tenant script registry). At apply
time, `script_ref` would be resolved to full text and stored inline in the manifest
snapshot — the immutable snapshot property is preserved. `script_ref` is a source-time
convenience, not a runtime indirection. Design details deferred to Phase 5.

### 1.7 Current Tooling (MCP)

The runtime is already operable via MCP tools:

| Tool | Runtime Analogy | Status |
|------|-----------|--------|
| `meridian_manifest_validate` | Type checker | Done |
| `meridian_manifest_plan` | Linker (dry-run) | Done |
| `meridian_manifest_apply` | Loader | Done |
| `meridian_manifest_history` | Version control | Done |
| `meridian_starlark_validate` | Syntax checker | Done |
| `meridian_cel_validate` | Expression checker | Done |
| `meridian_cookbook_discover` | Package manager search | Done |
| `meridian_instrument_describe` | Register inspector | Done |
| `meridian_saga_describe` | Program inspector | Done |

---

## 2. Foundational Improvements (Closing the Gaps)

Before building the Relationship Graph, Generator, and IDE, three foundational gaps must
be closed. These are prerequisite work that makes the higher layers reliable.

### 2.1 Typed Service Modules (Close the Validation Gap)

#### Problem

The manifest validator compiles Starlark scripts but uses **stub modules** that accept
any attribute access. A script calling `position_keeping.nonexistent_handler()` passes
validation today. Handler parameters are not checked against `handlers.yaml` at manifest
validation time.

The typed service modules (`BuildServiceModules` in
`shared/pkg/saga/schema/service_modules.go`) exist and work correctly at **runtime** --
they validate parameters, coerce types, and reject unknown handlers. But the **manifest
validator** doesn't use them. It uses simpler stubs.

#### Solution

Wire the manifest validator to use `BuildServiceModules` (or a validation-only subset) instead of stub modules. This means:

1. Load `handlers.yaml` schema at validator initialisation
2. Build typed Starlark modules from the schema (same as runtime, but without actual gRPC dispatch)
3. Use these modules when compiling Starlark scripts during validation
4. Unknown handlers, wrong parameter names, wrong types, missing required params -- all caught at manifest validate time

#### What This Unlocks

- **Relationship Graph**: If the validator knows which handlers each script calls with
  what params, that's the same data the relationship graph needs. Extract it during
  validation.
- **Handler Evolution**: The validator can detect deprecated handler calls and suggest conversions.
- **Generator Reliability**: The AI generator's output is validated against real handler schemas, not stubs.

#### Design Reference: cadence-workflow/starlark-worker

The cadence-workflow/starlark-worker project uses a **plugin interface** pattern:

```go
type IPlugin interface {
    ID() string                        // "workflow", "uuid", "json"
    Create(info RunInfo) starlark.Value // creates typed Starlark module
    Register(registry worker.Registry) // registers activities
}
```

Each plugin becomes a typed Starlark module where builtins and properties are declared as maps:

```go
var builtins = map[string]*starlark.Builtin{
    "execute_activity": starlark.NewBuiltin("execute_activity", _executeActivity),
}
```

Meridian's `BuildServiceModules` already does this -- it builds
`starlarkstruct.Struct` instances from handler schemas. The gap is only in the
validator not using them.

### 2.2 Handler Evolution (ABI Versioning)

#### Problem

`handlers.yaml` is the ABI -- the contract between Starlark scripts and services. Today
there is informal deprecation (e.g., `currency` param description says "Deprecated: use
instrument_code instead") but no structured versioning. If a handler signature changes,
every deployed Starlark script that calls it may break. The validator cannot detect or
auto-fix deprecated calls.

#### Solution: K8s-Style Inline Conversions

Inspired by Kubernetes API versioning with conversion webhooks:

```yaml
# handlers.yaml - current ABI with inline conversion rules

position_keeping.record_entry:
  version: 2
  description: "Record a position log entry"
  params:
    quantity:
      type: Decimal
      required: true
    instrument_code:
      type: string
      required: true
    side:
      type: enum
      values: [DEBIT, CREDIT]
      required: true
    correlation_id:
      type: string
      required: true
  returns:
    log_id:
      type: string
  compensate: position_keeping.cancel_entry

  # K8s-style conversion from previous version
  conversions:
    - from_version: 1
      from_name: position_keeping.initiate_log
      param_mapping:
        amount: quantity
        currency: instrument_code
        direction: side
      defaults:
        correlation_id: "'auto_' + old_params.account_id"  # CEL expression
      sunset: "3.0"  # manifest version where v1 calls stop being auto-converted
```

**Conversion default expression model:**

Default values in conversion rules use **CEL expressions** evaluated against the old parameter
values. The activation context is `{old_params: {<old param values>}}`. This reuses the same
CEL engine already used for validation/pricing throughout Meridian.

| Default Type | Example | Notes |
|-------------|---------|-------|
| Literal | `"USD"` | Simple string/number constant |
| Rename | `old_params.amount` | Direct field reference (same as `param_mapping`) |
| Computed | `"old_params.first + '_' + old_params.last"` | String concatenation, type coercion |

CEL is the right choice because: (a) already used everywhere in Meridian, (b) pure and
deterministic — no side effects, (c) sub-millisecond evaluation, (d) the same compiler
validates the expression at handler schema load time.

Side-effecting operations (ID generation, timestamps) are **not supported** in conversion
defaults. If a new parameter requires generated values, the handler implementation must
provide a server-side default when the parameter is absent.

**Design principles (borrowed from K8s, Go, and Atlas):**

| Principle | Source | Application |
|-----------|--------|-------------|
| **Internal representation is always current** | K8s API versioning | `handlers.yaml` describes current handlers only, not history |
| **Conversion rules are inline** | K8s conversion webhooks | Conversion lives with the handler definition, not in separate files |
| **Deprecated params get structured annotations** | Go deprecation | `deprecated: true` + `replaced_by:` instead of description text |
| **Hash verification** | Atlas `atlas.sum` | `handlers.sum` file prevents accidental ABI breaks |
| **Mechanical rewriting** | `go fix` | Starlark AST is simple enough for reliable automated transformation |

**Why not separate migration files (Atlas-style)?**

Atlas migrations make sense when the target is **state** (database rows). Handler
migrations target **code** (Starlark scripts inside manifests). The conversion rules
are small (a few lines per handler per version bump) and belong with the handler
definition -- same place you'd look to understand the handler.

#### Mutating Validator Phase (K8s Admission Controller Pattern)

The validator gains a **mutating phase** before the existing validation phase:

```text
Manifest submitted
    |
    v
Mutating phase (auto-convert deprecated handler calls)
  -> Parse Starlark AST
  -> Match handler calls against conversion rules
  -> Rewrite to current handler signatures
  -> Return: { mutated_manifest, conversion_warnings[] }
    |
    v
Validating phase (existing validator, now with typed modules)
  -> Structural validation
  -> Starlark compilation against typed service modules
  -> CEL expression checking
  -> Cross-reference validation
  -> Trigger validation
    |
    v
Clean manifest ready for diff/plan/apply
```

The user never needs to run a separate `meridian fix` command -- conversion is
automatic. But standalone `meridian fix` (or MCP tool `meridian_manifest_fix`) is
available for pre-flight inspection.

### 2.3 Trigger Validation (Cross-Reference All Trigger Types)

#### Problem

The manifest validator validates `event:` triggers against the topics registry but does
not validate other trigger types. The trigger taxonomy is implicit -- spread across proto
regex constraints, frontend parsers, and event router code.

#### Current Validation State

| Trigger Type | Format | Validated Against | Gap |
|-------------|--------|-------------------|-----|
| `event:<topic>` | `event:position-keeping.transaction-captured.v1` | Topics registry (`topics.All()`) | None -- already validated, with "Did you mean?" suggestions |
| `api:<path>` | `api:/v1/deposits` | Regex only (starts with `api:`) | No path uniqueness check, no format validation |
| `webhook:<source>` | `webhook:stripe.payment_intent.succeeded` | Regex only | No validation against provider connections |
| `scheduled:<name>` | `scheduled:monthly_billing` | Regex only | No uniqueness check |

#### Solution: Full Trigger Cross-Reference Validation

**Event triggers** (already done):

- Channel validated against `topics.All()` registry
- CEL filter validated against event filter environment
- Warning issued when event trigger has no filter (`MISSING_EVENT_FILTER`)

**API triggers** (new):

API triggers bind existing gRPC endpoints to saga execution. When a request hits the
endpoint, the gateway's Vanguard transcoder (REST→gRPC) routes to the
`SagaExecutionService.ExecuteSaga` RPC, which dispatches the bound saga. Platform sagas
provide default bindings; tenants can override them via `CreateTenantOverride`.

- Path must reference a real gRPC endpoint: validate against the generated OpenAPI spec
  (`api/openapi/meridian.swagger.json`, produced by `buf generate` via
  `buf.build/grpc-ecosystem/openapiv2`). This mirrors how event triggers are validated
  against the topics registry.
- Path uniqueness: no two sagas may declare the same `api:` path within a manifest
- Path format: must start with `/`, valid URL path characters
- Configuration: `buf.gen.yaml` already defines the OpenAPI generator; `api/openapi/`
  is the output directory

**Gateway route sync**: The API Gateway needs to know which `(tenant_id, path)` pairs
are bound to sagas. Since bindings change when manifests are applied, the gateway must
watch or cache the active saga bindings from reference data. Implementation options:

- **Cache with invalidation**: Gateway maintains a local `(tenant_id, path) → saga_name`
  cache, invalidated on manifest apply via event notification
- **Lookup on miss**: Gateway queries reference data on cache miss, caches result with TTL
- **Hot path**: Cache hit = no DB lookup. Cache miss (new tenant, new binding) = single
  lookup, then cached

This mechanism must be part of Phase 0 (trigger validation) since API trigger validation
is meaningless if the gateway cannot route to the bound saga at runtime.

**Webhook triggers** (new):

- Source must match a registered provider connection in the manifest's `operational_gateway.provider_connections[]`
- Example: `webhook:stripe.payment_intent.succeeded` requires a provider connection
  with `connection_id` matching `stripe` (or configurable mapping)
- This prevents webhook triggers that reference non-existent external providers

**Scheduled triggers** (new):

- Schedule name uniqueness across all sagas
- Future: validate schedule expression format (cron, `monthly`, `daily`, etc.)

**Instruction route validation** (enhance existing):

- Already validates: `connection_id` references existing provider connection, mapping references
- Add: validate instruction type naming conventions
- Add: warn on instruction routes without corresponding saga triggers

#### Data Sources for Validation

The validator can cross-reference against multiple registries:

| Registry | Location | What It Validates |
|----------|----------|-------------------|
| **Topics registry** | `shared/platform/events/topics/topics.go` | Event trigger channels |
| **AsyncAPI specs** | `api/asyncapi/*.yaml` | Event payload schemas for CEL filter field validation |
| **OpenAPI spec** | `api/openapi/meridian.swagger.json` (generated by `buf generate`) | API trigger endpoint paths |
| **JSON Schema** | `api/jsonschema/` (generated by `buf generate --template buf.gen.jsonschema.yaml`) | Manifest structural validation |
| **Provider connections** | Manifest's `operational_gateway.provider_connections[]` | Webhook trigger sources |
| **Instruction routes** | Manifest's `operational_gateway.instruction_routes[]` | Instruction type references |
| **Handler schemas** | `shared/pkg/saga/schema/handlers.yaml` | Starlark handler calls |
| **Manifest itself** | Current manifest being validated | Cross-references between instruments, account types, sagas |

#### AsyncAPI-Driven CEL Field Validation

The AsyncAPI specs define event payload schemas. When a saga has:

```yaml
trigger: "event:position-keeping.transaction-captured.v1"
filter: "event.instrument_code == 'KWH' && event.direction == 'DEBIT'"
```

The validator can load the AsyncAPI schema for
`position-keeping.transaction-captured.v1` and verify that `instrument_code` and
`direction` are actual fields in the event payload. This catches typos in CEL filter
expressions that reference non-existent event fields.

### 2.4 Validation Coverage Matrix

This matrix captures every validation that should exist. Checked items are implemented
today; unchecked are gaps to close in Phase 0.

**Structural Validation:**

- [x] Proto field constraints (buf.validate)
- [x] Duplicate code/name detection
- [x] Immutability enforcement (codes cannot change between versions)
- [x] CEL expression compilation and type-checking
- [x] Starlark script syntax parsing
- [ ] Starlark handler call validation against handlers.yaml (stub modules today)
- [ ] Starlark handler parameter type/required validation at manifest validation time
- [ ] Handler deprecation detection and auto-conversion

**Cross-Reference Validation:**

- [x] Account type `allowed_instruments` references existing instruments
- [x] Valuation rule `from_instrument`/`to_instrument` references existing instruments
- [x] Instruction route `connection_id` references existing provider connection
- [x] Instruction route `fallback_connection_id` references existing provider connection
- [x] Instruction route mapping references existing mappings
- [ ] Saga script `instrument_code` params reference existing instruments (requires typed modules)
- [ ] Saga script `account_id` references existing account types (where literal)

**Trigger Validation:**

- [x] Event trigger channel exists in topics registry (`topics.All()`)
- [x] Event trigger "Did you mean?" suggestions (Levenshtein matching)
- [x] Event trigger without filter produces warning (`MISSING_EVENT_FILTER`)
- [ ] API trigger path uniqueness across all sagas
- [ ] API trigger path references real gRPC endpoint (validated against `api/openapi/meridian.swagger.json`)
- [ ] Webhook trigger source validation against provider connections
- [ ] Scheduled trigger name uniqueness
- [ ] CEL filter field validation against AsyncAPI event payload schemas

**Gateway Validation:**

- [x] Duplicate instruction type detection
- [x] Provider connection required fields
- [ ] Instruction type naming convention enforcement
- [ ] Instruction route without corresponding saga trigger (orphan warning)
- [ ] Provider connection without any instruction routes (unused warning)

**ABI Validation:**

- [x] Handler schema YAML parsing and type definitions
- [x] Runtime parameter validation (in service_modules.go)
- [ ] Pre-deploy parameter validation (in manifest validator)
- [ ] Handler version tracking and conversion rules
- [ ] `handlers.sum` hash verification

### 2.5 Validation Error Model

Every validation failure returns a structured error with enough context for both
humans and AI to resolve the issue without guessing:

```json
{
  "errors": [
    {
      "code": "UNKNOWN_HANDLER",
      "severity": "ERROR",
      "location": "sagas[0].script:12",
      "message": "Unknown handler: position_keeping.nonexistent_method",
      "suggestion": "Did you mean: position_keeping.initiate_log?",
      "available": ["initiate_log", "cancel_entry", "query_balance"]
    }
  ],
  "warnings": [
    {
      "code": "DEPRECATED_HANDLER",
      "severity": "WARNING",
      "location": "sagas[1].script:8",
      "message": "Deprecated handler: position_keeping.initiate_log → position_keeping.record_entry",
      "auto_converted": true
    }
  ]
}
```

**Error categories and expected messages:**

| Category | Code | Example Message |
|----------|------|-----------------|
| **Structural** | `UNKNOWN_HANDLER` | Handler `X` not found. Available: [...] |
| **Structural** | `MISSING_REQUIRED_PARAM` | Handler `X` requires param `Y` (type: Decimal) |
| **Structural** | `WRONG_PARAM_TYPE` | Param `amount` expects Decimal, got string |
| **Structural** | `UNKNOWN_PARAM` | Handler `X` has no param `Y`. Available: [...] |
| **Cross-reference** | `DANGLING_INSTRUMENT_REF` | Account type `FLEET_RECEIVABLE` references instrument `XYZ` which does not exist |
| **Cross-reference** | `INSTRUMENT_IN_USE` | Cannot remove instrument `GBP`: referenced by account types [FLEET_RECEIVABLE, CHARGING_REVENUE] and 2 sagas |
| **Cross-reference** | `ACCOUNT_TYPE_IN_USE` | Cannot remove account type `X`: referenced by sagas [record_charging_session] |
| **Trigger** | `UNKNOWN_EVENT_TOPIC` | Event topic `X` not found. Did you mean: `Y`? |
| **Trigger** | `UNKNOWN_API_ENDPOINT` | API path `/v1/nonexistent` does not match any gRPC endpoint |
| **Trigger** | `DUPLICATE_TRIGGER` | API path `/v1/deposits` already bound to saga `X` |
| **Trigger** | `ORPHAN_PROVIDER` | Provider connection `stripe` has no instruction routes |
| **CEL** | `UNKNOWN_EVENT_FIELD` | CEL filter references `event.typo_field` not found in AsyncAPI schema |
| **Deprecation** | `DEPRECATED_HANDLER` | Handler `X` deprecated, auto-converted to `Y` |
| **Deprecation** | `DEPRECATED_PARAM` | Param `amount` deprecated, auto-converted to `quantity` |

**Destructive change detection**: When a manifest update removes or modifies a resource
that other resources depend on, the validator reports the full dependency chain. Removing
instrument `GBP` when account types and sagas reference it is an error, not a silent drop.
The relationship graph (Phase 1) makes these cross-references exhaustive.

---

## 3. The Relationship Graph (Layer 1 -- Runtime Introspection)

### 3.1 Problem

The relationships between resources are currently **implicit**:

- "Which sagas write to account type X?" -- grep through Starlark scripts
- "Which events trigger saga Y?" -- parse the trigger string
- "Which instruments does valuation rule Z convert between?" -- inspect handler params
- "What happens when event E fires?" -- manual trace through manifests

This makes the system opaque. A debugger/inspector for the runtime is missing.

### 3.2 Solution: Manifest Relationship Index

When a manifest is validated (or applied), extract and index the relationships:

```text
saga -> triggers_on -> event channel
saga -> reads_from -> account_type (via handler calls in Starlark)
saga -> writes_to -> account_type (via handler calls in Starlark)
saga -> uses_instrument -> instrument (via handler params)
saga -> calls_handler -> handler (via typed service module calls)
account_type -> denominated_in -> instrument
valuation_rule -> converts -> (instrument_from, instrument_to)
event -> produced_by -> service (from AsyncAPI channel naming)
webhook_trigger -> requires -> provider_connection
instruction_route -> uses -> provider_connection
instruction_route -> transforms_with -> mapping
```

### 3.3 Extraction Method

**From typed service modules** (primary, replaces regex parsing):

Once the validator uses `BuildServiceModules` (section 2.1), handler calls are
intercepted at the Starlark level. Each call provides:

- Handler name (e.g., `position_keeping.initiate_log`)
- Parameter names and values (where literal)
- Whether values are literal strings or dynamic expressions

This is strictly more accurate than regex parsing. Dynamic values are flagged as
`dynamicTargets` in the graph -- relationships that exist but whose exact value depends
on runtime data.

**From trigger parsing** (existing `parseTriggerService`):

The frontend's `star-parser.ts` already extracts the producing service from trigger
strings. The backend equivalent builds the event-to-saga index.

**From CEL analysis**:

Parse CEL expressions to extract referenced fields, giving insight into what event data a saga depends on.

**Completeness guarantee**: For literal handler params, the graph is **complete** -- it
captures every relationship. For dynamic params (computed strings like
`"FLEET_" + customer_type`), the graph marks the relationship as **dynamic** with the
code snippet. Impact analysis should treat dynamic relationships as "possibly affected."

### 3.4 Storage

**Decision: embedded in manifest version record.** Built when manifest is applied (or
validated in dry-run mode), stored as a JSON column alongside the manifest version snapshot.
Queryable via gRPC/REST and MCP tool.

Rebuilt on each manifest apply -- the graph is a derived artifact, not a primary data source.

### 3.5 UI Integration

Surface relationships contextually in the existing frontend:

- **Account type detail page** -- "Sagas that touch this account" panel
- **Saga detail page** -- "Triggered by" + "Reads/writes" + "Calls handlers" panels
- **Instrument detail page** -- "Used by account types" + "Valuation rules" panels
- **Manifest graph** -- Full dependency visualization (partially exists)

### 3.6 What This Unlocks

- **Impact analysis**: "If I change instrument X, what breaks?" -- query the graph
- **The generator**: AI can inspect the graph to understand the current economy before modifying it
- **The IDE**: Show relationships as the user builds, not just after deploy
- **Handler evolution**: "These 4 sagas call the deprecated handler" -- found via graph, fixed via mutating validator

---

## 4. The Generator (Layer 2 -- AI-Assisted Generation)

### 4.1 Philosophy: shadcn, Not npm

The generator follows the [shadcn/ui](https://ui.shadcn.com/) philosophy:

- **Not a library** -- you don't `import` patterns at runtime
- **Copy and own** -- output is your manifest, your Starlark, your CEL
- **Registry for discovery** -- cookbook patterns are examples and inspiration
- **Modify freely** -- no abstraction between you and the generated code

The cookbook is context for the LLM, not a template engine. Patterns are dissolved into the output.

### 4.2 Generation Pipeline

```text
Source (business description in natural language)
    |
    v
Lexer/Parser (AI extracts: industry, instruments, pricing model,
              settlement terms, compliance requirements)
    |
    v
Semantic Analysis (AI matches to cookbook patterns as guidance,
                   resolves instrument dependencies,
                   checks handler availability via handlers.yaml)
    |
    v
Code Generation (AI produces: manifest YAML, .star scripts,
                 CEL expressions)
    |
    v
Mutating Phase (auto-convert any deprecated handler calls)
    |
    v
Type Checking (meridian_manifest_validate -- typed modules, full cross-reference)
    |
    v
Linking (meridian_manifest_plan -- resolves against current state,
         produces diff)
    |
    v
Output (complete, self-contained program ready to load)
```

### 4.3 Generation Context (What the AI Needs)

For reliable generation, the AI needs these as context:

| Context | Source | Purpose |
|---------|--------|---------|
| Manifest proto schema | `manifest.proto` | What fields exist, types, constraints |
| Handler schemas | `handlers.yaml` | What service calls are valid, parameter types |
| Topic registry | `topics.All()` | What event channels exist for triggers |
| AsyncAPI specs | `api/asyncapi/*.yaml` | Event payload schemas |
| Cookbook patterns | `cookbook/patterns/*/` | Few-shot examples (not templates) |
| Current economy state | Relationship graph (Layer 1) | What already exists (for incremental changes) |
| Instrument dimensions | Reference data | Valid dimension types, precision constraints |

### 4.4 Generation Modes

#### "Build My Economy" (Interactive Generation)

1. User describes business
2. AI asks clarifying questions (instruments? pricing? settlement?)
3. AI generates manifest draft
4. User reviews in editor (full ownership -- shadcn style)
5. User modifies if needed
6. Validate -> Plan -> Apply

#### "I'm Feeling Lucky" (Single-Pass Generation)

1. User provides minimal description ("EV charging UK")
2. AI makes all decisions using cookbook defaults
3. Generates, validates, plans in one pass
4. Shows result with "Customise" option
5. "I'm Feeling Lucky" always stops at preview for non-empty economies (plan output shown for approval)

#### "Amend" (Incremental Generation)

1. User has a running economy
2. User says "add carbon credit tracking"
3. AI reads current manifest + relationship graph
4. Mutating phase auto-converts any deprecated handler calls in existing scripts
5. AI generates additions (new instruments, account types, sagas)
6. Shows impact analysis via relationship graph
7. User approves -> apply

### 4.5 Error Recovery

The generator's type checker (manifest validator with typed modules) produces structured errors with:

- Location paths (`sagas[0].script`, `instruments[2].code`)
- Severity levels (error blocks apply, warning allows)
- Suggested fixes ("Did you mean...?")
- Available fields (for unknown handler params or event channels)

The AI generation loop: generate -> validate -> if errors, fix and re-validate -> until clean.

### 4.6 What We DON'T Build (v1)

- **Libraries/imports** -- no shared Starlark modules. Every economy is self-contained. Copy-paste is the model.
- **Template engine** -- no Jinja/Mustache for manifests. AI generates directly.
- **Package manager** -- no `meridian install carbon-offset`. The cookbook is reference material, not installable packages.

These are v2 concerns. The runtime metaphor supports them, but copy-paste (shadcn) is the right first step.

---

## 5. The IDE (Layer 3 -- Wizard UI)

### 5.1 Where the Conversation Happens

**Both built-in and MCP-native:**

- **Built-in chat UI** in the Meridian frontend -- the "I'm Feeling Lucky" moment.
  Controlled experience, optimised for first-time users.
- **MCP tools** remain the API -- any AI client (Claude Code, ChatGPT, custom agents)
  can generate manifests via the same tools.

The built-in UI uses the MCP tools under the hood. It's a frontend to the generator, not a separate system.

### 5.2 Core Screens

#### 5.2.1 The Prompt

```text
+------------------------------------------------------+
|  Describe your business...                           |
|  +----------------------------------------------+   |
|  | I run an EV charging network across 50 sites  |   |
|  | in the UK and need to bill fleet customers    |   |
|  | monthly...                                    |   |
|  +----------------------------------------------+   |
|  [Build My Economy]              [I'm Feeling Lucky]    |
+------------------------------------------------------+
```

#### 5.2.2 The Conversation (Build My Economy mode)

AI asks clarifying questions with clickable options. Each answer refines the generation target.

#### 5.2.3 The Preview (shadcn moment)

Show the generated manifest in an editable format:

- Left panel: manifest YAML with syntax highlighting
- Right panel: economy graph (instruments -> account types -> sagas)
- Inline validation errors from the type checker
- Relationship graph showing how resources connect
- Conversion warnings if deprecated handler calls were auto-fixed

**This is the "you own this" moment.** The user sees the actual code, not an abstracted summary.

#### 5.2.4 The Deploy

- Validate -> Plan diff -> Show what will be created
- One-click deploy (for new economies)
- Plan review required (for existing economies with changes)
- Post-deploy: live economy dashboard with relationship graph

### 5.3 The Editor Experience

Since the user owns the output (shadcn), the editor is critical:

- **Manifest YAML** -- syntax highlighting, schema-aware autocomplete
- **Starlark (.star)** -- highlighting, handler auto-complete from `handlers.yaml`
- **CEL** -- inline validation, field autocomplete from AsyncAPI event schemas
- **Live validation** -- errors appear inline as you type (via `meridian_manifest_validate`)
- **Relationship preview** -- graph updates as you edit

### 5.4 Iteration Loop

After initial deploy, the same IDE supports:

- "Add a new saga" -> AI generates, user reviews in editor, apply
- "Change pricing model" -> AI finds the relevant CEL/valuation rule, modifies, shows diff
- "What happens if I remove instrument X?" -> relationship graph shows impact

---

## 6. Economy Simulator (What-If Impact Analysis)

### 6.1 The Vision

A tenant changes one part of their economy — a pricing strategy, a tariff, a bucketing
rule — and sees the financial impact **before deploying**. What would margins look like?
Which counterparties become unprofitable? This is the killer feature for operational
finance: simulate the new economy against existing data.

### 6.2 Existing Simulation Infrastructure

Meridian already has read-only simulation tools:

| Tool | What It Does | Limitation |
|------|-------------|------------|
| `meridian_saga_simulate` | Traces Starlark execution with stubbed service calls | Single saga, no real data |
| `meridian_valuation_simulate` | Dry-runs a valuation method | Single valuation, no batch |
| `meridian_cel_evaluate` | Evaluates CEL expressions with injected variables | Expression-level only |
| `meridian_manifest_plan` | Diffs manifest changes against current state | Structural diff, no financial impact |

**Forecasting service** (`services/forecasting/`) executes Starlark strategies against
market data to produce forward curves. Currently runs fixed strategies on schedule — not
scenario-driven.

### 6.3 The Gap: Scenario-Driven Impact Analysis

None of the existing tools answer: "If I change my economy rules, what happens to my
financial performance across historical data?"

| Missing Capability | Example |
|-------------------|---------|
| Batch re-valuation | Re-run 10k historical postings with new pricing |
| Position restatement | "What would balances be if bucketing rule changed?" |
| Scenario parametrisation | "If fee tier = 5% instead of 3%?" |
| Delta reporting | "Impact: +£2.5M revenue, -3 counterparties profitable" |
| Reconciliation replay | "How would settlement variances change?" |

### 6.4 Design: Economy Replay Engine

The bitemporal data model makes this architecturally possible. Every position, posting,
and valuation is timestamped with both valid-time and transaction-time. The quality ladder
(ESTIMATE → PROVISIONAL → ACTUAL → VERIFIED) means the system already handles multiple
versions of truth.

```text
Economy Replay Request:
  manifest_changes: <modified manifest YAML>  -- the "what-if"
  replay_scope:
    date_range: [2025-01-01, 2025-12-31]
    accounts: [FLEET_RECEIVABLE, ENERGY_DELIVERED]  -- or all
    instruments: [GBP, KWH]  -- or all
  comparison: CURRENT_ECONOMY  -- baseline

Economy Replay Pipeline:
  1. Validate modified manifest (full type-checking)
  2. Extract changed valuation rules / saga logic / bucketing rules
  3. Query historical postings within scope (bitemporal read)
  4. Re-evaluate each posting against new rules:
     - New valuation method → recalculate amounts
     - New bucketing rule → recompute position assignments
     - New saga logic → trace execution path (stubbed)
  5. Compute deltas: (new_result - original_result) per posting
  6. Aggregate into impact report
```

**Forecasting service integration**: The forecasting service already reads market data
and produces forward curves. Extend it to accept scenario parameters (e.g.,
"demand + 10%") and produce scenario-specific curves that feed into the replay engine.

### 6.5 Impact Report

```text
Economy Impact Summary:
  Date range: 2025-01-01 to 2025-12-31
  Postings re-evaluated: 47,832
  Changed rules: kwh_to_gbp_peak (valuation method)

  Revenue impact: +£2.53M (+4.2%)
  Margin impact: +1.8pp (from 12.3% to 14.1%)

  Counterparty breakdown:
    FLEET_ALPHA: +£890k (profitable → more profitable)
    FLEET_BETA:  -£120k (marginal → unprofitable)  ⚠️
    FLEET_GAMMA: +£340k (unprofitable → profitable) ✓

  Position impact:
    ENERGY_DELIVERED: -12,400 KWH (bucketing change)
    CHARGING_REVENUE: +£2.53M GBP (pricing change)
```

### 6.6 Implementation Approach

This is a **Phase 3-4 feature** that builds on earlier phases:

- Phase 0: Typed service modules (required for accurate simulation)
- Phase 1: Relationship graph (identifies what changes affect which accounts)
- Phase 2: Generator (produces modified manifests from natural language)
- **Phase 3-4: Economy Simulator** (replays modified economy against historical data)

The existing `meridian_valuation_simulate` and forecasting infrastructure provide the
foundation. The new work is batch orchestration, delta computation, and impact reporting.

---

## 7. Operational Resilience

### 7.1 Error Taxonomy (What Exists)

Meridian classifies saga errors into two categories that drive automatic behaviour:

| Classification | Behaviour | Examples |
|---------------|-----------|---------|
| **FATAL** | Immediate compensation (LIFO rollback) | Insufficient funds, account closed, validation failed, business rule violation |
| **TRANSIENT** | Retry with backoff, release lease | Timeout, connection refused, deadlock, rate limit, circuit breaker |

**Default: FATAL.** Unknown errors are treated as fatal (fail-safe). This prevents
infinite retries on unexpected errors.

**Zombie detection**: Sagas exceeding max replay count (default 5) transition to
`FAILED_MANUAL_INTERVENTION` status.

### 7.2 Compensation Model (LIFO Rollback)

When a FATAL error occurs at step N:

```text
Forward:  Step A → Step B → Step C (FATAL error)
Rollback: Compensate C → Compensate B → Compensate A  (reverse order)
```

- Only steps with a `CompensateHandler` defined are compensated
- Compensation params derived automatically from forward step output (`*_id` pattern)
- **Best-effort**: if one compensation fails, remaining compensations still execute
- All compensation errors collected and reported

### 7.3 Dead Letter Queue

Failed Kafka messages (after exhausting retries) land in a per-topic DLQ with rich
metadata:

| DLQ Header | Purpose |
|-----------|---------|
| `dlq.original_topic` | Source topic for traceability |
| `dlq.error_message` | Human-readable failure reason |
| `dlq.retry_count` | Attempts made before DLQ |
| `dlq.correlation_id` | End-to-end tracing |
| `dlq.first_failure_time` | When it first failed |

**DLQ Inspector API** exists (`shared/platform/kafka/dlq_admin.go`) with filtering,
statistics, and replay capabilities. Individual or batch replay back to original topic.

### 7.4 Gap: Operator Dashboard

The APIs exist but there is no UI for operators to see and resolve failures:

| Capability | API Status | UI Status |
|-----------|-----------|-----------|
| Saga execution history | Causation tree API (gRPC) | Timeline component exists, limited |
| Failed saga list | Queryable via saga admin API | No dedicated view |
| DLQ inspection | DLQ Inspector API | No UI |
| DLQ replay | DLQ Replay API | No UI |
| Zombie saga detection | Prometheus metric (`saga_zombie_detected_total`) | Alert only |
| Error classification breakdown | Metrics exist | No dashboard |

**Needed**: An operations dashboard that surfaces:

- Failed/compensated/zombie sagas with error details and causation tree
- DLQ messages with one-click replay
- Error trend charts (by saga, by error classification, by tenant)
- Resolution workflow: investigate → fix root cause → replay from DLQ

### 7.5 In-Flight Saga Migration

**Decision: Drain.** Running sagas complete on the version they started with. New saga
instances use the new manifest version immediately. The event router's `Reload()`
atomically swaps the registry, so new events use the new version while in-flight sagas
continue with their existing execution plan.

This works because saga instances already persist their step results and handler params.
A running saga doesn't re-read the manifest mid-execution — it follows its persisted
execution plan. The overlap period is the time for the longest running saga to complete
(typically seconds to minutes, bounded by Starlark's guaranteed termination).

Alternatives considered:

| Option | Why Not |
|--------|---------|
| **Immediate switchover** | Risk of mid-execution schema mismatch. Only viable if handler ABI is backward-compatible. |
| **Versioned execution** | Each saga instance records its manifest version. Most correct, highest complexity. Deferred to future if drain proves insufficient. |

---

## 8. Observability

### 8.1 Current Stack

Meridian has a complete Grafana-based observability stack:

| Component | Purpose | Integration |
|-----------|---------|-------------|
| **Grafana Alloy** | OTel collector (OTLP gRPC :4317) | Receives traces/metrics from all services |
| **Grafana Tempo** | Distributed tracing | Spans for every gRPC call, saga step, handler execution |
| **Grafana Loki** | Log aggregation | JSON structured logs with tenant_id, correlation_id, trace_id |
| **Prometheus** | Metrics | Per-tenant request counts, latencies, DB query durations |
| **Grafana** | Dashboards | Visualization at localhost:3000 (dev) |

**OpenTelemetry integration**: W3C Trace Context propagation across service boundaries.
Every request carries trace_id and span_id through gRPC interceptors. Logs automatically
include trace context for correlation.

**Per-tenant metrics**: All Prometheus metrics include a `tenant` label. Cardinality
managed via route patterns (not actual paths).

### 8.2 Gap: Saga Flow Tracing

The tracing infrastructure exists but lacks saga-specific visualization:

- **Have**: Individual gRPC call spans, handler execution spans
- **Need**: End-to-end saga flow view showing step sequence, handler calls, compensation
  path, and produced events as a single trace waterfall
- **Need**: Saga-specific Grafana dashboards (execution duration by saga type, error rates,
  compensation frequency, DLQ depth)

The causation tree API (`GetCausationTree`) provides the data model — what's missing is
the dashboard that renders it alongside OTel traces.

---

## 9. Tenant Isolation Model

### 9.1 Current Architecture

| Layer | Isolation Mechanism |
|-------|-------------------|
| **Database** | Schema-per-tenant (`org_{tenant_id}`). `SET LOCAL search_path` scoped to transaction. |
| **API Gateway** | Tenant resolved from subdomain or `X-Tenant-Slug` header. Injected into gRPC context. |
| **Observability** | All metrics/logs tagged with `tenant_id`. Per-tenant query filtering. |
| **Deployment (demo)** | Single Go binary, all services in-process. Shared pod. |
| **Deployment (K8s)** | Separate pods per service. Resource limits per pod (CPU/memory). |

### 9.2 Scaling Model

- **Demo/single-binary**: Vertical scaling only. Suitable for dev and small deployments.
- **K8s production**: Horizontal pod autoscaling per service. Each service independently
  scalable. Resource limits enforced (e.g., tenant service: 50m-200m CPU, 64-256Mi memory).

### 9.3 Gap: Per-Tenant Rate Limiting

No per-tenant throttling exists today. A noisy tenant could consume disproportionate
resources. The gateway middleware pattern supports adding rate limiting — this is an
implementation task, not an architectural change.

## 10. Security Model

### 10.1 Manifest Authorisation

Manifest operations (validate, plan, apply) are gated by the tenant isolation layer
but lack fine-grained role-based access control. The staff model (`UserEntity.Role`)
defines an `operator` default role but does not enforce permissions on manifest apply.

**Current state**: Any authenticated caller within a tenant context can apply manifests.
Tenant isolation (schema-per-tenant) prevents cross-tenant access, but within a tenant,
all staff have equal manifest privileges.

**Required (Phase 0)**:

| Operation | Required Role | Rationale |
|-----------|--------------|-----------|
| `ManifestValidate` | `viewer` | Read-only, safe to allow broadly |
| `ManifestPlan` | `editor` | Shows diff, no side effects |
| `ManifestApply` | `admin` | Mutates running economy |
| `CreateTenantOverride` | `admin` | Replaces platform saga with custom script |

Implementation: gRPC interceptor checks `UserEntity.Role` against required role for
each RPC. The API key `Scopes` field (`pq.StringArray`) already supports scoped access.

### 10.2 Saga Runtime Isolation

Saga execution enforces tenant isolation via `PartyScope` in `RunnerInput`. All handler
calls within a saga inherit the tenant context, and all database queries use
`WithGormTenantTransaction(ctx, ...)` to apply schema-per-tenant scoping. No saga can
access data from another tenant's schema.

**Starlark sandbox**: Saga scripts run in Starlark's sandboxed interpreter with no
filesystem, network, or OS access. The only external operations available are the typed
service module calls (which enforce tenant scope) and pure CEL expressions.

---

## 11. Testing Strategy

### 11.1 Per-Phase Testing

| Phase | Test Type | What It Validates |
|-------|-----------|-------------------|
| Phase 0 | Unit + integration | Typed modules reject invalid handler calls; trigger validation catches bad paths; CEL field validation warns on typos; conversion rules rewrite deprecated calls |
| Phase 1 | Integration | Graph extraction produces correct relationships; impact analysis traces transitive dependencies |
| Phase 2 | Integration + golden files | Generator produces valid manifests from business descriptions; generated manifests pass validation; golden file comparison for regression |
| Phase 3 | E2E (Playwright) | IDE wizard produces deployable economies; editor validation fires inline; deploy flow works end-to-end |
| Phase 3.5 | Integration | Operator dashboard surfaces failed sagas; DLQ replay works end-to-end |
| Phase 4 | Integration + benchmark | Simulator replays historical postings accurately; impact reports match manual calculation; batch performance within acceptable bounds |

### 11.2 Manifest Validation Test Matrix

The manifest validator is the critical safety gate. Every validation rule requires
both a positive test (valid manifest passes) and a negative test (invalid manifest
is rejected with the correct structured error).

```text
For each validation rule:
  1. Happy path: valid manifest passes without warnings
  2. Rejection: invalid input rejected with structured error (code, location, severity, suggestion)
  3. Boundary: edge cases (empty arrays, max lengths, special characters)
  4. Conversion: deprecated calls auto-converted with warning (not error)
  5. Error message quality: verify suggestion text and available alternatives are present
```

### 11.3 Destructive Change Scenarios (Unhappy Path)

Manifest updates that remove or modify resources must be validated against
dependencies. Each scenario tests that the validator blocks the change with
a clear error describing what depends on the removed resource.

| Scenario | Expected Error | Code |
|----------|---------------|------|
| Remove instrument referenced by account type | "Cannot remove instrument `GBP`: referenced by account types [FLEET_RECEIVABLE]" | `INSTRUMENT_IN_USE` |
| Remove account type referenced by saga | "Cannot remove account type `ENERGY_DELIVERED`: referenced by sagas [record_charging_session]" | `ACCOUNT_TYPE_IN_USE` |
| Remove provider connection with active instruction routes | "Cannot remove provider `stripe`: 3 instruction routes depend on it" | `PROVIDER_IN_USE` |
| Change instrument dimensions breaking existing positions | "Instrument `KWH` dimension change from ENERGY to VOLUME: 847 existing positions use ENERGY" | `DIMENSION_CHANGE_BLOCKED` |
| Remove saga that another saga triggers via event chain | "Saga `settle_payment` produces event consumed by saga `reconcile_settlement`" | `EVENT_CHAIN_BREAK` |
| Add handler call with wrong parameter types | "Handler `initiate_log` param `amount`: expected Decimal, got string" | `WRONG_PARAM_TYPE` |
| Reference nonexistent event topic in trigger | "Event topic `position-keeping.typo.v1` not found. Did you mean: `position-keeping.transaction-captured.v1`?" | `UNKNOWN_EVENT_TOPIC` |

**Phase dependency**: Full cross-reference validation (event chain breaks, transitive
dependencies) requires the Relationship Graph (Phase 1). Phase 0 covers direct
references only (instrument ↔ account type, handler ↔ params).

### 11.4 Simulator Accuracy Verification

The economy simulator (Phase 4) requires verification that replay results match
actual historical outcomes when run with unchanged rules (identity test). This
establishes a baseline before testing modified rules.

---

## 12. Migration Path

### 12.1 Existing Tenants

Tenants deployed before typed service modules can continue operating without changes.
The migration path is incremental:

1. **Phase 0 deploy**: Existing manifests re-validated against typed modules on next
   `ManifestApply`. If validation fails, the apply is blocked with structured errors
   showing exactly which handler calls need updating.
2. **Conversion rules handle most cases**: Deprecated handler calls (e.g., `initiate_log`
   → `record_entry`) are auto-converted by the mutating validator phase. Tenants see
   conversion warnings but their manifests still apply.
3. **No forced migration**: Tenants are not required to update their manifests until
   they choose to apply a new version. Running economies continue unaffected.

### 12.2 Platform Saga Seeding

New tenants receive platform default sagas via `SeedTenant()`, which creates
`saga_definition` rows with `platform_ref` (pointer to `platform_saga_definition`,
no script copy). Tenants override via `CreateTenantOverride`, which replaces the
`platform_ref` with a custom script (similarity check < 90%, created as DRAFT).

Existing tenants without seeded sagas can be seeded retroactively — `SeedTenant()`
is idempotent (`ON CONFLICT DO NOTHING`).

---

## 13. Compiler Theory Applied to Meridian

### 13.1 Lessons from Runtime/Compiler Design

| Compiler Concept | Meridian Application |
|------------------|---------------------|
| **Lexical analysis** | Parse business description into domain tokens (instruments, pricing models, settlement terms) |
| **Syntax analysis** | Structure tokens into manifest sections (instruments[], sagas[], account_types[]) |
| **Semantic analysis** | Validate references (saga uses instrument that exists, trigger references valid topic) |
| **Type checking** | Manifest validator with typed service modules + CEL type-check + Starlark compilation |
| **Optimisation** | Simplify redundant sagas, merge similar account types, suggest common patterns |
| **Code generation** | Produce manifest JSON + `.star` scripts + CEL expressions |
| **Linking** | Manifest plan -- resolve against current state, produce execution plan |
| **Loading** | Manifest apply -- install into the runtime (reference data service) |
| **Debug symbols** | Relationship graph -- map from running state back to source declarations |
| **REPL** | The IDE -- edit, validate, plan, apply in a tight loop |
| **Standard library** | Cookbook patterns -- pre-written, tested, copy-and-modify |
| **ABI** | Handler schemas (`handlers.yaml`) -- versioned, with conversion rules |
| **ISA** | Starlark + CEL -- the instruction set. Bounded, deterministic, analysable |
| **Mutating pass** | Auto-convert deprecated handler calls before validation (K8s admission controller pattern) |
| **Deprecation** | Structured handler deprecation with mechanical rewrite rules |

### 13.2 What a "Meridian Program" Looks Like

```yaml
# This is a complete, self-contained Meridian program.
# No imports, no external dependencies, no template references.
# You own every line. Modify freely.

version: "1.0"
metadata:
  name: "Acme EV Charging"
  industry: energy
  description: "Fleet EV charging with time-of-use billing"

instruments:
  - code: GBP
    type: FIAT
    dimensions: { unit: GBP, precision: 2 }
  - code: KWH
    type: COMMODITY
    dimensions: { unit: kWh, precision: 3 }

account_types:
  - code: FLEET_RECEIVABLE
    normal_balance: DEBIT
    behavior_class: CUSTOMER
    allowed_instruments: [GBP]
  - code: ENERGY_DELIVERED
    normal_balance: DEBIT
    behavior_class: CLEARING
    allowed_instruments: [KWH]
  - code: CHARGING_REVENUE
    normal_balance: CREDIT
    behavior_class: REVENUE
    allowed_instruments: [GBP]

party_types:
  - code: FLEET_OPERATOR
    schema:
      company_name: { type: string, required: true }
      fleet_size: { type: integer }
    validation: "attributes.fleet_size > 0"

valuation_rules:
  - name: kwh_to_gbp_peak
    from_instrument: KWH
    to_instrument: GBP
    method: TIME_OF_USE
    source: "rate_lookup('peak', observation.timestamp)"
  - name: kwh_to_gbp_offpeak
    from_instrument: KWH
    to_instrument: GBP
    method: TIME_OF_USE
    source: "rate_lookup('offpeak', observation.timestamp)"

operational_gateway:
  provider_connections:
    - connection_id: ocpp_chargers
      endpoint: "wss://chargers.acme.energy/ocpp"
      auth_type: HMAC
      retry_policy: { max_attempts: 3, backoff: EXPONENTIAL }

sagas:
  - name: record_charging_session
    trigger: "webhook:ocpp_chargers.meter_values"
    script: |
      def execute():
          ctx = input_data
          step(name="log_energy")
          position_keeping.record_entry(
              account_id=ctx["site_account_id"],
              instrument_code="KWH",
              quantity=Decimal(ctx["kwh_delivered"]),
              side="DEBIT",
          )

  - name: monthly_fleet_invoice
    trigger: "scheduled:monthly_billing"
    filter: "now.day == 1"
    script: |
      def execute():
          # Monthly fleet invoice logic (simplified)
          ctx = input_data
          for fleet in ctx["fleets"]:
              step(name="invoice_" + fleet["id"])
              payment_order.initiate(
                  debtor_account_id=fleet["account_id"],
                  amount=fleet["total_due"],
                  instrument_code="GBP",
              )
```

### 13.3 The Generation Target

The generator's job is to produce output like the above from input like:

> "I run an EV charging network. 50 sites in the UK. Fleet customers billed monthly.
> Peak/off-peak pricing. Energy measured in kWh, billed in GBP. OCPP charger protocol."

Every field in the output maps to a manifest proto field. Every Starlark call maps to a
handler in `handlers.yaml`. Every trigger maps to a topic in the registry or a provider
connection. The type checker validates all of this.

### 13.4 File Format Conventions

| Artifact | Format | Why |
|----------|--------|-----|
| Manifest (human-authored) | YAML | Readable, commentable, familiar to K8s/DevOps practitioners |
| Manifest (stored) | JSON | Proto-native serialisation, unambiguous |
| Handler schemas | YAML | Human-authored ABI definition |
| Saga scripts | `.star` (Starlark) | Google reference implementation convention |
| Cookbook pattern metadata | JSON | Machine-readable registry format |
| Cookbook manifest fragments | YAML | Human-authored examples |
| CEL expressions | Inline strings | Sub-expressions within YAML/JSON fields |
| AsyncAPI event schemas | YAML | Industry standard for event-driven APIs |

The split follows the Kubernetes convention: **YAML for human-authored source, JSON for
machine artifacts, Proto for the type system.**

---

## 14. Implementation Phases

### Phase 0: Foundational Improvements (prerequisite for all layers)

Phase 0 is sequenced as **five independent PRs** that can be developed in parallel.
Each PR is self-contained and delivers value on its own.

#### 0.1 Typed Service Modules in Validator (PR 1 -- highest priority)

**Closes the single largest validation gap.** Today, the manifest validator uses stub
Starlark modules that accept any handler call. This PR replaces them with typed modules
generated from `handlers.yaml`.

- Wire `BuildServiceModules` into the manifest validator
- Replace stub `starlarkModule` with schema-generated typed modules
- Handler calls validated against `handlers.yaml` at manifest validation time
- Extract handler call metadata for relationship graph (data structure only -- UI in Phase 1)
- **Test**: submit a manifest with `position_keeping.nonexistent_handler()` and verify rejection

#### 0.2 Trigger Validation (PR 2 -- parallelisable with PR 1)

Extends existing event trigger validation to cover all trigger types.

- API triggers: validate path references real gRPC endpoint (against OpenAPI spec),
  path uniqueness, format validation
- Webhook triggers: validate source against provider connections
- Scheduled triggers: name uniqueness
- Instruction routes: validate instruction type conventions, orphan warnings
- **Test**: submit manifest with `api:/nonexistent/path` and verify rejection

#### 0.3 AsyncAPI CEL Field Validation (PR 3 -- parallelisable)

Validates that CEL filter expressions reference real event payload fields.

- Load AsyncAPI specs for the event topic referenced in the trigger
- Extract field references from CEL expression
- Validate each field exists in the AsyncAPI payload schema
- Warning (not error) for fields not found -- AsyncAPI specs may lag
- **Test**: submit manifest with `filter: "event.typo_field == 'X'"` and verify warning

#### 0.4 Handler Evolution (PR 4 -- can follow PR 1)

Builds on typed service modules to support handler versioning.

- Add structured `conversions` block to `handlers.yaml` (with CEL default expressions)
- Add `deprecated` field to handler params (replace description-only deprecation)
- Implement mutating validator phase (auto-convert deprecated calls)
- Add `handlers.sum` hash file for ABI integrity
- **Test**: submit manifest with deprecated `initiate_log(amount=..., direction=...)` and verify
  auto-conversion to `record_entry(quantity=..., side=...)`

#### 0.5 Manifest RBAC (PR 5 -- parallelisable)

Enforces role-based access control on manifest operations.

- Add gRPC interceptor checking `UserEntity.Role` against required role per RPC
- `ManifestValidate` requires `viewer`, `ManifestPlan` requires `editor`, `ManifestApply` requires `admin`
- `CreateTenantOverride` requires `admin`
- API key `Scopes` field enforces scoped access for service-to-service calls
- **Test**: call `ManifestApply` with `viewer` role and verify rejection

### Phase 1: Relationship Graph

- Extract relationships from typed service module calls during validation
- Build index at apply-time, store alongside manifest version
- Surface in existing UI (account detail -> attached sagas, saga detail -> triggering events)
- New MCP tool: `meridian_economy_graph` -- query relationships
- Impact analysis: "what breaks if I change X?"

### Phase 2: Generator Backend

- Package generation context (handler schemas, topic registry, patterns) for LLM consumption
- New MCP tool: `meridian_economy_generate` -- takes business description, returns manifest
- Validation loop: generate -> mutate -> validate -> fix -> re-validate
- "Amend" mode: read current manifest + graph, generate incremental changes

### Phase 3: IDE Frontend

- Conversational prompt UI ("Build My Economy" + "I'm Feeling Lucky")
- Manifest editor with syntax highlighting, inline validation, relationship preview
- Deploy flow: validate -> plan diff -> one-click apply
- Post-deploy dashboard with live relationship graph

### Phase 3.5: Operational Dashboard

- Failed/compensated/zombie saga list with error details and causation tree
- DLQ inspection and one-click replay UI
- Error trend charts (by saga, by error classification, by tenant)
- Resolution workflow: investigate → fix root cause → replay from DLQ
- Saga-specific Grafana dashboards (execution duration, error rates, compensation frequency)

### Phase 4: Economy Simulator

- Async job framework: submit replay request, receive job ID, poll/stream progress
- Batch re-valuation engine (replay historical postings with modified economy rules)
- Position restatement simulator (what-if bucketing/validation changes)
- Scenario parametrisation for forecasting service (e.g., "demand + 10%")
- Impact report generation (revenue, margin, counterparty profitability)
- Identity test: unchanged rules must reproduce actual historical results (accuracy baseline)
- Integration with generator: "Change pricing" → generate manifest diff → simulate impact → approve

### Phase 5: Polish and Network Effects (future)

- Community pattern contributions (shadcn model)
- "Explore economies" -- anonymised pattern sharing
- Starlark standard library (reusable fragments -- the "libraries" concept)
- Multi-tenant economy templates
- `script_ref` support with separate `.star` file management
- Per-tenant rate limiting in API gateway

---

## 15. Success Criteria

1. A user can describe a business in natural language and get a deployed economy in under 5 minutes
2. The generated manifest is human-readable and freely editable (shadcn test)
3. No invalid manifests reach the apply stage -- typed modules catch handler errors,
   trigger validation catches routing errors
4. The relationship graph answers "what touches X?" for any resource in under 1 second
5. The same workflow works via MCP tools (headless) and the built-in UI
6. Handler evolution never breaks running sagas -- conversion rules maintain backward compatibility
7. Every trigger type is validated against its respective registry before deploy
8. A tenant can simulate economy changes against historical data and see financial impact
   before deploying
9. Failed sagas are visible in an operator dashboard with one-click DLQ replay
10. In-flight sagas drain safely when manifests are updated (no mid-execution breakage)
11. Manifest apply requires `admin` role -- `viewer` and `editor` roles are rejected
12. Existing tenants can re-apply manifests without modification (conversion rules
    handle deprecated calls automatically)
13. Economy simulator identity test passes -- unchanged rules reproduce actual historical results

---

## 16. Throughput Profile

The architecture optimises for **correctness over raw throughput**. This is a deliberate
design choice, not a limitation to fix.

### Sustainable Throughput: 5-10k TPS per Cluster

A single saga (e.g., a 4-step settlement) produces **18-22 database writes** (saga step
results, position entries, event outbox, audit trail) with ~200-400ms end-to-end latency
dominated by sequential gRPC handler calls.

| Component | Per-Saga Cost | Why Sequential |
|-----------|--------------|----------------|
| Handler gRPC calls (4 steps) | 200-400ms | Compensation ordering requires step-by-step |
| Saga state persistence | 8 writes | Per-step persistence enables recovery from any failure |
| Position entries | 2 writes | Double-entry guarantee per step |
| Event outbox | 4 writes | Exactly-once event delivery (FR-31 atomicity) |

### Why This Is the Right Trade-off

Each "expensive" property is load-bearing:

- **Per-step persistence** = saga can compensate from any failure point
- **Synchronous handler calls** = double-entry guarantee per step
- **Strong consistency** = regulatory audit trail integrity
- **Outbox pattern** = exactly-once event delivery

Removing any of these to increase throughput would undermine the core value proposition.

### Market Context

| System | TPS | Notes |
|--------|-----|-------|
| UK Faster Payments (peak) | ~1,500 | Real-time payment rail |
| BACS daily batch (amortised) | ~3,000 | UK batch payments |
| Stripe (estimated) | ~10,000 | Payment processor |
| Visa global peak | ~65,000 | Card network |

**5-10k TPS covers every target use case** (energy settlement, carbon accounting, GPU billing,
UN voucher programs). Visa-scale card processing is not our market.

### Scaling Path (Without Sacrificing Guarantees)

1. **Horizontal partitioning by economy** -- tenant sagas are independent, shard by tenant
2. **gRPC co-location** -- reduce 50-100ms handler latency (single largest win)
3. **Read replicas / follower reads** -- for query load, not write path
4. **Pre-compiled Starlark caching** -- reduce interpreter startup overhead

These optimisations reach **20-30k TPS** without sacrificing any correctness guarantees.

---

## 17. Decisions Made During Design Review

| Decision | Resolution | Rationale |
|----------|-----------|-----------|
| **Graph storage format** | Embedded in manifest version record (JSON column) | Graph is a derived artifact, rebuilt on each apply. No need for separate table. |
| **AsyncAPI field validation depth** | Full field existence validation (not just schema-level) | Catches typos in CEL filter field names. Same pattern as topic registry validation. |
| **Script ref** | Phase 5 (future). Inline `script:` only for now. | `script_ref` is a source-time convenience resolved at apply-time. Adds filesystem management complexity. |
| **Conversion default expressions** | CEL expressions against old parameter values | Reuses existing CEL engine. Pure, deterministic, validated at schema load time. |
| **API trigger model** | Endpoint-binding (maps existing gRPC endpoints to sagas) | Platform sagas provide defaults; tenants override via `CreateTenantOverride`. |
| **In-flight saga migration** | Drain (running sagas complete on old version) | Saga instances persist their execution plan. New events use new version immediately. |
| **Error default classification** | FATAL (fail-safe) | Prevents infinite retries on unexpected errors. Explicit `TransientError` wrapper for retryable cases. |
| **AI model** | MCP server is model-agnostic; client selects the model | MCP tools are the API surface. Claude Code, ChatGPT, or custom agents call the same tools. No model coupling in the platform. |
| **Multi-manifest** | Single self-contained manifest per tenant | Manifest proto is explicitly self-contained (no external references). Simplifies validation, diffing, and rollback. Multi-manifest composition deferred to future if needed. |
| **Versioning UX** | Linear version history only | Current repository stores linear versions with diff summaries and point-in-time queries. Branching adds complexity without clear use case. Git manages source-level branching; the runtime stores applied versions. |
| **Simulator execution model** | Async job with progress tracking | Batch replay of thousands of postings cannot be synchronous. Implement as background job with progress events. Existing simulation tools (single-item) remain synchronous. |
| **Operator dashboard scope** | Per-tenant with platform-wide aggregate view | Tenants see their own failed sagas and DLQ. Platform admins see cross-tenant aggregates and trends. Same dashboard, filtered by role. |
| **CEL version pinning** | Pin cel-go version, add golden-file replay tests | Kubernetes issue #120821 proved cel-go upgrades can break stored expressions (cost estimation, type scoping). Meridian pins cel-go in `go.mod`, adds golden-file tests comparing evaluation results across upgrades. Simulator (Phase 4) stores evaluation results alongside replay for drift detection. |

## 18. Open Questions

1. **Handler sunset enforcement**: When do conversion rules get removed? After N manifest
   versions? Calendar date? Needs operational experience from Phase 0.4 before deciding.
2. **Simulator scope**: Full position restatement (expensive, accurate) or valuation-only
   replay (fast, approximate)? May offer both as simulator modes — needs prototyping in
   Phase 4 to determine performance characteristics.
3. ~~**CEL version pinning for replay**~~: Promoted to decision — see §17.

---

## Appendix A: Current Architecture Reference

### Key Files

| Path | Purpose |
|------|---------|
| `api/proto/meridian/control_plane/v1/manifest.proto` | Manifest schema (the "program" format) |
| `shared/pkg/saga/schema/handlers.yaml` | Platform handler schemas (the "ABI") |
| `shared/pkg/saga/schema/service_modules.go` | Typed Starlark module generation from handlers |
| `shared/pkg/saga/schema/schema.go` | Handler schema parsing and validation |
| `services/control-plane/internal/applier/handlers.yaml` | Control plane handler schemas (manifest apply) |
| `services/control-plane/internal/applier/handlers.go` | Handler registration with compensation |
| `services/control-plane/internal/validator/manifest_validator.go` | Type checker (with validation gaps) |
| `services/control-plane/internal/differ/manifest_differ.go` | Diff engine |
| `services/control-plane/internal/planner/manifest_planner.go` | Execution planner |
| `services/control-plane/internal/manifest/repository.go` | Manifest version storage |
| `services/reference-data/saga/` | Saga definition persistence and platform defaults |
| `services/event-router/internal/registry/saga_registry.go` | Event-to-saga index with CEL filter compilation |
| `services/event-router/internal/handlers/saga_dispatch_handler.go` | Event dispatch with chain depth and idempotency |
| `shared/platform/events/topics/topics.go` | Topic registry (valid event channels) |
| `api/asyncapi/*.yaml` | Event payload schemas (9 service specs) |
| `cookbook/patterns/*/pattern.json` | Cookbook pattern metadata |
| `cookbook/patterns/*/manifest-fragment.yaml` | Pattern manifest fragments |
| `cookbook/patterns/*/*.star` | Pattern saga scripts |
| `examples/manifests/*.json` | Complete example manifests |
| `services/mcp-server/internal/tools/economy.go` | MCP manifest tools |
| `services/mcp-server/internal/tools/cookbook_discover.go` | Pattern discovery with compatibility |
| `frontend/src/lib/visualization/star-parser.ts` | Frontend Starlark AST analysis (regex-based) |
| `buf.gen.yaml` | Code generation config (proto -> Go, gRPC, OpenAPI) |
| `buf.gen.jsonschema.yaml` | JSON Schema generation from manifest proto |
| `api/openapi/meridian.swagger.json` | Generated OpenAPI spec (all gRPC endpoints) |
| `api/jsonschema/` | Generated JSON Schema for manifest validation |
| `services/mcp-server/internal/tools/simulation.go` | Existing simulation tools (CEL, valuation, saga) |
| `shared/pkg/saga/error_classification.go` | FATAL/TRANSIENT error classification |
| `shared/pkg/saga/saga_executor.go` | Failure handling and compensation decisions |
| `shared/pkg/saga/step_execution.go` | Idempotent step execution with outbox |
| `shared/platform/kafka/dlq.go` | Dead letter queue producer with metadata |
| `shared/platform/kafka/dlq_admin.go` | DLQ inspection and replay utilities |
| `shared/platform/observability/` | OTel tracing, Prometheus metrics, structured logging |
| `services/reference-data/saga/override_api.go` | Platform saga override API |
| `services/reference-data/saga/defaults/` | Platform default saga scripts (embedded `.star` files) |
| `services/reference-data/saga/platform_sync.go` | Platform saga sync (embedded → DB at startup) |
| `services/reference-data/saga/seeder.go` | Tenant saga seeding (post-provisioning hook) |
| `frontend/src/features/sagas/pages/index.tsx` | Starlark Config page (`/starlark-config`) |
| `services/forecasting/` | Forecasting service (forward curves from market data) |
| `deployments/k8s/observability/grafana-stack.yaml` | Grafana/Tempo/Loki/Prometheus stack |

### Database Tables (per-tenant schema)

| Table | Key | Versioned | Purpose |
|-------|-----|-----------|---------|
| `instrument_definition` | `(code, version)` | Yes | Instrument register |
| `account_type_definitions` | `(code, version)` | Yes | Account type register |
| `saga_definition` | `(name, version)` | Yes | Saga register (tenant scripts) |
| `platform_saga_definition` | `(name)` | Yes (semver) | Platform default sagas (public schema) |
| `manifest_versions` | `id` | Yes (snapshots) | Full manifest history |
| `manifest_apply_job` | `id` | No | Apply job tracking |

### MCP Tools (existing)

| Tool | Runtime Stage |
|------|--------------|
| `meridian_cookbook_discover` | Pattern discovery |
| `meridian_manifest_validate` | Validation |
| `meridian_manifest_plan` | Planning (diff against current state) |
| `meridian_manifest_apply` | Apply (install into runtime) |
| `meridian_starlark_validate` | Script validation |
| `meridian_cel_validate` | Expression validation |
| `meridian_economy_structure` | Current state inspection |
| `meridian_saga_simulate` | Saga dry-run (stubbed service calls) |
| `meridian_valuation_simulate` | Valuation dry-run |
| `meridian_cel_evaluate` | CEL expression evaluation |

### MCP Tools (new, proposed)

| Tool | Purpose | Phase |
|------|---------|-------|
| `meridian_manifest_fix` | Auto-convert deprecated handler calls | 0 |
| `meridian_economy_graph` | Relationship query and impact analysis | 1 |
| `meridian_economy_generate` | AI-assisted manifest generation from business description | 2 |
| `meridian_economy_simulate` | What-if impact analysis (replay with modified economy) | 4 |

---

## Appendix B: Trigger Taxonomy

### Trigger Types

```text
event:<topic-name>
  Example: event:position-keeping.transaction-captured.v1
  Validated against: topics.All() registry
  Filter: CEL on { event, metadata, chain_depth }
  Routing: Kafka consumer -> SagaRegistry -> CEL filter -> dispatch

webhook:<source>.<event-type>
  Example: webhook:ocpp_chargers.meter_values
  Validated against: manifest operational_gateway.provider_connections[]
  Filter: CEL on incoming payload (future)
  Routing: Webhook receiver -> saga invocation

api:<path>
  Example: api:/v1/deposits
  Validated against: OpenAPI spec (must reference real gRPC endpoint) + uniqueness
  Filter: none (request is the trigger)
  Routing: HTTP gateway (Vanguard REST->gRPC) -> SagaExecutionService -> saga
  Override: platform sagas provide defaults; tenants override via CreateTenantOverride

scheduled:<schedule-name>
  Example: scheduled:monthly_billing
  Validated against: uniqueness within manifest
  Filter: CEL (e.g., "now.day == 1")
  Routing: Scheduler -> saga invocation
```

### Chain Depth Safety

| Depth | Behaviour |
|-------|-----------|
| 0 | Direct trigger (API, webhook, schedule) |
| 1-9 | Event-driven cascade (saga A -> event -> saga B) |
| 10+ | Dropped with warning (configurable via `maxChainDepth`) |

---

## Appendix C: Glossary

| Term | Definition |
|------|-----------|
| **Manifest** | A declarative program describing an entire financial economy |
| **Saga** | An executable workflow written in Starlark, triggered by events/API/schedule |
| **Handler** | A typed service function callable from Starlark (e.g., `position_keeping.initiate_log`) |
| **Handler schema** | YAML definition of handler parameters, return types, and compensation |
| **ABI** | The handler schema contract between Starlark scripts and services |
| **Instrument** | A unit of measure with dimensions (GBP, KWH, TONNE_CO2E) |
| **Account Type** | A register category with a denomination and behaviour class |
| **CEL Expression** | A pure, sub-millisecond predicate for validation/pricing/filtering |
| **The Generator** | AI-assisted generation of manifests from business descriptions |
| **The IDE** | Frontend wizard for conversational economy creation |
| **The Economy Runtime** | The Meridian runtime that executes manifests |
| **The Relationship Graph** | Materialised index of cross-resource dependencies |
| **Typed Service Modules** | Starlark structs generated from handler schemas that validate calls at validation time |
| **Mutating Phase** | Pre-validation pass that auto-converts deprecated handler calls |
| **Chain Depth** | Counter tracking saga cascade depth, enforcing system-level termination |
| **Conversion Rule** | Inline handler migration definition (K8s-style) for backward compatibility |
| `.star` file | Starlark script file (Google reference implementation convention) |
