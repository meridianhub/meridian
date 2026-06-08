# Self-Referential Test Review: `App.tsx` and `doctor.sh`

> Paths in this document are relative to the repository root.

## Context

The `/assess` cross-layer scan flagged two files as `self_referential_tests`:

- `frontend/src/App.tsx` - the most-churned production file (66 commits).
- `scripts/doctor.sh` - the development-environment doctor.

The concern behind the flag: when tests are co-entered with the code they cover,
they risk **pinning the author's mental model** (internal structure, naming,
implementation detail) rather than **specified or observable behaviour**. A
structure-pinning test breaks on a safe refactor and can pass while the behaviour
is broken - it gives false confidence.

This review classifies each relevant test against two criteria:

| Class | Definition | Examples |
|-------|------------|----------|
| **Behaviour-pinning** (good) | Asserts observable outcomes a user or caller can see | Rendered text, navigation/URL outcomes, `aria-current`, error messages, exit codes, stdout |
| **Structure-pinning** (bad) | Asserts internal construction the user never sees | CSS class names, DOM nesting, presence of a function name / comment in source, private state |

## Method

A structured, multi-lens (Six-Hats-style) classification was applied solo:

- **White (facts):** read `App.tsx`, `doctor.sh`, and every test that exercises them.
- **Black (risk):** which assertions break on a behaviour-preserving refactor, or pass on broken behaviour?
- **Yellow (value):** which assertions survive refactors and would catch a real regression?
- **Green (alternatives):** which structure assertions can be re-expressed as behaviour?
- **Red (smell):** does the flag feel like a true positive or a false positive?
- **Blue (synthesis):** the verdict and the conservative change set below.

---

## Part 1 - `frontend/src/App.tsx`

`App.tsx` is a routing/composition root: it wires providers (`QueryClientProvider`,
`AuthProvider`, `TenantProvider`, `ApiClientProvider`), declares the route table,
and gates routes with `ProtectedRoute` / `PlatformOnlyRoute` / `AdminOnlyRoute` /
`FeatureGuard`. Its observable behaviour is **what renders at each URL for each
auth/role state** - which is exactly what its tests assert.

### Tests reviewed

| Test file | Type | Classification | Notes |
|-----------|------|----------------|-------|
| `frontend/e2e/smoke.spec.ts` | Playwright e2e | **Behaviour** | Asserts page title matches `/Meridian/`, `/healthz` responds OK. |
| `frontend/e2e/login-redirect.spec.ts` | Playwright e2e | **Behaviour** | Asserts redirect URLs (`/login`), visible headings, tenant subtitle text, skeleton disappearance. |
| `frontend/e2e/navigation.spec.ts` | Playwright e2e | **Behaviour** | Asserts each sidebar route renders its `h1`, `aria-current="page"` on the active link, 404 copy, mobile sidebar `data-open` toggle. |
| `frontend/e2e/auth-flows.spec.ts` | Playwright e2e | **Behaviour** | Asserts role normalization outcomes, callback token handling, visible error messages, return-to-login navigation. |
| `frontend/src/test/App.test.tsx` | Vitest unit | **Behaviour** | Asserts the rendered `h1` text and that the tree renders without crashing. |
| `frontend/src/test/app-routing.test.tsx` | Vitest unit | **Behaviour** | Asserts redirect/render outcomes of the route guards via visible text (`Login Page`, `Protected Content`, `Tenant Management`). |
| `frontend/src/test/page-structure.test.tsx` | Vitest unit | **Structure (intentional)** | Asserts CSS classes (`.space-y-6`, `text-3xl font-bold tracking-tight`) and DOM nesting. Does **not** test `App.tsx`; it is a cross-page UI-convention fitness function. |
| `frontend/src/test/router.test.tsx` | Vitest unit | **Mixed** | Tests `@/lib/router`, **not** `App.tsx`. Error-boundary cases are behaviour; `getRoutes`/`getRouteHandlers` shape assertions are structure. |
| `frontend/src/test/architecture/*.test.ts` | Vitest unit | **Structure (intentional)** | Architecture fitness functions (barrel exports, cross-feature import bans, file-size ratchets). Not `App.tsx` tests. |

### Verdict: false positive for `App.tsx`

Every test that **actually exercises `App.tsx`** (the four e2e specs plus
`App.test.tsx` and `app-routing.test.tsx`) is behaviour-pinning. They assert
user-visible outcomes - rendered headings, navigation URLs, redirect targets,
active-link state, error copy - none of which depend on `App.tsx`'s internal
component decomposition. They would survive a refactor of `App.tsx`'s internals
and would fail on a real routing/auth regression. That is the correct shape.

The genuinely structure-pinning suites in the frontend
(`page-structure.test.tsx`, `architecture/*.test.ts`) are **deliberate fitness
functions** that enforce documented, cross-cutting conventions. They do not pin
*one author's mental model of `App.tsx`*; they pin a team-wide standard by design.
They are out of scope for this flag and were left unchanged.

**No conversions made for `App.tsx`. The flag is a well-evidenced false positive.**

### Observation left for human decision (not a test issue)

`frontend/src/lib/router.tsx` (`getRoutes` / `getRouteHandlers` / `createRouteHandler`)
is **not imported by `App.tsx` or `main.tsx`** - `App.tsx` declares its routes
inline as JSX. `router.test.tsx` is the only consumer of that module. This is a
parallel route abstraction whose structure-pinning tests guard code that is not
wired into the running app. Deciding whether to wire it in or remove it is a
product/architecture call, not a test conversion - flagged here for a human.

---

## Part 2 - `scripts/doctor.sh`

`doctor.sh` validates (and with `--fix`, repairs) the local dev environment. Its
observable behaviour is its **exit code, stdout/stderr messages, and fix actions**.
It is exercised by `scripts/doctor_test.sh` (not wired into CI).

### Tests reviewed (`scripts/doctor_test.sh`, as committed)

| Test | Mechanism | Classification | Notes |
|------|-----------|----------------|-------|
| Script exists and is executable | `[ -f ] && [ -x ]` | **Behaviour** (artifact property) | Fine. |
| `--help` prints `Usage:` / `--check` / `--fix` / `--verbose` | runs script, greps stdout | **Behaviour** | Exemplary - asserts actual output. |
| PKG_MANAGER validation exists | `grep 'case "$PKG_MANAGER" in'` source | **Structure** | Passes on rename-free source; says nothing about runtime safety. |
| No `eval` with install cmd | `grep 'eval.*install_cmd'` absent | **Structure** | Source-pattern assertion. |
| Security model documented | `grep "Security model:"` source | **Structure** | Asserts a *comment* exists - no runtime behaviour at all. |
| Git hooks function exists | `grep check_git_hooks` source | **Structure** | Function-name presence. |
| `cmp` detects out-of-sync hooks | re-implements `cmp` in the test | **Vacuous** | Tests the `cmp` binary, not `doctor.sh`. |
| `get_install_cmd` exists | `grep get_install_cmd()` source | **Structure** | Function-name presence. |
| macOS/Linux Go install strings | `grep "macos-go.*brew install go"` etc. | **Structure** | Pins exact source strings. |
| PKG_MANAGER is utilized | `grep '\$PKG_MANAGER'` source | **Structure** | Source-pattern assertion. |
| Network uses `curl --fail` | `grep "curl.*--fail"` source | **Structure** | Source-pattern assertion. |
| Handles `gtimeout` | `grep "gtimeout"` source | **Structure** | Source-pattern assertion. |
| Validates Docker daemon | `grep "docker info"` / `check_docker_daemon` | **Structure** | Source-pattern assertion. |

### Verdict: partially valid - and the harness was broken

Two findings:

1. **Structure-pinning dominates.** Apart from the `--help` suite and the
   exists/executable check, the harness greps `doctor.sh`'s source for function
   names, command substrings, and comments. These break on a behaviour-preserving
   refactor (rename a function, reword a comment) and pass even if the
   corresponding behaviour is broken. This part of the `/assess` flag is correct.

2. **Pre-existing defect: the harness never ran to completion.** `doctor_test.sh`
   sets `set -euo pipefail` and increments counters with `((TESTS_PASSED++))`.
   Post-increment evaluates to the *old* value, so `((TESTS_PASSED++))` when the
   counter is `0` returns exit status `1`, which `set -e` treats as fatal. The
   script therefore **aborted after the first passing assertion** (exit 1) and
   silently never executed the remaining suites. It is not in CI, so this went
   unnoticed - a textbook self-referential-test failure mode: co-authored with
   the code, never actually exercised end-to-end.

### Changes made

| Change | File | Rationale |
|--------|------|-----------|
| Replace `((TESTS_PASSED++))` / `((TESTS_FAILED++))` with `VAR=$((VAR + 1))` | `scripts/doctor_test.sh` | Root-cause fix for the `set -e` abort; the harness now runs all suites (20 assertions, exit 0). |
| Add behaviour test: `--help` exits `0` | `scripts/doctor_test.sh` | Uses the previously-defined-but-unused `assert_exit_code` helper to pin the observable exit contract. |
| Add behaviour test: unknown option exits `1` and prints `Unknown option` | `scripts/doctor_test.sh` | Exercises the real argument parser at runtime instead of grepping for the `case` statement - a true structure→behaviour conversion. |

The existing grep-based structure assertions were **left in place** (conservative:
they still function as cheap source-lint smoke checks and removing them would drop
signal). They are documented above as structure-pinning.

### Items left for human decision

- **Hermetic behaviour coverage of the environment checks.** Asserting
  doctor.sh's real behaviour (e.g. "on macOS with Go absent, suggest
  `brew install go`"; "exits non-zero when Docker daemon is down") requires a
  controlled/containerized environment with tools selectively present or absent.
  That is a meaningful test-infrastructure investment, not an in-place conversion,
  and is left for a human to scope. Until then the install-command and
  daemon-detection assertions remain source-pattern greps.
- **Wire `doctor_test.sh` into CI.** It currently runs nowhere. A broken harness
  that no one runs provides no protection. Adding it to a lightweight CI job would
  have caught the `set -e` abort immediately. Flagged for a human decision on CI
  placement.

---

## Summary

| File | `/assess` flag verdict | Action |
|------|------------------------|--------|
| `frontend/src/App.tsx` | **False positive** | None. Tests that exercise `App.tsx` are behaviour-pinning. |
| `scripts/doctor.sh` | **Partially valid** | Fixed a pre-existing `set -e` abort in `doctor_test.sh`; added 3 behaviour assertions; documented remaining structure-pinning greps and flagged hermetic env coverage + CI wiring for human decision. |
