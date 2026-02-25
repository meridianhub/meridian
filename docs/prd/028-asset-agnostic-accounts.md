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
  System is pre-production so all changes are direct refactors -- no
  deprecation periods, dual fields, or backwards compatibility shims.
  Phase 1 (high): Current Account field generalization.
  Phase 2 (medium): Drop "Bank" from Internal Account naming.
  Phase 3 (low): Remove common Currency enum.
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

**Pre-production system** -- no live clients, no deployed tenants. All
changes are direct refactors with no deprecation periods or backwards
compatibility shims required.

## Current State

### Current Account Service

| Layer | Banking-Specific Element | Details |
|-------|--------------------------|---------|
| Domain | `accountIdentification` | IBAN format, regex-validated |
| Domain | `balance`, `availableBalance` | `Money` type (currency-only) |
| Domain | `overdraftLimit` etc. | Hard-coded overdraft facility |
| Entity | `Currency` | `CHAR(3)` ISO 4217, no dimension |
| Entity | `AccountType` | `VARCHAR(50)` -- "current", "savings" |
| Proto | `base_currency` | `Currency` enum (7 fiat codes only) |
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

- Replace `currency CHAR(3)` column with `instrument_code VARCHAR(32)`
- Add `dimension VARCHAR(20) NOT NULL` column
- Drop `currency` column and its default `'GBP'`

**Domain:**

- Replace `Money` balance types with instrument-aware equivalents
  (or defer to Position Keeping, which already handles this)
- Replace currency field with `instrumentCode` and `dimension`
- Migrate `cadomain.Money` cross-service usage (payment-order imports
  this type) to a shared instrument-aware type in `shared/pkg/`

**Proto:**

- Replace `base_currency` (Currency enum) with `instrument_code` (string)
- `dimension` resolved at write-time from Reference Data and persisted
  in the DB column (not re-derived on every read)
- Update `InitiateCurrentAccountRequest` to accept `instrument_code`
  instead of `base_currency` -- `dimension` is not a request field,
  it is derived by the service and returned in responses

#### 1.2 Generalize `account_identification` (IBAN -> External Identifier)

**Database:**

- Rename column: `account_identification` -> `external_identifier`
- Widen from VARCHAR(34) to VARCHAR(255)

**Domain:**

- Rename field: `accountIdentification` -> `externalIdentifier`
- Remove hard-coded IBAN validation from domain constructor
- Validation delegated to product type's CEL validation rules
  (IBAN regex for banking products, MPAN for energy, etc.)

**Proto:**

- Rename field: `account_identification` -> `external_identifier`
- Remove IBAN pattern constraint from proto validation
- Document that format is product-type-dependent

#### 1.3 Remove Overdraft as Hard-Coded Facility

**Domain:**

- Remove `overdraftLimit`, `overdraftEnabled`, `overdraftRate`
  from the core domain model
- Credit facility behaviour driven by Product Directory configuration
- Overdraft becomes one possible product-type behaviour, not a core
  account concept

**Proto:**

- Remove `OverdraftConfiguration` from `CurrentAccountFacility`
- Credit facility configured via product-type attributes

**Database:**

- Drop `overdraft_limit`, `overdraft_enabled`, `overdraft_rate` columns
- Product-type-driven configuration replaces hard-coded fields

#### 1.4 Generalize Account Type Vocabulary

**Current:** `account_type` uses banking terms -- "current", "savings"

**Proposed:** Replace `account_type` with `behavior_class` derived from
`product_type_code` in the Product Directory. This mirrors the internal
account pattern where `behavior_class` is the abstract classification.

**Prerequisite:** Product Directory (PRD-023) must be operational before
this subtask executes, since behaviour class resolution depends on it.

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
| `internal_bank_account` (DB table) | `internal_account` |
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
| `CORRESPONDENT_TYPE_VOSTRO` | `COUNTERPARTY_TYPE_VOSTRO` |

Note: NOSTRO/VOSTRO are useful beyond banking (any correspondent/mirror
account pattern), so the behaviour class names can stay. The "Bank"
prefix on the container type is what needs to go.

### Phase 3: Common Proto Cleanup (Low Priority, Opportunistic)

#### 3.1 Remove Currency Enum

The `meridian.common.v1.Currency` enum is inherently limited (only 7
currencies). Services should reference instruments by code string, not
by enum. This is already the pattern in internal accounts and position
keeping.

- Remove `Currency` enum from common proto
- All services use `instrument_code` string references
- `MoneyAmount` wrapping `google.type.Money` remains valid for
  currency-denominated amounts but should not be the only representation

## Cross-Cutting Concerns

### Starlark Handler Migration

Handler prefix `internal_bank_account.*` -> `internal_account.*`:
update all registered handlers and all existing saga scripts that
reference the old prefix. Since the system is pre-production, update
all references in a single pass.

### Database Migration Strategy

- Write new Atlas migrations that rename/drop/add columns directly
- No backfill needed for pre-production data (test data can be
  re-seeded)
- Update GORM entity structs to match new schema

### Reference Data Alignment

Current account creation resolves `instrument_code` against the
Reference Data service to derive `dimension` at write-time (same
pattern as internal accounts). The resolved `dimension` is persisted
in the DB column so reads have no runtime dependency on Reference Data.

## Success Criteria

1. A new current account can be created with `instrument_code: "KWH"`
   -- service derives `dimension: "ENERGY"` from Reference Data and
   returns it in the response. No currency assumption.
2. A new current account can use a non-IBAN `external_identifier`
   (e.g., MPAN meter ID)
3. Internal account API references `InternalAccountFacility` (no "Bank")
   in proto
4. No banking-specific fields remain hard-coded in domain models
5. All tests pass with updated field names and types
6. Starlark saga scripts use new handler prefixes

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
| Large blast radius across codebase | Phase: fields first, naming second |
| `cadomain.Money` cross-service dep | Migrate to shared type in Phase 1 |
| Product Directory not yet operational | Phase 1.4 has explicit prerequisite |
| Test fixtures assume banking fields | Update alongside code changes per phase |
| DB table rename breaks references | Rename in migrations + all Go references |
| Starlark scripts use old prefixes | Find-and-replace all saga definitions |
