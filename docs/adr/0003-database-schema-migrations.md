# 3. Database Schema Migrations with Atlas

Date: 2025-10-25

## Status

Accepted (Revised)

## Context

Each BIAN service domain requires database schema management with versioned migrations. The domain model in the persistence layer must evolve safely across deployments without manual SQL execution or schema drift between environments.

Financial services require:
* Immutable migration history (once applied, migrations cannot be modified)
* Rollback capability for failed deployments
* Schema validation and safety checks
* Clear audit trail of schema changes
* Support for CockroachDB/YugabyteDB (PostgreSQL-compatible)
* **Integration with Go structs** (single source of truth)

## Decision Drivers

* Team has Java/Flyway background and values migration immutability
* Go-native tooling preferred for build pipeline simplicity
* **Desire to reduce manual SQL writing** (auto-generate from Go structs)
* Must support transactional DDL (PostgreSQL/CockroachDB feature)
* Need CLI tool for local development and CI/CD integration
* **Type safety between Go code and database schema**
* Version control migrations alongside service code
* **Schema linting and safety checks** before deployment

## Considered Options

1. **Atlas** - Modern schema-as-code tool with Go ORM integration
2. golang-migrate - Go-native migration library and CLI
3. Flyway - Industry-standard migration tool (Java-based)

## Decision Outcome

Chosen option: **"Atlas"**, because:

* **Automatic migration generation from Go structs** (GORM/Ent integration)
* Go-native with no JVM dependency (simplifies Docker images and CI/CD)
* **Schema linting catches dangerous changes** before they reach production
* **Migration testing** validates changes work correctly
* Excellent PostgreSQL/CockroachDB support with transactional DDL
* **Declarative and versioned workflows** (best of both worlds)
* **Type safety** - Go structs are source of truth for database schema
* More powerful than golang-migrate while maintaining immutability

### Positive Consequences

* **Single source of truth**: Go structs define both application and database schema
* **Automatic migration generation**: No manual SQL writing for most changes
* **Safety checks**: Linting catches destructive changes (data loss, breaking changes)
* **Testing**: Validate migrations on test database before production
* Migrations versioned alongside service code in Git
* No JVM dependency (smaller Docker images, faster builds)
* Transactional DDL ensures migrations are atomic
* CLI tool integrates with Go build pipeline
* **Can still write manual migrations** when needed (hybrid approach)
* **Schema diffing**: Compare environments to detect drift

### Negative Consequences

* Newer tool than Flyway (less battle-tested, though mature enough)
* Learning curve for schema-as-code approach (though optional)
* Some advanced features require Atlas Pro (CI/CD integrations, enterprise DBs)
* Team must learn Atlas HCL or use ORM integration

## Pros and Cons of the Options

### Atlas - Modern schema-as-code tool

https://atlasgo.io/

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

https://github.com/golang-migrate/migrate

* Good, because Go-native with no external dependencies
* Good, because simple and proven
* Good, because migrations are plain SQL files
* Bad, because **requires manual SQL writing** for all changes
* Bad, because **no schema linting or safety checks**
* Bad, because **no automatic migration generation**
* Bad, because **no type safety** between Go structs and database
* Bad, because fewer features than Atlas

### Flyway - Industry-standard migration tool

https://flywaydb.org/

* Good, because industry standard with 10+ years of maturity
* Good, because team has existing Flyway experience
* Bad, because requires JVM (larger Docker images, slower builds)
* Bad, because adds Java dependency to Go project
* Bad, because **no integration with Go structs**
* Bad, because **manual SQL writing required**

## Implementation Details

### Project Structure

```
services/financial-accounting-service/
├── atlas.hcl                    # Atlas configuration
├── internal/
│   └── domain/
│       └── models.go            # Go structs (source of truth)
└── migrations/                  # Generated migrations
    ├── 20250125120000_initial.sql
    └── atlas.sum                # Migration checksums
```

### Go Struct as Source of Truth

**internal/domain/models.go:**
```go
package domain

import (
    "time"
    "github.com/google/uuid"
)

// FinancialBookingLog represents a financial booking entry
type FinancialBookingLog struct {
    ID              uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
    ControlRecordID string     `gorm:"uniqueIndex;not null;size:255"`
    BookingPurpose  string     `gorm:"not null;size:500"`
    AmountBlock     AmountData `gorm:"type:jsonb;not null"`
    ValueDate       time.Time  `gorm:"not null;index"`
    BookingCurrency string     `gorm:"not null;size:3"`
    BaseCurrency    string     `gorm:"not null;size:3"`
    CreatedAt       time.Time  `gorm:"not null;default:now()"`
    UpdatedAt       time.Time  `gorm:"not null;default:now()"`
    Version         int        `gorm:"not null;default:1"`
}

type AmountData struct {
    Amount   float64 `json:"amount"`
    Currency string  `json:"currency"`
}
```

### Atlas Configuration

**atlas.hcl:**
```hcl
env "local" {
  src = "file://internal/domain"
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
  src = "gorm://internal/domain"
  dev = "docker://postgres/15/dev"
  url = getenv("DATABASE_URL")

  migration {
    dir = "file://migrations"
  }
}
```

### Workflow

**1. Modify Go Struct:**
```go
// Add new field
type FinancialBookingLog struct {
    // ... existing fields
    Status string `gorm:"not null;default:'pending';index"`
}
```

**2. Generate Migration:**
```bash
# Atlas inspects Go structs and generates migration
atlas migrate diff add_status \
  --env gorm \
  --to "gorm://internal/domain"
```

**Generated migration (migrations/20250125120000_add_status.sql):**
```sql
-- Add column "status" to table: "financial_booking_logs"
ALTER TABLE "financial_booking_logs"
  ADD COLUMN "status" text NOT NULL DEFAULT 'pending';

-- Create index "idx_financial_booking_logs_status"
CREATE INDEX "idx_financial_booking_logs_status"
  ON "financial_booking_logs" ("status");
```

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
      - 'internal/domain/**'
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
* [GitHub Issue #3: Platform Services](https://github.com/bjcoombs/meridian/issues/3)
* [ADR-0004: Unified Schema Management](./0004-unified-schema-management.md)

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
