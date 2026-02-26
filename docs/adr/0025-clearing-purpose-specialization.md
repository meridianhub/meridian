---
name: adr-025-clearing-purpose-specialisation
description: Distinguish clearing accounts by purpose (deposit/withdrawal/settlement/general) for precise account resolution
triggers:
  - Resolving clearing accounts for specific operations
  - Creating purpose-specific clearing accounts
  - Querying clearing accounts by operation type
instructions: |
  Use the ClearingPurpose enum to distinguish clearing accounts by their operational purpose.
  Set clearing_purpose field only when account_type is CLEARING. Filter clearing accounts
  by purpose using clearing_purpose_filter in ListInternalAccounts RPC.
---

# 25. Clearing Purpose Specialisation

Date: 2026-01-16

## Status

Accepted

## Context

The Internal Account service supports multiple clearing accounts per instrument (e.g., CLR-GBP-DEPOSIT, CLR-GBP-WITHDRAW). Initially, these accounts were distinguished only by naming convention. This created ambiguity when services needed to programmatically resolve the correct clearing account for a specific operation.

### Problems with Naming-Only Approach

- **No type-safe resolution**: Services relied on string parsing of account codes to distinguish deposit vs withdrawal clearing accounts
- **No database-level filtering**: Cannot query "all deposit clearing accounts for GBP" without pattern matching on account_code
- **Validation gaps**: No enforcement that clearing accounts are used for their intended purpose
- **Inconsistent conventions**: Different teams might use different naming patterns (CLR-DEPOSIT-GBP vs CLR-GBP-DEPOSIT)

### Use Cases Requiring Purpose Distinction

1. **Current Account Service**: When processing a customer deposit, needs the deposit clearing account specifically
2. **Payment Service**: Withdrawal operations need the withdrawal clearing account
3. **Settlement Service**: Settlement operations need settlement clearing accounts
4. **Account Resolution**: Services need to programmatically select the correct clearing account without parsing strings

## Decision Drivers

* Type safety for clearing account resolution
* Database-queryable clearing account purposes
* Backward compatibility with existing clearing accounts
* BIAN compliance for internal account types
* Support for deposit/withdrawal/settlement/general patterns
* Clear validation rules for clearing_purpose usage

## Considered Options

1. **ClearingPurpose enum field** (chosen)
2. Account subtype string field
3. Account tags/labels system
4. Separate tables per clearing purpose

## Decision Outcome

Chosen option: "ClearingPurpose enum field", because it provides type safety, database filtering, and clear semantics while maintaining backward compatibility with existing accounts.

### Implementation

**Proto definition:**

```proto
enum ClearingPurpose {
  CLEARING_PURPOSE_UNSPECIFIED = 0;
  CLEARING_PURPOSE_DEPOSIT = 1;
  CLEARING_PURPOSE_WITHDRAWAL = 2;
  CLEARING_PURPOSE_SETTLEMENT = 3;
  CLEARING_PURPOSE_GENERAL = 4;
}

message InternalAccountFacility {
  // ... existing fields ...
  ClearingPurpose clearing_purpose = 12;
}

message ListInternalAccountsRequest {
  // ... existing fields ...
  ClearingPurpose clearing_purpose_filter = 5;
}
```

**Database schema:**

```sql
ALTER TABLE internal_accounts
ADD COLUMN clearing_purpose VARCHAR(32);

-- Constraint ensures clearing_purpose is set for CLEARING accounts
-- and UNSPECIFIED for non-CLEARING accounts
ALTER TABLE internal_accounts
ADD CONSTRAINT chk_clearing_purpose_consistency
CHECK (
  (account_type = 'CLEARING' AND clearing_purpose IS NOT NULL AND clearing_purpose != 'UNSPECIFIED')
  OR
  (account_type != 'CLEARING' AND (clearing_purpose IS NULL OR clearing_purpose = 'UNSPECIFIED'))
);
```

**Validation rules:**

- For CLEARING accounts: `clearing_purpose` must be non-UNSPECIFIED (DEPOSIT, WITHDRAWAL, SETTLEMENT, or GENERAL)
- For non-CLEARING accounts: `clearing_purpose` must be UNSPECIFIED or omitted
- Enforced at both proto validation layer and database constraint

### Positive Consequences

* **Type-safe resolution**: Services can filter by ClearingPurpose enum instead of parsing account codes
* **Database-level filtering**: `clearing_purpose_filter` in ListInternalAccounts enables efficient queries
* **Clear semantics**: Unambiguous distinction between deposit/withdrawal/settlement operations
* **Validation enforcement**: Cannot accidentally use a deposit clearing account for withdrawals
* **Backward compatible**: Existing clearing accounts can be migrated with appropriate purpose assignment
* **Future-proof**: Additional purposes (if needed) can be added as new enum values

### Negative Consequences

* **Additional field complexity**: One more field to manage in the proto and database
* **Migration required**: Existing clearing accounts need purpose backfilled via database migration
* **Coupled constraint**: Must maintain consistency between account_type and clearing_purpose

## Pros and Cons of the Options

### ClearingPurpose Enum Field (Chosen)

* Good, because provides compile-time type safety
* Good, because supports efficient database filtering via indexed column
* Good, because enum values are self-documenting
* Good, because validation rules are clear and enforceable
* Good, because backward compatible with null-to-enum migration
* Bad, because requires database migration for existing data
* Bad, because adds cross-field validation complexity

### Account Subtype String Field

A generic `subtype` string field that could hold any value.

* Good, because more flexible (any string value)
* Good, because simpler schema (no enum)
* Bad, because no compile-time type safety
* Bad, because prone to typos ("DEPSOIT" vs "DEPOSIT")
* Bad, because difficult to validate at database level
* Bad, because unclear what values are valid

### Account Tags/Labels System

A many-to-many tags system where accounts can have multiple labels.

* Good, because highly flexible
* Good, because supports multiple tags per account
* Bad, because overly complex for this use case (only need one purpose per account)
* Bad, because difficult to query efficiently
* Bad, because no type safety
* Bad, because tags proliferation without governance

### Separate Tables Per Clearing Purpose

Create separate tables: `deposit_clearing_accounts`, `withdrawal_clearing_accounts`, etc.

* Good, because complete type separation
* Good, because no validation needed (table choice implies purpose)
* Bad, because data duplication across tables
* Bad, because complex queries spanning purposes
* Bad, because difficult to add new purposes
* Bad, because breaks Internal Account service domain model

## Links

* [ADR-0024: Internal Account Service](0024-internal-account-service.md)
* [Internal Account README](../../services/internal-account/README.md)
* [Proto Definitions](../../api/proto/meridian/internal_account/v1/internal_account.proto)
* [BIAN v13.0 Internal Account Service Domain](https://bian.org/semantic-apis/internal-account/)

## Notes

### Default Account Templates

The provisioning system creates purpose-specific clearing accounts per instrument when a tenant is initialized:

| Account Code | Purpose | Description |
|--------------|---------|-------------|
| `CLR-{INSTRUMENT}-DEPOSIT` | DEPOSIT | For deposit operations |
| `CLR-{INSTRUMENT}-WITHDRAW` | WITHDRAWAL | For withdrawal operations |
| `CLR-{INSTRUMENT}-SETTLE` | SETTLEMENT | For settlement operations |

### Migration Strategy

For existing clearing accounts without a purpose:

1. Analyze account_code patterns to infer purpose (DEPOSIT, WITHDRAW, SETTLE keywords)
2. Default remaining CLEARING accounts to GENERAL purpose
3. Run database migration to backfill clearing_purpose column
4. Enable constraint enforcement after migration completes

### Future Considerations

If clearing accounts need multiple purposes simultaneously (e.g., an account that handles both deposits AND settlements), the current design would need to evolve. Options include:

1. Add MULTI_PURPOSE enum value
2. Migrate to a tags-based approach
3. Create composite clearing accounts

Current analysis suggests single-purpose accounts are sufficient for Meridian's operational model.

### Refactoring Trigger

Reconsider this design if:

- More than 2 clearing accounts per instrument-purpose combination are needed
- Clearing accounts need to serve multiple purposes
- Purpose categories exceed 10+ distinct values
