# Database Migrations

This directory contains database migrations organized by schema, following BIAN service domain boundaries.

## Architecture

### Schema-per-Service Pattern

Each BIAN service domain has its own PostgreSQL schemas for business data and audit:

- **`current_account`**: Customer and Account models (BIAN Current Account domain)
- **`current_account_audit`**: Audit logs for current_account service
- **`position_keeping`**: Transaction models (BIAN Position Keeping domain)
- **`position_keeping_audit`**: Audit logs for position_keeping service

Each microservice owns both its business schema and audit schema, ensuring complete service isolation.

### Migration Organization

```text
migrations/
├── current_account/        # Current Account service
│   ├── 20251103174203_initial.sql         # Business tables (customers, accounts)
│   ├── 20251103180000_audit_system.sql    # Service-specific audit (current_account_audit)
│   └── atlas.sum
└── position_keeping/       # Position Keeping service
    ├── 20251103174231_initial.sql         # Business tables (transactions)
    ├── 20251103180000_audit_system.sql    # Service-specific audit (position_keeping_audit)
    └── atlas.sum
```

## Migration Tools

### Atlas (Automated Schema Migrations)

Atlas generates migrations from GORM models via the `cmd/atlas-loader` utility.

**Configuration files:**
- `atlas.current_account.hcl` - Current Account schema config
- `atlas.position_keeping.hcl` - Position Keeping schema config

### Manual SQL (Audit System)

The audit system uses raw SQL for:
- Stored procedures/functions
- Triggers
- Complex PostgreSQL features not supported by GORM

## Common Operations

### Generate New Migrations

**For all schemas:**
```bash
make migrate-diff-all
```

**For specific schema:**
```bash
make migrate-diff-current       # current_account schema
make migrate-diff-position      # position_keeping schema
```

### Apply Migrations

**Local development (via Tilt):**
```bash
tilt up
# Migrations apply automatically in order:
# 1. current_account (includes current_account_audit)
# 2. position_keeping (includes position_keeping_audit)
```

**Manual application:**
```bash
export DATABASE_URL="postgres://user:pass@host:5432/dbname"
make migrate-apply-all          # Apply all schemas (each includes its audit schema)
```

### Check Migration Status

```bash
export DATABASE_URL="postgres://user:pass@host:5432/dbname"
make migrate-status-all
```

### Lint Migrations

```bash
make migrate-lint-all
```

## Migration Order

Migrations must be applied in this order due to foreign key dependencies:

1. **current_account** - Customers, accounts, and current_account_audit schema
2. **position_keeping** - Transactions (references accounts) and position_keeping_audit schema

Each service migration includes both business tables and its audit schema, ensuring complete service isolation.

This order is enforced in:
- Tiltfile (via `resource_deps`)
- Makefile (`migrate-apply-all` target)
- CI/CD pipelines (see `.github/workflows/`)

## Audit System

### Overview

Each microservice has its own audit schema for complete isolation:

- **`current_account_audit.change_log`**: Logs changes to customers and accounts
- **`position_keeping_audit.change_log`**: Logs changes to transactions

All changes are automatically logged with:
- **What changed**: Table name, operation (INSERT/UPDATE/DELETE)
- **Who changed it**: `created_by`/`updated_by` from BaseModel
- **When changed**: Timestamp with timezone
- **What data changed**: JSONB diff of old vs new values

### Service-Specific Audit

Each service's audit schema is:
- **Isolated**: Only accessible by the owning service
- **Self-contained**: Includes all triggers, functions, and views
- **Independently deployable**: Service migrations include audit setup

Audit triggers are automatically attached during migration.

### Querying Audit History

**For current_account service:**
```sql
-- Get history for a specific customer or account
SELECT * FROM current_account_audit.get_record_history('record-uuid-here');

-- View human-readable change summary
SELECT * FROM current_account_audit.change_summary
WHERE table_full_name = 'current_account.customers'
ORDER BY changed_at DESC
LIMIT 100;

-- Find all changes by a user
SELECT * FROM current_account_audit.change_log
WHERE changed_by = 'user@example.com'
ORDER BY changed_at DESC;
```

**For position_keeping service:**
```sql
-- Get history for a specific transaction
SELECT * FROM position_keeping_audit.get_record_history('record-uuid-here');

-- View human-readable change summary
SELECT * FROM position_keeping_audit.change_summary
WHERE table_full_name = 'position_keeping.transactions'
ORDER BY changed_at DESC
LIMIT 100;
```

## BaseModel Fields

All domain models inherit from `BaseModel` which includes audit fields:

```go
type BaseModel struct {
    ID        uuid.UUID  // Primary key
    CreatedAt time.Time  // Creation timestamp
    CreatedBy string     // Who created this record (optional until auth context available)
    UpdatedAt time.Time  // Last update timestamp
    UpdatedBy string     // Who last updated this record (optional until auth context available)
    DeletedAt *time.Time // Soft delete timestamp
}
```

**Note**: `CreatedBy` and `UpdatedBy` are currently optional (nullable) fields. Once authentication/authorization context is available in the application layer, these should be populated from the current user context. The audit triggers will use these values to track who made changes.

## Schema Modifications

### Adding a New Table

1. Create GORM model in `internal/domain/models/`
2. Add `TableName()` method returning `schema.table_name`
3. Update `cmd/atlas-loader/main.go` to include model in appropriate schema filter
4. Generate migration:
   ```bash
   make migrate-diff-current  # or migrate-diff-position
   ```
5. Enable audit logging:
   - Add trigger to the service's audit migration (e.g., `migrations/current_account/YYYYMMDDHHMMSS_audit_system.sql`)
   - Or run: `SELECT current_account_audit.enable_audit_log('table_name');` (for current_account tables)
   - Or run: `SELECT position_keeping_audit.enable_audit_log('table_name');` (for position_keeping tables)

### Adding a New Schema

1. Create new Atlas config: `atlas.new_schema.hcl`
2. Update `cmd/atlas-loader/main.go` with new schema filter
3. Create migration directory: `migrations/new_schema/`
4. Update `Makefile` with new targets
5. Update `Tiltfile` with new migration resource
6. Add to audit system if needed

## Troubleshooting

### Migration Conflicts

**Symptom**: Atlas detects unexpected schema drift

**Solution**:
```bash
# Check current state
make migrate-status-all

# Verify checksums
make migrate-hash-all

# If checksums mismatch, regenerate sum files
atlas migrate hash --env local --config file://atlas.current_account.hcl
```

### Audit Triggers Not Firing

**Check trigger exists:**
```sql
SELECT schemaname, tablename, tgname, tgenabled
FROM pg_trigger t
JOIN pg_class c ON t.tgrelid = c.oid
JOIN pg_namespace n ON c.relnamespace = n.oid
WHERE tgname LIKE 'audit_%';
```

**Manually attach trigger:**
```sql
SELECT audit.enable_audit_log('schema_name', 'table_name');
```

### Foreign Key Constraint Errors

Remember:
- `position_keeping` schema references `current_account.accounts`
- Always apply `current_account` migrations before `position_keeping`

## Production Considerations

### Pre-Deployment Checklist

- [ ] Run `make migrate-lint-all` to check for destructive changes
- [ ] Test migrations on staging environment first
- [ ] Verify `make migrate-status-all` shows expected state
- [ ] Ensure `CreatedBy`/`UpdatedBy` fields are set by application
- [ ] Audit log retention policy configured (see `audit.change_log` table)

### Rollback Strategy

Atlas migrations are forward-only. For rollbacks:

1. Create a new "undo" migration
2. Apply it like any other migration
3. Never edit existing migration files

### Performance

Audit triggers add minimal overhead:
- Single INSERT per change
- Asynchronous to business transaction
- JSONB compression reduces storage

For high-volume tables, consider:
- Partitioning `audit.change_log` by timestamp
- Archiving old audit records
- Sampling (log every Nth change)

## Further Reading

- [Atlas Documentation](https://atlasgo.io/)
- [GORM Schema Documentation](https://gorm.io/docs/models.html)
- [PostgreSQL Triggers](https://www.postgresql.org/docs/current/triggers.html)
- [BIAN Service Landscape](https://bian.org/)
