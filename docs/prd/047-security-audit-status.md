# PRD-047 Security Audit — Remediation Status

**Date**: 2026-03-17
**Reference**: [PRD-047: Security Audit — Findings and Remediation](047-security-audit.md)

---

## 1. Summary

A security audit identified **16 findings**: 3 CRITICAL, 5 HIGH, 6 MEDIUM, 2 LOW.

Wave 1 remediation is complete. 11 PRs merged. All CRITICAL and most HIGH findings are
resolved or have foundations in place. Wave 2 RBAC rollout (CRITICAL-1 full coverage)
and Wave 3 defense-in-depth items remain.

| Severity | Total | Resolved | Partial | Pending |
|----------|-------|----------|---------|---------|
| CRITICAL | 3     | 2        | 1       | 0       |
| HIGH     | 5     | 3        | 2       | 0       |
| MEDIUM   | 6     | 2        | 1       | 3       |
| LOW      | 2     | 0        | 0       | 2       |

---

## 2. Remediated Findings (Wave 1 — Complete)

### PR #1710 — Remove Security Scanner Continue-on-Error

**Finding**: MEDIUM-3 — Security scanners were advisory-only; `continue-on-error: true`
in `build.yml` and `|| true` in gosec allowed vulnerable code to merge.

**Fix**: Removed `continue-on-error: true` from Trivy in `build.yml`. Removed `|| true`
from gosec. Security scanning now fails the build on CRITICAL/HIGH findings.

**Status**: RESOLVED

---

### PR #1711 — Add Container Resource Limits to Demo docker-compose

**Finding**: MEDIUM-4 (partial) — Demo `docker-compose.yml` had no container resource
limits, allowing any single container to exhaust host resources.

**Fix**: Added CPU and memory limits to all containers in `docker-compose.yml`.

**Status**: RESOLVED (partial — SSH root access and sslmode=disable remain in Wave 3)

---

### PR #1712 — Enforce Saga Step Limit + Remove Dead MemoryWarningThreshold

**Findings**:
- HIGH-1 — `MaxStepsPerExecution = 1_000_000` was defined but `SetMaxExecutionSteps()`
  was never called on the saga runtime thread, allowing CPU exhaustion via tenant scripts.
- HIGH-4 (partial) — `MemoryWarningThreshold` constant was defined but never referenced
  (dead code). Removed to eliminate false confidence.

**Fix**: Added `SetMaxExecutionSteps(MaxStepsPerExecution)` to the saga runtime.
Deleted dead `MemoryWarningThreshold` constant.

**Status**: RESOLVED (HIGH-1); HIGH-4 memory monitoring remains in Wave 3

---

### PR #1713 — Harden Forecasting Starlark Runtime

**Finding**: HIGH-2 — The forecasting runtime had no script size limit, no step limit,
no static validation, and a 30-second timeout (6x the saga default), enabling 30 seconds
of unbounded CPU and memory per invocation.

**Fix**: Added script size limit (64KB), step limit, static validation, and reduced
default timeout from 30s to 10s.

**Status**: RESOLVED — script size limit, step limit, static validation, and timeout
reduction (30s → 10s) all applied. Shared sandbox extraction for consistency across
runtimes is tracked in Wave 3.

---

### PR #1715 — Register TenantGuard in Bootstrap

**Finding**: CRITICAL-2 — `TenantGuard` (a GORM plugin that rejects queries without
tenant scope) was fully implemented and tested but never registered. `bootstrap.NewDatabase()`
created GORM connections without calling `db.Use(NewTenantGuard())`, enabling cross-tenant
data leakage with no runtime error.

**Fix**: Registered `TenantGuard` in `bootstrap.NewDatabase()`.

**Status**: RESOLVED (GORM path); pgx raw SQL path remains for Wave 3 audit (task 3.12)

---

### PR #1719 — SHA-Pin Supply Chain Actions

**Finding**: HIGH-5 — `appleboy/ssh-action` and `appleboy/scp-action` were pinned to
mutable version tags. A compromised action release would gain root shell on the demo
server and code injection into built images.

**Fix**: Pinned both actions to immutable commit SHAs.

**Status**: RESOLVED

---

### PR #1722 — Fix AUTH_ENABLED Defaults

**Finding**: CRITICAL-3 — `AUTH_ENABLED` defaulted to `false` in the API Gateway and
position-keeping service (vs. `true` in `bootstrap.DefaultAuthConfig()`). A missing
env var in deployment meant the external entry point ran unauthenticated, enabling
tenant impersonation via `x-tenant-id` header.

**Fix**: Changed `LoadAuthConfig()` defaults to `true` in both services.

**Status**: RESOLVED

---

### PR #1724 — Add tenant_id to event_outbox

**Finding**: MEDIUM-5 — `event_outbox` table had no `tenant_id` column; the outbox
worker had no tenant awareness, creating potential for event loss or cross-tenant
event processing bias.

**Fix**: Added `tenant_id` column to `event_outbox` migration. Updated
`PostgresOutboxRepository` and `Worker` to scope queries by tenant.

**Status**: RESOLVED

---

### PR #1729 — Enable AUTH_ENABLED in All K8s ConfigMaps

**Finding**: HIGH-3 — All 11 service K8s ConfigMaps set `AUTH_ENABLED: "false"`. The
first Kubernetes deployment using these manifests would run all services unauthenticated.

**Fix**: Set `AUTH_ENABLED: "true"` in all 11 ConfigMaps.

**Status**: RESOLVED (with OPA regression prevention from PR #1736)

---

### PR #1731 — Create Method RBAC Interceptor Framework

**Finding**: CRITICAL-1 — 15 of 19 services accepted any authenticated request
regardless of role. An `auditor`-role JWT could initiate payments, freeze accounts,
and execute deposits.

**Fix**: Implemented `NewMethodRBACInterceptor` — a fail-closed gRPC interceptor that
enforces a per-method permission map. Framework is complete and tested. Integrated into
`GrpcServerBuilder`.

**Status**: PARTIAL — framework in place; permission maps for the 15 remaining services
are Wave 2 work (approximately 1 week)

---

### PR #1736 — Add OPA Gatekeeper Constraint + Security Defaults CI Gate

**Findings**:
- HIGH-3 (regression prevention) — OPA Gatekeeper had no policy blocking
  `AUTH_ENABLED: "false"` in ConfigMaps.
- HIGH-3 (CI gate) — No CI job verified security defaults after PRs.

**Fix**: Added `AuthEnabledConstraint` OPA Gatekeeper policy. Added `security-defaults`
CI job that fails if any ConfigMap contains `AUTH_ENABLED: "false"`.

**Status**: RESOLVED (regression prevention for HIGH-3)

---

## 3. Remaining Work — Wave 2 (RBAC Rollout)

These items are from PRD Section 5.2 and were not completed in Wave 1.

| Item | Finding | Description |
|------|---------|-------------|
| RBAC permission maps — 15 services | CRITICAL-1 | Define `MethodPermissions` maps for current-account, payment-order, financial-accounting, position-keeping, and 11 others. ~1 week effort. |
| Migrate hand-rolled interceptor chains | Consistency | Services that build their own interceptor chains should migrate to `GrpcServerBuilder` for uniform security defaults. |
| Rate-limiting middleware | MEDIUM-1 | Add rate-limiting interceptor to the gRPC interceptor chain. No rate limiting currently exists on financial API endpoints. |
| `GrpcServerBuilder.Build()` fail-closed | MEDIUM-6 | Unified binary gRPC port is built without `.WithAuthInterceptor()`; `Build()` should require auth configuration or fail. |
| NetworkPolicy for all services | MEDIUM-2 | Replicate existing NetworkPolicy to all services to restrict inter-service traffic. |

---

## 4. Remaining Work — Wave 3 (Defense in Depth)

These items are from PRD Section 5.3.

| Item | Finding | Description |
|------|---------|-------------|
| Extract `shared/platform/sandbox` | HIGH-1, HIGH-2, HIGH-4 | Centralise `HardenThread()` across all Starlark runtimes for consistent step limits, script size limits, and static validation. |
| Go `runtime.MemStats` monitoring | HIGH-4 | Implement memory monitoring in sandbox. `MemoryWarningThreshold` constant was removed in PR #1712; a real memory limit requires MemStats integration. |
| Service-to-service auth | MEDIUM-2 | Use existing `OAuth2Client` for service-to-service JWT. Currently all internal gRPC uses insecure credentials. |
| Demo server hardening | MEDIUM-4 | Remaining items: create non-root deploy user, move JWT signing key from env var to file mount. |
| Security tests for runtimes | Test parity | Add security-focused tests for forecasting and valuation Starlark runtimes (step limit enforcement, script size rejection, timeout behaviour). |
| ~~Auth hook in `wrapHandler`~~ | ~~Sandbox RBAC~~ | ~~Done: Authorization check added in `wrapHandler` using ResourceType/RequiredPermission on HandlerDef.~~ |
| ~~Remove `invoke_handler` shim~~ | ~~LOW-1~~ | ~~Done: Removed backward-compatibility shim. All scripts migrated to typed service modules.~~ |
| Cosign image signing | Supply chain | Add cosign signing to the build pipeline for Docker image provenance. |
| Cross-tenant isolation tests | Regression | Integration tests that assert one tenant cannot read or write another tenant's data. |
| Audit pgx raw SQL paths | CRITICAL-2 | The pgx path (`shared/platform/db/db.go`) has no equivalent to TenantGuard. Audit raw SQL for missing tenant scoping. |
| Fix X-Forwarded-For trust in security logs | LOW-2 | `extractClientIP` trusts the `x-forwarded-for` header without validation. Direct connections can spoof IP in audit logs. |

---

## 5. Success Criteria Status

From PRD Section 6:

| Criterion | Status |
|-----------|--------|
| All CRITICAL findings resolved | Partial — CRITICAL-1 framework complete, full rollout in Wave 2 |
| All HIGH findings resolved | Partial — HIGH-1, HIGH-2, HIGH-3, HIGH-5 resolved; HIGH-4 memory monitoring in Wave 3 |
| Security scanning blocks PR merges on CRITICAL/HIGH | Met — PR #1710 |
| TenantGuard active on GORM path — cross-tenant queries fail-closed | Met — PR #1715 |
| RBAC enforced on all gRPC endpoints via method permission interceptor | Partial — interceptor exists (PR #1731); permission maps for 15 services pending |
| No K8s manifest can deploy with `AUTH_ENABLED=false` (OPA + CI) | Met — PR #1729 + PR #1736 |
| All Starlark runtimes have consistent step limits, script size limits, and static validation | Partial — saga and forecasting hardened; shared sandbox extraction in Wave 3 |
