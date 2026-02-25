---
name: prd-asset-agnostic-accounts
description: >-
  Generalize Current Account and Internal Bank Account services to be
  truly asset-agnostic. Replace banking-specific fields (IBAN, Currency,
  overdraft) with instrument-aware equivalents and drop "Bank" from
  Internal Account naming.
triggers:
  - Working on current account or internal account field generalization
  - Replacing currency fields with instrument_code and dimension
  - Renaming internal bank account to internal account
  - Making account services support non-fiat asset classes
  - Generalizing IBAN to external_identifier
instructions: |
  Approach: generalize fields, keep BIAN service domain names. The BIAN
  classification provides taxonomy value -- the problem is field-level
  assumptions that force banking concepts on non-banking use cases.
  Phase 1 (high): Current Account field generalization.
  Phase 2 (medium): Drop "Bank" from Internal Account naming.
  Phase 3 (low): Deprecate common Currency enum.
  All changes must be backwards compatible (dual fields, dual handlers).
---

# Asset-Agnostic Account Services

> **Status**: Not Started
> **Task Master Tag**: `asset-agnostic-accounts`
> **Last Updated**: 2026-02-25
> **Related PRDs**: [Internal Bank Account](002-internal-bank-account.md),
> [Universal Asset System](001-universal-asset-system.md),
> [Product Directory](023-product-directory.md)

## Problem Statement

Meridian positions itself as the "Operating System for the Real-World Economy"
-- handling not just fiat currency but energy (kWh), compute (GPU-hours),
carbon credits, and any future asset class. Yet the two core account services
carry banking-specific terminology and field assumptions that create conceptual
friction for non-banking tenants:

1. **Current Account Service** -- deeply banking-specific: `currency` CHAR(3),
   `account_identification` (IBAN-validated), overdraft facility hard-coded
   into the domain. A solar co-op or AI compute platform would not recognise
   these as their concepts.

2. **Internal Bank Account Service** -- carries "Bank" in every layer (service
   directory, proto package, DB table name, domain model) despite already being
   generalized internally with `instrument_code` + `dimension`. Correspondent
   details use NOSTRO/VOSTRO/SWIFT terminology.

A non-bank tenant sees "Current Account" and "Internal Bank Account" in the
API and immediately assumes this is banking-only software. The field-level
assumptions reinforce that perception.

## Approach

Generalize the *fields*, keep the BIAN service domain names. The BIAN
classification ("Current Account", "Internal Account") provides taxonomy
value for compliance-focused tenants. The problem is not the service name
-- it is the field-level assumptions that force banking concepts onto
non-banking use cases.

## Current State

### Current Account Service

| Layer | Banking-Specific Element | Details |
|-------|--------------------------|---------|
| Domain | `accountIdentification` | IBAN format, regex-validated |
| Domain | `balance`, `availableBalance` | `Money` type (currency-only) |
| Domain | `overdraftLimit` etc. | Hard-coded overdraft facility |
| Entity | `Currency` | `CHAR(3)` ISO 4217, no dimension |
| Entity | `AccountType` | `VARCHAR(50)` -- "current", "savings" |
| Proto | `base_currency` | `Currency` enum (GBP, USD, EUR only) |
| Proto | `account_identification` | IBAN pattern validation |
| Proto | `OverdraftConfiguration` | Nested message in facility |
| Migration | `currency` column | Default `'GBP'` |
| Starlark | `current_account.*` | Handler prefix |

### Internal Bank Account Service

| Layer | Banking-Specific Element | Details |
|-------|--------------------------|---------|
| Directory | `internal-bank-account/` | "Bank" in path |
| Domain | `InternalBankAccount` struct | "Bank" in type name |
| Entity | `internal_bank_account` table | "Bank" in table name |
| Proto | `internal_bank_account.v1` | "bank_account" in package |
| Proto | `InternalBankAccountFacility` | "Bank" in message name |
| Proto | `CorrespondentBankDetails` | "Bank" in type |
| Proto | `swift_code` field | SWIFT/BIC specific |
| Proto | `NOSTRO/VOSTRO` enums | Banking correspondent terms |
| Starlark | `internal_bank_account.*` | Handler prefix |
| Migration | `correspondent_bank_*` cols | "Bank" in column names |

### What Internal Account Already Does Right

The internal account service already has the generalized model that
current account lacks:

- `instrument_code` (VARCHAR 32) -- any instrument, not just currency
- `dimension` (VARCHAR 20) -- CURRENCY, ENERGY, COMPUTE, CARBON, etc.
- `behavior_class` in proto -- abstract account classification
- `AccountType` enum -- CLEARING, NOSTRO, VOSTRO, HOLDING, etc.
- Product Directory integration for configurable account types

## Proposed Changes

### Phase 1: Current Account -- Field Generalization (High Priority)

Align the current account service with the internal account's multi-asset
model. This is the high-value change -- it makes the customer-facing
account service truly asset-agnostic.

#### 1.1 Replace `currency` with `instrument_code` + `dimension`

**Database:**

- Add `instrument_code VARCHAR(32) NOT NULL` column
  (backfill from `currency` via instrument code mapping)
- Add `dimension VARCHAR(20) NOT NULL` column
  (backfill all existing as `'CURRENCY'`)
- Deprecate `currency` column
  (keep for backwards compat during migration, drop later)

**Domain:**

- Replace `Money` balance types with instrument-aware equivalents
  (or defer to Position Keeping, which already handles this)
- Add `instrumentCode` and `dimension` fields

**Proto:**

- Replace `base_currency` (Currency enum) with `instrument_code` (string)
  -- dimension derivable from reference data
- Update `InitiateCurrentAccountRequest` to accept `instrument_code`
  instead of `base_currency`

#### 1.2 Generalize `account_identification` (IBAN -> External Identifier)

**Database:**

- Rename column: `account_identification` -> `external_identifier`
- Widen from VARCHAR(34) to VARCHAR(255)
- Validation rules move to product type configuration
  (IBAN regex for banking products, MPAN for energy, etc.)

**Domain:**

- Rename field: `accountIdentification` -> `externalIdentifier`
- Remove hard-coded IBAN validation from domain constructor
- Validation delegated to product type's CEL validation rules

**Proto:**

- Rename field: `account_identification` -> `external_identifier`
- Remove IBAN pattern constraint from proto validation
- Document that format is product-type-dependent

#### 1.3 Make Overdraft a Product-Type Behaviour (Not Hard-Coded)

**Domain:**

- Extract `overdraftLimit`, `overdraftEnabled`, `overdraftRate`
  from the core domain model
- Move to `attributes` map or a dedicated credit facility sub-entity
  controlled by product type
- Overdraft becomes one possible "credit facility" behaviour,
  configured via Product Directory

**Proto:**

- Deprecate `OverdraftConfiguration` as a top-level field on
  `CurrentAccountFacility`
- Add generic `CreditFacility` or move to product-type-driven attributes

**Database:**

- Existing `overdraft_limit`, `overdraft_rate` columns remain
  for backwards compat
- New accounts use product-type-driven configuration

#### 1.4 Generalize Account Type Vocabulary

**Current:** `account_type` uses banking terms -- "current", "savings"

**Proposed:** Account type is derived from `product_type_code` in the
Product Directory. The `account_type` column becomes a legacy field.
Product types define the behaviour class (just as internal accounts
already work).

### Phase 2: Internal Account -- Drop "Bank" from Naming (Medium Priority)

Remove "Bank" from the service identity. The internal model is already
asset-agnostic; the naming just hasn't caught up.

#### 2.1 Rename Service Directory and Package

| Current | Proposed |
|---------|----------|
| `services/internal-bank-account/` | `services/internal-account/` |
| `meridian.internal_bank_account.v1` | `meridian.internal_account.v1` |
| `InternalBankAccountFacility` | `InternalAccountFacility` |
| `InternalBankAccount` (Go struct) | `InternalAccount` (Go struct) |
| `internal_bank_account` (DB table) | Keep as-is or rename with view |
| `internal_bank_account.*` (Starlark) | `internal_account.*` |

#### 2.2 Generalize Correspondent Terminology

| Current | Proposed |
|---------|----------|
| `CorrespondentBankDetails` | `CounterpartyDetails` |
| `correspondent_bank_id` | `counterparty_id` |
| `correspondent_bank_name` | `counterparty_name` |
| `correspondent_external_ref` | `counterparty_external_ref` |
| `swift_code` | Move to `attributes` (product-type-specific) |
| `CORRESPONDENT_TYPE_NOSTRO` | `COUNTERPARTY_TYPE_NOSTRO` |

Note: NOSTRO/VOSTRO are useful beyond banking (any correspondent/mirror
account pattern), so the behaviour class names can stay. The "Bank"
prefix on the container type is what needs to go.

### Phase 3: Common Proto Generalization (Low Priority, Opportunistic)

#### 3.1 Currency Enum -> Instrument Reference

The `meridian.common.v1.Currency` enum is inherently limited (only 7
currencies). Services should reference instruments by code string, not
by enum. This is already the pattern in internal accounts and position
keeping.

- Deprecate `Currency` enum in common proto
- Services that still use it (current account) migrate to
  `instrument_code` string in Phase 1
- `MoneyAmount` wrapping `google.type.Money` remains valid for
  currency-denominated amounts but should not be the only representation

## Cross-Cutting Concerns

### Proto Backwards Compatibility

All proto field renames MUST follow gRPC backwards compatibility:

- Add new fields alongside old ones
- Mark old fields as `deprecated = true`
- Old field numbers are NEVER reused
- Both old and new fields work during transition period

### Starlark Handler Migration

Handler prefix change (`internal_bank_account.*` -> `internal_account.*`)
requires:

- Dual-registration during transition (both prefixes route to same handler)
- Existing saga scripts continue to work with old prefix
- New saga scripts use new prefix
- Deprecation warning logged for old prefix usage

### Database Migration Strategy

- New columns added alongside old ones
- Backfill migration populates new columns from old data
- Application reads from new columns, writes to both during transition
- Old columns dropped in a future cleanup migration (separate PRD)

### Reference Data Alignment

Current account creation should resolve `instrument_code` against the
Reference Data service to derive `dimension`, just as internal accounts
already do. This ensures consistency.

## Success Criteria

1. A new current account can be created with `instrument_code: "KWH"`
   and `dimension: "ENERGY"` -- no currency assumption
2. A new current account can use a non-IBAN `external_identifier`
   (e.g., MPAN meter ID)
3. Internal account API references `InternalAccountFacility` (no "Bank")
   in proto
4. All existing banking tenants continue to work unchanged
   (backwards compatible)
5. Starlark sagas written against old handler prefixes still execute
6. No data loss during migration -- all existing accounts retain data

## Out of Scope

- Renaming "Current Account" service itself to "Customer Account" --
  this is a BIAN service domain name and provides taxonomy value. The
  fields are the problem, not the BIAN classification.
- Removing NOSTRO/VOSTRO as behaviour classes -- these are useful
  patterns beyond banking.
- Changing Position Keeping or Financial Accounting services -- they
  are already instrument-agnostic.
- UI changes -- the frontend will adapt to proto changes naturally.

## Risk Assessment

| Risk | Mitigation |
|------|-----------|
| Proto renames break clients | Dual-field with deprecation |
| Handler prefix breaks sagas | Dual-registration + warnings |
| DB table rename | Keep table name, change app layer |
| Large blast radius | Phase: fields first, naming second |
| Test fixtures assume banking | Update per phase, self-contained |
