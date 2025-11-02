# Database Migrations

This directory contains database schema migrations managed by [Atlas](https://atlasgo.io/).

## Overview

Meridian uses Atlas with GORM integration for database schema management. This provides:

- **Automatic migration generation** from Go struct changes
- **Schema linting** to catch dangerous operations
- **Migration testing** for validation
- **Checksum validation** (similar to Flyway) for integrity

## Quick Start

### Generate a New Migration

When you modify Go models in `internal/domain/models/`, generate a migration:

```bash
make migrate-diff
# Enter a descriptive name when prompted (e.g., "add_customer_email")
```

### Lint Migrations

Check for destructive changes before applying:

```bash
make migrate-lint
```

### Apply Migrations

Apply pending migrations to your database:

```bash
export DATABASE_URL="postgres://user:pass@localhost:5432/meridian?sslmode=disable"
make migrate-apply
```

### Check Migration Status

View applied and pending migrations:

```bash
export DATABASE_URL="postgres://user:pass@localhost:5432/meridian?sslmode=disable"
make migrate-status
```

### Verify Migration Integrity

Verify migration checksums match expected values:

```bash
make migrate-hash
```

## How It Works

### 1. Define Models

Domain models are defined in `internal/domain/models/` with GORM tags:

```go
type Account struct {
    BaseModel
    AccountNumber string `gorm:"type:varchar(34);uniqueIndex;not null"`
    Balance       int64  `gorm:"not null;default:0"`
}
```

### 2. Generate Migrations

Atlas reads Go structs via the loader in `cmd/atlas-loader/main.go` and generates SQL:

```bash
make migrate-diff
```

This creates a timestamped SQL file in `migrations/` and updates `atlas.sum`.

### 3. Review and Test

1. Review the generated SQL in `migrations/`
2. Run `make migrate-lint` to catch issues
3. Test locally with `make migrate-apply`

### 4. Commit

Commit both the migration SQL and `atlas.sum` file:

```bash
git add migrations/
git commit -m "feat: Add customer email field"
```

## Migration Safety

### Linting Rules

Atlas checks for:

- **Destructive changes**: DROP TABLE, DROP COLUMN
- **Data dependencies**: Foreign key issues
- **Incompatible changes**: Type changes that lose data

### Best Practices

1. **Never edit existing migrations** - Migrations are immutable once committed
2. **Always lint before applying** - Catch issues early
3. **Test locally first** - Validate on dev database
4. **Review generated SQL** - Ensure it matches intent
5. **Keep migrations small** - One logical change per migration

## Troubleshooting

### Migration Conflict

If `atlas.sum` has conflicts:

```bash
# Regenerate checksums
make migrate-hash
```

### Failed Migration

If a migration fails partway through:

1. Manually fix the database state
2. Create a new migration to correct the issue
3. Never edit the failed migration file

### Model Changes Not Detected

Ensure you've:

1. Added the model to `cmd/atlas-loader/main.go`
2. Run `go mod tidy` to update dependencies
3. Checked GORM tags are correct

## Environment Configuration

Atlas uses environments defined in `atlas.hcl`:

- **local**: Development with Docker dev database
- **ci**: CI/CD pipeline testing
- **production**: Production migrations (apply only, no diff)

## Advanced Usage

### Manual Migration

For complex changes, create a manual migration:

```bash
atlas migrate new my_complex_change --env local
# Edit the generated file with custom SQL
```

### Rollback

Atlas doesn't have built-in rollback. To revert:

1. Create a new migration that reverses changes
2. Use database-specific recovery if needed

### Multiple Environments

```bash
# CI environment
atlas migrate apply --env ci

# Production
export PROD_DATABASE_URL="..."
atlas migrate apply --env production
```

## Further Reading

- [Atlas Documentation](https://atlasgo.io/getting-started)
- [Atlas GORM Guide](https://atlasgo.io/guides/orms/gorm)
- [Migration Best Practices](https://atlasgo.io/versioned/apply)
