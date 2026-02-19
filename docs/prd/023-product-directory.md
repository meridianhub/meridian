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
  is_system flag for platform blueprints, CEL compilation at draft creation.
  Optimistic locking: UpdateDefinition includes updated_at in the WHERE clause; returns
  ErrOptimisticLock when no rows match (concurrent modification detected).
  Only one ACTIVE definition per code is allowed (enforced by partial unique index).
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
    NormalBalance          NormalBalance  // DEBIT or CREDIT (typed enum)
    BehaviorClass          BehaviorClass // Fixed system behavior category (typed enum)
    InstrumentCode         string        // Designated instrument: "GBP", "KWH", "TONNE_CO2E"
    DefaultSagaPrefix      string        // e.g., "SAVINGS" for "{prefix}.deposit" routing
    FiatMethodID           *uuid.UUID    // Default method for any fiat→fiat conversion (e.g., forex-spot)
    FiatMethodVersion      *int          // Version of the fiat conversion method (nil when FiatMethodID is nil)
    ValuationMethods       []ValuationMethodTemplate // Explicit non-fiat conversion templates
    ValidationCEL          string        // CEL expression for transaction validation
    BucketingCEL           string        // CEL expression for fungibility bucketing
    EligibilityCEL         string        // CEL expression evaluated at account creation
    AttributeSchema        json.RawMessage // JSON Schema validating the Attributes map
    Attributes             map[string]any // Extensible metadata validated against AttributeSchema
    Status                 Status        // DRAFT, ACTIVE, DEPRECATED (typed enum). ACTIVE is immutable
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
// Templates have their own DRAFT/ACTIVE/DEPRECATED status and
// SuccessorID fields, enabling independent lifecycle management.
// In v1, templates are activated and deprecated alongside their
// parent AccountTypeDefinition. Independent template lifecycle
// (add/replace on an active product type) is deferred to v2.
// SuccessorID points to a replacement template when deprecated.
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

**BehaviorClass** is a fixed set of system behavior categories that
services use to apply hard-coded constraints. The dynamic `Code`
(e.g., `CLEARING_GBP`) is the user-facing product identifier, but
the system needs `BehaviorClass = "CLEARING"` to know that
org-scoping is forbidden, or `BehaviorClass = "CUSTOMER"` to enable
party association. This replaces the role currently played by the
`InternalAccountType` enum.

| BehaviorClass | System Behavior |
|---------------|-----------------|
| `CUSTOMER` | Party-scoped, eligibility checks, external-facing |
| `CLEARING` | Global (no org-scoping), settlement operations |
| `NOSTRO` | Correspondent banking, our money at another bank |
| `VOSTRO` | Correspondent banking, their money at our bank |
| `HOLDING` | Temporary holding, time-bound lifecycle |
| `SUSPENSE` | Unidentified/pending transactions, auto-resolution |
| `REVENUE` | P&L tracking, credit normal balance |
| `EXPENSE` | P&L tracking, debit normal balance |
| `INVENTORY` | Non-cash asset tracking (energy, commodities) |

New behavior classes can be added by extending the CHECK constraint
in a migration, but this is deliberately a platform-level change
(not tenant-configurable) because it maps to hard-coded service
logic.

#### Registry Interface

```go
type AccountTypeRegistry interface {
    // Servicing BQ -- Core CRUD + lifecycle
    GetDefinition(ctx context.Context, code string, version int) (*AccountTypeDefinition, error)
    GetActiveDefinition(ctx context.Context, code string) (*AccountTypeDefinition, error)
    ListActive(ctx context.Context) ([]*AccountTypeDefinition, error)
    ListByStatus(ctx context.Context, status Status) ([]*AccountTypeDefinition, error)
    CreateDraft(ctx context.Context, def *AccountTypeDefinition) error
    // UpdateDefinition uses optimistic locking: the caller's UpdatedAt must match
    // the stored value. Returns ErrOptimisticLock on concurrent modification.
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
    behavior_class           VARCHAR(20) NOT NULL
                             CHECK (behavior_class IN (
                                 'CUSTOMER', 'CLEARING', 'NOSTRO', 'VOSTRO',
                                 'HOLDING', 'SUSPENSE', 'REVENUE', 'EXPENSE',
                                 'INVENTORY'
                             )),
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
1. If default_saga_prefix is set:
   a. Try: "{default_saga_prefix}.{operation}" with tenant override
   b. Try: "{default_saga_prefix}.{operation}" platform default
2. Fallback: "{operation}" (backwards compatible)
```

When `default_saga_prefix` is empty or null, steps 1a/1b are skipped
and resolution goes directly to the fallback. This is the expected
path for product types that use the standard (non-prefixed) saga
definitions.

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

#### Template Lifecycle and Supersedes

Valuation method templates carry their own `Status` and `SuccessorID`
fields. The schema supports independent lifecycle management, but in
v1, templates are activated and deprecated alongside their parent
product type. Independent template lifecycle (add/replace templates
on an already-active product type) is deferred to v2.

Within a product type version:

- **Adding a conversion** (e.g., TONNE_CO2E→GBP to `CURRENT_GBP`)
  adds a template row to the DRAFT product type before activation.
- **Replacing a method version** deprecates the old template (setting
  `successor_id` to the new one) and activates the replacement in a
  new product type version. The partial unique index ensures only one
  ACTIVE template per `(account_type, input_instrument)` pair.
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

### Eligibility CEL

The `EligibilityCEL` field holds a CEL expression evaluated at account
creation time to determine whether the requesting party may open an
account of this type. It uses a dedicated CEL environment following
the same compiler infrastructure as `ValidationCEL` and `BucketingCEL`
(security constraints: 4096 byte limit, depth 10, cost limit 10000).

**CEL environment variables:**

| Variable | Type | Source |
|----------|------|--------|
| `party.type` | `string` | Party service: `"PERSON"` or `"ORGANIZATION"` |
| `party.status` | `string` | Party service: `"ACTIVE"`, `"RESTRICTED"`, `"SUSPENDED"`, `"TERMINATED"` |
| `party.external_reference_type` | `string` | Party service: `"COMPANIES_HOUSE"`, `"LEI"`, etc. |
| `attributes` | `map[string]string` | Request-supplied attributes for the new account |

**Return type:** `bool` (true = eligible, false = rejected).

**Evaluation point:** The account-creating service calls the registry's
`CheckEligibility` method before persisting the account. Rejection
returns a `FAILED_PRECONDITION` gRPC status with the expression that
failed.

**Compilation:** The CEL expression is compiled when the product type
is created in DRAFT status, identical to how `ValidationCEL` is
compiled today. Compilation failure prevents the draft from being
saved.

**Examples:**

```text
# Only active parties can open accounts
party.status == 'ACTIVE'

# Restrict to organisations (e.g., internal groupings, syndicates)
party.type == 'ORGANIZATION' && party.status == 'ACTIVE'

# Personal accounts only
party.type == 'PERSON'

# No eligibility restriction (all parties eligible)
true
```

### Attribute Schema

The `AttributeSchema` field holds a JSON Schema (draft 2020-12) that
validates the `Attributes` map on the product type definition itself
and on account-level attributes at creation time. This uses the
existing `santhosh-tekuri/jsonschema/v5` library already in the
dependency tree (used by the position tool's `SchemaValidator`).

**Validation points:**

1. **Product type creation/update**: When a product type is saved in
   DRAFT status, the `Attributes` map is validated against
   `AttributeSchema`. Invalid attributes prevent the draft from being
   saved.
2. **Account creation**: When an account is created with
   `product_type_code`, any account-level attributes supplied in the
   request are validated against the product type's `AttributeSchema`.

**Schema compilation:** Schemas are compiled and cached on first use,
following the existing `SchemaValidator` pattern (double-checked
locking, SHA256 cache key).

**Empty schema:** An empty or null `AttributeSchema` means no
validation -- all attributes are accepted. This preserves backwards
compatibility for product types that don't need structured attributes.

**Example schemas:**

```json
// Savings account: requires interest tier, optional overdraft limit
{
  "type": "object",
  "properties": {
    "interest_tier": {
      "type": "string",
      "enum": ["STANDARD", "PREMIUM", "INTRODUCTORY"]
    },
    "overdraft_limit": {
      "type": "string",
      "pattern": "^[0-9]+(\\.[0-9]{2})?$"
    }
  },
  "required": ["interest_tier"],
  "additionalProperties": { "type": "string" }
}
```

```json
// Energy inventory: requires grid zone and source type
{
  "type": "object",
  "properties": {
    "grid_zone": {
      "type": "string",
      "description": "Electrical grid zone identifier"
    },
    "source_type": {
      "type": "string",
      "enum": ["solar", "wind", "hydro", "nuclear", "gas", "coal"]
    },
    "settlement_period": {
      "type": "string",
      "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"
    }
  },
  "required": ["grid_zone"],
  "additionalProperties": { "type": "string" }
}
```

#### Manifest Surface

The manifest remains the authoring surface for product definitions:

```yaml
account_types:
  - code: CURRENT_GBP
    name: "GBP Current Account"
    behavior_class: CUSTOMER
    normal_balance: DEBIT
    instrument_code: GBP
    fiat_method: "forex-spot-v1"     # Resolved to UUID via Valuation Engine lookup
    policies:
      validation: "amount > 0 && amount <= 1000000"
      eligibility: "party.status == 'ACTIVE'"
    attribute_schema:
      type: object
      properties:
        overdraft_limit:
          type: string
          pattern: "^[0-9]+(\\.[0-9]{2})?$"
      additionalProperties:
        type: string

  - code: INVENTORY_KWH
    name: "kWh Inventory Account"
    behavior_class: INVENTORY
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
    repeated string suggestions = 6; // "Did you mean?" candidates
}
```

The compilation pipeline validates:

1. All CEL expressions compile successfully -- `ValidationCEL`,
   `BucketingCEL`, and `EligibilityCEL` (existing CEL compiler with
   environment-specific variable declarations)
2. `AttributeSchema` is valid JSON Schema (compilation via
   `santhosh-tekuri/jsonschema/v5`), and `Attributes` map validates
   against it
3. Saga scripts parse and pass dry-run validation (existing saga
   validator)
4. Cross-references are valid (instrument_code references a defined
   instrument, valuation method IDs reference existing methods)
5. Structured errors returned for iteration, including **"Did you
   mean?" suggestions** for unresolvable references. When an
   `instrument_code` or `valuation_method` reference fails to
   resolve, the validator computes Levenshtein distance against all
   ACTIVE resources of the same type and populates
   `ValidationError.suggestions` with the closest matches (up to 3,
   distance threshold <= 3). This accelerates the AI-as-configurator
   feedback loop and helps humans catch typos in manifest definitions
   (e.g., `instrument_code: "GBB"` → suggestion: `"GBP"`)

### Read-Through Cache Requirement

Consuming services (`services/current-account`,
`services/internal-bank-account`) must implement a read-through cache
for account type definitions, following the `LocalInstrumentCache`
pattern in `services/reference-data/cache/instrument_cache.go`.

Account creation and valuation resolution are hot paths. A synchronous
gRPC call to Reference Data for every `InitiateAccount` or
`EvaluateAssetValuation` request is not acceptable. The cache must:

- **Tenant-isolated**: Separate LRU cache per tenant (same as
  `instrument_cache.go`).
- **TTL with jitter**: 5-minute base TTL with 30-second jitter to
  prevent thundering herd on expiry.
- **Singleflight deduplication**: Concurrent requests for the same
  account type code collapse into a single gRPC call.
- **Precompiled CEL programs**: Cache the compiled CEL programs
  (`ValidationCEL`, `BucketingCEL`, `EligibilityCEL`) alongside the
  definition, not just the raw strings. This avoids re-compilation
  on every evaluation.
- **Precompiled JSON Schema**: Cache the compiled `AttributeSchema`
  validator alongside the definition.
- **Cache warming on startup**: Services prefetch ACTIVE account type
  definitions on startup, same as `cache/prefetch.go` does for
  instruments.
- **Platform default pinning**: Definitions with `is_system = true`
  (platform blueprints like `CURRENT_GBP`, `CLEARING_USD`) are
  prefetched into every tenant's cache at startup and assigned a
  24-hour TTL instead of the standard 5 minutes. Platform defaults
  are used by the vast majority of tenants and should never incur a
  gRPC round-trip on the hot path. These entries are refreshed in
  the background on TTL expiry rather than evicted, so a transient
  Reference Data outage does not cause cache misses for platform
  types.

The Reference Data service already provides the gRPC endpoint; the
consuming service wraps it with a `LocalAccountTypeCache` using the
same `hashicorp/golang-lru/v2` and `singleflight` libraries.

## Platform Blueprint Seed Data

System account types seeded during tenant provisioning:

| Code | BehaviorClass | Instrument | Normal Balance | Saga Prefix |
|------|---------------|------------|----------------|-------------|
| `CURRENT_GBP` | CUSTOMER | GBP | DEBIT | `CURRENT` |
| `CURRENT_EUR` | CUSTOMER | EUR | DEBIT | `CURRENT` |
| `CURRENT_USD` | CUSTOMER | USD | DEBIT | `CURRENT` |
| `SAVINGS_GBP` | CUSTOMER | GBP | DEBIT | `SAVINGS` |
| `CLEARING_GBP` | CLEARING | GBP | DEBIT | `CLEARING` |
| `NOSTRO_GBP` | NOSTRO | GBP | DEBIT | `NOSTRO` |
| `VOSTRO_GBP` | VOSTRO | GBP | CREDIT | `VOSTRO` |
| `HOLDING_GBP` | HOLDING | GBP | DEBIT | `HOLDING` |
| `SUSPENSE_GBP` | SUSPENSE | GBP | DEBIT | `SUSPENSE` |
| `REVENUE_GBP` | REVENUE | GBP | CREDIT | `REVENUE` |
| `EXPENSE_GBP` | EXPENSE | GBP | DEBIT | `EXPENSE` |
| `INVENTORY_KWH` | INVENTORY | KWH | DEBIT | `INVENTORY` |

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

1. A tenant can define a new account type (e.g.,
   `ENERGY_SETTLEMENT`) via manifest without any code changes or
   deployments.
2. Accounts created with `product_type_code = "ENERGY_SETTLEMENT"`
   automatically route to the correct Starlark saga.
3. The manifest compilation pipeline validates product definitions and
   returns structured errors when CEL expressions, cross-references,
   or valuation method references are invalid.
4. Existing accounts and services continue to work unchanged during migration (backwards compatible).
5. Platform blueprints are seeded during tenant provisioning and are read-only for tenants.
6. `InternalAccountType` and `common/v1/AccountType` proto enums are
   fully removed, replaced by dynamic registry lookups.
7. Valuation method templates defined on a product type are
   automatically seeded as ValuationFeatures when an account of that
   type is created -- no per-account manual configuration required.
8. Account creation with an `EligibilityCEL` expression rejects
   ineligible parties with a structured error.
9. Product type `Attributes` are validated against `AttributeSchema`
   at both product definition and account creation time.
10. `BehaviorClass`, `Status`, and `NormalBalance` are typed Go enums
    with `IsValid()` methods, proto enums with `buf/validate`, and SQL
    CHECK constraints -- three-layer validation with no stringly-typed
    gaps.
11. `ActivateAccountType` performs all cross-reference pre-checks
    (instrument, valuation methods, CEL compilation, schema validation,
    saga existence) and returns structured errors listing all failures.
12. `CreateDraft` and platform seeding are idempotent via `ON CONFLICT`
    and deterministic UUID generation respectively. Re-applying the
    same manifest produces identical state.

## Design for Correctness

This section defines invariants that the AccountTypeRegistry must enforce
to catch errors at compile/creation time rather than at runtime. These
requirements are derived from existing codebase patterns and known bugs
in the current account type system.

### Type Safety

#### Go Typed Enums with Exhaustive Validation

All string-based fields with a fixed set of allowed values must be
declared as Go typed string constants with an `IsValid()` method,
following the pattern in `internal-bank-account/domain/account_type.go`
and `reference-data/registry/instrument_status.go`.

```go
// Status represents the lifecycle status of an account type definition.
type Status string

const (
    StatusDraft      Status = "DRAFT"
    StatusActive     Status = "ACTIVE"
    StatusDeprecated Status = "DEPRECATED"
)

func (s Status) IsValid() bool {
    switch s {
    case StatusDraft, StatusActive, StatusDeprecated:
        return true
    }
    return false
}

// BehaviorClass represents a fixed system behavior category.
type BehaviorClass string

const (
    BehaviorClassCustomer  BehaviorClass = "CUSTOMER"
    BehaviorClassClearing  BehaviorClass = "CLEARING"
    BehaviorClassNostro    BehaviorClass = "NOSTRO"
    BehaviorClassVostro    BehaviorClass = "VOSTRO"
    BehaviorClassHolding   BehaviorClass = "HOLDING"
    BehaviorClassSuspense  BehaviorClass = "SUSPENSE"
    BehaviorClassRevenue   BehaviorClass = "REVENUE"
    BehaviorClassExpense   BehaviorClass = "EXPENSE"
    BehaviorClassInventory BehaviorClass = "INVENTORY"
)

func (b BehaviorClass) IsValid() bool { /* switch-based check */ }

// NormalBalance represents the accounting normal balance direction.
type NormalBalance string

const (
    NormalBalanceDebit  NormalBalance = "DEBIT"
    NormalBalanceCredit NormalBalance = "CREDIT"
)

func (n NormalBalance) IsValid() bool { /* switch-based check */ }
```

All three types are validated at the domain layer (constructor and
setter methods), at the persistence layer (SQL CHECK constraints),
and at the API layer (proto enum with `buf/validate`). Three-layer
validation ensures no invalid value can enter the system regardless
of entry point.

**Note on exhaustive linter**: The `exhaustive` linter in
`.golangci.yml` only covers `iota` enums, not typed string constants.
All `switch` statements on `Status`, `BehaviorClass`, and
`NormalBalance` must include a `default` case that returns an error
or panics in tests. This is enforced by code review until the linter
supports string-based exhaustive checks.

#### Case Normalization

All `Code`, `BehaviorClass`, `NormalBalance`, and `InstrumentCode`
values are stored in uppercase. The domain constructor normalises
input via `strings.ToUpper()` before validation. This prevents the
class of bugs where `"current"` and `"CURRENT"` are treated as
different values (a known issue in the current account service where
`account_type` is stored as lowercase `"current"` while internal
bank accounts use uppercase `"CLEARING"`).

#### Proto Enum for BehaviorClass

The proto definition for `BehaviorClass` uses a proto enum (not a
string) with `buf/validate` rules:

```protobuf
enum BehaviorClass {
    BEHAVIOR_CLASS_UNSPECIFIED = 0;
    BEHAVIOR_CLASS_CUSTOMER    = 1;
    BEHAVIOR_CLASS_CLEARING    = 2;
    // ... remaining values
}

// In the message:
BehaviorClass behavior_class = 6 [(buf.validate.field).enum = {
    defined_only: true, not_in: [0]
}];
```

This guarantees that gRPC callers cannot send an unrecognised
behavior class. The existing pattern is proven in
`internal_bank_account.proto::InternalAccountType`.

### Immutability Invariants

#### ACTIVE Definitions Are Immutable

Once a definition transitions to ACTIVE, no fields may be modified.
`UpdateDefinition` returns `ErrNotDraft` if `status != DRAFT`. This
follows the existing InstrumentRegistry pattern where
`updateDefinition` checks `if inst.Status != StatusDraft { return
ErrNotDraft }`.

To change an active product type: create a new version in DRAFT,
configure it, activate it, deprecate the old version with
`successor_id` pointing to the new one.

#### Write-Once Fields

The following fields are set once and cannot be changed after initial
creation, even in DRAFT status:

- `Code` -- immutable primary key (like instrument code)
- `IsSystem` -- platform blueprints cannot be reclassified
- `BehaviorClass` -- changing system behavior category after creation
  would invalidate all accounts created under the old category

Attempted modification returns `ErrFieldImmutable` with the field
name. This follows the `ErrSuccessorWriteOnce` pattern in the
instrument registry.

#### Version Pinning at Account Creation

When an account is created, it records an immutable
`(product_type_code, product_type_version)` pair. The account
operates under the rules of that version for its entire lifetime.
Product type deprecation prevents new account creation but does not
affect existing accounts.

### Fail-Fast: Activation Pre-Checks

The `ActivateAccountType` operation is the primary safety gate.
Unlike the current InstrumentRegistry (which only checks status),
the AccountTypeRegistry performs comprehensive cross-reference
validation at activation time:

1. **Instrument exists and is ACTIVE**: The `instrument_code`
   references a live instrument in the InstrumentRegistry.
2. **Fiat method exists** (if set): `fiat_method_id` and
   `fiat_method_version` reference an existing valuation method.
3. **Valuation method templates reference existing methods**: Each
   `ValuationMethodTemplate.ValuationMethodID` resolves to a live
   method.
4. **Input instruments exist and are ACTIVE**: Each template's
   `InputInstrument` references a live instrument.
5. **CEL expressions compile**: All three CEL fields (validation,
   bucketing, eligibility) compile successfully with their respective
   environments. While these are also checked at draft creation,
   re-validation at activation catches cases where the CEL
   environment has changed since draft creation.
6. **Attribute schema is valid**: `AttributeSchema` compiles as
   valid JSON Schema, and the definition's own `Attributes` map
   validates against it.
7. **Saga exists** (if prefix set): If `default_saga_prefix` is
   non-empty, at least one saga matching the
   `{prefix}.{operation}` pattern exists in the saga registry
   (platform or tenant).
8. **No duplicate ACTIVE code**: The partial unique index
   `uq_active_account_type_code` enforces this at the database
   level, but the activation check returns a descriptive error
   (`ErrActiveCodeExists`) before hitting the constraint.

Activation failure returns a structured error listing all failed
checks, not just the first one. This enables the manifest
compilation pipeline to report all issues in a single pass.

### Idempotency Contracts

#### CreateDraft: ON CONFLICT DO NOTHING

`CreateDraft` uses `INSERT ... ON CONFLICT (code, version) DO
NOTHING` and returns the existing definition if the row already
exists. This makes manifest re-application safe -- applying the
same manifest twice produces the same result. This follows the
pattern in `saga/postgres_registry.go` where duplicate saga
definitions are handled with conflict resolution.

#### ActivateAccountType: Idempotent Transition

Calling `ActivateAccountType` on an already-ACTIVE definition
returns success (nil error), not `ErrNotDraft`. This follows the
idempotent transition pattern in `ValuationFeature.Activate()`
where `if status == ACTIVE { return nil }`. This prevents saga
retries from failing on the activation step.

#### Deterministic UUID Generation for Seeding

Platform blueprint seeding uses `uuid.NewSHA1(namespaceUUID,
[]byte(code))` to generate deterministic IDs. Re-running the
seed operation produces the same UUIDs and hits `ON CONFLICT`
gracefully. This follows the existing pattern in the saga
definition seeder.

#### ValuationFeature Seeding: Upsert Semantics

When an account is created and ValuationFeatures are seeded from
templates, the seeding uses `INSERT ... ON CONFLICT
(account_id, instrument_code) DO NOTHING`. If a feature already
exists for that account+instrument pair (e.g., from a retry),
the existing feature is preserved.

### CEL Environment Scoping

Each CEL policy type has a dedicated compilation environment with
an explicit set of declared variables. This prevents the class of
bugs where a CEL expression compiles successfully against one
environment but fails at evaluation time because the expected
variables are not present.

| CEL Field | Environment Variables | Notes |
|-----------|----------------------|-------|
| `ValidationCEL` | `amount`, `attributes`, `timestamp`, `source` | Same as instrument validation |
| `BucketingCEL` | `attributes` | Same as instrument bucketing |
| `EligibilityCEL` | `party.type`, `party.status`, `party.external_reference_type`, `attributes` | New environment |

Attempting to compile a CEL expression that references an
undeclared variable (e.g., `party.type` in a `ValidationCEL`
field) returns a compilation error, not a runtime error. This is
the same behavior as the existing CEL compiler in
`services/reference-data/cel/compiler.go`.

### Concrete Bugs This Design Prevents

The following are real failure modes identified in the current
codebase that the AccountTypeRegistry eliminates:

| Bug | Current Cause | How Registry Prevents It |
|-----|---------------|--------------------------|
| Case mismatch (`"current"` vs `"CURRENT"`) | Current account stores lowercase, IBA stores uppercase | Case normalization at domain layer |
| No account type validation at creation | `InitiateCurrentAccount` accepts any string | Registry lookup at creation, reject unknown codes |
| Hardcoded `ACCOUNT_TYPE_CURRENT` in saga handlers | `saga_handlers.go:467` uses proto enum constant | Registry-based lookup replaces hardcoded value |
| AccountResolver divergence | Copy-pasted 3x across services with different safety checks | Single source of truth in registry |
| Missing instrument validation at account creation | Current account skips instrument check when client is nil | Activation pre-check ensures instrument exists |
| Stale valuation features | Per-account features manually configured, easy to forget | Product-type-level templates, auto-seeded |
| CEL variable mismatch | Expression compiles but fails at eval when variables differ | Environment-scoped compilation |
| Enum extension requires deployment | Adding a new account type needs code change + deploy | Registry entry via manifest, no deployment |

## Testing Strategy

- **Unit tests**: Domain model validation (typed enums, case
  normalization, immutability guards, write-once fields), lifecycle
  transitions (including idempotent activation), CEL compilation
  (validation, bucketing, eligibility environments -- including
  cross-environment variable rejection), attribute schema validation,
  optimistic locking, deterministic UUID generation. Following
  InstrumentRegistry test patterns.
- **Integration tests**: Testcontainer-based (CockroachDB). Full
  registry CRUD, multi-tenant isolation, platform blueprint seeding
  with ON CONFLICT idempotency, activation pre-check failures
  (invalid instrument, missing valuation method, CEL compilation
  error), partial unique index enforcement, SQL CHECK constraint
  coverage for BehaviorClass/NormalBalance/Status.
- **E2E tests**: Manifest apply with account type registration,
  account creation with product_type_code, saga routing verification,
  valuation feature seeding from templates, re-application
  idempotency (apply same manifest twice, verify identical state).

## References

- **InstrumentRegistry** (pattern template): `services/reference-data/registry/registry.go`
- **Saga registry**: `services/reference-data/saga/`
- **Manifest AccountTypeDefinition**: `api/proto/meridian/control_plane/v1/manifest.proto:165`
- **Applier handlers.yaml**: `services/control-plane/internal/applier/handlers.yaml:57`
- **CEL compiler**: `services/reference-data/cel/compiler.go`
- **ValuationFeature domain**: `shared/pkg/valuationfeature/`
- **ValuationEngine interface**: `shared/pkg/valuation/`
- **LocalInstrumentCache** (cache pattern template): `services/reference-data/cache/instrument_cache.go`
- **Cache prefetch**: `services/reference-data/cache/prefetch.go`
- **JSON Schema validator**: `cmd/position-tool/internal/validation/schema.go`
- **Party service proto**: `api/proto/meridian/party/v1/party.proto`
- **BIAN alignment PRD**: `.taskmaster/docs/prd-bian-alignment.md`
- **BIAN 13.0 Product Directory**: SD-CR-006 (external)
