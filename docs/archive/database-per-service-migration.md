---
name: database-per-service-migration
description: "[ARCHIVED] Historical runbook. The database-per-service migration is complete. See docs/architecture/data-model.md for the current topology."
status: archived
triggers: []

instructions: |
  ARCHIVED. This runbook documented the one-time migration from a shared database
  to database-per-service. That work is complete - every service already owns its
  own Postgres database with schema-per-tenant isolation. For the current topology,
  see docs/architecture/data-model.md.
---

# Database-Per-Service Migration Runbook

**When to use this runbook**: Migrating from a shared database to isolated databases per service, or setting up new
service databases following the database-per-service pattern.

## Overview

This runbook documents the migration from a single shared `meridian` database to six service-specific databases,
implementing true database-level isolation for each BIAN service domain.

### Architecture Change Summary

**Before (single shared database):**

```text
Database: meridian
  └── Schema: org_acme_bank
       └── Tables: account, party, ledger_posting, position_log, payment_order, ...
```

**After (database-per-service):**

```text
Database: meridian_platform           → Tenant Service
Database: meridian_current_account    → Current Account Service
Database: meridian_financial_accounting → Financial Accounting Service
Database: meridian_position_keeping   → Position Keeping Service
Database: meridian_payment_order      → Payment Order Service
Database: meridian_party              → Party Service
```

### Benefits Achieved

- **True isolation**: One service cannot access another service's data
- **Independent scaling**: Each database can be scaled based on service load
- **Simplified operations**: Backup, restore, and maintenance per service
- **Clear ownership**: Each team owns their database schema
- **Failure isolation**: Database issues in one service don't affect others

## Prerequisites

### Required Access

- [ ] CockroachDB admin credentials (for database/user creation)
- [ ] Kubernetes cluster access (for updating connection strings)
- [ ] GitHub repository access (for migration files)
- [ ] Atlas CLI installed (`brew install ariga/tap/atlas`)

### Pre-Migration Checklist

- [ ] All services healthy and stable
- [ ] Database backups completed and verified
- [ ] Migration window scheduled (recommend off-peak hours)
- [ ] Rollback plan reviewed and ready
- [ ] Communication sent to stakeholders

## Migration Steps

### Phase 1: Database and User Creation

#### Step 1.1: Create Service Databases

```sql
-- Connect to CockroachDB as admin
cockroach sql --certs-dir=/path/to/certs --host=<db-host>

-- Create databases for each service
CREATE DATABASE meridian_platform;
CREATE DATABASE meridian_current_account;
CREATE DATABASE meridian_financial_accounting;
CREATE DATABASE meridian_position_keeping;
CREATE DATABASE meridian_payment_order;
CREATE DATABASE meridian_party;
```

#### Step 1.2: Create Service-Specific Users

```sql
-- Create users with secure passwords (use secrets management in production)
CREATE USER platform_svc WITH PASSWORD '<secure-password>';
CREATE USER current_account_svc WITH PASSWORD '<secure-password>';
CREATE USER financial_accounting_svc WITH PASSWORD '<secure-password>';
CREATE USER position_keeping_svc WITH PASSWORD '<secure-password>';
CREATE USER payment_order_svc WITH PASSWORD '<secure-password>';
CREATE USER party_svc WITH PASSWORD '<secure-password>';
```

#### Step 1.3: Grant Database Access

```sql
-- Each user can only access their service's database
GRANT ALL ON DATABASE meridian_platform TO platform_svc;
GRANT ALL ON DATABASE meridian_current_account TO current_account_svc;
GRANT ALL ON DATABASE meridian_financial_accounting TO financial_accounting_svc;
GRANT ALL ON DATABASE meridian_position_keeping TO position_keeping_svc;
GRANT ALL ON DATABASE meridian_payment_order TO payment_order_svc;
GRANT ALL ON DATABASE meridian_party TO party_svc;

-- Verify grants
SHOW GRANTS FOR current_account_svc;
-- Should only show meridian_current_account
```

### Phase 2: Tenant Schema Setup

#### Step 2.1: Create Tenant Schemas in Each Database

For each database, create schemas for each tenant:

```sql
-- Example for current_account database
USE meridian_current_account;

CREATE SCHEMA org_acme_bank;
CREATE SCHEMA org_demo_corp;
-- Add schemas for each tenant

-- Grant schema access to service user
GRANT ALL ON SCHEMA org_acme_bank TO current_account_svc;
GRANT ALL ON SCHEMA org_demo_corp TO current_account_svc;
```

Repeat for each service database with appropriate tenant schemas.

### Phase 3: Atlas Configuration

#### Step 3.1: Create Service-Specific Atlas Config

Each service needs its own `atlas.hcl` configuration:

**services/current-account/atlas/atlas.hcl:**

```hcl
data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "../../utilities/atlas-loader",
    "--service=current-account"
  ]
}

env "local" {
  migration {
    dir = "file://migrations"
  }
  dev = "docker://postgres/16/dev?search_path=public"
  src = data.external_schema.gorm.url

  lint {
    destructive {
      error = true
    }
  }
}

env "production" {
  migration {
    dir = "file://migrations"
  }

  lint {
    destructive {
      error = true
    }
    data_depend {
      error = true
    }
  }
}
```

#### Step 3.2: Generate Initial Migrations

```bash
# For each service, generate migrations from entities
cd services/current-account
atlas migrate diff initial \
  --env local \
  --config file://atlas/atlas.hcl

# Verify generated migration
cat migrations/*.sql

# Verify checksums
atlas migrate hash --env local --config file://atlas/atlas.hcl
```

### Phase 4: Data Migration (If Required)

#### Step 4.1: Export Data from Shared Database

If migrating existing data from a shared database:

```bash
# Export tables belonging to each service
cockroach dump meridian org_acme_bank.account \
  --certs-dir=/path/to/certs \
  > exports/current_account_data.sql

cockroach dump meridian org_acme_bank.party \
  --certs-dir=/path/to/certs \
  > exports/party_data.sql
```

#### Step 4.2: Import Data to Service Databases

```bash
# Import to service-specific database
cockroach sql \
  --database=meridian_current_account \
  --certs-dir=/path/to/certs \
  < exports/current_account_data.sql
```

### Phase 5: Application Configuration

#### Step 5.1: Update Connection Strings

Update each service's connection configuration:

**Kubernetes ConfigMap example:**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: current-account-config
data:
  DATABASE_URL: "postgres://current_account_svc:${PASSWORD}@cockroachdb:26257/meridian_current_account?sslmode=verify-full"
```

#### Step 5.2: Update Search Path Configuration

Ensure tenant routing uses `search_path`:

```go
// Example connection with tenant schema
connStr := fmt.Sprintf(
    "%s?search_path=%s",
    baseConnStr,
    tenantSchema, // e.g., "org_acme_bank"
)
```

### Phase 6: Verification

#### Step 6.1: Verify Database Isolation

```sql
-- As current_account_svc user, try to access party database
-- This should FAIL
\c meridian_party
-- ERROR: permission denied

-- Verify can only access own database
\c meridian_current_account
SELECT current_database();
-- meridian_current_account
```

#### Step 6.2: Run Service Health Checks

```bash
# Check each service can connect and query
kubectl exec -it current-account-pod -- /app/healthcheck

# Check migration status
atlas migrate status \
  --env production \
  --config file://services/current-account/atlas/atlas.hcl \
  --url "$DATABASE_URL"
```

#### Step 6.3: Verify Data Integrity

```sql
-- Verify record counts match expected values
USE meridian_current_account;
SET search_path = org_acme_bank;
SELECT COUNT(*) FROM account;

-- Verify foreign key relationships (within service)
SELECT COUNT(*) FROM lien WHERE account_id NOT IN (SELECT id FROM account);
-- Should return 0
```

## Rollback Procedure

### Emergency Rollback

If critical issues arise during migration:

#### Step R1: Stop Traffic

```bash
# Scale services to 0 to stop writes
kubectl scale deployment current-account --replicas=0 -n production
```

#### Step R2: Restore Connection Strings

```bash
# Revert to shared database connection
kubectl apply -f backup/pre-migration-configmaps.yaml
```

#### Step R3: Verify Shared Database

```sql
-- Verify shared database is intact
USE meridian;
SELECT COUNT(*) FROM org_acme_bank.account;
```

#### Step R4: Resume Services

```bash
# Scale services back up
kubectl scale deployment current-account --replicas=3 -n production
```

### Rollback Checklist

- [ ] All services stopped
- [ ] Connection strings reverted
- [ ] Shared database verified
- [ ] Services restarted and healthy
- [ ] Incident documented

## Verification Checklist

### Post-Migration Verification

- [ ] All six databases created and accessible
- [ ] Service users have correct database grants
- [ ] Tenant schemas created in each database
- [ ] Atlas migrations applied successfully
- [ ] Services connect with correct credentials
- [ ] Data integrity verified (if data migrated)
- [ ] Cross-database access blocked (isolation verified)
- [ ] gRPC inter-service communication working
- [ ] Monitoring dashboards updated for new databases
- [ ] Backup jobs configured for new databases

### Performance Verification

- [ ] Query response times within baseline
- [ ] Connection pool sizes appropriate
- [ ] No deadlocks or lock contention
- [ ] Index usage as expected

## Lessons Learned

### What Worked Well

1. **Schema-per-tenant pattern**: Enables transparent routing via `search_path`
2. **Dedicated service users**: CockroachDB enforces isolation at connection level
3. **Atlas per-service configs**: Independent schema evolution per service
4. **Singular table names**: Natural SQL syntax (`FROM account` vs `FROM accounts`)

### Challenges Encountered

1. **Cross-service queries**: Required refactoring to use gRPC instead of JOINs
2. **Shared reference data**: Tenant registry must be accessible (via gRPC, not SQL)
3. **Migration coordination**: Required staging all services before production

### Recommendations

1. **Start new projects with database-per-service**: Retrofitting is harder
2. **Use gRPC from day one**: Avoid SQL dependencies between services
3. **Automate user/grant creation**: Use infrastructure-as-code
4. **Test isolation**: Verify users cannot cross database boundaries

## Related Documentation

- [ADR-002: Microservices Per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)
- [ADR-003: Database Schema Migrations](../adr/0003-database-schema-migrations.md)
- [Incident Response Runbook](../runbooks/incident-response.md)
- [Disaster Recovery Runbook](../runbooks/disaster-recovery.md)
