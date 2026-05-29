---
name: adr-003-database-schema-migrations
description: Use Atlas for database schema migrations with versioned, declarative migrations per service
triggers:

  - Setting up database migrations
  - Managing schema evolution
  - Deploying database changes
  - Handling schema drift

instructions: |
  Use Atlas for declarative schema migrations. Each service has its own
  services/<service>/migrations/ directory and services/<service>/atlas/atlas.hcl config.
  Migrations do NOT run automatically on startup - apply them explicitly via the unified
  binary's `--migrate` flag (or `atlas migrate apply` in CI). Atlas generates SQL by diffing
  the GORM models loaded through utilities/atlas-loader against the live schema.
---

# 3. Database Schema Migrations with Atlas

Date: 2025-10-25 (Revised: 2025-12-16)

## Status

Accepted

Supersedes aspects of initial unified schema approach. See [ADR-0005](0005-adapter-pattern-layer-translation.md) for
layer separation details.

## Context

Each BIAN service domain requires database schema management with versioned migrations. The persistence layer must
evolve safely across deployments without manual SQL execution or schema drift between environments.

Financial services require:

* Immutable migration history (once applied, migrations cannot be modified)
* Rollback capability for failed deployments
* Schema validation and safety checks
* Clear audit trail of schema changes
* Support for CockroachDB/YugabyteDB (PostgreSQL-compatible)
* **Database entities optimized for persistence concerns** (audit fields, indexes, constraints)
* **Separation from domain models** to allow independent evolution

## Decision Drivers

* Team has Java/Flyway background and values migration immutability
* Go-native tooling preferred for build pipeline simplicity
* **Desire to reduce manual SQL writing** (auto-generate from database entity structs)
* Must support transactional DDL (PostgreSQL/CockroachDB feature)
* Need CLI tool for local development and CI/CD integration
* **Type safety between persistence layer and database schema**
* Version control migrations alongside service code
* **Schema linting and safety checks** before deployment
* **Database entities separate from domain models** for flexibility (audit fields, denormalization, optimization)

## Considered Options

1. **Atlas** - Modern schema-as-code tool with Go ORM integration
2. golang-migrate - Go-native migration library and CLI
3. Flyway - Industry-standard migration tool (Java-based)

## Decision Outcome

Chosen option: **"Atlas"**, because:

* **Automatic migration generation from database entity structs** (GORM/Ent integration)
* Go-native with no JVM dependency (simplifies Docker images and CI/CD)
* **Schema linting catches dangerous changes** before they reach production
* **Migration testing** validates changes work correctly
* Excellent PostgreSQL/CockroachDB support with transactional DDL
* **Declarative and versioned workflows** (best of both worlds)
* **Type safety** - Database entity structs are source of truth for database schema
* More powerful than golang-migrate while maintaining immutability

### Positive Consequences

* **Database entities as source of truth**: Persistence layer structs define database schema
* **Automatic migration generation**: No manual SQL writing for most changes
* **Safety checks**: Linting catches destructive changes (data loss, breaking changes)
* **Testing**: Validate migrations on test database before production
* Migrations versioned alongside service code in Git
* No JVM dependency (smaller Docker images, faster builds)
* Transactional DDL ensures migrations are atomic
* CLI tool integrates with Go build pipeline
* **Can still write manual migrations** when needed (hybrid approach)
* **Schema diffing**: Compare environments to detect drift
* **Separation of concerns**: Database entities can include audit fields, indexes, and optimizations without polluting
domain models

### Negative Consequences

* Newer tool than Flyway (less battle-tested, though mature enough)
* Learning curve for schema-as-code approach (though optional)
* Some advanced features require Atlas Pro (CI/CD integrations, enterprise DBs)
* Team must learn Atlas HCL or use ORM integration

## Pros and Cons of the Options

### Atlas - Modern schema-as-code tool

<https://atlasgo.io/>

* Good, because **auto-generates migrations from Go structs** (GORM, Ent, SQLBoiler)
* Good, because **schema linting prevents dangerous changes**
* Good, because **migration testing** validates before deployment
* Good, because Go-native with excellent PostgreSQL/CockroachDB support
* Good, because supports both declarative and versioned workflows
* Good, because **type safety** between Go code and database
* Good, because can generate golang-migrate compatible files
* Good, because **schema inspection and visualization**
* Bad, because newer than Flyway (less enterprise adoption history)
* Bad, because some features require Atlas Pro (though free tier is generous)
* Bad, because learning curve for HCL (mitigated by ORM integration)

### golang-migrate - Go-native migration library

<https://github.com/golang-migrate/migrate>

* Good, because Go-native with no external dependencies
* Good, because simple and proven
* Good, because migrations are plain SQL files
* Bad, because **requires manual SQL writing** for all changes
* Bad, because **no schema linting or safety checks**
* Bad, because **no automatic migration generation**
* Bad, because **no type safety** between Go structs and database
* Bad, because fewer features than Atlas

### Flyway - Industry-standard migration tool

<https://flywaydb.org/>

* Good, because industry standard with 10+ years of maturity
* Good, because team has existing Flyway experience
* Bad, because requires JVM (larger Docker images, slower builds)
* Bad, because adds Java dependency to Go project
* Bad, because **no integration with Go structs**
* Bad, because **manual SQL writing required**

## Implementation Details

### Database Architecture

**Database-per-service with schema-per-org:**

```
Database: meridian_party              (service-specific)
  └── Schema: org_acme_bank           (org-specific)
       └── Table: party               (singular, unqualified)
  └── Schema: org_demo_corp
       └── Table: party

Database: meridian_current_account
  └── Schema: org_acme_bank
       └── Tables: account, lien, audit_log, audit_outbox
```

- Each service has its own database (PostgreSQL in develop/demo, CockroachDB in production)
- Each organization gets its own schema within each service database
- The search_path is set transactionally via `SET LOCAL search_path TO org_<id>` by `shared/platform/db/gorm_tenant_scope.go`
- Queries use unqualified table names; PostgreSQL resolves via `search_path`
- As of PR #2125, `public` is **not** in the search_path — reference data is replicated into each tenant schema on provisioning rather than shared via public. See [data-model.md](../architecture/data-model.md#tenant-isolation-mechanism) for the current mechanism.

### Naming Conventions

**Table names:**
- **Singular nouns**: `account`, `party`, `lien` (not `accounts`, `parties`, `liens`)
- **Unqualified**: No schema prefix in migrations (relies on `search_path`)
- **Snake_case**: `payment_order`, `audit_trail_entry`

**Compound names follow the pattern `<context>_<entity>`:**
- `payment_order` - an order for payment
- `ledger_posting` - a posting to a ledger
- `audit_trail_entry` - an entry in an audit trail
- `financial_booking_log` - a log entry for financial bookings

**Rationale:**
- Singular names read naturally in queries: `SELECT * FROM party WHERE id = $1`
- Unqualified names enable transparent org routing via `search_path`
- Consistent with Rails/ActiveRecord conventions

### Project Structure

```text
project-root/
├── services/                           # Service-specific code and migrations
│   ├── current-account/
│   │   ├── atlas/
│   │   │   └── atlas.hcl               # Service-specific Atlas config
│   │   ├── migrations/
│   │   │   ├── 20251216000001_initial.sql
│   │   │   └── atlas.sum
│   │   └── adapters/
│   │       └── persistence/
│   │           ├── account_entity.go   # Database entities (GORM tags)
│   │           └── lien_entity.go
│   ├── party/
│   │   ├── atlas/
│   │   │   └── atlas.hcl
│   │   ├── migrations/
│   │   │   └── ...
│   │   └── adapters/
│   │       └── persistence/
│   │           └── party_entity.go
│   └── ...                             # Other services follow same pattern
├── shared/
│   └── domain/
│       └── models/                     # Shared domain models (no persistence tags)
└── utilities/
    └── atlas-loader/                   # GORM schema loader for Atlas
```

### Database Entity as Source of Truth for Persistence

**services/financial-accounting/adapters/persistence/booking_log_entity.go:**

```go
package persistence

import (
    "time"
    "github.com/google/uuid"
)

// BookingLogEntity represents the database persistence model
// Optimized for database concerns: audit fields, indexes, constraints
type BookingLogEntity struct {
    // Primary key
    ID              uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

    // Business fields
    ControlRecordID string     `gorm:"uniqueIndex;not null;size:255"`
    BookingPurpose  string     `gorm:"not null;size:500"`
    AmountCents     int64      `gorm:"not null"`
    Currency        string     `gorm:"not null;size:3;index"`
    ValueDate       time.Time  `gorm:"not null;index"`
    Status          string     `gorm:"not null;size:50;index"`

    // Audit fields (NOT in domain model)
    CreatedAt       time.Time  `gorm:"not null;default:now()"`
    UpdatedAt       time.Time  `gorm:"not null;default:now()"`
    CreatedBy       string     `gorm:"size:255"`
    UpdatedBy       string     `gorm:"size:255"`

    // Optimistic locking
    Version         int        `gorm:"not null;default:1"`

    // Soft delete
    DeletedAt       *time.Time `gorm:"index"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per naming conventions.
func (BookingLogEntity) TableName() string {
    return "financial_booking_log"
}
```

**Corresponding domain model (services/financial-accounting/domain/booking_log.go) - NO persistence tags:**

```go
package domain

import (
    "time"
    "github.com/google/uuid"
)

// FinancialBookingLog - Pure domain model with business logic
// No persistence concerns, no GORM tags
type FinancialBookingLog struct {
    ID              uuid.UUID
    ControlRecordID string
    BookingPurpose  string
    Amount          Money  // Rich domain type
    ValueDate       time.Time
    Status          BookingStatus  // Domain enum
}

type Money struct {
    AmountCents int64
    Currency    Currency
}

type BookingStatus string

const (
    BookingStatusPending BookingStatus = "pending"
    BookingStatusPosted  BookingStatus = "posted"
    BookingStatusFailed  BookingStatus = "failed"
)

// Domain behavior methods
func (b *FinancialBookingLog) Post() error {
    if b.Status != BookingStatusPending {
        return ErrInvalidStatusTransition
    }
    b.Status = BookingStatusPosted
    return nil
}
```

### Atlas Configuration

Each service owns its Atlas config at `services/<service>/atlas/atlas.hcl`, and its migrations at
`services/<service>/migrations/`. The config invokes the shared GORM loader at `utilities/atlas-loader` to derive the
desired schema:

**services/financial-accounting/atlas/atlas.hcl:**

```hcl
data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./utilities/atlas-loader",
    "--schema=financial_accounting"
  ]
}

env "local" {
  migration {
    dir = "file://services/financial-accounting/migrations"
  }

  dev = "docker://postgres/16/dev"
  src = data.external_schema.gorm.url
  schemas = ["financial_accounting"]

  lint {
    destructive {
      error = true  # Fail on data loss
    }
    data_depend {
      error = true
    }
    incompatible {
      error = true
    }
  }
}

env "ci" {
  migration {
    dir = "file://services/financial-accounting/migrations"
  }

  dev = "docker://postgres/16/dev"
  src = data.external_schema.gorm.url
  schemas = ["financial_accounting"]

  lint {
    destructive {
      error = true
    }
    data_depend {
      error = true
    }
    incompatible {
      error = true
    }
  }
}
```

### Workflow

**1. Modify Database Entity:**

```go
// Add new field to persistence model
type BookingLogEntity struct {
    // ... existing fields
    NarrativeText string `gorm:"type:text"` // New field for audit trail
}
```

**2. Generate Migration:**

```bash
# Atlas inspects database entities and generates migration
atlas migrate diff add_narrative \
  --env local \
  --config file://services/financial-accounting/atlas/atlas.hcl
```

**Generated migration (services/financial-accounting/migrations/20250125120000_add_narrative.sql):**

```sql
-- Add column "narrative_text" to table: "financial_booking_log"
ALTER TABLE "financial_booking_log"
  ADD COLUMN "narrative_text" text;
```

**Note:** Domain model does NOT need to change if this is purely an audit/persistence concern. See
[ADR-0005](0005-adapter-pattern-layer-translation.md) for adapter patterns.

**3. Lint Migration:**

```bash
# Catch dangerous changes before deployment
atlas migrate lint \
  --env local \
  --config file://services/financial-accounting/atlas/atlas.hcl \
  --latest 1
```

**4. Verify Migration Checksums:**

```bash
# Verify migration checksums
atlas migrate hash \
  --env local \
  --config file://services/financial-accounting/atlas/atlas.hcl
```

**5. Apply Migration:**

```bash
# Deploy to target environment
atlas migrate apply \
  --env local \
  --config file://services/financial-accounting/atlas/atlas.hcl \
  --url "$DATABASE_URL"
```

> **Runtime note:** Meridian does **not** auto-run migrations on service startup. In production/demo, migrations are
> applied by the unified binary's `--migrate` flag (`cmd/meridian/main.go`, which applies the embedded SQL and exits),
> and in CI via `atlas migrate apply`. There is no implicit migration-on-boot.

### CI/CD Integration

See `.github/workflows/migrations.yml` for the actual implementation. Key path triggers:

```yaml
on:
  pull_request:
    paths:
      - 'services/*/migrations/**'
      - 'services/*/atlas/**'
      - 'shared/atlas/**'
      - 'utilities/atlas-loader/**'
      - 'shared/domain/models/**'
      - 'scripts/migrate-all-orgs.sh'
```

Migration verification uses the per-service configs:

```bash
# Verify checksums for each service
atlas migrate hash --env ci --config file://services/current-account/atlas/atlas.hcl
atlas migrate hash --env ci --config file://services/position-keeping/atlas/atlas.hcl
```

### Immutability Principle

Atlas maintains Flyway-style immutability:

* Once a migration is applied, it MUST NOT be modified
* `atlas.sum` file contains checksums (like Flyway)
* If a migration has an error, create a new migration to fix it
* Version control tracks all migration changes

### Manual Migrations When Needed

For complex changes, write SQL manually:

```bash

# Create empty migration file

atlas migrate new complex_data_migration \
  --config file://services/financial-accounting/atlas/atlas.hcl --env local
```

Edit the generated file with custom SQL, then Atlas manages it like any other migration.

## Links

* [Atlas Documentation](https://atlasgo.io/)
* [Atlas GORM Integration](https://atlasgo.io/guides/orms/gorm)
* [CockroachDB with Atlas](https://atlasgo.io/guides/databases/cockroachdb)
* [GitHub Issue #3: Platform Services](https://github.com/meridianhub/meridian/issues/3)
* [ADR-0004: Event Schema Evolution](./0004-event-schema-evolution.md)
* [ADR-0005: Adapter Pattern for Layer Translation](./0005-adapter-pattern-layer-translation.md)

## Notes

### Migration to Atlas from golang-migrate

If migrating from golang-migrate:

* Atlas can import existing golang-migrate files
* Maintain migration history (no need to replay all migrations)
* Continue using immutability principles

### CockroachDB Compatibility Considerations

> **Production target:** Meridian's production deployment target is **CockroachDB**. The `develop` and `demo` environments currently run PostgreSQL 16 (faster local boot, wire-compatible), but every migration and runtime SQL path must work unchanged on CockroachDB. The constraints below are binding for all new migrations. See [data-model.md](../architecture/data-model.md) for the current topology and [docs/reports/cockroachdb-migration-audit.md](../reports/cockroachdb-migration-audit.md) for the compatibility audit.

While CockroachDB is PostgreSQL wire-compatible, some PostgreSQL features are **not supported**:

| Feature | PostgreSQL | CockroachDB | Workaround |
|---------|------------|-------------|------------|
| Range types (`TSTZRANGE`, `DATERANGE`, etc.) | Supported | Not supported ([#27791](https://github.com/cockroachdb/cockroach/issues/27791)) | Use explicit `start`/`end` columns |
| GiST indexes for ranges | Supported | Not supported | Use B-tree composite indexes |
| Exclusion constraints | Supported | Not supported | Application-level enforcement |
| PL/pgSQL functions | Supported | Limited support | Keep functions simple |

**Recommendation:** Design schemas using the common subset of PostgreSQL/CockroachDB features.
Use explicit timestamp columns (`period_start`, `period_end`) instead of range types for temporal
data. See [ADR-0017](0017-temporal-quality-ladder.md) for the temporal data pattern.

For deployments using PostgreSQL or YugabyteDB exclusively, database-specific optimizations
(TSTZRANGE with GiST indexes, exclusion constraints) can be added as an optional enhancement.

### Future Considerations

* Atlas Pro for advanced CI/CD integrations
* Schema visualization for documentation
* Multi-tenant schema management patterns
* Cross-region migration strategies for distributed SQL
* Database-specific optimization paths (PostgreSQL/YugabyteDB TSTZRANGE support)
