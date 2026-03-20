---
name: adr-037-ai-native-codebase-architecture
description: Architectural patterns that make a large codebase navigable and safe for AI agents
triggers:
  - Adding new services, packages, or modules to the codebase
  - Deciding whether to commit generated files
  - Creating conventions or guard rails for code quality
  - Onboarding new AI tooling or agent workflows
  - Questioning why a convention exists or whether to relax it
instructions: |
  This codebase is designed for AI agents to work on safely and efficiently. Follow the
  layered safety model: types catch errors at compile time, linters catch them before push,
  architecture tests catch structural drift, CI enforces everything, and coverage gates
  prevent regression. When adding new code, match existing conventions — they exist so AI
  can pattern-match reliably. When in doubt, read the relevant guide in docs/guides/.
---

# 37. AI-Native Codebase Architecture

Date: 2026-03-20

## Status

Accepted

## Context

Meridian is a large, multi-service codebase (~200k+ lines of Go, ~120k+ lines of TypeScript) that routinely exceeds any single LLM context window. AI agents — primarily Claude Code sessions, often running as parallel teammates in marathon workflows — are first-class contributors. They create worktrees, implement features, create PRs, and iterate on review feedback autonomously.

This creates a specific engineering problem: **how do you maintain code quality and architectural coherence when the primary contributors can only see a fraction of the codebase at any time?**

The traditional answer — "developers know the codebase" — doesn't apply. An AI agent starts each session with zero institutional memory beyond what's written down. It pattern-matches from what it can read. If patterns are inconsistent, the agent produces inconsistent code. If conventions exist only in people's heads, the agent violates them. If guard rails are advisory, the agent ignores them under pressure.

The same problem affects human contributors on large codebases, but AI amplifies it: agents work faster, touch more files, and run in parallel. A single inconsistency can propagate across 8 PRs in a marathon before anyone notices.

This ADR documents the architectural patterns Meridian uses to keep a large codebase coherent despite contributors (human and AI) who cannot hold it all in context at once.

## Decision Drivers

* AI agents pattern-match from what they can read — conventions must be discoverable, not tribal
* Parallel agent workflows (8+ teammates) amplify inconsistency if guard rails are weak
* The codebase must be immediately buildable from any worktree without extra toolchain steps
* Errors caught later cost more — a CI failure wastes 10 minutes, a review comment wastes 30
* Human contributors benefit from the same patterns — this is not AI-specific infrastructure

## Decision Outcome

Adopt a **layered safety model** where each layer catches errors that slip through the previous one, combined with **denormalized availability** so every worktree is immediately usable, and **machine-readable conventions** so AI agents can discover patterns programmatically.

### The Layered Safety Model

Errors are cheapest to fix closest to where they're introduced. Each layer catches a different class of mistake:

```
Layer 1: Type System          — catches at write time    (seconds)
Layer 2: Linters              — catches before push      (seconds)
Layer 3: Architecture Tests   — catches structural drift (minutes)
Layer 4: CI Convention Checks — catches on every PR      (minutes)
Layer 5: Coverage Gates       — catches missing tests    (minutes)
Layer 6: Code Review (bots)   — catches design issues    (hours)
Layer 7: Process Retrospectives — catches workflow waste  (days)
```

No single layer is sufficient. The value is in the combination — each layer catches what the previous one missed.

### Layer 1: Type System — Make Invalid States Unrepresentable

**Pattern**: Use the type system to prevent errors at compile time, not runtime.

Go generics enforce dimensional safety on quantities:

```go
type Quantity[D Dimension] struct {
    Amount decimal.Decimal
    Unit   InstrumentCode  // "GBP", "kWh", "TONNE_CO2E"
}

var deposit Quantity[Currency]
var energy  Quantity[Electricity]
deposit = energy  // Compile error — can't mix dimensions
```

Proto-generated clients enforce API contracts:

```typescript
// Generated from .proto — type-checked at compile time
positionKeeping.initiateLog({
    amount: new Decimal("100.00"),  // Type-safe
    direction: Direction.CREDIT,     // Enum-validated
})
```

Starlark (non-Turing-complete) guarantees tenant saga termination — no while loops, no recursion, mathematically provable finite execution.

**Why this matters for AI**: An agent cannot accidentally add kilowatt-hours to British pounds, call an API with wrong parameter types, or generate a saga that runs forever. The compiler rejects it before the agent even pushes.

### Layer 2: Linters — Catch Mistakes Before Push

**Pattern**: 23 golangci-lint rules with strict enforcement. Key AI-relevant linters:

| Linter | What it catches |
|--------|----------------|
| `nolintlint` | Unexplained lint suppressions — forces agents to justify every `//nolint` |
| `godox` | TODO/FIXME in committed code — prevents agents from leaving breadcrumbs |
| `funlen` | Functions exceeding 80 lines — prevents context-window-busting functions |
| `cyclop` | High cyclomatic complexity — prevents functions an agent can't reason about |
| `forcetypeassert` | Unchecked type assertions — prevents `v := x.(T)` panics |
| `exhaustive` | Non-exhaustive enum switches — catches missing cases |
| `depguard` | Forbidden imports — enforces architectural boundaries |

**Why this matters for AI**: An agent under review pressure might add `//nolint` without explanation, leave a TODO instead of finishing work, or write a 200-line function. Linters make these hard errors, not suggestions.

### Layer 3: Architecture Tests — Enforce Structural Contracts

**Pattern**: Go tests in `tests/architecture/` that scan the codebase and fail if conventions are violated.

| Test | Contract |
|------|----------|
| `TestFileSize` | No non-test Go file exceeds 800 lines |
| `TestFunctionSize` | No function exceeds 60 lines (with ratchet for existing violations) |
| `TestDomainErrorNaming` | Error variables follow `Err[A-Z]` pattern |
| `TestRepositoryMethodVerbs` | Repository methods use Create/FindByID/Update/List/Delete |
| `TestNoInternalCrossServiceImports` | Services don't import each other's internals |
| `TestNoCrossServiceDomainImports` | Services communicate via gRPC, not direct domain imports |
| `TestSharedNeverImportsServices` | Shared packages never depend on service packages |
| `TestServiceServerGoExists` | Every gRPC service has `service/server.go` |
| `TestSharedPackagesHaveDocGo` | Every shared package has a `doc.go` with usage examples |

**Why this matters for AI**: An agent creating a new service will pattern-match from existing services. If 12 out of 13 services use `server.go` and one uses `grpc_service.go`, the agent might copy the outlier. Architecture tests make the convention a compile-time contract, not a statistical tendency.

### Layer 4: CI Convention Checks — Belt and Suspenders

**Pattern**: `scripts/verify-service-conventions.sh` runs on every PR and errors (not warns) on violations.

Checks include file size limits (with `//meridian:large-file` escape hatch), `time.Sleep` in tests (should use `await.Until()`), Go proto freshness, frontend proto freshness, and stale `//nolint` directives.

**Why this matters for AI**: Architecture tests run locally; CI checks run on every PR regardless. If an agent skips local tests (common in fast iteration), CI catches it.

### Layer 5: Coverage Gates — Prevent Test Regression

**Pattern**: Codecov enforces 75% project coverage and 70% patch coverage on every PR. Per-service coverage checks ensure no individual service drops below threshold.

**Why this matters for AI**: An agent implementing a feature might skip tests to save time. Coverage gates make this a blocking failure, not a suggestion.

### Layer 6: Code Review — Catch Design Issues

**Pattern**: CodeRabbit (automated) and human reviewers provide design-level feedback that static analysis cannot catch.

**Why this matters for AI**: Agents iterate on review feedback autonomously — fix code, push, wait for re-review. The review loop is part of the agent workflow, not separate from it.

### Layer 7: Process Retrospectives — Catch Workflow Waste

**Pattern**: Every marathon run produces a structured retrospective logged in `memory/marathon-retros.md`. Learnings feed back into the `/tm` orchestrator template — model selection heuristics, merge ordering, false signal patterns, conflict resolution strategies.

**Why this matters**: The AI workflow itself improves over time. False REVIEW_CLEAR signals from one marathon become sonnet-avoidance rules in the next. This is the feedback loop that makes the system self-correcting.

### Denormalized Availability

**Pattern**: Commit generated files (Go `.pb.go`, TypeScript `*_pb.ts`) to git despite being derivable from `.proto` sources.

**Analogy**: Database denormalization trades storage redundancy for read-time performance. Here, "read-time" is "can an AI teammate start working immediately in a fresh worktree."

| Without denormalization | With denormalization |
|------------------------|---------------------|
| Clone → install toolchain → generate → build | Clone → build |
| Every worktree needs buf, protoc, npm, protoc-gen-es, protoc-gen-go | Every worktree is immediately buildable |
| 8 parallel teammates each run generation (8× toolchain failures possible) | 8 parallel teammates start coding immediately |

**Consistency mechanism**: CI freshness checks regenerate and diff, failing if committed files are stale. This is the equivalent of a database trigger ensuring denormalized copies stay in sync.

**Prior art**: Google checks in all generated code (Bazel outputs, proto files) for the same reason — builds must work from a clean checkout.

### Machine-Readable Conventions

**Pattern**: Convention documents use YAML front-matter with `triggers` and `instructions` fields.

```yaml
---
name: repository-conventions
description: Canonical repository locations, interface naming, method naming
triggers:
  - Adding a repository to a service
  - Naming repository methods
  - Implementing GORM persistence
instructions: |
  Repository interfaces live in domain/repository.go. Implementations in
  adapters/persistence/. Use standard verbs: Create, FindByID, Update, List, Delete.
---
```

**Why this matters for AI**: An agent can programmatically search for relevant guides based on what it's doing. The `triggers` field acts as a semantic index — "I'm adding a repository, which guides apply?" — without requiring the agent to read every document.

18 guides in `docs/guides/`, 37 ADRs in `docs/adr/`, all with this front-matter.

### Hexagonal Architecture — Predictable Boundaries

**Pattern**: All standard services follow a hexagonal (ports and adapters) architecture defined in ADR-015:

```
services/{name}/
├── domain/              # Pure business logic, interfaces, errors — no imports from adapters/
├── adapters/
│   └── persistence/     # GORM implementations of domain interfaces
├── service/
│   └── server.go        # gRPC handler entry point — orchestrates domain + adapters
├── migrations/          # Atlas schema files
└── README.md            # Service guide with YAML front-matter
```

The boundaries are strict and tested:
- **domain/** has zero dependencies on infrastructure — it defines interfaces, the adapters implement them
- **adapters/** depends on domain (implements its interfaces) but never on service/
- **service/** depends on both (wires them together) but is the only layer that does
- **shared/** never imports services/ — dependency flows one direction only

Architecture tests (`TestNoInternalCrossServiceImports`, `TestAdaptersNeverImportService`) enforce these boundaries. A violation is a CI failure, not a review comment.

3 reference services are explicitly designated: `party/` (minimal), `current-account/` (inter-service calls), `financial-accounting/` (Kafka + observability).

**Why this matters for AI**: An agent doesn't need to understand the whole codebase to make a safe change. Hexagonal boundaries mean: "if I'm adding a repository method, I only need to read `domain/` and `adapters/persistence/`." The architecture tests guarantee that a local change stays local — no hidden cross-service coupling to discover by accident.

### API Contract Specifications — Three Layers of Truth

**Pattern**: Every service boundary has a machine-readable contract specification:

| Layer | Format | Location | Purpose |
|-------|--------|----------|---------|
| **Synchronous APIs** | Protocol Buffers | `api/proto/meridian/` | gRPC service definitions, request/response types |
| **REST APIs** | OpenAPI/Swagger | `api/openapi/meridian.swagger.json` | Auto-generated from proto, 21k+ lines |
| **Async Events** | AsyncAPI | `api/asyncapi/{service}.yaml` | Kafka event schemas per service |

All three are **generated from or defined alongside the source of truth** (proto files for sync, AsyncAPI specs for async), committed to git, and validated in CI.

```yaml
# api/asyncapi/position-keeping.yaml — machine-readable event contract
channels:
  position-keeping.position-log.created:
    publish:
      message:
        $ref: '#/components/messages/PositionLogCreated'
```

**Why this matters for AI**: When an agent needs to integrate with a service, it can read the contract specification without reading the implementation. Proto files define what methods exist and what types they accept. AsyncAPI specs define what events are published and their schemas. The agent can generate correct client code from the contract alone — no need to trace through service internals.

### Breadcrumb-Driven Behavior — Keywords That Light Up Patterns

**Pattern**: The project's CLAUDE.md (loaded into every AI session) uses specific phrases and keywords that activate preferred agent behavior. These act as "breadcrumbs" — compact triggers that reference larger patterns.

Examples from Meridian's CLAUDE.md:

| Breadcrumb | Behavior it triggers |
|------------|---------------------|
| "Use `await.Until()` instead of `time.Sleep`" | Agent reaches for polling, not sleeping |
| "NEVER use `time.Sleep` in tests" | Absolute prohibition, agent doesn't rationalize exceptions |
| "Constructor injection for dependencies" | Agent uses DI, doesn't create singletons |
| "CockroachDB, not PostgreSQL" | Agent avoids PG-specific features (LISTEN/NOTIFY, PL/pgSQL) |
| "Atlas, NOT Flyway" | Agent uses correct migration tool |
| "NEVER edit existing migration files" | Agent creates new migrations, doesn't modify history |
| "`git branch -D` not `-d`" | Agent uses force-delete after squash merges |

These aren't documentation — they're **behavioral programming**. An LLM responds to pattern and emphasis. "NEVER" in capitals with context hits differently than a buried convention in a style guide. The CLAUDE.md is effectively a prompt that shapes every action the agent takes.

**Why this matters**: A 200-line CLAUDE.md replaces hours of onboarding. Every session starts with the same behavioral baseline. When the agent encounters a test that needs async waiting, the breadcrumb "await.Until()" is already loaded — it doesn't need to discover the package by exploring.

**Design principle**: Breadcrumbs should be **specific and actionable** ("use X instead of Y"), not vague ("follow best practices"). They should include the **why** when the reason isn't obvious ("CockroachDB doesn't support LISTEN/NOTIFY"). And they should use **strong language** for hard rules ("NEVER") vs. soft preferences ("prefer").

### Package Documentation as Discoverability

**Pattern**: Every shared package has a `doc.go` with a package comment and usage examples.

```go
// Package await provides polling-based assertions for asynchronous tests.
// Use instead of time.Sleep to make tests both reliable and fast.
//
// Basic usage:
//
//     err := await.Until(func() bool {
//         return order.Status == "COMPLETED"
//     })
//     require.NoError(t, err)
package await
```

**Why this matters for AI**: An agent exploring `shared/platform/` can read `doc.go` files to understand what each package does without reading implementation files. This is the difference between "grep for what might work" and "read the menu."

## Positive Consequences

* AI agents produce consistent code because conventions are enforced, not suggested
* New human contributors can onboard by reading guides and following existing patterns
* Parallel agent workflows (8+ teammates) don't diverge because guard rails are structural
* Errors are caught early and cheaply — linter failures cost seconds, not review cycles
* The codebase stays navigable as it grows — conventions scale, tribal knowledge doesn't

## Negative Consequences

* Additional infrastructure to maintain (architecture tests, convention scripts, guide front-matter)
* Generated files in git increase repository size
* Strict linting occasionally requires `//nolint` with explanation for legitimate exceptions
* New contributors must learn the convention system (mitigated by guides being self-documenting)

## The Underlying Principle

Every pattern in this ADR follows one principle: **make the right thing easy and the wrong thing hard.**

An AI agent (or a human in a hurry) will take the path of least resistance. If the easiest path produces correct, consistent code — because the type system prevents dimensional errors, the linter rejects unexplained suppressions, the architecture tests enforce structure, and CI blocks violations — then quality becomes the default, not the exception.

This is not about restricting what agents can do. It's about making a codebase that's **larger than any single context window** behave as if it fits in one — because every local decision is guided by structural constraints that encode the global design.

## Links

* [ADR-015: Service Directory Structure](./0015-service-directory-structure.md)
* [ADR-035: Multi-Asset Purity](./0035-multi-asset-purity.md)
* [docs/guides/README.md](../guides/README.md) — Convention guide index
* [tests/architecture/](../../tests/architecture/) — Architecture test suites
* [scripts/verify-service-conventions.sh](../../scripts/verify-service-conventions.sh) — CI convention checks

## Notes

The patterns documented here were not designed exclusively for AI. Google's proto-in-git practice predates LLMs. Architecture tests are standard in Java ecosystems (ArchUnit). Linter strictness is a quality engineering practice. What's novel is recognizing that these patterns compose into a system that makes AI contribution reliable at scale — and investing in them with that explicit goal.

The frontend (React/TypeScript) currently benefits from proto denormalization and type-safe generated clients but lacks the architecture test and convention enforcement layers that the Go backend has. This is a known gap, not a design choice.
