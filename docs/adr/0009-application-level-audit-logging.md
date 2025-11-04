# ADR-0009: Application-Level Audit Logging

**Status:** Proposed

**Date:** 2025-11-04

**Deciders:** Ben (Tech Lead)

**Context:**

The initial migration system (PR #67) implemented a database-level audit factory pattern using PostgreSQL PL/pgSQL stored procedures and triggers. This approach encountered a critical compatibility issue with CockroachDB:

```
pq: plpgsql not supported in user-defined functions:
at or near "table_name": syntax error: unimplemented: this syntax
```

CockroachDB does not support PL/pgSQL procedural language features including:
- `EXECUTE` for dynamic SQL
- `DECLARE` variables and control flow
- `FOREACH` loops
- Trigger functions with procedural logic

This incompatibility prevents the audit factory pattern from working in the local development environment (CockroachDB) while it would work in production PostgreSQL.

## Decision Drivers

1. **Development-Production Parity**: Local environment should match production behavior
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
- Still requires trigger functions (CockroachDB trigger support is limited)
- Harder to test trigger logic
- Maintenance burden (3x the code for shared, current_account, position_keeping)

**Verdict:** ❌ Rejected - CockroachDB trigger function support is still limited even without PL/pgSQL

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
- ❌ Can't test audit behavior locally
- ❌ Deployment complexity (two database systems)
- ❌ CockroachDB advantages lost (horizontal scaling, multi-region)

**Verdict:** ❌ Rejected - Violates development-production parity principle

## Decision

**We will implement application-level audit logging using GORM hooks** to achieve CockroachDB compatibility while maintaining testability and development-production parity.

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
func (m *Customer) AfterCreate(tx *gorm.DB) error {
    return recordAudit(tx, "current_account_audit", "customers", "INSERT", m.ID, nil, m)
}

func (m *Customer) AfterUpdate(tx *gorm.DB) error {
    var old Customer
    tx.First(&old, m.ID) // Get old values
    return recordAudit(tx, "current_account_audit", "customers", "UPDATE", m.ID, &old, m)
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
2. **JSON serialization overhead**: `toJSON()` conversion cost
3. **Transaction size increase**: Audit INSERT adds to transaction

### Mitigation Strategies

1. **Async audit logging**: Use goroutine + channel for non-blocking audit writes
2. **Batch audit writes**: Accumulate audit records and batch-insert periodically
3. **Selective auditing**: Only audit critical tables (customers, accounts, transactions)
4. **Audit sampling**: For high-volume tables, sample audit records (e.g., 10%)

### Benchmarking Plan

```go
// Test performance impact in internal/domain/models/audit_test.go
func BenchmarkAuditOverhead(b *testing.B) {
    // Measure INSERT time with/without audit hooks
}
```

Target: <10ms overhead per operation (acceptable for API latency budget of 100-200ms)

## Compliance Considerations

### Audit Trail Integrity

**Risk:** Application-level audit can be bypassed by:
- Raw SQL queries (not using GORM)
- Direct database access (psql, SQL clients)
- Application crashes between business op and audit INSERT

**Mitigations:**
1. **Policy:** All database access MUST use GORM (enforced in code review)
2. **Monitoring:** Alert on direct database connections in production
3. **Transaction wrapping:** Wrap business op + audit in same transaction
4. **Idempotency:** Use transaction ID to detect and recover missing audit records

### Regulatory Requirements

For industries requiring tamper-proof audit trails (finance, healthcare):
- Consider **write-once storage** for audit tables (CockroachDB doesn't support this)
- Implement **cryptographic signatures** on audit records
- Export audit logs to **immutable external system** (S3 Glacier, blockchain)

## Testing Strategy

### Unit Tests

```go
func TestCustomerAuditLogging(t *testing.T) {
    db := setupTestDB()

    customer := &Customer{Name: "ACME Corp"}
    db.Create(customer)

    var audit AuditLog
    db.Table("current_account_audit.audit_log").
        Where("record_id = ?", customer.ID).
        First(&audit)

    assert.Equal(t, "INSERT", audit.Operation)
    assert.Equal(t, "customers", audit.TableName)
    assert.NotNil(t, audit.NewValues)
}
```

### Integration Tests

```go
func TestAuditTransactionAtomicity(t *testing.T) {
    // Verify audit record is rolled back when business transaction fails
}

func TestAuditPerformanceOverhead(t *testing.T) {
    // Measure latency impact of audit logging
}
```

## Migration Path

### Phase 1: Remove Incompatible Migrations (Immediate)

1. Delete `migrations/shared/20251103190000_audit_factory.sql`
2. Create new audit table migrations for each schema
3. Update checksums with `atlas migrate hash`

### Phase 2: Implement Application-Level Audit (Sprint N+1)

1. Create `internal/domain/models/audit.go` with hook implementations
2. Add GORM hooks to `Customer`, `Account`, `Transaction` models
3. Write comprehensive unit tests

### Phase 3: Validate and Benchmark (Sprint N+2)

1. Run performance benchmarks
2. Validate audit completeness in staging environment
3. Document audit query patterns for compliance reporting

## Consequences

### Positive

- ✅ CockroachDB and PostgreSQL compatibility achieved
- ✅ Audit logic is easily testable
- ✅ Single source of truth in Go code
- ✅ Development-production parity maintained
- ✅ Can include rich application context in audit records

### Negative

- ⚠️ Potential performance impact (mitigated by async/batch strategies)
- ⚠️ Audit trail not database-enforced (mitigated by policy and monitoring)
- ⚠️ Additional application code to maintain
- ⚠️ Risk of missing audit records on application crash (mitigated by transaction wrapping)

### Neutral

- Migration from database-level to application-level approach
- Change in audit implementation location (database → application)

## References

- [ADR-0003: Database Schema Migrations](./0003-database-schema-migrations.md)
- [CockroachDB User-Defined Functions](https://www.cockroachlabs.com/docs/stable/user-defined-functions.html)
- [GORM Hooks Documentation](https://gorm.io/docs/hooks.html)
- [Audit Logging Best Practices](https://www.sqreen.com/checklists/audit-logging-best-practices)

## Decision Review

**Review Date:** 2025-12-04 (30 days)

**Success Criteria:**
- [ ] Audit logging works identically in CockroachDB and PostgreSQL
- [ ] Performance overhead <10ms per operation
- [ ] Unit test coverage >90% for audit logic
- [ ] Zero missing audit records in staging for 2 weeks

**Stakeholders to Consult:**
- Security team (audit trail integrity)
- Compliance team (regulatory requirements)
- DevOps team (monitoring and alerting)
