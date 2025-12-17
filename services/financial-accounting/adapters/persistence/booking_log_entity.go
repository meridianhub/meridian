// Package persistence provides database entities and repository implementations
// for the financial accounting service domain models.
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"gorm.io/gorm"
)

// FinancialBookingLogEntity represents the database persistence model
// Optimized for database concerns: audit fields, indexes, constraints
type FinancialBookingLogEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey"`

	// Business fields
	FinancialAccountType    string `gorm:"not null;size:50;index"`
	ProductServiceReference string `gorm:"not null;size:255;index"`
	BusinessUnitReference   string `gorm:"not null;size:255;index"`
	ChartOfAccountsRules    string `gorm:"not null;type:text"`
	BaseCurrency            string `gorm:"not null;size:3;index"`
	Status                  string `gorm:"not null;size:50;index"`

	// Idempotency
	IdempotencyKey string `gorm:"uniqueIndex;not null;size:255"`

	// Audit fields
	CreatedAt time.Time      `gorm:"not null"`
	UpdatedAt time.Time      `gorm:"not null"`
	CreatedBy string         `gorm:"size:255"`
	UpdatedBy string         `gorm:"size:255"`
	DeletedAt gorm.DeletedAt `gorm:"index"` // Soft delete

	// Version for optimistic locking
	Version int `gorm:"not null"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (FinancialBookingLogEntity) TableName() string {
	return "financial_booking_log"
}

// contextKey is a private type for context keys to avoid collisions
type contextKey string

// bookingLogOldValueKey is the context key for storing old values before UPDATE
const bookingLogOldValueKey contextKey = "audit:booking_log_old_value"

var (
	// ErrNilTransaction is returned when a nil transaction is passed to recordAudit
	ErrNilTransaction = errors.New("tx cannot be nil for audit recording")
	// ErrOldValueType is returned when old value has incorrect type in context
	ErrOldValueType = errors.New("failed to retrieve old values from context: invalid type")
)

// AuditOutbox represents an audit record waiting to be processed by the background worker.
// Uses financial-accounting database's audit_outbox table.
type AuditOutbox struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      uuid.UUID `gorm:"type:uuid;not null;index" json:"record_id"`
	OldValues     *string   `gorm:"type:jsonb" json:"old_values,omitempty"`                          // JSON representation (nullable)
	NewValues     *string   `gorm:"type:jsonb" json:"new_values,omitempty"`                          // JSON representation (nullable)
	Status        string    `gorm:"type:varchar(20);not null;default:'pending';index" json:"status"` // pending, processing, completed, failed
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	RetryCount    int       `gorm:"not null;default:0" json:"retry_count"`
	LastError     *string   `gorm:"type:text" json:"last_error,omitempty"`
	ChangedBy     *string   `gorm:"type:varchar(100)" json:"changed_by,omitempty"`
	TransactionID *string   `gorm:"type:varchar(100)" json:"transaction_id,omitempty"`
	ClientIP      *string   `gorm:"type:varchar(45)" json:"client_ip,omitempty"`
	UserAgent     *string   `gorm:"type:text" json:"user_agent,omitempty"`
}

// TableName returns the table name for AuditOutbox.
func (AuditOutbox) TableName() string {
	return "audit_outbox"
}

// AuditLog represents a permanent audit log entry.
type AuditLog struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      uuid.UUID `gorm:"type:uuid;not null;index" json:"record_id"`
	OldValues     *string   `gorm:"type:jsonb" json:"old_values,omitempty"` // JSON representation (nullable)
	NewValues     *string   `gorm:"type:jsonb" json:"new_values,omitempty"` // JSON representation (nullable)
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	ChangedBy     *string   `gorm:"type:varchar(100)" json:"changed_by,omitempty"`
	TransactionID *string   `gorm:"type:varchar(100)" json:"transaction_id,omitempty"`
	ClientIP      *string   `gorm:"type:varchar(45)" json:"client_ip,omitempty"`
	UserAgent     *string   `gorm:"type:text" json:"user_agent,omitempty"`
}

// TableName returns the table name for AuditLog.
func (AuditLog) TableName() string {
	return "audit_log"
}

// recordAudit writes an audit outbox entry within the current transaction.
func recordAudit(tx *gorm.DB, tableName, operation string, recordID uuid.UUID, oldValue, newValue interface{}) error {
	if tx == nil {
		return ErrNilTransaction
	}

	var oldJSON, newJSON *string

	if oldValue != nil {
		oldBytes, err := json.Marshal(oldValue)
		if err != nil {
			return fmt.Errorf("failed to marshal old value: %w", err)
		}
		s := string(oldBytes)
		oldJSON = &s
	}

	if newValue != nil {
		newBytes, err := json.Marshal(newValue)
		if err != nil {
			return fmt.Errorf("failed to marshal new value: %w", err)
		}
		s := string(newBytes)
		newJSON = &s
	}

	var changedBy *string
	if tx.Statement != nil && tx.Statement.Context != nil {
		if userID := getUserIDFromContext(tx.Statement.Context); userID != "" {
			changedBy = &userID
		}
	}
	if changedBy == nil {
		systemUser := "system"
		changedBy = &systemUser
	}

	outbox := AuditOutbox{
		ID:        uuid.New(),
		Table:     tableName,
		Operation: operation,
		RecordID:  recordID,
		OldValues: oldJSON,
		NewValues: newJSON,
		Status:    "pending",
		ChangedBy: changedBy,
		CreatedAt: time.Now(),
	}

	return tx.Create(&outbox).Error
}

// getUserIDFromContext extracts the user ID from the context.
func getUserIDFromContext(ctx any) string {
	if ctx == nil {
		return ""
	}

	stdCtx, ok := ctx.(context.Context)
	if !ok {
		return ""
	}

	if userID, ok := stdCtx.Value(auth.UserIDContextKey).(string); ok {
		return userID
	}

	return ""
}

// AfterCreate is a GORM hook that runs after INSERT operations.
func (e *FinancialBookingLogEntity) AfterCreate(tx *gorm.DB) error {
	// Skip audit if ID is not set (defensive check for edge cases)
	if e.ID == uuid.Nil {
		return nil
	}
	return recordAudit(tx, "financial_booking_log", "INSERT", e.ID, nil, e)
}

// BeforeUpdate is a GORM hook that captures old values before UPDATE.
func (e *FinancialBookingLogEntity) BeforeUpdate(tx *gorm.DB) error {
	// Skip audit capture if ID is not set (happens when using db.Model().Update())
	if e.ID == uuid.Nil {
		return nil
	}

	var old FinancialBookingLogEntity
	if err := tx.First(&old, e.ID).Error; err != nil {
		return fmt.Errorf("failed to fetch old booking log values: %w", err)
	}

	if tx.Statement != nil && tx.Statement.Context != nil {
		ctx := context.WithValue(tx.Statement.Context, bookingLogOldValueKey, &old)
		tx.Statement.Context = ctx
	}

	return nil
}

// AfterUpdate is a GORM hook that runs after UPDATE operations.
func (e *FinancialBookingLogEntity) AfterUpdate(tx *gorm.DB) error {
	// Skip audit if ID is not set (defensive check)
	if e.ID == uuid.Nil {
		return nil
	}

	var old *FinancialBookingLogEntity
	if tx.Statement != nil && tx.Statement.Context != nil {
		if oldValue := tx.Statement.Context.Value(bookingLogOldValueKey); oldValue != nil {
			var ok bool
			old, ok = oldValue.(*FinancialBookingLogEntity)
			if !ok {
				return ErrOldValueType
			}
		}
	}

	// Skip if old values weren't captured (happens with partial updates via db.Model().Update())
	if old == nil {
		slog.Warn("audit skipped: old values not captured for booking log update",
			"table", "financial_booking_log",
			"record_id", e.ID,
			"reason", "partial update via db.Model().Update() bypasses BeforeUpdate hook")
		return nil
	}

	return recordAudit(tx, "financial_booking_log", "UPDATE", e.ID, old, e)
}

// AfterDelete is a GORM hook that runs after DELETE operations.
func (e *FinancialBookingLogEntity) AfterDelete(tx *gorm.DB) error {
	// Skip audit if ID is not set (defensive check)
	if e.ID == uuid.Nil {
		return nil
	}
	return recordAudit(tx, "financial_booking_log", "DELETE", e.ID, e, nil)
}
