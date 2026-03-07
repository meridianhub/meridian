# The Meridian Economy Runtime: From Business Description to Running Economy

## Status: DRAFT v2

## Executive Summary

Meridian is a transaction integrity engine that tenants configure through manifests
(YAML/JSON declarations), Starlark scripts (saga orchestration), and CEL expressions
(validation/pricing). Today these are authored manually and applied via the control plane.
This PRD formalises the system as an **Economy Runtime** and defines the layers needed to
close current gaps and unlock AI-assisted configuration.

**Three new layers:**

1. **Relationship Graph** -- runtime introspection of cross-resource dependencies
2. **Compiler** -- AI-assisted generation of manifests from business descriptions
3. **IDE** -- conversational wizard UI for economy creation

**Three foundational improvements (identified during design review):**

1. **Typed Service Modules** -- close the validation gap between Starlark scripts and handler schemas
2. **Handler Evolution** -- K8s-style versioned handlers with inline conversion rules
3. **Trigger Validation** -- cross-reference all trigger types against their respective registries

The tagline: **"Describe your business. We build your bank."**

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
| **Type System** | Handler schemas (`handlers.yaml`), topic registry, instrument dimensions | Compile-time validation before deploy |
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

**Dual storage:**

- **Inline in manifest**: Simple sagas embedded as `script:` string field
- **By reference**: Complex sagas referenced as `script_ref:` pointing to a named script

```yaml
sagas:
  # Inline for simple sagas
  - name: simple_deposit
    trigger: "api:/v1/deposit"
    script: |
      def execute():
          position_keeping.initiate_log(...)

  # Reference for complex sagas
  - name: monthly_fleet_invoice
    trigger: "scheduled:monthly"
    filter: "now.day == 1"
    script_ref: "monthly_fleet_invoice.star"
```

At **apply time**, `script_ref` is resolved to full text and stored in the manifest
snapshot. The immutable snapshot property is preserved -- `script_ref` is a source-time
convenience, not a runtime indirection.

At **runtime**, scripts are loaded from `saga_definition` table (per-tenant schema) as
today. Platform sagas live in `platform_saga_definition` (public schema), synced from
embedded `.star` files.

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

Before building the Relationship Graph, Compiler, and IDE, three foundational gaps must
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
- **Compiler Reliability**: The AI compiler's output is validated against real handler schemas, not stubs.

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
- [ ] Starlark handler parameter type/required validation at compile time
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
- [ ] Compile-time parameter validation (in manifest validator)
- [ ] Handler version tracking and conversion rules
- [ ] `handlers.sum` hash verification

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
- **The compiler**: AI can inspect the graph to understand the current economy before modifying it
- **The IDE**: Show relationships as the user builds, not just after deploy
- **Handler evolution**: "These 4 sagas call the deprecated handler" -- found via graph, fixed via mutating validator

---

## 4. The Compiler (Layer 2 -- AI-Assisted Generation)

### 4.1 Philosophy: shadcn, Not npm

The compiler follows the [shadcn/ui](https://ui.shadcn.com/) philosophy:

- **Not a library** -- you don't `import` patterns at runtime
- **Copy and own** -- output is your manifest, your Starlark, your CEL
- **Registry for discovery** -- cookbook patterns are examples and inspiration
- **Modify freely** -- no abstraction between you and the generated code

The cookbook is context for the LLM, not a template engine. Patterns are dissolved into the output.

### 4.2 Compiler Pipeline

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

### 4.3 Compiler Context (What the AI Needs)

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

#### "Build My Bank" (Interactive Compilation)

1. User describes business
2. AI asks clarifying questions (instruments? pricing? settlement?)
3. AI generates manifest draft
4. User reviews in editor (full ownership -- shadcn style)
5. User modifies if needed
6. Validate -> Plan -> Apply

#### "I'm Feeling Lucky" (Single-Pass Compilation)

1. User provides minimal description ("EV charging UK")
2. AI makes all decisions using cookbook defaults
3. Generates, validates, plans in one pass
4. Shows result with "Customise" option
5. "I'm Feeling Lucky" always stops at preview for non-empty economies (plan output shown for approval)

#### "Amend" (Incremental Compilation)

1. User has a running economy
2. User says "add carbon credit tracking"
3. AI reads current manifest + relationship graph
4. Mutating phase auto-converts any deprecated handler calls in existing scripts
5. AI generates additions (new instruments, account types, sagas)
6. Shows impact analysis via relationship graph
7. User approves -> apply

### 4.5 Error Recovery

The compiler's type checker (manifest validator with typed modules) produces structured errors with:

- Location paths (`sagas[0].script`, `instruments[2].code`)
- Severity levels (error blocks apply, warning allows)
- Suggested fixes ("Did you mean...?")
- Available fields (for unknown handler params or event channels)

The AI compiler loop: generate -> validate -> if errors, fix and re-validate -> until clean.

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
  can compile manifests via the same tools.

The built-in UI uses the MCP tools under the hood. It's a frontend to the compiler, not a separate system.

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
|  [Build My Bank]              [I'm Feeling Lucky]    |
+------------------------------------------------------+
```

#### 5.2.2 The Conversation (Build My Bank mode)

AI asks clarifying questions with clickable options. Each answer refines the compilation target.

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

## 6. Compiler Theory Applied to Meridian

### 6.1 Lessons from Runtime/Compiler Design

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

### 6.2 What a "Meridian Program" Looks Like

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
          position_keeping.initiate_log(
              account_id=ctx["site_account_id"],
              instrument_code="KWH",
              quantity=Decimal(ctx["kwh_delivered"]),
              side="DEBIT",
              correlation_id=ctx["session_id"],
          )

  - name: monthly_fleet_invoice
    trigger: "scheduled:monthly_billing"
    filter: "now.day == 1"
    script_ref: "monthly_fleet_invoice.star"
```

### 6.3 The Compilation Target

The compiler's job is to produce output like the above from input like:

> "I run an EV charging network. 50 sites in the UK. Fleet customers billed monthly.
> Peak/off-peak pricing. Energy measured in kWh, billed in GBP. OCPP charger protocol."

Every field in the output maps to a manifest proto field. Every Starlark call maps to a
handler in `handlers.yaml`. Every trigger maps to a topic in the registry or a provider
connection. The type checker validates all of this.

### 6.4 File Format Conventions

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

## 7. Implementation Phases

### Phase 0: Foundational Improvements (prerequisite for all layers)

Phase 0 is sequenced as **four independent PRs** that can be developed in parallel.
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
- **Test**: submit manifest with deprecated `initiate_log(amount=...)` and verify auto-conversion
  to `initiate_log(quantity=...)`

### Phase 1: Relationship Graph

- Extract relationships from typed service module calls during validation
- Build index at apply-time, store alongside manifest version
- Surface in existing UI (account detail -> attached sagas, saga detail -> triggering events)
- New MCP tool: `meridian_economy_graph` -- query relationships
- Impact analysis: "what breaks if I change X?"

### Phase 2: Compiler Backend

- Package compiler context (handler schemas, topic registry, patterns) for LLM consumption
- New MCP tool: `meridian_economy_compile` -- takes business description, returns manifest
- Validation loop: generate -> mutate -> validate -> fix -> re-validate
- "Amend" mode: read current manifest + graph, generate incremental changes

### Phase 3: IDE Frontend

- Conversational prompt UI ("Build My Bank" + "I'm Feeling Lucky")
- Manifest editor with syntax highlighting, inline validation, relationship preview
- Deploy flow: validate -> plan diff -> one-click apply
- Post-deploy dashboard with live relationship graph

### Phase 4: Polish and Network Effects (future)

- Community pattern contributions (shadcn model)
- "Explore economies" -- anonymised pattern sharing
- Starlark standard library (reusable fragments -- the "libraries" concept)
- Multi-tenant economy templates
- `script_ref` support with separate `.star` file management

---

## 8. Success Criteria

1. A user can describe a business in natural language and get a deployed economy in under 5 minutes
2. The generated manifest is human-readable and freely editable (shadcn test)
3. No invalid manifests reach the apply stage -- typed modules catch handler errors,
   trigger validation catches routing errors
4. The relationship graph answers "what touches X?" for any resource in under 1 second
5. The same workflow works via MCP tools (headless) and the built-in UI
6. Handler evolution never breaks running sagas -- conversion rules maintain backward compatibility
7. Every trigger type is validated against its respective registry before deploy

---

## 9. Throughput Profile

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

## 10. Decisions Made During Design Review

| Decision | Resolution | Rationale |
|----------|-----------|-----------|
| **Graph storage format** | Embedded in manifest version record (JSON column) | Graph is a derived artifact, rebuilt on each apply. No need for separate table. |
| **AsyncAPI field validation depth** | Full field existence validation (not just schema-level) | Catches typos in CEL filter field names. Same pattern as topic registry validation. |
| **Script ref resolution** | Phase 4 (future). Inline `script:` only for now. | `script_ref` is a source-time convenience; resolved to full text at apply-time. |
| **Conversion default expressions** | CEL expressions against old parameter values | Reuses existing CEL engine. Pure, deterministic, validated at schema load time. |
| **API trigger model** | Endpoint-binding (maps existing gRPC endpoints to sagas) | Platform sagas provide defaults; tenants override via `CreateTenantOverride`. |

## 11. Open Questions

1. **AI model**: Which LLM powers the compiler? Claude via MCP? Pluggable?
2. **Multi-manifest**: Can an economy be split across multiple manifests (like microservices), or always one file?
3. **Versioning UX**: Git-like branching for manifests? Or linear version history only?
4. **Handler sunset enforcement**: When do conversion rules get removed? After N manifest versions? Calendar date?

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

| Tool | Compiler Stage |
|------|---------------|
| `meridian_cookbook_discover` | Standard library search |
| `meridian_manifest_validate` | Type checking |
| `meridian_manifest_plan` | Linking |
| `meridian_manifest_apply` | Loading |
| `meridian_starlark_validate` | Syntax checking |
| `meridian_cel_validate` | Expression checking |
| `meridian_economy_structure` | Current state inspection |

### MCP Tools (new, proposed)

| Tool | Compiler Stage | Phase |
|------|---------------|-------|
| `meridian_manifest_fix` | Mutating pass (auto-convert deprecated handlers) | 0 |
| `meridian_economy_graph` | Debug symbols (relationship query) | 1 |
| `meridian_economy_compile` | Full compilation (business description -> manifest) | 2 |

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
| **The Compiler** | AI-assisted generation of manifests from business descriptions |
| **The IDE** | Frontend wizard for conversational economy creation |
| **The Economy Runtime** | The Meridian runtime that executes manifests |
| **The Relationship Graph** | Materialised index of cross-resource dependencies |
| **Typed Service Modules** | Starlark structs generated from handler schemas that validate calls at compile time |
| **Mutating Phase** | Pre-validation pass that auto-converts deprecated handler calls |
| **Chain Depth** | Counter tracking saga cascade depth, enforcing system-level termination |
| **Conversion Rule** | Inline handler migration definition (K8s-style) for backward compatibility |
| `.star` file | Starlark script file (Google reference implementation convention) |
