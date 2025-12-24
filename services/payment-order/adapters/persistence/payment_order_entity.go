// Package persistence provides database access for the payment-order domain.
package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
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

// AuditID returns the record ID as a string for audit logging.
// Implements the audit.Auditable interface.
func (p PaymentOrderEntity) AuditID() string {
	return p.ID.String()
}

// AuditTableName returns the table name for audit logging.
// Implements the audit.Auditable interface.
func (p PaymentOrderEntity) AuditTableName() string {
	return p.TableName()
}

// AfterCreate is a GORM hook that runs after INSERT operations on PaymentOrderEntity.
// It writes an audit outbox entry with the new payment order data.
func (p *PaymentOrderEntity) AfterCreate(tx *gorm.DB) error {
	return audit.RecordCreate(tx, *p)
}

// Note: BeforeUpdate and AfterUpdate hooks are NOT used for PaymentOrderEntity because
// the repository uses GORM's Model().Updates(map) pattern for optimistic locking,
// which doesn't trigger struct-based hooks. Update auditing is handled explicitly
// in PaymentOrderRepository.Update() to ensure atomicity and capture old state.

// AfterDelete is a GORM hook that runs after DELETE operations on PaymentOrderEntity.
// It writes an audit outbox entry with the deleted payment order data.
func (p *PaymentOrderEntity) AfterDelete(tx *gorm.DB) error {
	return audit.RecordDelete(tx, *p)
}
