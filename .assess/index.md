# Assess Wiki Index

_Last updated: 2026-05-29_

Catalog of every hotspot ever flagged by `/assess` in this repo. Status reflects the most recent run.

| File | First Flagged | Last Seen | Status | Latest CCN | Latest LOC |
|------|---------------|-----------|--------|------------|------------|
| `frontend/src/App.tsx` | 2026-05-29 | 2026-05-29 | new | 91.0 | 289 |
| `cmd/meridian/main.go` | 2026-05-29 | 2026-05-29 | new | 69.0 | 431 |
| `services/control-plane/internal/validator/manifest_validator_test.go` | 2026-05-22 | 2026-05-29 | persistent | 218.0 | 1054 |
| `services/current-account/service/grpc_service_test.go` | 2026-05-22 | 2026-05-29 | persistent | 138.0 | 1427 |
| `services/payment-order/service/grpc_service_test.go` | 2026-05-22 | 2026-05-29 | persistent | 204.0 | 2283 |
| `services/current-account/service/grpc_service_integration_test.go` | 2026-05-29 | 2026-05-29 | new | 98.0 | 1069 |
| `services/payment-order/cmd/main.go` | 2026-05-29 | 2026-05-29 | new | 57.0 | 306 |
| `Makefile` | 2026-05-29 | 2026-05-29 | new | 72.0 | 463 |
| `services/current-account/adapters/persistence/repository.go` | 2026-05-29 | 2026-05-29 | new | 92.0 | 408 |
| `services/api-gateway/server.go` | 2026-05-29 | 2026-05-29 | new | 100.0 | 421 |
| `services/current-account/service/coverage_unit_test.go` | 2026-05-22 | 2026-05-29 | graduated | - | - |
| `services/position-keeping/domain/financial_position_log_test.go` | 2026-05-22 | 2026-05-29 | graduated | 336 | 1597 |
| `frontend/src/features/manifests/components/manifest-graph.tsx` | 2026-05-22 | 2026-05-29 | graduated | - | - |
| `services/mcp-server/internal/tools/economy_test.go` | 2026-05-22 | 2026-05-29 | graduated | 194 | 1023 |
| `frontend/src/features/manifests/components/manifest-graph.test.tsx` | 2026-05-22 | 2026-05-29 | graduated | - | - |
| `services/identity/service/grpc_service_test.go` | 2026-05-22 | 2026-05-29 | graduated | 161 | 1534 |
| `shared/pkg/saga/schema/service_modules.go` | 2026-05-22 | 2026-05-29 | graduated | - | - |

## Legend

- **active** - in the latest top hotspots list
- **graduated** - was a hotspot, no longer is (good)
- **regressed** - still a hotspot, and getting worse
- **persistent** - still a hotspot, roughly unchanged

## How this gets updated

Each `/assess` run reads this file, the prior `complexity-stats.json`, and the latest run output, then rewrites this index. Per-file detail lives in `hotspots/<slug>.md`. Run history lives in `log.md`.
