# 037 - Multi-Asset Purity Audit Results

Audit date: 2026-03-06
Scope: All production Go files in `services/` and `shared/`
Excluded: `*_test.go`, `cmd/seed-*`, `utilities/`, `adapters/stripe/`

## Category 1: Hardcoded Currency Switch Statements (Critical)

These switch statements embed asset-specific logic that should be driven by Reference Data.

| File | Line | Description | Severity |
|------|------|-------------|----------|
| `shared/pkg/proto/mappers/currency.go` | 25-42 | Switch on currency codes to map to proto enum. Already marked `Deprecated`. | Critical |
| `services/position-keeping/adapters/balance_mapper.go` | 254-272 | `inferInstrumentProperties()` hardcodes dimension + precision per instrument code. Should use Reference Data. | Critical |
| `shared/pkg/bucketing/bucketing.go` | 41-77 | `dimensionRegistry` hardcodes instrument-to-dimension mapping for ~40 codes. Should be populated from Reference Data. | Critical |

## Category 2: Hardcoded Currency Registries (Critical)

Static registries that duplicate what Reference Data should provide.

| File | Line | Description | Severity |
|------|------|-------------|----------|
| `shared/domain/money/money.go` | 35-38 | `CurrencyGBP`, `CurrencyUSD`, `CurrencyEUR`, `CurrencyJPY` constants. Legacy package. | Critical |
| `shared/pkg/money/money.go` | 53-56 | Duplicate currency constants in newer package. | Critical |
| `shared/platform/quantity/currency/currency.go` | 34-63 | `InstrumentUSD`, `InstrumentEUR`, `InstrumentGBP`, `InstrumentJPY` plus registry map. | Critical |
| `services/financial-accounting/domain/currency.go` | 14-29 | Re-exports `CurrencyGBP`..`CurrencyAUD` from `shared/domain/money`. | High |
| `services/position-keeping/domain/quantity.go` | 100-110 | Re-exports `CurrencyGBP`..`CurrencyAUD` from `shared/domain/money`. | High |
| `services/current-account/domain/quantity.go` | 44-54 | Re-exports `CurrencyGBP`..`CurrencyAUD` from `shared/domain/money`. | High |

## Category 3: Hardcoded Physics Instrument Checks (High)

Hard-coded instrument-specific branching logic.

| File | Line | Description | Severity |
|------|------|-------------|----------|
| `shared/pkg/saga/handlers.go` | 239-241 | `IsPhysicsInstrument()` hardcodes `"KWH" \|\| "GAS"`. Should query instrument dimension from Reference Data. | High |

## Category 4: Hardcoded Precision Fallbacks (High)

Default precision values that assume currency (2 decimal places).

| File | Line | Description | Severity |
|------|------|-------------|----------|
| `services/internal-account/service/lien_service.go` | 709 | `const defaultPrecision = 2`. Falls back when Reference Data client is nil. | High |
| `services/financial-accounting/adapters/persistence/repository.go` | 319-336 | Backward-compat defaults: dimension="CURRENCY", precision=2 for empty DB rows. | Medium |
| `cmd/seed-demo/main.go` | 268 | `precision: 2` in seed data struct. | Low (seed) |

## Category 5: Service-Level Hardcoded Instrument Codes (Medium)

Instrument codes embedded in production service logic.

| File | Line | Description | Severity |
|------|------|-------------|----------|
| `services/current-account/service/lien_service.go` | 1077 | `const currentAccountInstrumentCode = "GBP"`. Intentional: CA is currency-only by business rule. | Medium (Allowlisted) |
| `services/current-account/service/client_interfaces.go` | 69 | Comment: "always pass instrument_code=GBP". Matches above. | Medium (Allowlisted) |
| `services/financial-accounting/client/starlark.go` | 80-178 | `ProducesInstruments: []string{"USD","EUR","GBP","NZD"}` and `BaseInstrumentCode: "USD"` in default handler schemas. | Medium |
| `services/current-account/client/starlark.go` | 48 | `ProducesInstruments: []string{"USD","EUR","GBP","NZD"}`. | Medium |
| `services/position-keeping/client/starlark.go` | 40 | `ProducesInstruments: []string{"KWH","GAS","WATER"}`. | Medium |

## Category 6: Deprecated Package Imports (High)

The `shared/domain/money` package is the legacy currency-only money type.
Services should migrate to `shared/pkg/amount` or `shared/platform/quantity`.

| File | Line | Import |
|------|------|--------|
| `services/position-keeping/domain/quantity.go` | 17 | `shared/domain/money` |
| `services/financial-accounting/domain/currency.go` | 8 | `shared/domain/money` |
| `shared/pkg/proto/mappers/currency.go` | 6 | `shared/domain/money` |

## Category 7: Stripe Zero-Decimal Currency Registry (Low - External Adapter)

| File | Line | Description | Severity |
|------|------|-------------|----------|
| `services/reconciliation/adapters/stripe/settlement_transformer.go` | 161 | Zero-decimal currency set matching Stripe's API. External requirement, not a Meridian concern. | Low (Allowlisted) |

## Summary

| Severity | Count | Action |
|----------|-------|--------|
| Critical | 6 | Must remediate: Replace with Reference Data lookups |
| High | 6 | Should remediate: Extract to config or Reference Data |
| Medium | 5 | Context-dependent: Some intentional (payment-order, current-account) |
| Low | 3 | Acceptable: Seed data, external adapters, comments |

### Remediation Priority

1. **shared/pkg/bucketing** and **position-keeping/adapters/balance_mapper.go** -
   Hardcode dimension + precision per instrument. Should call Reference Data
   at startup or accept instrument metadata as parameters.
2. **shared/pkg/proto/mappers/currency.go** -
   Already deprecated. Remove proto Currency enum usage entirely.
3. **shared/domain/money** -
   Migrate remaining 3 importers to `shared/pkg/amount` or
   `shared/platform/quantity`.
4. **shared/pkg/saga/handlers.go** `IsPhysicsInstrument` -
   Should check instrument dimension == "ENERGY" instead of hardcoding codes.
5. **internal-account defaultPrecision** -
   Already has Reference Data fallback path; remove the nil guard when
   Reference Data is always available.
