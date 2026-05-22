# Assess Wiki Index

_Last updated: 2026-05-22_

Catalog of every hotspot ever flagged by `/assess` in this repo. Status reflects the most recent run.

| File | First Flagged | Last Seen | Status | Latest CCN | Latest LOC |
|------|---------------|-----------|--------|------------|------------|
| `services/control-plane/internal/validator/manifest_validator_test.go` | 2026-05-22 | 2026-05-22 | persistent | 477.0 | 2889 |
| `services/current-account/service/coverage_unit_test.go` | 2026-05-22 | 2026-05-22 | persistent | 423.0 | 5753 |
| `services/payment-order/service/grpc_service_test.go` | 2026-05-22 | 2026-05-22 | persistent | 204.0 | 2283 |
| `services/position-keeping/domain/financial_position_log_test.go` | 2026-05-22 | 2026-05-22 | persistent | 336.0 | 1597 |
| `services/current-account/service/grpc_service_test.go` | 2026-05-22 | 2026-05-22 | persistent | 138.0 | 1427 |
| `frontend/src/features/manifests/components/manifest-graph.tsx` | 2026-05-22 | 2026-05-22 | persistent | 155.0 | 993 |
| `services/mcp-server/internal/tools/economy_test.go` | 2026-05-22 | 2026-05-22 | persistent | 194.0 | 1023 |
| `frontend/src/features/manifests/components/manifest-graph.test.tsx` | 2026-05-22 | 2026-05-22 | persistent | 152.0 | 696 |
| `services/identity/service/grpc_service_test.go` | 2026-05-22 | 2026-05-22 | persistent | 161.0 | 1534 |
| `shared/pkg/saga/schema/service_modules.go` | 2026-05-22 | 2026-05-22 | persistent | 136.0 | 474 |

## Legend

- **active** - in the latest top hotspots list
- **graduated** - was a hotspot, no longer is (good)
- **regressed** - still a hotspot, and getting worse
- **persistent** - still a hotspot, roughly unchanged

## How this gets updated

Each `/assess` run reads this file, the prior `complexity-stats.json`, and the latest run output, then rewrites this index. Per-file detail lives in `hotspots/<slug>.md`. Run history lives in `log.md`.
