# Adding Audit to a New Service

This guide walks through adding application-level audit logging to a new Meridian service.

## Prerequisites

- Service follows the standard Meridian directory structure (ADR-0015)
- Service uses GORM for database access
- Database migrations use Atlas (ADR-0003)

## Step 1: Add Database Migration

Create an audit system migration file in your service's migrations directory.

**File:** `services/<your-service>/migrations/YYYYMMDD000001_audit_system.sql`

```sql
-- Audit System for <Your Service>
-- Application-level audit logging (ADR-0009)
-- Uses unqualified table names (database-per-service architecture)

-- Create audit_log table (permanent audit records)
CREATE TABLE IF NOT EXISTS audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification (VARCHAR to support both UUID and string IDs)
    record_id VARCHAR(50) NOT NULL,

    -- Change metadata
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by VARCHAR(100),

    -- Change details (TEXT for compatibility with shared audit types)
    old_values TEXT,
    new_values TEXT,

    -- Additional context
    transaction_id VARCHAR(100),
    client_ip VARCHAR(45),
    user_agent TEXT
);

-- Indexes for efficient audit queries
CREATE INDEX IF NOT EXISTS idx_audit_log_table_name ON audit_log(table_name);
CREATE INDEX IF NOT EXISTS idx_audit_log_record_id ON audit_log(record_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_changed_at ON audit_log(changed_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_changed_by ON audit_log(changed_by);
CREATE INDEX IF NOT EXISTS idx_audit_log_operation ON audit_log(operation);

-- Create audit_outbox table (transactional outbox for async processing)
CREATE TABLE IF NOT EXISTS audit_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification (VARCHAR to support both UUID and string IDs)
    record_id VARCHAR(50) NOT NULL,

    -- Change details (TEXT for compatibility with shared audit types)
    old_values TEXT,
    new_values TEXT,

    -- Processing status (MUST include 'completed')
    status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retry_count INT NOT NULL DEFAULT 0,
    last_error TEXT,

    -- Additional context
    changed_by VARCHAR(100),
    transaction_id VARCHAR(100),
    client_ip VARCHAR(45),
    user_agent TEXT
);

-- Index for worker to efficiently find pending entries
CREATE INDEX IF NOT EXISTS idx_audit_outbox_status_created ON audit_outbox(status, created_at);

-- Optional: Create helper view for audit queries
-- Note: Uses TEXT columns, cast to JSONB only when valid JSON format
CREATE OR REPLACE VIEW change_summary AS
SELECT
    id,
    table_name,
    operation,
    record_id,
    changed_at,
    changed_by,
    CASE
        WHEN operation = 'UPDATE'
             AND new_values IS NOT NULL
             AND new_values != ''
             AND new_values ~ '^{.*}$' THEN
            COALESCE(
                (SELECT json_object_agg(key, value)
                 FROM jsonb_each(new_values::jsonb)
                 WHERE (old_values IS NULL
                        OR old_values = ''
                        OR NOT (old_values ~ '^{.*}$')
                        OR (old_values ~ '^{.*}$'
                            AND new_values::jsonb->key IS DISTINCT FROM old_values::jsonb->key
                        ))),
                '{}'::json
            )
        ELSE NULL
    END AS changed_fields,
    transaction_id
FROM audit_log
ORDER BY changed_at DESC;
```

## Step 2: Implement Auditable Interface on Entities

For each entity that requires audit logging:

**File:** `services/<your-service>/adapters/persistence/<entity>_entity.go`

```go
package persistence

import (
    "time"

    "github.com/google/uuid"
    "github.com/meridianhub/meridian/shared/platform/audit"
    "gorm.io/gorm"
)

// MyEntity represents the database persistence model.
type MyEntity struct {
    // Primary key
    ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

    // Business fields
    Name   string `gorm:"type:varchar(255);not null"`
    Status string `gorm:"type:varchar(20);not null;default:'ACTIVE'"`

    // Optimistic locking (recommended)
    Version int64 `gorm:"not null;default:1"`

    // Audit fields
    CreatedAt time.Time  `gorm:"not null;default:now()"`
    CreatedBy string     `gorm:"type:varchar(100);not null"`
    UpdatedAt time.Time  `gorm:"not null;default:now()"`
    UpdatedBy string     `gorm:"type:varchar(100);not null"`
    DeletedAt *time.Time `gorm:"index"` // Soft delete support
}

// TableName overrides the default table name.
func (MyEntity) TableName() string {
    return "my_entity"
}

// ================== Audit Interface Implementation ==================

// AuditID returns the record ID as a string for audit logging.
// Implements audit.Auditable interface.
func (e MyEntity) AuditID() string {
    return e.ID.String()
}

// AuditTableName returns the table name for audit logging.
// Implements audit.Auditable interface.
func (e MyEntity) AuditTableName() string {
    return e.TableName()
}

// ================== GORM Hooks for Audit ==================

// AfterCreate records an INSERT audit entry.
func (e *MyEntity) AfterCreate(tx *gorm.DB) error {
    return audit.RecordCreate(tx, *e)
}

// BeforeUpdate captures old values before the update.
// NOTE: Skipped for map-based updates (ID not populated).
func (e *MyEntity) BeforeUpdate(tx *gorm.DB) error {
    return audit.CaptureOldValue(tx, *e)
}

// AfterUpdate records an UPDATE audit entry with old and new values.
// NOTE: Skipped if BeforeUpdate was skipped (old values not captured).
func (e *MyEntity) AfterUpdate(tx *gorm.DB) error {
    return audit.RecordUpdate(tx, *e)
}

// AfterDelete records a DELETE audit entry.
func (e *MyEntity) AfterDelete(tx *gorm.DB) error {
    return audit.RecordDelete(tx, *e)
}
```

### For String-Based IDs

If your entity uses string IDs (like tenant IDs):

```go
type TenantEntity struct {
    ID string `gorm:"type:varchar(50);primaryKey"`
    // ... other fields
}

func (e TenantEntity) AuditID() string {
    return e.ID  // Return directly, no conversion needed
}
```

## Step 3: Handle Map-Based Updates

If your repository uses map-based updates for optimistic locking, GORM hooks are bypassed.
Use `audit.RecordUpdateManual()` instead:

```go
func (r *Repository) Update(ctx context.Context, entity *MyEntity) error {
    return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        // 1. Fetch old values for audit
        var oldEntity MyEntity
        if err := tx.First(&oldEntity, entity.ID).Error; err != nil {
            return err
        }

        // 2. Perform map-based update with optimistic locking
        newVersion := entity.Version + 1
        result := tx.Model(&MyEntity{}).
            Where("id = ? AND version = ?", entity.ID, entity.Version).
            Updates(map[string]interface{}{
                "name":       entity.Name,
                "status":     entity.Status,
                "version":    newVersion,
                "updated_at": time.Now(),
                "updated_by": audit.GetUserFromContext(ctx),
            })

        if result.Error != nil {
            return result.Error
        }
        if result.RowsAffected == 0 {
            return ErrOptimisticLock
        }

        // 3. Manually record audit
        entity.Version = newVersion
        return audit.RecordUpdateManual(tx, &oldEntity, entity)
    })
}
```

## Step 4: Initialize Audit in Service Main

Configure the audit system during service startup:

**File:** `services/<your-service>/cmd/main.go`

```go
package main

import (
    "context"
    "log/slog"
    "os"

    "github.com/meridianhub/meridian/shared/platform/audit"
)

func main() {
    ctx := context.Background()
    logger := slog.Default()

    // Initialize database connection
    db := initDatabase()

    // ================== Audit Configuration ==================

    // Set schema name for this service (used in metrics and Kafka)
    audit.SetSchemaName("my_service")

    // Configure Kafka publisher (optional - falls back to outbox if unavailable)
    if kafkaServers := os.Getenv("KAFKA_BOOTSTRAP_SERVERS"); kafkaServers != "" {
        publisher, err := audit.NewPublisher(audit.PublisherConfig{
            BootstrapServers: kafkaServers,
            Topic:            "audit.events.my-service",
            SchemaName:       "my_service",
            ClientID:         "my-service-audit-publisher",
        })
        if err == nil {
            audit.SetGlobalPublisher(publisher)
            defer publisher.Close()
            logger.Info("Kafka audit publisher configured")
        } else {
            logger.Warn("Kafka audit publisher not available, using outbox only", "error", err)
        }
    }

    // ... rest of service initialisation
}
```

## Step 5: Write Tests

Create integration tests for your audit hooks:

**File:** `services/<your-service>/adapters/persistence/<entity>_audit_test.go`

```go
package persistence_test

import (
    "context"
    "testing"

    "github.com/google/uuid"
    "github.com/meridianhub/meridian/shared/platform/audit"
    "github.com/meridianhub/meridian/shared/platform/auth"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
    t.Helper()
    db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
    require.NoError(t, err)

    // Create test tables
    err = db.AutoMigrate(&MyEntity{}, &testAuditOutbox{})
    require.NoError(t, err)

    return db
}

// testAuditOutbox is SQLite-compatible (no UUID/JSONB)
type testAuditOutbox struct {
    ID        string `gorm:"primaryKey"`
    Table     string `gorm:"column:table_name"`
    Operation string
    RecordID  string
    OldValues string
    NewValues string
    Status    string
    ChangedBy *string
}

func (testAuditOutbox) TableName() string {
    return "audit_outbox"
}

func TestMyEntity_AuditCreate(t *testing.T) {
    db := setupTestDB(t)

    entity := &MyEntity{
        ID:     uuid.New(),
        Name:   "Test Entity",
        Status: "ACTIVE",
    }

    err := db.Create(entity).Error
    require.NoError(t, err)

    // Verify audit entry created
    var outbox testAuditOutbox
    err = db.First(&outbox).Error
    require.NoError(t, err)

    assert.Equal(t, "my_entity", outbox.Table)
    assert.Equal(t, "INSERT", outbox.Operation)
    assert.Equal(t, entity.ID.String(), outbox.RecordID)
    assert.NotEmpty(t, outbox.NewValues)
    assert.Empty(t, outbox.OldValues)
}

func TestMyEntity_AuditUpdate(t *testing.T) {
    db := setupTestDB(t)

    // Create initial entity
    entity := &MyEntity{
        ID:     uuid.New(),
        Name:   "Original Name",
        Status: "ACTIVE",
    }
    err := db.Create(entity).Error
    require.NoError(t, err)

    // Clear the CREATE audit entry
    db.Exec("DELETE FROM audit_outbox")

    // Update entity
    entity.Name = "Updated Name"
    err = db.Save(entity).Error
    require.NoError(t, err)

    // Verify audit entry
    var outbox testAuditOutbox
    err = db.First(&outbox).Error
    require.NoError(t, err)

    assert.Equal(t, "UPDATE", outbox.Operation)
    assert.NotEmpty(t, outbox.OldValues)
    assert.NotEmpty(t, outbox.NewValues)
    assert.Contains(t, outbox.OldValues, "Original Name")
    assert.Contains(t, outbox.NewValues, "Updated Name")
}

func TestMyEntity_AuditDelete(t *testing.T) {
    db := setupTestDB(t)

    entity := &MyEntity{
        ID:     uuid.New(),
        Name:   "To Delete",
        Status: "ACTIVE",
    }
    err := db.Create(entity).Error
    require.NoError(t, err)

    // Clear CREATE audit
    db.Exec("DELETE FROM audit_outbox")

    // Delete entity
    err = db.Delete(entity).Error
    require.NoError(t, err)

    var outbox testAuditOutbox
    err = db.First(&outbox).Error
    require.NoError(t, err)

    assert.Equal(t, "DELETE", outbox.Operation)
    assert.NotEmpty(t, outbox.OldValues)
    assert.Empty(t, outbox.NewValues)
}

func TestMyEntity_AuditWithUser(t *testing.T) {
    db := setupTestDB(t)

    // Add user to context
    ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user-123")

    entity := &MyEntity{
        ID:   uuid.New(),
        Name: "With User",
    }

    err := db.WithContext(ctx).Create(entity).Error
    require.NoError(t, err)

    var outbox testAuditOutbox
    err = db.First(&outbox).Error
    require.NoError(t, err)

    require.NotNil(t, outbox.ChangedBy)
    assert.Equal(t, "user-123", *outbox.ChangedBy)
}
```

## Step 6: Update Migration Hash

After adding the migration, update the Atlas hash:

```bash
cd services/<your-service>
atlas migrate hash
```

## Verification Checklist

- [ ] Migration creates both `audit_log` and `audit_outbox` tables
- [ ] Migration includes `'completed'` status in CHECK constraint
- [ ] Entity implements `AuditID()` and `AuditTableName()` methods
- [ ] Entity has all four GORM hooks: `AfterCreate`, `BeforeUpdate`, `AfterUpdate`, `AfterDelete`
- [ ] Map-based updates use `audit.RecordUpdateManual()`
- [ ] Service main initializes `audit.SetSchemaName()`
- [ ] Integration tests verify audit entries are created
- [ ] Atlas hash updated

## Common Patterns

### Entities with Composite Keys

```go
func (e CompositeEntity) AuditID() string {
    return fmt.Sprintf("%s:%s", e.TenantID, e.ResourceID)
}
```

### Entities with Soft Delete

The `AfterDelete` hook works with GORM soft deletes. The audit records the soft delete operation.

### Selective Auditing

If you only want to audit certain tables, simply don't add hooks to non-audited entities.

## Troubleshooting

See [Audit Monitoring Guide](../operations/audit-monitoring.md) for operational troubleshooting.

## Related Documentation

- [ADR-0009: Application-Level Audit Logging](../adr/0009-application-level-audit-logging.md)
- [Audit Package README](../../shared/platform/audit/README.md)
- [ADR-0003: Database Schema Migrations](../adr/0003-database-schema-migrations.md)
