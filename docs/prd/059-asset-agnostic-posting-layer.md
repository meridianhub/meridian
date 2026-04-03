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

  Key constraint: payment-order remains currency-only because its current payment
  rail integrations only support ISO 4217. Do not change payment-order's currency
  validation.
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

### 4. payment-order (NOT affected - currency-only due to current payment rail constraints)

Current payment rail integrations (Stripe, bank transfers) only carry ISO 4217
currencies. The fiat restriction here is a correct business constraint for the
current integrations, not a missed refactor.

## Proto Surface Area

4 proto files import `google.type.Money`, containing 22 Money fields total:

| Proto File | Money Fields | Count |
|------------|-------------|-------|
| `financial_accounting/v1/financial_accounting.proto` | `LedgerPosting.posting_amount`, `CaptureLedgerPostingRequest.posting_amount`, `ListLedgerPostingsRequest.currency` (filter) | 3 |
| `events/v1/financial_accounting_events.proto` | `FinancialBookingLogPostedEvent.total_debits/total_credits`, `LedgerPostingCapturedEvent.posting_amount`, `LedgerPostingAmendedEvent.previous_amount/new_amount`, `BalanceValidationFailedEvent.total_debits/total_credits/variance` | 8 |
| `events/v1/current_account_events.proto` | `AccountClosedEvent.closing_balance`, `TransactionInitiatedEvent.transaction_amount`, `TransactionCompletedEvent.new_balance/new_available_balance`, `OverdraftConfiguredEvent.overdraft_limit/previous_limit`, `OverdraftLimitExceededEvent.attempted_amount/current_balance/overdraft_limit/shortage` | 10 |
| `common/v1/types.proto` | `MoneyAmount.amount` (wrapper type) | 1 |

**Note:** `ListLedgerPostingsRequest.currency` (field 7) has validation
`max_len: 3, pattern: "^[A-Z]{0,3}$"` which cannot match instrument codes like
"TONNE_CO2E" or "GPU_HOUR". This filter must be renamed and revalidated.

The replacement type already exists and is proven:

```protobuf
// quantity/v1/quantity.proto - InstrumentAmount
message InstrumentAmount {
  string amount = 1;            // Decimal as string (arbitrary precision)
  string instrument_code = 2;   // "USD", "KWH", "TONNE_CO2E", "GPU_HOUR"
  int32 version = 3;            // Schema evolution
  // ... attributes, temporal bounds, source
}
```

5 proto files already use `InstrumentAmount` (position_keeping, internal_account,
current_account service proto, position_keeping_events). The migration pattern is
established.

## Wire-Format Compatibility

Changing a field's message type at the same field number is a **wire-incompatible**
change. `google.type.Money` field 2 = `units` (int64, varint) while
`InstrumentAmount` field 2 = `instrument_code` (string, length-delimited). An old
client would misparse the bytes.

**Decision: Clean swap.** Acceptable because:

- No external API consumers exist yet
- No SLAs or production deployments
- The outbox can be drained before deployment (see Deployment section)
- Doing this after GA would require maintaining parallel fields for years

**CEL validation rewrite required.** Current proto-level CEL rules reference
Money's `units`/`nanos` structure:

```cel
this.units > 0 || (this.units == 0 && this.nanos > 0)
```

These must be rewritten for InstrumentAmount. Note: `double(this.amount) > 0.0`
is the documented Protovalidate pattern but loses precision for very large
decimals. For arbitrary-precision safety, use regex-based string validation:

```cel
this.amount.matches("^[1-9][0-9]*(\\.[0-9]+)?$") || this.amount.matches("^0\\.[0-9]*[1-9][0-9]*$")
```

## Solution

Replace `google.type.Money` with `InstrumentAmount` in all affected protos, and
replace `ParseCurrency()` with `InstrumentResolver.Resolve()` in all Go
conversion paths.

### Phase 1: Proto migration (foundation for all other phases)

1. **financial_accounting.proto:** Replace `LedgerPosting.posting_amount` (field 4)
   and `CaptureLedgerPostingRequest.posting_amount` (field 3) from
   `google.type.Money` to `meridian.quantity.v1.InstrumentAmount`.

2. **CEL validation:** Rewrite positive-amount CEL rules for InstrumentAmount.
   Without this, deploying the proto change creates a window with zero
   positive-amount enforcement.

3. **ListLedgerPostingsRequest:** Rename `currency` (field 7) to `instrument_code`,
   expand validation to `max_len: 32` and preserve optionality (proto3 scalar
   default is empty string, so use `pattern: "^$|^[A-Z][A-Z0-9_]*$"` or
   `ignore_empty` semantics to allow unset/empty as "no filter").

4. **financial_accounting_events.proto:** Migrate all 8 Money fields to
   InstrumentAmount.

5. **Run `buf generate api/proto`** to regenerate Go files.

### Phase 2: financial-accounting Go layer (demo blocker)

1. **Go adapters (inbound):** Replace `fromProtoMoney()` to use
   `InstrumentResolver.Resolve()` instead of `ParseCurrency()`. Since
   `fromProtoMoney()` is a standalone function (not a method on a service
   struct), either change its signature to accept a resolver parameter, or
   convert it to a method on the service/adapter struct. The resolver is already
   wired into financial-accounting at `server.go:193` (`instrumentResolver`
   field) with `WithInstrumentResolver()` option at line 212 - no new plumbing
   needed, just thread the resolver through the call chain. Also add
   `InstrumentResolver` to `PostingService` struct for `buildDepositPostings()`.

2. **Go adapters (outbound):** Replace `toProtoMoney()` (`adapters.go:79`) to
   output `InstrumentAmount` instead of `google.type.Money`. This reverse path
   must match the proto change - if we accept non-fiat in, we must output
   non-fiat too.

3. **Posting service:** Replace `buildDepositPostings()` to use the resolver
   for instrument validation. **Critical:** `decimalFromCents()` at
   `posting_service.go:78` hardcodes division by 100, and `DepositEvent.AmountCents`
   is cent-based. After removing ParseCurrency, KWH amounts would silently get
   divided by 100 - data corruption. **Recommended fix:** Change `DepositEvent`
   to carry a decimal string amount + instrument code (aligning with
   `InstrumentAmount`'s string-based precision model). This is preferred over
   using `InstrumentResolver` to compute a divisor because it eliminates the
   cent-based assumption entirely rather than parameterizing it.

4. **Seed script:** Remove the KWH skip workaround in `cmd/seed-dev/cmd/fixtures.go`.

5. **Frontend/gateway:** Verify posting amount rendering handles InstrumentAmount
   format (string amount + instrument_code vs units/nanos + currency_code).

### Phase 3: position-keeping

1. **balance_mapper.go:** Replace `ToDomainMoney()` and
   `ToDomainMoneyFromInstrumentAmount()` to follow the same pattern as
   `ToDomainAssetFromInstrumentAmount()` (`balance_mapper.go:220`), which
   already uses `InstrumentResolver.Resolve()` correctly. The asset-aware
   path is proven - the Money functions just need to match it.

2. **No new plumbing needed.** The resolver is already wired into
   position-keeping via `app/container.go` (`initializeInstrumentResolver()`)
   and passed via `service.WithInstrumentResolver()`.

### Phase 4: current-account

1. **Caller migration:** `NewMoneyFromInstrument()` (line 93, CURRENCY-only) is
   the deprecated path. `NewAmountFromInstrument()` (line 73) already delegates
   to `sharedamount.NewFromInstrument` which supports ALL dimensions. Phase 4 is
   migrating callers from the old function to the existing dimension-agnostic one,
   not removing a safety gate or creating new capability.

2. Verify all callers handle non-monetary dimensions correctly (account creation,
   balance queries).

3. **current_account_events.proto:** Migrate all 10 Money fields to InstrumentAmount.
   The type system should support any asset; business rules constrain which assets
   allow overdrafts at a higher layer (application logic, not proto types).
   - Account position fields: `closing_balance`, `transaction_amount`,
     `new_balance`, `new_available_balance`
   - Overdraft fields: `overdraft_limit`, `previous_limit`,
     `attempted_amount`, `current_balance`, `shortage`

### Phase 5: Cleanup

1. Mark `ParseCurrency()` and `CurrencyToInstrument()` as deprecated with
   clear migration guidance pointing to `InstrumentResolver`.

2. Remove unused `Currency` constants from service domain packages once no
   production code references them (test fixtures may retain them).

3. Assess whether `MoneyAmount` wrapper in `common/v1/types.proto` is still
   needed after the migration.

## Deployment

**Pre-deployment checklist:**

1. **Drain the outbox.** Before deploying proto changes, ensure all pending events
   in the outbox table are published. Unprocessed events serialized with the old
   proto schema (google.type.Money wire format) would be silently corrupt after
   deployment. This is operationally trivial in pre-production.

2. **Deploy Phases 1-3 atomically.** Do not deploy Phase 1 proto changes
   without the corresponding Phase 2 Go adapter changes in the same release.
   A half-deployed state where the proto expects InstrumentAmount but the Go code
   still calls `ParseCurrency()` would fail on every request. A state where FA
   accepts KWH postings but PK can't track positions and CA can't show balances
   is worse than the current consistent fiat-only rejection. Treat Phases 1-3
   as an atomic epic.

3. **Verify Kafka consumers.** Confirm no downstream consumers depend on the
   `google.type.Money` wire format for financial accounting events.

## Non-Goals

- **Changing payment-order** - fiat-only is correct for payment rails
- **Removing google.type.Money from common/v1** - `MoneyAmount` wrapper may
  still be used by external-facing APIs where ISO 4217 is appropriate
- **Adding new instrument types to reference data** - instruments are tenant
  configuration, not platform changes
- **Changing the InstrumentAmount proto** - it is already production-ready
- **InstrumentAmount.version migration story** - version=1 is correct for this
  migration; version evolution is a separate concern

## Testing Strategy

### Unit Tests

- Adapter conversion accepts "GBP", "KWH", "TONNE_CO2E", "GPU_HOUR" and rejects
  empty/invalid codes
- Posting capture roundtrip for non-fiat instruments
- Balance mapper converts non-fiat amounts correctly
- CEL validation rules reject zero/negative InstrumentAmount values

### Integration Tests

- End-to-end: create booking log with KWH instrument, capture postings, verify
  they appear in the booking log detail response
- **Precision round-trip:** Create KWH posting with 3dp precision (e.g.,
  "123.456"), verify the amount survives the full lifecycle without truncation
  to 2dp. The InstrumentResolver's precision must be respected through the
  entire posting pipeline.
- Seed script deposits KWH successfully (no skip workaround)
- Demo environment shows KWH transactions on the ledger detail page
- Non-fiat postings round-trip correctly: create, store, query, display
- Event deserialization: verify downstream consumers can deserialize non-fiat
  InstrumentAmount fields from `FinancialBookingLogPostedEvent`

### Regression Tests

- All existing GBP/USD posting tests continue to pass unchanged
- Payment-order tests remain currency-only
- ListLedgerPostings filtering works for both "GBP" and "KWH" instrument codes

## Complexity Assessment

| Phase | Estimate | Dev Parallelizable | Deploy |
|-------|----------|-------------------|--------|
| Phase 1: Proto migration | 3 points | No (foundation) | Atomic with Phases 2-3 |
| Phase 2: financial-accounting Go | 3 points | No (depends on Phase 1) | Atomic with Phases 1, 3 |
| Phase 3: position-keeping | 2 points | Yes (dev in parallel after Phase 1) | Atomic with Phases 1-2 |
| Phase 4: current-account + events | 3 points | Yes (dev in parallel after Phase 1) | Can follow separately |
| Phase 5: Cleanup | 2 points | Yes | Can follow separately |
| **Total** | **13 points** | | |

**Development** can parallelize: Phases 2-3 can be developed concurrently after
Phase 1 lands. **Deployment** is atomic for Phases 1-3: a half-migrated state
where FA accepts KWH but PK can't track positions is worse than the current
consistent fiat-only rejection. Phases 4-5 can deploy independently.

## Success Criteria

1. KWH ledger detail page shows postings on the demo environment
2. Seed script creates KWH deposits without skipping
3. `ParseCurrency()` has zero callers in production code paths
4. Non-fiat postings round-trip correctly (create, store, query, display)
5. Non-fiat precision is preserved (KWH 3dp not truncated to 2dp)
6. All existing fiat tests pass without modification
7. payment-order remains currency-only (no regression)
8. Proto-level CEL validation enforces positive amounts for InstrumentAmount

## Resolved Questions

1. **Proto backward compatibility:** Clean swap acceptable. No external consumers,
   pre-production system, outbox drain eliminates wire-format corruption risk.

2. **Event consumers:** Events are proto-serialized in the outbox
   (`shared/platform/events/outbox_pgx.go:359`). Drain outbox before deploying.
   No external Kafka consumers depend on the Money wire format.

3. **current_account_events overdraft fields:** All 10 fields migrate to
   InstrumentAmount. The type system should support any asset; business rules
   constrain which assets allow overdrafts at the application layer, not at the
   proto type level. Restricting at the proto level creates a future constraint
   (e.g., energy pre-purchase agreements are effectively KWH overdrafts).

## De-Risk Findings

The following reduce implementation risk compared to the original estimate:

1. **InstrumentResolver already wired into FA** (`server.go:193`) and PK
   (`app/container.go`). No new dependency injection needed - just use the
   existing resolver in the adapter functions.

2. **PK already has the asset-aware pattern.** `ToDomainAssetFromInstrumentAmount()`
   at `balance_mapper.go:220` uses `InstrumentResolver.Resolve()` correctly.
   The Money functions just need to follow the same pattern.

3. **CA already has the dimension-agnostic path.** `NewAmountFromInstrument()`
   (line 73) supports all dimensions. Phase 4 is caller migration, not
   capability creation.
