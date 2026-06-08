---
name: mutation-testing-runbook
description: How to run, interpret, and act on mutation testing against Meridian's financial-correctness core
triggers:
  - The weekly mutation-testing workflow surfaced new surviving mutants
  - A reviewer asks whether the tests actually constrain balance/saga/money logic
  - Mutation score on a targeted package dropped after a change
  - Onboarding: understanding what mutation testing covers and why
instructions: |
  Mutation testing measures whether the test suite actually catches behavioural changes,
  not just whether lines execute. A surviving mutant in the financial core means money or
  quantities can move incorrectly without any test failing. Run with gremlins; a high
  timeout-coefficient is required (the unit suites are fast, but each mutant needs time to
  compile). Triage survivors by impact (balance/amount/status = High); write killer tests
  for High-impact survivors and document Medium/Low ones. The CI workflow is opt-in
  (workflow_dispatch + weekly schedule) and non-blocking.
---

# Mutation Testing Runbook

Mutation testing for Meridian's **financial-correctness core**: the packages where a bug
silently moves money, energy, carbon, or compute quantities the wrong way.

## What mutation testing is (and why)

Line/branch coverage tells you a line *ran* during tests. It does **not** tell you a test
would *fail* if that line were wrong. Mutation testing closes that gap: it makes small,
semantics-changing edits ("mutants") to the source - flip `>` to `>=`, `++` to `--`,
`!= nil` to `== nil` - then re-runs the tests. A mutant that tests still pass against is
"LIVED" (survived): a behaviour no test constrains. A mutant the tests catch is "KILLED".

For a ledger this is the difference between "we have 90% coverage" and "a reviewer can
trust that a wrong-direction balance computation would break a test."

## Tool

We use [gremlins](https://github.com/go-gremlins/gremlins) (`go-gremlins/gremlins`),
pinned to `v0.6.0`. It is the most actively maintained Go mutation tester and runs against
the repo's Go toolchain (1.26.x). Configuration lives in `.gremlins.yaml` at the repo root.

```bash
go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0
```

### Critical: the timeout coefficient

gremlins derives each mutant's test timeout from the baseline coverage-run duration times a
coefficient. The financial-core unit suites are fast (sub-second coverage runs), so the
default coefficient leaves no headroom for the Go compile+link step that precedes each
mutant's test run - **every mutant reports `TIMED OUT` instead of `KILLED`/`LIVED`**.
`.gremlins.yaml` sets `timeout-coefficient: 20` to fix this. If you still see spurious
`TIMED OUT` results on a slower machine, raise it further (`--timeout-coefficient 30`).

## Targeted packages

These are the packages in scope - the ones where a survivor means value moves wrong:

| Package | Path | Why it matters |
|---------|------|----------------|
| Position-keeping domain | `services/position-keeping/domain/` | Balance computation, status transitions, double-entry direction |
| Position-keeping service | `services/position-keeping/service/` | Balance retrieval, currency resolution, validation |
| Saga orchestration | `shared/pkg/saga/` | Distributed-transaction state machine, Starlark Decimal arithmetic, compensation |
| Money | `shared/pkg/money/` | Instrument-aware monetary arithmetic |
| Quantity (dimensional) | `shared/platform/quantity/` | `Qty[D]` multi-asset arithmetic and comparison |

## Running locally

```bash
# A single package (fast - start here when iterating on one area)
gremlins unleash ./services/position-keeping/domain/

# Show only survivors and kills as they stream (l=lived, v=...); omit -S for all statuses
gremlins unleash -S lv ./shared/pkg/saga/

# Money package (smallest, ~5s)
gremlins unleash ./shared/pkg/money/

# Dry run - list mutants without running tests (fast sanity check / mutant count)
gremlins unleash --dry-run ./shared/platform/quantity/
```

Integration tests are gated behind the `integration` build tag and need CockroachDB
testcontainers. Mutation testing runs the **fast unit suites only** - do not pass
`--tags integration`.

Reading the summary line:

```text
Killed: 122, Lived: 23, Not covered: 0
Timed out: 7, Not viable: 0, Skipped: 0
Test efficacy: 84.14%
Mutator coverage: 100.00%
```

- **Test efficacy** = `KILLED / (KILLED + LIVED)` - the headline number. Higher is better.
- **Mutator coverage** = `(KILLED + LIVED) / total` - how much of the mutated code the tests
  even exercise. Low coverage means dead/untested code, a different problem.
- **Not covered** - mutants on lines no test touches. These are coverage gaps, not efficacy
  gaps; address with any test that exercises the line.
- **Timed out** - usually means the timeout coefficient is too low (see above), not a real
  signal. Re-run with a higher coefficient before triaging these as survivors.

## Triaging survivors

Classify each `LIVED` mutant by blast radius:

| Impact | What | Action |
|--------|------|--------|
| **High** | Balance computation, amount/quantity arithmetic, posting direction (DEBIT/CREDIT), status-machine transitions, optimistic-concurrency version | Write a killer test now |
| **Medium** | Input validation, error classification, retry/backoff boundaries, currency resolution | Write a killer test if cheap; otherwise log as follow-up |
| **Low** | Logging, metrics, hash functions, test fixtures, defensive-copy guards | Document and move on |

Watch for two non-actionable categories:

- **Equivalent mutants** - a mutation that does not change observable behaviour, so *no*
  test can kill it. Example: `if len(s) > 0` vs `>= 0` guarding `append([]T(nil), s...)` -
  appending an empty slice to nil yields nil either way. Document and skip.
- **Test-only code** - mutants in `*_test.go` helpers or `testfixtures/`. Out of scope.

## Writing killer tests (TDD)

A killer test must **fail against the mutant and pass against correct code**. Verify both
directions:

1. Write the assertion that pins the behaviour the mutant breaks.
2. Confirm it passes on the real code: `go test ./<pkg>/ -run <TestName>`.
3. Temporarily inject the mutation (edit the source), confirm the test now **fails**.
4. Revert the source. The test stays.

Conventions: pure unit tests for arithmetic/domain logic (no DB). For integration paths use
CockroachDB testcontainers (`shared/platform/testdb.SetupCockroachDB`) and the `await`
package - never `time.Sleep`. For unexported functions, expose them via the package's
`export_test.go` (`var XForTesting = x`), matching the existing pattern.

## Baseline mutation scores

Captured on this branch (Go 1.26.4, `timeout-coefficient: 20`, unit suites only). "After"
reflects the killer tests added in this change; packages already at full efficacy are
unchanged.

| Package | Baseline efficacy | After | High-impact survivors killed |
|---------|------------------:|------:|------------------------------|
| `shared/pkg/money` | 100.00% | 100.00% | none (already complete) |
| `shared/pkg/types` | 100.00% | 100.00% | none (already complete) |
| `shared/platform/quantity` | 95.24% | 100.00% | `Qty.LessThan` / `GreaterThan` equal-value boundary |
| `services/position-keeping/domain` | 84.14% | 97.37% | `Version++` + audit-entry guards across all 6 state transitions |
| `services/position-keeping/service` | 92.08% | 92.62% | `resolveOpeningBalance` currency-resolution boundary/negation |
| `shared/pkg/saga` | see note | see note | Starlark `Decimal` `<` / `>` equal-value boundary |

> **Saga sweep note.** The `shared/pkg/saga` unit suite takes ~30s to run, so with
> `timeout-coefficient: 20` each mutant gets a ~10-minute timeout and the full-package sweep
> (several hundred covered mutants) does not complete in a practical inline run - this is the
> case the weekly CI job exists for. A partial baseline sweep surfaced ~180 survivors
> concentrated in validation/reporting/schema code (`validation/report.go`, `validator.go`,
> `runtime.go`, `schema/validation_modules.go`) - Medium impact, not direct value movement.
> The High-impact arithmetic survivor (Starlark `Decimal` comparison boundary, `decimal.go:112,116`)
> was killed and verified by injection in this change. Run the full saga sweep via the weekly
> CI job (or `gremlins unleash -S lv ./shared/pkg/saga/` with patience) to capture its score.

### Documented remaining survivors (Medium/Low, future work)

- **`services/position-keeping/domain/transaction_lineage.go:46,52`** - `len(ids) > 0`
  boundary guards on defensive slice copies. **Equivalent mutants**: `append([]uuid.UUID(nil), empty...)`
  is nil regardless, so no test can distinguish. Skip.
- **`services/position-keeping/domain/testfixtures/fixtures.go`** - test-only helper. Skip.
- **`shared/pkg/saga/validation/report.go`, `validator.go`, `runtime.go`,
  `schema/validation_modules.go`** - the bulk of the saga survivors (~180 observed) live in
  validation, reporting, and schema-derivation code. **Medium impact**: these affect
  manifest-validation messages and schema handling, not direct value movement. Worthwhile
  follow-up sweep, but lower priority than balance/amount/arithmetic survivors.
- **`shared/pkg/saga/backoff.go`** - retry backoff boundary conditions (Medium). Killing
  these requires asserting exact backoff durations at interval edges; worthwhile follow-up.
- **`shared/pkg/saga/decimal.go:89`** - `ARITHMETIC_BASE` on the string-hash accumulator
  (`h*31 + c`). **Low**: the hash only needs to be self-consistent within a run; correctness
  does not depend on the exact constant. Skip.
- **`shared/pkg/saga/claiming.go`, `circular_detector.go`, `handlers.go`** - Medium-impact
  saga-orchestration survivors; triage in a dedicated follow-up sweep.

The full survivor list for any package is reproducible with
`gremlins unleash -S lv ./<path>/` and is published as a CI artifact (see below).

## CI workflow

`.github/workflows/mutation-testing.yml` runs the sweep. It is **opt-in and non-blocking**:

- **Trigger**: `workflow_dispatch` (manual, with a `target` package selector) and a weekly
  `schedule` (Sunday 02:00 UTC).
- **Not a required check** - it never gates a merge. It exists to surface test-suite erosion
  over time, not to block PRs.
- Each target package runs as a matrix job. Results (`.json` + `.log`) are uploaded as
  `mutation-results-<target>` artifacts (30-day retention), and a survivor summary is written
  to the job summary.

To run on demand: Actions → Mutation Testing → Run workflow → pick a target (or `all`).

## When the weekly run surfaces new survivors

1. Download the `mutation-results-<target>` artifact (or read the job summary).
2. Reproduce locally: `gremlins unleash -S lv ./<path>/`.
3. Triage by the impact table above.
4. For High-impact survivors, write killer tests (TDD loop above) and open a PR.
5. For Medium/Low or equivalent mutants, add them to the "remaining survivors" list here so
   the next run's diff is meaningful.
