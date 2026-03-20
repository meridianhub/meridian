---
name: ai-native-codebase-architecture
description: Patterns and implementation checklist for making a codebase safe and productive for AI agent contributors
triggers:
  - Setting up a new codebase for AI-assisted development
  - Improving code quality infrastructure for AI contributors
  - Adding architecture tests, linter rules, or CI enforcement
  - Evaluating why AI agents produce inconsistent code
  - Building convention enforcement or guard rail systems
instructions: |
  This guide describes patterns for making a codebase safe and productive for AI agents.
  If you're being asked to set up or improve AI development infrastructure, follow the
  implementation checklist at the end. If you're working within a codebase that already
  uses these patterns, the guard rails enforce themselves - just follow existing conventions.
---

# AI-Native Codebase Architecture

Large codebases routinely exceed any single LLM context window. When AI agents are regular contributors - creating branches, implementing features, iterating on review feedback - a specific engineering problem emerges: **how do you maintain code quality and architectural coherence when contributors can only see a fraction of the codebase at any time?**

The traditional answer - "developers know the codebase" - doesn't apply. An AI agent starts each session with zero institutional memory beyond what's written down. It pattern-matches from what it can read. If patterns are inconsistent, the agent produces inconsistent code. If conventions exist only in people's heads, the agent violates them. If guard rails are advisory, the agent ignores them under pressure.

The same problem affects human contributors on large codebases, but AI amplifies it: agents work faster, touch more files, and can run in parallel. A single inconsistency can propagate across multiple PRs before anyone notices.

This guide describes patterns for keeping a large codebase coherent despite contributors (human and AI) who cannot hold it all in context at once, and provides an implementation checklist for setting them up. The examples come from Meridian (a multi-service Go/TypeScript platform), but the patterns are language-agnostic.

## The Core Idea: Contracts, Not Norms

Most codebases run on norms - conventions that people are expected to follow. Norms work when everyone knows them and cares about them. They break down when contributors don't know the norms exist (every AI agent, every new hire) or are under pressure to ship (everyone, eventually).

The alternative is contracts: **define what you expect, then enforce it automatically.** Types define expected shapes. Linters define expected style. Architecture tests define expected structure. CI defines expected quality. Coverage gates define expected test discipline. Each layer is a contract. A norm says "we prefer X." A contract says "the build fails if you don't do X."

Errors are cheapest to fix closest to where they're introduced. Cost increases with distance:

```
Layer 1: Code Design          - catches at write time    (seconds to fix)
Layer 2: Linters              - catches before push      (seconds)
Layer 3: Architecture Tests   - catches structural drift (minutes)
Layer 4: CI Pipeline          - catches on every PR      (minutes)
Layer 5: Coverage Gates       - catches missing tests    (minutes)
Layer 6: Code Review (bots)   - catches design issues    (hours)
Layer 7: Automated Retros     - catches workflow drift   (end of cycle)
```

No single layer is sufficient. The value is in the combination - each layer catches what the previous one missed. Layers 1-6 are automated enforcement. Layer 7 is the feedback loop that improves the contracts themselves.

## The Patterns

### Breadcrumb-Driven Behavior - Start Here

Project-level instruction files (loaded into AI agent context at session start) use specific phrases and keywords that activate preferred behavior. These act as "breadcrumbs" - compact triggers that reference larger patterns the agent should follow.

**This is the single highest-leverage pattern in this guide.** It costs nothing, requires no toolchain, and works with every AI coding tool. A team with nothing else from this document can start with a breadcrumb file today and see immediate improvement in AI output consistency.

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

### Make Bugs Self-Evident

The goal is a codebase where incorrect code looks wrong - ideally at compile time, but at minimum on first read. Three strategies compound on each other:

**Immutability by default.** Mutable state is where bugs hide. An AI agent that creates a mutable object and passes it through three functions has created three opportunities for something to silently modify it. Prefer value types, immutable data structures, and functions that return new values rather than modifying existing ones. When mutation is necessary, make it explicit and contained - a single method that owns the state transition, not scattered writes across a call chain.

**Functional style where it fits.** Pure functions (same input, same output, no side effects) are inherently testable and easy to reason about in isolation. An agent can understand a pure function by reading it - no need to trace what else might have changed. Push side effects to the edges (entry points, adapters) and keep the core logic pure.

**Type system as constraint.** Use the type system to prevent errors at compile time, not runtime. Look for categories of runtime errors that could become compile-time errors. Dimensional types, generated API clients, and non-Turing-complete scripting languages are three high-leverage approaches.

Go generics can enforce dimensional safety on quantities:

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

An agent cannot accidentally mix incompatible types, call an API with wrong parameter types, or generate a script that runs forever. The compiler rejects it before the agent even pushes.

### Linters - Target AI Failure Modes

Configure linters with strict enforcement and rules that specifically target AI failure modes.

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

These rules target the specific shortcuts agents take when iterating quickly. The linter turns bad habits into blocked pushes.

**Escape hatches matter.** Strict rules without explicit exceptions become "turn off the linter" rules. Design grep-able escape hatches for legitimate exceptions:

```
// myproject:large-file - this file contains generated lookup tables
```

The escape hatch serves three purposes: it makes the exception deliberate (not accidental), discoverable (grep for all exceptions across the codebase), and reviewable (the comment explains why). An agent encountering a rule violation can either fix the code or add an escape hatch with justification - both are better than suppressing the rule globally.

### Architecture Tests - Conventions as Contracts

Write tests that scan the codebase and fail if conventions are violated.

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

These tests work across language boundaries - Go test functions that walk the AST, Vitest suites that glob-and-assert over TypeScript, ArchUnit for Java/Kotlin. Even a shell script that greps the source tree is better than nothing.

An agent creating a new service or feature module will pattern-match from existing ones. Architecture tests guarantee that the pattern it copies is the canonical one, not an exception.

### CI Pipeline - The Non-Negotiable Safety Net

CI is where everything comes together. A robust CI pipeline is not optional for AI-assisted development - it is the primary mechanism that prevents AI agents from shipping broken code. An agent that can push, get fast feedback, and iterate is productive. An agent pushing into a vacuum with no CI feedback is dangerous.

A CI pipeline for sustained AI delivery should include:

| Check | Purpose | Why it matters for AI |
|-------|---------|----------------------|
| **Build** | Compilation, type checking | Catches type errors agents introduce |
| **Lint** | Style and correctness rules | Catches shortcuts agents take under pressure |
| **Unit tests** | Logic correctness | Catches regressions in changed code |
| **Integration tests** | Cross-component behavior | Catches interface mismatches between services |
| **Architecture tests** | Structural conventions | Catches convention drift (Layer 3) |
| **Convention checks** | Project-specific rules | Catches banned patterns, stale files |
| **Coverage gates** | Test completeness | Forces agents to write tests, not skip them |
| **Generated file freshness** | Denormalized files in sync | Catches stale proto output, OpenAPI specs |
| **Security scanning** | Dependency vulnerabilities | Catches vulnerable dependencies agents add |
| **Automated code review** | Design-level feedback | Catches issues static analysis misses |

**Coverage gates deserve emphasis.** Without them, the fastest path for an agent is "implement the feature, push, move on." Coverage gates - both project-wide minimum (e.g. 75%) and per-PR patch coverage (e.g. 70%) - force the agent to write tests as part of the implementation, not as a follow-up that never happens. Per-component coverage thresholds (e.g. no service drops below 80%) prevent quality from concentrating in well-tested areas while new code ships untested.

**Speed matters.** A CI pipeline that takes 30 minutes to return feedback is a pipeline that agents (and humans) learn to ignore. Optimize for fast feedback: parallelize test suites, cache dependencies, run the fastest checks first so failures surface early.

### Predictable Architecture - Every Service Looks the Same

All services follow the same internal architecture, enforced by architecture tests. The primary value isn't the architecture pattern itself - it's that every service is structurally identical, so an agent (or a human) can navigate any service without learning a new layout.

A hexagonal (ports and adapters) layout illustrates the principle:

```
services/{name}/
├── domain/              # Pure business logic, interfaces, errors - no infrastructure imports
├── adapters/
│   └── persistence/     # Implementations of domain interfaces
├── service/
│   └── entrypoint       # Orchestrates domain + adapters
├── migrations/          # Schema files
└── README.md            # Service guide
```

The boundaries that matter for enforcement:
- **domain/** has zero dependencies on infrastructure - it defines interfaces, the adapters implement them
- **adapters/** depends on domain (implements its interfaces) but never on service/
- **service/** depends on both (wires them together) but is the only layer that does
- **shared/** never imports services/ - dependency flows one direction only

The specific pattern - hexagonal, clean architecture, vertical slices - matters less than:

1. **Uniformity** - every service uses the same pattern, so navigating one means navigating all
2. **Enforcement** - architecture tests from Layer 3 catch violations as build failures, not review comments

Designate 2-3 services as **reference implementations** that agents should copy from.

### Denormalized Availability

Commit generated files (e.g. protobuf output, OpenAPI specs) to git despite being derivable from source definitions.

The analogy is database denormalization: trade storage redundancy for read-time performance. Here, "read-time" is "can a contributor start working immediately in a fresh worktree."

| Without denormalization | With denormalization |
|------------------------|---------------------|
| Clone -> install toolchain -> generate -> build | Clone -> build |
| Every worktree needs full generation toolchain | Every worktree is immediately buildable |
| N parallel workers each run generation (N x toolchain failures possible) | N parallel workers start coding immediately |

CI freshness checks regenerate and diff, failing if committed files are stale. This is the equivalent of a database trigger ensuring denormalized copies stay in sync.

**Prior art**: Google's monorepo checks in many categories of generated code so that builds work from a clean checkout.

### API Contract Specifications - Multiple Layers of Truth

Every service boundary has machine-readable contract specifications at multiple levels. A single contract format rarely covers all integration scenarios - synchronous APIs, auto-generated REST documentation, and asynchronous event schemas serve different consumers.

| Layer | Format | Purpose |
|-------|--------|---------|
| **Synchronous APIs** | Protocol Buffers, GraphQL SDL, OpenAPI | Service-to-service RPC definitions, request/response types |
| **REST/HTTP APIs** | OpenAPI/Swagger (often auto-generated from the above) | External consumers, documentation, client generation |
| **Async Events** | AsyncAPI | Event schemas per service, channel definitions |

The key is that all three are **generated from or defined alongside the source of truth**, committed to git (denormalized availability), and validated in CI.

A concrete example - an AsyncAPI spec for an event-driven service:

```yaml
# asyncapi/{service}.yaml - machine-readable event contract
channels:
  position-keeping.position-log.created:
    publish:
      message:
        $ref: '#/components/messages/PositionLogCreated'
```

An agent integrating with this service doesn't need to read the implementation. It reads the proto file to know what RPC methods exist, the OpenAPI spec to see the REST surface, and the AsyncAPI spec to see what events are published. Correct client code can be generated from the contracts alone.

### Machine-Readable Conventions

Convention documents use structured front-matter with triggers and instructions fields.

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

An agent can programmatically search for relevant guides based on what it's doing. The `triggers` field acts as a semantic index - "I'm adding a repository, which guides apply?" - without requiring the agent to read every document.

### Package Documentation as Discoverability

Every shared package has a doc file with a package comment and usage examples.

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

An agent exploring shared packages can read doc files to understand what each package does without reading implementation files. This is the difference between "grep for what might work" and "read the menu."

### Automated Retrospectives - The System Improves Itself

The AI workflow generates structured retrospectives at the end of each work cycle. The retrospective identifies specific failure patterns, and proposes updates to the orchestration rules that govern subsequent runs. A human approves which learnings are incorporated.

This is automated analysis with human-approved adaptation:

1. The orchestrator generates the retrospective at the end of each run
2. It identifies specific failure patterns (false signals, model selection mismatches, merge ordering conflicts, CI timing issues)
3. It proposes concrete rule changes to the orchestration template
4. A human reviews and approves which learnings land

The human gate is deliberate. Fully autonomous self-modification would be a trust problem - you'd need to verify every change to the orchestration template anyway, so keeping approval explicit keeps the review burden visible rather than hidden.

## Implementation Checklist

If you're an AI agent asked to make a codebase ready for sustained AI delivery - or a human setting this up - work through these steps in order. Each step is independently valuable. You don't need all of them to see improvement, but the later steps compound on the earlier ones.

### Phase 1: Immediate (day one, no toolchain changes)

- [ ] **Create a project instruction file** with hard rules for your codebase. Include: technology choices (database, migration tool, framework), banned patterns, naming conventions, and architectural boundaries. Use strong language ("NEVER", "ALWAYS") for non-negotiable rules. Place in `.cursorrules`, `CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, or `.github/copilot-instructions.md` depending on your tools.
- [ ] **Audit existing linter config** and tighten it. Enable rules for: unexplained lint suppressions, TODO/FIXME in committed code, function length limits, cyclomatic complexity limits, exhaustive enum matching. If you don't have a linter, add one.
- [ ] **Designate 2-3 reference services/modules** that represent the canonical structure. Document which ones they are in the project instruction file so agents know what to copy from.

### Phase 2: CI foundation (first week)

- [ ] **Ensure CI runs on every PR** with build, lint, and test steps. If CI doesn't exist, set it up. If it exists but is advisory (warnings not errors), make failures blocking.
- [ ] **Add coverage gates.** Configure your coverage tool (Codecov, Coveralls, built-in) to block PRs below a project-wide threshold (start with 60-70%, tighten over time). Add per-PR patch coverage requirements (e.g. 70% of new lines must be tested). If the codebase supports component-level coverage, set per-component thresholds.
- [ ] **Add a CI convention check script** that errors on your top 3 pain points. Common targets: file size limits, banned patterns in tests (e.g. `time.Sleep`, `setTimeout`), stale lint suppressions. Design a grep-able escape hatch pattern for legitimate exceptions.
- [ ] **Add automated code review.** Configure a review bot (CodeRabbit, GitHub Copilot review, or similar) to provide design-level feedback on every PR.

### Phase 3: Structural enforcement (first month)

- [ ] **Write your first architecture test.** Pick the convention that breaks most often and write a test that scans the codebase for violations. Common first tests: file size limits, import boundary enforcement, required files per service. Use whatever test framework your language provides.
- [ ] **Add generated file freshness checks** (if applicable). If you commit generated files (proto output, OpenAPI specs, etc.), add a CI step that regenerates and diffs. Fail if committed files are stale.
- [ ] **Enforce a consistent service/module structure.** Document the expected directory layout. Write architecture tests that verify every service matches. Make violations CI failures.
- [ ] **Add doc files to shared packages.** Every shared package or utility module should have a doc file (Go `doc.go`, Python module docstring, TypeScript JSDoc header) explaining what it does and showing usage examples.

### Phase 4: Discoverability and contracts (ongoing)

- [ ] **Add front-matter to convention documents.** Use structured triggers and instructions fields so AI agents can programmatically search for relevant guides.
- [ ] **Define API contracts at every service boundary.** Proto files, OpenAPI specs, AsyncAPI schemas - whatever fits your stack. Commit them to git and validate in CI.
- [ ] **Commit generated files** for any artifacts that would otherwise require toolchain setup to produce. Add freshness checks to keep them in sync.

### Phase 5: Feedback loop (when running regular AI workflows)

- [ ] **Set up automated retrospectives.** At the end of each AI work cycle, generate a structured retrospective identifying failure patterns. Propose rule changes to the orchestration template. Gate changes on human approval.
- [ ] **Review and update breadcrumbs** based on retrospective findings. If agents keep making the same mistake, add a breadcrumb. If a breadcrumb is stale, update or remove it.

## Trade-offs

These patterns work, but they aren't free:

- Architecture tests, convention scripts, and guide front-matter are infrastructure you maintain alongside the code they protect
- Generated files in git increase repository size
- Strict linting occasionally requires escape hatches with explanation for legitimate exceptions
- New contributors must learn the convention system (mitigated by guides being self-documenting)
- CI pipeline complexity grows with each check added - invest in speed to keep feedback loops fast

The cost is front-loaded. The payoff compounds - every new service, feature, and contributor benefits from guard rails that already exist.

## The Underlying Principle

Every pattern in this guide follows one principle: **make the right thing easy and the wrong thing hard.**

An AI agent (or a human in a hurry) will take the path of least resistance. If the easiest path produces correct, consistent code - because the type system prevents type errors, the linter rejects unexplained suppressions, the architecture tests enforce structure, and CI blocks violations - then quality becomes the default, not the exception.

This is not about restricting what agents can do. It's about making a codebase that's **larger than any single context window** behave as if it fits in one - because every local decision is guided by structural constraints that encode the global design.

## Further Reading

* [ADR-015: Standard Service Directory Structure](../adr/0015-standard-service-directory-structure.md)
* [ADR-035: Multi-Asset Purity](../adr/0035-multi-asset-purity.md)
* [tests/architecture/](../../tests/architecture/) - Architecture test suites
* [scripts/verify-service-conventions.sh](../../scripts/verify-service-conventions.sh) - CI convention checks

---

The patterns documented here were not designed exclusively for AI. Google's proto-in-git practice predates LLMs. Architecture tests are standard in Java ecosystems (ArchUnit). Linter strictness is a quality engineering practice. What's novel is recognizing that these patterns compose into a system that makes AI contribution reliable at scale - and investing in them with that explicit goal.
