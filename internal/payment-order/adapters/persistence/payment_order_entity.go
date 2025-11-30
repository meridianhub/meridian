// Package persistence provides database access for the payment-order domain.
package persistence

import (
	"time"

	"github.com/google/uuid"
)

// PaymentOrderEntity represents the database persistence model for payment orders.
// Optimized for database concerns: audit fields, indexes, constraints.
type PaymentOrderEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"primaryKey"`

	// Foreign key to current_accounts (debtor)
	DebtorAccountID string `gorm:"not null;index:idx_payment_orders_debtor_account"`

	// Creditor reference (external identifier)
	CreditorReference string `gorm:"not null;size:255"`

	// Monetary amount
	AmountCents int64  `gorm:"not null;check:amount_cents > 0"`
	Currency    string `gorm:"not null;size:3"`

	// Lifecycle state
	Status string `gorm:"not null;size:20;index:idx_payment_orders_status;check:status IN ('INITIATED','RESERVED','EXECUTING','COMPLETED','FAILED','CANCELLED','REVERSED')"`

	// Reference to the lien created for this payment order (set after RESERVED)
	LienID string `gorm:"size:255"`

	// External gateway reference (set after EXECUTING)
	GatewayReferenceID string `gorm:"size:255;index:idx_payment_orders_gateway_ref"`

	// Ledger booking reference (set after COMPLETED)
	LedgerBookingID string `gorm:"size:255"`

	// Tracing identifiers
	CorrelationID string `gorm:"not null;size:255"`
	CausationID   string `gorm:"size:255"`

	// Idempotency key for preventing duplicate payment orders
	IdempotencyKey string `gorm:"not null;size:255;uniqueIndex:idx_payment_orders_idempotency_key"`

	// Failure/cancellation/reversal reason
	FailureReason string `gorm:"size:1000"`
	ErrorCode     string `gorm:"size:50"`

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

// TableName overrides the default table name
func (PaymentOrderEntity) TableName() string {
	return "payment_orders"
}
