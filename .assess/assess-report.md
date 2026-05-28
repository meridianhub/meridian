# Codebase Assessment: meridian

_Generated 2026-05-28 by `/ai-native-toolkit:assess` v1.10.0._

## How to read this report

This is an improvement roadmap, not a verdict. It measures one thing: **is the codebase kept honest, not just scaffolded.** It pairs three views:

- **Where the codebase is today** - the complexity heatmap shows current complexity and churn. Vivid red = complex AND actively changing = the files most likely to bite an agent (or a human) next week.
- **Whether an agent can navigate it** - the doc graph shows the docs' link structure: how much is reachable from the entry point, and which docs are stale maps of churning code.
- **What keeps it from getting worse** - the AI Readiness score (0-8) across three bands: read-side foundation (can the agent form a true picture?), write-side enforcement (can it be trusted to produce good output?), and meta (does the system keep itself honest over time?).

A codebase can be 8/8 and still on fire (great scaffolding, legacy debt) - or 2/8 with a calm treemap (small codebase, no enforcement needed yet). The views matter together.

The "Top 3 Actions" table at the bottom names specific files. Start there.

## What changed since the v1.4 snapshot

This is a fourth re-run, on `/assess` v1.10.0. Two big additions from v1.10 land squarely on this codebase:

- **The instruction-file grader now finds `.github/claude-review-instructions.md`** and grades it `A` (score 100, 31 positive directives, 8 tradeoff phrases, 30 path references). In v1.4 it scored `F` because only canonical repo-root paths were checked. **Layer 0a swings from Partial back to Present.** This obsoletes most of `assess-2026-05-22.4` ("Move CLAUDE.md into repo root") - the file isn't there, but a grade-A substitute is.
- **A deterministic doc-link graph** now scores Layer 0b on the same truth-pressure scale: structure, staleness, broken refs. It exposes a real navigability gap that previous runs couldn't measure - **only 30% of docs are reachable from `README.md`** and 5 stale ADR hubs are routing traffic through frozen pages beside a churning codebase. Layer 0 net: stays **Partial**, but for a different reason than before.
- **Layer 1 (NEW): Runtime Liveness scored Present** - rung 3 "Reachable". The deterministic core found 13 runbooks under `docs/runbooks/` and `docs/operations/` containing executable observability queries (`logcli`, `promtool`, `kubectl logs`, etc.). An agent can reach runtime state via those paths.
- **Code diff:** 9 persistent, 0 graduated, 0 new, **1 regressed**: `frontend/src/features/manifests/components/manifest-graph.tsx` ccn climbed +1 (155 → 156). Marginal but worth noting - still no linter rule fences it.

## Snapshots

### Complexity - riskiest to change

[![Complexity hotspot](./complexity-heatmap.svg)](./complexity-heatmap.svg)

- **Files scored:** 3,264 (lizard, full coverage; generated bindings auto-excluded)
- **Churn window chosen:** last 12 months
- **Complexity profile:** p95 ccn 82 (max 477); p95 LOC 575 (max 5,753); total 656,693 LOC of hand-written code
- **Top hotspots** (composite: complexity x recent churn):
  1. `services/control-plane/internal/validator/manifest_validator_test.go` - 2,889 LOC, ccn 477, 16 commits (test)
  2. `services/current-account/service/coverage_unit_test.go` - 5,753 LOC, ccn 423, 6 commits (test; largest file in the repo)
  3. `services/payment-order/service/grpc_service_test.go` - 2,283 LOC, ccn 204, 17 commits (test)
  4. `services/position-keeping/domain/financial_position_log_test.go` - 1,597 LOC, ccn 336, 2 commits (test)
  5. `services/current-account/service/grpc_service_test.go` - 1,427 LOC, ccn 138, 27 commits (test)
  6. `frontend/src/features/manifests/components/manifest-graph.tsx` - 993 LOC, ccn 156, 15 commits (production; **regressed +1 ccn since last run**)
  7. `services/mcp-server/internal/tools/economy_test.go` - 1,023 LOC, ccn 194, 6 commits (test)

Size encodes lines of code, colour encodes cyclomatic complexity (dark red = high), saturation encodes recent git churn (vivid = active). Vivid red blocks are the migration risk.

### Doc navigability - can an agent find its way?

[![Doc map](./doc-graph.svg)](./doc-graph.svg)

**Navigability, in words.** Of 254 docs, only **30% are reachable** by following links from `README.md` - an agent landing on the entry point can discover roughly a third of the documentation by traversal. The rest sits across **43 disconnected islands**, with **24% orphans** (62 docs that nothing links to). The repo also has **15 broken links to ghost files** (the link target doesn't exist) and **19 missing cross-references** (one doc mentions another's filename in prose but never links to it). To a human this is annoying; to an agent traversing by graph, it caps coverage at 30%.

**Lying maps (stale docs of churning code).** Terms first: **stale-days** is days since the doc itself was last edited; **subject churn** is commits in the window to the code the doc describes; **centrality (pagerank)** is how many other docs route through this one. The deterministic core ranks docs by `pagerank x stale-days / subject-churn` - the highest scores are central docs sitting frozen beside genuinely churning code. The worst 5 (all ADRs):

| Doc | Pagerank | Stale-days | Subject churn | Priority |
|---|---|---|---|---|
| `docs/adr/0004-event-schema-evolution.md` | 0.061 | 59d | 8,588 commits | 87 |
| `docs/adr/0005-adapter-pattern-layer-translation.md` | 0.066 (top) | 59d | 8,588 commits | 71 |
| `docs/adr/0014-financial-instrument-reference-data.md` | 0.021 | 88d | 8,588 commits | 46 |
| `docs/adr/0002-microservices-per-bian-domain.md` | 0.047 | 53d | 8,588 commits | 40 |
| `docs/adr/0003-database-schema-migrations.md` | 0.055 | 53d | 8,588 commits | 39 |

Caveat from the model: all five have `subject_method: repo-baseline`, meaning their "subject churn" is the whole repo's 12-month churn rather than the specific subsystem they describe. That's a coarse proxy. But ADRs 0004 (event schema), 0005 (adapter pattern), and 0002 (microservices/BIAN) sit at the top of the pagerank list (22, 17, 29 incoming links respectively) - they're the load-bearing entry points an agent following links from any service README will route through. If they're stale, the lying map effect compounds.

**Doc-to-code association.** Only **21.7%** of docs (55 of 254) have a derivable mapping to specific code; the other 78% are mapped only via `repo-baseline` (the weakest signal). And the modularity check fires: with 535 module directories and only 47 having a base `README.md` (**8.8% coverage**), this is flagged as a large repo without per-module maintained base docs. Most service-level READMEs are excellent (24 in the skill-card format) - the gap is at the deeper directory levels.

Colour = staleness (vivid red = a frozen doc beside churning code = a lying map); structure = reachability (centre = entry, rim = unreachable, dashed ring = orphan); size = file length. Open the SVG directly for hover tooltips on each node.

## AI Readiness

**Score: 7.0 / 8** - AI-Native

| Layer | Band | Status | Evidence | Gap |
|-------|------|--------|----------|-----|
| 0: Agent Instructions & Navigability | read | Partial | **0a (instructions):** `.github/claude-review-instructions.md` graded **A** (score 100, 31 positive directives, 8 tradeoff phrases, 30 path references, 85 days fresh). v1.10 grader now finds this file - in v1.4 it returned F. **0b (navigability):** 254 docs, 622 links, 1,975-line marathon retro log in `~/.claude/projects/`, 24 service READMEs in structured skill-card format, 39 ADRs, architecture docs, 10 skills in `docs/skills/` | **0b is the gap.** Only 30% reachability from `README.md`; 43 islands; 62 orphan docs (24%); **15 broken links to ghost files**; **19 missing cross-references**; 5 stale ADR hubs (top: ADR-0004 event schema at priority 87, ADR-0005 adapter pattern at 71); only 21.7% of docs map to specific code; 8.8% module base-doc coverage (47 / 535 module directories) |
| 1: Runtime Legibility / Liveness | read | Present | **Rung 3 - Reachable.** OpenTelemetry, Prometheus, structured logging (logrus) all instrumented. 53 discoverable signals across `docs/runbooks/`, `docs/operations/`, `docs/skills/`. **13 docs contain runnable queries** an agent can invoke: `docs/runbooks/disaster-recovery.md`, `event-router.md`, `incident-response.md`, `internal-account-operations.md`, `market-information-operations.md`, `production-deployment.md`, `saga-failure-recovery.md`, `saga-validation-failure.md`, `troubleshooting-saga-handlers.md` etc. | Dead-code scan degraded (no `staticcheck`/`deadcode`/`knip`/`ts-prune` on PATH) - install one to cross-check intra-repo dead exports. Liveness boundary: this scores what the repo provides; agent's live runtime access depends on actual MCP wiring not visible from the repo |
| 2: Code Design | write | Present | Go 1.26 with phantom typed `Quantity[D Dimension]` (`shared/platform/quantity/`); TypeScript strict mode fully enabled (strict, noUnusedLocals, noUnusedParameters, noFallthroughCasesInSwitch, noUncheckedSideEffectImports); schema-driven service modules via `shared/pkg/saga/schema/handlers.yaml`; Starlark/CEL bounded expressiveness; immutability as core principle | None material |
| 3: Linters | write | Partial | Go: `.golangci.yml` v2 with funlen (80/60), cyclop (15), gocognit (20), gocyclo (15); godox catches TODO/FIXME; nolintlint forces explanations on suppressions; depguard; errorlint, exhaustive, errcheck; markdownlint, gitleaks, trivy wired in | (a) Go complexity linters excluded from most service code paths (`services/*/service/`, `*/repository/`, `*/adapters/`, `*/internal/`, etc). (b) Frontend `frontend/eslint.config.js` has no `complexity`, `max-lines`, `max-lines-per-function`. `manifest-graph.tsx` regressed +1 ccn since last run (now 156); rule would have surfaced it |
| 4: Architecture Tests | write | Present | `tests/architecture/structure_test.go` enforces standard service layout (domain/, adapters/persistence/, service/), `server.go` presence, `doc.go` in every shared package, with ratchet maps; `scripts/verify-service-conventions.sh` enforces 800-line file cap, bans `time.Sleep` in tests, checks proto freshness; `shared/pkg/saga/linter.go` is a custom 712-line Starlark linter; runs in `.github/workflows/conventions.yml` | 800-line rule exempts test files - hence `coverage_unit_test.go` at 5,753 LOC and four other tests above 1,500 LOC |
| 5: CI Pipeline | write | Present | 30 GitHub workflows: build, sharded test, quality, codeql, security, proto, saga-validation, schema-validation, e2e, conventions, migrations, asyncapi, markdown, service-readme-lint; concurrency groups with cancel-in-progress; path-filtered triggers | Documented flakies on NFR benchmarks and some operational-gateway repository tests |
| 6: Coverage Gates | write | Present | `codecov.yml` project 75% / patch 70% targets, `informational: false` (blocks merge); per-component 80% across 48 components; flags for unittests/integration/frontend; `require_changes: true` | None material |
| 7: Code Review Bots | write | Present | CodeRabbit with `request_changes_workflow: true`, auto-approve, base branches develop+main; Claude review bot keyed to the 795-line A-graded instructions file; both active on recent PRs | None material |
| 8: AI Project Management | meta | Present | Task Master integration: 111 task files, 6 PRDs, recent merged PRs reference task IDs (`#2208 ... (063-saga-durability-parity.2)`); 1,975-line marathon retro log with validated-vs-pending tracking column for every template change; wave-based lead/teammate orchestration; per-tenant CI flake patterns documented | `.taskmaster/` directory lives in parent dir, not tracked in repo - only `templates/` is checked in. Same portability gap as before |

### Maturity Level

| Score | Level | Description |
|-------|-------|-------------|
| 0-2 | Not Ready | Agent will produce inconsistent, unvalidated code |
| 3-4 | Basic | Norms exist but aren't enforced. Agent works but drifts |
| 5-6 | Solid | Contracts catch most issues. Agent is productive |
| 7-8 | AI-Native | System self-improves. Agents work reliably at scale |

## Top 3 Actions

| # | Action | Layer | Effort | Command / First Step | Hotspot files this addresses | Issue |
|---|--------|-------|--------|---------------------|------------------------------|-------|
| 1 | Reconnect orphan docs and fix broken references so agents can actually traverse the doc set. 30% reachability from `README.md` caps any link-following agent at 30% coverage; 43 islands mean entire subsystems are invisible to traversal. Start with the entry point - extend `README.md` (or add `docs/README.md` as a proper MOC) to link the major doc trees, then triage the 15 broken links and 19 missing cross-references. | 0 | medium | Open `.assess/doc-graph.svg` in a browser to see island structure. Walk `docs/runbooks/`, `docs/architecture/`, `docs/prd/`, `docs/adr/`, `docs/guides/`, `docs/skills/` and ensure each has at least one inbound link from the entry MOC. Fix the 15 broken links by either creating the missing target or removing the dead reference. Address the 19 missing cross-references (filenames mentioned in prose without a link). | `README.md`, 62 orphan docs, 15 ghost-file targets, 19 missing-link pairs | `assess-2026-05-22.5` |
| 2 | Refresh the 5 stale ADR hubs - the highest-pagerank docs in the repo are 53-88 days stale beside a churning codebase. Each is a "lying map" routing traffic to outdated descriptions. Priority order matches `stale_hubs`: ADR-0004 (event schema, priority 87), 0005 (adapter pattern, top pagerank 0.066, priority 71), 0014 (financial instruments, 88d stale, priority 46), 0002 (microservices/BIAN, 29 incoming links), 0003 (DB migrations, 10 incoming). | 0 | medium-large | For each ADR: read the actual current code being described, update with what's true now, OR supersede with a new ADR if the design moved on. Where possible, add a "see also: code at <path>" link so the next stale check has a precise subject_method (`explicit-links`) instead of `repo-baseline`. | `docs/adr/0004-event-schema-evolution.md`, `docs/adr/0005-adapter-pattern-layer-translation.md`, `docs/adr/0014-financial-instrument-reference-data.md`, `docs/adr/0002-microservices-per-bian-domain.md`, `docs/adr/0003-database-schema-migrations.md` | `assess-2026-05-22.6` |
| 3 | Split the 5,753-line `current-account/service/coverage_unit_test.go` and add a `_test.go` size cap to `verify-service-conventions.sh` (e.g. 2,000 lines, same `//meridian:large-file` escape hatch as production code). Five of the seven top hotspots are tests because the 800-line rule explicitly excludes `_test.go`. | 4 | medium | Group cases by saga (`deposit`, `withdrawal`, `lien_active`, `lien_executed`, `lien_terminated`, `freeze`) and move each into its own `*_test.go` file. Then extend `scripts/verify-service-conventions.sh` with a `_test.go > 2000 lines` check alongside the existing 800-line rule. | `services/current-account/service/coverage_unit_test.go` (5,753), `services/control-plane/internal/validator/manifest_validator_test.go` (2,889), `services/payment-order/service/grpc_service_test.go` (2,283), `services/financial-accounting/service/financial_accounting_service_test.go` (1,943), `services/position-keeping/service/record_measurement_test.go` (1,695) | `assess-2026-05-22.1` |

### Why these three?

The v1.10 doc-graph promoted two new actions over what was in the prior Top 3. Action 1 is the navigability foundation - while individual docs are good, only 30% of the corpus is reachable from the entry point, so the agent can't follow links to find them. Action 2 is the highest-leverage staleness fix: the 5 top hub ADRs route the majority of internal traversal, so refreshing them propagates accuracy through the whole graph. Action 3 stays because the test-file size loophole is still the dominant complexity hotspot - five of the seven worst files are tests the 800-line rule exempts. Note this also feeds back into the navigability story: Action 1 will reveal whether `coverage_unit_test.go` is referenced anywhere in docs (it shouldn't be, but the broken-link scan would catch it).

The previously-tracked Top-3 items are demoted to Additional Opportunities. `assess-2026-05-22.4` ("Move CLAUDE.md into repo root") is effectively moot now that the v1.10 grader recognises `.github/claude-review-instructions.md` and grades it A - the breadcrumbs work, they just live in `.github/` rather than at the root. Leaving the task open per the rotation rule but recommending the user close it.

## Additional Opportunities

- **Frontend ESLint complexity rules** (Layer 3) - `manifest-graph.tsx` regressed +1 ccn this run (155 → 156), still no rule fences it. Tracked as `assess-2026-05-22.2`.
- **Un-exclude `services/*/service/*.go` in `.golangci.yml`** with a ratchet (Layer 3). Tracked as `assess-2026-05-22.3`.
- **Move CLAUDE.md and `.taskmaster/` into repo root** - tracked as `assess-2026-05-22.4`. Less urgent now that v1.10 grades the in-repo substitute as A; keep open for portability but consider closing if the team is happy with the current location.
- **Install a static dead-code tool** (`staticcheck`, `deadcode`, or `knip`/`ts-prune`) so Layer 1 can run the intra-repo scan instead of degrading. None are on PATH right now.
- **Run a contradictions sweep over `docs/`** with an LLM - the v1.10 skill explicitly notes that contradictions between pages are out of deterministic scope. Now that 254 docs are tracked, an LLM pass would catch claims that conflict with each other or with the code.
- **Add per-module base READMEs** to lift the 8.8% module-doc coverage. The skill-card format already used by service READMEs is a good template.
- Audit the 16 production Go files in the 600-800 LOC range before they hit the cap.
- Re-enable `unparam` once the upstream golangci-lint generics panic is fixed.

## Strengths

- **`.github/claude-review-instructions.md` is grade-A** (score 100, 31 positive directives, 8 tradeoff phrases, 30 path references). That's the kind of instruction file the v1.10 grader is designed to reward.
- **Runtime liveness at rung 3** - 13 runbooks contain executable queries (`logcli`, `promtool`, `kubectl` commands). An agent debugging an incident can reach runtime state via documented invokable paths, not just see that telemetry exists.
- **Schema-driven everything**: `handlers.yaml` → auto-generated Starlark clients → AI-constrained saga DSL. The custom 712-line Starlark linter in `shared/pkg/saga/linter.go` keeps it honest.
- **Marathon retro log is rare** - 1,975 lines of "this template change was validated / pending / removed" with a status column. Process feedback loop closing.
- **Coverage gates are component-scoped at 80% across 48 components**, not just a project-wide average.
- **Architecture tests use a ratchet pattern with explicit allowlists** (`servicesWithoutServerGo`, `knownMissingDocGo`) - exactly how you migrate a system without freezing it.

**Wiki:** see `.assess/index.md` for the full hotspot catalog across all runs, `.assess/log.md` for run history, and `.assess/hotspots/<file>.md` for per-file briefings.

---

_Report generated by [`/ai-native-toolkit:assess`](https://github.com/bjcoombs/ai-native-toolkit). Install in any Claude Code session: `/plugin marketplace add https://github.com/bjcoombs/ai-native-toolkit` then `/plugin install ai-native-toolkit@ai-native-toolkit`._
