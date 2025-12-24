// Package persistence provides PostgreSQL persistence for tenants.
package persistence

import (
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

// AfterCreate is a GORM hook that runs after INSERT operations on TenantEntity.
// It writes an audit outbox entry with the new tenant data.
func (t *TenantEntity) AfterCreate(tx *gorm.DB) error {
	return audit.RecordCreate(tx, *t)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on TenantEntity.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
//
// Note: This hook only runs for model-based updates (Save) where t.ID is populated.
// Map-based updates (Updates(map)) via Repository methods bypass this hook.
func (t *TenantEntity) BeforeUpdate(tx *gorm.DB) error {
	return audit.CaptureOldValue(tx, *t)
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on TenantEntity.
// It retrieves the old values from context and writes an audit outbox entry.
//
// Note: This hook only runs for model-based updates (Save) where BeforeUpdate captured old values.
// Map-based updates (Updates(map)) via Repository methods bypass audit recording.
func (t *TenantEntity) AfterUpdate(tx *gorm.DB) error {
	return audit.RecordUpdate(tx, *t)
}

// AfterDelete is a GORM hook that runs after DELETE operations on TenantEntity.
// It writes an audit outbox entry with the deleted tenant data.
func (t *TenantEntity) AfterDelete(tx *gorm.DB) error {
	return audit.RecordDelete(tx, *t)
}

// systemUser is an alias for audit.DefaultAuditUser for backward compatibility with tests.
const systemUser = audit.DefaultAuditUser

// AuditOutbox is an alias for the shared audit.AuditOutbox type.
// Kept for backward compatibility with existing tests.
type AuditOutbox = audit.AuditOutbox
