---
name: prd-multi-asset-purity
description: Remove hardcoded asset references and enforce instrument resolution via Reference Data
triggers:
  - Hardcoded currency codes or instrument codes in production logic
  - Currency registries with fixed lists of ISO 4217 codes
  - Precision fallbacks (defaultPrecision = 2) instead of Reference Data lookup
  - Asset-specific code paths (if code == "KWH", switch on instrument code)
  - Database columns that restrict instrument code length (CHAR(3))
  - Adding new instruments requiring Go code changes
instructions: |
  This PRD enforces the multi-asset purity principle established by PRD 001 (Universal Asset System).
  The architecture is correct; the service layer has accumulated hardcoded violations.
  Key principle: instrument properties (dimension, precision, rounding) come from Reference Data, never code.
  No production code should reference specific instrument codes (GBP, KWH, GPU_HOUR, etc.).
  External adapter boundaries (e.g., Stripe) are exceptions — they map to external API constraints.
  Refer to ADR-0013 (Quantity Types) and ADR-0014 (Reference Data) for the intended architecture.
---

# PRD 037: Multi-Asset Purity

**Status:** Draft
**Parent:** [PRD 001 - Universal Asset System](001-universal-asset-system.md) (architecture defined, enforcement needed)
**ADRs:**

- [0013 - Universal Quantity Type System](../adr/0013-generic-asset-quantity-types.md)
- [0014 - Financial Instrument Reference Data](../adr/0014-financial-instrument-reference-data.md)

## Problem Statement

PRD 001 established Meridian's multi-asset architecture. The infrastructure layer
(proto definitions, handler schemas, quantity system, manifest configuration)
delivers on that vision. However, the service layer has accumulated hardcoded
currency registries, asset-specific code paths, and precision assumptions that
contradict the design.

These aren't cosmetic issues — they create a false ceiling where adding a new
currency or asset class requires Go code changes instead of configuration. This
directly undermines Meridian's value proposition as an AI-configurable,
multi-asset transaction engine.

### The Anti-Pattern

PR [#1457](https://github.com/meridianhub/meridian/pull/1457)
("Enable multi-asset position keeping for kWh support") demonstrates the
anti-pattern clearly:

- Adds `NewMoneyFromInstrumentCode()` that hardcodes `"ENERGY"` as dimension for all non-currency instruments
- Assumes precision `2` for all non-currency instruments (energy needs 6, carbon needs 3)
- References `KWH` as a specific instrument in production code paths
- Works around `CHAR(3)` column constraints instead of fixing them

The correct approach: a single generic path resolving instrument properties
from Reference Data, making any instrument work without code changes.

### The Principle

**No production code should contain specific instrument codes.** Instrument
properties (dimension, precision, rounding mode, display format) are data
managed by Reference Data, not constants managed by developers.

| Acceptable | Not Acceptable |
|------------|----------------|
| Test fixtures: `code: "GBP"` | Production logic: `if code == "GBP"` |
| Demo seeders: `instrumentCode: "KWH"` | Service layer: `switch code { case "USD" }` |
| Documentation: "e.g., GBP, KWH" | Validation: `currency.ByCode(code)` as gate |
| External adapters: Stripe currency handling | Shared domain: `const CurrencyGBP = "GBP"` |

## Scope

### In Scope

1. Deep audit of all production code for hardcoded asset/currency references (expanding the initial findings below)
2. Remove hardcoded currency registries (`shared/domain/money`, `shared/platform/quantity/currency`)
3. Remove asset-specific code paths and precision fallbacks
4. Instrument resolution via Reference Data for all services
5. Database schema migrations to widen instrument code columns
6. CI lint rule to prevent regression

### Out of Scope

- Adding new asset classes (this PRD makes them possible, not creates them)
- Manifest schema changes (already multi-asset capable)
- Proto enum extensions (Dimension enum is already extensible)
- External adapter internals (Stripe adapter correctly handles Stripe's currency-only API)
- Demo/utility display code (horizon-demo can reference specific assets for demonstration)

## Known Issues (Initial Audit)

The following issues were identified in an initial codebase audit. Task 1 (Deep Audit) will expand this list.

### Critical: Hardcoded Currency Registries

| File | Lines | Issue |
|------|-------|-------|
| `shared/domain/money/money.go` | 30-51 | `Currency` type with 7 hardcoded ISO 4217 codes. `IsValid()` rejects anything not in {GBP, USD, EUR, JPY, CHF, CAD, AUD}. `DecimalPlaces()` hardcodes precision per currency. |
| `shared/platform/quantity/currency/currency.go` | 58-105 | Parallel registry of 8 hardcoded currencies. `ByCode()` used as validation gate across services. |

### Critical: Services Gated on Currency Registries

| File | Lines | Issue |
|------|-------|-------|
| `services/current-account/domain/account.go` | 115-119 | `NewCurrentAccount()` calls `currency.ByCode()` — rejects all non-currency instruments. |
| `services/payment-order/domain/quantity.go` | 6-14, 55-69 | Explicit "Currency-Only" design constraint. `NewMoney()` gates on `currency.ByCode()`. |
| `services/financial-accounting/service/grpc_posting_endpoints.go` | 544-556 | `isValidCurrencyCode()` enforces exactly 3 uppercase letters — rejects `GPU_HOUR`, `CARBON_CREDIT`. |

### High: Hardcoded Precision and Dimension Fallbacks

| File | Lines | Issue |
|------|-------|-------|
| `services/internal-account/service/lien_service.go` | 709, 716 | `const defaultPrecision = 2` — wrong for energy (6), carbon (3). |
| `services/current-account/service/grpc_account_endpoints.go` | 54, 83-89 | Falls back to precision `2` when instrument not in currency registry. |
| `services/position-keeping/adapters/balance_mapper.go` | 256-272 | `inferInstrumentProperties()` switch statement maps specific codes to dimensions. |
| `services/position-keeping/domain/quantity.go` | ~310-320 | (Open PR #1457, not yet merged) `NewMoneyFromInstrumentCode()` hardcodes `"ENERGY"` and precision `2`. |

### Medium: Database Schema Constraints

| File | Context | Issue |
|------|---------|-------|
| `services/current-account` persistence | `currency` column | `CHAR(3)` — cannot store instrument codes longer than 3 characters. |
| `services/position-keeping` persistence | `transaction_log_entry.currency` column | `CHAR(3)` per PR #1457 comments. |

### Medium: Positive-Amount Constraint

| File | Lines | Issue |
|------|-------|-------|
| `services/financial-accounting/domain/ledger_posting.go` | 12, 68 | Rejects zero/negative amounts per BIAN. Deferred: requires BIAN compliance review before changing for non-CURRENCY dimensions. |

## Design Principles

1. **Instrument properties are data, not code.** Dimension, precision, rounding
   mode, display format come from Reference Data, never from Go constants.

2. **No instrument code appears in production logic.** Codes like `GBP`, `KWH`,
   `GPU_HOUR` may appear in test fixtures and demos — never in service logic.

3. **Fail closed on unknown instruments.** If Reference Data doesn't know an
   instrument code, reject it. No default precision or guessed dimension.

4. **External adapters are exceptions.** Stripe only handles fiat. The Stripe
   adapter correctly hardcodes currency handling for Stripe's API.

5. **Incremental migration.** Services migrate one at a time. The shared
   `currency` package is deprecated as services adopt Reference Data lookups.

## Technical Approach

### Instrument Resolution Pattern

```go
// BEFORE: Hardcoded currency check
cur := Currency(code)
if cur.IsValid() {
    return NewMoney(amount, cur)
}
// Fallback with hardcoded precision...

// AFTER: Reference Data resolution
inst, err := refdata.ResolveInstrument(ctx, code)
if err != nil {
    return Money{}, fmt.Errorf("unknown instrument %q: %w", code, err)
}
return quantity.NewMoney(amount, inst), nil
```

### Reference Data Contract

The Reference Data service already supports multi-asset instruments via proto. The resolution path provides:

- Instrument code (e.g., `GBP`, `KWH`, `GPU_HOUR`)
- Dimension (e.g., `CURRENCY`, `ENERGY`, `COMPUTE`)
- Precision (decimal places)
- Rounding mode (optional, for regulatory compliance)

Services resolve instrument properties through Reference Data with in-process caching for hot-path performance.

### Currency Package Deprecation Path

1. Add Reference Data resolution to services currently using `currency.ByCode()`
2. Mark `currency.ByCode()` and `Currency.IsValid()` as deprecated
3. Remove hardcoded currency constants once all callers are migrated
4. Retain the `currency` package only if needed for external adapter mappings (e.g., Stripe)

### Schema Migrations

Instrument code columns must be widened to support the full
`InstrumentCodePattern` (`^[A-Z][A-Z0-9_]*$`, max 32 chars):

- `CHAR(3)` -> `VARCHAR(32)`
- Identify all affected tables across services
- Create Atlas migrations per service, ordered before code changes

**Migration safety requirements:**

- **Index/constraint audit**: For each affected table, verify indexes,
  unique constraints, FK lengths, and expression indexes that reference
  the column. Ensure all are compatible with `VARCHAR(32)`.
- **Rollback strategy**: Each Atlas migration must document how to revert
  schema and data if needed. Column widening (`CHAR(3)` -> `VARCHAR(32)`)
  is backward-compatible for existing data but test the reverse path.
- **Mixed-version compatibility**: During rollout, old code (expecting
  `CHAR(3)`) and new code (writing longer codes) may run concurrently.
  Schema widening must deploy before any code that writes longer codes.
  Read paths must tolerate both short and long values.

### CI Lint Rule

Add a CI check that scans production Go files (excluding `*_test.go`, `cmd/seed-*`, `utilities/`) for:

- Direct string comparisons against known instrument codes
- Imports of deprecated `shared/domain/money` or
  `shared/platform/quantity/currency` (exclude external adapter paths
  like `**/adapters/stripe/**` where currency-specific logic is
  intentionally retained)
- `defaultPrecision` or similar hardcoded precision constants

## Success Criteria

1. No production Go file contains hardcoded instrument codes (verified by CI lint rule)
2. `shared/domain/money/money.go` Currency constants are deprecated or removed
3. `shared/platform/quantity/currency/currency.go` `ByCode()` is deprecated or removed
4. All instrument property resolution goes through Reference Data
5. Database columns support instrument codes up to 32 characters
6. Adding a new instrument requires zero Go code changes — only Reference Data configuration
7. Existing tests continue to pass (instrument codes in test fixtures are acceptable)

## Risks

| Risk | Mitigation |
|------|------------|
| Reference Data service unavailability | Cache instrument properties with appropriate TTL. On startup, attempt to populate cache from Reference Data; if unavailable, fall back to a last-known-good snapshot from durable storage. Surface an alert when snapshot fallback is used. Fail closed only for truly unknown instruments at request time. |
| Performance regression from runtime lookups | Instrument properties are immutable per tenant deployment. Cache aggressively (per-process in-memory). |
| Breaking existing API contracts | Proto fields remain string-typed. No wire format changes. |
| Migration ordering | Schema migrations (widen columns) must precede code changes that use longer codes. |
| Scope creep into payment-order redesign | Payment-order's currency-only constraint is a business rule, not a code smell. The fix is to validate via Reference Data (instrument must have CURRENCY dimension), not to remove the constraint. |
