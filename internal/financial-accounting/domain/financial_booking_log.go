package domain

import (
	"time"

	"github.com/google/uuid"
)

// FinancialBookingLog represents the BIAN Financial Booking Log aggregate root.
// It maintains records of financial transactions and their processing status.
//
// The booking log serves as the parent entity for ledger postings in double-entry
// bookkeeping, ensuring all related postings are tracked together.
//
// Lifecycle:
//   - Created in PENDING status via InitiateFinancialBookingLog
//   - Updated with chart of accounts rules and status transitions
//   - Transitions to POSTED when all postings balance (debits == credits)
//   - Cannot be modified once in terminal state (POSTED, FAILED, CANCELLED)
type FinancialBookingLog struct {
	// ID uniquely identifies this booking log
	ID uuid.UUID

	// FinancialAccountType defines the type of account (ASSET, LIABILITY, etc.)
	FinancialAccountType string

	// ProductServiceReference identifies the financial product or service
	ProductServiceReference string

	// BusinessUnitReference identifies the responsible business unit
	BusinessUnitReference string

	// ChartOfAccountsRules defines the accounting rules to apply
	ChartOfAccountsRules string

	// BaseCurrency is the currency for this booking log
	BaseCurrency Currency

	// Status represents the current lifecycle state
	Status TransactionStatus

	// CreatedAt is when this booking log was created
	CreatedAt time.Time

	// UpdatedAt is when this booking log was last modified
	UpdatedAt time.Time

	// Postings contains all ledger postings for this log
	// NOTE: Loaded separately via repository to avoid N+1 queries
	Postings []*LedgerPosting
}

// NewFinancialBookingLog creates a new booking log in PENDING status.
//
// Parameters are validated at the service layer per BIAN standards.
// The booking log is created without postings; postings are added via
// CaptureLedgerPosting operations.
func NewFinancialBookingLog(
	accountType string,
	productServiceRef string,
	businessUnitRef string,
	chartOfAccountsRules string,
	baseCurrency Currency,
) *FinancialBookingLog {
	now := time.Now().UTC()

	return &FinancialBookingLog{
		ID:                      uuid.New(),
		FinancialAccountType:    accountType,
		ProductServiceReference: productServiceRef,
		BusinessUnitReference:   businessUnitRef,
		ChartOfAccountsRules:    chartOfAccountsRules,
		BaseCurrency:            baseCurrency,
		Status:                  TransactionStatusPending,
		CreatedAt:               now,
		UpdatedAt:               now,
		Postings:                make([]*LedgerPosting, 0),
	}
}

// UpdateStatus transitions the booking log to a new status.
//
// Status transitions are validated at the service layer:
//   - PENDING → POSTED (when debits == credits)
//   - PENDING → FAILED (validation errors)
//   - PENDING → CANCELLED (business cancellation)
//   - Terminal states (POSTED, FAILED, CANCELLED) cannot transition
func (l *FinancialBookingLog) UpdateStatus(newStatus TransactionStatus) {
	l.Status = newStatus
	l.UpdatedAt = time.Now().UTC()
}

// UpdateChartOfAccountsRules updates the accounting rules.
//
// Can only be updated while in PENDING status.
// Service layer enforces this constraint.
func (l *FinancialBookingLog) UpdateChartOfAccountsRules(rules string) {
	l.ChartOfAccountsRules = rules
	l.UpdatedAt = time.Now().UTC()
}

// AddPosting adds a ledger posting to this booking log.
//
// This is typically called when loading from the repository.
// New postings are created via CaptureLedgerPosting service method.
func (l *FinancialBookingLog) AddPosting(posting *LedgerPosting) {
	l.Postings = append(l.Postings, posting)
}

// IsTerminal returns true if the status is terminal (cannot transition).
func (l *FinancialBookingLog) IsTerminal() bool {
	return l.Status == TransactionStatusPosted ||
		l.Status == TransactionStatusFailed ||
		l.Status == TransactionStatusCancelled
}
