# Hotspot: `services/current-account/service/coverage_unit_test.go`

_First flagged: 2026-05-22. Last seen: 2026-05-28. Status: persistent._

## Current metrics

| Metric | Value |
|--------|-------|
| LOC | 5753 |
| Cyclomatic complexity (file max) | 423.0 |
| Commits in churn window | 0 |
| Has test file | unknown |

## History across runs

| Run date | LOC | CCN | Commits | Status |
|----------|-----|-----|---------|--------|
| 2026-05-28 | 5753 | 423.0 | 0 | persistent |

## Briefing for editing this file

Use this briefing when about to modify `services/current-account/service/coverage_unit_test.go`:

Hotspot (persistent). 5753 LOC, max cyclomatic complexity 423.0, 0 commits in churn window. (Briefing refined by LLM via assess_finalize - see Suggested actions below.)

## Suggested actions

- Split into per-saga test files (deposit, withdrawal, lien lifecycle, freeze)
- Add a `_test.go > 2000 lines` check to scripts/verify-service-conventions.sh

