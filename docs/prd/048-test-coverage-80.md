# PRD-048: Test Coverage to 80%

## Objective

Raise Meridian's overall test coverage from 69.5% to 80%+ by systematically
covering untested unhappy paths, error handling, and money-critical code
across all services. This is not a coverage-percentage exercise — each
service gets tests that expose real bugs and harden production behavior.

## Current State (2026-03-18)

<!-- markdownlint-disable MD013 -->

| Service | Coverage | Lines to 80% | Priority | Rationale |
|---------|----------|-------------|----------|-----------|
| financial-gateway | 23% | 758 | P0 | Money flows. PR #1757 started. |
| audit-worker | 59% | 171 | P1 | Audit trail integrity |
| financial-accounting | 60% | 713 | P1 | Double-entry correctness |
| internal-account | 60% | 700 | P1 | Balance management |
| position-keeping | 62% | 1,152 | P2 | Quality ladder |
| current-account | 66% | 866 | P2 | Customer accounts |
| market-information | 67% | 542 | P2 | Market data feeds |
| operational-gateway | 67% | 391 | P2 | Command routing |
| payment-order | 68% | 753 | P2 | Saga orchestration |
| forecasting | 69% | 198 | P3 | Forecast calculations |
| reconciliation | 69% | 536 | P2 | Estimate-vs-actual |
| shared/platform | 70% | 638 | P2 | Shared infrastructure |
| control-plane | 72% | 703 | P3 | Manifest management |
| party | 72% | 345 | P3 | Party management |
| tenant | 73% | 232 | P3 | Tenant lifecycle |
| event-router | 74% | 79 | P3 | Event routing |
| reference-data | 76% | 326 | P3 | Reference data |
| shared/pkg | 78% | 175 | P3 | Shared packages |
| api-gateway | 78% | 63 | P3 | API gateway |
| identity | 79% | 17 | P3 | Identity service |
| mcp-server | 82% | 0 | Done | Above 80% |
| frontend | 82% | 0 | Done | Above 80% |
| shared/domain | 95% | 0 | Done | Above 80% |

<!-- markdownlint-enable MD013 -->

### Total effort

~9,398 additional lines need coverage to reach 80% overall.

## Approach

### Principles

1. **Unhappy paths first** — error paths, edge cases, and failure modes
   before happy-path padding
2. **Fix what you find** — when tests expose bugs, fix the production
   code in the same PR
3. **Money-critical services first** — financial-gateway,
   financial-accounting, internal-account, payment-order get priority
4. **Pure functions before integration** — table-driven unit tests on
   validation, calculation, and parsing logic
5. **One PR per service** — each service gets its own PR

### What NOT to test

- Generated code (`.pb.go`, `_grpc.pb.go`, `.pb.validate.go`)
- `cmd/main.go` startup wiring
- Observability (metrics/tracing)
- Simple getters/setters with no logic

### Codecov config changes

Tighten enforcement to prevent regression:

```yaml
coverage:
  status:
    project:
      default:
        target: 75%
        threshold: 2%
        informational: false
    patch:
      default:
        target: 70%
        informational: false
```

## Financial-Gateway Follow-Up Issues

Discovered during PR #1757 and need separate resolution.

### 1. Platform fee not propagated to saga response

`starlark.go:319` hardcodes `platform_fee_minor_units: int64(0)`.
The proto `DispatchPaymentResponse` has no platform fee field.
The gRPC service drops `result.PlatformFeeAmount` when mapping.

**Fix**: Add field to proto, propagate through gRPC, read in handler.
**Impact**: Saga fee accounting records zero platform fee.

### 2. REFUNDED and DISPUTED webhooks not processed

`webhook_handler.go` returns 200 OK for refund/dispute events but
publishes no domain event. Ledger never learns about chargebacks.

**Fix**: Define domain events, publish to outbox. Requires product
decision on downstream consumers.
**Impact**: Chargebacks and Stripe-initiated refunds silently swallowed.

### 3. CancelPayment RPC unimplemented (active production gap)

`financial_gateway.cancel_payment` Starlark handler calls
`CancelPayment` gRPC which returns `Unimplemented`. Every saga
compensation for `dispatch_payment` fails at runtime.

**Fix**: Implement `CancelPayment` RPC or change compensation strategy.
**Impact**: Saga rollbacks cannot compensate the dispatch step.
**Note**: This is an active production bug — consider a standalone
hotfix PR ahead of the broader coverage initiative.

### 4. Circuit breaker is global across all tenants

One `gobreaker` instance serves all tenants. If one tenant's
control-plane manifest is consistently failing, the breaker trips
for all tenants.

**Fix**: Per-tenant circuit breakers or accept risk with monitoring.
**Impact**: Single-tenant outage can cascade platform-wide.

## Success Criteria

- Overall coverage >= 80% (from 69.5%)
- No service below 70% (currently 6 services below)
- Codecov patch target enforced at 70% (non-informational)
- All money-critical services >= 80%
- Financial-gateway follow-up issues resolved

## Phasing

### Phase 1: Money-Critical Services (P0-P1)

- financial-gateway follow-up (proto, webhooks, cancel RPC)
- financial-accounting (double-entry, journal posting)
- internal-account (account lifecycle, balance management)
- audit-worker (audit trail completeness)

### Phase 2: Core Operations (P2)

- payment-order (saga orchestration, status transitions)
- position-keeping (quality ladder, temporal queries)
- current-account (customer account operations)
- reconciliation (matching logic, estimate-vs-actual)
- shared/platform (DB helpers, event outbox, auth)
- operational-gateway, market-information

### Phase 3: Close the Gap (P3)

- Services between 72-79% need small targeted additions
- Tighten codecov targets to final 80%/70% enforcement
- identity, api-gateway, shared/pkg (nearly there already)

### Phase 4: Enforcement

- Set codecov project target to 80% (enforced)
- Set codecov patch target to 75% (enforced)
- Add CI check that fails if any service drops below 70%
