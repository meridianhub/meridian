# PRD-012: Test Coverage to 80%

## Objective

Raise Meridian's overall test coverage from **69.5% to 80%+** by systematically covering untested unhappy paths, error handling, and money-critical code across all services. This is not a coverage-percentage exercise — each service gets tests that expose real bugs and harden production behavior.

## Current State (2026-03-18)

| Service | Current % | Lines to 80% | Priority | Rationale |
|---------|-----------|-------------|----------|-----------|
| financial-gateway | 23% | 758 | P0 (in progress) | Money flows through here. PR #1757 addresses this. |
| audit-worker | 59% | 171 | P1 | Audit trail integrity — regulatory requirement |
| financial-accounting | 60% | 713 | P1 | Double-entry correctness, ledger balances |
| internal-account | 60% | 700 | P1 | Internal bank accounts, balance management |
| position-keeping | 62% | 1,152 | P2 | Position lifecycle, quality ladder |
| current-account | 66% | 866 | P2 | Customer-facing accounts |
| market-information | 67% | 542 | P2 | Market data feeds, pricing |
| operational-gateway | 67% | 391 | P2 | Operational command routing |
| payment-order | 68% | 753 | P2 | Payment orchestration sagas |
| forecasting | 69% | 198 | P3 | Forecast calculations |
| reconciliation | 69% | 536 | P2 | Estimate-vs-actual matching |
| shared/platform | 70% | 638 | P2 | Shared infrastructure (DB, events, auth) |
| control-plane | 72% | 703 | P3 | Manifest management, tenant config |
| party | 72% | 345 | P3 | Party/customer management |
| tenant | 73% | 232 | P3 | Tenant lifecycle |
| event-router | 74% | 79 | P3 | Event routing (small gap) |
| reference-data | 76% | 326 | P3 | Reference data management |
| shared/pkg | 78% | 175 | P3 | Shared packages (close to 80%) |
| api-gateway | 78% | 63 | P3 | API gateway (very close) |
| identity | 79% | 17 | P3 | Identity service (1 file away) |
| mcp-server | 82% | 0 | Done | Already above 80% |
| frontend | 82% | 0 | Done | Already above 80% |
| shared/domain | 95% | 0 | Done | Already above 80% |

**Total lines needed to reach 80% overall: ~9,398 additional lines covered**

## Approach

### Principles

1. **Unhappy paths first** — every service gets its error paths, edge cases, and failure modes tested before happy-path coverage padding
2. **Fix what you find** — when tests expose bugs, fix the production code in the same PR
3. **Money-critical services first** — financial-gateway, financial-accounting, internal-account, payment-order get priority
4. **Pure functions before integration** — table-driven unit tests on validation, calculation, and parsing logic give the highest coverage per effort
5. **One PR per service** — each service gets its own PR for reviewability

### What NOT to test

- Generated code (`.pb.go`, `_grpc.pb.go`, `.pb.validate.go`) — already excluded in codecov.yml
- `cmd/main.go` startup wiring — caught immediately on deploy
- Observability (metrics/tracing) — low blast radius
- Simple getters/setters with no logic

### Codecov Config Changes

Tighten enforcement to prevent regression:

```yaml
coverage:
  status:
    project:
      default:
        target: 75%        # raise from 50%
        threshold: 2%
        informational: false  # enforce, not informational
    patch:
      default:
        target: 70%        # raise from 60%
        informational: false  # enforce
```

## Financial-Gateway Follow-Up Issues

These were discovered during PR #1757 and need separate resolution:

### 1. Platform fee not propagated to saga response

**Problem**: `starlark.go:319` hardcodes `platform_fee_minor_units: int64(0)`. The proto `DispatchPaymentResponse` has no platform fee field, and the gRPC service drops `result.PlatformFeeAmount` when mapping to the proto response.

**Fix**: Add `platform_fee_minor_units` field to `DispatchPaymentResponse` proto, propagate through gRPC service, read in Starlark handler.

**Impact**: Every saga that uses the dispatch response for fee accounting records zero platform fee.

### 2. REFUNDED and DISPUTED webhooks acknowledged but not processed

**Problem**: `webhook_handler.go` returns 200 OK for refund and dispute events but publishes no domain event. The ledger never learns about chargebacks or refunds received via webhook.

**Fix**: Define `PaymentRefundedEvent` and `PaymentDisputedEvent` domain events, publish to outbox on these webhook types. Requires product decision on downstream consumers.

**Impact**: Chargebacks and Stripe-initiated refunds are silently swallowed.

### 3. CancelPayment RPC unimplemented but referenced by saga compensation

**Problem**: `financial_gateway.cancel_payment` Starlark handler calls `CancelPayment` gRPC which returns `Unimplemented`. Every saga compensation for `dispatch_payment` fails.

**Fix**: Implement `CancelPayment` RPC or change compensation strategy to use Stripe's cancel API directly.

**Impact**: Saga rollbacks for failed payment workflows cannot compensate the dispatch step.

### 4. Circuit breaker is global across all tenants

**Problem**: One `gobreaker` instance serves all tenants. If one tenant's control-plane manifest is consistently failing, the breaker trips for all tenants.

**Fix**: Per-tenant circuit breakers (keyed by tenant ID in a sync.Map), or accept the risk with monitoring.

**Impact**: Single-tenant outage can cascade to platform-wide payment unavailability.

## Success Criteria

- Overall coverage >= 80% (from 69.5%)
- No service below 70% (currently 6 services below)
- Codecov patch target enforced at 70% (non-informational)
- All money-critical services (financial-gateway, financial-accounting, internal-account, payment-order) >= 80%
- Financial-gateway follow-up issues resolved

## Phasing

### Phase 1: Money-Critical Services (P0-P1)
- financial-gateway follow-up (proto change, webhook events, cancel RPC)
- financial-accounting (double-entry, journal posting, balance queries)
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
