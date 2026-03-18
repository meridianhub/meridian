---
name: prd-codebase-consistency
description: Standardize naming, patterns, and docs across services for AI navigability
triggers:
  - Improving codebase consistency or standardization
  - Adding doc.go files or package documentation
  - Standardizing service file naming
  - Proto generated file management
  - Updating the new service checklist
instructions: |
  This PRD addresses micro-inconsistencies that compound across 16 services.
  Streams 1-4 are independent; Stream 5 depends on Stream 4.
  Prioritize Stream 1 (proto files) and Stream 2
  (doc.go) as they have the highest impact on AI code generation reliability.
  Update the new-bian-service-checklist.md to reflect all conventions established here.
---

# PRD-049: Codebase Consistency & AI Navigability

## Overview

A comprehensive codebase audit identified micro-inconsistencies across Meridian's 16 services
that individually are trivial but collectively degrade AI code generation reliability and
developer onboarding speed. This PRD addresses the structural improvements that make the
codebase self-describing - reducing the need to read surrounding code before writing new code.

### The Core Problem

When an AI (or new developer) needs to write code in a service, they currently must:

1. **Guess filenames** - Is it `grpc_service.go`, `server.go`, or `financial_accounting_service.go`?
2. **Read proto source** to understand types (generated `.pb.go` files aren't in git)
3. **Scan every shared package** to find utilities (no `doc.go` explaining purpose)
4. **Check each service independently** for error naming, repository shape, config location

Each guess costs a file read. Each file read burns context window. The result: AI performance
degrades as codebase size increases - not because the architecture is wrong, but because the
codebase isn't self-describing enough.

## Goals

1. **Commit proto-generated Go files** to git so types are readable without running codegen
2. **Add `doc.go`** to all shared packages so purpose is discoverable in one read
3. **Standardize service file naming** so navigation is predictable
4. **Document canonical patterns** for errors, repositories, and value types
5. **Update the new-service checklist** to encode all conventions established here

## Non-Goals

- Rewriting service internals or changing architecture
- New feature development
- Performance optimization
- Large file refactoring (covered in PRD-012)
- Test coverage improvements (covered in PRD-048)

## Complexity Assessment

Total estimated complexity: **21 story points** across 5 streams.

Streams 1-4 are independent. Stream 5 depends on Stream 4.

```mermaid
graph LR
    S1["Stream 1: Commit Proto Files<br/>5 pts"] --> DONE
    S2["Stream 2: Package Documentation<br/>5 pts"] --> DONE
    S3["Stream 3: Service Naming<br/>3 pts"] --> DONE
    S4["Stream 4: Convention Documentation<br/>5 pts"] --> S5
    S5["Stream 5: Checklist Update<br/>3 pts"] --> DONE
```

Stream 5 depends on Stream 4 (conventions must be documented before the checklist encodes them).

---

## Stream 1: Commit Proto-Generated Files to Git (5 pts)

### Rationale

Generated `.pb.go` files are currently excluded from git. This means:

- AI cannot read service API types without running `buf generate`
- Import aliases vary per service (`iav1`, `iba`, `posv1`) with no discoverable convention
- New worktrees require codegen before compilation
- Code review cannot show proto-generated type changes inline

This is the same rationale behind shadcn/ui's approach: copy the code into your project so it's
readable, searchable, and versionable. Generated code that's invisible is code that doesn't
exist for navigation purposes.

### Task 1.1: Add Proto-Generated Files to Git

**Problem**: `.pb.go` and `_grpc.pb.go` files are gitignored. Every worktree, every CI run,
and every AI session must regenerate them before types are readable.

**Files Affected**:

- `.gitignore` (remove `*.pb.go` exclusion if present)
- `api/proto/meridian/*/v1/*.pb.go` (generated files to commit)
- `api/proto/meridian/*/v1/*_grpc.pb.go` (generated files to commit)

**Acceptance Criteria**:

1. Run `buf generate api/proto` and commit all generated `.pb.go` files
2. Verify `.gitignore` does not exclude `*.pb.go` in the `api/proto/` tree
3. `go build ./...` succeeds from a clean checkout without running `buf generate`
4. Document in CONTRIBUTING.md: "Run `buf generate api/proto` after
   modifying `.proto` files and commit the generated output"

### Task 1.2: Document Proto Import Alias Convention

**Problem**: Services use inconsistent import aliases for proto packages:
`iav1`, `iba`, `posv1`, `pkv1`, `fav1` - no discoverable pattern.

**Files Affected**:

- `docs/guides/proto-conventions.md` (new)

**Acceptance Criteria**:

1. Document the canonical import alias pattern: `{service-abbreviation}v1`
2. List all current proto packages with their canonical alias
3. Include the mapping: proto path -> Go import path -> canonical alias
4. Reference from CONTRIBUTING.md

### Task 1.3: Add CI Check for Proto Freshness

**Problem**: Once proto files are committed, they can drift from `.proto` source if a developer
modifies a `.proto` file but forgets to regenerate.

**Files Affected**:

- `.github/workflows/quality.yml` (add proto freshness check)

**Acceptance Criteria**:

1. CI step runs `buf generate api/proto` and checks for uncommitted changes
2. Fails with clear message: "Proto generated files are stale. Run `buf generate api/proto` and commit."
3. Only runs when `.proto` files or `buf.gen.yaml` are modified (path filter)

---

## Stream 2: Package Documentation (5 pts)

### Rationale

`shared/pkg/` has 18 packages and `shared/platform/` has 21 packages. Fewer than half have
`doc.go` files. Without them, understanding a package requires scanning all exported symbols -
expensive for humans, catastrophic for AI context windows.

A `doc.go` file costs 5-15 lines and saves hundreds of lines of exploratory reads.

### Task 2.1: Add doc.go to All shared/pkg/ Packages

**Problem**: 14 of 18 `shared/pkg/` packages lack `doc.go`. An AI or developer discovering
`shared/pkg/mapping/` has no way to know what it does without reading implementation files.

**Packages missing doc.go** (verify current state before implementing):

- `amount/`, `bucketing/`, `credentials/`, `dispatch/`, `grpc/`, `health/`,
  `idempotency/`, `interceptors/`, `mapping/`, `money/`, `proto/`, `refdata/`,
  `tokens/`, `types/`, `validation/`

**Acceptance Criteria**:

1. Every package under `shared/pkg/` has a `doc.go` file
2. Each `doc.go` contains: package purpose (1 sentence),
   key types/functions (2-3 lines),
   usage example or "see X for usage" pointer
3. Format follows Go convention: `// Package X provides...`
4. No implementation code in `doc.go` files

**Example**:

```go
// Package mapping provides a bidirectional JSON transformation engine
// for converting between external formats and Meridian domain types.
//
// Key types: Engine, MappingSpec, TransformResult
// Used by: operational-gateway, api-gateway for inbound/outbound data mapping
package mapping
```

### Task 2.2: Add doc.go to All shared/platform/ Packages

**Problem**: 13 of 21 `shared/platform/` packages lack `doc.go`.

**Packages missing doc.go** (verify current state before implementing):

- `auth/`, `db/`, `env/`, `gateway/`, `kafka/`, `observability/`, `ports/`,
  `quantity/`, `ratelimit/`, `redislock/`, `sandbox/`, `scheduler/`, `tenant/`

**Acceptance Criteria**:

1. Every package under `shared/platform/` has a `doc.go` file
2. Same format requirements as Task 2.1
3. Platform packages should note whether they're infrastructure-only or have domain semantics

### Task 2.3: Add shared/ Navigation README

**Problem**: No documentation explains the `shared/pkg/` vs `shared/platform/` split.
Developers don't know where to add new shared code.

**Files Affected**:

- `shared/README.md` (new)

**Acceptance Criteria**:

1. Explains the two-tier split: `pkg/` = domain logic shared across services,
   `platform/` = infrastructure utilities
2. Decision guide: "If it knows about business concepts
   (money, sagas, instruments) -> pkg/.
   If it's infrastructure plumbing (DB, auth, events) -> platform/"
3. Lists all packages with one-line descriptions
   (can be generated from doc.go files)
4. Notes the canonical value type: `shared/platform/quantity.Quantity[D]`
   is the primary dimensional type; `shared/pkg/money` and
   `shared/pkg/amount` are convenience wrappers

---

## Stream 3: Service File Naming Standardization (3 pts)

### Rationale

Three different names for the same concept:

| Service | Main service file |
|---------|------------------|
| current-account | `grpc_service.go` |
| party | `grpc_service.go` |
| internal-account | `server.go` |
| reconciliation | `server.go` |
| financial-accounting | `financial_accounting_service.go` |

This means "find the gRPC handler registration" requires checking 2-3 filenames per service.

### Task 3.1: Rename Variant Service Files to server.go

**Problem**: `grpc_service.go` and `{service}_service.go` variants create
navigation ambiguity. ADR-015 establishes `server.go` as the canonical name
for the gRPC service implementation file. Several services diverge from this.

**Files Affected** (verify current state before implementing):

- `services/current-account/service/grpc_service.go` -> rename to `server.go`
- `services/party/service/grpc_service.go` -> rename to `server.go`
- `services/financial-accounting/service/financial_accounting_service.go`
  -> rename to `server.go`
- Any other services not using `server.go`

**Acceptance Criteria**:

1. All services use `server.go` as the main gRPC service file per ADR-015
2. All imports and references updated
3. All tests pass
4. `git log --follow` preserves file history (use `git mv`)

### Task 3.2: Standardize gRPC Handler File Splitting Convention

**Problem**: Some services split handlers into `grpc_{operation}_endpoints.go` files, others
inline everything. No convention for when to split.

**Files Affected**:

- `docs/guides/service-file-conventions.md` (new)

**Acceptance Criteria**:

1. Document the convention: split into `grpc_{operation}_endpoints.go`
   when `server.go` exceeds 400 LOC
2. Document the naming pattern: `server.go` (constructor + registration),
   `grpc_{operation}_endpoints.go` (handler implementations)
3. List the current state of each service for reference
4. Do NOT refactor existing services in this task (documentation only - refactoring is in PRD-012)

---

## Stream 4: Convention Documentation (5 pts)

### Rationale

Inconsistencies persist because conventions are implicit. Documenting them explicitly creates
a reference that both humans and AI can check before writing new code.

### Task 4.1: Document Error Naming Convention

**Problem**: Mixed patterns across services: `ErrNotFound` (generic) vs `ErrAccountNotFound`
(entity-prefixed). Neither is wrong, but the inconsistency means you can't predict error
names without reading each service's `errors.go`.

**Files Affected**:

- `docs/guides/error-conventions.md` (new)

**Acceptance Criteria**:

1. Establish the canonical pattern: entity-prefixed errors
   (`Err{Entity}NotFound`) for domain errors,
   generic errors (`ErrNotFound`) only in shared packages
2. Document the standard error set every domain should define: `NotFound`, `Conflict`, `InvalidStatus`, `OptimisticLock`
3. Document gRPC status code mapping convention (e.g., `ErrNotFound` -> `codes.NotFound`)
4. Include examples from existing services
5. Do NOT refactor existing errors in this task (documentation only - migration is future work)

### Task 4.2: Document Repository Pattern Convention

**Problem**: Repository interfaces live in `domain/repository.go` (position-keeping) or
`adapters/persistence/repository.go` (most others). Method names vary: `Create`/`Insert`,
`Find`/`Get`, `Update`/`UpdateStatus`.

**Files Affected**:

- `docs/guides/repository-conventions.md` (new)

**Acceptance Criteria**:

1. Establish canonical location: `adapters/persistence/repository.go` (matching majority pattern)
2. Document standard method naming: `Create`, `FindByID`, `List`, `Update`, `Delete`
3. Document optional methods: `CreateBatch`, `CreateWithOutbox`, `SoftDelete`
4. Document the GORM vs pgx decision: GORM for standard CRUD, pgx for performance-critical paths (position-keeping)
5. Include the standard constructor pattern and tenant scoping
6. Do NOT refactor existing repositories (documentation only)

### Task 4.3: Document Value Type Hierarchy

**Problem**: Three packages deal with "amounts of things": `shared/platform/quantity/`,
`shared/pkg/money/`, `shared/pkg/amount/`. Unclear which to use when.

**Files Affected**:

- `docs/guides/value-types.md` (new)

**Acceptance Criteria**:

1. Document the hierarchy: `Quantity[D]` is the foundational type
   (dimensional safety), `Money` wraps `Quantity[Currency]`
   for convenience, `Amount` provides decimal arithmetic
2. Decision guide: use `Quantity[D]` for multi-asset contexts,
   `Money` for currency-only contexts, `Amount` for raw decimals
3. Note which is used where (e.g., position-keeping uses `Quantity[D]`, current-account uses both)
4. Flag `shared/pkg/money/` as a thin wrapper with 2 imports - candidate for future removal

---

## Stream 5: Update New Service Checklist (3 pts)

Depends on: Stream 4 (conventions must be documented first).

### Task 5.1: Update new-bian-service-checklist.md with Established Conventions

**Problem**: The current checklist (`docs/guides/new-bian-service-checklist.md`) doesn't
encode the naming conventions, file patterns, or documentation requirements established
in this PRD and ADR-015.

**Files Affected**:

- `docs/guides/new-bian-service-checklist.md`

**Acceptance Criteria**:

1. Add task: "Create `doc.go` for every new package" with format example
2. Add task: "Create `errors.go` in `domain/` with entity-prefixed errors" referencing error-conventions.md
3. Update Task 7 (gRPC Service Handler) to mandate `server.go` naming per ADR-015
4. Add task: "Regenerate proto files and commit" after proto definition task
5. Add task: "Create service README.md with YAML frontmatter"
6. Reference the new convention docs (error-conventions.md, repository-conventions.md, value-types.md)
7. Add "Starlark Client Bindings" as a required task (currently most services have this but it's not in the checklist)

### Task 5.2: Add Service Consistency Verification Script

**Problem**: No automated way to verify a service follows conventions. Checklist is manual.

**Files Affected**:

- `scripts/verify-service-conventions.sh` (new)

**Acceptance Criteria**:

1. Script accepts a service name and checks:
   - `server.go` exists in `service/` (per ADR-015)
   - `domain/errors.go` exists
   - `doc.go` exists in each package
   - `README.md` exists with YAML frontmatter
   - Proto generated files exist and are committed
   - Atlas migration directory exists
2. Outputs pass/fail per check with actionable fix instructions
3. Can be run in CI for new service PRs

---

## Parallelization Summary

| Stream | Points | Dependencies | Parallelizable With |
|--------|--------|-------------|-------------------|
| 1. Proto Files | 5 | None | 2, 3, 4 |
| 2. Package Docs | 5 | None | 1, 3, 4 |
| 3. Service Naming | 3 | None | 1, 2, 4 |
| 4. Convention Docs | 5 | None | 1, 2, 3 |
| 5. Checklist Update | 3 | Stream 4 | 1, 2, 3 (after 4) |

**Critical path**: Stream 4 (5 pts) -> Stream 5 (3 pts) = 8 pts.
With parallelization, streams 1-4 run concurrently, then Stream 5 follows.

## Task Master Parsing Guidance

When parsing this PRD into Task Master tasks:

- **Create exactly 13 tasks** corresponding to the 13 numbered tasks (1.1 through 5.2)
- Each task maps to a single PR-able unit of work
- Preserve stream grouping in task numbering
- Mark Stream 5 tasks as depending on Stream 4 tasks
- All other streams have zero dependencies

**Task-to-stream mapping:**

| Task ID | PRD Reference | Stream |
|---------|--------------|--------|
| 1 | Task 1.1: Add Proto-Generated Files to Git | Proto Files |
| 2 | Task 1.2: Document Proto Import Alias Convention | Proto Files |
| 3 | Task 1.3: Add CI Check for Proto Freshness | Proto Files |
| 4 | Task 2.1: Add doc.go to shared/pkg/ | Package Docs |
| 5 | Task 2.2: Add doc.go to shared/platform/ | Package Docs |
| 6 | Task 2.3: Add shared/ Navigation README | Package Docs |
| 7 | Task 3.1: Rename Variant Service Files to server.go | Service Naming |
| 8 | Task 3.2: Standardize Handler File Splitting Convention | Service Naming |
| 9 | Task 4.1: Document Error Naming Convention | Convention Docs |
| 10 | Task 4.2: Document Repository Pattern Convention | Convention Docs |
| 11 | Task 4.3: Document Value Type Hierarchy | Convention Docs |
| 12 | Task 5.1: Update New Service Checklist | Checklist Update |
| 13 | Task 5.2: Add Service Consistency Verification Script | Checklist Update |

## Success Criteria

1. `go build ./...` succeeds from a clean clone without running `buf generate`
2. Every `shared/` package has a `doc.go` that explains its purpose in < 15 lines
3. Every service uses `server.go` as the main service file per ADR-015
4. Convention docs exist for errors, repositories, and value types
5. New service checklist references all established conventions
6. Verification script passes for all existing services (or documents known exceptions)
