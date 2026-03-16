# PRD-047: Codebase Security Audit — Findings and Remediation Plan

## Status: Draft
## Author: Six Thinking Hats Security Audit Team
## Date: 2026-03-16

---

## 1. Executive Summary

A comprehensive security audit of the Meridian codebase was conducted using a four-person expert panel (Security Engineer, Platform Engineer, DevOps/SRE, Sandbox Specialist) through the Six Thinking Hats methodology.

**Central Finding**: The security *architecture* is ahead of *enforcement*. The codebase contains well-designed, tested security controls that aren't wired up in production. Most critical fixes activate existing code rather than building new capabilities.

**Findings**: 3 CRITICAL, 5 HIGH, 6 MEDIUM, 2 LOW vulnerabilities across authentication, authorization, tenant isolation, sandbox security, CI/CD, and infrastructure.

**Remediation**: A 3-wave plan where Wave 1 (~25 lines of changes, ~4 hours) closes 5+ findings with zero code risk.

---

## 2. Scope

### In Scope
- Go application code (`services/`, `shared/`, `cmd/`)
- gRPC API security (authentication, authorization, interceptors)
- Multi-tenant data isolation (schema-based, GORM, pgx)
- Starlark/CEL sandbox security (saga, forecasting, valuation runtimes)
- CI/CD pipeline security (GitHub Actions, scanning, supply chain)
- Container and Kubernetes security posture
- Demo deployment security
- Secrets management

### Out of Scope
- Penetration testing of running environments
- Third-party dependency CVE triage (covered by existing Trivy/govulncheck)
- Frontend/UI security (no frontend code at time of audit)
- Business logic correctness

---

## 3. Findings

### 3.1 Controls That Exist But Aren't Enforced

This is the central theme of the audit. The team built security infrastructure but didn't complete the wiring:

| Control | Status | Gap |
|---------|--------|-----|
| TenantGuard (GORM plugin) | Implemented, tested (240 lines of tests) | Never registered in `bootstrap.NewDatabase()` |
| RBAC (7-role system) | Fully implemented with resource-type permissions | Only 1 of 11 services calls any RBAC check |
| Saga step limit | Constant defined (`MaxStepsPerExecution = 1M`) | Never applied via `SetMaxExecutionSteps()` |
| Memory threshold | Constant defined (`MemoryWarningThreshold = 10MB`) | Dead code — referenced nowhere |
| OPA Gatekeeper | Deployed, blocks `LOCAL_DEV_MODE=true` | No rule for `AUTH_ENABLED=false` |
| Security scanners | 7 tools running | Build pipeline scans are advisory-only (`continue-on-error: true`) |

### 3.2 Validated Vulnerabilities

#### CRITICAL-1: RBAC Not Enforced — Any Authenticated User Is Super-Admin

**Description**: The RBAC framework (`shared/platform/auth/rbac.go`) defines 7 roles with a resource-type permission matrix, delegation hierarchy, and tested helper functions. However, only 1 of 11 services (`reconciliation/service/finalizer.go:295`) calls any RBAC check. All other service handlers accept any authenticated request regardless of role.

**Evidence**:
- `ExecuteDeposit` (`grpc_deposit_endpoints.go:28`) — No role check
- `InitiatePaymentOrder` (`grpc_initiate.go:26`) — No role check
- `ControlCurrentAccount` (`grpc_control_endpoints.go:34`) — Imports `auth` but only uses `GetUserIDFromContext()` for audit logging, never checks roles
- `InitiateWithdrawal`, `InitiateLien`, `ExecuteLien` — No role checks

**Impact**: An `auditor`-role JWT (intended read-only) can create accounts, initiate payments, freeze accounts, execute deposits, and create liens. The 7-role hierarchy provides zero runtime protection.

**Exploitability**: Trivial — any valid JWT grants full access.

#### CRITICAL-2: TenantGuard Not Registered — Silent Cross-Tenant Data Access

**Description**: `TenantGuard` (`shared/platform/db/tenant_guard.go`) is a GORM plugin that rejects queries without tenant scope. It is fully implemented and tested but never registered in production. `bootstrap.NewDatabase()` at `bootstrap.go:77` creates GORM connections without calling `db.Use(NewTenantGuard())`.

**Attack path**:
1. Developer writes `gormDB.Find(&results)` without `WithGormTenantTransaction`
2. `SkipDefaultTransaction: true` means no auto-transaction wraps the call
3. `SET LOCAL search_path` is never called
4. Query runs against default `search_path` (public)
5. Cross-tenant data leakage with NO runtime error

**Impact**: Cross-tenant data leakage or corruption. One tenant could see another's account balances, transactions, or positions.

**Note**: The pgx path (`shared/platform/db/db.go`) has no equivalent guard at all.

#### CRITICAL-3: AUTH_ENABLED Defaults to `false` in API Gateway and Position-Keeping

**Description**: Different components use different defaults:

| Component | Default | File |
|-----------|---------|------|
| `bootstrap.DefaultAuthConfig()` | **true** | `shared/platform/bootstrap/auth.go:53` |
| API Gateway `LoadAuthConfig()` | **false** | `services/api-gateway/config.go:221` |
| Position-keeping `loadAuthConfig()` | **false** | `services/position-keeping/app/config.go:220` |

**Attack path**: Missing env var in deployment = unauthenticated API surface. `TenantExtractionInterceptor` trusts `x-tenant-id` headers without JWT validation, enabling tenant impersonation.

**Impact**: Complete authentication bypass for the API gateway (the external entry point) and position-keeping service.

#### HIGH-1: Saga Runtime Step Limit Defined But Not Enforced

**Description**: `MaxStepsPerExecution = 1_000_000` is defined at `shared/pkg/saga/runtime.go:29` but `SetMaxExecutionSteps()` is never called on the saga thread. The valuation runtime at `starlark_runtime.go:127` does call it, proving the pattern works.

**Impact**: CPU exhaustion via tenant saga scripts. The 5-second timeout is the only backstop, but 5 seconds of unbounded CPU per request enables amplification attacks.

#### HIGH-2: Forecasting Runtime Has Zero Safety Controls

**Description**: The forecasting Starlark runtime (`services/forecasting/starlark/runner.go`) has:
- No script size limit (saga has 64KB)
- No step limit (not even an unenforced constant)
- No static validation (saga has `ValidateSagaScript` with blocked function checks)
- 30-second timeout (6x longer than saga/valuation)

**Impact**: 30 seconds of unbounded CPU and memory per invocation. A script allocating large data structures could OOM the process.

#### HIGH-3: AUTH_ENABLED=false in All K8s Service ConfigMaps

**Description**: All 11 service K8s configmaps in `services/*/k8s/configmap.yaml` set `AUTH_ENABLED: "false"`. The production overlay does not override this. No OPA Gatekeeper policy blocks `AUTH_ENABLED=false`.

**Impact**: First Kubernetes deployment using these manifests runs all services unauthenticated.

#### HIGH-4: Memory Exhaustion in All Starlark Runtimes

**Description**: `MemoryWarningThreshold = 10 * 1024 * 1024` is defined in `runtime.go:27` but never referenced anywhere. No memory limits exist in any of the three runtimes. A script allocating large lists/dicts can OOM the process.

**Impact**: Process death affecting all co-hosted services and tenants.

#### HIGH-5: GitHub Actions Supply Chain Risk

**Description**: `appleboy/ssh-action@v1.2.5` and `appleboy/scp-action@v1.0.0` handle the SSH private key for demo deployment. They are community-maintained (single developer), pinned to minor versions (not SHAs).

**Impact**: Compromised action release = root shell on demo server + ability to inject malicious code into built images.

#### MEDIUM-1: No Rate Limiting on Inbound Financial API Endpoints

**Description**: No rate limiting middleware in the gRPC interceptor chain for any service. Rate limiting only exists for outbound provider connections and MCP sessions.

**Impact**: API abuse, database exhaustion, Kafka flooding. Each payment order call triggers multiple downstream operations (amplification).

#### MEDIUM-2: No Inter-Service Authentication

**Description**: Services connect using `grpc.WithTransportCredentials(insecure.NewCredentials())`. No mTLS, no service mesh, no service-to-service JWT.

**Impact**: Any pod with network access can call any internal gRPC endpoint. Combined with disabled auth, this enables full cross-tenant access.

#### MEDIUM-3: Security Scanners Advisory-Only in Build Pipeline

**Description**: Trivy in `build.yml` uses `continue-on-error: true`. Gosec uses `|| true`. The dedicated `security.yml` workflow does fail on CRITICAL/HIGH, but may not be a required status check.

**Impact**: Vulnerable code can merge if `security.yml` isn't a required check.

#### MEDIUM-4: Demo Server Hardening Gaps

**Description**: SSH as `root`, no container resource limits in docker-compose, `sslmode=disable` on internal Postgres, no evidence of firewall/fail2ban.

**Impact**: Limited by network topology (Caddy fronts external access), but fragile.

#### MEDIUM-5: Outbox Worker Tenant Scope Ambiguity

**Description**: `PostgresOutboxRepository` and `Worker` have zero tenant awareness. No `tenant_id` column in `event_outbox`, no `tenant_id` in Kafka headers. Worker queries without tenant scope.

**Impact**: Potential event loss or single-tenant bias in event processing.

#### MEDIUM-6: Unified Binary gRPC Port Unauthenticated

**Description**: `cmd/meridian/main.go:253` builds gRPC server without `.WithAuthInterceptor()`. The HTTP gateway has separate auth config, but gRPC port 50051 is accessible to all containers on the Docker network.

**Impact**: Any container on the network gets full unauthenticated gRPC access.

#### LOW-1: Deprecated `invoke_handler` Shim Bypasses Schema Validation

**Description**: The `invoke_handler` backward-compat shim accepts handler names as strings with no parameter type coercion or schema validation, unlike typed service modules.

**Impact**: Incorrect parameter types could cause crashes or unexpected behavior.

#### LOW-2: X-Forwarded-For Spoofing in Security Logs

**Description**: `extractClientIP` trusts `x-forwarded-for` header. Direct connections could spoof IP in audit logs.

**Impact**: Corrupted forensic data, not access escalation.

---

## 4. Existing Strengths

The audit identified significant security maturity that remediation should build on:

- **Container security**: Distroless base images, non-root execution, static binaries, multi-stage builds across all Dockerfiles
- **K8s pod security**: `runAsNonRoot`, dropped capabilities, read-only filesystem, seccomp profiles, resource limits, health probes
- **Auth interceptor design**: Anti-subdomain-hopping check with Prometheus metrics, JWKS support, key rotation ready
- **Tenant isolation primitives**: `pq.QuoteIdentifier()` used consistently (369 occurrences), `SET LOCAL search_path`, regex validation, strongly-typed TenantID
- **Token handling**: `crypto/rand`, SHA256 storage, constant-time comparison
- **Password security**: bcrypt cost 12, policy enforcement, history checking
- **Control plane RBAC**: Fail-closed allowlist pattern — proven and ready to generalize
- **Security scanning**: 7 tools (govulncheck, gosec, Trivy, gitleaks, CodeQL, dependency-review, SBOM) with daily scheduled runs
- **Saga circular detection**: Three-phase (draft, activation, runtime) with depth limits
- **Starlark builtin whitelisting**: Safe-by-default approach — dangerous functions don't exist in runtime
- **OPA Gatekeeper**: Deployed with `LOCAL_DEV_MODE` constraint — template ready for `AUTH_ENABLED`

---

## 5. Remediation Plan

### Wave 1: "This Week" — Activate Existing Controls (~4 hours, zero code risk)

| # | Task | Files | Changes | Closes |
|---|------|-------|---------|--------|
| 1.1 | Add `thread.SetMaxExecutionSteps(MaxStepsPerExecution)` to saga runtime after thread setup | `shared/pkg/saga/runtime.go` | +1 line | HIGH-1 |
| 1.2 | Add `thread.SetMaxExecutionSteps(1_000_000)` to forecasting runtime after thread creation | `services/forecasting/starlark/runner.go` | +1 line | HIGH-2 (partial) |
| 1.3 | Register TenantGuard in bootstrap: `db.Use(NewTenantGuard())` after `gorm.Open()` | `shared/platform/bootstrap/bootstrap.go` | +2 lines | CRITICAL-2 |
| 1.4 | Change AUTH_ENABLED default from `false` to `true` in API Gateway and position-keeping | `services/api-gateway/config.go`, `services/position-keeping/app/config.go` | 2 lines | CRITICAL-3 |
| 1.5 | Set `AUTH_ENABLED: "true"` in all 11 K8s service configmaps | `services/*/k8s/configmap.yaml` | 11 lines | HIGH-3 |
| 1.6 | Remove `continue-on-error: true` from Trivy and `\|\| true` from gosec in build pipeline | `.github/workflows/build.yml` | -2 lines | MEDIUM-3 |
| 1.7 | SHA-pin `appleboy/ssh-action` and `appleboy/scp-action` to commit SHAs | `.github/workflows/deploy-demo.yml` | 2 lines | HIGH-5 |
| 1.8 | Add container resource limits to demo docker-compose (memory: 1G, cpus: 1.0) | `deploy/demo/docker-compose.yml` | +8 lines | MEDIUM-4 (partial) |
| 1.9 | Delete dead `MemoryWarningThreshold` constant (misleading — creates false confidence) | `shared/pkg/saga/runtime.go` | -1 line | HIGH-4 (honesty) |
| 1.10 | Add OPA Gatekeeper constraint for `AUTH_ENABLED=false` (clone `LOCAL_DEV_MODE` pattern) | New YAML in `deployments/k8s/` | ~15 lines | HIGH-3 (permanent) |

### Wave 2: "This Sprint" — Interceptor-Based RBAC + Sandbox Hardening (~1 week)

| # | Task | Effort | Closes |
|---|------|--------|--------|
| 2.1 | Create `NewMethodRBACInterceptor` — fail-closed declarative permission mapping per RPC method (follows control plane `ManifestRBACUnaryInterceptor` pattern) | ~100 lines + maps | CRITICAL-1 |
| 2.2 | Define permission maps for all 11 services, wire into GrpcServerBuilder | Config per service | CRITICAL-1 |
| 2.3 | Migrate hand-rolled interceptor chains (position-keeping) to GrpcServerBuilder | Refactor | Consistency |
| 2.4 | Add script size limit (64KB) and static validation to forecasting runtime (reuse saga `ValidateSagaScript` patterns) | ~50 lines | HIGH-2 (complete) |
| 2.5 | Reduce forecasting `DefaultTimeout` from 30s to 10s | 1 line | HIGH-2 |
| 2.6 | Replicate NetworkPolicy from audit-worker to all services | YAML per service | MEDIUM-2 (partial) |
| 2.7 | Add CI "Security Defaults Gate" job validating AUTH_ENABLED, LOCAL_DEV_MODE, non-root Dockerfiles, SHA-pinned actions | ~30 lines YAML | Regression prevention |
| 2.8 | Add rate limiting middleware to gRPC interceptor chain | New interceptor | MEDIUM-1 |
| 2.9 | Make `GrpcServerBuilder.Build()` fail-closed on auth — require explicit `.WithoutAuth()` opt-out | ~30 lines | MEDIUM-6 |

### Wave 3: "Next Month" — Defense in Depth (~2-3 weeks)

| # | Task | Effort | Closes |
|---|------|--------|--------|
| 3.1 | Extract `shared/platform/sandbox/` package with `HardenThread()`, `ValidateScript()` — single security control point for all Starlark runtimes | Refactor | HIGH-1, HIGH-2, HIGH-4 (architectural) |
| 3.2 | Implement Go `runtime.MemStats` monitoring in sandbox package | Medium | HIGH-4 |
| 3.3 | Service-to-service auth via existing `OAuth2Client` (`shared/platform/auth/oauth.go`) | Medium | MEDIUM-2 |
| 3.4 | Add `tenant_id` column to `event_outbox` table + Kafka headers | Medium | MEDIUM-5 |
| 3.5 | Create non-root deploy user on demo droplet, update SSH config | Small | MEDIUM-4 |
| 3.6 | Move JWT signing key from env var to file mount / Docker secret | Small | Key leak prevention |
| 3.7 | Add security test suites for forecasting/valuation runtimes (model on saga's `runtime_security_test.go`) | Medium | Test parity |
| 3.8 | Add authorization hook in `wrapHandler` (`schema/service_modules.go`) for saga handler ACLs | Medium | Sandbox RBAC |
| 3.9 | Remove deprecated `invoke_handler` shim | Small | LOW-1 |
| 3.10 | Add cosign image signing to build pipeline (keyless via Sigstore OIDC) | Small | Supply chain |
| 3.11 | Add end-to-end cross-tenant isolation integration tests (attempt Tenant B access with Tenant A context — must fail) | Medium | Regression prevention |
| 3.12 | Audit raw SQL paths (`shared/platform/db/db.go`) — consider tenant guard wrapper for `QueryContext`/`ExecContext` | Medium | CRITICAL-2 (complete) |

---

## 6. Success Criteria

- All CRITICAL findings resolved (Wave 1 + Wave 2)
- All HIGH findings resolved (Wave 1 + Wave 2)
- Security scanning blocks PR merges on CRITICAL/HIGH vulnerabilities
- TenantGuard active in production — cross-tenant queries fail-closed
- RBAC enforced on all gRPC endpoints via method permission interceptor
- No K8s manifest can deploy with `AUTH_ENABLED=false` (OPA + CI gate)
- All three Starlark runtimes have consistent step limits, script size limits, and static validation

## 7. Non-Goals

- Full penetration test (separate engagement)
- SOC 2 / compliance certification (future PRD)
- Frontend security audit (no frontend at time of audit)
- Performance impact analysis of TenantGuard / RBAC interceptor (should be negligible but should be benchmarked)

## 8. Dependencies

- None for Wave 1 (all self-contained)
- Wave 2.1-2.2 depends on agreement on the permission map per service (requires product input on which roles access which endpoints)
- Wave 3.3 depends on service mesh or certificate infrastructure decisions

## 9. Risks

| Risk | Mitigation |
|------|-----------|
| TenantGuard activation breaks tests with missing tenant scope | These are latent bugs — fix by adding proper scoping (which matches production behavior) |
| AUTH_ENABLED=true breaks local dev workflow | Local dev uses `.env` with explicit `AUTH_ENABLED=false` — unaffected |
| Method RBAC interceptor rejects legitimate requests | Start with audit mode (log violations, don't block), then switch to enforce after validation |
| Forecasting timeout reduction from 30s to 10s breaks legitimate forecasts | Monitor for timeout increases after deployment; 10s is still 2x the saga/valuation default |
