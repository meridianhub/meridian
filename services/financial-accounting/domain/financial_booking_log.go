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
//   - Cannot be modified once in terminal state (POSTED, FAILED, CANCELLED, TERMINATED)
//   - Can be SUSPENDED temporarily and then RESUMED or TERMINATED
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

	// statusHistory provides an audit trail for all status changes
	// Access via StatusHistory() method which returns a defensive copy
	statusHistory []*StatusChangeEntry

	// preSuspendStatus stores the status before suspension for resumption
	preSuspendStatus *TransactionStatus
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
		statusHistory:           make([]*StatusChangeEntry, 0),
		preSuspendStatus:        nil,
	}
}

// WithStatus returns a new booking log with updated status.
//
// Status transitions are validated at the service layer:
//   - PENDING → POSTED (when debits == credits)
//   - PENDING → FAILED (validation errors)
//   - PENDING → CANCELLED (business cancellation)
//   - Terminal states (POSTED, FAILED, CANCELLED, TERMINATED) cannot transition
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
		statusHistory:           l.statusHistory,
		preSuspendStatus:        l.preSuspendStatus,
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
		statusHistory:           l.statusHistory,
		preSuspendStatus:        l.preSuspendStatus,
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
		statusHistory:           l.statusHistory,
		preSuspendStatus:        l.preSuspendStatus,
	}
}

// IsTerminal returns true if the status is terminal (cannot transition).
func (l *FinancialBookingLog) IsTerminal() bool {
	return l.Status == TransactionStatusPosted ||
		l.Status == TransactionStatusFailed ||
		l.Status == TransactionStatusCancelled ||
		l.Status == TransactionStatusTerminated
}

// StatusHistory returns a defensive copy of the status history.
func (l FinancialBookingLog) StatusHistory() []*StatusChangeEntry {
	if l.statusHistory == nil {
		return []*StatusChangeEntry{}
	}
	result := make([]*StatusChangeEntry, len(l.statusHistory))
	copy(result, l.statusHistory)
	return result
}

// StatusHistoryCount returns the number of status history entries.
func (l *FinancialBookingLog) StatusHistoryCount() int {
	return len(l.statusHistory)
}

// CanTransitionTo checks if the log can transition to the target status.
func (l *FinancialBookingLog) CanTransitionTo(target TransactionStatus) error {
	if l.Status == TransactionStatusTerminated {
		return ErrAlreadyTerminated
	}

	if !l.Status.CanTransitionTo(target) {
		return ErrInvalidStatusTransition
	}

	return nil
}

// ControlLog applies a control action (SUSPEND, RESUME, TERMINATE) to the log.
// It validates the transition using the state machine and returns a new instance
// with updated status and status history for compliance tracking.
//
// Parameters:
//   - action: The control action to apply (SUSPEND, RESUME, TERMINATE)
//   - reason: Context explaining why the action is being performed
//   - operatorID: Identifier of who is performing the action (required)
//
// Returns:
//   - A new FinancialBookingLog with updated status on success
//   - An error if the action is invalid or the transition is not allowed
func (l FinancialBookingLog) ControlLog(
	action ControlAction,
	reason string,
	operatorID string,
) (FinancialBookingLog, error) {
	// Validate action
	if !action.IsValid() {
		return l, ErrInvalidControlAction
	}

	// Validate operator ID
	if operatorID == "" {
		return l, ErrEmptyOperatorID
	}

	// Check if already terminated
	if l.Status == TransactionStatusTerminated {
		return l, ErrAlreadyTerminated
	}

	var targetStatus TransactionStatus
	var newPreSuspendStatus *TransactionStatus

	switch action {
	case ControlActionSuspend:
		if !l.Status.CanSuspend() {
			return l, ErrCannotSuspend
		}
		// Store current status for later resumption
		current := l.Status
		newPreSuspendStatus = &current
		targetStatus = TransactionStatusSuspended

	case ControlActionResume:
		if !l.Status.CanResume() {
			return l, ErrCannotResume
		}
		// Determine what status to resume to
		if l.preSuspendStatus != nil {
			targetStatus = *l.preSuspendStatus
		} else {
			// Default to PENDING if no pre-suspend status recorded
			targetStatus = TransactionStatusPending
		}
		newPreSuspendStatus = nil

	case ControlActionTerminate:
		if !l.Status.CanTerminate() {
			return l, ErrCannotTerminate
		}
		targetStatus = TransactionStatusTerminated
		newPreSuspendStatus = l.preSuspendStatus // preserve for audit

	case ControlActionUnspecified:
		return l, ErrInvalidControlAction
	}

	// Create status change entry for audit trail
	statusEntry := NewStatusChangeEntry(
		l.Status,
		targetStatus,
		reason,
		operatorID,
		action,
	)

	// Build new status history
	newHistory := make([]*StatusChangeEntry, len(l.statusHistory)+1)
	copy(newHistory, l.statusHistory)
	newHistory[len(l.statusHistory)] = statusEntry

	// Return new instance (immutability pattern)
	return FinancialBookingLog{
		ID:                      l.ID,
		FinancialAccountType:    l.FinancialAccountType,
		ProductServiceReference: l.ProductServiceReference,
		BusinessUnitReference:   l.BusinessUnitReference,
		ChartOfAccountsRules:    l.ChartOfAccountsRules,
		BaseCurrency:            l.BaseCurrency,
		Status:                  targetStatus,
		CreatedAt:               l.CreatedAt,
		UpdatedAt:               time.Now().UTC(),
		postings:                l.postings,
		statusHistory:           newHistory,
		preSuspendStatus:        newPreSuspendStatus,
	}, nil
}

// Suspend temporarily suspends the log processing.
// Returns a new instance with SUSPENDED status or an error if suspension is not allowed.
func (l FinancialBookingLog) Suspend(reason string, operatorID string) (FinancialBookingLog, error) {
	return l.ControlLog(ControlActionSuspend, reason, operatorID)
}

// Resume resumes a suspended log.
// Returns a new instance with the pre-suspend status or an error if resumption is not allowed.
func (l FinancialBookingLog) Resume(reason string, operatorID string) (FinancialBookingLog, error) {
	return l.ControlLog(ControlActionResume, reason, operatorID)
}

// Terminate permanently terminates the log.
// Returns a new instance with TERMINATED status or an error if termination is not allowed.
func (l FinancialBookingLog) Terminate(reason string, operatorID string) (FinancialBookingLog, error) {
	return l.ControlLog(ControlActionTerminate, reason, operatorID)
}

// IsSuspended returns true if the log is currently suspended.
func (l *FinancialBookingLog) IsSuspended() bool {
	return l.Status == TransactionStatusSuspended
}

// IsTerminated returns true if the log has been terminated.
func (l *FinancialBookingLog) IsTerminated() bool {
	return l.Status == TransactionStatusTerminated
}
