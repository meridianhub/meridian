---
name: adr-003-database-schema-migrations
description: Use Atlas for database schema migrations with versioned, declarative migrations per service
triggers:

  - Setting up database migrations
  - Managing schema evolution
  - Deploying database changes
  - Handling schema drift

instructions: |
  Use Atlas for declarative schema migrations. Each service has own migrations directory.
  Migrations run automatically on service startup. Atlas generates SQL from desired state,
  ensuring safe schema evolution without manual SQL.
---

# 3. Database Schema Migrations with Atlas

Date: 2025-10-25 (Revised: 2025-10-25)

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

### Project Structure

```text
services/financial-accounting-service/
├── atlas.hcl                           # Atlas configuration
├── internal/
│   ├── domain/
│   │   └── booking_log.go              # Pure domain models (no persistence tags)
│   └── adapters/
│       └── persistence/
│           ├── booking_log_entity.go   # Database entities (GORM tags, source of truth for DB)
│           └── booking_log_repository.go
└── migrations/                         # Generated migrations from entities
    ├── 20250125120000_initial.sql
    └── atlas.sum                       # Migration checksums
```

### Database Entity as Source of Truth for Persistence

**internal/adapters/persistence/booking_log_entity.go:**

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

// TableName overrides the default table name
func (BookingLogEntity) TableName() string {
    return "financial_booking_logs"
}
```

**Corresponding domain model (internal/domain/booking_log.go) - NO persistence tags:**

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

**atlas.hcl:**

```hcl
env "local" {
  src = "file://internal/adapters/persistence"  # Database entities, not domain models
  dev = "docker://postgres/15/dev"
  url = "postgres://user:pass@localhost:5432/financial_accounting?sslmode=disable"

  migration {
    dir = "file://migrations"
  }

  lint {
    destructive {
      error = true  # Fail on data loss
    }
  }
}

env "gorm" {
  src = "gorm://internal/adapters/persistence"  # Scan persistence layer
  dev = "docker://postgres/15/dev"
  url = getenv("DATABASE_URL")

  migration {
    dir = "file://migrations"
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
  --env gorm \
  --to "gorm://internal/adapters/persistence"
```

**Generated migration (migrations/20250125120000_add_narrative.sql):**

```sql
-- Add column "narrative_text" to table: "financial_booking_logs"
ALTER TABLE "financial_booking_logs"
  ADD COLUMN "narrative_text" text;
```

**Note:** Domain model does NOT need to change if this is purely an audit/persistence concern. See
[ADR-0005](0005-adapter-pattern-layer-translation.md) for adapter patterns.

**3. Lint Migration:**

```bash

# Catch dangerous changes before deployment

atlas migrate lint \
  --env gorm \
  --latest 1
```

**4. Test Migration:**

```bash

# Validate on test database

atlas migrate test \
  --env gorm \
  --dev-url "docker://postgres/15/test"
```

**5. Apply Migration:**

```bash

# Deploy to production

atlas migrate apply \
  --env gorm \
  --url "$PROD_DATABASE_URL"
```

### CI/CD Integration

**.github/workflows/migrate.yml:**

```yaml
name: Database Migrations

on:
  pull_request:
    paths:

      - 'internal/adapters/persistence/**'
      - 'migrations/**'

jobs:
  lint-and-test:
    runs-on: ubuntu-latest
    steps:

      - uses: actions/checkout@v3

      - name: Install Atlas

        run: |
          curl -sSf https://atlasgo.sh | sh

      - name: Lint migrations

        run: |
          atlas migrate lint \
            --env gorm \
            --latest 1

      - name: Test migrations

        run: |
          atlas migrate test \
            --env gorm \
            --dev-url "docker://postgres/15/test"

  deploy:
    needs: lint-and-test
    if: github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    steps:

      - name: Apply migrations

        run: |
          atlas migrate apply \
            --env gorm \
            --url "${{ secrets.DATABASE_URL }}"
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

atlas migrate new complex_data_migration --env gorm
```

Edit the generated file with custom SQL, then Atlas manages it like any other migration.

## Links

* [Atlas Documentation](https://atlasgo.io/)
* [Atlas GORM Integration](https://atlasgo.io/guides/orms/gorm)
* [CockroachDB with Atlas](https://atlasgo.io/guides/databases/cockroachdb)
* [GitHub Issue #3: Platform Services](https://github.com/meridianhub/meridian/issues/3)
* [ADR-0004: Separated Schema Management](./0004-separated-schema-management.md)
* [ADR-0005: Adapter Pattern for Layer Translation](./0005-adapter-pattern-layer-translation.md)

## Notes

### Migration to Atlas from golang-migrate

If migrating from golang-migrate:

* Atlas can import existing golang-migrate files
* Maintain migration history (no need to replay all migrations)
* Continue using immutability principles

### Future Considerations

* Atlas Pro for advanced CI/CD integrations
* Schema visualization for documentation
* Multi-tenant schema management patterns
* Cross-region migration strategies for distributed SQL
