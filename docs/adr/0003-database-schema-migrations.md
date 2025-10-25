# 3. Database Schema Migrations with golang-migrate

Date: 2025-10-25

## Status

Accepted

## Context

Each BIAN service domain requires database schema management with versioned migrations. The domain model in the persistence layer must evolve safely across deployments without manual SQL execution or schema drift between environments.

Financial services require:
* Immutable migration history (once applied, migrations cannot be modified)
* Rollback capability for failed deployments
* Checksum verification to detect tampering
* Clear audit trail of schema changes
* Support for CockroachDB/YugabyteDB (PostgreSQL-compatible)

## Decision Drivers

* Team has Java/Flyway background and values migration immutability
* Go-native tooling preferred for build pipeline simplicity
* Must support transactional DDL (PostgreSQL/CockroachDB feature)
* Need CLI tool for local development and CI/CD integration
* Must handle distributed SQL databases (CockroachDB/YugabyteDB)
* Version control migrations alongside service code

## Considered Options

1. golang-migrate - Go-native migration library and CLI
2. Flyway - Industry-standard migration tool (Java-based)
3. Atlas - Modern schema-as-code tool with type safety

## Decision Outcome

Chosen option: "golang-migrate", because:

* Go-native with no JVM dependency (simplifies Docker images and CI/CD)
* Excellent PostgreSQL/CockroachDB support with transactional DDL
* CLI tool integrates seamlessly with Go build pipeline
* Versioned migrations stored as `.sql` files in version control
* Supports both up and down migrations
* Widely adopted in Go ecosystem with proven stability
* Simpler than Atlas while maintaining migration immutability

### Positive Consequences

* Migrations versioned alongside service code in Git
* No JVM dependency (smaller Docker images, faster builds)
* Transactional DDL ensures migrations are atomic
* CLI tool available for local development: `migrate -path migrations -database "postgres://..." up`
* Integrates with Go application startup for auto-migration
* Checksum validation prevents migration tampering
* Clear migration history in `schema_migrations` table

### Negative Consequences

* Less feature-rich than Flyway (no Java callbacks, no repeatable migrations)
* No built-in baseline/repair commands (must handle manually)
* Less mature tooling compared to Flyway's 10+ year history
* Schema diffing requires manual SQL writing (no auto-generation)

## Pros and Cons of the Options

### golang-migrate - Go-native migration library

https://github.com/golang-migrate/migrate

* Good, because Go-native with no external dependencies
* Good, because CLI tool integrates with build pipeline
* Good, because migrations are plain SQL files in version control
* Good, because transactional DDL support for PostgreSQL/CockroachDB
* Good, because can embed migrations in Go binary
* Good, because widely adopted (18k+ GitHub stars)
* Bad, because fewer features than Flyway
* Bad, because no repeatable migrations or callbacks
* Bad, because manual SQL writing required

### Flyway - Industry-standard migration tool

https://flywaydb.org/

* Good, because industry standard with 10+ years of maturity
* Good, because team has existing Flyway experience
* Good, because advanced features (repeatable migrations, callbacks, baseline)
* Good, because excellent documentation and community
* Good, because supports many database types
* Bad, because requires JVM (larger Docker images, slower builds)
* Bad, because adds Java dependency to Go project
* Bad, because Go application cannot embed migrations easily
* Bad, because CLI separate from Go toolchain

### Atlas - Modern schema-as-code tool

https://atlasgo.io/

* Good, because modern approach with type-safe schema definitions
* Good, because auto-generates migrations from schema diff
* Good, because Go-native with excellent PostgreSQL support
* Good, because schema inspection and visualization
* Bad, because relatively new (less battle-tested than Flyway)
* Bad, because schema-as-code requires learning new DSL
* Bad, because may be overkill for simple versioned migrations
* Bad, because less team familiarity

## Links

* [golang-migrate Documentation](https://github.com/golang-migrate/migrate)
* [CockroachDB Migration Best Practices](https://www.cockroachlabs.com/docs/stable/migration-overview.html)
* [GitHub Issue #3: Platform Services](https://github.com/bjcoombs/meridian/issues/3)

## Notes

### Migration File Structure

Each service maintains its own migrations directory:

```
services/financial-accounting-service/
├── migrations/
│   ├── 000001_create_financial_booking_log.up.sql
│   ├── 000001_create_financial_booking_log.down.sql
│   ├── 000002_create_ledger_posting.up.sql
│   ├── 000002_create_ledger_posting.down.sql
│   └── ...
```

### Migration Naming Convention

* Format: `{version}_{description}.{up|down}.sql`
* Version: 6-digit zero-padded number (000001, 000002, ...)
* Description: Snake case, descriptive (create_table, add_column, etc.)
* Always create both up and down migrations

### Example Migration

**000001_create_financial_booking_log.up.sql:**
```sql
CREATE TABLE financial_booking_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    control_record_id VARCHAR(255) NOT NULL UNIQUE,
    booking_purpose VARCHAR(500) NOT NULL,
    amount_block JSONB NOT NULL,
    value_date TIMESTAMPTZ NOT NULL,
    booking_currency VARCHAR(3) NOT NULL,
    base_currency VARCHAR(3) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    version INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_financial_booking_log_control_record
    ON financial_booking_log(control_record_id);
CREATE INDEX idx_financial_booking_log_value_date
    ON financial_booking_log(value_date);
```

**000001_create_financial_booking_log.down.sql:**
```sql
DROP TABLE IF EXISTS financial_booking_log;
```

### Integration with Application

Migrations can run automatically on service startup (for development) or via CI/CD (for production):

```go
package main

import (
    "github.com/golang-migrate/migrate/v4"
    _ "github.com/golang-migrate/migrate/v4/database/postgres"
    _ "github.com/golang-migrate/migrate/v4/source/file"
)

func runMigrations(dbURL string) error {
    m, err := migrate.New(
        "file://migrations",
        dbURL,
    )
    if err != nil {
        return err
    }

    if err := m.Up(); err != nil && err != migrate.ErrNoChange {
        return err
    }

    return nil
}
```

### CI/CD Integration

Migrations run as part of deployment pipeline:

```yaml
# .github/workflows/deploy.yml
- name: Run database migrations
  run: |
    migrate -path services/${{ matrix.service }}/migrations \
            -database "${{ secrets.DB_URL }}" \
            up
```

### Immutability Principle

Following Flyway patterns:
* Once a migration is applied in any environment, it MUST NOT be modified
* If a migration has an error, create a new migration to fix it
* Use version control (Git) to track all migration changes
* Schema migrations table tracks applied migrations with checksums

### Future Considerations

* Consider Atlas if schema complexity grows and auto-diffing becomes valuable
* May add custom checksum verification for compliance requirements
* Could add baseline migration support for existing databases
* Watch for distributed SQL migration challenges (schema changes across regions)
