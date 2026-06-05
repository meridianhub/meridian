# Assess Wiki Index

_Last updated: 2026-06-04_

Catalog of every hotspot ever flagged by `/assess` in this repo. Status reflects the most recent run.

| File | First Flagged | Last Seen | Status | Latest CCN | Latest LOC |
|------|---------------|-----------|--------|------------|------------|
| `services/current-account/service/grpc_service_integration_test.go` | 2026-05-29 | 2026-06-04 | new | 98.0 | 1069 |
| `Makefile` | 2026-05-29 | 2026-06-04 | new | 72.0 | 463 |
| `scripts/demo.sh` | 2026-06-04 | 2026-06-04 | new | 118.0 | 1113 |
| `services/payment-order/service/grpc_service_test.go` | 2026-05-22 | 2026-06-04 | new | 204.0 | 2283 |
| `services/financial-accounting/service/financial_accounting_service_test.go` | 2026-06-04 | 2026-06-04 | new | 116.0 | 1943 |
| `services/current-account/service/grpc_service_test.go` | 2026-05-22 | 2026-06-04 | new | 138.0 | 1427 |
| `frontend/src/App.tsx` | 2026-05-29 | 2026-06-04 | new | 91.0 | 289 |
| `cmd/meridian/main.go` | 2026-05-29 | 2026-06-04 | new | 69.0 | 431 |
| `scripts/doctor.sh` | 2026-06-04 | 2026-06-04 | new | 73.0 | 709 |
| `services/current-account/service/lien_service_test.go` | 2026-06-04 | 2026-06-04 | new | 90.0 | 1538 |

## Legend

- **active** - in the latest top hotspots list
- **graduated** - was a hotspot, no longer is (good)
- **regressed** - still a hotspot, and getting worse
- **persistent** - still a hotspot, roughly unchanged

## How this gets updated

Each `/assess` run reads this file, the prior `complexity-stats.json`, and the latest run output, then rewrites this index. Per-file detail lives in `hotspots/<slug>.md`. Run history lives in `log.md`.
