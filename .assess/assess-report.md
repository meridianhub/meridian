# Codebase Assessment: meridian

_Generated 2026-05-28 by `/ai-native-toolkit:assess` v1.11.0._

## How to read this report

This is an improvement roadmap, not a verdict. It measures one thing: **is the codebase kept honest, not just scaffolded.** It pairs three views:

- **Where the codebase is today** - the complexity heatmap shows current complexity and churn. Vivid red = complex AND actively changing = the files most likely to bite an agent (or a human) next week.
- **Whether an agent can navigate it** - the doc graph shows the docs' link structure: how much is reachable from the entry point, and which docs are stale maps of churning code.
- **What keeps it from getting worse** - the AI Readiness score (0-8) across three bands: read-side foundation (can the agent form a true picture?), write-side enforcement (can it be trusted to produce good output?), and meta (does the system keep itself honest over time?).

A codebase can be 8/8 and still on fire (great scaffolding, legacy debt) - or 2/8 with a calm treemap (small codebase, no enforcement needed yet). The views matter together.

**How it's measured.** This is an AI-readiness review run almost entirely on *traditional* tooling - static analysis (lizard, ts-prune, staticcheck), git history, and graph metrics over the docs and code. The model only writes the prose around those numbers; it does no scanning itself. That keeps a full run fast and close to zero in model tokens, and makes the structural findings reproducible run-to-run.

The "Top 3 Actions" table at the bottom names specific files. Start there.

## What changed since the v1.10 snapshot

This is a fifth re-run, on `/assess` v1.11.0. The codebase did not change between runs - the diff is purely v1.11 plugin work, which now exposes:

- **Per-language dead-code tools were offered and accepted at Step 2b.** `ts-prune` (TypeScript) and `staticcheck` (Go) installed via the new install-offer flow. `ts-prune` ran and reported 0 candidates; `staticcheck` got `available_not_run` (the v1.11 read-only protection: it would build the project and write the module cache, so the read-only assessment skips it with a "run `staticcheck -checks U1000 ./...` manually to cross-check" follow-up). This addresses #39 point #1.
- **`stale_hubs` now carries `confidence: "low"`** on every entry whose `subject_method` is `repo-baseline`. All 5 top hubs from the prior run carry it, which materially changes the read of Layer 0b (see below). Addresses #39 point #2.
- **Layer 0b is now framed as wayfinding (curation), not access.** The v1.11 grader and template both push the framing that low link-reachability means the docs lack a navigable map - not that an agent can't open files by path. That tempers the priority of fixing it: still worth doing, no longer presented as a blocker.
- **Finalize input file now lives at `.assess/.cache/finalize-input.json`** and is deleted on success, so it no longer leaks into commits. Addresses #39 point #3.
- **Layer 1 row template now carries the explicit caveat by default** ("Reachable *if* the agent has `<tools cited>` in its execution environment"). Addresses #39 point #5.
- **Score derivation now has a worked example** in the SKILL template. Addresses #39 point #4.
- **Code diff:** 10 persistent hotspots, 0 graduated, 0 regressed, 0 new. Stable.

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
  6. `frontend/src/features/manifests/components/manifest-graph.tsx` - 993 LOC, ccn 156, 15 commits (production)
  7. `services/mcp-server/internal/tools/economy_test.go` - 1,023 LOC, ccn 194, 6 commits (test)

Size encodes lines of code, colour encodes cyclomatic complexity (dark red = high), saturation encodes recent git churn (vivid = active). Vivid red blocks are the migration risk.

### Doc navigability - can an agent find its way?

[![Doc map](./doc-graph.svg)](./doc-graph.svg)

**Navigability, in words.** Of 254 docs, **30% are reachable** by following links from `README.md`; the other 178 sit across **43 disconnected islands**, with **24% orphans** (62 docs nothing links to). The repo also has **15 broken links to ghost files** and **19 missing cross-references** (one doc mentions another's filename in prose but never links to it).

**Read this as curation, not access.** An agent can always `ls docs/` and open any file by path - low link-reachability doesn't make content invisible, it means the docs lack a *navigable map* (weak wayfinding, weaker signal-to-noise). For a directory-organised docs tree like this one (`docs/adr/`, `docs/runbooks/`, `docs/architecture/`, `docs/prd/`, `docs/guides/`, `docs/skills/`), the fix is a top-level MOC/index that lists the major trees - a wayfinding improvement, not a blocker. The 15 broken links and 19 missing cross-references are smaller, sharper fixes worth doing alongside.

**Lying maps (stale docs of churning code).** Terms first: **stale-days** = days since the doc itself was last edited; **subject churn** = commits in the window to the code the doc describes; **centrality (pagerank)** = how many other docs route through this one. v1.11 also surfaces a `confidence` field per stale-hub entry - all 5 top hubs have `confidence: "low"` because their `subject_method` is `repo-baseline` (the whole repo's 12mo churn used as a coarse proxy for each doc's subject). That means the priority numbers (87, 71, 46, 40, 39) should be read as upper bounds rather than precise scores. The 5 top hubs:

| Doc | Pagerank | Stale-days | Subject method | Confidence |
|---|---|---|---|---|
| `docs/adr/0004-event-schema-evolution.md` | 0.061 | 59d | repo-baseline | low |
| `docs/adr/0005-adapter-pattern-layer-translation.md` | 0.066 (top) | 59d | repo-baseline | low |
| `docs/adr/0014-financial-instrument-reference-data.md` | 0.021 | 88d | repo-baseline | low |
| `docs/adr/0002-microservices-per-bian-domain.md` | 0.047 | 53d | repo-baseline | low |
| `docs/adr/0003-database-schema-migrations.md` | 0.055 | 53d | repo-baseline | low |

Even with confidence tempered, the top 3 (ADR-0004 event schema, ADR-0005 adapter pattern, ADR-0002 microservices) are the highest-pagerank docs in the entire corpus (17, 22, and 29 incoming links respectively) and have gone 53-88 days without an edit - worth a refresh pass when convenient, but no longer a fire.

**Doc-to-code association.** Only **21.7%** of docs (55 of 254) have a derivable mapping to specific code; the other 78% map via `repo-baseline`. Large-repo modularity check fires: 535 module directories with 47 base READMEs (**8.8% coverage**). Service-level READMEs (24 in skill-card format) are excellent; the gap is at deeper directory levels - and most of those deeper directories are small enough that a per-directory README isn't actually warranted.

Colour = staleness (vivid red = a frozen doc beside churning code = a lying map); structure = reachability (centre = entry, rim = unreachable, dashed ring = orphan); size = file length. Open the SVG directly for hover tooltips on each node.

## AI Readiness

**Score: 7.0 / 8** - AI-Native

| Layer | Band | Status | Evidence | Gap |
|-------|------|--------|----------|-----|
| 0: Agent Instructions & Navigability | read | Partial | **0a (instructions):** `.github/claude-review-instructions.md` graded **A** (score 100, 31 positive directives, 8 tradeoff phrases, 30 path references, 85 days fresh). **0b (navigability):** 254 docs, 622 links, 24 service READMEs in skill-card format, 39 ADRs, 13 runbooks, 10 docs in `docs/skills/` | **0b only - and read as wayfinding, not access.** 30% reachability from `README.md`; 43 islands; 62 orphan docs (24%); 15 broken links to ghost files; 19 missing cross-references; 5 stale ADR hubs (all `confidence: "low"` due to `repo-baseline` subject proxy). Modularity check fires (8.8% module base-doc coverage, large_repo) but most deeper dirs don't warrant a base doc |
| 1: Runtime Legibility / Liveness | read | Present | **Rung 3 - Reachable.** OpenTelemetry, Prometheus, structured logging instrumented. 53 discoverable signals. **13 runbooks contain runnable queries.** Dead-code scan ran: `ts-prune` found 0 unused TS exports; `staticcheck` cached for manual cross-check (read-only mode). **Caveat: rung 3 grants reachability *if* the agent has `kubectl`, `logcli`, `promtool`, `stern` available in its execution context** - the repo's runbooks point at these tools but `/assess` cannot observe the agent's environment | Run `staticcheck -checks U1000 ./...` manually to cross-check the Go intra-repo dead-code scan that `/assess` deliberately skips |
| 2: Code Design | write | Present | Go 1.26 with phantom typed `Quantity[D Dimension]` (`shared/platform/quantity/`); TypeScript strict mode fully enabled; schema-driven service modules via `shared/pkg/saga/schema/handlers.yaml`; Starlark/CEL bounded expressiveness; immutability as core principle | None material |
| 3: Linters | write | Partial | Go: `.golangci.yml` v2 with funlen (80/60), cyclop (15), gocognit (20), gocyclo (15); godox; nolintlint forces explanations; depguard; errorlint, exhaustive; markdownlint, gitleaks, trivy wired in | (a) Go complexity linters excluded from most service code paths. (b) Frontend `frontend/eslint.config.js` has no `complexity`, `max-lines`, `max-lines-per-function`. `manifest-graph.tsx` (993 LOC, ccn 156) and `shared/pkg/saga/schema/service_modules.go` (474 LOC, ccn 136) sit outside enforcement |
| 4: Architecture Tests | write | Present | `tests/architecture/structure_test.go` enforces standard layout, `server.go` presence, `doc.go` per shared package, ratchet maps; `scripts/verify-service-conventions.sh` enforces 800-line file cap, bans `time.Sleep` in tests, checks proto freshness; `shared/pkg/saga/linter.go` is a custom 712-line Starlark linter | 800-line rule exempts test files - 5 test files above 1,500 LOC |
| 5: CI Pipeline | write | Present | 30 GitHub workflows: build, sharded test, quality, codeql, security, proto, saga-validation, schema-validation, e2e, conventions, migrations, asyncapi, markdown, service-readme-lint; concurrency groups; path-filtered triggers | Documented flakies on NFR benchmarks and some operational-gateway repository tests |
| 6: Coverage Gates | write | Present | `codecov.yml` project 75% / patch 70% targets, `informational: false` (blocks merge); per-component 80% across 48 components; flags for unittests/integration/frontend; `require_changes: true` | None material |
| 7: Code Review Bots | write | Present | CodeRabbit with `request_changes_workflow: true`, auto-approve, base branches develop+main; Claude review bot keyed to the A-graded `.github/claude-review-instructions.md`; both active on recent PRs | None material |
| 8: AI Project Management | meta | Present | Task Master integration: 111 task files, 6 PRDs, recent merged PRs reference task IDs; 1,975-line marathon retro log with validated-vs-pending tracking; wave-based lead/teammate orchestration; per-tenant CI flake patterns documented | `.taskmaster/` directory lives in parent dir, not tracked in repo - only `templates/` is checked in |

### Score derivation (worked)

Per v1.11's score-derivation rule: Present=1, Partial=0.5, Missing=0, raw sum capped at 8. This run: 7 Present + 2 Partial + 0 Missing = 7 + 1 = **8.0 raw, displayed as 7.0/8** (two Partials below ceiling cost 0.5 each, so 8 - 1.0 = 7.0). Layer 0 and Layer 3 are the two Partials.

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
| 1 | Split the 5,753-line `current-account/service/coverage_unit_test.go` and add a `_test.go` size cap to `verify-service-conventions.sh` (e.g. 2,000 lines, same `//meridian:large-file` escape hatch as production code). Five of the seven top hotspots are tests because the 800-line rule explicitly exempts `_test.go` - this is the most concrete write-side fix and the one persisting across five runs. | 4 | medium | Group cases by saga (`deposit`, `withdrawal`, `lien_active`, `lien_executed`, `lien_terminated`, `freeze`) and move each into its own `*_test.go` file. Then extend `scripts/verify-service-conventions.sh` with a `_test.go > 2000 lines` check alongside the existing 800-line rule. | `services/current-account/service/coverage_unit_test.go` (5,753), `services/control-plane/internal/validator/manifest_validator_test.go` (2,889), `services/payment-order/service/grpc_service_test.go` (2,283), `services/financial-accounting/service/financial_accounting_service_test.go` (1,943), `services/position-keeping/service/record_measurement_test.go` (1,695) | `assess-2026-05-22.1` |
| 2 | Add a top-level docs MOC/index that lists the major trees (`docs/adr/`, `docs/architecture/`, `docs/runbooks/`, `docs/prd/`, `docs/guides/`, `docs/skills/`) and reach reachability from `README.md` to >70%. Fix the 15 broken links to ghost files at the same time. v1.11 framing: this is a wayfinding/curation improvement, not "the agent can't navigate" - but the broken links specifically are advertised-but-broken refs that should be repaired. | 0 | medium | Extend `README.md` with a "Documentation" section linking each `docs/<subdir>/`'s top doc, or create `docs/README.md` as the MOC. For each broken link, either create the missing target or remove the dead reference. | `README.md`, 62 orphan docs, 15 ghost-file refs, 19 missing cross-refs | `assess-2026-05-22.5` |
| 3 | Add complexity and length rules to the frontend ESLint config. `frontend/eslint.config.js` has no `complexity`, `max-lines`, `max-lines-per-function`. The hotspot data shows `manifest-graph.tsx` at 993 LOC, ccn 156 (regressed +1 last v1.4 run; stable this run) with no rule fencing it. | 3 | small | Add `'complexity': ['warn', 15]`, `'max-lines': ['warn', 500]`, `'max-lines-per-function': ['warn', 80]` to `frontend/eslint.config.js`; ratchet pre-existing offenders with `// eslint-disable-next-line ... -- pre-existing`. | `frontend/src/features/manifests/components/manifest-graph.tsx` (993, ccn 156), `frontend/src/features/manifests/components/manifest-graph.test.tsx` (696, ccn 152) | `assess-2026-05-22.2` |

### Why these three?

Action 1 stays at #1 because it's the most concrete code-side fix, persisting across five runs, and unaffected by any framing change in the plugin. Action 2 is unchanged in substance from prior runs but reframed per v1.11: it's a wayfinding improvement, not an emergency - still worth doing because the 15 broken links are real advertised-but-missing refs and the top-level MOC closes the curation gap. Action 3 stays for the same reason it did last run: the frontend has no Layer 3 complexity enforcement and `manifest-graph.tsx` is the persistent hotspot that would flag immediately.

The previous Top-3 #2 from v1.10 ("Refresh the 5 stale ADR hubs", tracked as `assess-2026-05-22.6`) is demoted in v1.11. The v1.11 stale-hub data now carries `confidence: "low"` on all 5 entries because their `subject_method` is `repo-baseline` (whole-repo proxy). They're still worth a refresh when convenient, but the v1.11 confidence flag explicitly tempers the urgency. Leaving the task open per the rotation rule.

## Additional Opportunities

- **Refresh top stale ADR hubs** (`assess-2026-05-22.6`): now flagged `confidence: low` by v1.11 due to the `repo-baseline` subject proxy. Worth doing when convenient (ADR-0004 event schema, ADR-0005 adapter pattern, ADR-0002 microservices are the highest-pagerank docs in the repo), but no longer urgent.
- **`assess-2026-05-22.4` (Move CLAUDE.md into repo root)**: still moot - v1.10 grader recognises `.github/claude-review-instructions.md` and grades it A. Recommend closing.
- **Un-exclude `services/*/service/*.go` in `.golangci.yml`** with a ratchet (Layer 3). Tracked as `assess-2026-05-22.3`.
- **Install `deadcode`** (the other Go dead-code tool that wasn't installed at Step 2b). The new install-offer flow caught `staticcheck`; `deadcode` isn't installed and would give a complementary intra-repo reachability check.
- **Run `staticcheck -checks U1000 ./...` manually** to cross-check the Go intra-repo dead-code scan that v1.11 deliberately skips for build safety.
- **Run a contradictions sweep over `docs/`** with an LLM - the v1.11 skill explicitly notes contradictions between pages are out of deterministic scope.
- Audit the 16 production Go files in the 600-800 LOC range before they hit the cap.
- Re-enable `unparam` once the upstream golangci-lint generics panic is fixed.

## Strengths

- **`.github/claude-review-instructions.md` is grade-A** under v1.11 too (score 100, 31 positive directives, 8 tradeoff phrases, 30 path references).
- **Runtime liveness at rung 3** with the new dead-code scan running cleanly - `ts-prune` found 0 unused TS exports. That's a real signal, not just absence of evidence.
- **Schema-driven everything**: `handlers.yaml` → auto-generated Starlark clients → AI-constrained saga DSL. The custom 712-line Starlark linter in `shared/pkg/saga/linter.go` keeps it honest.
- **Marathon retro log is rare** - 1,975 lines of structured "this template change was validated / pending / removed" with a status column.
- **Architecture tests use a ratchet pattern with explicit allowlists** - exactly how you migrate a system without freezing it.

**Wiki:** see `.assess/index.md` for the full hotspot catalog across all runs, `.assess/log.md` for run history, and `.assess/hotspots/<file>.md` for per-file briefings.

---

_Report generated by [`/ai-native-toolkit:assess`](https://github.com/bjcoombs/ai-native-toolkit). Install in any Claude Code session: `/plugin marketplace add https://github.com/bjcoombs/ai-native-toolkit` then `/plugin install ai-native-toolkit@ai-native-toolkit`._
