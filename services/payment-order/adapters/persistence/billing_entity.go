package persistence

import (
	"time"

	"github.com/google/uuid"
)

// BillingRunEntity represents the database persistence model for billing runs.
type BillingRunEntity struct {
	ID            uuid.UUID  `gorm:"primaryKey"`
	TenantID      string     `gorm:"not null;size:255"`
	CycleStart    time.Time  `gorm:"not null"`
	CycleEnd      time.Time  `gorm:"not null"`
	Status        string     `gorm:"not null;size:20"`
	DunningLevel  int        `gorm:"not null;default:0"`
	FailureReason *string    `gorm:"size:1000"`
	LastRetryAt   *time.Time `gorm:""`
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
}

// TableName overrides the default table name.
func (BillingRunEntity) TableName() string {
	return "billing_run"
}

// InvoiceEntity represents the database persistence model for invoices.
type InvoiceEntity struct {
	ID             uuid.UUID  `gorm:"primaryKey"`
	BillingRunID   uuid.UUID  `gorm:"not null"`
	PartyID        string     `gorm:"not null;size:255"`
	AccountID      string     `gorm:"not null;size:255"`
	InvoiceNumber  string     `gorm:"not null;size:50;uniqueIndex:idx_invoice_number"`
	PeriodStart    time.Time  `gorm:"not null"`
	PeriodEnd      time.Time  `gorm:"not null"`
	LineItems      string     `gorm:"type:jsonb;not null;default:'[]'"` // JSON serialized
	SubtotalCents  int64      `gorm:"not null"`
	Currency       string     `gorm:"not null;size:3"`
	Status         string     `gorm:"not null;size:20"`
	PaymentOrderID *uuid.UUID `gorm:""`
	CreatedAt      time.Time  `gorm:"not null"`
}

// TableName overrides the default table name.
func (InvoiceEntity) TableName() string {
	return "invoice"
}
