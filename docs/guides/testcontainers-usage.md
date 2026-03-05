# Testcontainers Usage Guide

This guide describes how to use testcontainers for CockroachDB integration tests across Meridian services.

**Shared utility**: `shared/platform/testdb/` - Provides reusable testcontainer setup for all services via `testdb.SetupCockroachDB`.

The examples below use position-keeping but the pattern applies to all services.

## Overview

The `testdb` package implements a complete testing environment with:

- **Isolated CockroachDB containers** - Each test gets its own database
- **Automatic schema setup** - Service schema loaded automatically
- **GORM database handle** - Configured and ready for repository use
- **Proper cleanup** - Automatic container termination and resource management

## Quick Start

```go
import (
    "testing"
    "github.com/meridianhub/meridian/shared/platform/testdb"
)

func TestMyRepository(t *testing.T) {
    // Setup test environment with CockroachDB testcontainer
    db, cleanup := testdb.SetupCockroachDB(t, nil)
    defer cleanup()

    // Use the GORM database handle
    repo := repository.NewRepository(db)
    log := createTestLog(t, "ACC-001")
    err := repo.Create(context.Background(), log)
    require.NoError(t, err)
}
```

## Architecture

### Test Flow

1. **Setup** - `testdb.SetupCockroachDB(t, models)` creates container, runs migrations
2. **Test** - Use the returned GORM `*gorm.DB` handle for repository operations
3. **Cleanup** - `defer cleanup()` terminates container and closes connections

## Database Schema

The test database includes the complete position-keeping schema:

### Tables

- **financial_position_logs** - Aggregate root table
  - Primary key: `id` (UUID)
  - Unique index: `log_id` (UUID)
  - Columns: account_id, version, status tracking, reconciliation status

- **transaction_log_entries** - Transaction entries (1-to-many)
  - Foreign key: `financial_position_log_id` → financial_position_logs
  - Columns: transaction_id, account_id, amount_cents, currency, direction, timestamp

- **transaction_lineages** - Transaction relationships (1-to-1)
  - Foreign key: `financial_position_log_id` → financial_position_logs
  - JSONB columns: child_transaction_ids, related_transaction_ids

- **audit_trail_entries** - Audit trail (1-to-many)
  - Foreign key: `financial_position_log_id` → financial_position_logs
  - JSONB column: system_context

### Cascade Deletes

All related tables use `ON DELETE CASCADE` to automatically clean up child records when a financial_position_log is
deleted.

## Container Configuration

- **Image**: CockroachDB (via testcontainers)
- **Database**: Service-specific test database
- **Wait Strategy**: Container readiness check with timeout
- **Connection**: SSL disabled for test performance

## Performance

### Container Startup

- **Cold start**: ~2-3 seconds (first test)
- **Subsequent tests**: ~1-2 seconds per container
- **Parallel execution**: Supported (each test gets own container)

### Best Practices

1. **Use defer for cleanup**:

   ```go
   db, cleanup := testdb.SetupCockroachDB(t, nil)
   defer cleanup()
   ```

2. **Run tests in parallel** (when safe):

   ```go
   func TestConcurrentOperations(t *testing.T) {
       t.Parallel()
       db, cleanup := testdb.SetupCockroachDB(t, nil)
       defer cleanup()
   }
   ```

3. **Cache test data creation**:

   ```go
   var testLog *domain.FinancialPositionLog

   func getTestLog(t *testing.T) *domain.FinancialPositionLog {
       if testLog == nil {
           testLog = createComplexLog(t)
       }
       return testLog
   }
   ```

## Examples

### Basic Repository Test

```go
func TestCreate(t *testing.T) {
    db, cleanup := testdb.SetupCockroachDB(t, nil)
    defer cleanup()

    repo := repository.NewRepository(db)
    log := createTestLog(t, "ACC-001")
    err := repo.Create(context.Background(), log)
    require.NoError(t, err)

    // Verify
    retrieved, err := repo.FindByID(context.Background(), log.LogID)
    require.NoError(t, err)
    assert.Equal(t, log.LogID, retrieved.LogID)
}
```

### Batch Operation Test

```go
func TestCreateBatch(t *testing.T) {
    db, cleanup := testdb.SetupCockroachDB(t, nil)
    defer cleanup()

    repo := repository.NewRepository(db)
    logs := make([]*domain.FinancialPositionLog, 100)
    for i := 0; i < 100; i++ {
        logs[i] = createTestLog(t, fmt.Sprintf("ACC-%03d", i))
    }

    err := repo.CreateBatch(context.Background(), logs)
    require.NoError(t, err)
}
```

### Direct SQL Test

```go
func TestCustomQuery(t *testing.T) {
    db, cleanup := testdb.SetupCockroachDB(t, nil)
    defer cleanup()

    repo := repository.NewRepository(db)

    // Insert test data
    log := createTestLog(t, "ACC-001")
    err := repo.Create(context.Background(), log)
    require.NoError(t, err)

    // Run custom query via GORM's underlying connection
    var status string
    db.Raw(`SELECT current_status
            FROM position_keeping.financial_position_logs
            WHERE log_id = ?`, log.LogID).Scan(&status)
    assert.Equal(t, "PENDING", status)
}
```

### Benchmark Test

```go
func BenchmarkCreate(b *testing.B) {
    // Setup once for all iterations
    t := &testing.T{}
    db, cleanup := testdb.SetupCockroachDB(t, nil)
    defer cleanup()

    repo := repository.NewRepository(db)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        log := createTestLog(t, fmt.Sprintf("ACC-%d", i))
        _ = repo.Create(context.Background(), log)
    }
}
```

## Troubleshooting

### Container Won't Start

**Problem**: Timeout waiting for database

**Solution**: Check Docker is running and has resources:

```bash
docker ps
docker system df
```

### Schema Load Fails

**Problem**: Foreign key constraint errors

**Solution**: Ensure tables are created in correct order:

1. financial_position_logs (parent)
2. transaction_log_entries (child)
3. transaction_lineages (child)
4. audit_trail_entries (child)

### Connection Pool Exhausted

**Problem**: Too many open connections

**Solution**: Ensure `cleanup()` is called with defer:

```go
db, cleanup := testdb.SetupCockroachDB(t, nil)
defer cleanup()  // CRITICAL - must use defer
```

### Slow Tests

**Problem**: Tests taking too long

**Solutions**:

- Run tests in parallel: `t.Parallel()`
- Use fewer containers: Share container across subtests with `t.Run()`
- Cache test data: Create complex aggregates once, reuse them

## Migration from Inline Setup

If you have tests with inline testcontainer setup, migrate to `testdb.SetupCockroachDB`:

### Before

```go
func TestOldWay(t *testing.T) {
    ctx := context.Background()
    container, err := cockroach.Run(ctx, "cockroachdb/cockroach:latest-v24.3", ...)
    require.NoError(t, err)
    defer container.Terminate(ctx)

    connStr, _ := container.ConnectionString(ctx)
    db, _ := gorm.Open(postgres.Open(connStr), &gorm.Config{})

    // Load schema manually...
    db.Exec("CREATE TABLE...")

    repo := repository.NewRepository(db)
    // ... test code ...
}
```

### After

```go
func TestNewWay(t *testing.T) {
    db, cleanup := testdb.SetupCockroachDB(t, nil)
    defer cleanup()

    repo := repository.NewRepository(db)
    // ... test code ...
}
```

**Benefits**:

- 90% less boilerplate
- Consistent schema across tests
- Better error handling
- Easier to maintain

## See Also

- [repository_test.go][repo-test] - Example integration tests
- [testcontainers-go docs](https://golang.testcontainers.org/) - Official documentation

[repo-test]: ../../services/position-keeping/adapters/persistence/postgres_repository_test.go
