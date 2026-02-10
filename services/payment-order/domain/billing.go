// Package domain contains billing cycle domain entities for orchestrating
// periodic billing runs and invoice generation.
package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Billing domain errors.
var (
	ErrInvalidBillingRunTransition = errors.New("invalid billing run status transition")
	ErrBillingRunTerminal          = errors.New("billing run is in terminal state")
	ErrMissingTenantID             = errors.New("tenant ID is required")
	ErrInvalidBillingPeriod        = errors.New("cycle end must be after cycle start")
	ErrMissingBillingRunID         = errors.New("billing run ID is required")
	ErrMissingPartyID              = errors.New("party ID is required")
	ErrMissingAccountID            = errors.New("account ID is required for invoice")
	ErrMissingInvoiceNumber        = errors.New("invoice number is required")
	ErrEmptyLineItems              = errors.New("invoice must have at least one line item")
	ErrInvalidInvoiceTransition    = errors.New("invalid invoice status transition")
	ErrInvoiceTerminal             = errors.New("invoice is in terminal state")
)

// BillingRunStatus represents the lifecycle state of a billing run.
type BillingRunStatus string

// BillingRunStatus constants for the billing run state machine.
const (
	BillingRunStatusInitiated  BillingRunStatus = "INITIATED"
	BillingRunStatusProcessing BillingRunStatus = "PROCESSING"
	BillingRunStatusCompleted  BillingRunStatus = "COMPLETED"
	BillingRunStatusFailed     BillingRunStatus = "FAILED"
)

// BillingRun represents a single billing cycle execution for a tenant.
type BillingRun struct {
	ID            uuid.UUID
	TenantID      string
	CycleStart    time.Time
	CycleEnd      time.Time
	Status        BillingRunStatus
	DunningLevel  int
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewBillingRun creates a new billing run in INITIATED status.
func NewBillingRun(tenantID string, cycleStart, cycleEnd time.Time) (*BillingRun, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if !cycleEnd.After(cycleStart) {
		return nil, ErrInvalidBillingPeriod
	}

	now := time.Now()
	return &BillingRun{
		ID:         uuid.New(),
		TenantID:   tenantID,
		CycleStart: cycleStart,
		CycleEnd:   cycleEnd,
		Status:     BillingRunStatusInitiated,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// StartProcessing transitions the billing run from INITIATED to PROCESSING.
func (b *BillingRun) StartProcessing() error {
	if b.Status != BillingRunStatusInitiated {
		return ErrInvalidBillingRunTransition
	}
	b.Status = BillingRunStatusProcessing
	b.UpdatedAt = time.Now()
	return nil
}

// Complete transitions the billing run from PROCESSING to COMPLETED.
func (b *BillingRun) Complete() error {
	if b.Status != BillingRunStatusProcessing {
		return ErrInvalidBillingRunTransition
	}
	b.Status = BillingRunStatusCompleted
	b.UpdatedAt = time.Now()
	return nil
}

// Fail transitions the billing run to FAILED from any non-terminal state.
func (b *BillingRun) Fail(reason string) error {
	if b.Status == BillingRunStatusFailed {
		return nil // idempotent
	}
	if b.IsTerminal() {
		return ErrBillingRunTerminal
	}
	b.Status = BillingRunStatusFailed
	b.FailureReason = reason
	b.UpdatedAt = time.Now()
	return nil
}

// IsTerminal returns true if the billing run is in a terminal state.
func (b *BillingRun) IsTerminal() bool {
	return b.Status == BillingRunStatusCompleted || b.Status == BillingRunStatusFailed
}

// BillingRunIdempotencyKey generates a deterministic idempotency key for a billing run.
// Format: billing_run_{tenant_id}_{period_start_rfc3339}_{period_end_rfc3339}
func BillingRunIdempotencyKey(tenantID string, cycleStart, cycleEnd time.Time) string {
	return fmt.Sprintf("billing_run_%s_%s_%s",
		tenantID,
		cycleStart.UTC().Format(time.RFC3339),
		cycleEnd.UTC().Format(time.RFC3339),
	)
}

// InvoiceStatus represents the lifecycle state of an invoice.
type InvoiceStatus string

// InvoiceStatus constants for the invoice state machine.
const (
	InvoiceStatusDraft   InvoiceStatus = "DRAFT"
	InvoiceStatusIssued  InvoiceStatus = "ISSUED"
	InvoiceStatusPaid    InvoiceStatus = "PAID"
	InvoiceStatusVoid    InvoiceStatus = "VOID"
	InvoiceStatusOverdue InvoiceStatus = "OVERDUE"
)

// Invoice represents a billing invoice generated from a billing run.
type Invoice struct {
	ID             uuid.UUID
	BillingRunID   uuid.UUID
	PartyID        string
	AccountID      string
	InvoiceNumber  string
	PeriodStart    time.Time
	PeriodEnd      time.Time
	LineItems      []InvoiceLineItem
	SubtotalCents  int64
	Currency       string
	Status         InvoiceStatus
	PaymentOrderID *uuid.UUID
	CreatedAt      time.Time
}

// InvoiceLineItem represents a single line item on an invoice.
type InvoiceLineItem struct {
	Description       string
	Quantity          decimal.Decimal
	UnitPriceCents    int64
	TotalCents        int64
	ValuationAnalysis map[string]any
}

// NewInvoice creates a new invoice in DRAFT status.
func NewInvoice(
	billingRunID uuid.UUID,
	partyID string,
	accountID string,
	invoiceNumber string,
	periodStart, periodEnd time.Time,
	lineItems []InvoiceLineItem,
	currency string,
) (*Invoice, error) {
	if billingRunID == uuid.Nil {
		return nil, ErrMissingBillingRunID
	}
	if partyID == "" {
		return nil, ErrMissingPartyID
	}
	if accountID == "" {
		return nil, ErrMissingAccountID
	}
	if invoiceNumber == "" {
		return nil, ErrMissingInvoiceNumber
	}
	if len(lineItems) == 0 {
		return nil, ErrEmptyLineItems
	}

	var subtotal int64
	for _, item := range lineItems {
		subtotal += item.TotalCents
	}

	return &Invoice{
		ID:            uuid.New(),
		BillingRunID:  billingRunID,
		PartyID:       partyID,
		AccountID:     accountID,
		InvoiceNumber: invoiceNumber,
		PeriodStart:   periodStart,
		PeriodEnd:     periodEnd,
		LineItems:     lineItems,
		SubtotalCents: subtotal,
		Currency:      currency,
		Status:        InvoiceStatusDraft,
		CreatedAt:     time.Now(),
	}, nil
}

// Issue transitions the invoice from DRAFT to ISSUED.
func (inv *Invoice) Issue() error {
	if inv.Status != InvoiceStatusDraft {
		return ErrInvalidInvoiceTransition
	}
	inv.Status = InvoiceStatusIssued
	return nil
}

// MarkPaid transitions the invoice from ISSUED or OVERDUE to PAID.
func (inv *Invoice) MarkPaid(paymentOrderID uuid.UUID) error {
	if inv.Status != InvoiceStatusIssued && inv.Status != InvoiceStatusOverdue {
		return ErrInvalidInvoiceTransition
	}
	inv.Status = InvoiceStatusPaid
	inv.PaymentOrderID = &paymentOrderID
	return nil
}

// MarkOverdue transitions the invoice from ISSUED to OVERDUE.
func (inv *Invoice) MarkOverdue() error {
	if inv.Status != InvoiceStatusIssued {
		return ErrInvalidInvoiceTransition
	}
	inv.Status = InvoiceStatusOverdue
	return nil
}

// Void transitions the invoice to VOID from DRAFT or ISSUED.
func (inv *Invoice) Void() error {
	if inv.Status != InvoiceStatusDraft && inv.Status != InvoiceStatusIssued {
		return ErrInvalidInvoiceTransition
	}
	inv.Status = InvoiceStatusVoid
	return nil
}

// IsTerminal returns true if the invoice is in a terminal state.
func (inv *Invoice) IsTerminal() bool {
	return inv.Status == InvoiceStatusPaid || inv.Status == InvoiceStatusVoid
}
