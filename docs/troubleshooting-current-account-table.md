# Troubleshooting: current-account Service Table Name Mismatch

## Issue

The `current-account` service is in CrashLoopBackOff with repeated errors:

```text
ERROR: relation "current_accounts" does not exist (SQLSTATE 42P01)
SELECT * FROM "current_accounts" WHERE account_id = 'health-check-probe'
```

## Root Cause

The application code is looking for a table named `current_accounts` (plural), but
the database schema creates it as `accounts` within the `current_account` schema.

**Actual schema structure:**

```sql
meridian=> SHOW TABLES;
schema_name       | table_name
------------------+-----------
current_account   | accounts     ← Table is "accounts"
current_account   | customers
```

**Application query:**

```sql
SELECT * FROM "current_accounts" WHERE account_id = ...
                  ^^^^^^^^^^^^^^^^
                  Looking for "current_accounts" (doesn't exist)
```

## Expected Behavior

One of these must be true:

1. **Option A**: Application queries `current_account.accounts`
2. **Option B**: Migration creates table named `current_accounts`

## Investigation Needed

1. Check GORM model tag in repository code:
   - File: `internal/current-account/adapters/persistence/repository.go:91`
   - Look for `gorm:"table:current_accounts"` tag
   - Should be `gorm:"table:accounts"` or include schema `current_account.accounts`

2. Check Atlas migration files:
   - Location: `migrations/current_account/`
   - Verify table name in CREATE TABLE statements

3. Check if search_path is set correctly for the schema

## Temporary Workaround

None available - this requires code or migration changes.

## Resolution

This is an application-level bug that needs to be fixed in a separate PR.
The issue is outside the scope of Tilt configuration improvements.

**Recommended fix location**: Repository code or migration files, not Tilt config.

## Related Files

- Application: `internal/current-account/adapters/persistence/repository.go`
- Migrations: `migrations/current_account/*.sql`
- Atlas config: `atlas.current_account.hcl`
