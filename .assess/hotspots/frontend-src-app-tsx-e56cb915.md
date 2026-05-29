# Hotspot: `frontend/src/App.tsx`

_First flagged: 2026-05-29. Last seen: 2026-05-29. Status: new._

## Current metrics

| Metric | Value |
|--------|-------|
| LOC | 289 |
| Cyclomatic complexity (file max) | 91.0 |
| Commits in churn window | 66 |
| Has test file | yes |

## History across runs

| Run date | LOC | CCN | Commits | Status |
|----------|-----|-----|---------|--------|
| 2026-05-29 | 289 | 91.0 | 66 | new |

## Briefing for editing this file

Use this briefing when about to modify `frontend/src/App.tsx`:

Hotspot (new). 289 LOC, max cyclomatic complexity 91.0, 66 commits in churn window. (Briefing refined by LLM via assess_finalize - see Suggested actions below.)

## Suggested actions

- Promote frontend eslint complexity rules from warn to error so they ratchet (currently App.tsx ccn 91 is only flagged advisorily)
- Decompose App.tsx: extract routing and provider wiring into separate modules
- Add as a first target for the new CI mutation-testing pass

