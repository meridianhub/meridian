# Frontend vs Backend RPC Audit

**Date:** 2026-02-23
**Task:** 026-operations-console.59

## Methodology

Each frontend page was read and all API calls identified. For each API call, the corresponding
proto file and Go service handler were verified for:

1. Proto RPC definition with gateway HTTP annotation
2. Handler implementation (fully implemented, not returning `codes.Unimplemented`)
3. Shape match between frontend expected types and proto response

---

## Page-by-Page Audit

### 1. Dashboard (`frontend/src/pages/dashboard/index.tsx`)

| RPC | Proto | Handler | Gateway | Shape | Status |
|-----|-------|---------|---------|-------|--------|
| `PaymentOrderService.ListPaymentOrders` | `payment_order.proto` ✅ | ✅ | `/v1/payment-orders` ✅ | ✅ | **OK** |
| `FinancialAccountingService.ListFinancialBookingLogs` | `financial_accounting.proto` ✅ | ✅ | `/v1/booking-logs` ✅ | ✅ | **OK** |
| `FinancialAccountingService.ListLedgerPostings` | `financial_accounting.proto` ✅ | ✅ | `/v1/postings` ✅ | ✅ | **OK** |

**Result: No gaps. Dashboard is fully wired.**

---

### 2. Accounts (`frontend/src/pages/accounts/index.tsx`)

| RPC | Proto | Handler | Gateway | Shape | Status |
|-----|-------|---------|---------|-------|--------|
| `CurrentAccountService.ListCurrentAccounts` | **MISSING** ❌ | **MISSING** ❌ | **MISSING** ❌ | N/A | **GAP** |

**Details:**

- The frontend calls `POST /api/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts`
- The `current_account.proto` does **not** define a `ListCurrentAccounts` RPC
- The Go service handler has no `ListCurrentAccounts` method
- The proto has `RetrieveCurrentAccount` (single account by ID) and `ListByOrganization`
  (domain-only, no gRPC surface)

**Expected response shape (from `frontend/src/pages/accounts/types.ts`):**

```typescript
interface ListCurrentAccountsResponse {
  accounts?: CurrentAccount[]
  nextPageToken?: string
}
interface CurrentAccount {
  accountId: string
  iban: string
  status: string
  baseCurrency: string
  createdAt: { seconds: bigint | number; nanos?: number } | null
}
```

**Gap:** Missing `ListCurrentAccounts` RPC — requires proto change, Go handler, gateway
registration, and frontend has request filters for `status` and `iban`.

---

### 3. Payments (`frontend/src/pages/payments/index.tsx`)

| RPC | Proto | Handler | Gateway | Shape | Status |
|-----|-------|---------|---------|-------|--------|
| `PaymentOrderService.ListPaymentOrders` | `payment_order.proto` ✅ | ✅ | `/v1/payment-orders` ✅ | ✅ | **OK** |
| `PaymentOrderService.RetrievePaymentOrder` | `payment_order.proto` ✅ | ✅ | `/v1/payment-orders/{id}` ✅ | ✅ | **OK** |
| `PaymentOrderService.InitiatePaymentOrder` | `payment_order.proto` ✅ | ✅ | `/v1/payment-orders` (POST) ✅ | ✅ | **OK** |
| `PaymentOrderService.CancelPaymentOrder` | `payment_order.proto` ✅ | ✅ | `/v1/payment-orders/{id}/cancel` ✅ | ✅ | **OK** |
| `PaymentOrderService.ReversePaymentOrder` | `payment_order.proto` ✅ | ✅ | `/v1/payment-orders/{id}/reverse` ✅ | ✅ | **OK** |

**Result: No gaps. Payments page is fully wired.**

---

### 4. Positions (`frontend/src/pages/positions/index.tsx` and `detail.tsx`)

| RPC | Proto | Handler | Gateway | Shape | Status |
|-----|-------|---------|---------|-------|--------|
| `PositionKeepingService.ListFinancialPositionLogs` | `position_keeping.proto` ✅ | ✅ | `/v1/position-logs` ✅ | ✅ | **OK** |
| `PositionKeepingService.RetrieveFinancialPositionLog` | `position_keeping.proto` ✅ | ✅ | `/v1/position-logs/{log_id}` ✅ | ✅ | **OK** |

**Shape note:** The frontend's `FinancialPositionLog.statusTracking.currentStatus` is a string,
but the proto uses the `TransactionStatus` enum. The frontend handles this with a
numeric-to-string mapping at lines 14-24 of `positions/index.tsx`. This is a minor shape
mismatch — the frontend works correctly but a type-safe adapter would be cleaner.

**Result: No blocking gaps. Minor shape adaptation in frontend.**

---

### 5. Ledger (`frontend/src/pages/ledger/index.tsx`)

| RPC | Proto | Handler | Gateway | Shape | Status |
|-----|-------|---------|---------|-------|--------|
| `FinancialAccountingService.ListFinancialBookingLogs` | `financial_accounting.proto` ✅ | ✅ | `/v1/booking-logs` ✅ | ✅ | **OK** |
| `FinancialAccountingService.RetrieveFinancialBookingLog` | `financial_accounting.proto` ✅ | ✅ | `/v1/booking-logs/{id}` ✅ | ✅ | **OK** |
| `FinancialAccountingService.ListLedgerPostings` | `financial_accounting.proto` ✅ | ✅ | `/v1/postings` ✅ | ✅ | **OK** |

**Result: No gaps. Ledger page is fully wired.**

---

### 6. Reconciliation (`frontend/src/pages/reconciliation/index.tsx` and `detail.tsx`)

#### Index page (list of runs)

| RPC | Proto | Handler | Gateway | Shape | Status |
|-----|-------|---------|---------|-------|--------|
| `GET /api/v1/reconciliation/runs` | **MISSING** ❌ | **MISSING** ❌ | **MISSING** ❌ | N/A | **GAP** |

**Details:**

- Frontend calls `GET /api/v1/reconciliation/runs` to list all settlement runs
- The `reconciliation.proto` does **not** define a `ListSettlementRuns` (or equivalent) RPC
- Existing RPCs: `InitiateAccountReconciliation`, `ExecuteAccountReconciliation`,
  `RetrieveAccountReconciliation` (single by run_id), `ControlAccountReconciliation`,
  `ListReconciliationResults` (variances within a run)
- The persistence layer has a `SettlementRunRepository.List()` method — domain capability
  exists, but no gRPC surface

#### Detail page endpoints

| Endpoint | Proto RPC | Handler | Gateway | Status |
|----------|-----------|---------|---------|--------|
| `GET .../runs/{runId}` | `RetrieveAccountReconciliation` ✅ | ✅ | `/v1/reconciliation/runs/{run_id}` ✅ | **OK** |
| `GET .../runs/{runId}/variances` | **MISSING** ❌ | **MISSING** ❌ | **MISSING** ❌ | **GAP** |
| `GET .../runs/{runId}/disputes` | **MISSING** ❌ | **MISSING** ❌ | **MISSING** ❌ | **GAP** |
| `PATCH .../runs/{runId}/disputes/{id}` | **MISSING** ❌ | **MISSING** ❌ | **MISSING** ❌ | **GAP** |
| `GET .../runs/{runId}/assertions` | **MISSING** ❌ | **MISSING** ❌ | **MISSING** ❌ | **GAP** |
| `POST .../runs/{runId}/assertions` | **MISSING** ❌ | **MISSING** ❌ | **MISSING** ❌ | **GAP** |

**Note on existing proto:** `ListReconciliationResults` returns variances but via
`GET /v1/reconciliation/runs/{run_id}/results`. The frontend calls
`/api/v1/reconciliation/runs/{runId}/variances` — the path differs. Additionally
`ControlDispute` and `InitiateDispute` exist but the frontend uses a REST-style update
pattern (`PATCH .../disputes/{id}`) not the gRPC pattern.

**Gap:** Missing `ListAccountReconciliations` (list all runs) RPC and missing REST-accessible
list/CRUD endpoints for variances, disputes, and assertions per run.

---

### 7. Parties (`frontend/src/pages/parties/index.tsx`)

**Skipped** — covered by task 58.

---

### 8. Audit Log (`frontend/src/pages/audit/index.tsx`)

| RPC | Proto | Handler | Gateway | Shape | Status |
|-----|-------|---------|---------|-------|--------|
| `AuditService.ListAuditEntries` | **MISSING** ❌ | **MISSING** ❌ | **MISSING** ❌ | N/A | **GAP** |

**Details:**

- Frontend calls `POST /meridian.audit.v1.AuditService/ListAuditEntries?...`
- The `audit/v1/audit_events.proto` defines only the `AuditEvent` message (for Kafka
  publishing) — there is **no `AuditService`** defined in any proto
- No Go handler implements `ListAuditEntries`
- No gateway registration exists
- The frontend gracefully handles 501/503 responses (returns empty list) — currently the
  page silently shows no data

**Gap:** Missing `AuditService` with `ListAuditEntries` RPC. Requires new proto service,
Go handler, gateway registration, and a queryable audit storage layer.

---

### 9. Other Pages (Quick Scan)

| Page | API Calls | Status |
|------|-----------|--------|
| `forecasting/index.tsx` | `clients.forecasting.computeForwardCurve` | Typed gRPC client — needs proto/service check |
| `internal-accounts/index.tsx` | `clients.internalAccount.listInternalAccounts` | Typed gRPC client — appears wired |
| `mappings/index.tsx` | `clients.mapping.listMappings` | Typed gRPC client — appears wired |
| `market-data/index.tsx` | `clients.marketInformation.listDataSets` | Typed gRPC client — appears wired |
| `starlark/index.tsx` | Saga registry client | Typed gRPC client — appears wired |
| `tenants/index.tsx` | Tenant client | Typed gRPC client — appears wired |
| `reference-data/` | Reference data clients | Typed gRPC client — appears wired |

---

## Summary of Gaps

### Total gaps: 6

| # | Page | Missing Endpoint | Priority | Complexity |
|---|------|-----------------|----------|------------|
| 1 | Accounts | `ListCurrentAccounts` RPC in `CurrentAccountService` | High | 5 pts |
| 2 | Reconciliation | `ListAccountReconciliations` RPC (list all runs) | High | 5 pts |
| 3 | Reconciliation | Variances list (`GET .../runs/{id}/variances`) | High | 3 pts |
| 4 | Reconciliation | Disputes list/CRUD endpoints | Medium | 5 pts |
| 5 | Reconciliation | Balance Assertions list/CRUD endpoints | Medium | 5 pts |
| 6 | Audit Log | `AuditService.ListAuditEntries` (entire service missing) | Medium | 8 pts |

---

## Recommendations

1. **`ListCurrentAccounts`** (GAP 1) is the highest-value gap — the Accounts page is completely
   non-functional without it. Add the RPC to `current_account.proto`, implement the Go handler,
   and register the gateway endpoint.

2. **Reconciliation list** (GAP 2) makes the Reconciliation index page show no data. Add
   `ListAccountReconciliations` RPC to `reconciliation.proto` backed by the existing
   `SettlementRunRepository.List()`.

3. **Reconciliation detail sub-resources** (GAPS 3-5): Variances, disputes, and assertions are
   already supported in the domain layer but lack HTTP-accessible gRPC gateway endpoints. The
   existing `ListReconciliationResults` RPC covers variances but at a different URL path than
   the frontend expects. Align paths or add gateway annotations.

4. **`AuditService.ListAuditEntries`** (GAP 6) requires the most work — a new proto service and
   a queryable read model. The frontend already handles 501 gracefully, so this can be lower
   priority.
