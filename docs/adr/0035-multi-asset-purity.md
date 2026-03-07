---
name: adr-0035-multi-asset-purity
description: Enforce Reference Data as the sole source of instrument properties across all services
triggers:
  - Adding a new instrument type or asset class to the platform
  - Creating or modifying account/position constructors that accept instrument codes
  - Reviewing CI failures from the multi-asset purity lint check
  - Migrating a service from hardcoded currency logic to Reference Data resolution
instructions: |
  All instrument properties (code, dimension, precision) must be resolved from Reference Data
  at the API boundary (gRPC layer). Domain constructors trust caller-provided instrument
  properties and must not consult local currency registries. Use quantity.NewInstrument() and
  amount.Zero()/amount.New() for domain-level amount construction. The CI lint rule
  (scripts/lint-multi-asset-purity.sh) enforces this at merge time.
---

# 35. Multi-Asset Purity Enforcement

Date: 2026-03-07

## Status

Accepted

## Context

Meridian's architecture supports multiple asset dimensions (CURRENCY, ENERGY, CARBON, COMPUTE,
DATA, COUNT, etc.) through the `quantity.Instrument` type system. However, early service
implementations hardcoded assumptions about currency-only instruments:

- Domain constructors called `currency.ByCode()` to validate instrument codes against a local
  ISO 4217 registry, rejecting non-currency instruments like KWH or GPU_HOUR
- Precision was hardcoded to 2 decimal places (appropriate for GBP/USD but not for energy
  meters at 6dp or carbon credits at 0dp)
- Switch statements on instrument codes created implicit registries that required code changes
  to support new asset classes

These hardcoded paths prevented the platform from fulfilling its multi-asset mission. A
systematic migration was needed to centralize instrument knowledge in Reference Data and
make all services instrument-agnostic.

## Decision Drivers

* **Multi-asset support**: Services must handle any valid instrument without code changes
* **Single source of truth**: Reference Data service owns instrument definitions (code, dimension, precision)
* **Fail-closed safety**: Missing Reference Data must prevent account creation, not fall back to defaults
* **Backward compatibility**: Existing CURRENCY accounts must continue working during migration
* **CI enforcement**: New violations must be caught before merge, not discovered in production

## Considered Options

1. **Runtime validation via middleware** - Inject instrument validation at the gRPC interceptor level
2. **Domain-level validation with extensible registry** - Keep validation in domain, make registry pluggable
3. **API-boundary validation with trust-the-caller domains** - Validate at gRPC layer, domain trusts caller

## Decision Outcome

Chosen option: "API-boundary validation with trust-the-caller domains", because it
cleanly separates concerns (validation at boundaries, pure logic in domains) and
eliminates the need for domain packages to know about external registries.

### Architecture

```
gRPC Layer (API Boundary)
  |
  +-- instrumentGetter.GetInstrument(ctx, code, version)
  |     Returns: dimension, precision, metadata
  |     Fails: codes.InvalidArgument (unknown), codes.FailedPrecondition (unavailable)
  |
  v
Domain Layer (Pure Logic)
  |
  +-- quantity.NewInstrument(code, version, dimension, precision)
  +-- amount.Zero(inst) / amount.New(inst, minorUnits)
  |     No external lookups. No registry calls. Trusts caller.
  |
  v
Persistence Layer
  |
  +-- Stores instrument_code (VARCHAR 32) + dimension + precision
  +-- Reconstructs Amount via quantity.NewInstrument on read
```

### Migrated Services

| Service | Migration | Key Change |
|---------|-----------|------------|
| current-account | Task 6 | Removed `currency.ByCode()` from domain, fail-closed gRPC |
| position-keeping | Task 7 | `InstrumentResolver` replaces hardcoded precision lookup |
| internal-account | Task 8 | Widened instrument columns, Reference Data resolution |
| financial-accounting | Task 9 | `InstrumentResolver` for journal entry validation |
| payment-order | Task 10 | Documented as intentionally currency-only (business constraint) |

### Positive Consequences

* New asset classes (e.g., water rights, bandwidth credits) work without code changes
* Instrument precision is always correct (resolved from Reference Data, not assumed)
* CI lint prevents regression to hardcoded patterns
* Domain code is simpler (no external dependencies for instrument validation)

### Negative Consequences

* Account creation requires Reference Data availability (fail-closed)
* Persistence layer reconstruction uses `quantity.NewInstrument` directly (bypasses registry)
* `amount.NewFromInstrument` retains a CURRENCY-specific path that consults the legacy currency
  registry for backward compatibility during persistence reconstruction. Non-CURRENCY dimensions
  use caller-provided precision directly. This compatibility path will be removed when the
  legacy currency packages are deleted.
* Legacy `shared/domain/money` and `shared/platform/quantity/currency` packages are deprecated
  but not yet removed (backward compatibility for tests and seed data)

## CI Enforcement

The multi-asset purity lint (`scripts/lint-multi-asset-purity.sh`) runs on every PR and checks for:

1. **Hardcoded instrument codes** in string comparisons (`== "GBP"`)
2. **Switch statements** on instrument codes (`case "KWH"`)
3. **Deprecated imports** (`shared/domain/money`)
4. **Hardcoded precision** (`defaultPrecision = 2`)
5. **Legacy registry calls** (`currency.ByCode()`)

Allowlisted paths: test files, seed commands, the currency package itself, and
payment-order (intentionally currency-only). Known violations are tracked in
`is_known_violation()` and must be documented.

## Links

* [Multi-Asset Purity Lint Script](../../scripts/lint-multi-asset-purity.sh)
* [Shared Amount Package](../../shared/pkg/amount/)
* [Quantity Instrument Type](../../shared/platform/quantity/instrument.go)
* [Reference Data Instrument Registry](../../services/reference-data/registry/)

## Notes

The deprecated `shared/domain/money` and `shared/platform/quantity/currency` packages should
be removed once all test fixtures and seed data are migrated to use `shared/pkg/amount`
directly. The `--strict` mode of the lint script will fail on known violations and can be
used to track progress toward full removal.
