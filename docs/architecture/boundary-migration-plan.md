# BIAN Service Boundary Migration Plan

**Document Version:** 2.0
**Date:** 2025-12-15
**Status:** ✅ Completed
**Related Documents:**

- [Task 14: Service Coupling Analysis](service-coupling-analysis.md)
- [Task 15: BIAN Service Boundaries](bian-service-boundaries.md)
- [Event-Driven Architecture](event-driven-architecture.md)
- [ADR-0002: Microservices Per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)

## Executive Summary

### Current State (December 2025)

Meridian's microservices architecture demonstrates **excellent BIAN boundary compliance** with **zero violations**.

**Verified State:**

- **Zero P0 violations**: No service imports another service's internal packages
- **Zero P1 violations**: Platform code is correctly located in `shared/platform/` (not `internal/platform/`)
- **Safe proto dependencies**: Services communicate via versioned gRPC contracts
- **Independent databases**: Each service owns its schema with no cross-service database access
- **Stable architecture**: Position-keeping (I=0.00) and financial-accounting (I=0.00) are stable providers; current-account (I=1.00) is an orchestration layer with expected high instability
- **Depguard enabled**: Automated linting enforces dependency constraints

### Historical Note

Version 1.0 of this document (November 2025) described a migration from `internal/platform/` to `pkg/platform/`. Upon investigation in December 2025, we found:

1. **No `internal/platform/` directory exists** - platform code was already in `shared/platform/`
2. **No migration was required** - the described violations did not exist in the codebase
3. **Document was based on hypothetical analysis** - not the actual codebase state

### Resolution

Instead of a migration, we implemented **depguard linting** to enforce boundary constraints going forward:

- Blocks deprecated packages (`io/ioutil`, `github.com/pkg/errors`)
- Prevents test-only packages from leaking into production code
- Provides automated CI enforcement of dependency rules

See `.golangci.yml` for the complete depguard configuration.

---

## Current Architecture Summary

### Platform Code Location

**Actual Location:** `shared/platform/`
**Contains:**
- `observability/` - OpenTelemetry tracing, logging, metrics
- `kafka/` - Kafka event publishing infrastructure
- `testdb/` - Test database helpers (Testcontainers)
- `auth/` - JWT validation and gRPC interceptors
- `db/` - Database utilities and tenant scoping
- `tenant/` - Multi-tenant context handling
- `audit/` - Audit logging utilities
- `await/` - Async utilities

### Service Communication

| From | To | Protocol | Notes |
|------|-----|----------|-------|
| current-account | position-keeping | gRPC | Circuit breaker pattern |
| current-account | financial-accounting | gRPC | Circuit breaker pattern |
| position-keeping | (events) | Kafka | Async event publishing |
| financial-accounting | (events) | Kafka | Async event publishing |

### Coupling Metrics

| Service | Afferent (Ca) | Efferent (Ce) | Instability (I) | Role |
|---------|---------------|---------------|-----------------|------|
| current-account | 0 | 2 | 1.00 | Orchestrator |
| position-keeping | 1 | 0 | 0.00 | Stable provider |
| financial-accounting | 1 | 0 | 0.00 | Stable provider |

### Depguard Configuration

The following dependency rules are enforced via golangci-lint's depguard linter:

**Blocked in production code:**
- `io/ioutil` - Deprecated since Go 1.16
- `github.com/pkg/errors` - Use standard library errors
- `testing` - Test-only package
- `github.com/stretchr/testify/*` - Test-only packages
- `github.com/testcontainers/testcontainers-go` - Test-only package

**Allowed in test files:**
- All test packages above are permitted
- Only `io/ioutil` remains blocked (deprecated)

---

## Archived Content

<details>
<summary>Click to expand original v1.0 migration plan (now obsolete)</summary>

The following sections describe a migration that was planned but never needed because the codebase was already correctly structured. This content is preserved for historical reference only.

### Original Phase 1-3 Plans

The original document described migrating from `internal/platform/` to `pkg/platform/`.
This was based on an analysis that did not accurately reflect the actual codebase state.

The platform code was always located in `shared/platform/`, which is a valid Go convention
for shared code within a monorepo. No migration was required.

</details>

---

## Validation Commands

### Verify Linting

```bash
# Run golangci-lint (includes depguard)
golangci-lint run

# Expected: 0 issues
```

### Verify Build

```bash
make build
make test
```

---

**Document Version:** 2.0
**Last Updated:** 2025-12-15
**Status:** Completed - No migration required
