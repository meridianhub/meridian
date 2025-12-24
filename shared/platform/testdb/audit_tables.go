// Package testdb provides utilities for setting up test databases.
package testdb

import (
	"testing"

	"gorm.io/gorm"
)

// CreateAuditTables creates the audit_outbox and audit_log tables required for audit logging hooks.
// These tables are normally created by Atlas migrations but need to be set up manually in tests
// that use GORM AutoMigrate for the main entities.
//
// The audit_outbox table stores pending audit events (INSERT, UPDATE, DELETE operations)
// with the transactional outbox pattern for reliable event publishing.
//
// The audit_log table stores the final audit trail after events are processed.
//
// Note: Uses TEXT for old_values/new_values columns to allow empty strings,
// as JSONB would reject them.
func CreateAuditTables(t *testing.T, db *gorm.DB) {
	t.Helper()

	// Create audit_outbox table (required for audit logging hooks)
	err := db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id VARCHAR(50) NOT NULL,
			old_values TEXT,
			new_values TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT
		)
	`).Error
	if err != nil {
		t.Fatalf("Failed to create audit_outbox table: %v", err)
	}

	// Create audit_log table (required for audit processing)
	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id VARCHAR(50) NOT NULL,
			changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			changed_by VARCHAR(100),
			old_values TEXT,
			new_values TEXT,
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT
		)
	`).Error
	if err != nil {
		t.Fatalf("Failed to create audit_log table: %v", err)
	}
}
