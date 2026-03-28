package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidStatusTransition is returned when an invalid status transition is attempted
	ErrInvalidStatusTransition = errors.New("invalid status transition")
	// ErrAlreadyPosted is returned when attempting to modify a posted transaction
	ErrAlreadyPosted = errors.New("transaction already posted")
	// ErrAlreadyReconciled is returned when attempting to modify a reconciled transaction
	ErrAlreadyReconciled = errors.New("transaction already reconciled")
	// ErrCannotAmend is returned when a transaction cannot be amended
	ErrCannotAmend = errors.New("transaction cannot be amended in current state")
	// ErrEmptyAccountID is returned when account ID is empty
	ErrEmptyAccountID = errors.New("account ID cannot be empty")
	// ErrTooManyEntries is returned when the maximum number of entries is exceeded
	ErrTooManyEntries = errors.New("maximum number of transaction entries exceeded")
	// ErrTooManyAuditEntries is returned when the maximum number of audit entries is exceeded
	ErrTooManyAuditEntries = errors.New("maximum number of audit entries exceeded")
	// ErrInvalidReconciliationStatus is returned when trying to mark as reconciled with unreconciled status
	ErrInvalidReconciliationStatus = errors.New("reconciliation status must not be unreconciled when marking as reconciled")
	// ErrInvalidEffectiveDate is returned when the effective date is invalid (e.g., in the future)
	ErrInvalidEffectiveDate = errors.New("effective date cannot be in the future")
)

const (
	// MaxTransactionEntries is the maximum number of transaction entries allowed
	MaxTransactionEntries = 10000
	// MaxAuditEntries is the maximum number of audit entries allowed
	MaxAuditEntries = 10000
)

// FinancialPositionLog represents a comprehensive log of financial position for an account.
// This is the aggregate root for the Position Keeping domain.
//
// The Version field implements optimistic concurrency control to prevent lost updates
// in concurrent scenarios. The persistence layer should use this field in UPDATE
// statements (e.g., WHERE log_id = ? AND version = ?) to detect conflicts.
// The Version is incremented by all state-changing lifecycle methods that modify
// StatusTracking (MarkReconciled, MarkPosted, Reject, Amend, Fail, Cancel).
// AddEntry does NOT increment Version as it represents accumulation within a draft
// state rather than a lifecycle state transition.
//
// Opening Balance Support:
// The OpeningBalance field stores the initial balance when the position log is created
// during a migration scenario. This allows capturing existing balances from legacy systems
// without requiring the full transaction history. The OpeningBalanceRecordedAt field tracks
// when the opening balance was recorded. Both fields are immutable after initialization.
type FinancialPositionLog struct {
	LogID                    uuid.UUID
	AccountID                string
	AccountServiceDomain     string // BIAN Service Domain: "CURRENT_ACCOUNT", "INTERNAL_ACCOUNT", or ""
	TransactionLogEntries    []*TransactionLogEntry
	TransactionLineage       *TransactionLineage
	AuditTrail               []*AuditTrailEntry
	StatusTracking           *StatusTracking
	CreatedAt                time.Time
	UpdatedAt                time.Time
	Version                  int64 // Optimistic lock version, incremented on status transitions
	OpeningBalance           Money // Initial balance for migration scenarios (immutable after init)
	OpeningBalanceRecordedAt time.Time
}

// NewFinancialPositionLog creates a FinancialPositionLog for the given account, initializing identifiers, timestamps, version, empty entry and audit collections, and status tracking.
// If initialEntry is non-nil it will be appended to the log; any error produced while adding the entry is returned.
// Returns ErrEmptyAccountID when accountID is empty.
func NewFinancialPositionLog(
	accountID string,
	initialEntry *TransactionLogEntry,
	lineage *TransactionLineage,
) (*FinancialPositionLog, error) {
	if accountID == "" {
		return nil, ErrEmptyAccountID
	}

	now := time.Now().UTC()
	log := &FinancialPositionLog{
		LogID:                 uuid.New(),
		AccountID:             accountID,
		TransactionLogEntries: make([]*TransactionLogEntry, 0),
		TransactionLineage:    lineage,
		AuditTrail:            make([]*AuditTrailEntry, 0),
		StatusTracking:        NewStatusTracking(),
		CreatedAt:             now,
		UpdatedAt:             now,
		Version:               1,
	}

	// Add initial entry if provided
	if initialEntry != nil {
		if err := log.AddEntry(initialEntry); err != nil {
			return nil, err
		}
	}

	return log, nil
}

// NewFinancialPositionLogWithOpeningBalance creates a FinancialPositionLog with an opening balance
// for migration scenarios. This constructor is used when bringing in existing account balances
// from legacy systems where the full transaction history is not available.
//
// The opening balance is represented as a Money value which may be positive (CREDIT direction)
// or negative (DEBIT direction). An opening balance transaction entry is automatically created
// with TransactionSourceOpeningBalance source and TransactionStatusPosted status.
//
// Parameters:
//   - accountID: The unique identifier for the account
//   - openingBalance: The initial balance (positive for credit, negative for debit)
//   - effectiveDate: The date when the opening balance becomes effective (cannot be in future)
//   - migrationReference: A reference string identifying the migration source/batch
//
// Returns:
//   - ErrEmptyAccountID when accountID is empty
//   - ErrInvalidEffectiveDate when effectiveDate is in the future
func NewFinancialPositionLogWithOpeningBalance(
	accountID string,
	openingBalance Money,
	effectiveDate time.Time,
	migrationReference string,
) (*FinancialPositionLog, error) {
	if accountID == "" {
		return nil, ErrEmptyAccountID
	}

	now := time.Now().UTC()

	// Validate effective date is not in the future (allow 1 minute tolerance for clock skew)
	if effectiveDate.After(now.Add(time.Minute)) {
		return nil, ErrInvalidEffectiveDate
	}

	log := &FinancialPositionLog{
		LogID:                    uuid.New(),
		AccountID:                accountID,
		TransactionLogEntries:    make([]*TransactionLogEntry, 0),
		TransactionLineage:       nil,
		AuditTrail:               make([]*AuditTrailEntry, 0),
		StatusTracking:           NewStatusTracking(),
		CreatedAt:                now,
		UpdatedAt:                now,
		Version:                  1,
		OpeningBalance:           openingBalance,
		OpeningBalanceRecordedAt: now,
	}

	if err := addOpeningBalanceEntry(log, openingBalance, accountID, effectiveDate, migrationReference); err != nil {
		return nil, err
	}

	// Mark the opening balance transaction as posted (it's already finalized)
	if err := log.StatusTracking.UpdateStatus(TransactionStatusPosted, "Opening balance established"); err != nil {
		return nil, err
	}
	log.Version++

	return log, nil
}

// addOpeningBalanceEntry creates and adds an opening balance transaction entry if amount is non-zero.
func addOpeningBalanceEntry(log *FinancialPositionLog, openingBalance Money, accountID string, effectiveDate time.Time, migrationReference string) error {
	if openingBalance.IsZero() {
		return nil
	}

	direction, entryAmount := resolveOpeningBalanceDirection(openingBalance)

	entry, err := NewTransactionLogEntry(
		uuid.New(),
		accountID,
		entryAmount,
		direction,
		effectiveDate,
		"Opening balance",
		migrationReference,
		TransactionSourceOpeningBalance,
	)
	if err != nil {
		return err
	}

	return log.AddEntry(entry)
}

// resolveOpeningBalanceDirection determines posting direction and absolute amount from an opening balance.
func resolveOpeningBalanceDirection(openingBalance Money) (PostingDirection, Money) {
	if openingBalance.IsNegative() {
		return PostingDirectionDebit, openingBalance.Negate()
	}
	return PostingDirectionCredit, openingBalance
}

// HasOpeningBalance returns true if the log was created with an opening balance.
func (l *FinancialPositionLog) HasOpeningBalance() bool {
	return !l.OpeningBalanceRecordedAt.IsZero()
}

// AddEntry adds a new transaction entry to the log.
func (l *FinancialPositionLog) AddEntry(entry *TransactionLogEntry) error {
	if l.StatusTracking.CurrentStatus == TransactionStatusPosted {
		return ErrAlreadyPosted
	}

	if len(l.TransactionLogEntries) >= MaxTransactionEntries {
		return ErrTooManyEntries
	}

	l.TransactionLogEntries = append(l.TransactionLogEntries, entry)
	l.UpdatedAt = time.Now().UTC()

	return nil
}

// AddAuditEntry adds a new audit trail entry to the log.
func (l *FinancialPositionLog) AddAuditEntry(entry *AuditTrailEntry) error {
	if len(l.AuditTrail) >= MaxAuditEntries {
		return ErrTooManyAuditEntries
	}

	l.AuditTrail = append(l.AuditTrail, entry)
	l.UpdatedAt = time.Now().UTC()

	return nil
}

// MarkReconciled marks the log as reconciled with a specific reconciliation status.
func (l *FinancialPositionLog) MarkReconciled(
	reconciliationStatus ReconciliationStatus,
	reason string,
	auditEntry *AuditTrailEntry,
) error {
	if l.StatusTracking.CurrentStatus == TransactionStatusPosted {
		return ErrAlreadyPosted
	}

	// Validate reconciliation status is actually a reconciled state
	if reconciliationStatus == ReconciliationStatusUnreconciled {
		return ErrInvalidReconciliationStatus
	}

	// Validate we can add audit entry before modifying state
	if auditEntry != nil && len(l.AuditTrail) >= MaxAuditEntries {
		return ErrTooManyAuditEntries
	}

	// Update transaction status to reconciled
	if err := l.StatusTracking.UpdateStatus(TransactionStatusReconciled, reason); err != nil {
		return err
	}

	// Update reconciliation status
	l.StatusTracking.MarkReconciled(reconciliationStatus)

	// Add audit entry (we already checked capacity above)
	if auditEntry != nil {
		if err := l.AddAuditEntry(auditEntry); err != nil {
			return err
		}
	}

	l.UpdatedAt = time.Now().UTC()
	l.Version++

	return nil
}

// MarkPosted marks the log as posted to the ledger.
func (l *FinancialPositionLog) MarkPosted(
	reason string,
	auditEntry *AuditTrailEntry,
) error {
	if l.StatusTracking.CurrentStatus == TransactionStatusPosted {
		return ErrAlreadyPosted
	}

	// Validate we can add audit entry before modifying state
	if auditEntry != nil && len(l.AuditTrail) >= MaxAuditEntries {
		return ErrTooManyAuditEntries
	}

	// Update status to posted
	if err := l.StatusTracking.UpdateStatus(TransactionStatusPosted, reason); err != nil {
		return err
	}

	// Add audit entry (we already checked capacity above)
	if auditEntry != nil {
		if err := l.AddAuditEntry(auditEntry); err != nil {
			return err
		}
	}

	l.UpdatedAt = time.Now().UTC()
	l.Version++

	return nil
}

// Reject rejects the log with a reason.
func (l *FinancialPositionLog) Reject(
	reason string,
	auditEntry *AuditTrailEntry,
) error {
	if l.StatusTracking.CurrentStatus == TransactionStatusPosted {
		return ErrAlreadyPosted
	}

	// Validate we can add audit entry before modifying state
	if auditEntry != nil && len(l.AuditTrail) >= MaxAuditEntries {
		return ErrTooManyAuditEntries
	}

	// Update status to rejected
	if err := l.StatusTracking.UpdateStatus(TransactionStatusRejected, reason); err != nil {
		return err
	}

	// Add audit entry (we already checked capacity above)
	if auditEntry != nil {
		if err := l.AddAuditEntry(auditEntry); err != nil {
			return err
		}
	}

	l.UpdatedAt = time.Now().UTC()
	l.Version++

	return nil
}

// Amend creates an amendment to the log (updates status to amended).
func (l *FinancialPositionLog) Amend(
	reason string,
	auditEntry *AuditTrailEntry,
) error {
	if !l.CanBeAmended() {
		return ErrCannotAmend
	}

	// Validate we can add audit entry before modifying state
	if auditEntry != nil && len(l.AuditTrail) >= MaxAuditEntries {
		return ErrTooManyAuditEntries
	}

	// Update status to amended
	if err := l.StatusTracking.UpdateStatus(TransactionStatusAmended, reason); err != nil {
		return err
	}

	// Add audit entry (we already checked capacity above)
	if auditEntry != nil {
		if err := l.AddAuditEntry(auditEntry); err != nil {
			return err
		}
	}

	l.UpdatedAt = time.Now().UTC()
	l.Version++

	return nil
}

// CanBeAmended checks if the log can be amended in its current state.
func (l *FinancialPositionLog) CanBeAmended() bool {
	// Can only amend reconciled transactions that are not yet posted
	return l.StatusTracking.CurrentStatus == TransactionStatusReconciled &&
		!l.StatusTracking.CurrentStatus.IsFinal()
}

// Fail marks the log as failed with a failure reason.
func (l *FinancialPositionLog) Fail(
	failureReason string,
	auditEntry *AuditTrailEntry,
) error {
	if l.StatusTracking.CurrentStatus == TransactionStatusPosted {
		return ErrAlreadyPosted
	}

	// Validate we can add audit entry before modifying state
	if auditEntry != nil && len(l.AuditTrail) >= MaxAuditEntries {
		return ErrTooManyAuditEntries
	}

	// Update status to failed
	if err := l.StatusTracking.MarkFailed(failureReason); err != nil {
		return err
	}

	// Add audit entry (we already checked capacity above)
	if auditEntry != nil {
		if err := l.AddAuditEntry(auditEntry); err != nil {
			return err
		}
	}

	l.UpdatedAt = time.Now().UTC()
	l.Version++

	return nil
}

// Cancel cancels the log.
func (l *FinancialPositionLog) Cancel(
	reason string,
	auditEntry *AuditTrailEntry,
) error {
	if l.StatusTracking.CurrentStatus == TransactionStatusPosted {
		return ErrAlreadyPosted
	}

	// Validate we can add audit entry before modifying state
	if auditEntry != nil && len(l.AuditTrail) >= MaxAuditEntries {
		return ErrTooManyAuditEntries
	}

	// Update status to cancelled
	if err := l.StatusTracking.UpdateStatus(TransactionStatusCancelled, reason); err != nil {
		return err
	}

	// Add audit entry (we already checked capacity above)
	if auditEntry != nil {
		if err := l.AddAuditEntry(auditEntry); err != nil {
			return err
		}
	}

	l.UpdatedAt = time.Now().UTC()
	l.Version++

	return nil
}

// IsPosted returns true if the log has been posted.
func (l *FinancialPositionLog) IsPosted() bool {
	return l.StatusTracking.CurrentStatus == TransactionStatusPosted
}

// IsReconciled returns true if the log has been reconciled.
func (l *FinancialPositionLog) IsReconciled() bool {
	return l.StatusTracking.IsReconciled()
}

// IsFinal returns true if the log is in a final state.
func (l *FinancialPositionLog) IsFinal() bool {
	return l.StatusTracking.CurrentStatus.IsFinal()
}

// EntryCount returns the number of transaction entries in the log.
func (l *FinancialPositionLog) EntryCount() int {
	return len(l.TransactionLogEntries)
}

// AuditEntryCount returns the number of audit entries in the log.
func (l *FinancialPositionLog) AuditEntryCount() int {
	return len(l.AuditTrail)
}
