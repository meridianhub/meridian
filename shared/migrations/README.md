---
name: database-migrations
description: Atlas and SQL migrations for schema-per-service architecture
triggers:
  - Database schema migrations
  - Atlas migration generation and application
  - Audit system setup and triggers
  - Schema-per-service patterns
  - Migration troubleshooting
instructions: |
  Migrations use Atlas for GORM models and raw SQL for audit triggers.
  Generate migrations: make migrate-diff-all
  Apply migrations: make migrate-apply-all (or via Tilt automatically)
  Each service has business + audit schemas for complete isolation.
---

# Database Migrations

This directory contains database migrations organized by schema, following BIAN service domain boundaries.

## Architecture

### Schema-per-Service Pattern

Each BIAN service domain has its own PostgreSQL schemas for business data and audit:

- **`_audit_factory`**: Shared audit infrastructure (reusable factory for all services)
- **`current_account`**: Customer and Account models (BIAN Current Account domain)
- **`current_account_audit`**: Audit logs for current_account service
- **`position_keeping`**: Transaction models (BIAN Position Keeping domain)
- **`position_keeping_audit`**: Audit logs for position_keeping service

Each microservice owns both its business schema and audit schema, ensuring complete service isolation. The shared
`_audit_factory` schema provides reusable audit infrastructure to eliminate duplication.

### Migration Organisation

```text
migrations/
├── shared/                 # Shared infrastructure (audit factory)
│   ├── 20251103190000_audit_factory.sql    # Reusable audit creation procedures
│   └── atlas.sum
├── current_account/        # Current Account service
│   ├── 20251103174203_initial.sql         # Business tables (customers, accounts)
│   ├── 20251103180000_audit_system.sql    # Initializes current_account_audit via factory
│   └── atlas.sum
└── position_keeping/       # Position Keeping service
    ├── 20251103174231_initial.sql         # Business tables (transactions)
    ├── 20251103180000_audit_system.sql    # Initializes position_keeping_audit via factory
    └── atlas.sum
```

**Migration Application Order:**

1. `shared/` - Creates `_audit_factory` schema with reusable procedures
2. `current_account/` - Creates business + audit schemas
3. `position_keeping/` - Creates business + audit schemas (depends on current_account for FKs)

## Migration Tools

### Atlas (Automated Schema Migrations)

Atlas generates migrations from GORM models via the `cmd/atlas-loader` utility.

**Configuration files:**

- `atlas/current_account/atlas.hcl` - Current Account schema config
- `atlas/position_keeping/atlas.hcl` - Position Keeping schema config
- `atlas/financial_accounting/atlas.hcl` - Financial Accounting schema config
- `atlas/payment_order/atlas.hcl` - Payment Order schema config
- `atlas/shared/atlas.hcl` - Shared audit factory schema config

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

The audit system uses a **shared factory pattern** to eliminate code duplication while maintaining service isolation:

- **`_audit_factory` schema**: Contains reusable procedures (`init_service_audit()`) that create audit infrastructure
- **Per-service audit schemas**: Each service gets its own audit schema (e.g., `current_account_audit`,
`position_keeping_audit`)
- **Zero duplication**: All audit logic lives in one place, called by each service migration

Each service audit schema includes:

- **`change_log` table**: JSONB-based change tracking with indexes
- **`log_change()` trigger function**: Captures INSERT/UPDATE/DELETE operations
- **`enable_audit_log()` helper**: Attaches triggers to tables
- **`change_summary` view**: Human-readable change history
- **`get_record_history()` function**: Query changes for specific records

All changes are automatically logged with:

- **What changed**: Table name, operation (INSERT/UPDATE/DELETE)
- **Who changed it**: `created_by`/`updated_by` from BaseModel
- **When changed**: Timestamp with timezone
- **What data changed**: JSONB diff of old vs new values

### Audit Factory Usage

To add audit to a new service:

```sql
-- In your service's audit migration file:
SELECT _audit_factory.init_service_audit(
    'your_service',              -- Service schema to audit
    'your_service_audit',        -- Audit schema name
    ARRAY['table1', 'table2']    -- Tables to attach triggers to
);
```

This single call creates the entire audit infrastructure for your service.

### Service-Specific Audit

Each service's audit schema is:

- **Isolated**: Only accessible by the owning service
- **Self-contained**: All triggers, functions, and views in service audit schema
- **Independently deployable**: Service migrations call the factory
- **Zero maintenance**: Bug fixes to audit logic update the factory, not each service

Audit triggers are automatically attached during migration via the factory.

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

**Note**: `CreatedBy` and `UpdatedBy` are currently optional (nullable) fields. Once authentication/authorisation
context is available in the application layer, these should be populated from the current user context. The audit
triggers will use these values to track who made changes.

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

1. Create new Atlas config directory: `atlas/new_schema/atlas.hcl`
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

atlas migrate hash --env local --config file://atlas/current_account/atlas.hcl
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
