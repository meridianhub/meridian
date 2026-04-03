---
name: prd-asset-agnostic-posting-layer
description: >
  Replace hardcoded ISO 4217 currency validation in the posting layer with
  InstrumentResolver, enabling non-fiat assets (KWH, TONNE_CO2E, GPU_HOUR) to
  flow through financial-accounting, position-keeping, and current-account services.
triggers:
  - Working on non-fiat asset support in ledger postings
  - Fixing currency validation failures for energy, carbon, or compute instruments
  - Replacing google.type.Money with InstrumentAmount in service proto contracts
  - Removing ParseCurrency calls from production code paths
  - Working on the Universal Asset System migration in any service
instructions: |
  This PRD addresses missed refactors where hardcoded ISO 4217 currency validation
  blocks non-fiat assets from flowing through the posting layer. The replacement type
  (InstrumentAmount in quantity/v1) and resolver (InstrumentResolver in shared/pkg/refdata)
  already exist - this work wires them through the remaining services.

  Key constraint: payment-order is intentionally currency-only (payment rails carry
  ISO 4217 only). Do not change payment-order's currency validation.
---

# PRD-059: Asset-Agnostic Posting Layer

## Problem Statement

Meridian's core value proposition is asset-agnostic infrastructure - currencies,
energy, carbon credits, and compute hours flow through the same ledger with the
same financial rigor. The proto schemas, domain types, and database layer already
support this. But three services still have hardcoded ISO 4217 validation in their
posting/amount conversion paths, silently rejecting non-fiat assets at runtime.

**Observed symptom:** The demo UI at `/ledger` shows both GBP and KWH booking logs,
but KWH detail pages are empty - postings were rejected by `ParseCurrency()` which
only accepts 7 fiat codes (GBP, USD, EUR, JPY, CHF, CAD, AUD).

The seed script already documents this limitation:

```go
// NOTE: financial-accounting currently only supports ISO-4217 currencies,
// so KWH deposits fail at the posting layer with InvalidArgument
```

This is not a design gap - it is a missed refactor. The `InstrumentResolver`
(shared/pkg/refdata) and `InstrumentAmount` proto type (quantity/v1) were built
specifically to replace the fiat-only path. Three services still use the old path.

## Affected Services

### 1. financial-accounting (BLOCKING)

| Location | Issue |
|----------|-------|
| `service/adapters.go:60` | `fromProtoMoney()` calls `ParseCurrency()` - rejects non-fiat |
| `service/posting_service.go:142` | `buildDepositPostings()` calls `ParseCurrency()` - rejects non-fiat |
| `domain/currency.go` | Re-exports `ParseCurrency` and `CurrencyToInstrument` - fiat-only |

**Proto dependency:** `LedgerPosting.posting_amount` uses `google.type.Money` which
assumes ISO 4217 via its `currency_code` field. The booking log itself already uses
`base_instrument_code` (string) - the posting is the gap.

### 2. position-keeping (BLOCKING)

| Location | Issue |
|----------|-------|
| `adapters/balance_mapper.go:132` | `ToDomainMoney()` calls `ParseCurrency()` - rejects non-fiat |
| `adapters/balance_mapper.go:208` | `ToDomainMoneyFromInstrumentAmount()` calls `ParseCurrency()` - rejects non-fiat |

### 3. current-account (BLOCKING)

| Location | Issue |
|----------|-------|
| `domain/quantity.go:94` | `NewMoneyFromInstrument()` explicitly rejects non-CURRENCY dimension |

### 4. payment-order (NOT affected - intentionally currency-only)

Payment rails only carry ISO 4217 currencies. The fiat restriction here is a
correct business constraint, not a missed refactor.

## Proto-Level Dependency

13 production files import `google.type.Money`. The primary proto definition
that needs updating:

```protobuf
// financial_accounting.proto - LedgerPosting
google.type.Money posting_amount = 4;  // <-- fiat-only by design
```

The replacement already exists:

```protobuf
// quantity/v1/quantity.proto - InstrumentAmount
message InstrumentAmount {
  string amount = 1;            // Decimal as string (arbitrary precision)
  string instrument_code = 2;   // "USD", "KWH", "TONNE_CO2E", "GPU_HOUR"
  int32 version = 3;            // Schema evolution
  // ... attributes, temporal bounds, source
}
```

## Solution

Replace `google.type.Money` with `InstrumentAmount` in the posting proto, and
replace `ParseCurrency()` with `InstrumentResolver.Resolve()` in all Go
conversion paths.

### Phase 1: financial-accounting (highest impact, demo blocker)

1. **Proto:** Replace `LedgerPosting.posting_amount` from `google.type.Money` to
   `meridian.quantity.v1.InstrumentAmount`. Update the CapturePosting request
   message similarly.

2. **Go adapters:** Replace `fromProtoMoney()` to use `InstrumentResolver.Resolve()`
   instead of `ParseCurrency()`. The resolver returns dimension, precision, and
   rounding mode for any registered instrument.

3. **Posting service:** Replace `buildDepositPostings()` to use the resolver
   for currency validation.

4. **Wire resolver:** Inject `InstrumentResolver` into the service layer
   (it is already available in the dependency graph via refdata package).

5. **Seed script:** Remove the KWH skip workaround in `cmd/seed-dev/cmd/fixtures.go`.

6. **Events proto:** Update `FinancialBookingLogPostedEvent` fields
   (`total_debits`, `total_credits`) from `google.type.Money` to `InstrumentAmount`.

### Phase 2: position-keeping

1. **balance_mapper.go:** Replace `ToDomainMoney()` and
   `ToDomainMoneyFromInstrumentAmount()` to use `InstrumentResolver` instead
   of `ParseCurrency()`.

2. Wire `InstrumentResolver` into the adapter layer.

### Phase 3: current-account

1. **domain/quantity.go:** Remove the explicit `CURRENCY` dimension check in
   `NewMoneyFromInstrument()`. Allow any dimension registered in reference data.

2. Verify all callers handle non-monetary dimensions correctly (account creation,
   balance queries).

### Phase 4: Cleanup

1. Mark `ParseCurrency()` and `CurrencyToInstrument()` as deprecated with
   clear migration guidance pointing to `InstrumentResolver`.

2. Remove unused `Currency` constants from service domain packages once no
   production code references them (test fixtures may retain them).

3. Update `google.type.Money` usage in any remaining event protos.

## Non-Goals

- **Changing payment-order** - fiat-only is correct for payment rails
- **Removing google.type.Money from common/v1** - `MoneyAmount` wrapper may
  still be used by external-facing APIs where ISO 4217 is appropriate
- **Adding new instrument types to reference data** - instruments are tenant
  configuration, not platform changes
- **Changing the InstrumentAmount proto** - it is already production-ready

## Testing Strategy

### Unit Tests
- `fromProtoMoney()` (or its replacement) accepts "GBP", "KWH", "TONNE_CO2E",
  "GPU_HOUR" and rejects empty/invalid codes
- Posting capture roundtrip for non-fiat instruments
- Balance mapper converts non-fiat amounts correctly

### Integration Tests
- End-to-end: create booking log with KWH instrument, capture postings, verify
  they appear in the booking log detail response
- Seed script deposits KWH successfully (no skip workaround)
- Demo environment shows KWH transactions on the ledger detail page

### Regression Tests
- All existing GBP/USD posting tests continue to pass unchanged
- Payment-order tests remain currency-only

## Complexity Assessment

| Phase | Estimate | Parallelizable |
|-------|----------|----------------|
| Phase 1: financial-accounting | 3 points | No (proto change is foundation) |
| Phase 2: position-keeping | 2 points | Yes (after Phase 1 proto) |
| Phase 3: current-account | 2 points | Yes (after Phase 1 proto) |
| Phase 4: Cleanup | 1 point | Yes |
| **Total** | **8 points** | Phases 2-4 parallelize after Phase 1 |

Critical path: Phase 1 (proto + financial-accounting) then Phases 2-4 in parallel.

## Success Criteria

1. KWH ledger detail page shows postings on the demo environment
2. Seed script creates KWH deposits without skipping
3. `ParseCurrency()` has zero callers in production code paths
4. All existing fiat tests pass without modification
5. payment-order remains currency-only (no regression)

## Open Questions

1. **Proto backward compatibility:** Changing `LedgerPosting.posting_amount` from
   `google.type.Money` to `InstrumentAmount` is a breaking proto change. Should we
   add a new field and deprecate the old one, or is a clean swap acceptable given
   no external consumers? (Recommendation: clean swap - no external consumers exist
   yet, and maintaining two fields adds permanent complexity.)

2. **Event consumers:** Do any downstream consumers of
   `FinancialBookingLogPostedEvent` depend on the `google.type.Money` format for
   `total_debits`/`total_credits`? Need to audit Kafka consumers.
