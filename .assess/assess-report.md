# Codebase Assessment: meridian

_Generated 2026-05-21._

## How to read this report

This is an improvement roadmap, not a verdict. It pairs two views:

- **Where the codebase is today** - the hotspot SVG shows current complexity and churn at a glance. Vivid red = complex AND actively changing = the files most likely to bite an agent (or a human) next week.
- **What scaffolding is in place to keep it from getting worse** - the 7-layer AI Readiness score measures whether the system enforces contracts that catch the issues the hotspots reveal.

A codebase can be 7/7 and still on fire (great scaffolding, legacy debt) - or 2/7 with a calm treemap (small codebase, no enforcement needed yet). The pair matters.

The "Top 3 Actions" table at the bottom names specific files. Start there.

## Hotspot snapshot

![Complexity hotspot](./complexity-heatmap.svg)

- **Files scored:** 3,400 (lizard, full coverage; no scc fallback needed)
- **Churn window chosen:** last 12 months
- **Complexity profile:** p95 ccn 88 (max 864); p95 LOC 613 (max 5,753); total 745,851 LOC
- **Top hotspots** (composite: complexity x recent churn):
  1. `api/proto/meridian/current_account/v1/current_account.pb.go` - 5,084 LOC, ccn 864, 9 commits (generated; ignore)
  2. `api/proto/meridian/position_keeping/v1/position_keeping.pb.go` - 3,295 LOC, ccn 561, 9 commits (generated; ignore)
  3. `services/control-plane/internal/validator/manifest_validator_test.go` - 2,889 LOC, ccn 477, 16 commits (test file; outside linter complexity rules)
  4. `services/current-account/service/coverage_unit_test.go` - 7,246 LOC (highest LOC in the repo; test file, exempt)
  5. `services/payment-order/service/grpc_service_test.go` - 2,918 LOC (test file)

Generated `.pb.go` files dominate the absolute top of the treemap by both size and complexity, which is expected. After filtering them out, **the real signal is concentrated in test files**: three of the four largest hand-written files in the repo are tests, all exempt from the 800-line file cap and from all complexity linters. The largest, `coverage_unit_test.go`, is 7,246 lines - 9x the production file cap. The behaviour-under-test isn't bloated, but the test files themselves are growing past the point of review-ability.

Size encodes lines of code, hue encodes cyclomatic complexity (red = high), saturation encodes recent git churn (vivid = active). Vivid red blocks are the migration risk.

## AI Readiness

**Score: 6.5 / 7** - AI-Native

| Layer | Status | Evidence | Gap |
|-------|--------|----------|-----|
| 0: Breadcrumbs | Present | `.github/claude-review-instructions.md` (795 lines, in-repo, scoped per PR); 24 service READMEs in skill-card format (`name`/`description`/`triggers`/`instructions`); 39 ADRs; architecture docs (`docs/architecture-layers.md`, `docs/patterns.md`, `docs/data-flows.md`, `docs/saga-handler-loading.md`); 10 skills in `docs/skills/`; service README lint workflow | Primary `CLAUDE.md` lives in the parent directory (`/Users/ben/dev/github.com/meridianhub/meridian/CLAUDE.md`, ~29KB), not tracked in the repo. Contributors who clone into a different parent layout lose it. No `AGENTS.md` for non-Claude tools |
| 1: Code Design | Present | Go 1.26 with phantom typed `Quantity[D Dimension]` (`shared/platform/quantity/`); TypeScript strict mode fully enabled (strict, noUnusedLocals, noUnusedParameters, noFallthroughCasesInSwitch, noUncheckedSideEffectImports); schema-driven service modules via `shared/pkg/saga/schema/handlers.yaml`; Starlark/CEL bounded expressiveness for tenant logic; immutability documented as core principle | None material |
| 2: Linters | Partial | `.golangci.yml` v2 with funlen (80/60), cyclop (15), gocognit (20), gocyclo (15); godox catches TODO/FIXME; nolintlint forces explanations on every suppression; depguard for dependency boundaries; errorlint, exhaustive, errcheck; markdownlint, gitleaks, trivy all wired in | Complexity linters (cyclop/gocyclo/gocognit/funlen) are excluded from `services/*/service/*.go`, `*/repository/*.go`, `*/adapters/*.go`, `*/internal/*.go`, `*/handler/*.go`, `*/client/*.go`, `*/worker/*.go`, `*/connector/*.go`, `*/starlark/*.go`, `shared/platform/kafka/`, `shared/pkg/saga/`, `services/tenant/provisioner/`, all `cmd/`, all `_test.go` - which is the majority of hand-written code. Rules ratchet new work elsewhere but the legacy is unfenced |
| 3: Architecture Tests | Present | `tests/architecture/structure_test.go` enforces standard service layout (domain/, adapters/persistence/, service/), `server.go` presence, `doc.go` in every shared package, with explicit ratchet maps (`servicesWithoutServerGo`, `knownMissingDocGo`); `scripts/verify-service-conventions.sh` enforces 800-line file cap, bans `time.Sleep` in tests, checks proto freshness, flags stale `//nolint`; `shared/pkg/saga/linter.go` is a custom 712-line Starlark linter for AI-generated saga DSL; runs in `.github/workflows/conventions.yml` | 800-line rule explicitly exempts test files - hence `coverage_unit_test.go` at 7,246 lines |
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

| # | Action | Layer | Effort | Command / First Step | Hotspot files this addresses |
|---|--------|-------|--------|---------------------|------------------------------|
| 1 | Split the 7,246-line `current-account/service/coverage_unit_test.go` and add a test-file size rule to `verify-service-conventions.sh` (e.g. 2,000 lines, with an exempt comment for indivisible cases). Tests grew past review-ability because the 800-line rule explicitly excludes `_test.go`. | 3 | medium | Open `services/current-account/service/coverage_unit_test.go`; group cases by saga (`deposit`, `withdrawal`, `lien_active`, `lien_executed`, `lien_terminated`, `freeze`) and move each into its own `*_test.go` file. Then add a `_test.go > 2000 lines` check in `scripts/verify-service-conventions.sh` alongside the existing 800-line rule. | `services/current-account/service/coverage_unit_test.go` (7,246), `services/control-plane/internal/validator/manifest_validator_test.go` (3,415), `services/payment-order/service/grpc_service_test.go` (2,918), `services/financial-accounting/service/financial_accounting_service_test.go` (2,367) |
| 2 | Track `CLAUDE.md` and the Task Master state inside the repo so the breadcrumbs are portable. Today both live in `/Users/ben/dev/github.com/meridianhub/meridian/` (parent dir), so any contributor who clones `meridian-main` standalone gets none of it - including the principles encoded in CLAUDE.md and the 1,975-line marathon retro log. | 0, 7 | small | `cp /Users/ben/dev/github.com/meridianhub/meridian/CLAUDE.md meridian-main/CLAUDE.md`; decide whether to also vendor `.taskmaster/tasks.json` and `.taskmaster/prd/` into the repo (worth it - PRDs are the source-of-truth for in-flight work) or keep them out but add `AGENTS.md` at the root pointing tools to `.github/claude-review-instructions.md` and `docs/`. | - (orientation, not file-specific) |
| 3 | Pick one excluded path in `.golangci.yml` and un-exclude it with a ratchet. Start with `services/*/service/*.go` - that's the gRPC entry point layer where complexity tends to creep in via validation/conversion/persistence stacks. Use `//nolint:cyclop // <explanation>` per existing offender (nolintlint already requires the explanation). The exclusion list currently covers most hand-written production code, so cyclop/gocognit/funlen catch nothing meaningful today. | 2 | medium | Delete the `services/.*/service/.*\.go` block from `.golangci.yml` exclusions; run `golangci-lint run ./services/.../service/...`; for each existing violation, add `//nolint:cyclop // pre-existing, tracked in <issue>` and open follow-up tasks. Likely first offenders given file size: `services/control-plane/internal/applier/executor.go` (708 LOC), `services/control-plane/internal/applier/grpc_handler.go` (668), `services/forecasting/starlark/runner.go` (685). | `services/control-plane/internal/applier/executor.go`, `services/control-plane/internal/applier/grpc_handler.go`, `services/forecasting/starlark/runner.go`, `services/financial-accounting/client/starlark.go` (691) |

### Why these three?

The hotspot data shows the only real risk surfaces left in this codebase: oversized test files (the 800-line rule excludes them by construction) and a complexity-linter exclusion list that covers most hand-written code. Action 1 closes the test-file loophole on the system that needs it most. Action 2 makes the existing breadcrumb system portable - everything else here is in-repo, but CLAUDE.md and Task Master state are not, which is the one place a fresh contributor or fork is left in the dark. Action 3 picks the smallest defensible un-exclusion to make Layer 2 mean something for production code; the rest of the exclusion paths can follow the same pattern once a precedent exists.

## Additional Opportunities

- Add `AGENTS.md` at repo root so non-Claude AI tools (Cursor, Aider, Continue, Gemini CLI) get the same entry point as Claude. Most could happily point straight at `.github/claude-review-instructions.md`.
- Triage the 16 production files in the 600-800 LOC range (`repository.go` 795 and 767, `provisioning_worker.go` 765, `import.go` 755, `postgres_repository.go` 749, `audit.go` 735, `manager.go` 731, `account.go` 721, `container.go` 716, `linter.go` 712) before they hit the cap and need `//meridian:large-file` escape hatches.
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
