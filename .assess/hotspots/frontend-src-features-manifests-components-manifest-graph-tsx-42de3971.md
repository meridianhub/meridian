# Hotspot: `frontend/src/features/manifests/components/manifest-graph.tsx`

_First flagged: 2026-05-22. Last seen: 2026-05-28. Status: regressed._

## Current metrics

| Metric | Value |
|--------|-------|
| LOC | 993 |
| Cyclomatic complexity (file max) | 156.0 |
| Commits in churn window | 0 |
| Has test file | unknown |

## History across runs

| Run date | LOC | CCN | Commits | Status |
|----------|-----|-----|---------|--------|
| 2026-05-28 | 993 | 156.0 | 0 | regressed |

## Briefing for editing this file

Use this briefing when about to modify `frontend/src/features/manifests/components/manifest-graph.tsx`:

Hotspot (regressed). 993 LOC, max cyclomatic complexity 156.0, 0 commits in churn window. (Briefing refined by LLM via assess_finalize - see Suggested actions below.)

## Suggested actions

- Add ESLint `complexity` (warn 15), `max-lines` (warn 500), `max-lines-per-function` (warn 80) to frontend/eslint.config.js
- Refactor the node/edge rendering logic into smaller components - file regressed +1 ccn this run with no rule in place

