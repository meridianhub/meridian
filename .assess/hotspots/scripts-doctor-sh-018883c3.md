# Hotspot: `scripts/doctor.sh`

_First flagged: 2026-06-04. Last seen: 2026-06-19. Status: persistent._

## Current metrics

| Metric | Value |
|--------|-------|
| LOC | 709 |
| Cyclomatic complexity (file max) | 73.0 |
| Commits in churn window | 8 |
| Has test file | yes |

## History across runs

| Run date | LOC | CCN | Commits | Status |
|----------|-----|-----|---------|--------|
| 2026-06-19 | 709 | 73.0 | 8 | persistent |

## Briefing for editing this file

Use this briefing when about to modify `scripts/doctor.sh`:

Hotspot (persistent). 709 LOC, max cyclomatic complexity 73.0, 8 commits in churn window. (Briefing refined by LLM via assess_finalize - see Suggested actions below.) Growth profile: monotonic (+803 LOC, 0 net reductions over 8 commits in 3 months).

## Suggested actions

- Review the co-introduced test independently - confirm it pins intended behaviour, not the script's own output (self_referential_tests)
- Split the 803-line accretion-only script into sourced modules (scripts/doctor.d/*.sh) so no file exceeds the 800-line ratchet

