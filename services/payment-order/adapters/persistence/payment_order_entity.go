// Package persistence provides database access for the payment-order domain.
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"gorm.io/gorm"
)

// PaymentOrderEntity represents the database persistence model for payment orders.
// Optimized for database concerns: audit fields, indexes, constraints.
type PaymentOrderEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"primaryKey"`

	// Foreign key to account (debtor)
	DebtorAccountID string `gorm:"not null;index:idx_payment_order_debtor_account"`

	// Creditor reference (external identifier)
	CreditorReference string `gorm:"not null;size:255"`

	// Monetary amount
	AmountCents int64  `gorm:"not null;check:amount_cents > 0"`
	Currency    string `gorm:"not null;size:3"`

	// Lifecycle state
	Status string `gorm:"not null;size:20;index:idx_payment_order_status;check:status IN ('INITIATED','RESERVED','EXECUTING','COMPLETED','FAILED','CANCELLED','REVERSED')"`

	// Reference to the lien created for this payment order (set after RESERVED)
	LienID string `gorm:"size:255"`

	// External gateway reference (set after EXECUTING)
	GatewayReferenceID string `gorm:"size:255;index:idx_payment_order_gateway_ref"`

	// Ledger booking reference (set after COMPLETED)
	LedgerBookingID string `gorm:"size:255"`

	// Tracing identifiers
	CorrelationID string `gorm:"not null;size:255"`
	CausationID   string `gorm:"size:255"`

	// Idempotency key for preventing duplicate payment orders
	IdempotencyKey string `gorm:"not null;size:255;uniqueIndex:idx_payment_order_idempotency_key"`

	// Failure/cancellation/reversal reason
	FailureReason string `gorm:"size:1000"`
	ErrorCode     string `gorm:"size:50"`

	// Lien execution tracking for async retry mechanism
	// Check constraint: NULL or IN ('PENDING', 'SUCCEEDED', 'FAILED')
	LienExecutionStatus   *string `gorm:"column:lien_execution_status;size:20"`
	LienExecutionAttempts int     `gorm:"column:lien_execution_attempts;not null;default:0"`
	LienExecutionError    *string `gorm:"column:lien_execution_error;size:1000"`

	// Audit fields
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
	Version     int       `gorm:"not null;default:1"`
	ReservedAt  *time.Time
	ExecutingAt *time.Time
	CompletedAt *time.Time
	FailedAt    *time.Time
	CancelledAt *time.Time
	ReversedAt  *time.Time
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (PaymentOrderEntity) TableName() string {
	return "payment_order"
}

// =============================================================================
// Audit Infrastructure
// =============================================================================

// ErrNilTransaction is returned when a nil transaction is passed to recordAudit
var ErrNilTransaction = errors.New("tx cannot be nil for audit recording")

// systemUser is the default user ID for background jobs and migrations
const systemUser = "system"

// AuditOutbox represents an audit record waiting to be processed by the background worker.
// Records are written to the outbox within the same transaction as the business operation,
// ensuring atomicity and preventing lost audit records.
//
// The background worker asynchronously moves records from outbox to audit_log.
type AuditOutbox struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Table         string    `gorm:"column:table_name;type:varchar(100);not null;index" json:"table_name"`
	Operation     string    `gorm:"type:varchar(10);not null;index" json:"operation"` // INSERT, UPDATE, DELETE
	RecordID      uuid.UUID `gorm:"type:uuid;not null;index" json:"record_id"`
	OldValues     *string   `gorm:"type:jsonb" json:"old_values,omitempty"`                          // JSONB representation of old values
	NewValues     *string   `gorm:"type:jsonb" json:"new_values,omitempty"`                          // JSONB representation of new values
	Status        string    `gorm:"type:varchar(20);not null;default:'pending';index" json:"status"` // pending, processing, completed, failed
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	RetryCount    int       `gorm:"not null;default:0" json:"retry_count"`
	LastError     *string   `gorm:"type:text" json:"last_error,omitempty"`
	ChangedBy     *string   `gorm:"type:varchar(100)" json:"changed_by,omitempty"`
	TransactionID *string   `gorm:"type:varchar(100)" json:"transaction_id,omitempty"`
	ClientIP      *string   `gorm:"type:varchar(45)" json:"client_ip,omitempty"` // Pointer for NULL support
	UserAgent     *string   `gorm:"type:text" json:"user_agent,omitempty"`
}

// TableName overrides the table name for AuditOutbox.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (AuditOutbox) TableName() string {
	return "audit_outbox"
}

// recordAudit writes an audit outbox entry within the current transaction.
// This function is called by GORM hooks (AfterCreate, AfterUpdate, AfterDelete).
//
// Parameters:
//   - tx: The GORM transaction (must be non-nil)
//   - tableName: The table being audited (e.g., "payment_order")
//   - operation: The operation type ("INSERT", "UPDATE", "DELETE")
//   - recordID: The UUID of the record being audited
//   - oldValue: The old state (nil for INSERT, populated for UPDATE/DELETE)
//   - newValue: The new state (populated for INSERT/UPDATE, nil for DELETE)
//
// Returns:
//   - error: Any error encountered during audit recording
func recordAudit(tx *gorm.DB, tableName, operation string, recordID uuid.UUID, oldValue, newValue interface{}) error {
	if tx == nil {
		return ErrNilTransaction
	}

	// Serialize old and new values to JSON
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

	// Extract user ID from context
	var changedBy *string
	if tx.Statement != nil && tx.Statement.Context != nil {
		if userID := getUserIDFromContext(tx.Statement.Context); userID != "" {
			changedBy = &userID
		}
	}
	if changedBy == nil {
		// Default to system
		sysUser := systemUser
		changedBy = &sysUser
	}

	// Create audit outbox entry
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

	// Write to outbox within the same transaction
	return tx.Create(&outbox).Error
}

// getUserIDFromContext extracts the user ID from the context.
// Returns empty string if not found or if type assertion fails.
func getUserIDFromContext(ctx any) string {
	if ctx == nil {
		return ""
	}

	// Safely convert to context.Context interface
	stdCtx, ok := ctx.(context.Context)
	if !ok {
		return ""
	}

	// Try to get user from auth context (set by gRPC interceptors)
	if claims, exists := auth.GetClaimsFromContext(stdCtx); exists && claims != nil {
		return claims.Subject
	}

	return ""
}

// =============================================================================
// Payment Order Audit Hooks
// =============================================================================

// AfterCreate is a GORM hook that runs after INSERT operations on PaymentOrderEntity.
// It writes an audit outbox entry with the new payment order data.
func (p *PaymentOrderEntity) AfterCreate(tx *gorm.DB) error {
	return recordAudit(tx, "payment_order", "INSERT", p.ID, nil, p)
}

// Note: BeforeUpdate and AfterUpdate hooks are NOT used for PaymentOrderEntity because
// the repository uses GORM's Model().Updates(map) pattern for optimistic locking,
// which doesn't trigger struct-based hooks. Update auditing is handled explicitly
// in PaymentOrderRepository.Update() to ensure atomicity and capture old state.

// AfterDelete is a GORM hook that runs after DELETE operations on PaymentOrderEntity.
// It writes an audit outbox entry with the deleted payment order data.
func (p *PaymentOrderEntity) AfterDelete(tx *gorm.DB) error {
	return recordAudit(tx, "payment_order", "DELETE", p.ID, p, nil)
}
