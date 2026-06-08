# Cross-Service Tests

Suite of end-to-end, integration, contract, and load tests that exercise Meridian across
multiple services. Per-package unit tests live next to the code they cover under `services/`;
this directory holds the tests that span service boundaries.

## Test Suites

| Directory | Scope |
|-----------|-------|
| `audit-e2e/` | Audit system end-to-end flow across services - see [audit-e2e/README.md](audit-e2e/README.md) |
| `clearinge2e/` | Clearing and settlement end-to-end flow |
| `identity-e2e/` | Identity, SSO, and authentication end-to-end flow |
| `reconciliation-e2e/` | Reconciliation run end-to-end flow |
| `mapping-e2e/` | Mapping-layer transformation end-to-end flow |
| `integration/` | Cross-service integration tests |
| `contracts/` | Behavioural API contract tests |
| `proto/` | Protobuf wire-compatibility tests |
| `architecture/` | Architecture and dependency-boundary assertions |
| `multi_tenant/` | Multi-tenant isolation tests |
| `load/` | Load and performance tests |

## Detailed Suite Docs

- [Audit System End-to-End Tests](audit-e2e/README.md) - full audit flow from service operations to tenant `audit_log` tables
