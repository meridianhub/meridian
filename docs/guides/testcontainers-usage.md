# Testcontainers Usage Guide

This guide describes how to use testcontainers for CockroachDB integration tests across Meridian services.

**Shared utility**: `shared/platform/testdb/` - Provides reusable testcontainer setup for all services via `testdb.SetupCockroachDB`.

The examples below use position-keeping but the pattern applies to all services.

## Overview

The testhelpers package implements a complete testing environment with:

- **Isolated CockroachDB containers** - Each test gets its own database
- **Automatic schema setup** - Service schema loaded automatically
- **GORM database handle** - Configured and ready for repository use
- **Repository instances** - Pre-configured repository for immediate use
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
   tc := testhelpers.SetupTestContainer(t)
   defer tc.Cleanup(t)
   ```

2. **Run tests in parallel** (when safe):

   ```go
   func TestConcurrentOperations(t *testing.T) {
       t.Parallel()
       tc := testhelpers.SetupTestContainer(t)
       defer tc.Cleanup(t)
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
    tc := testhelpers.SetupTestContainer(t)
    defer tc.Cleanup(t)

    log := createTestLog(t, "ACC-001")
    err := tc.Repo.Create(context.Background(), log)
    require.NoError(t, err)

    // Verify
    retrieved, err := tc.Repo.FindByID(context.Background(), log.LogID)
    require.NoError(t, err)
    assert.Equal(t, log.LogID, retrieved.LogID)
}
```

### Batch Operation Test

```go
func TestCreateBatch(t *testing.T) {
    tc := testhelpers.SetupTestContainer(t)
    defer tc.Cleanup(t)

    logs := make([]*domain.FinancialPositionLog, 100)
    for i := 0; i < 100; i++ {
        logs[i] = createTestLog(t, fmt.Sprintf("ACC-%03d", i))
    }

    err := tc.Repo.CreateBatch(context.Background(), logs)
    require.NoError(t, err)
}
```

### Direct SQL Test

```go
func TestCustomQuery(t *testing.T) {
    tc := testhelpers.SetupTestContainer(t)
    defer tc.Cleanup(t)

    // Insert test data
    log := createTestLog(t, "ACC-001")
    err := tc.Repo.Create(context.Background(), log)
    require.NoError(t, err)

    // Run custom query
    var status string
    err = tc.Pool.QueryRow(context.Background(),
        `SELECT current_status
         FROM position_keeping.financial_position_logs
         WHERE log_id = $1`,
        log.LogID).Scan(&status)
    require.NoError(t, err)
    assert.Equal(t, "PENDING", status)
}
```

### Benchmark Test

```go
func BenchmarkCreate(b *testing.B) {
    // Setup once for all iterations
    tc := testhelpers.SetupTestContainer(&testing.T{})
    defer tc.Cleanup(&testing.T{})

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        log := createTestLog(&testing.T{}, fmt.Sprintf("ACC-%d", i))
        _ = tc.Repo.Create(context.Background(), log)
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

**Solution**: Ensure `Cleanup()` is called with defer:

```go
tc := testhelpers.SetupTestContainer(t)
defer tc.Cleanup(t)  // CRITICAL - must use defer
```

### Slow Tests

**Problem**: Tests taking too long

**Solutions**:

- Run tests in parallel: `t.Parallel()`
- Use fewer containers: Share container across subtests with `t.Run()`
- Cache test data: Create complex aggregates once, reuse them

## Migration from Inline Setup

If you have tests with inline testcontainer setup, migrate to this package:

### Before

```go
func TestOldWay(t *testing.T) {
    ctx := context.Background()
    pgContainer, err := postgres.Run(ctx, "postgres:16-alpine", ...)
    require.NoError(t, err)
    defer pgContainer.Terminate(ctx)

    connStr, _ := pgContainer.ConnectionString(ctx, "sslmode=disable")
    pool, _ := pgxpool.New(ctx, connStr)
    defer pool.Close()

    // Load schema manually...
    _, err = pool.Exec(ctx, "CREATE TABLE...")

    repo := repository.NewPostgresRepository(pool)
    // ... test code ...
}
```

### After

```go
func TestNewWay(t *testing.T) {
    tc := testhelpers.SetupTestContainer(t)
    defer tc.Cleanup(t)

    // ... test code using tc.Repo ...
}
```

**Benefits**:

- 90% less boilerplate
- Consistent schema across tests
- Better error handling
- Easier to maintain

## See Also

- [postgres_repository_test.go][repo-test] - Example integration tests
- [postgres_repository_bench_test.go][repo-bench] - Example benchmarks
- [testcontainers-go docs](https://golang.testcontainers.org/) - Official documentation

[repo-test]: ../../services/position-keeping/repository/postgres_repository_test.go
[repo-bench]: ../../services/position-keeping/repository/postgres_repository_bench_test.go
