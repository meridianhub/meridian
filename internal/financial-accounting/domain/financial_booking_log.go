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

	// postings contains all ledger postings for this log (unexported for immutability)
	// NOTE: Loaded separately via repository to avoid N+1 queries
	// Access via Postings() method which returns a defensive copy
	postings []*LedgerPosting
}

// Postings returns a defensive copy of the postings slice.
// This prevents external mutation per CONTRIBUTING.md immutability guidelines.
func (l FinancialBookingLog) Postings() []*LedgerPosting {
	if l.postings == nil {
		return []*LedgerPosting{}
	}
	result := make([]*LedgerPosting, len(l.postings))
	copy(result, l.postings)
	return result
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
		postings:                make([]*LedgerPosting, 0),
	}
}

// WithStatus returns a new booking log with updated status.
//
// Status transitions are validated at the service layer:
//   - PENDING → POSTED (when debits == credits)
//   - PENDING → FAILED (validation errors)
//   - PENDING → CANCELLED (business cancellation)
//   - Terminal states (POSTED, FAILED, CANCELLED) cannot transition
//
// Returns a new instance following immutability guidelines per CONTRIBUTING.md.
func (l FinancialBookingLog) WithStatus(newStatus TransactionStatus) FinancialBookingLog {
	return FinancialBookingLog{
		ID:                      l.ID,
		FinancialAccountType:    l.FinancialAccountType,
		ProductServiceReference: l.ProductServiceReference,
		BusinessUnitReference:   l.BusinessUnitReference,
		ChartOfAccountsRules:    l.ChartOfAccountsRules,
		BaseCurrency:            l.BaseCurrency,
		Status:                  newStatus,
		CreatedAt:               l.CreatedAt,
		UpdatedAt:               time.Now().UTC(),
		postings:                l.postings,
	}
}

// WithChartOfAccountsRules returns a new booking log with updated accounting rules.
//
// Can only be updated while in PENDING status.
// Service layer enforces this constraint.
//
// Returns a new instance following immutability guidelines per CONTRIBUTING.md.
func (l FinancialBookingLog) WithChartOfAccountsRules(rules string) FinancialBookingLog {
	return FinancialBookingLog{
		ID:                      l.ID,
		FinancialAccountType:    l.FinancialAccountType,
		ProductServiceReference: l.ProductServiceReference,
		BusinessUnitReference:   l.BusinessUnitReference,
		ChartOfAccountsRules:    rules,
		BaseCurrency:            l.BaseCurrency,
		Status:                  l.Status,
		CreatedAt:               l.CreatedAt,
		UpdatedAt:               time.Now().UTC(),
		postings:                l.postings,
	}
}

// WithPosting returns a new booking log with an additional posting.
//
// This is typically called when loading from the repository.
// New postings are created via CaptureLedgerPosting service method.
//
// Returns a new instance following immutability guidelines per CONTRIBUTING.md.
func (l FinancialBookingLog) WithPosting(posting *LedgerPosting) FinancialBookingLog {
	newPostings := make([]*LedgerPosting, len(l.postings)+1)
	copy(newPostings, l.postings)
	newPostings[len(l.postings)] = posting

	return FinancialBookingLog{
		ID:                      l.ID,
		FinancialAccountType:    l.FinancialAccountType,
		ProductServiceReference: l.ProductServiceReference,
		BusinessUnitReference:   l.BusinessUnitReference,
		ChartOfAccountsRules:    l.ChartOfAccountsRules,
		BaseCurrency:            l.BaseCurrency,
		Status:                  l.Status,
		CreatedAt:               l.CreatedAt,
		UpdatedAt:               l.UpdatedAt,
		postings:                newPostings,
	}
}

// IsTerminal returns true if the status is terminal (cannot transition).
func (l *FinancialBookingLog) IsTerminal() bool {
	return l.Status == TransactionStatusPosted ||
		l.Status == TransactionStatusFailed ||
		l.Status == TransactionStatusCancelled
}
