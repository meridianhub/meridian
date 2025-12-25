package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ControlAction represents an administrative control operation on a booking log.
type ControlAction string

// Control actions for administrative operations.
const (
	ControlActionSuspend   ControlAction = "SUSPEND"
	ControlActionResume    ControlAction = "RESUME"
	ControlActionTerminate ControlAction = "TERMINATE"
)

// IsValid returns true if the control action is valid.
func (c ControlAction) IsValid() bool {
	switch c {
	case ControlActionSuspend, ControlActionResume, ControlActionTerminate:
		return true
	}
	return false
}

// String returns the string representation of the control action.
func (c ControlAction) String() string {
	return string(c)
}

// Control operation errors.
var (
	// ErrInvalidControlAction is returned when an invalid control action is provided.
	ErrInvalidControlAction = errors.New("invalid control action")
	// ErrCannotSuspendTerminal is returned when trying to suspend a booking log in terminal state.
	ErrCannotSuspendTerminal = errors.New("cannot suspend booking log in terminal state")
	// ErrCannotResumePending is returned when trying to resume a booking log that is not suspended.
	ErrCannotResumePending = errors.New("cannot resume booking log that is not suspended")
	// ErrCannotTerminateTerminal is returned when trying to terminate a booking log already in terminal state.
	ErrCannotTerminateTerminal = errors.New("cannot terminate booking log already in terminal state")
	// ErrReasonRequired is returned when a reason is required but not provided.
	ErrReasonRequired = errors.New("reason is required for control operations")
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

// IsSuspended returns true if the booking log is currently suspended.
func (l *FinancialBookingLog) IsSuspended() bool {
	return l.Status == TransactionStatusFailed
}

// ControlLog applies a control action (SUSPEND, RESUME, TERMINATE) to the booking log.
//
// Control Actions:
//   - SUSPEND: Transitions from PENDING to FAILED (suspended state uses FAILED status)
//   - RESUME: Transitions from FAILED (suspended) back to PENDING
//   - TERMINATE: Transitions from PENDING or FAILED to CANCELLED (terminal state)
//
// State Machine:
//
//	PENDING → SUSPEND → FAILED (suspended)
//	FAILED (suspended) → RESUME → PENDING
//	PENDING/FAILED → TERMINATE → CANCELLED (terminal)
//
// Parameters:
//   - action: The control action to perform (SUSPEND, RESUME, TERMINATE)
//   - reason: Explanation for the control action (required)
//
// Returns a new instance following immutability guidelines per CONTRIBUTING.md.
// Returns an error if:
//   - action is invalid
//   - reason is empty
//   - state transition is not allowed
func (l FinancialBookingLog) ControlLog(action ControlAction, reason string) (FinancialBookingLog, error) {
	if !action.IsValid() {
		return l, ErrInvalidControlAction
	}
	if reason == "" {
		return l, ErrReasonRequired
	}

	var newStatus TransactionStatus

	switch action {
	case ControlActionSuspend:
		// SUSPEND: PENDING → FAILED (suspended)
		if l.IsTerminal() {
			return l, ErrCannotSuspendTerminal
		}
		newStatus = TransactionStatusFailed

	case ControlActionResume:
		// RESUME: FAILED (suspended) → PENDING
		if !l.IsSuspended() {
			return l, ErrCannotResumePending
		}
		newStatus = TransactionStatusPending

	case ControlActionTerminate:
		// TERMINATE: PENDING/FAILED → CANCELLED
		if l.Status == TransactionStatusPosted ||
			l.Status == TransactionStatusCancelled ||
			l.Status == TransactionStatusReversed {
			return l, ErrCannotTerminateTerminal
		}
		newStatus = TransactionStatusCancelled

	default:
		return l, ErrInvalidControlAction
	}

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
	}, nil
}
