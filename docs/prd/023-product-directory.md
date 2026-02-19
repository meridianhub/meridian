---
name: prd-product-directory
description: BIAN-aligned AccountTypeRegistry for runtime-configurable product catalog within Reference Data
triggers:
  - Working on account types or product types
  - Adding new account types to the system
  - Configuring product-specific business logic (Starlark sagas, CEL validation per product)
  - Multi-tenant product catalog or blueprint configuration
  - Questions about the relationship between accounts and product types
  - Valuation method or conversion configuration per product type
  - BIAN Product Directory service domain
instructions: |
  The AccountTypeRegistry lives within Reference Data (not a separate service).
  It follows the InstrumentRegistry pattern exactly: DRAFT/ACTIVE/DEPRECATED lifecycle,
  is_system flag for platform blueprints, CEL compilation at draft creation, optimistic locking.
  Product definitions reference sagas by name convention ({prefix}.{operation}), not by embedding scripts.
  Product types also define accepted valuation methods (conversion rules) -- ValuationFeatures are
  configured at the product type level and seeded to accounts on creation, not per-account.
  The manifest remains the authoring surface; the registry is the runtime query surface.
---

# Product Directory - Account Type Registry and Runtime-Configurable Product Catalog

## Status: Not Started

## Executive Summary

Meridian's account type system is fragmented across three separate definitions
with no single source of truth. Account types are hardcoded as proto enums,
preventing tenants from defining custom account types at runtime. This PRD
introduces an AccountTypeRegistry within the Reference Data service -- following
the established InstrumentRegistry pattern -- to provide a runtime-configurable,
BIAN-aligned product catalog that supports multi-tenant customization
and bi-temporal versioning. Product types also define accepted valuation
methods (asset conversion rules), correcting the current design where
ValuationFeatures are configured per-account rather than per-product-type.

## Problem Statement

### Current Fragmentation

Account type definitions exist in three disconnected locations:

| Location | Type | Enforcement | Scope |
|----------|------|-------------|-------|
| `internal_bank_account/v1::InternalAccountType` | Proto enum (9 values) | Compile-time + SQL CHECK | Internal accounts only |
| `common/v1/types.proto::AccountType` | Proto enum (6 values) | Compile-time | Financial accounting classification |
| `control_plane/v1/manifest.proto::AccountTypeDefinition` | Manifest message with CEL policies | Runtime validation | Tenant configuration |

Additionally, the Current Account service has an `account_type varchar(50)`
column hardcoded to `"current"` with no validation against any registry.

### What Cannot Be Done Today

1. **Cannot specify a product type when creating an account** --
   `InitiateCurrentAccountRequest` has no `product_type` field.
2. **Cannot define new account types at runtime** -- Adding a type
   requires modifying a Go enum, updating a SQL CHECK constraint,
   recompiling, and redeploying.
3. **Cannot associate different business logic with different product
   types** -- Saga routing is by operation name, not by product type.
4. **Cannot query what account types are available** -- No runtime API
   to list, filter, or inspect account type definitions.
5. **Cannot define valuation methods at the product type level** --
   ValuationFeatures (asset conversion rules) are currently configured
   per-account. If a product type accepts USD deposits into a GBP
   account, every individual account must have its ValuationFeature
   configured manually rather than inheriting from the product definition.

### Existing Infrastructure (Already Built)

The infrastructure needed for a product catalog largely exists:

| Capability | Location | Status |
|------------|----------|--------|
| InstrumentRegistry (DRAFT/ACTIVE/DEPRECATED lifecycle) | `services/reference-data/registry/` | Production |
| CEL compiler with security constraints | `services/reference-data/cel/compiler.go` | Production |
| Saga registry with platform defaults + tenant overrides | `services/reference-data/saga/` | Production |
| Bi-temporal reference data nodes | `services/reference-data/node/` | Production |
| `register_account_type` handler in applier | `services/control-plane/internal/applier/handlers.yaml:57` | Production |
| Manifest `AccountTypeDefinition` with CEL policies | `api/proto/meridian/control_plane/v1/manifest.proto:165` | Production |
| Manifest validation (CEL compilation, cross-references) | `services/control-plane/internal/validator/` | Production |
| Schema-per-tenant isolation | `shared/platform/db/tenant_scope.go` | Production |
| Starlark execution with typed service modules | `shared/pkg/saga/` | Production |
| handlers.yaml schema registry (760 lines, 9 namespaces) | `shared/pkg/saga/schema/handlers.yaml` | Production |
| ValuationFeature domain model (per-account) | `shared/pkg/valuationfeature/` | Production |
| ValuationEngine interface + Starlark runtime | `shared/pkg/valuation/` | Production |

The Product Directory is a **composition of existing capabilities behind a new API surface**, not a new system from scratch.

## BIAN Alignment

### Service Domain Mapping

BIAN 13.0 defines a **Product Directory** service domain (SD-CR-006) that
maintains product/service specifications. BIAN service domains are logical
separations, not deployment mandates. The Product Directory maps to a
**module within Reference Data**, consistent with how instruments, sagas,
and nodes already coexist within Reference Data.

### Behavior Qualifier Mapping

| BIAN BQ | Meridian Mapping | Implementation |
|---------|------------------|----------------|
| **Operations** | CEL policy evaluation, saga routing | Existing CEL compiler + saga naming convention |
| **Servicing** | AccountTypeRegistry CRUD + lifecycle | New (follows InstrumentRegistry pattern) |
| **Production** | Manifest compilation and application | Existing manifest applier + validation pipeline |
| **SalesAndMarketing** | Product catalog browsing and discovery | New read-only projections from registry |

### Separation of Concerns

BIAN Product Directory stores metadata about products, not executable code. Meridian follows this separation:

- **Product catalog** (AccountTypeRegistry): Stores metadata, CEL policies,
  designated instrument, default saga names. Consistent with how
  InstrumentRegistry stores CEL validation expressions.
- **Saga registry** (existing): Stores executable Starlark scripts.
  Product definitions reference sagas by name, not by embedding scripts.
- **Convention-based routing**: Saga names follow
  `{product_type_code}.{operation}` pattern (e.g., `SAVINGS.deposit`),
  with fallback to `{operation}` for backwards compatibility.

## Solution Design

### AccountTypeRegistry -- New Module in Reference Data

A new Go package `services/reference-data/accounttype/` following the InstrumentRegistry pattern exactly.

#### Data Model

```go
type AccountTypeDefinition struct {
    ID                     uuid.UUID
    Code                   string        // Immutable PK: "CURRENT_GBP", "SAVINGS_GBP"
    Version                int           // Allows multiple versions of the same code
    DisplayName            string        // "GBP Personal Current Account"
    Description            string        // Detailed description of this product type
    NormalBalance          string        // "DEBIT" or "CREDIT"
    InstrumentCode         string        // Designated instrument: "GBP", "KWH", "TONNE_CO2E"
    DefaultSagaPrefix      string        // e.g., "SAVINGS" for "{prefix}.deposit" routing
    FiatMethodID           *uuid.UUID    // Default method for any fiat→fiat conversion (e.g., forex-spot)
    FiatMethodVersion      *int          // Version of the fiat conversion method (nil when FiatMethodID is nil)
    ValuationMethods       []ValuationMethodTemplate // Explicit non-fiat conversion templates
    ValidationCEL          string        // CEL expression for transaction validation
    BucketingCEL           string        // CEL expression for fungibility bucketing
    EligibilityCEL         string        // v1 placeholder: reserved field, not enforced at runtime
    AttributeSchema        []byte        // v1 placeholder: stored but not validated
    Attributes             map[string]any // Extensible metadata (fee config, interest config, etc.)
    Status                 Status        // DRAFT, ACTIVE, DEPRECATED. ACTIVE is immutable
    IsSystem               bool          // Platform blueprint (read-only for tenants)
    SuccessorID            *uuid.UUID    // Points to replacement when deprecated
    CreatedAt              time.Time
    UpdatedAt              time.Time
    ActivatedAt            *time.Time
    DeprecatedAt           *time.Time
}

// ValuationMethodTemplate defines an explicit non-fiat asset conversion
// for this product type. Fiat-to-fiat conversions are handled
// universally by the product type's FiatMethodID and do not need
// per-currency templates.
//
// Templates are only needed for cross-class conversions where the input
// instrument is a different asset class to the account's designated
// instrument (e.g., kWh→GBP, CO2→GBP).
//
// Templates have their own lifecycle independent of the parent
// AccountTypeDefinition. New conversion methods can be added to an
// existing product type without creating a new product type version.
// Deprecated templates use SuccessorID to point to their replacement,
// following the same supersedes pattern as AccountTypeDefinition itself.
//
// Example: INVENTORY_KWH accepts carbon credit deposits:
//   - {InputInstrument: "TONNE_CO2E", MethodID: <carbon-kWh-method>}
//
// Replacing a method version:
//   - Deprecate old template (SuccessorID → new template)
//   - New template with updated method version, Status=ACTIVE
type ValuationMethodTemplate struct {
    ID                     uuid.UUID
    AccountTypeID          uuid.UUID     // Parent product type
    InputInstrument        string        // Instrument to convert FROM (e.g., "USD")
    ValuationMethodID      uuid.UUID     // Reference to Valuation Engine method
    ValuationMethodVersion int           // Method version for immutability
    Parameters             map[string]any // Default method parameters
    Status                 Status        // DRAFT, ACTIVE, DEPRECATED
    SuccessorID            *uuid.UUID    // Points to replacement when deprecated
    CreatedAt              time.Time
    UpdatedAt              time.Time
}
```

#### Registry Interface

```go
type AccountTypeRegistry interface {
    // Servicing BQ -- Core CRUD + lifecycle
    GetDefinition(ctx context.Context, code string, version int) (*AccountTypeDefinition, error)
    GetActiveDefinition(ctx context.Context, code string) (*AccountTypeDefinition, error)
    ListActive(ctx context.Context) ([]*AccountTypeDefinition, error)
    ListByStatus(ctx context.Context, status Status) ([]*AccountTypeDefinition, error)
    CreateDraft(ctx context.Context, def *AccountTypeDefinition) error
    UpdateDefinition(ctx context.Context, code string, version int, updates *AccountTypeDefinition) error
    ActivateAccountType(ctx context.Context, code string, version int) error
    DeprecateAccountType(ctx context.Context, code string, version int, successorID *uuid.UUID) error

    // Operations BQ -- Policy evaluation
    ValidateTransaction(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error)
    CheckEligibility(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error)

    // SalesAndMarketing BQ -- Catalog browsing
    ListByInstrument(ctx context.Context, instrumentCode string) ([]*AccountTypeDefinition, error)
    GetProductFeatures(ctx context.Context, code string, version int) (map[string]any, error)
}
```

#### Database Schema

```sql
CREATE TABLE account_type_definitions (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code                     VARCHAR(50) NOT NULL,
    version                  INT NOT NULL DEFAULT 1,
    display_name             VARCHAR(255) NOT NULL,
    description              TEXT,
    normal_balance           VARCHAR(10) NOT NULL CHECK (normal_balance IN ('DEBIT', 'CREDIT')),
    instrument_code          VARCHAR(50) NOT NULL,
    default_saga_prefix      VARCHAR(100),
    fiat_method_id           UUID,
    fiat_method_version      INT,
    validation_cel           TEXT,
    bucketing_cel            TEXT,
    eligibility_cel          TEXT,
    attribute_schema         JSONB,
    attributes               JSONB DEFAULT '{}',
    status                   VARCHAR(20) NOT NULL DEFAULT 'DRAFT'
                             CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
    is_system                BOOLEAN NOT NULL DEFAULT FALSE,
    successor_id             UUID REFERENCES account_type_definitions(id),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at             TIMESTAMPTZ,
    deprecated_at            TIMESTAMPTZ,

    CONSTRAINT uq_account_type_code_version UNIQUE (code, version),
    CONSTRAINT chk_successor_not_self CHECK (successor_id != id),
    CONSTRAINT chk_fiat_method_pair
        CHECK ((fiat_method_id IS NULL) = (fiat_method_version IS NULL))
);

CREATE INDEX idx_account_type_status ON account_type_definitions (status);
CREATE INDEX idx_account_type_code ON account_type_definitions (code);

-- At most one ACTIVE definition per code at any time.
CREATE UNIQUE INDEX uq_active_account_type_code
    ON account_type_definitions (code)
    WHERE status = 'ACTIVE';

-- Explicit (non-fiat) valuation method templates per product type.
-- Fiat-to-fiat conversions use the parent's fiat_method_id and do
-- not need entries here. Templates are for cross-class conversions
-- (e.g., kWh→GBP, CO2→GBP).
-- Each template has its own DRAFT/ACTIVE/DEPRECATED lifecycle,
-- independent of the parent account type version. New conversions
-- can be added without bumping the product type version.
-- Deprecated templates point to their replacement via successor_id.
CREATE TABLE account_type_valuation_methods (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_type_id          UUID NOT NULL
                             REFERENCES account_type_definitions(id),
    input_instrument         VARCHAR(50) NOT NULL,
    valuation_method_id      UUID NOT NULL,
    valuation_method_version INT NOT NULL DEFAULT 1,
    parameters               JSONB DEFAULT '{}',
    status                   VARCHAR(20) NOT NULL DEFAULT 'DRAFT'
                             CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
    successor_id             UUID REFERENCES account_type_valuation_methods(id),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Only one ACTIVE template per (account_type, input_instrument)
    CONSTRAINT chk_successor_not_self
        CHECK (successor_id != id)
);

-- Partial unique index: at most one ACTIVE method per input instrument
-- per product type. Multiple DEPRECATED/DRAFT entries are allowed.
CREATE UNIQUE INDEX uq_active_valuation_method
    ON account_type_valuation_methods (account_type_id, input_instrument)
    WHERE status = 'ACTIVE';
```

### Saga Routing Convention

Product types carry a `default_saga_prefix` field. Saga resolution follows:

```text
1. Try: "{default_saga_prefix}.{operation}" with tenant override
2. Try: "{default_saga_prefix}.{operation}" platform default
3. Fallback: "{operation}" (backwards compatible)
```

Example: Product type `SAVINGS` with `default_saga_prefix = "SAVINGS"`:

- Deposit operation resolves: `SAVINGS.deposit` (tenant) -> `SAVINGS.deposit` (platform) -> `deposit` (fallback)

### Multi-Tenant Product Catalogs

Following the saga registry pattern (platform defaults + tenant overrides):

- **Platform blueprints**: Seeded with `is_system = true` during tenant provisioning. Read-only for tenants.
- **Tenant products**: Created by tenants with `is_system = false`. Full CRUD lifecycle.
- **Blueprint extension**: Tenants create their own definitions with custom
  CEL policies and saga prefixes. No inheritance mechanism in v1 -- tenants
  create independent definitions.

### Valuation Method Configuration

Product types define which asset conversions they accept. This replaces
the current per-account ValuationFeature configuration with a
product-type-level definition.

#### Two-Tier Conversion Resolution

An instrument is classified as fiat when its `Dimension == MONETARY` in
the InstrumentRegistry (e.g., GBP, USD, EUR are all `MONETARY`; KWH is
`ELECTRICITY`; TONNE_CO2E is `EMISSIONS`).

Conversion method resolution follows two tiers:

1. **Fiat-to-fiat** (universal): If both the input instrument and the
   account's designated instrument are `MONETARY`, use the product
   type's `FiatMethodID`. No per-currency templates needed -- a GBP
   account automatically accepts USD, EUR, CHF, or any other fiat
   currency via the same forex spot method.

2. **Cross-class** (explicit templates): If the input instrument is a
   different asset class (e.g., kWh into a GBP account), an explicit
   `ValuationMethodTemplate` must exist. These require specific
   conversion logic that cannot be generalised.

```text
Resolution order:
  1. Check explicit template for (product_type, input_instrument)
  2. If none, check if both instruments are fiat → use fiat_method_id
  3. If neither → reject (no conversion available)

Example: CURRENT_GBP (instrument_code=GBP, fiat_method_id=forex-spot)
  - Deposit USD → fiat→fiat → forex-spot method (automatic)
  - Deposit EUR → fiat→fiat → forex-spot method (automatic)
  - Deposit kWh → no template → rejected
  - Deposit kWh → add explicit template → accepted

Example: INVENTORY_KWH (instrument_code=KWH, fiat_method_id=nil)
  - Deposit MWH → explicit template for MWH→KWH required
  - Deposit GBP → explicit template for GBP→KWH required
```

#### Current Design (Per-Account -- Being Replaced)

```text
Account A (GBP) → manually add ValuationFeature(USD→GBP)
Account B (GBP) → manually add ValuationFeature(USD→GBP)
Account C (GBP) → forgot to add feature → runtime error
```

#### New Design (Per-Product-Type)

```text
Product Type CURRENT_GBP:
  instrument_code: GBP
  fiat_method: forex-spot  (handles all fiat→GBP automatically)
  valuation_methods:       (only non-fiat, if any)
    - input: TONNE_CO2E, method: carbon-to-gbp-v1

Account creation (product_type_code=CURRENT_GBP):
  → any fiat deposit auto-converts via forex-spot
  → CO2 deposit converts via explicit template
```

When an account is created with a `product_type_code`, the system seeds
ValuationFeatures from the product type's explicit templates. Fiat→fiat
conversion does not require seeded features -- it is resolved at
runtime from the product type's `fiat_method_id`.

#### Independent Lifecycle and Supersedes

Explicit valuation method templates have their own
DRAFT → ACTIVE → DEPRECATED lifecycle, independent of the parent
product type version. This means:

- **v1 scope**: Templates follow their parent's lifecycle -- they are
  activated and deprecated alongside the product type. Independent
  template lifecycle management (add/replace templates on an active
  product type) is supported by the schema but deferred to v2.
- **Adding a new conversion** (e.g., TONNE_CO2E→GBP to `CURRENT_GBP`)
  creates a new template row without changing the product type
  definition.
- **Replacing a method version** deprecates the old template (setting
  `successor_id` to the new one) and activates the replacement. The
  partial unique index ensures only one ACTIVE template per
  `(account_type, input_instrument)` pair.
- **Existing accounts are unaffected** -- their already-seeded
  ValuationFeatures continue to reference the method version they were
  created with. New accounts pick up the current ACTIVE templates.
- **Fiat method upgrades** are done by updating `fiat_method_id` on the
  product type definition. The new method applies to future valuation
  requests. Running sagas pin the fiat method at initiation time to
  prevent mid-saga method switches.
- **Deprecated product types** do not affect existing accounts. Accounts
  retain an immutable `(product_type_code, product_type_version)`
  reference and continue operating under the rules of the version they
  were created with. Deprecation only prevents new account creation.

#### Manifest Surface

The manifest remains the authoring surface for product definitions:

```yaml
account_types:
  - code: CURRENT_GBP
    name: "GBP Current Account"
    normal_balance: DEBIT
    instrument_code: GBP
    fiat_method: "forex-spot-v1"     # Resolved to UUID via Valuation Engine lookup
    policies:
      validation: "amount > 0 && amount <= 1000000"

  - code: INVENTORY_KWH
    name: "kWh Inventory Account"
    normal_balance: DEBIT
    instrument_code: KWH
    valuation_methods:               # Cross-class only
      - input_instrument: TONNE_CO2E
        method_id: "carbon-to-kwh-v1"
        method_version: 1
```

The manifest applier resolves string references to UUIDs during
compilation: `fiat_method` and `method_id` strings are looked up
against the Valuation Engine's method registry. Unresolvable references
produce a structured validation error.

### Compilation Pipeline Endpoint

New endpoint for validating product definitions without persisting:

```protobuf
rpc ValidateProductDefinition(ValidateProductDefinitionRequest)
    returns (ValidateProductDefinitionResponse);

message ValidateProductDefinitionRequest {
    AccountTypeDefinition definition = 1;
    repeated SagaDefinition associated_sagas = 2;
}

message ValidateProductDefinitionResponse {
    bool valid = 1;
    repeated ValidationError errors = 2;
}

message ValidationError {
    string field = 1;      // e.g., "policies.validation"
    string error_code = 2; // e.g., "CEL_COMPILATION_FAILED"
    string message = 3;    // Human-readable error description
    int32 line = 4;
    int32 column = 5;
}
```

The compilation pipeline validates:

1. CEL expressions compile successfully (existing CEL compiler)
2. Saga scripts parse and pass dry-run validation (existing saga
   validator)
3. Cross-references are valid (instrument_code references a defined
   instrument, valuation method IDs reference existing methods)
4. Structured errors returned for iteration

## Platform Blueprint Seed Data

System account types seeded during tenant provisioning:

| Code | Display Name | Normal Balance | Instrument | Saga Prefix |
|------|-------------|----------------|------------|-------------|
| `CURRENT_GBP` | GBP Current Account | DEBIT | GBP | `CURRENT` |
| `CURRENT_EUR` | EUR Current Account | DEBIT | EUR | `CURRENT` |
| `CURRENT_USD` | USD Current Account | DEBIT | USD | `CURRENT` |
| `SAVINGS_GBP` | GBP Savings Account | DEBIT | GBP | `SAVINGS` |
| `CLEARING_GBP` | GBP Clearing Account | DEBIT | GBP | `CLEARING` |
| `NOSTRO_GBP` | GBP Nostro Account | DEBIT | GBP | `NOSTRO` |
| `VOSTRO_GBP` | GBP Vostro Account | CREDIT | GBP | `VOSTRO` |
| `HOLDING_GBP` | GBP Holding Account | DEBIT | GBP | `HOLDING` |
| `SUSPENSE_GBP` | GBP Suspense Account | DEBIT | GBP | `SUSPENSE` |
| `REVENUE_GBP` | GBP Revenue Account | CREDIT | GBP | `REVENUE` |
| `EXPENSE_GBP` | GBP Expense Account | DEBIT | GBP | `EXPENSE` |
| `INVENTORY_KWH` | kWh Inventory Account | DEBIT | KWH | `INVENTORY` |

Tenants extend with their own entries (e.g., `ENERGY_SETTLEMENT_KWH`,
`CARBON_INVENTORY_CO2E`, `VOUCHER_FOOD_GBP`).

## Non-Goals for v1

- **Product inheritance/composition**: No "SAVINGS extends DEPOSIT" or
  mixin system. Tenants create independent definitions. Composition
  deferred to a future Product Combination service.
- **Product bundles**: No bundling of multiple products into a combined
  offering.
- **Pricing engine integration**: Fee schedules stored as extensible
  attributes, not as a first-class pricing domain.
- **Approval workflows**: No multi-step approval chain for product
  definition changes. Lifecycle transitions are immediate. Governance
  deferred to a future Product Design service domain.
- **UI**: Control plane UI is out of scope. REST API is the interaction
  surface.
- **Per-account valuation overrides**: v1 seeds ValuationFeatures from
  the product type template. Per-account overrides of method parameters
  are a future enhancement.

## Success Criteria

1. A tenant can define a new account type (e.g., `ENERGY_SETTLEMENT`) via manifest without any code changes or deployments.
2. Accounts created with `product_type_code = "ENERGY_SETTLEMENT"`
   automatically route to the correct Starlark saga.
3. The manifest compilation pipeline validates product definitions and
   returns structured errors when CEL expressions, cross-references,
   or valuation method references are invalid.
4. Existing accounts and services continue to work unchanged during migration (backwards compatible).
5. Platform blueprints are seeded during tenant provisioning and are read-only for tenants.
6. Valuation method templates defined on a product type are
   automatically seeded as ValuationFeatures when an account of that
   type is created -- no per-account manual configuration required.

## Testing Strategy

- **Unit tests**: Domain model validation, lifecycle transitions, CEL
  compilation, optimistic locking. Following InstrumentRegistry test
  patterns.
- **Integration tests**: Testcontainer-based (CockroachDB). Full registry
  CRUD, multi-tenant isolation, platform blueprint seeding.
- **E2E tests**: Manifest apply with account type registration, account
  creation with product_type_code, saga routing verification,
  valuation feature seeding from templates.

## References

- **InstrumentRegistry** (pattern template): `services/reference-data/registry/registry.go`
- **Saga registry**: `services/reference-data/saga/`
- **Manifest AccountTypeDefinition**: `api/proto/meridian/control_plane/v1/manifest.proto:165`
- **Applier handlers.yaml**: `services/control-plane/internal/applier/handlers.yaml:57`
- **CEL compiler**: `services/reference-data/cel/compiler.go`
- **ValuationFeature domain**: `shared/pkg/valuationfeature/`
- **ValuationEngine interface**: `shared/pkg/valuation/`
- **BIAN alignment PRD**: `.taskmaster/docs/prd-bian-alignment.md`
- **BIAN 13.0 Product Directory**: SD-CR-006 (external)
