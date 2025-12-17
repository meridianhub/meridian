package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// FinancialPositionLog represents the aggregate root for position keeping.
// Maps to financial_position_log table (singular, unqualified for search_path routing).
type FinancialPositionLog struct {
	BaseModel

	// Aggregate Root Fields
	LogID     uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_position_keeping_financial_position_logs_log_id" json:"log_id"`
	AccountID string    `gorm:"type:varchar(34);not null;index:idx_position_keeping_financial_position_logs_account_id" json:"account_id"` // IBAN format, FK to accounts.account_number
	Account   *Account  `gorm:"foreignKey:AccountID;references:AccountNumber;constraint:OnDelete:RESTRICT" json:"account,omitempty"`
	Version   int64     `gorm:"version;not null;default:1" json:"version"` // Optimistic locking - gorm:version enables auto-increment and concurrent update protection

	// Status Tracking (embedded value object)
	CurrentStatus        string    `gorm:"type:varchar(20);not null;index:idx_position_keeping_financial_position_logs_current_status" json:"current_status"`
	PreviousStatus       *string   `gorm:"type:varchar(20)" json:"previous_status,omitempty"`
	StatusUpdatedAt      time.Time `gorm:"type:timestamptz;not null" json:"status_updated_at"`
	StatusReason         string    `gorm:"type:text;not null" json:"status_reason"`
	FailureReason        *string   `gorm:"type:text" json:"failure_reason,omitempty"`
	ReconciliationStatus string    `gorm:"type:varchar(20);not null" json:"reconciliation_status"`

	// Relationships (loaded via GORM associations)
	TransactionLogEntries []TransactionLogEntry `gorm:"foreignKey:FinancialPositionLogID;constraint:OnDelete:CASCADE" json:"transaction_log_entries,omitempty"`
	TransactionLineage    *TransactionLineage   `gorm:"foreignKey:FinancialPositionLogID;constraint:OnDelete:CASCADE" json:"transaction_lineage,omitempty"`
	AuditTrailEntries     []AuditTrailEntry     `gorm:"foreignKey:FinancialPositionLogID;constraint:OnDelete:CASCADE" json:"audit_trail_entries,omitempty"`
}

// TableName specifies the table name for GORM.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (FinancialPositionLog) TableName() string {
	return "financial_position_log"
}

// TransactionLogEntry represents a single transaction entry in the position log.
// Maps to transaction_log_entry table (singular, unqualified for search_path routing).
type TransactionLogEntry struct {
	BaseModel

	// Domain Fields
	EntryID                uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_position_keeping_transaction_log_entries_entry_id" json:"entry_id"`
	FinancialPositionLogID uuid.UUID `gorm:"type:uuid;not null;index:idx_position_keeping_transaction_log_entries_log_id" json:"financial_position_log_id"`
	TransactionID          uuid.UUID `gorm:"type:uuid;not null;index:idx_position_keeping_transaction_log_entries_transaction_id" json:"transaction_id"`
	AccountID              string    `gorm:"type:varchar(34);not null" json:"account_id"` // IBAN format, FK to accounts.account_number
	Account                *Account  `gorm:"foreignKey:AccountID;references:AccountNumber;constraint:OnDelete:RESTRICT" json:"account,omitempty"`
	AmountCents            int64     `gorm:"not null" json:"amount_cents"`                        // Store as smallest currency unit
	Currency               string    `gorm:"type:char(3);not null;default:'GBP'" json:"currency"` // ISO 4217
	Direction              string    `gorm:"type:varchar(10);not null" json:"direction"`          // 'debit' or 'credit'
	Timestamp              time.Time `gorm:"type:timestamptz;not null;index:idx_position_keeping_transaction_log_entries_timestamp" json:"timestamp"`
	Description            *string   `gorm:"type:text" json:"description,omitempty"`
	Reference              *string   `gorm:"type:varchar(100)" json:"reference,omitempty"`
	Source                 string    `gorm:"type:varchar(50);not null" json:"source"`

	// Foreign key relationship (not loaded by default)
	FinancialPositionLog *FinancialPositionLog `gorm:"constraint:OnDelete:CASCADE" json:"-"`
}

// TableName specifies the table name for GORM.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (TransactionLogEntry) TableName() string {
	return "transaction_log_entry"
}

// TransactionLineage tracks parent-child relationships between transactions.
// Maps to transaction_lineage table (singular, unqualified for search_path routing).
type TransactionLineage struct {
	BaseModel

	// Domain Fields
	FinancialPositionLogID uuid.UUID      `gorm:"type:uuid;not null;uniqueIndex:idx_position_keeping_transaction_lineages_log_id" json:"financial_position_log_id"` // One-to-one
	TransactionID          uuid.UUID      `gorm:"type:uuid;not null;index:idx_position_keeping_transaction_lineages_transaction_id" json:"transaction_id"`
	ParentTransactionID    *uuid.UUID     `gorm:"type:uuid;index:idx_position_keeping_transaction_lineages_parent_id" json:"parent_transaction_id,omitempty"`
	ChildTransactionIDs    datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"child_transaction_ids"`   // Array of UUIDs as JSONB
	RelatedTransactionIDs  datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"related_transaction_ids"` // Array of UUIDs as JSONB
	TransactionType        string         `gorm:"type:varchar(50);not null" json:"transaction_type"`

	// Foreign key relationship (not loaded by default)
	FinancialPositionLog *FinancialPositionLog `gorm:"constraint:OnDelete:CASCADE" json:"-"`
}

// TableName specifies the table name for GORM.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (TransactionLineage) TableName() string {
	return "transaction_lineage"
}

// AuditTrailEntry captures audit information for compliance.
// Maps to audit_trail_entry table (singular, unqualified for search_path routing).
type AuditTrailEntry struct {
	BaseModel

	// Domain Fields
	AuditID                uuid.UUID      `gorm:"type:uuid;not null;uniqueIndex:idx_position_keeping_audit_trail_entries_audit_id" json:"audit_id"`
	FinancialPositionLogID uuid.UUID      `gorm:"type:uuid;not null;index:idx_position_keeping_audit_trail_entries_log_id" json:"financial_position_log_id"`
	Timestamp              time.Time      `gorm:"type:timestamptz;not null;index:idx_position_keeping_audit_trail_entries_timestamp" json:"timestamp"`
	UserID                 string         `gorm:"type:varchar(100);not null;index:idx_position_keeping_audit_trail_entries_user_id" json:"user_id"`
	Action                 string         `gorm:"type:varchar(100);not null" json:"action"`
	Details                *string        `gorm:"type:text" json:"details,omitempty"`
	IPAddress              *string        `gorm:"type:varchar(45)" json:"ip_address,omitempty"` // IPv4 or IPv6
	SystemContext          datatypes.JSON `gorm:"type:jsonb" json:"system_context,omitempty"`   // Flexible key-value pairs

	// Foreign key relationship (not loaded by default)
	FinancialPositionLog *FinancialPositionLog `gorm:"constraint:OnDelete:CASCADE" json:"-"`
}

// TableName specifies the table name for GORM.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (AuditTrailEntry) TableName() string {
	return "audit_trail_entry"
}

// Context keys for storing old values during UPDATE operations.
// These allow BeforeUpdate hooks to pass old state to AfterUpdate hooks.
const (
	financialPositionLogOldValueKey contextKey = "audit:financial_position_log:old_value"
	transactionLogEntryOldValueKey  contextKey = "audit:transaction_log_entry:old_value"
)

// Audit hook errors for FinancialPositionLog and TransactionLogEntry
var (
	// ErrFinancialPositionLogOldValueType is returned when old value has incorrect type in context
	ErrFinancialPositionLogOldValueType = fmt.Errorf("failed to retrieve old financial_position_log values from context: invalid type")

	// ErrFinancialPositionLogOldValueNotFound is returned when old value is not found in context
	ErrFinancialPositionLogOldValueNotFound = fmt.Errorf("old financial_position_log values not found in context")

	// ErrTransactionLogEntryOldValueType is returned when old value has incorrect type in context
	ErrTransactionLogEntryOldValueType = fmt.Errorf("failed to retrieve old transaction_log_entry values from context: invalid type")

	// ErrTransactionLogEntryOldValueNotFound is returned when old value is not found in context
	ErrTransactionLogEntryOldValueNotFound = fmt.Errorf("old transaction_log_entry values not found in context")
)

// AfterCreate is a GORM hook that runs after INSERT operations on FinancialPositionLog.
// It writes an audit outbox entry with the new financial position log data.
func (f *FinancialPositionLog) AfterCreate(tx *gorm.DB) error {
	return recordAudit(tx, "financial_position_log", "INSERT", f.ID, nil, f)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on FinancialPositionLog.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
func (f *FinancialPositionLog) BeforeUpdate(tx *gorm.DB) error {
	// First, call the base model's BeforeUpdate to handle UpdatedBy
	if err := f.BaseModel.BeforeUpdate(tx); err != nil {
		return err
	}

	// Capture old values before the update
	var old FinancialPositionLog
	if err := tx.First(&old, f.ID).Error; err != nil {
		return fmt.Errorf("failed to fetch old financial_position_log values: %w", err)
	}

	// Store old values in transaction context for AfterUpdate to access
	if tx.Statement != nil && tx.Statement.Context != nil {
		ctx := context.WithValue(tx.Statement.Context, financialPositionLogOldValueKey, &old)
		tx.Statement.Context = ctx
	}

	return nil
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on FinancialPositionLog.
// It retrieves the old values from context and writes an audit outbox entry.
func (f *FinancialPositionLog) AfterUpdate(tx *gorm.DB) error {
	// Retrieve old values from context (captured in BeforeUpdate)
	var old *FinancialPositionLog
	if tx.Statement != nil && tx.Statement.Context != nil {
		if oldValue := tx.Statement.Context.Value(financialPositionLogOldValueKey); oldValue != nil {
			var ok bool
			old, ok = oldValue.(*FinancialPositionLog)
			if !ok {
				return ErrFinancialPositionLogOldValueType
			}
		}
	}

	if old == nil {
		return ErrFinancialPositionLogOldValueNotFound
	}

	return recordAudit(tx, "financial_position_log", "UPDATE", f.ID, old, f)
}

// AfterDelete is a GORM hook that runs after DELETE operations on FinancialPositionLog.
// It writes an audit outbox entry with the deleted financial position log data.
func (f *FinancialPositionLog) AfterDelete(tx *gorm.DB) error {
	return recordAudit(tx, "financial_position_log", "DELETE", f.ID, f, nil)
}

// AfterCreate is a GORM hook that runs after INSERT operations on TransactionLogEntry.
// It writes an audit outbox entry with the new transaction log entry data.
func (t *TransactionLogEntry) AfterCreate(tx *gorm.DB) error {
	return recordAudit(tx, "transaction_log_entry", "INSERT", t.ID, nil, t)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on TransactionLogEntry.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
func (t *TransactionLogEntry) BeforeUpdate(tx *gorm.DB) error {
	// First, call the base model's BeforeUpdate to handle UpdatedBy
	if err := t.BaseModel.BeforeUpdate(tx); err != nil {
		return err
	}

	// Capture old values before the update
	var old TransactionLogEntry
	if err := tx.First(&old, t.ID).Error; err != nil {
		return fmt.Errorf("failed to fetch old transaction_log_entry values: %w", err)
	}

	// Store old values in transaction context for AfterUpdate to access
	if tx.Statement != nil && tx.Statement.Context != nil {
		ctx := context.WithValue(tx.Statement.Context, transactionLogEntryOldValueKey, &old)
		tx.Statement.Context = ctx
	}

	return nil
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on TransactionLogEntry.
// It retrieves the old values from context and writes an audit outbox entry.
func (t *TransactionLogEntry) AfterUpdate(tx *gorm.DB) error {
	// Retrieve old values from context (captured in BeforeUpdate)
	var old *TransactionLogEntry
	if tx.Statement != nil && tx.Statement.Context != nil {
		if oldValue := tx.Statement.Context.Value(transactionLogEntryOldValueKey); oldValue != nil {
			var ok bool
			old, ok = oldValue.(*TransactionLogEntry)
			if !ok {
				return ErrTransactionLogEntryOldValueType
			}
		}
	}

	if old == nil {
		return ErrTransactionLogEntryOldValueNotFound
	}

	return recordAudit(tx, "transaction_log_entry", "UPDATE", t.ID, old, t)
}

// AfterDelete is a GORM hook that runs after DELETE operations on TransactionLogEntry.
// It writes an audit outbox entry with the deleted transaction log entry data.
func (t *TransactionLogEntry) AfterDelete(tx *gorm.DB) error {
	return recordAudit(tx, "transaction_log_entry", "DELETE", t.ID, t, nil)
}
