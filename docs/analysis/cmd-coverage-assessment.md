# cmd/ Package Test Coverage Assessment

**Date:** 2026-03-18
**Task:** test-coverage-80 #18
**Scope:** All `services/*/cmd/` packages (~8,183 lines across 18 services)

---

## Executive Summary

The `cmd/` packages contain two fundamentally different kinds of code that require different treatment:

1. **Wiring code** (`main()`, `run()`, client factory functions) — couples all dependencies together, requires a running database + gRPC stack to test meaningfully. Integration tests exercise this code; unit tests cannot do so without excessive mocking.

2. **Testable utility code** — pure functions for configuration parsing, HTTP route setup, health server logic, and factory functions with branching logic. These are independently testable, and several services already test them with good results.

**Recommendation: Add targeted unit tests for the testable patterns; do not exclude `cmd/` from codecov.** Excluding it entirely would hide real coverage gaps. The `audit-worker` demonstrates a 43.2% coverage floor is achievable through unit tests alone.

---

## Current Coverage Baseline

Measured with `go test -coverprofile ./services/*/cmd/...`:

| Service | Coverage | Build Status | Test File(s) |
|---------|----------|--------------|--------------|
| audit-worker | **43.2%** | ✅ | `main_test.go`, `main_cockroach_test.go` |
| tenant | **14.7%** | ✅ | `main_test.go` |
| event-router | **10.9%** | ✅ | `main_test.go` |
| reconciliation | **8.9%** | ✅ | `main_test.go` |
| party | **3.3%** | ✅ | `main_test.go` |
| position-keeping | **1.1%** | ✅ | `main_test.go`, `idempotency_test.go` |
| api-gateway | 0.0% | ✅ (no tests) | — |
| current-account | 0.0% | ✅ (no unit tests) | `party_wrapper_test.go` (wrapper only) |
| financial-accounting | 0.0% | ✅ (no tests) | — |
| financial-gateway | 0.0% | ❌ BUILD FAIL | `main_test.go` (blocked by missing proto symbols) |
| forecasting | 0.0% | ✅ (no tests) | — |
| internal-account | 0.0% | ✅ (no tests) | — |
| market-information | 0.0% | ✅ (no tests) | — |
| mcp-server | 0.0% | ✅ (no tests) | — |
| operational-gateway | 0.0% | ✅ (no tests) | — |
| payment-order | 0.0% | ❌ BUILD FAIL | `gateway_factory_test.go` (blocked by missing proto symbols) |
| reference-data | 0.0% | ✅ (no tests) | — |
| control-plane | 0.0% | ❌ BUILD FAIL | — |

**Build failures** in `control-plane`, `financial-gateway`, and `payment-order` are due to undefined proto symbols (`PaymentRefundedEvent`, `PaymentDisputedEvent`) — an unrelated issue blocking coverage measurement for those packages.

---

## Code Categorization

Each function type is classified by its testing value and feasibility.

### Category A: Pure Wiring — Exclude from unit test targets

These functions couple all dependencies together and cannot be tested without a running stack.

| Function | Pattern | All Services |
|----------|---------|-------------|
| `main()` | Calls `bootstrap.RunWithRetry` | All 18 services |
| `run()` | Inits DB, tracer, gRPC server, registers services, starts servers, handles shutdown | All 18 services |
| `createCurrentAccountClient()` | Reads env vars, calls service client constructor | Current-account, payment-order |
| `createFinancialAccountingClient()` | Same pattern | Multiple services |
| `create*Client()` | Multiple variants of the same env-reads-then-constructs pattern | All multi-service services |

**Why unit tests provide no value here:** These functions compose known-good components (bootstrap, gRPC, GORM). The risk is in configuration, not in the composition. End-to-end tests provide the real coverage.

**Why exclusion from codecov is also wrong:** The testable functions in the same package (below) have genuine value. Excluding the whole `cmd/` directory removes visibility into those gaps.

### Category B: Configuration Parsing — High-value unit test targets

Pure functions that read environment variables and validate/transform them. Fast, deterministic, no infrastructure.

| Function | Service(s) | Currently Tested? |
|----------|-----------|-------------------|
| `parseLogLevel(levelStr string) slog.Level` | **15/18 services** | `party`, `reconciliation` only |
| `getPort() string` | audit-worker | ✅ audit-worker |
| `getDBConnectionString() (string, error)` | audit-worker | ✅ audit-worker |
| `getAuditSchema() (string, error)` | audit-worker | ✅ audit-worker |
| `loadWorkerConfig() (WorkerConfig, error)` | tenant | ✅ tenant |
| `WorkerConfig.Validate() error` | tenant | ✅ tenant |

**Key gap:** `parseLogLevel` is duplicated across 15 services (nearly identical switch statement: `debug/warn/warning/error/default→info`). Only 2 services test it. The remaining 13 are identical untested copies.

**Recommendation:** Apply the `audit-worker` pattern to all services that have configuration parsing functions. Total effort: ~5 tests per service × 13 services = ~65 test cases, all trivial.

### Category C: HTTP/Health Setup — High-value unit test targets

Functions that configure HTTP servers or health check endpoints. Testable with `net/http/httptest`.

| Function | Service(s) | Currently Tested? |
|----------|-----------|-------------------|
| `setupRoutes(mux *http.ServeMux)` | audit-worker | ✅ audit-worker |
| `createServer(port string) *http.Server` | audit-worker | ✅ audit-worker |
| `healthHandler()` | forecasting, reference-data | ❌ not tested |
| `newHealthServer()` | position-keeping, reconciliation | position-keeping ✅ |
| `healthServer.Check()` | position-keeping, reconciliation | position-keeping ✅ |
| `healthServer.Watch()` | position-keeping, reconciliation | position-keeping ✅ |

**Recommendation:** The `healthHandler` functions in `forecasting` and `reference-data` return `http.HandlerFunc` values and are testable in isolation with `httptest.NewRecorder`.

### Category D: Factory Functions with Branching Logic — High-value unit test targets

Functions with switch statements or business logic determining which component to create. The branching is the risk.

| Function | Service | Currently Tested? | What's Tested |
|----------|---------|-------------------|---------------|
| `createPaymentGateway()` | payment-order | ✅ (build fails) | All providers: mock, stripe, financial-gateway, invalid |
| `wireScheduler()` | reconciliation | ✅ partial | nil Redis, invalid DB URL, valid config (integration skip) |
| `buildEventStreamComponents()` | api-gateway | ❌ | Kafka vs outbox source; Redis vs local fanout |
| `wireBFFSSO()` | api-gateway | ❌ | SSO disabled (no URL), missing JWT key, success path |
| `buildOAuthConfig()` | mcp-server | ❌ | OAuth enabled vs disabled |
| `buildBearerValidator()` | mcp-server | ❌ | Passthrough vs real validator |
| `buildValuationComponents()` | reconciliation | ❌ | gRPC client creation fallback |

**Recommendation:** These branching factory functions carry the most risk and provide the most unit-test value per effort. `buildEventStreamComponents` and `wireBFFSSO` in api-gateway are particularly important — they handle Redis availability fallback and JWT key validation, which are production security decisions.

### Category E: Simple Adapter Delegation — Low unit test value

Private structs in `cmd/` that delegate to injected dependencies. No branching logic.

| Type | Service | What it does |
|------|---------|-------------|
| `currentAccountClientAdapter` | position-keeping | Delegates `RetrieveCurrentAccount` to gRPC client |
| `internalAccountClientAdapter` | position-keeping | Delegates `RetrieveInternalAccount` to gRPC client |
| `referenceDataClientAdapter` | financial-accounting | Delegates `GetInstrument` to gRPC client |
| `manifestHistoryAdapter`, `referenceDataAdapter`, etc. | mcp-server/wire.go | Proto adapter delegation |
| `localPaymentOrderClient` | payment-order | Delegates to local service |
| `simpleHealthServer` | payment-order | Returns `SERVING` unconditionally |
| `noopEventPublisher` | financial-accounting, reconciliation | Silently discards events |
| `gobreakerStateToMetricState()` | current-account, internal-account | 3-case enum conversion |
| `makeCircuitBreakerCallback()` | current-account, internal-account | Returns a logging closure |

**Assessment:** These adapters contain no logic. Testing them would be testing that Go delegation works. The `noopEventPublisher` tests in `reconciliation` pass via interface assertion only — appropriate.

---

## Services with No Testable Logic

Some services have `cmd/` packages that are pure wiring with no factory functions or utility code extracted:

- **operational-gateway** (245 lines): `main()` + `run()` + `parseLogLevel()`. Only `parseLogLevel` is testable.
- **internal-account** (414 lines): same pattern plus `makeCircuitBreakerCallback` / `gobreakerStateToMetricState` (low value).
- **market-information** (346 lines): `main()` + `run()` + `parseLogLevel()`.
- **forecasting** (314 lines): `main()` + `run()` + `parseLogLevel()` + `healthHandler()` (testable).

---

## mcp-server/cmd/wire.go (390 lines)

This file is unusual — it registers MCP tools, resources, and prompts by wiring gRPC clients to the MCP server. It contains 23 functions, but they are all registration functions that:
1. Call the injected gRPC client
2. Marshal/unmarshal proto messages
3. Register the result with the MCP server

There is no branching logic. The correctness of this file is verified by integration tests that call the MCP tools. Adding unit tests would require mocking all 6 gRPC clients, producing test-only infrastructure that mirrors the real integration.

**Recommendation:** Exclude `mcp-server/cmd/wire.go` from coverage targets via codecov `ignore` list.

---

## codecov.yml Recommendations

### No changes needed to coverage targets

The current configuration (75% project target, 70% patch target) is correct for `services/` and `shared/`. The `cmd/` packages are included by the `services/` path glob and this is appropriate — the testable utility functions in `cmd/` should count toward coverage.

### Recommended: Add one ignore entry

```yaml
ignore:
  - "**/*.pb.go"
  - "**/*.pb.validate.go"
  - "**/*_grpc.pb.go"
  - "**/mocks/**"
  - "utilities/**"
  - "frontend/src/api/gen/**"
  - "services/mcp-server/cmd/wire.go"   # ← ADD: pure MCP registration, 23 adapter functions, no logic
```

This is the only file that is pure boilerplate with no extractable logic. All other cmd/ files contain at least `parseLogLevel` which is worth covering.

---

## Prioritized Recommendations

### Priority 1 — Fix build failures (prerequisite)

The `control-plane/cmd`, `financial-gateway/cmd`, and `payment-order/cmd` packages fail to build. Until resolved, coverage cannot be measured and tests cannot run. This blocks the gateway_factory_test.go tests in payment-order that are already written.

**Action:** Fix the undefined proto symbol errors (`PaymentRefundedEvent`, `PaymentDisputedEvent`) — likely requires regenerating proto files or updating imports.

### Priority 2 — `parseLogLevel` across all services (low effort, high coverage delta)

The function is identical across 15 services. Add a test table covering `debug`, `warn`, `warning`, `error`, default/unknown. Modeled on `services/reconciliation/cmd/main_test.go:TestParseLogLevel`.

**Expected coverage impact per service:** +3-5% (the function is 6-9 lines in a package of 200-700 lines).

**Services without `parseLogLevel` tests:** api-gateway, audit-worker (already has `getPort` etc. but not `parseLogLevel`), current-account, financial-accounting, forecasting, internal-account, market-information, mcp-server, operational-gateway, position-keeping, reference-data, tenant.

### Priority 3 — Factory functions in api-gateway (security-relevant)

`buildEventStreamComponents` and `wireBFFSSO` make security-relevant decisions:
- `wireBFFSSO`: returns `ErrJWTSigningKeyRequired` when neither key source is provided outside local dev — this should be tested
- `buildEventStreamComponents`: returns `ErrRedisURLRequired` when Redis fan-out is enabled but no client provided

These error paths are production safety guards. Unit tests provide confidence that the guards actually trigger.

### Priority 4 — Scheduler wiring in reconciliation (already partially done)

`TestWireScheduler_ValidConfig_ReturnsScheduler` is skipped in CI (requires integration DB). The nil-Redis and invalid-URL paths are already covered. No action needed.

### Priority 5 — healthHandler in forecasting and reference-data (fast win)

Simple HTTP handler, testable with `httptest`:

```go
func TestHealthHandler_Returns200(t *testing.T) {
    h := healthHandler(nil)
    req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
    w := httptest.NewRecorder()
    h(w, req)
    assert.Equal(t, http.StatusOK, w.Code)
}
```

---

## Summary Table: What to Test vs What to Exclude

| Code Pattern | Action | Rationale |
|-------------|--------|-----------|
| `main()` | Leave uncovered | Untestable without full stack; E2E covers it |
| `run()` | Leave uncovered | Same as main(); wiring only |
| `parseLogLevel()` | **Add tests** (15 services) | Pure function, 0 dependencies, already tested in 2 services |
| `getPort()`, `getDBConnectionString()` | **Already tested** in audit-worker | — |
| `loadWorkerConfig()`, `WorkerConfig.Validate()` | **Already tested** in tenant | — |
| `setupRoutes()`, `createServer()` | **Already tested** in audit-worker | — |
| `healthHandler()` | **Add tests** (forecasting, ref-data) | Pure httptest, 5 lines each |
| `healthServer.Check/Watch()` | **Already tested** in position-keeping | — |
| `createPaymentGateway()` | **Already tested** (blocked by build fail) | Unblock by fixing proto |
| `buildEventStreamComponents()` | **Add tests** for error paths | Security-relevant guards |
| `wireBFFSSO()` | **Add tests** for error paths | Security-relevant guards |
| `buildOAuthConfig()` | **Add tests** | Env-driven branching |
| Adapter delegation functions | Leave uncovered | No logic, pure delegation |
| `gobreakerStateToMetricState()` | Leave uncovered | 3-case enum, low risk |
| `noopEventPublisher` | **Already tested** (interface assertion) | — |
| `mcp-server/cmd/wire.go` | **Add codecov ignore** | 390 lines of pure registration boilerplate |
| `createCurrentAccountClient()` etc. | Leave uncovered | Env reads + constructor call, no branching |

---

## Total Line Count by Category

Estimated breakdown of ~8,183 cmd/ lines:

| Category | Approx Lines | Testable? | Currently Covered? |
|----------|-------------|-----------|-------------------|
| main() + run() orchestration | ~4,500 | Minimally | Via E2E only |
| parseLogLevel (15 copies) | ~180 | Yes | ~2 services |
| Config parsing functions | ~400 | Yes | audit-worker, tenant |
| HTTP/health setup | ~350 | Yes | audit-worker, position-keeping |
| Factory functions with branching | ~600 | Yes | payment-order (build fails), reconciliation partial |
| Adapter delegation | ~800 | No | Interface assertions only |
| Proto adapter registration (wire.go) | ~390 | No | Recommend ignore |
| Misc (init, error vars, type defs) | ~963 | N/A | — |
