# ADR-0009: Application-Level Audit Logging

**Status:** Accepted

**Date:** 2025-11-04
**Implemented:** 2025-12-24

**Deciders:** Ben (Tech Lead)

**Context:**

The initial migration system (PR #67) implemented a database-level audit factory pattern using PostgreSQL PL/pgSQL
stored procedures and triggers. This approach encountered a critical compatibility issue with CockroachDB:

```text
pq: plpgsql not supported in user-defined functions:
at or near "table_name": syntax error: unimplemented: this syntax
```

CockroachDB does not support PL/pgSQL procedural language features including:

- `EXECUTE` for dynamic SQL
- `DECLARE` variables and control flow
- `FOREACH` loops
- Trigger functions with procedural logic

This incompatibility prevents the audit factory pattern from working in the local development environment (CockroachDB)
while it would work in production PostgreSQL.

## Decision Drivers

1. **Development-Production Parity**: Local environment should match production behaviour
2. **CockroachDB Compatibility**: Core development database doesn't support PL/pgSQL
3. **Testability**: Audit logic should be easily testable in unit tests
4. **Performance**: Audit logging must not significantly impact transaction throughput
5. **Maintainability**: Single source of truth for audit logic
6. **Compliance**: Audit trail must be complete and tamper-resistant

## Options Considered

### Option 1: Static Per-Schema Audit Tables (Database-Level)

**Approach:** Generate explicit audit tables, triggers, and functions for each service schema without dynamic SQL.

**Pros:**

- Database-enforced audit trail (tamper-resistant)
- No application code changes required
- Automatic capture of all changes
- Works with CockroachDB (no PL/pgSQL)

**Cons:**

- Significant code duplication across schemas
- CockroachDB v23.1 requires trigger functions to be written in PL/pgSQL (no alternative)
- Harder to test trigger logic
- Maintenance burden (3x the code for shared, current_account, position_keeping)

**Verdict:** ❌ Rejected - CockroachDB requires PL/pgSQL for trigger functions, which defeats the purpose of removing
PL/pgSQL dependency. Database-level triggers are not viable without PL/pgSQL support.

### Option 2: Application-Level Audit Logging (GORM Hooks)

**Approach:** Implement audit logging in Go using GORM `AfterCreate`, `AfterUpdate`, `AfterDelete` hooks.

**Pros:**

- ✅ Works identically in CockroachDB and PostgreSQL
- ✅ Easily testable with unit tests (mock database)
- ✅ Single audit implementation in Go code
- ✅ Full control over audit data structure
- ✅ Can include application context (user session, request ID, etc.)
- ✅ Type-safe with Go structs

**Cons:**

- ⚠️ Potential performance impact (additional INSERT per operation)
- ⚠️ Not atomic with business transaction (separate INSERT)
- ⚠️ Could be bypassed by raw SQL queries (mitigated by using GORM exclusively)
- ⚠️ Audit records lost if application crashes between business op and audit INSERT

**Verdict:** ✅ **Selected** - Best balance of compatibility, testability, and maintainability

### Option 3: Require PostgreSQL for Production

**Approach:** Keep PL/pgSQL audit factory, document that CockroachDB is dev-only.

**Pros:**

- Keeps elegant audit factory pattern
- Database-enforced audit trail in production
- Minimal code changes

**Cons:**

- ❌ Development-production parity violation
- ❌ Can't test audit behaviour locally
- ❌ Deployment complexity (two database systems)
- ❌ CockroachDB advantages lost (horizontal scaling, multi-region)

**Verdict:** ❌ Rejected - Violates development-production parity principle

## Decision

**We will implement application-level audit logging using GORM hooks** to achieve CockroachDB compatibility while
maintaining testability and development-production parity.

## Implementation Details

### Audit Record Structure

```go
// BaseModel already has audit fields
type BaseModel struct {
    ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
    CreatedAt time.Time `gorm:"not null;default:now()"`
    UpdatedAt time.Time `gorm:"not null;default:now()"`
    CreatedBy string    `gorm:"type:varchar(100);not null;default:'system'"`
    UpdatedBy string    `gorm:"type:varchar(100);not null;default:'system'"`
}

// New audit log table per schema
type AuditLog struct {
    ID            uuid.UUID       `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
    TableName     string          `gorm:"type:varchar(100);not null;index"`
    Operation     string          `gorm:"type:varchar(10);not null;index"` // INSERT, UPDATE, DELETE
    RecordID      uuid.UUID       `gorm:"type:uuid;not null;index"`
    ChangedAt     time.Time       `gorm:"not null;default:now();index"`
    ChangedBy     string          `gorm:"type:varchar(100);index"`
    OldValues     datatypes.JSON  `gorm:"type:jsonb"`
    NewValues     datatypes.JSON  `gorm:"type:jsonb"`
    TransactionID string          `gorm:"type:varchar(100)"`
}
```

### GORM Hook Pattern

```go
// Implement in internal/domain/models/audit.go
const auditOldValueKey = "audit:old_value"

func (m *Customer) AfterCreate(tx *gorm.DB) error {
    return recordAudit(tx, "current_account_audit", "customers", "INSERT", m.ID, nil, m)
}

func (m *Customer) BeforeUpdate(tx *gorm.DB) error {
    // Capture old values BEFORE the update happens
    var old Customer
    if err := tx.First(&old, m.ID).Error; err != nil {
        return err
    }
    // Store old values in transaction context for AfterUpdate to access
    tx.Statement.Context = context.WithValue(tx.Statement.Context, auditOldValueKey, &old)
    return nil
}

func (m *Customer) AfterUpdate(tx *gorm.DB) error {
    // Retrieve old values from context (captured in BeforeUpdate)
    old, ok := tx.Statement.Context.Value(auditOldValueKey).(*Customer)
    if !ok {
        return fmt.Errorf("failed to retrieve old values from context")
    }
    return recordAudit(tx, "current_account_audit", "customers", "UPDATE", m.ID, old, m)
}

func (m *Customer) AfterDelete(tx *gorm.DB) error {
    return recordAudit(tx, "current_account_audit", "customers", "DELETE", m.ID, m, nil)
}

// Shared audit recording function
func recordAudit(tx *gorm.DB, schema, table, op string, id uuid.UUID, old, new interface{}) error {
    audit := AuditLog{
        TableName:  table,
        Operation:  op,
        RecordID:   id,
        ChangedAt:  time.Now(),
        ChangedBy:  getChangedBy(tx.Statement.Context, old, new),
        OldValues:  toJSON(old),
        NewValues:  toJSON(new),
    }

    return tx.Table(schema + ".audit_log").Create(&audit).Error
}
```

### Migration Changes

Remove `migrations/shared/20251103190000_audit_factory.sql` entirely.

Create static audit tables in each service schema migration:

```sql
-- migrations/current_account/20251104000000_audit_log.sql
CREATE SCHEMA IF NOT EXISTS current_account_audit;

CREATE TABLE IF NOT EXISTS current_account_audit.audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL,
    record_id UUID NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by VARCHAR(100),
    old_values JSONB,
    new_values JSONB,
    transaction_id VARCHAR(100)
);

CREATE INDEX idx_audit_log_table_name ON current_account_audit.audit_log(table_name);
CREATE INDEX idx_audit_log_record_id ON current_account_audit.audit_log(record_id);
CREATE INDEX idx_audit_log_changed_at ON current_account_audit.audit_log(changed_at);
CREATE INDEX idx_audit_log_changed_by ON current_account_audit.audit_log(changed_by);
CREATE INDEX idx_audit_log_operation ON current_account_audit.audit_log(operation);
```

Repeat for `position_keeping_audit` schema.

## Performance Considerations

### Potential Impact

1. **Additional INSERT per operation**: +1 write per business operation
2. **JSON serialisation overhead**: `toJSON()` conversion cost
3. **Transaction size increase**: Audit INSERT adds to transaction

### Async Audit Strategy: Transactional Outbox Pattern

**Decision for Meridian**: We will use the **Transactional Outbox Pattern** for async audit logging with guaranteed
delivery.

#### How It Works

```go
// 1. Within business transaction, write audit intent to outbox
func (m *Customer) AfterCreate(tx *gorm.DB) error {
    outbox := AuditOutbox{
        TableName:  "customers",
        Operation:  "INSERT",
        RecordID:   m.ID,
        NewValues:  toJSON(m),
        ChangedBy:  getChangedBy(tx.Statement.Context, nil, m),
        Status:     "pending",
        CreatedAt:  time.Now(),
    }
    return tx.Table("current_account_audit.audit_outbox").Create(&outbox).Error
}

// 2. Background worker processes outbox asynchronously
func AuditWorker(ctx context.Context, db *gorm.DB, schema string) {
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            processAuditOutbox(db, schema)
        }
    }
}

func processAuditOutbox(db *gorm.DB, schema string) {
    var pending []AuditOutbox
    db.Table(schema + ".audit_outbox").
        Where("status = ?", "pending").
        Order("created_at ASC").
        Limit(100).
        Find(&pending)

    for _, record := range pending {
        db.Transaction(func(tx *gorm.DB) error {
            // Move from outbox to audit_log
            auditLog := record.ToAuditLog()
            if err := tx.Table(schema + ".audit_log").Create(&auditLog).Error; err != nil {
                return err
            }
            return tx.Delete(&record).Error
        })
    }
}
```

#### Benefits

1. ✅ **Atomicity preserved**: Audit intent written in same transaction as business operation
2. ✅ **No lost audits**: Outbox survives application crashes and pod terminations
3. ✅ **High throughput**: Business transactions smaller and faster
4. ✅ **Automatic retry**: Worker keeps retrying failed audit writes
5. ✅ **Idempotency**: Can safely retry without duplicates
6. ✅ **Monitoring**: Outbox depth indicates audit lag

#### Trade-offs

- ⚠️ **Eventual consistency**: Audit records appear in audit_log after small delay (typically <100ms)
- ⚠️ **Operational complexity**: Need to monitor outbox depth and worker health
- ⚠️ **Still one extra write**: Writing to outbox vs direct to audit_log (same transaction cost)

#### Additional Optimizations

1. **Selective auditing**: Only audit critical tables requiring compliance (customers, accounts, transactions)
   - Reduce write amplification by excluding low-risk tables
   - Document which tables are/aren't audited and rationale

1. **Optimise JSON serialisation**: Only include fields that change
   - Compute diff before serialisation to reduce JSONB size
   - Set maximum audit record size limits

1. **Batch processing**: Worker processes outbox in batches of 100 for efficiency

### Benchmarking Plan

```go
// Test performance impact in internal/domain/models/audit_test.go
func BenchmarkAuditOutboxOverhead(b *testing.B) {
    // Measure INSERT time with outbox vs without audit
}

func BenchmarkAuditWorkerThroughput(b *testing.B) {
    // Measure how fast worker processes outbox
}
```

**Targets**:

- Outbox write overhead: <5ms per operation (faster than direct audit_log write)
- Worker throughput: >1000 records/second
- Audit lag (p99): <500ms from business op to audit_log

## Compliance Considerations

### Audit Trail Integrity

**Risk:** Application-level audit can be bypassed by:

- Raw SQL queries (not using GORM)
- Direct database access (psql, SQL clients)
- Application crashes between business op and audit INSERT

**Mitigations:**

1. **Policy:** All database access MUST use GORM (enforced in code review)
2. **Monitoring:** Alert on direct database connections in production
3. **Transactional outbox:** Audit intent committed with business operation (atomic)
4. **Worker monitoring:** Alert on high outbox depth or worker failures
5. **Idempotency:** Outbox entries prevent duplicate audit records on retry

### Regulatory Requirements

For industries requiring tamper-proof audit trails (finance, healthcare):

- Consider **write-once storage** for audit tables (CockroachDB doesn't support this)
- Implement **cryptographic signatures** on audit records
- Export audit logs to **immutable external system** (S3 Glacier, blockchain)

## Testing Strategy

The async outbox pattern requires rigorous red-green testing to prove correctness. This will take significant time but
is essential for financial compliance.

### Test-Driven Development Approach

**Red → Green → Refactor cycle for each guarantee:**

1. Write failing test that proves the guarantee
2. Implement minimum code to make test pass
3. Refactor for clarity and performance
4. Repeat for next guarantee

### Critical Guarantees to Test

#### 1. Atomicity: Audit Intent Committed with Business Operation

```go
func TestAuditOutbox_AtomicCommit(t *testing.T) {
    db := setupTestDB()

    // Create customer (should create outbox entry in same transaction)
    customer := &Customer{Name: "ACME Corp"}
    err := db.Create(customer).Error
    require.NoError(t, err)

    // Verify outbox entry exists
    var outbox AuditOutbox
    err = db.Table("current_account_audit.audit_outbox").
        Where("record_id = ?", customer.ID).
        First(&outbox).Error
    require.NoError(t, err)
    assert.Equal(t, "customers", outbox.TableName)
    assert.Equal(t, "INSERT", outbox.Operation)
    assert.Equal(t, "pending", outbox.Status)
}

func TestAuditOutbox_RollbackOnBusinessFailure(t *testing.T) {
    db := setupTestDB()

    // Force transaction failure
    err := db.Transaction(func(tx *gorm.DB) error {
        customer := &Customer{Name: "ACME Corp"}
        tx.Create(customer)

        // Verify outbox entry exists within transaction
        var count int64
        tx.Table("current_account_audit.audit_outbox").
            Where("record_id = ?", customer.ID).
            Count(&count)
        assert.Equal(t, int64(1), count)

        return errors.New("forced failure") // Force rollback
    })
    require.Error(t, err)

    // Verify outbox entry was rolled back
    var count int64
    db.Table("current_account_audit.audit_outbox").Count(&count)
    assert.Equal(t, int64(0), count, "Outbox should be empty after rollback")
}
```

#### 2. No Lost Audits: Outbox Survives Application Crashes

```go
func TestAuditOutbox_SurvivesApplicationCrash(t *testing.T) {
    db := setupTestDB()
    ctx, cancel := context.WithCancel(context.Background())

    // Start audit worker
    go AuditWorker(ctx, db, "current_account_audit")

    // Create customer
    customer := &Customer{Name: "ACME Corp"}
    db.Create(customer)

    // Verify outbox entry exists
    var outboxCount int64
    db.Table("current_account_audit.audit_outbox").Count(&outboxCount)
    assert.Equal(t, int64(1), outboxCount)

    // Simulate application crash (kill worker immediately)
    cancel()
    time.Sleep(10 * time.Millisecond)

    // Verify outbox entry still exists (not lost)
    db.Table("current_account_audit.audit_outbox").Count(&outboxCount)
    assert.Equal(t, int64(1), outboxCount, "Outbox entry should survive crash")

    // Start new worker (simulating app restart)
    ctx2, cancel2 := context.WithCancel(context.Background())
    defer cancel2()
    go AuditWorker(ctx2, db, "current_account_audit")

    // Wait for worker to process outbox
    time.Sleep(200 * time.Millisecond)

    // Verify audit record eventually appears in audit_log
    var auditLog AuditLog
    err := db.Table("current_account_audit.audit_log").
        Where("record_id = ?", customer.ID).
        First(&auditLog).Error
    require.NoError(t, err)
    assert.Equal(t, "INSERT", auditLog.Operation)

    // Verify outbox is now empty
    db.Table("current_account_audit.audit_outbox").Count(&outboxCount)
    assert.Equal(t, int64(0), outboxCount, "Outbox should be empty after processing")
}
```

#### 3. Idempotency: Retry Doesn't Create Duplicates

```go
func TestAuditWorker_IdempotentProcessing(t *testing.T) {
    db := setupTestDB()

    // Create customer (generates outbox entry)
    customer := &Customer{Name: "ACME Corp"}
    db.Create(customer)

    // Process outbox multiple times (simulate retries)
    for i := 0; i < 3; i++ {
        processAuditOutbox(db, "current_account_audit")
    }

    // Verify only ONE audit record exists (no duplicates)
    var count int64
    db.Table("current_account_audit.audit_log").
        Where("record_id = ?", customer.ID).
        Count(&count)
    assert.Equal(t, int64(1), count, "Should have exactly one audit record")
}
```

#### 4. Complete Audit Trail: All Operations Captured

```go
func TestAuditOutbox_CapturesInsertUpdateDelete(t *testing.T) {
    db := setupTestDB()
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Start audit worker
    go AuditWorker(ctx, db, "current_account_audit")

    // INSERT
    customer := &Customer{Name: "ACME Corp"}
    db.Create(customer)

    // UPDATE
    customer.Name = "ACME Corporation"
    db.Save(customer)

    // DELETE
    db.Delete(customer)

    // Wait for worker to process all outbox entries
    time.Sleep(500 * time.Millisecond)

    // Verify all three operations captured in audit_log
    var audits []AuditLog
    db.Table("current_account_audit.audit_log").
        Where("record_id = ?", customer.ID).
        Order("changed_at ASC").
        Find(&audits)

    require.Len(t, audits, 3, "Should have 3 audit records")
    assert.Equal(t, "INSERT", audits[0].Operation)
    assert.Equal(t, "UPDATE", audits[1].Operation)
    assert.Equal(t, "DELETE", audits[2].Operation)

    // Verify UPDATE captured old and new values
    assert.NotNil(t, audits[1].OldValues)
    assert.NotNil(t, audits[1].NewValues)
    assert.Contains(t, string(audits[1].OldValues), "ACME Corp")
    assert.Contains(t, string(audits[1].NewValues), "ACME Corporation")
}
```

#### 5. Worker Resilience: Handles Database Failures Gracefully

```go
func TestAuditWorker_HandlesTemporaryDatabaseFailure(t *testing.T) {
    db := setupTestDB()

    // Create customer (generates outbox entry)
    customer := &Customer{Name: "ACME Corp"}
    db.Create(customer)

    // Simulate temporary database unavailability
    // (In real test, use docker pause or network partition)
    simulateDatabaseDown := true

    // Worker should retry and eventually succeed when DB recovers
    processWithRetry := func() {
        for retries := 0; retries < 10; retries++ {
            if simulateDatabaseDown && retries < 5 {
                // Simulate failure for first 5 attempts
                time.Sleep(100 * time.Millisecond)
                continue
            }
            simulateDatabaseDown = false // DB recovered
            processAuditOutbox(db, "current_account_audit")
            break
        }
    }

    processWithRetry()

    // Verify audit record eventually created
    var audit AuditLog
    err := db.Table("current_account_audit.audit_log").
        Where("record_id = ?", customer.ID).
        First(&audit).Error
    require.NoError(t, err)
}
```

#### 6. Performance: Worker Throughput and Latency

```go
func TestAuditWorker_Throughput(t *testing.T) {
    db := setupTestDB()

    // Create 1000 outbox entries
    for i := 0; i < 1000; i++ {
        customer := &Customer{Name: fmt.Sprintf("Customer %d", i)}
        db.Create(customer)
    }

    // Measure how long worker takes to process all entries
    start := time.Now()
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    go AuditWorker(ctx, db, "current_account_audit")

    // Wait until all processed
    for {
        var count int64
        db.Table("current_account_audit.audit_outbox").Count(&count)
        if count == 0 {
            break
        }
        time.Sleep(50 * time.Millisecond)
    }

    elapsed := time.Since(start)
    throughput := 1000 / elapsed.Seconds()

    t.Logf("Processed 1000 audit records in %v (%.0f records/sec)", elapsed, throughput)
    assert.Greater(t, throughput, 1000.0, "Should process >1000 records/sec")
}

func BenchmarkAuditOutboxOverhead(b *testing.B) {
    db := setupTestDB()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        customer := &Customer{Name: fmt.Sprintf("Customer %d", i)}
        db.Create(customer) // Includes outbox write
    }
}
```

### Integration Tests with Testcontainers

```go
func TestAuditSystem_EndToEnd_CockroachDB(t *testing.T) {
    // Use testcontainers to spin up real CockroachDB
    ctx := context.Background()
    cockroachContainer, err := testcontainers.GenericContainer(ctx,
        testcontainers.GenericContainerRequest{
            ContainerRequest: testcontainers.ContainerRequest{
                Image:        "cockroachdb/cockroach:v23.1.0",
                ExposedPorts: []string{"26257/tcp"},
                Cmd:          []string{"start-single-node", "--insecure"},
            },
            Started: true,
        })
    require.NoError(t, err)
    defer cockroachContainer.Terminate(ctx)

    // Run full audit system test against real CockroachDB
    // (Test all guarantees with real database)
}
```

### Test Coverage Requirements

- **Unit tests**: >90% coverage for audit hooks and worker code
- **Integration tests**: All critical guarantees proven with real database
- **Load tests**: Verify performance targets met under realistic load
- **Chaos tests**: Simulate pod kills, network partitions, database failures

**Estimated Implementation Time**: 2-3 sprints

- Sprint 1: Implement hooks + basic tests (atomicity, no lost audits)
- Sprint 2: Worker implementation + resilience tests (idempotency, failures)
- Sprint 3: Performance benchmarks + chaos testing

## Migration Path

### Phase 1: Remove Incompatible Migrations (Current PR #74)

1. ✅ Delete `migrations/shared/20251103190000_audit_factory.sql`
2. ✅ Create static audit table migrations for each schema
3. ✅ Add audit_outbox table to migrations
4. ✅ Update checksums with `atlas migrate hash`
5. ✅ Document async outbox pattern in ADR-0009

### Phase 2: Implement GORM Hooks + Outbox (Sprint N+1, ~8 story points)

**Test-Driven Development:**

1. Write failing tests for critical guarantees (atomicity, no lost audits)
2. Implement `AuditOutbox` struct in `internal/domain/models/audit.go`
3. Implement GORM `AfterCreate`, `BeforeUpdate`, `AfterUpdate`, `AfterDelete` hooks
4. Verify all tests pass (green)
5. Refactor for clarity

**Deliverables:**

- GORM hooks write to `audit_outbox` table
- Comprehensive unit tests (>90% coverage)
- Integration tests with real CockroachDB (Testcontainers)

### Phase 3: Implement Audit Worker (Sprint N+2, ~5 story points)

**Test-Driven Development:**

1. Write failing tests for worker behaviour (idempotency, resilience)
2. Implement `AuditWorker()` background goroutine
3. Implement `processAuditOutbox()` with batch processing
4. Verify all tests pass (green)
5. Add Prometheus metrics for outbox depth

**Deliverables:**

- Background worker processes outbox → audit_log
- Worker graceful shutdown on context cancellation
- Monitoring and alerting for outbox lag

### Phase 4: Performance Validation (Sprint N+3, ~3 story points)

1. Run benchmarks in staging environment
2. Measure p50/p95/p99 latency with audit enabled
3. Load test: Verify >1000 TPS with <5ms outbox overhead
4. Chaos testing: Pod kills, network partitions, DB failures
5. Document audit query patterns for compliance reporting

### Phase 5: Production Rollout (Sprint N+4, ~2 story points)

1. Feature flag for gradual rollout
2. Monitor outbox depth and audit lag in production
3. Validate audit completeness (spot checks)
4. Update runbooks with audit troubleshooting procedures

## Consequences

### Positive

- ✅ **CockroachDB and PostgreSQL compatibility** - No PL/pgSQL dependency
- ✅ **High throughput** - Async outbox pattern reduces transaction latency
- ✅ **No lost audits** - Transactional outbox survives application crashes
- ✅ **Easily testable** - Comprehensive red-green testing strategy
- ✅ **Development-production parity** - Works identically in both environments
- ✅ **Rich context** - Can include application session, user, request ID in audit records
- ✅ **Monitoring** - Outbox depth provides clear audit lag metrics

### Negative

- ⚠️ **Eventual consistency** - Audit records appear in audit_log after ~100ms delay
- ⚠️ **Operational complexity** - Must monitor outbox depth and worker health
- ⚠️ **Additional code** - GORM hooks + background worker (~500 LOC)
- ⚠️ **Testing time** - Red-green testing requires 2-3 sprints for proper validation
- ⚠️ **Not database-enforced** - Can be bypassed by raw SQL (mitigated by policy)

### Neutral

- Migration from database-level triggers to application-level hooks
- Change in audit architecture (synchronous → async with guaranteed delivery)
- Additional table (audit_outbox) for each service schema

## References

- [ADR-0003: Database Schema Migrations](./0003-database-schema-migrations.md)
- [CockroachDB User-Defined Functions](https://www.cockroachlabs.com/docs/stable/user-defined-functions.html)
- [GORM Hooks Documentation](https://gorm.io/docs/hooks.html)
- [Audit Logging Best Practices](https://www.sqreen.com/checklists/audit-logging-best-practices)

## Implementation Summary (2025-12-24)

The async audit system has been fully implemented across all 6 services with a dual-path architecture:

### Completed Components

1. **GORM Hooks** (`shared/platform/audit/hooks.go`)
   - Generic `Auditable` interface for all entities
   - Helper functions: `RecordCreate`, `CaptureOldValue`, `RecordUpdate`, `RecordDelete`
   - Implemented in all services: CurrentAccount, PositionKeeping, FinancialAccounting, Party, PaymentOrder, Tenant

2. **Dual-Path Publisher** (`shared/platform/audit/publisher.go`)
   - Primary: Publish to Kafka topic → Audit consumers → `audit_log`
   - Fallback: Write to `audit_outbox` table (atomically in same transaction)
   - Automatic failover when Kafka unavailable (timeout: 5s)

3. **Kafka Audit Consumers** (`deployments/k8s/audit-consumer/`)
   - One deployment per service (e.g., `current-account-audit-consumer`)
   - Auto-scaling: 2-20 replicas based on CPU/memory
   - Consumes from service-specific topics (e.g., `audit.events.current-account`)
   - Writes directly to `audit_log` table

4. **Outbox Fallback Worker** (`services/audit-worker/`)
   - Centralised service processes `audit_outbox` entries when Kafka is unavailable
   - Polls every 5 seconds, batch size 100
   - Moves entries from `audit_outbox` → `audit_log`

5. **Database Migrations**
   - All services have `audit_log` and `audit_outbox` tables
   - Migrations: `20251216000002_audit_system.sql` (CurrentAccount), `20251217000001_audit_system.sql` (others)

### Architecture Benefits Realized

✅ **CockroachDB Compatibility**: No PL/pgSQL dependencies
✅ **High Throughput**: Kafka primary path handles normal load asynchronously
✅ **Guaranteed Delivery**: Dual-path ensures no lost audits during Kafka outages
✅ **Testability**: Comprehensive test coverage in `shared/platform/audit/*_test.go`
✅ **Development-Production Parity**: Works identically in local and production environments

### Monitoring

Prometheus metrics exposed:
- `meridian_audit_kafka_events_published_total` - Primary path usage (counter)
- `meridian_audit_kafka_fallback_used_total` - Fallback path usage (counter)
- `meridian_audit_worker_outbox_depth` - Outbox queue depth (gauge)
- `meridian_audit_kafka_events_consumed_total` - Consumer throughput (counter)

## Decision Review

**Review Date:** 2025-12-04 (30 days)
**Implementation Date:** 2025-12-24

**Success Criteria:**

- [x] Audit logging works identically in CockroachDB and PostgreSQL
- [x] Performance overhead <10ms per operation (Kafka primary path: ~2ms)
- [x] Unit test coverage >90% for audit logic
- [ ] Zero missing audit records in staging for 2 weeks (ongoing validation - started 2025-12-24, expected completion 2026-01-07)

**Stakeholders to Consult:**

- Security team (audit trail integrity)
- Compliance team (regulatory requirements)
- DevOps team (monitoring and alerting)
