# Audit Hook Helpers

This package provides reusable generic helper functions for adding audit logging to GORM entities.
Instead of copying the same hook patterns to each model, you can implement a simple interface
and call the helper functions.

## Quick Start

### 1. Implement the `Auditable` interface on your entity

```go
import "github.com/meridianhub/meridian/shared/platform/audit"

type MyEntity struct {
    ID   uuid.UUID `gorm:"type:uuid;primaryKey"`
    Name string
    // ... other fields
}

// TableName returns the GORM table name
func (MyEntity) TableName() string {
    return "my_entity"
}

// AuditID returns the record ID as a string for audit logging.
// For UUID-based entities, return id.String().
// For string-based entities, return the ID directly.
func (e MyEntity) AuditID() string {
    return e.ID.String()
}

// AuditTableName returns the table name for audit logging.
func (e MyEntity) AuditTableName() string {
    return "my_entity"
}
```

### 2. Add GORM hooks that call the helpers

```go
import (
    "github.com/meridianhub/meridian/shared/platform/audit"
    "gorm.io/gorm"
)

// AfterCreate records an INSERT audit entry
func (e *MyEntity) AfterCreate(tx *gorm.DB) error {
    return audit.RecordCreate(tx, *e)
}

// BeforeUpdate captures old values for audit
func (e *MyEntity) BeforeUpdate(tx *gorm.DB) error {
    // If your entity embeds BaseModel, call its BeforeUpdate first:
    // if err := e.BaseModel.BeforeUpdate(tx); err != nil {
    //     return err
    // }
    return audit.CaptureOldValue(tx, *e)
}

// AfterUpdate records an UPDATE audit entry with old and new values
func (e *MyEntity) AfterUpdate(tx *gorm.DB) error {
    return audit.RecordUpdate(tx, *e)
}

// AfterDelete records a DELETE audit entry
func (e *MyEntity) AfterDelete(tx *gorm.DB) error {
    return audit.RecordDelete(tx, *e)
}
```

## API Reference

### Types

#### `Auditable` interface

```go
type Auditable interface {
    // AuditID returns the record ID as a string for audit logging.
    AuditID() string

    // AuditTableName returns the table name for audit logging.
    AuditTableName() string
}
```

#### `AuditOutbox` struct

Represents an audit record waiting to be processed by the background worker. Records are written
to the outbox within the same transaction as the business operation, ensuring atomicity.

#### `AuditLog` struct

Represents a permanent audit log entry. Records are moved from the audit_outbox to audit_log
by the background worker.

### Functions

#### `RecordCreate[T Auditable](tx *gorm.DB, entity T) error`

Writes an audit outbox entry for an INSERT operation. Call from your `AfterCreate` hook.

#### `CaptureOldValue[T Auditable](tx *gorm.DB, entity T) error`

Fetches and stores the old entity values before an update. Call from your `BeforeUpdate` hook. The function:

- Queries the database for the current record state
- Stores it in the transaction context for later retrieval
- Skips silently if the entity ID is empty (handles map-based updates)

#### `RecordUpdate[T Auditable](tx *gorm.DB, entity T) error`

Writes an audit outbox entry for an UPDATE operation with both old and new values.
Call from your `AfterUpdate` hook. The function:

- Retrieves the old values captured by `CaptureOldValue`
- Skips silently if old values are not available

#### `RecordDelete[T Auditable](tx *gorm.DB, entity T) error`

Writes an audit outbox entry for a DELETE operation. Call from your `AfterDelete` hook.

## Behavior Notes

### Empty IDs

The helpers skip audit recording when the entity ID is empty or nil. This handles:

- Map-based updates (`db.Model(&Entity{}).Updates(map...)`) which don't populate the model
- Defensive programming for edge cases

### Context Keys

Each entity type gets its own context key for storing old values, based on the table name.
This allows multiple entity types to be updated in the same transaction without key collisions.

### Error Handling

- `ErrNilTransaction`: Returned when `tx` is nil
- `ErrOldValueType`: Returned when the stored old value has an unexpected type
- `ErrOldValueNotFound`: Returned when old values are expected but not found

## Migration from Manual Hooks

Before (repeated in each entity):

```go
// 100+ lines of recordAudit function
// 50+ lines of context handling
// Error variables
// Context key definitions
```

After (per entity):

```go
func (e MyEntity) AuditID() string       { return e.ID.String() }
func (e MyEntity) AuditTableName() string { return "my_entity" }

func (e *MyEntity) AfterCreate(tx *gorm.DB) error  { return audit.RecordCreate(tx, *e) }
func (e *MyEntity) BeforeUpdate(tx *gorm.DB) error { return audit.CaptureOldValue(tx, *e) }
func (e *MyEntity) AfterUpdate(tx *gorm.DB) error  { return audit.RecordUpdate(tx, *e) }
func (e *MyEntity) AfterDelete(tx *gorm.DB) error  { return audit.RecordDelete(tx, *e) }
```

## Examples

See the following files for real-world usage:

- `shared/domain/models/customer.go` - UUID-based entity
- `shared/domain/models/account.go` - UUID-based entity
- `services/party/adapters/persistence/party_entity.go` - Service-level entity
- `services/tenant/adapters/persistence/entity.go` - String ID entity
