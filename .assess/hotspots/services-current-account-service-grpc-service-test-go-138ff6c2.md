# Hotspot: `services/current-account/service/grpc_service_test.go`

_First flagged: 2026-05-22. Last seen: 2026-05-29. Status: persistent._

## Current metrics

| Metric | Value |
|--------|-------|
| LOC | 1427 |
| Cyclomatic complexity (file max) | 138.0 |
| Commits in churn window | 27 |
| Has test file | yes |

## History across runs

| Run date | LOC | CCN | Commits | Status |
|----------|-----|-----|---------|--------|
| 2026-05-29 | 1427 | 138.0 | 27 | persistent |

## Briefing for editing this file

Use this briefing when about to modify `services/current-account/service/grpc_service_test.go`:

Hotspot (persistent). 1427 LOC, max cyclomatic complexity 138.0, 27 commits in churn window. (Briefing refined by LLM via assess_finalize - see Suggested actions below.)

## Suggested actions

- Large table-driven suite (ccn 138); consider splitting into per-RPC test files if it keeps growing

