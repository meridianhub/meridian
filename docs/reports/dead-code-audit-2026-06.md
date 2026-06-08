# Go Dead-Code Audit (U1000) — June 2026

**Unit:** assess-2026-06-04.9
**Tool:** `staticcheck` 2026.1 (v0.7.0), check `U1000` (unused code)
**Scope:** entire module `github.com/meridianhub/meridian` (`./...`), 358 packages, test files included
**Date:** 2026-06-08

## Purpose

The `/assess` Layer 1 (Code Health) scorecard carried a caveat: dead/unused Go
code had not been confirmed clean by a static analyser. This audit runs
`staticcheck`'s `U1000` check across the whole module to confirm or refute the
presence of unreachable, never-called code and to close that caveat.

`U1000` flags code that is unused: unexported functions, methods, types,
constants, variables, and struct fields that are never referenced anywhere the
analyser can see (including test files, which `staticcheck` analyses by
default).

## Method

```bash
# From the worktree root
staticcheck -checks U1000 ./... 2>&1 | tee /tmp/dead-code-report-9.txt
```

To guard against a false "clean" caused by a broken analysis (e.g. a build
failure short-circuiting the run), three cross-checks were performed:

1. `go build ./...` — exit 0 (the module compiles, so `staticcheck` has a valid
   type-checked program to analyse).
2. `go list ./...` — 358 packages enumerated and passed to the analyser.
3. `staticcheck ./...` with the **default** check set — 150 findings. The tool
   is demonstrably emitting diagnostics against this codebase, so the empty
   `U1000` result is a true negative, not a silent no-op.

## Result

```text
staticcheck -checks U1000 ./...   → 0 findings (exit 0)
```

**Zero unused-code findings across all 358 packages.** There is no confirmed
dead code to remove. No source files were modified.

This is the expected and desired outcome: the report itself is the deliverable
for a clean run.

## False positives

None to document — `U1000` produced no findings, so there were no
reflection-driven (GORM models, proto mappers), interface-method, external-
consumer (MCP tools, saga handlers), or test-only-helper false positives to
suppress with `//nolint:unused`. The categories are noted here only to record
that they were the patterns we were prepared to whitelist had any surfaced.

## Out-of-scope paths (not removed even if flagged)

To avoid colliding with concurrent test-authoring work (unit
assess-2026-06-04.8), the following paths were excluded from any modification.
`U1000` flagged nothing in them either, so no follow-ups are carried forward:

- `services/position-keeping/`
- `shared/pkg/saga/`
- `shared/pkg/money/`

## Layer 1 caveat closed

The `/assess` Layer 1 dead-code caveat is **closed**. A whole-module
`staticcheck -checks U1000 ./...` run, validated against a passing build and a
working analyser, confirms the Go tree contains no statically detectable unused
code as of 2026-06-08.

## Reproduction

```bash
go install honnef.co/go/tools/cmd/staticcheck@latest
cd <repo-root>
staticcheck -checks U1000 ./...   # expect: no output, exit 0
```
