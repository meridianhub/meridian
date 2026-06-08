# AI-Readiness Closure (assess 2026-06-04)

<!-- markdownlint-disable MD013 -->

## Problem Statement

The `/assess` run on 2026-06-04 (`.assess/assess-report.md`) scored Meridian **7.5 / 8 - AI-Native (Exemplary)**. Eight of nine layers are Present. The single half-point gap is **Layer 0 (Agent Instructions & Navigability)**, scored Partial: an agent's first hop into a subtree can land on a stale or unlinked map.

The deterministic cross-layer scan surfaced three classes of concrete defect:

- **Lying maps** - 10 README files describing service/module code that has churned underneath a comparatively static doc. A drifted README is the worst signal for an agent: it navigates fast to a stale conclusion.
- **Orphan docs** - 26 module READMEs and platform docs reachable only by guessing a path; nothing links to them (reachability 88.7%, 20 disconnected islands).
- **Unlinked prose cross-references** - 11 places where a doc names another doc without linking it.

Three mid-tier architecture docs are also genuinely stale against their subject code. Separately, two Additional Opportunities harden the financial-correctness core beyond the score: mutation testing (Layer 6 truth-pressure is currently unverified) and an LLM contradiction sweep over `docs/`.

## Technical Context

- Source tree: `meridian-main/` (Go monorepo, module `github.com/meridianhub/meridian`).
- Machine-readable inputs: `.assess/run-context.json` (orphan list, `missing_xrefs`, `stale_hubs`), `.assess/assess-report.md` (Top 3 Actions, cross-layer findings).
- The prior `assess-2026-05-22` tag took reachability from 30% to 88.7%; this PRD closes the residual.
- The assess gate (`.github/workflows/assess-gate.yml`) is already frozen into CI - reducing the orphan rate and refreshing the lying maps directly improves the gated snapshot.
- Doc-graph wiring touches shared index files. To eliminate merge conflicts across parallel work, **one task owns all root-level entry/index edits**; subtree tasks own only files inside their own subtree.

## Solution

Reconcile every flagged README against its current code (refresh, not rewrite - characterize current behaviour rather than regenerate from intent), wire every orphan back into the doc graph, add the missing cross-references, refresh the three stale architecture docs, then harden Layer 6 with mutation testing and a contradiction sweep. Each README reconciliation must diff doc claims against the actual code and either refresh drifted sections or delete genuinely-superseded ones - default to refresh; these READMEs describe live subtrees.

## Scope

Ten independent work items. Hot-file ownership noted to keep them parallel and conflict-free.

1. **Frontend wayfinding.** Reconcile `frontend/README.md` against `frontend/src/`; refresh drifted sections. Within the frontend subtree, link `frontend/docs/tenant-subdomain-routing.md` from `frontend/README.md`. (Owns: `frontend/README.md` and files under `frontend/`. Does NOT edit root index docs.)

2. **Scripts wayfinding.** Reconcile `scripts/README.md` against the current `scripts/` directory (verify it still describes `demo.sh`, `doctor.sh`, and the other scripts that exist); refresh or delete superseded sections. Link `scripts/kafka-tests/README.md` from `scripts/README.md`. (Owns: files under `scripts/`.)

3. **Shared subtree wayfinding.** Reconcile `shared/README.md` against `shared/` code. Add inbound links from `shared/README.md` to its orphan children: `shared/domain/money/PRECISION_GUARANTEES.md`, `shared/platform/bootstrap/README.md`, `shared/platform/observability/README.md`. (Owns: files under `shared/`.)

4. **Core ledger service READMEs.** Reconcile each lying-map README against its service code, refreshing drifted sections: `services/control-plane/README.md`, `services/current-account/README.md`, `services/financial-accounting/README.md`, `services/internal-account/README.md`, `services/position-keeping/README.md`. (Owns: those five `services/*/README.md` files only.)

5. **Identity and MCP-server wayfinding.** Reconcile `services/identity/README.md` and `services/mcp-server/README.md` against their code. Add inbound links from `services/mcp-server/README.md` to its orphan reference docs: `services/mcp-server/internal/resources/docs/cel-reference.md` and `services/mcp-server/internal/resources/docs/starlark-guide.md`. (Owns: files under `services/identity/` and `services/mcp-server/`.)

6. **Central orphan wiring and missing cross-references (hot-file owner).** Sole editor of root-level entry/index docs (`README.md`, any top-level docs index). Also commit this PRD into the repo at `docs/prd/064-assess-2026-06-04-ai-readiness-closure.md`. Wire the remaining orphan docs into the graph with inbound links: `CHANGELOG.md`, `CODE_OF_CONDUCT.md`, `.githooks/README.md`, `.github/claude-review-instructions.md`, `deploy/demo/README.md`, `deploy/demo/cloudflare-setup-checklist.md`, `deploy/demo/dex-identity-migration-plan.md`, `deploy/demo/pg-migration-compatibility-report.md`, `deployments/k8s/policies/tests/README.md`, `services/event-router/k8s/README.md`, `services/internal-account/benchmarks/README.md`, `services/internal-account/examples/README.md`, `services/market-information/README.md`, `services/payment-order/service/HANDLER_ARCHITECTURE.md`, `tests/audit-e2e/README.md`, `cookbook/docs/authoring-patterns.md`, `cookbook/docs/authoring-components.md`. Add inbound links from `README.md`/docs index to the subtree roots rescued by items 1-5 (`frontend/README.md`, `scripts/README.md`, `shared/README.md`). Add the 11 missing prose cross-references (`missing_xrefs`): `cookbook/README.md` -> authoring-patterns + authoring-components; `docs/adr/0015-standard-service-directory-structure.md` -> circuit-breaker-usage, incident-response, service-coupling-analysis, COVERAGE_ANALYSIS; `docs/prd/049-codebase-consistency.md` -> new-bian-service-checklist, error-conventions, repository-conventions, value-types; `docs/prd/README.md` -> archive/panic-audit-inventory. Target reachability >= 97% and island count <= 3.

7. **Refresh stale architecture docs.** Diff against current code and refresh: `docs/architecture/service-coupling-analysis.md` (153 days stale), `docs/architecture/event-driven-architecture.md` (95 days), `docs/architecture/bian-service-boundaries.md` (95 days). (Owns: those three files.)

8. **Mutation testing on the financial-correctness core.** Add a mutation-testing harness (`go-mutesting` or a maintained Go-ecosystem equivalent) targeting `services/position-keeping` and the saga engine, where a surviving mutant means money moves wrong. Triage survivors, add tests to kill the high-value ones, and wire an opt-in CI job (non-blocking initially) plus a short runbook on running it. Converts Layer 6 from "covered" to "behaviour-pinned" for the core.

9. **Confirm Go dead-code is clean.** Run `staticcheck -checks U1000 ./...` across the tree. Remove confirmed unreachable code; document any false positives (e.g. reflection/external consumers). Records the result so the Layer 1 caveat is closed.

10. **LLM contradiction sweep over `docs/`.** Run an LLM pass over `docs/` to flag pages that contradict each other or the code (out of `/assess` deterministic scope). Fix clear contradictions inline; list anything ambiguous in the PR description for human judgement.

## Out of Scope (manual follow-up, not agent tasks)

- **Independent human review of `frontend/src/App.tsx`** (Top 3 Action #2) and **`scripts/doctor.sh`** - both flagged `self_referential_tests` (code and tests entered together). An agent cannot self-certify that its own tests assert observable behaviour rather than the author's mental model. These require a human reviewer and are tracked separately for Ben to action.

## Success Criteria

- All 10 lying-map READMEs reconciled against current code (refreshed or, where genuinely superseded, deleted).
- Doc-graph reachability >= 97% (from 88.7%); island count <= 3 (from 20); all 26 orphans linked; all 11 missing cross-references added; 0 broken/dangling links.
- The 3 stale architecture docs refreshed against current code.
- Mutation-testing harness runs against `services/position-keeping` + saga engine; high-value survivors killed; opt-in CI job + runbook landed.
- `staticcheck -checks U1000 ./...` result recorded; confirmed dead code removed.
- Contradiction sweep completed; clear contradictions fixed, ambiguous ones surfaced.
- A re-run of `/assess` would score Layer 0 Present (8 / 8).

## Complexity Estimate

10 tasks. Items 1-7, 9 are documentation/reconciliation (1-3 points each, mostly markdown - 1-approval PRs, high parallelism). Item 8 (mutation testing) is the one heavy task (5-8 points: new tooling, survivor triage, CI integration). Item 10 (contradiction sweep) is 3 points. No inter-task dependencies - all 10 are first-wave parallel; item 6 is the single hot-file owner so the others never touch root index docs.
