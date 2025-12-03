// Package domain contains the PaymentOrder aggregate root and related domain logic
// for orchestrating payment saga workflows.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"

	cadomain "github.com/meridianhub/meridian/internal/current-account/domain"
)

// PaymentOrder domain errors
var (
	ErrInvalidPaymentOrderTransition = errors.New("invalid payment order status transition")
	ErrPaymentOrderTerminal          = errors.New("payment order is in terminal state")
	ErrPaymentOrderNotCancellable    = errors.New("payment order cannot be cancelled in current state")
	ErrInvalidPaymentOrderAmount     = errors.New("payment order amount must be positive")
	ErrMissingDebtorAccountID        = errors.New("debtor account ID is required")
	ErrMissingCreditorReference      = errors.New("creditor reference is required")
	ErrMissingIdempotencyKey         = errors.New("idempotency key is required")
	ErrMissingCorrelationID          = errors.New("correlation ID is required")
	ErrMissingLienID                 = errors.New("lien ID is required for Reserve transition")
	ErrMissingGatewayReferenceID     = errors.New("gateway reference ID is required for Execute transition")
	ErrMissingFailureReason          = errors.New("failure reason is required for Fail transition")
	ErrMissingReversalReason         = errors.New("reversal reason is required for Reverse transition")
)

// PaymentOrderStatus represents the lifecycle state of a payment order in the saga.
type PaymentOrderStatus string

// Payment order status constants following the proto definition.
//
// State Machine:
//
//	INITIATED -> RESERVED -> EXECUTING -> COMPLETED (happy path)
//
// Failure transitions (from any non-terminal state):
//
//	INITIATED -> FAILED (validation failure, account not found)
//	RESERVED  -> FAILED (gateway rejection, timeout) - triggers lien release
//	EXECUTING -> FAILED (gateway rejection) - triggers lien release
//
// Cancellation (user/system initiated before completion):
//
//	INITIATED -> CANCELLED (no compensation needed)
//	RESERVED  -> CANCELLED (triggers lien release)
//	EXECUTING -> not cancellable (must wait for gateway response)
//
// Reversal (post-completion compensation):
//
//	COMPLETED -> REVERSED (triggers compensating ledger entries)
//
// Terminal states: COMPLETED, FAILED, CANCELLED, REVERSED
const (
	PaymentOrderStatusInitiated PaymentOrderStatus = "INITIATED"
	PaymentOrderStatusReserved  PaymentOrderStatus = "RESERVED"
	PaymentOrderStatusExecuting PaymentOrderStatus = "EXECUTING"
	PaymentOrderStatusCompleted PaymentOrderStatus = "COMPLETED"
	PaymentOrderStatusFailed    PaymentOrderStatus = "FAILED"
	PaymentOrderStatusCancelled PaymentOrderStatus = "CANCELLED"
	PaymentOrderStatusReversed  PaymentOrderStatus = "REVERSED"
)

// LienExecutionStatus tracks the status of lien execution for completed payment orders.
type LienExecutionStatus string

const (
	// LienExecutionStatusUnspecified means the status is not set (payment not yet completed).
	LienExecutionStatusUnspecified LienExecutionStatus = ""
	// LienExecutionStatusPending means ExecuteLien has not been attempted yet or is in progress.
	LienExecutionStatusPending LienExecutionStatus = "PENDING"
	// LienExecutionStatusSucceeded means ExecuteLien completed successfully.
	LienExecutionStatusSucceeded LienExecutionStatus = "SUCCEEDED"
	// LienExecutionStatusFailed means ExecuteLien failed after all retry attempts.
	LienExecutionStatusFailed LienExecutionStatus = "FAILED"
)

// PaymentOrder represents the aggregate root for the payment order saga.
// It acts as the saga orchestrator for money movement, coordinating
// across CurrentAccount (reservations) and FinancialAccounting (ledger booking).
//
// Note: Fields are exported for persistence layer access. State transitions should only be
// performed via the state machine methods which enforce invariants.
// The Version field is a persistence concern exposed here for optimistic locking support.
type PaymentOrder struct {
	ID                 uuid.UUID
	DebtorAccountID    string
	CreditorReference  string
	Amount             cadomain.Money
	Status             PaymentOrderStatus
	LienID             string
	GatewayReferenceID string
	LedgerBookingID    string
	CorrelationID      string
	CausationID        string
	IdempotencyKey     string
	FailureReason      string
	ErrorCode          string
	Version            int
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ReservedAt         *time.Time
	ExecutingAt        *time.Time
	CompletedAt        *time.Time
	FailedAt           *time.Time
	CancelledAt        *time.Time
	ReversedAt         *time.Time
	// LienExecutionStatus tracks the status of lien execution for completed payment orders.
	LienExecutionStatus   LienExecutionStatus
	LienExecutionAttempts int
	LienExecutionError    string
}

// NewPaymentOrder creates a new payment order in INITIATED status.
// Validates that amount is positive and required fields are present.
func NewPaymentOrder(
	debtorAccountID string,
	creditorReference string,
	amount cadomain.Money,
	idempotencyKey string,
	correlationID string,
) (*PaymentOrder, error) {
	if debtorAccountID == "" {
		return nil, ErrMissingDebtorAccountID
	}
	if creditorReference == "" {
		return nil, ErrMissingCreditorReference
	}
	if !amount.IsPositive() {
		return nil, ErrInvalidPaymentOrderAmount
	}
	if idempotencyKey == "" {
		return nil, ErrMissingIdempotencyKey
	}
	if correlationID == "" {
		return nil, ErrMissingCorrelationID
	}

	now := time.Now()
	return &PaymentOrder{
		ID:                uuid.New(),
		DebtorAccountID:   debtorAccountID,
		CreditorReference: creditorReference,
		Amount:            amount,
		Status:            PaymentOrderStatusInitiated,
		IdempotencyKey:    idempotencyKey,
		CorrelationID:     correlationID,
		Version:           1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

// Reserve transitions the payment order from INITIATED to RESERVED.
// This is called when funds have been successfully reserved via a lien.
// Returns ErrInvalidPaymentOrderTransition if not in INITIATED state.
func (p *PaymentOrder) Reserve(lienID string) error {
	if lienID == "" {
		return ErrMissingLienID
	}

	if p.Status != PaymentOrderStatusInitiated {
		return ErrInvalidPaymentOrderTransition
	}

	now := time.Now()
	p.Status = PaymentOrderStatusReserved
	p.LienID = lienID
	p.ReservedAt = &now
	p.UpdatedAt = now
	return nil
}

// Execute transitions the payment order from RESERVED to EXECUTING.
// This is called when the payment has been sent to the external gateway.
// Returns ErrInvalidPaymentOrderTransition if not in RESERVED state.
func (p *PaymentOrder) Execute(gatewayReferenceID string) error {
	if gatewayReferenceID == "" {
		return ErrMissingGatewayReferenceID
	}

	if p.Status != PaymentOrderStatusReserved {
		return ErrInvalidPaymentOrderTransition
	}

	now := time.Now()
	p.Status = PaymentOrderStatusExecuting
	p.GatewayReferenceID = gatewayReferenceID
	p.ExecutingAt = &now
	p.UpdatedAt = now
	return nil
}

// Complete transitions the payment order from EXECUTING to COMPLETED.
// This is called when the gateway confirms the payment was successful.
// The ledgerBookingID is optional - it may be empty if ledger booking is performed asynchronously.
// Returns ErrInvalidPaymentOrderTransition if not in EXECUTING state.
func (p *PaymentOrder) Complete(ledgerBookingID string) error {
	if p.Status != PaymentOrderStatusExecuting {
		return ErrInvalidPaymentOrderTransition
	}

	now := time.Now()
	p.Status = PaymentOrderStatusCompleted
	p.LedgerBookingID = ledgerBookingID
	p.CompletedAt = &now
	p.UpdatedAt = now
	return nil
}

// Fail transitions the payment order to FAILED state from any non-terminal state.
// This is called when the payment fails at any stage of the saga.
// Idempotent: Returns nil if already failed.
// Returns ErrPaymentOrderTerminal if in a terminal state other than FAILED.
func (p *PaymentOrder) Fail(reason string, errorCode string) error {
	if reason == "" {
		return ErrMissingFailureReason
	}

	// Idempotent: already failed
	if p.Status == PaymentOrderStatusFailed {
		return nil
	}

	if p.IsTerminal() {
		return ErrPaymentOrderTerminal
	}

	now := time.Now()
	p.Status = PaymentOrderStatusFailed
	p.FailureReason = reason
	p.ErrorCode = errorCode
	p.FailedAt = &now
	p.UpdatedAt = now
	return nil
}

// Cancel transitions the payment order to CANCELLED state.
// Can only be called from INITIATED or RESERVED states.
// EXECUTING payments cannot be cancelled as they must wait for gateway response.
// Idempotent: Returns nil if already cancelled.
// Returns ErrPaymentOrderNotCancellable if in EXECUTING or terminal state.
func (p *PaymentOrder) Cancel(reason string) error {
	// Idempotent: already cancelled
	if p.Status == PaymentOrderStatusCancelled {
		return nil
	}

	if p.Status != PaymentOrderStatusInitiated && p.Status != PaymentOrderStatusReserved {
		return ErrPaymentOrderNotCancellable
	}

	now := time.Now()
	p.Status = PaymentOrderStatusCancelled
	p.FailureReason = reason // Reuse failure_reason for cancellation reason
	p.CancelledAt = &now
	p.UpdatedAt = now
	return nil
}

// Reverse transitions the payment order from COMPLETED to REVERSED.
// This is used for post-completion compensation (e.g., refunds, chargebacks).
// Requires a reason for audit purposes.
// Idempotent: Returns nil if already reversed.
// Returns ErrMissingReversalReason if reason is empty.
// Returns ErrInvalidPaymentOrderTransition if not in COMPLETED state.
func (p *PaymentOrder) Reverse(reason string) error {
	if reason == "" {
		return ErrMissingReversalReason
	}

	// Idempotent: already reversed
	if p.Status == PaymentOrderStatusReversed {
		return nil
	}

	if p.Status != PaymentOrderStatusCompleted {
		return ErrInvalidPaymentOrderTransition
	}

	now := time.Now()
	p.Status = PaymentOrderStatusReversed
	p.FailureReason = reason // Reuse failure_reason for reversal reason
	p.ReversedAt = &now
	p.UpdatedAt = now
	return nil
}

// IsTerminal returns true if the payment order is in a terminal state.
// Terminal states: COMPLETED, FAILED, CANCELLED, REVERSED
func (p *PaymentOrder) IsTerminal() bool {
	switch p.Status {
	case PaymentOrderStatusInitiated,
		PaymentOrderStatusReserved,
		PaymentOrderStatusExecuting:
		return false
	case PaymentOrderStatusCompleted,
		PaymentOrderStatusFailed,
		PaymentOrderStatusCancelled,
		PaymentOrderStatusReversed:
		return true
	}
	return false
}

// CanCancel returns true if the payment order can be cancelled.
// Only INITIATED and RESERVED states are cancellable.
func (p *PaymentOrder) CanCancel() bool {
	return p.Status == PaymentOrderStatusInitiated || p.Status == PaymentOrderStatusReserved
}

// CanReverse returns true if the payment order can be reversed.
// Only COMPLETED state can be reversed.
func (p *PaymentOrder) CanReverse() bool {
	return p.Status == PaymentOrderStatusCompleted
}

// RequiresLienRelease returns true if the payment order has a lien that needs to be released.
// This is true when the order is in RESERVED or EXECUTING states with an active lien.
//
// IMPORTANT: This method checks the current status. Call this BEFORE transitioning to
// FAILED or CANCELLED to determine if lien compensation is needed. After the transition,
// the status changes and this will return false.
//
// Usage pattern:
//
//	needsRelease := po.RequiresLienRelease()
//	po.Fail(reason, errorCode)
//	if needsRelease {
//	    // trigger lien release compensation
//	}
func (p *PaymentOrder) RequiresLienRelease() bool {
	return p.LienID != "" && (p.Status == PaymentOrderStatusReserved || p.Status == PaymentOrderStatusExecuting)
}

// SetCausationID sets the causation ID for tracking the event that caused the last state change.
func (p *PaymentOrder) SetCausationID(causationID string) {
	p.CausationID = causationID
	p.UpdatedAt = time.Now()
}

// SetLienExecutionPending marks lien execution as pending.
// Call this when starting lien execution retry attempts.
func (p *PaymentOrder) SetLienExecutionPending() {
	p.LienExecutionStatus = LienExecutionStatusPending
	p.UpdatedAt = time.Now()
}

// SetLienExecutionSucceeded marks lien execution as succeeded.
// Call this when ExecuteLien completes successfully.
func (p *PaymentOrder) SetLienExecutionSucceeded() {
	p.LienExecutionStatus = LienExecutionStatusSucceeded
	p.LienExecutionError = ""
	p.UpdatedAt = time.Now()
}

// maxLienExecutionErrorLength is the maximum length of the lien execution error message.
// Matches the database column size (VARCHAR(1000)) and proto field constraint.
const maxLienExecutionErrorLength = 1000

// SetLienExecutionFailed marks lien execution as failed after all retries exhausted.
// Call this when all retry attempts have failed and manual reconciliation is needed.
// Error messages exceeding maxLienExecutionErrorLength are truncated with a suffix.
func (p *PaymentOrder) SetLienExecutionFailed(err string) {
	p.LienExecutionStatus = LienExecutionStatusFailed
	// Truncate error message to fit database column size
	if len(err) > maxLienExecutionErrorLength {
		const truncatedSuffix = "...[truncated]"
		err = err[:maxLienExecutionErrorLength-len(truncatedSuffix)] + truncatedSuffix
	}
	p.LienExecutionError = err
	p.UpdatedAt = time.Now()
}

// RequiresLienExecution returns true if this payment order needs lien execution.
// This is true for COMPLETED payments with a lien that hasn't been successfully executed.
func (p *PaymentOrder) RequiresLienExecution() bool {
	return p.Status == PaymentOrderStatusCompleted &&
		p.LienID != "" &&
		p.LienExecutionStatus != LienExecutionStatusSucceeded
}
