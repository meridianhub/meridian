# Codebase Assessment: meridian

_Generated 2026-05-22._

## How to read this report

This is an improvement roadmap, not a verdict. It pairs two views:

- **Where the codebase is today** - the hotspot SVG shows current complexity and churn at a glance. Vivid red = complex AND actively changing = the files most likely to bite an agent (or a human) next week.
- **What scaffolding is in place to keep it from getting worse** - the 7-layer AI Readiness score measures whether the system enforces contracts that catch the issues the hotspots reveal.

A codebase can be 7/7 and still on fire (great scaffolding, legacy debt) - or 2/7 with a calm treemap (small codebase, no enforcement needed yet). The pair matters.

The "Top 3 Actions" table at the bottom names specific files. Start there.

## Hotspot snapshot

![Complexity hotspot](./complexity-heatmap.svg)

- **Files scored:** 3,264 (lizard, full coverage; `.pb.go`, `*_pb.ts`, and other generated bindings auto-excluded by the v1.3.0 treemap)
- **Churn window chosen:** last 12 months
- **Complexity profile:** p95 ccn 82 (max 477); p95 LOC 575 (max 5,753); total 656,693 LOC of hand-written code
- **Top hotspots** (composite: complexity x recent churn):
  1. `services/control-plane/internal/validator/manifest_validator_test.go` - 2,889 LOC, ccn 477, 16 commits (test)
  2. `services/current-account/service/coverage_unit_test.go` - 5,753 LOC, ccn 423, 6 commits (test; largest file in the repo)
  3. `services/payment-order/service/grpc_service_test.go` - 2,283 LOC, ccn 204, 17 commits (test)
  4. `services/position-keeping/domain/financial_position_log_test.go` - 1,597 LOC, ccn 336, 2 commits (test)
  5. `services/current-account/service/grpc_service_test.go` - 1,427 LOC, ccn 138, 27 commits (test)
  6. `frontend/src/features/manifests/components/manifest-graph.tsx` - 993 LOC, ccn 155, 15 commits (production)
  7. `services/mcp-server/internal/tools/economy_test.go` - 1,023 LOC, ccn 194, 6 commits (test)
  8. `frontend/src/features/manifests/components/manifest-graph.test.tsx` - 696 LOC, ccn 152, 11 commits (test)

With generated code now correctly filtered, the signal is unambiguous: **seven of the eight top hotspots are tests**, and the only hand-written production files that surface are `manifest-graph.tsx` (frontend) and `shared/pkg/saga/schema/service_modules.go` (474 LOC, ccn 136, 11 commits - the saga codegen). The `verify-service-conventions.sh` 800-line cap excludes test files; the frontend ESLint config has no complexity or function-length rules at all. Both gaps are real and addressable.

Size encodes lines of code, hue encodes cyclomatic complexity (red = high), saturation encodes recent git churn (vivid = active). Vivid red blocks are the migration risk.

## AI Readiness

**Score: 6.5 / 7** - AI-Native

| Layer | Status | Evidence | Gap |
|-------|--------|----------|-----|
| 0: Breadcrumbs | Present | `.github/claude-review-instructions.md` (795 lines, in-repo, scoped per PR); 24 service READMEs in skill-card format (`name`/`description`/`triggers`/`instructions`); 39 ADRs; architecture docs (`docs/architecture-layers.md`, `docs/patterns.md`, `docs/data-flows.md`, `docs/saga-handler-loading.md`); 10 skills in `docs/skills/`; service README lint workflow | Primary `CLAUDE.md` lives in the parent directory (`/Users/ben/dev/github.com/meridianhub/meridian/CLAUDE.md`, ~29KB), not tracked in the repo. Contributors who clone into a different parent layout lose it. No `AGENTS.md` for non-Claude tools |
| 1: Code Design | Present | Go 1.26 with phantom typed `Quantity[D Dimension]` (`shared/platform/quantity/`); TypeScript strict mode fully enabled (strict, noUnusedLocals, noUnusedParameters, noFallthroughCasesInSwitch, noUncheckedSideEffectImports); schema-driven service modules via `shared/pkg/saga/schema/handlers.yaml`; Starlark/CEL bounded expressiveness for tenant logic; immutability documented as core principle | None material |
| 2: Linters | Partial | Go: `.golangci.yml` v2 with funlen (80/60), cyclop (15), gocognit (20), gocyclo (15); godox catches TODO/FIXME; nolintlint forces explanations on every suppression; depguard for dependency boundaries; errorlint, exhaustive, errcheck; markdownlint, gitleaks, trivy all wired in | (a) Go complexity linters are excluded from `services/*/service/*.go`, `*/repository/*.go`, `*/adapters/*.go`, `*/internal/*.go`, `*/handler/*.go`, `*/client/*.go`, `*/worker/*.go`, `*/connector/*.go`, `*/starlark/*.go`, `shared/platform/kafka/`, `shared/pkg/saga/`, `services/tenant/provisioner/`, all `cmd/` - the majority of hand-written Go code. (b) Frontend `eslint.config.js` has no complexity, function-length, or max-lines rules - and the hotspot data now surfaces `frontend/src/features/manifests/components/manifest-graph.tsx` (993 LOC, ccn 155, 15 commits) and `shared/pkg/saga/schema/service_modules.go` (474 LOC, ccn 136) as production-code outliers that no rule currently catches |
| 3: Architecture Tests | Present | `tests/architecture/structure_test.go` enforces standard service layout (domain/, adapters/persistence/, service/), `server.go` presence, `doc.go` in every shared package, with explicit ratchet maps (`servicesWithoutServerGo`, `knownMissingDocGo`); `scripts/verify-service-conventions.sh` enforces 800-line file cap, bans `time.Sleep` in tests, checks proto freshness, flags stale `//nolint`; `shared/pkg/saga/linter.go` is a custom 712-line Starlark linter for AI-generated saga DSL; runs in `.github/workflows/conventions.yml` | 800-line rule explicitly exempts test files - hence `coverage_unit_test.go` at 5,753 LOC and four other tests above 1,500 LOC |
| 4: CI Pipeline | Present | 30 GitHub workflows: build, test (sharded), quality, codeql, security, proto, saga-validation, schema-validation, e2e, conventions, migrations, asyncapi, markdown, service-readme-lint, control-plane-ci, deploy-gate; concurrency groups with cancel-in-progress; path-filtered triggers | Per retro log, NFR benchmarks (TestNFR_SustainedThroughput) and some operational-gateway repository tests are documented flakies on shared runners |
| 5: Coverage Gates | Present | `codecov.yml` project 75% / patch 70% targets, `informational: false` on both (blocks merge); per-component 80% target across 48 components (24 backend, 24 frontend feature areas); flags for unittests/integration/frontend; carryforward enabled; `require_changes: true` | None material |
| 6: Code Review Bots | Present | CodeRabbit (`.coderabbit.yaml`) with `request_changes_workflow: true`, auto-approve, base branches develop+main, generated-file exclusions; Claude review bot (`.github/workflows/claude-review.yml`) keyed to a 795-line instruction file with explicit "focus on what's here, not what's missing" framing for incremental TM tasks; both active on recent PRs per merge history | None material |
| 7: AI Project Management | Present | Task Master integration: 111 task files, 6 PRDs, recent merged PRs reference task IDs (`#2208 feat: ... (063-saga-durability-parity.2)`, `#2207 ... (063-saga-durability-parity.1)`); 1,975-line marathon retro log with validated-vs-pending tracking column for every template change; wave-based lead/teammate orchestration patterns; per-tenant CI flake patterns documented and refined | `.taskmaster/` directory (state, tasks, PRDs) lives in parent dir, not tracked in repo - only `templates/` is checked in. Marathon retro log lives in `~/.claude/projects/`, not in the repo |

### Maturity Level

| Score | Level | Description |
|-------|-------|-------------|
| 0-1 | Not Ready | Agent will produce inconsistent, unvalidated code |
| 2-3 | Basic | Norms exist but aren't enforced. Agent works but drifts |
| 4-5 | Solid | Contracts catch most issues. Agent is productive |
| 6-7 | AI-Native | System self-improves. Agents work reliably at scale |

## Top 3 Actions

| # | Action | Layer | Effort | Command / First Step | Hotspot files this addresses | Issue |
|---|--------|-------|--------|---------------------|------------------------------|-------|
| 1 | Split the 5,753-line `current-account/service/coverage_unit_test.go` and add a test-file size rule to `verify-service-conventions.sh` (e.g. 2,000 lines, with the same `//meridian:large-file` escape hatch as production code). Seven of the eight top hotspots are tests because the 800-line rule explicitly excludes `_test.go`. | 3 | medium | Open `services/current-account/service/coverage_unit_test.go`; group cases by saga (`deposit`, `withdrawal`, `lien_active`, `lien_executed`, `lien_terminated`, `freeze`) and move each into its own `*_test.go` file. Then extend `scripts/verify-service-conventions.sh` with a `_test.go > 2000 lines` check alongside the existing 800-line rule. | `services/current-account/service/coverage_unit_test.go` (5,753), `services/control-plane/internal/validator/manifest_validator_test.go` (2,889), `services/payment-order/service/grpc_service_test.go` (2,283), `services/financial-accounting/service/financial_accounting_service_test.go` (1,943), `services/position-keeping/service/record_measurement_test.go` (1,695) | - |
| 2 | Add a complexity/length rule to the frontend ESLint config. Today `frontend/eslint.config.js` enables only TypeScript recommended + react-hooks + react-refresh + two project-specific rules - no `complexity`, `max-lines`, `max-lines-per-function`, or `max-statements`. The hotspot data already shows the gap: `manifest-graph.tsx` is 993 LOC with ccn 155 and 15 commits in the last year, second only to tests in the entire repo. | 2 | small | Edit `frontend/eslint.config.js`: add `'complexity': ['warn', 15]`, `'max-lines': ['warn', 500]`, `'max-lines-per-function': ['warn', 80]` to the main `rules` block. Run `npm run lint` in `frontend/` and triage offenders with `// eslint-disable-next-line max-lines -- pre-existing` (matching the Go side's nolint-with-explanation discipline). | `frontend/src/features/manifests/components/manifest-graph.tsx` (993, ccn 155, 15 commits), `frontend/src/features/manifests/components/manifest-graph.test.tsx` (696, ccn 152, 11 commits) | - |
| 3 | Pick one excluded path in `.golangci.yml` and un-exclude it with a ratchet. Start with `services/*/service/*.go` - the gRPC entry point layer where complexity creeps in via validation/conversion/persistence. Use `//nolint:cyclop // <explanation>` per existing offender (nolintlint already requires the explanation). The exclusion list currently covers most hand-written Go production code, so cyclop/gocognit/funlen catch nothing meaningful there today. | 2 | medium | Delete the `services/.*/service/.*\.go` block from `.golangci.yml` exclusions; run `golangci-lint run ./services/.../service/...`; add `//nolint:cyclop // pre-existing, tracked in <issue>` for each existing violation. Production-code hotspots already visible from sibling data: `shared/pkg/saga/schema/service_modules.go` (474 LOC, ccn 136) and large service files documented previously - `services/control-plane/internal/applier/executor.go` (708), `services/forecasting/starlark/runner.go` (685), `services/financial-accounting/client/starlark.go` (691). | `shared/pkg/saga/schema/service_modules.go`, `services/control-plane/internal/applier/executor.go`, `services/forecasting/starlark/runner.go`, `services/financial-accounting/client/starlark.go` | - |

### Why these three?

The new v1.3.0 treemap, with generated bindings filtered, exposes the real risk surfaces: oversized test files (Action 1) and a frontend with no complexity rules at all (Action 2). Action 1 is the same as last run's #1 - seven of eight top hotspots are tests, which the 800-line cap explicitly exempts. Action 2 is new evidence: `manifest-graph.tsx` and `service_modules.go` are now visible as production-code outliers because `.pb.ts` files no longer dominate the leaderboard; the frontend has no Layer 2 enforcement at all. Action 3 stays from last run - the Go exclusion list still covers most production code - but with sharper evidence now that `service_modules.go` is a measurable outlier inside a path the linter does scan but doesn't enforce complexity on.

## Additional Opportunities

- Track `CLAUDE.md` and `.taskmaster/` inside the repo so the breadcrumb system is portable to standalone clones. Today both live in the parent dir.
- Add `AGENTS.md` at repo root so non-Claude AI tools (Cursor, Aider, Continue, Gemini CLI) get the same entry point as Claude. Most could happily point straight at `.github/claude-review-instructions.md`.
- Triage the 16 production Go files in the 600-800 LOC range (`repository.go` 795 and 767, `provisioning_worker.go` 765, `import.go` 755, `postgres_repository.go` 749, `audit.go` 735, `manager.go` 731, `account.go` 721, `container.go` 716, `linter.go` 712) before they hit the cap and need `//meridian:large-file` escape hatches.
- The exclusion comment `# Disabled: gRPC services return terminal errors` for `wrapcheck` is plausible but worth revisiting - wrapping in adapter layers above the service entry would still benefit observability, and a more targeted exclusion (just the service entry methods) would be tighter.
- Re-enable `unparam` once the upstream golangci-lint generics panic is fixed (referenced inline in `.golangci.yml`).

## Strengths

- **Schema-driven everything**: `handlers.yaml` -> auto-generated Starlark clients -> AI-constrained saga DSL. The toolchain is genuinely original work and is what makes the AI orchestration in Layer 7 trustworthy. The custom 712-line Starlark linter in `shared/pkg/saga/linter.go` enforces it.
- **The 800-line rule has an escape hatch with discoverability** (`//meridian:large-file` comment, surfaced in the error message). Exactly one file currently uses it (`services/mcp-server/internal/auth/oidc.go`). That's the right discipline.
- **The marathon retro log is rare**: 1,975 lines of structured "this template change was validated / pending / removed". Most projects either don't run retros or don't keep the result in a queryable form. The "Template Changes" column with a Validated? status is the feedback loop closing.
- **Coverage gates are component-scoped at 80% across 48 components**, not just a single project-wide number. That's the difference between "we hit the average" and "no component can rot".
- **The architecture tests use a ratchet pattern with explicit allowlists** (`servicesWithoutServerGo`, `knownMissingDocGo`, `servicesWithNonStandardLayout`) and the instruction `Do NOT add new entries`. That's exactly how you migrate a system without freezing it.

---

_Report generated by [`/ai-native-toolkit:assess`](https://github.com/bjcoombs/ai-native-toolkit). Install in any Claude Code session: `/plugin marketplace add https://github.com/bjcoombs/ai-native-toolkit` then `/plugin install ai-native-toolkit@ai-native-toolkit`._
