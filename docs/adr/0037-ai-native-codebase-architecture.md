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
  prevent regression. When adding new code, match existing conventions - they exist so AI
  can pattern-match reliably. When in doubt, read the relevant guide in docs/guides/.
---

# 37. AI-Native Codebase Architecture

Date: 2026-03-20

## Status

Accepted

## Context

Large codebases routinely exceed any single LLM context window. When AI agents are regular contributors - creating branches, implementing features, iterating on review feedback - a specific engineering problem emerges: **how do you maintain code quality and architectural coherence when contributors can only see a fraction of the codebase at any time?**

The traditional answer - "developers know the codebase" - doesn't apply. An AI agent starts each session with zero institutional memory beyond what's written down. It pattern-matches from what it can read. If patterns are inconsistent, the agent produces inconsistent code. If conventions exist only in people's heads, the agent violates them. If guard rails are advisory, the agent ignores them under pressure.

The same problem affects human contributors on large codebases, but AI amplifies it: agents work faster, touch more files, and can run in parallel. A single inconsistency can propagate across multiple PRs before anyone notices.

This ADR documents patterns for keeping a large codebase coherent despite contributors (human and AI) who cannot hold it all in context at once. While the examples come from Meridian (a ~200k+ line Go / ~120k+ line TypeScript multi-service platform), the patterns are language-agnostic and applicable to any codebase where AI agents are regular contributors.

## Decision Drivers

* AI agents pattern-match from what they can read - conventions must be discoverable, not tribal
* Parallel agent workflows amplify inconsistency if guard rails are weak
* The codebase must be immediately buildable from any worktree without extra toolchain steps
* Errors caught later cost more - a CI failure wastes 10 minutes, a review comment wastes 30
* Human contributors benefit from the same patterns - this is not AI-specific infrastructure

## Decision Outcome

Adopt a **layered safety model** where each layer catches errors that slip through the previous one, combined with **denormalized availability** so every worktree is immediately usable, and **machine-readable conventions** so AI agents can discover patterns programmatically.

### The Layered Safety Model

Errors are cheapest to fix closest to where they're introduced. Each layer catches a different class of mistake, and cost increases with distance from introduction:

```
Layer 1: Type System          - catches at write time    (seconds to fix)
Layer 2: Linters              - catches before push      (seconds)
Layer 3: Architecture Tests   - catches structural drift (minutes)
Layer 4: CI Convention Checks - catches on every PR      (minutes)
Layer 5: Coverage Gates       - catches missing tests    (minutes)
Layer 6: Code Review (bots)   - catches design issues    (hours)
Layer 7: Automated Retros     - catches workflow drift   (end of cycle)
```

No single layer is sufficient. The value is in the combination - each layer catches what the previous one missed. Layers 1-6 are automated guard rails that enforce correctness on every change. Layer 7 is the feedback loop that improves the guard rails themselves.

### Layer 1: Type System - Make Invalid States Unrepresentable

**Pattern**: Use the type system to prevent errors at compile time, not runtime.

**Applying this to your codebase**: Look for categories of runtime errors that could become compile-time errors. Dimensional types, generated API clients, and non-Turing-complete scripting languages are three high-leverage approaches.

Meridian examples:

Go generics enforce dimensional safety on quantities:

```go
type Quantity[D Dimension] struct {
    Amount decimal.Decimal
    Unit   InstrumentCode  // "GBP", "kWh", "TONNE_CO2E"
}

var deposit Quantity[Currency]
var energy  Quantity[Electricity]
deposit = energy  // Compile error - can't mix dimensions
```

Proto-generated clients enforce API contracts:

```typescript
// Generated from .proto - type-checked at compile time
positionKeeping.initiateLog({
    amount: new Decimal("100.00"),  // Type-safe
    direction: Direction.CREDIT,     // Enum-validated
})
```

Starlark (non-Turing-complete) guarantees tenant saga termination - no while loops, no recursion, mathematically provable finite execution.

**Why this matters for AI**: An agent cannot accidentally mix incompatible types, call an API with wrong parameter types, or generate a script that runs forever. The compiler rejects it before the agent even pushes.

### Layer 2: Linters - Catch Mistakes Before Push

**Pattern**: Configure linters with strict enforcement and rules that specifically target AI failure modes.

AI agents under review pressure tend to: suppress lint warnings without explanation, leave TODOs instead of finishing work, write large monolithic functions, and skip exhaustive enum handling. Configure your linter to make these hard errors, not suggestions.

High-leverage lint rules for AI-heavy codebases:

| Rule category | What it catches | Example tools |
|---------------|----------------|---------------|
| Unexplained suppressions | `//nolint` or `# noqa` without justification | `nolintlint` (Go), `ruff` (Python) |
| Leftover markers | TODO/FIXME in committed code | `godox` (Go), custom ESLint rules |
| Function length | Functions exceeding threshold | `funlen` (Go), ESLint `max-lines-per-function` |
| Cyclomatic complexity | Functions too complex to reason about | `cyclop` (Go), ESLint `complexity` |
| Unsafe type operations | Unchecked type assertions/casts | `forcetypeassert` (Go), TypeScript `strict` |
| Exhaustive matching | Non-exhaustive enum/union switches | `exhaustive` (Go), TypeScript strict unions |
| Forbidden imports | Architectural boundary violations | `depguard` (Go), ESLint `no-restricted-imports` |

**Why this matters for AI**: These rules target the specific shortcuts agents take when iterating quickly. The linter turns bad habits into blocked pushes.

### Layer 3: Architecture Tests - Enforce Structural Contracts

**Pattern**: Write tests that scan the codebase and fail if conventions are violated.

Conventions are "statistical tendencies" without tests. If 12 out of 13 services follow a pattern and one doesn't, an AI agent might copy the outlier. Architecture tests make conventions into contracts.

Example architecture tests (language-agnostic concepts):

| Test | Contract |
|------|----------|
| File size limits | No non-test source file exceeds N lines |
| Function size limits | No function exceeds N lines (with ratchet for existing violations) |
| Naming conventions | Error variables, repository methods, etc. follow naming patterns |
| Import boundaries | Services don't import each other's internals |
| Dependency direction | Shared packages never depend on service packages |
| Structural requirements | Every service has expected entry point files |
| Documentation requirements | Every shared package has a doc file with usage examples |
| Feature module structure | Frontend feature modules follow consistent barrel export patterns |

These tests work across language boundaries. Meridian runs architecture tests for both Go backend (AST-walking test functions) and TypeScript frontend (glob-and-assert patterns). The same principle - convention as contract, not suggestion - applies regardless of language.

**Why this matters for AI**: An agent creating a new service or feature module will pattern-match from existing ones. Architecture tests guarantee that the pattern it copies is the canonical one, not an exception.

**Implementation note**: Most languages have tools for this - Go test functions that walk the AST, ArchUnit for Java/Kotlin, custom ESLint rules or Vitest suites for TypeScript. Even a shell script that greps the source tree is better than nothing.

### Layer 4: CI Convention Checks - Belt and Suspenders

**Pattern**: Run convention checks on every PR that error (not warn) on violations. Architecture tests run locally; CI checks run on every PR regardless. If an agent skips local tests (common in fast iteration), CI catches it.

Good CI convention checks include file size limits, banned patterns in test code (e.g. `time.Sleep` in Go, bare `setTimeout` in TypeScript), generated file freshness, and stale lint suppressions.

**Escape hatches matter**: Strict rules without explicit exceptions become "turn off the linter" rules. Design grep-able escape hatches for legitimate exceptions:

```go
// //meridian:large-file - this file contains generated lookup tables
```

The escape hatch serves three purposes: it makes the exception deliberate (not accidental), discoverable (grep for all exceptions across the codebase), and reviewable (the comment explains why). An agent encountering a size limit violation can either refactor the file or add an escape hatch with justification - both are better than suppressing the check globally.

### Layer 5: Coverage Gates - Prevent Test Regression

**Pattern**: Enforce minimum coverage on every PR - both project-wide and per-component. An agent implementing a feature might skip tests to save time. Coverage gates make this a blocking failure, not a suggestion.

**Why this matters for AI**: Without coverage gates, the fastest path for an agent is "implement the feature, push, move on." Coverage gates force the agent to write tests as part of the implementation, not as a follow-up that never happens.

### Layer 6: Code Review - Catch Design Issues

**Pattern**: Automated review bots and human reviewers provide design-level feedback that static analysis cannot catch. Agents iterate on review feedback autonomously - fix code, push, wait for re-review. The review loop is part of the agent workflow, not separate from it.

### Layer 7: Automated Retrospectives - The System Improves Itself

**Pattern**: The AI workflow generates structured retrospectives at the end of each work cycle. The retrospective identifies specific failure patterns, and proposes updates to the orchestration rules that govern subsequent runs. A human approves which learnings are incorporated.

This is not a manual process bolted on to an automated system. It is automated analysis with human-approved adaptation:

1. The orchestrator generates the retrospective at the end of each run
2. It identifies specific failure patterns (false signals, model selection mismatches, merge ordering conflicts, CI timing issues)
3. It proposes concrete rule changes to the orchestration template
4. A human reviews and approves which learnings land

The human gate is deliberate. Fully autonomous self-modification would be a trust problem - you'd need to verify every change to the orchestration template anyway, so keeping approval explicit keeps the review burden visible rather than hidden.

**Why this matters**: Layers 1-6 are static guard rails. Layer 7 is what makes the system adaptive - the guard rails themselves improve over time based on observed failures. Without it, you catch the same category of error forever. With it, each work cycle leaves the system better calibrated for the next.

### Denormalized Availability

**Pattern**: Commit generated files (e.g. protobuf output, OpenAPI specs) to git despite being derivable from source definitions.

**Analogy**: Database denormalization trades storage redundancy for read-time performance. Here, "read-time" is "can a contributor start working immediately in a fresh worktree."

| Without denormalization | With denormalization |
|------------------------|---------------------|
| Clone -> install toolchain -> generate -> build | Clone -> build |
| Every worktree needs full generation toolchain | Every worktree is immediately buildable |
| N parallel workers each run generation (N x toolchain failures possible) | N parallel workers start coding immediately |

**Consistency mechanism**: CI freshness checks regenerate and diff, failing if committed files are stale. This is the equivalent of a database trigger ensuring denormalized copies stay in sync.

**Prior art**: Google's monorepo checks in many categories of generated code so that builds work from a clean checkout. The principle is the same - availability at read time matters more than deduplication.

### Machine-Readable Conventions

**Pattern**: Convention documents use structured front-matter with triggers and instructions fields.

```yaml
---
name: repository-conventions
description: Canonical repository locations, interface naming, method naming
triggers:
  - Adding a repository to a service
  - Naming repository methods
  - Implementing persistence
instructions: |
  Repository interfaces live in domain/repository.go. Implementations in
  adapters/persistence/. Use standard verbs: Create, FindByID, Update, List, Delete.
---
```

**Why this matters for AI**: An agent can programmatically search for relevant guides based on what it's doing. The `triggers` field acts as a semantic index - "I'm adding a repository, which guides apply?" - without requiring the agent to read every document.

This works with any AI coding tool that loads project-level context. The front-matter is the convention's API.

### Breadcrumb-Driven Behavior - The Highest-Leverage Starting Point

**Pattern**: Project-level instruction files (loaded into AI agent context at session start) use specific phrases and keywords that activate preferred behavior. These act as "breadcrumbs" - compact triggers that reference larger patterns the agent should follow.

**This is probably the single highest-leverage pattern in this ADR.** It costs nothing, requires no toolchain, and works with every AI coding tool. A team with nothing else from this document can start with a breadcrumb file today and see immediate improvement in AI output consistency.

Most AI coding tools support project-level instruction files: `.cursorrules`, `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.github/copilot-instructions.md`, and similar. The specific file varies by tool, but the technique is universal.

Examples:

| Breadcrumb | Behavior it triggers |
|------------|---------------------|
| "Use `await.Until()` instead of `time.Sleep`" | Agent reaches for polling, not sleeping |
| "NEVER use `time.Sleep` in tests" | Absolute prohibition, agent doesn't rationalize exceptions |
| "Constructor injection for dependencies" | Agent uses DI, doesn't create singletons |
| "CockroachDB, not PostgreSQL" | Agent avoids PG-specific features (LISTEN/NOTIFY, PL/pgSQL) |
| "Atlas, NOT Flyway" | Agent uses correct migration tool |
| "NEVER edit existing migration files" | Agent creates new migrations, doesn't modify history |

These aren't documentation - they're **behavioral programming**. LLMs respond to pattern and emphasis. "NEVER" in capitals with context hits differently than a buried convention in a style guide. The instruction file is effectively a prompt that shapes every action the agent takes.

**Design principles for effective breadcrumbs**:
- **Specific and actionable**: "use X instead of Y", not "follow best practices"
- **Include the why** when the reason isn't obvious: "CockroachDB doesn't support LISTEN/NOTIFY"
- **Strong language for hard rules** ("NEVER") vs. soft preferences ("prefer")
- **Positive framing where possible**: "Use constructor injection" rather than "Don't use singletons"
- **Keep them current**: Stale breadcrumbs are worse than none - they train the agent on outdated patterns

### Predictable Architecture - Every Service Looks the Same

**Pattern**: All services follow the same internal architecture, enforced by architecture tests. The primary value isn't the architecture pattern itself - it's that every service is structurally identical, so an agent (or a human) can navigate any service without learning a new layout.

Meridian uses hexagonal (ports and adapters), which is a well-established pattern for separating business logic from infrastructure:

```
services/{name}/
├── domain/              # Pure business logic, interfaces, errors - no imports from adapters/
├── adapters/
│   └── persistence/     # Implementations of domain interfaces
├── service/
│   └── server.go        # Entry point - orchestrates domain + adapters
├── migrations/          # Schema files
└── README.md            # Service guide with YAML front-matter
```

The boundaries are strict and tested:
- **domain/** has zero dependencies on infrastructure - it defines interfaces, the adapters implement them
- **adapters/** depends on domain (implements its interfaces) but never on service/
- **service/** depends on both (wires them together) but is the only layer that does
- **shared/** never imports services/ - dependency flows one direction only

The architecture tests from Layer 3 enforce this uniformity across every service. If someone creates a service that puts persistence code in `domain/` or imports another service's internals, the build fails. Designate 2-3 services as **reference implementations** that agents should copy from - architecture tests guarantee all services match.

**Why this matters for AI**: An agent doesn't need to understand the whole codebase to make a safe change. Every service has the same shape, so "if I'm adding a repository method, I need `domain/` and `adapters/persistence/`" is always true, in every service. The architecture tests guarantee that a local change stays local - no hidden cross-service coupling to discover by accident.

### API Contract Specifications

**Pattern**: Every service boundary has a machine-readable contract specification - proto files, OpenAPI specs, AsyncAPI schemas, or whatever fits your stack. Commit these to git (denormalized availability) and validate them in CI.

When an agent needs to integrate with a service, it can read the contract specification without reading the implementation. The agent can generate correct client code from the contract alone - no need to trace through service internals.

### Package Documentation as Discoverability

**Pattern**: Every shared package has a doc file with a package comment and usage examples.

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

**Why this matters for AI**: An agent exploring shared packages can read doc files to understand what each package does without reading implementation files. This is the difference between "grep for what might work" and "read the menu."

## Adoption Guide - Where to Start

Not every team needs all of these patterns on day one. Here's a suggested order, from lowest effort to highest:

1. **Breadcrumb file** - Write a project instruction file with your hard rules. Takes 30 minutes. Immediate payoff.
2. **Linter strictness** (Layer 2) - Tighten existing linter config. Most codebases already have a linter; add AI-relevant rules.
3. **CI convention checks** (Layer 4) - Add a script that checks for your top 3 pain points. Shell scripts count.
4. **Coverage gates** (Layer 5) - Configure your coverage tool to block PRs below threshold.
5. **Architecture tests** (Layer 3) - Write tests for your most important structural invariants. Start with one, add more as violations appear.
6. **Denormalized availability** - Commit generated files, add freshness checks.
7. **Machine-readable conventions** - Add front-matter to existing documentation.
8. **Automated retrospectives** (Layer 7) - Close the feedback loop.

Each layer is independently valuable. You don't need all of them to see improvement.

## Positive Consequences

* AI agents produce consistent code because conventions are enforced, not suggested
* New human contributors can onboard by reading guides and following existing patterns
* Parallel agent workflows don't diverge because guard rails are structural
* Errors are caught early and cheaply - linter failures cost seconds, not review cycles
* The codebase stays navigable as it grows - conventions scale, tribal knowledge doesn't
* The system improves over time through automated retrospectives with human-approved adaptation

## Negative Consequences

* Additional infrastructure to maintain (architecture tests, convention scripts, guide front-matter)
* Generated files in git increase repository size
* Strict linting occasionally requires escape hatches with explanation for legitimate exceptions
* New contributors must learn the convention system (mitigated by guides being self-documenting)

## The Underlying Principle

Every pattern in this ADR follows one principle: **make the right thing easy and the wrong thing hard.**

An AI agent (or a human in a hurry) will take the path of least resistance. If the easiest path produces correct, consistent code - because the type system prevents type errors, the linter rejects unexplained suppressions, the architecture tests enforce structure, and CI blocks violations - then quality becomes the default, not the exception.

This is not about restricting what agents can do. It's about making a codebase that's **larger than any single context window** behave as if it fits in one - because every local decision is guided by structural constraints that encode the global design.

## Links

* [ADR-015: Standard Service Directory Structure](./0015-standard-service-directory-structure.md)
* [ADR-035: Multi-Asset Purity](./0035-multi-asset-purity.md)
* [docs/guides/README.md](../guides/README.md) - Convention guide index
* [tests/architecture/](../../tests/architecture/) - Architecture test suites
* [scripts/verify-service-conventions.sh](../../scripts/verify-service-conventions.sh) - CI convention checks

## Notes

The patterns documented here were not designed exclusively for AI. Google's proto-in-git practice predates LLMs. Architecture tests are standard in Java ecosystems (ArchUnit). Linter strictness is a quality engineering practice. What's novel is recognizing that these patterns compose into a system that makes AI contribution reliable at scale - and investing in them with that explicit goal.

These patterns apply across language boundaries. The Go backend and TypeScript frontend both have architecture tests, linter enforcement, and convention checks - the same layered model applied to different ecosystems.
