# PRD-047: Security Audit — Findings and Remediation

## Status: Draft

## Author: Six Thinking Hats Security Audit Team

## Date: 2026-03-16

## Related Documents

- [PRD-044: Auth Flow Architecture](044-auth-flow-architecture.md)
- [PRD-043: MCP Manifest Tenant Isolation](
  043-mcp-manifest-tenant-isolation.md)

---

## 1. Executive Summary

A comprehensive security audit of the Meridian codebase was conducted
using a four-person expert panel (Security Engineer, Platform Engineer,
DevOps/SRE, Sandbox Specialist) through the Six Thinking Hats
methodology.

**Central Finding**: The security *architecture* is ahead of
*enforcement*. The codebase contains well-designed, tested security
controls that aren't wired up in production. Most critical fixes
activate existing code rather than building new capabilities.

**Findings**: 3 CRITICAL, 5 HIGH, 6 MEDIUM, 2 LOW vulnerabilities
across authentication, authorization, tenant isolation, sandbox
security, CI/CD, and infrastructure.

**Remediation**: A 3-wave plan where Wave 1 (~25 lines of changes,
~4 hours) closes 5+ findings with zero code risk.

---

## 2. Scope

### In Scope

- Go application code (`services/`, `shared/`, `cmd/`)
- gRPC API security (authentication, authorization, interceptors)
- Multi-tenant data isolation (schema-based, GORM, pgx)
- Starlark/CEL sandbox security (saga, forecasting, valuation)
- CI/CD pipeline security (GitHub Actions, scanning, supply chain)
- Container and Kubernetes security posture
- Demo deployment security
- Secrets management

### Out of Scope

- Penetration testing of running environments
- Third-party dependency CVE triage (covered by Trivy/govulncheck)
- Frontend/UI security (no frontend code at time of audit)
- Business logic correctness

---

## 3. Findings

### 3.1 Controls That Exist But Aren't Enforced

This is the central theme. The team built security infrastructure
but didn't complete the wiring:

| Control | Status | Gap |
|---------|--------|-----|
| TenantGuard | Implemented, tested (240 LOC tests) | Not registered in bootstrap |
| RBAC (7-role) | Fully implemented | 1 of 19 services checks roles |
| Saga step limit | Constant defined (1M) | Never applied to thread |
| Memory threshold | Constant defined (10MB) | Dead code — never referenced |
| OPA Gatekeeper | Deployed, blocks `LOCAL_DEV_MODE` | No `AUTH_ENABLED` rule |
| Security scanners | 7 tools running | Advisory-only in build pipeline |

### 3.2 Validated Vulnerabilities

#### CRITICAL-1: RBAC Not Enforced

**Description**: The RBAC framework (`shared/platform/auth/rbac.go`)
defines 7 roles with a resource-type permission matrix, delegation
hierarchy, and tested helper functions. However, only 1 of 19
services calls any RBAC check
(`reconciliation/service/finalizer.go:295`). All other handlers
accept any authenticated request regardless of role.

**Evidence**:

- `ExecuteDeposit` (`grpc_deposit_endpoints.go:28`) — No role check
- `InitiatePaymentOrder` (`grpc_initiate.go:26`) — No role check
- `ControlCurrentAccount` (`grpc_control_endpoints.go:34`) — Imports
  `auth` but only uses `GetUserIDFromContext()` for audit logging
- `InitiateWithdrawal`, `InitiateLien`, `ExecuteLien` — No checks

**Impact**: An `auditor`-role JWT (intended read-only) can create
accounts, initiate payments, freeze accounts, execute deposits, and
create liens. The 7-role hierarchy provides zero runtime protection.

**Exploitability**: Trivial — any valid JWT grants full access.

#### CRITICAL-2: TenantGuard Not Registered

**Description**: `TenantGuard`
(`shared/platform/db/tenant_guard.go`) is a GORM plugin that rejects
queries without tenant scope. It is fully implemented and tested but
never registered in production. `bootstrap.NewDatabase()` at
`bootstrap.go:77` creates GORM connections without calling
`db.Use(NewTenantGuard())`.

**Attack path**:

1. Developer writes `gormDB.Find(&results)` without
   `WithGormTenantTransaction`
2. `SkipDefaultTransaction: true` means no auto-transaction
3. `SET LOCAL search_path` is never called
4. Query runs against default `search_path` (public)
5. Cross-tenant data leakage with NO runtime error

**Impact**: Cross-tenant data leakage or corruption. One tenant
could see another's account balances, transactions, or positions.

**Note**: The pgx path (`shared/platform/db/db.go`) has no
equivalent guard at all.

#### CRITICAL-3: AUTH_ENABLED Defaults to `false`

**Description**: Different components use different defaults:

| Component | Default | File |
|-----------|---------|------|
| `bootstrap.DefaultAuthConfig()` | **true** | `auth.go:53` |
| API Gateway `LoadAuthConfig()` | **false** | `config.go:221` |
| Position-keeping `loadAuthConfig()` | **false** | `config.go:220` |

**Attack path**: Missing env var in deployment = unauthenticated
API surface. `TenantExtractionInterceptor` trusts `x-tenant-id`
headers without JWT validation, enabling tenant impersonation.

**Impact**: Complete authentication bypass for the API gateway
(the external entry point) and position-keeping service.

#### HIGH-1: Saga Step Limit Not Enforced

**Description**: `MaxStepsPerExecution = 1_000_000` is defined at
`shared/pkg/saga/runtime.go:29` but `SetMaxExecutionSteps()` is
never called on the saga thread. The valuation runtime at
`starlark_runtime.go:127` does call it, proving the pattern works.

**Impact**: CPU exhaustion via tenant saga scripts. The 5-second
timeout is the only backstop, but 5 seconds of unbounded CPU per
request enables amplification attacks.

#### HIGH-2: Forecasting Runtime Has Zero Safety Controls

**Description**: The forecasting Starlark runtime
(`services/forecasting/starlark/runner.go`) has:

- No script size limit (saga has 64KB)
- No step limit (not even an unenforced constant)
- No static validation (saga has `ValidateSagaScript`)
- 30-second timeout (6x longer than saga/valuation)

**Impact**: 30 seconds of unbounded CPU and memory per invocation.
A script allocating large data structures could OOM the process.

#### HIGH-3: AUTH_ENABLED=false in All K8s ConfigMaps

**Description**: All 11 service K8s configmaps in
`services/*/k8s/configmap.yaml` set `AUTH_ENABLED: "false"`. The
production overlay does not override this. No OPA Gatekeeper policy
blocks `AUTH_ENABLED=false`.

**Impact**: First Kubernetes deployment using these manifests runs
all services unauthenticated.

#### HIGH-4: Memory Exhaustion in All Starlark Runtimes

**Description**: `MemoryWarningThreshold = 10 * 1024 * 1024` is
defined in `runtime.go:27` but never referenced anywhere. No memory
limits exist in any of the three runtimes. A script allocating large
lists/dicts can OOM the process.

**Impact**: Process death affecting all co-hosted services and
tenants.

#### HIGH-5: GitHub Actions Supply Chain Risk

**Description**: `appleboy/ssh-action@v1.2.5` and
`appleboy/scp-action@v1.0.0` handle the SSH private key for demo
deployment. They are community-maintained (single developer),
pinned to minor versions (not SHAs).

**Impact**: Compromised action release = root shell on demo server
plus ability to inject malicious code into built images.

#### MEDIUM-1: No Rate Limiting on Financial API Endpoints

**Description**: No rate limiting middleware in the gRPC interceptor
chain for any service. Rate limiting only exists for outbound
provider connections and MCP sessions.

**Impact**: API abuse, database exhaustion, Kafka flooding. Each
payment order call triggers multiple downstream operations.

#### MEDIUM-2: No Inter-Service Authentication

**Description**: Services connect using
`grpc.WithTransportCredentials(insecure.NewCredentials())`. No mTLS,
no service mesh, no service-to-service JWT.

**Impact**: Any pod with network access can call any internal gRPC
endpoint. Combined with disabled auth, this enables full
cross-tenant access.

#### MEDIUM-3: Security Scanners Advisory-Only

**Description**: Trivy in `build.yml` uses `continue-on-error: true`.
Gosec uses `|| true`. The dedicated `security.yml` workflow does fail
on CRITICAL/HIGH, but may not be a required status check.

**Impact**: Vulnerable code can merge if `security.yml` isn't
a required check.

#### MEDIUM-4: Demo Server Hardening Gaps

**Description**: SSH as `root`, no container resource limits in
docker-compose, `sslmode=disable` on internal Postgres, no evidence
of firewall/fail2ban.

**Impact**: Limited by network topology (Caddy fronts external
access), but fragile.

#### MEDIUM-5: Outbox Worker Tenant Scope Ambiguity

**Description**: `PostgresOutboxRepository` and `Worker` have zero
tenant awareness. No `tenant_id` column in `event_outbox`, no
`tenant_id` in Kafka headers. Worker queries without tenant scope.

**Impact**: Potential event loss or single-tenant bias in event
processing.

#### MEDIUM-6: Unified Binary gRPC Port Unauthenticated

**Description**: `cmd/meridian/main.go:253` builds gRPC server
without `.WithAuthInterceptor()`. The HTTP gateway has separate auth
config, but gRPC port 50051 is accessible to all containers on the
Docker network.

**Impact**: Any container on the network gets full unauthenticated
gRPC access.

#### LOW-1: Deprecated `invoke_handler` Shim

**Description**: The `invoke_handler` backward-compat shim accepts
handler names as strings with no parameter type coercion or schema
validation, unlike typed service modules.

**Impact**: Incorrect parameter types could cause crashes or
unexpected behavior.

#### LOW-2: X-Forwarded-For Spoofing in Security Logs

**Description**: `extractClientIP` trusts `x-forwarded-for` header.
Direct connections could spoof IP in audit logs.

**Impact**: Corrupted forensic data, not access escalation.

---

## 4. Existing Strengths

The audit identified significant security maturity that remediation
should build on:

- **Container security**: Distroless base images, non-root execution,
  static binaries, multi-stage builds across all Dockerfiles
- **K8s pod security**: `runAsNonRoot`, dropped capabilities,
  read-only filesystem, seccomp profiles, resource limits,
  health probes
- **Auth interceptor design**: Anti-subdomain-hopping check with
  Prometheus metrics, JWKS support, key rotation ready
- **Tenant isolation primitives**: `pq.QuoteIdentifier()` used
  consistently (369 occurrences), `SET LOCAL search_path`, regex
  validation, strongly-typed TenantID
- **Token handling**: `crypto/rand`, SHA256 storage,
  constant-time comparison
- **Password security**: bcrypt cost 12, policy enforcement,
  history checking
- **Control plane RBAC**: Fail-closed allowlist pattern — proven
  and ready to generalize
- **Security scanning**: 7 tools (govulncheck, gosec, Trivy,
  gitleaks, CodeQL, dependency-review, SBOM) with daily runs
- **Saga circular detection**: Three-phase (draft, activation,
  runtime) with depth limits
- **Starlark builtin whitelisting**: Safe-by-default approach —
  dangerous functions don't exist in runtime
- **OPA Gatekeeper**: Deployed with `LOCAL_DEV_MODE` constraint —
  template ready for `AUTH_ENABLED`

---

## 5. Remediation Plan

### Wave 1: Activate Existing Controls (~4 hours)

| # | Task | Closes |
|---|------|--------|
| 1.1 | Add `SetMaxExecutionSteps(MaxStepsPerExecution)` to saga runtime | HIGH-1 |
| 1.2 | Add `SetMaxExecutionSteps(1M)` to forecasting runtime | HIGH-2 |
| 1.3 | Register TenantGuard in bootstrap (GORM only; pgx gap in 3.12) | CRITICAL-2 |
| 1.4 | Change AUTH_ENABLED default to `true` in API Gateway + pos-keeping | CRITICAL-3 |
| 1.5 | Set `AUTH_ENABLED: "true"` in all 11 K8s configmaps | HIGH-3 |
| 1.6 | Remove `continue-on-error` from Trivy, `\|\| true` from gosec | MEDIUM-3 |
| 1.7 | SHA-pin `appleboy/ssh-action` and `appleboy/scp-action` | HIGH-5 |
| 1.8 | Add container resource limits to demo docker-compose | MEDIUM-4 |
| 1.9 | Delete dead `MemoryWarningThreshold` constant | HIGH-4 |
| 1.10 | Add OPA Gatekeeper constraint for `AUTH_ENABLED=false` | HIGH-3 |

### Wave 2: RBAC Interceptor + Sandbox Hardening (~1 week)

| # | Task | Closes |
|---|------|--------|
| 2.1 | Create `NewMethodRBACInterceptor` — fail-closed permission map | CRITICAL-1 |
| 2.2 | Define permission maps for all 19 services (~1 week) | CRITICAL-1 |
| 2.3 | Migrate hand-rolled interceptor chains to GrpcServerBuilder | Consistency |
| 2.4 | Add script size limit + static validation to forecasting | HIGH-2 |
| 2.5 | Reduce forecasting `DefaultTimeout` from 30s to 10s | HIGH-2 |
| 2.6 | Replicate NetworkPolicy to all services | MEDIUM-2 |
| 2.7 | Add CI "Security Defaults Gate" job | Regression |
| 2.8 | Add rate limiting middleware to gRPC interceptor chain | MEDIUM-1 |
| 2.9 | Make `GrpcServerBuilder.Build()` fail-closed on auth | MEDIUM-6 |

### Wave 3: Defense in Depth (~2-3 weeks)

| # | Task | Closes |
|---|------|--------|
| 3.1 | Extract `shared/platform/sandbox/` with `HardenThread()` | HIGH-1,2,4 |
| 3.2 | Implement Go `runtime.MemStats` monitoring in sandbox | HIGH-4 |
| 3.3 | Service-to-service auth via existing `OAuth2Client` | MEDIUM-2 |
| 3.4 | Add `tenant_id` to `event_outbox` table + Kafka headers | MEDIUM-5 |
| 3.5 | Create non-root deploy user on demo droplet | MEDIUM-4 |
| 3.6 | Move JWT signing key from env var to file mount | Key leak |
| 3.7 | Add security tests for forecasting/valuation runtimes | Test parity |
| 3.8 | Add auth hook in `wrapHandler` for saga handler ACLs | Sandbox RBAC |
| 3.9 | Remove deprecated `invoke_handler` shim | LOW-1 |
| 3.10 | Add cosign image signing to build pipeline | Supply chain |
| 3.11 | Add cross-tenant isolation integration tests | Regression |
| 3.12 | Audit raw SQL paths, consider tenant guard wrapper | CRITICAL-2 |

---

## 6. Success Criteria

- All CRITICAL findings resolved (Wave 1 + Wave 2)
- All HIGH findings resolved (Wave 1 + Wave 2)
- Security scanning blocks PR merges on CRITICAL/HIGH vulnerabilities
- TenantGuard active in production — cross-tenant queries fail-closed
- RBAC enforced on all gRPC endpoints via method permission
  interceptor
- No K8s manifest can deploy with `AUTH_ENABLED=false` (OPA + CI)
- All Starlark runtimes have consistent step limits, script size
  limits, and static validation

## 7. Non-Goals

- Full penetration test (separate engagement)
- SOC 2 / compliance certification (future PRD)
- Frontend security audit (no frontend at time of audit)
- Performance benchmarking of TenantGuard / RBAC interceptor

## 8. Dependencies

- None for Wave 1 (all self-contained)
- Wave 2.1-2.2 depends on agreement on the permission map per
  service (requires product input on role-endpoint mappings).
  Start stakeholder discussions during Wave 1 to avoid blocking.
  Effort estimate: ~1 week for 19 services
- Wave 3.3 depends on service mesh or certificate infrastructure

## 9. Risks

| Risk | Mitigation |
|------|-----------|
| TenantGuard breaks tests missing scope | Latent bugs — fix with proper scoping |
| AUTH_ENABLED=true breaks local dev | Dev uses `.env` with explicit `false` |
| RBAC interceptor rejects valid requests | Start in audit mode, then enforce |
| Forecasting timeout 30s to 10s breaks forecasts | Monitor; 10s is 2x saga default |
