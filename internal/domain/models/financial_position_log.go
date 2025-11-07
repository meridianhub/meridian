package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// FinancialPositionLog represents the aggregate root for position keeping.
// Maps to position_keeping.financial_position_logs table.
type FinancialPositionLog struct {
	BaseModel

	// Aggregate Root Fields
	LogID     uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_position_keeping_financial_position_logs_log_id" json:"log_id"`
	AccountID string    `gorm:"type:varchar(34);not null;index:idx_position_keeping_financial_position_logs_account_id" json:"account_id"` // IBAN format, FK to accounts.account_number
	Account   *Account  `gorm:"foreignKey:AccountID;references:AccountNumber;constraint:OnDelete:RESTRICT" json:"account,omitempty"`
	Version   int64     `gorm:"not null;default:1" json:"version"` // Optimistic locking

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
func (FinancialPositionLog) TableName() string {
	return "position_keeping.financial_position_logs"
}

// TransactionLogEntry represents a single transaction entry in the position log.
// Maps to position_keeping.transaction_log_entries table.
type TransactionLogEntry struct {
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`

	// Audit Fields (minimal - only created_at)
	CreatedAt time.Time `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`

	// Domain Fields
	EntryID                uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_position_keeping_transaction_log_entries_entry_id" json:"entry_id"`
	FinancialPositionLogID uuid.UUID `gorm:"type:uuid;not null;index:idx_position_keeping_transaction_log_entries_log_id" json:"financial_position_log_id"`
	TransactionID          uuid.UUID `gorm:"type:uuid;not null;index:idx_position_keeping_transaction_log_entries_transaction_id" json:"transaction_id"`
	AccountID              string    `gorm:"type:varchar(100);not null" json:"account_id"`
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
func (TransactionLogEntry) TableName() string {
	return "position_keeping.transaction_log_entries"
}

// TransactionLineage tracks parent-child relationships between transactions.
// Maps to position_keeping.transaction_lineages table.
type TransactionLineage struct {
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`

	// Audit Fields (minimal - only created_at)
	CreatedAt time.Time `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`

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
func (TransactionLineage) TableName() string {
	return "position_keeping.transaction_lineages"
}

// AuditTrailEntry captures audit information for compliance.
// Maps to position_keeping.audit_trail_entries table.
type AuditTrailEntry struct {
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`

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
func (AuditTrailEntry) TableName() string {
	return "position_keeping.audit_trail_entries"
}
