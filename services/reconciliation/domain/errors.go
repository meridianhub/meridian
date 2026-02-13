package domain

import "errors"

var (
	// ErrNotFound is returned when a domain entity is not found.
	ErrNotFound = errors.New("entity not found")
	// ErrConflict is returned when there's a conflict (e.g., duplicate ID).
	ErrConflict = errors.New("entity conflict")
	// ErrOptimisticLock is returned when optimistic locking fails.
	ErrOptimisticLock = errors.New("optimistic lock failure: resource was modified")
	// ErrInvalidStatusTransition is returned when an invalid status transition is attempted.
	ErrInvalidStatusTransition = errors.New("invalid status transition")
	// ErrEmptyAccountID is returned when account ID is empty.
	ErrEmptyAccountID = errors.New("account ID cannot be empty")
	// ErrEmptyInstrumentCode is returned when instrument code is empty.
	ErrEmptyInstrumentCode = errors.New("instrument code cannot be empty")
	// ErrInvalidPeriod is returned when the period start is after end.
	ErrInvalidPeriod = errors.New("period start must be before period end")
	// ErrEmptyScope is returned when reconciliation scope is missing.
	ErrEmptyScope = errors.New("reconciliation scope is required")
	// ErrEmptySettlementType is returned when settlement type is missing.
	ErrEmptySettlementType = errors.New("settlement type is required")
	// ErrRunNotRunning is returned when attempting to complete a run that is not running.
	ErrRunNotRunning = errors.New("settlement run is not in running state")
	// ErrEmptyVarianceReason is returned when variance reason is missing.
	ErrEmptyVarianceReason = errors.New("variance reason is required")
	// ErrNegativeAmount is returned when a monetary amount is negative where not allowed.
	ErrNegativeAmount = errors.New("amount cannot be negative")
	// ErrEmptyDisputeReason is returned when dispute reason is missing.
	ErrEmptyDisputeReason = errors.New("dispute reason is required")
	// ErrEmptyVarianceID is returned when variance ID is missing for a dispute.
	ErrEmptyVarianceID = errors.New("variance ID is required")
	// ErrEmptyAssertionExpression is returned when the assertion expression is missing.
	ErrEmptyAssertionExpression = errors.New("assertion expression is required")
	// ErrEmptyInitiatedBy is returned when initiated by is empty.
	ErrEmptyInitiatedBy = errors.New("initiated by cannot be empty")
	// ErrUnauthorized is returned when the caller lacks required authorization.
	ErrUnauthorized = errors.New("unauthorized: insufficient permissions")
	// ErrUnimplemented is returned for features not yet implemented.
	ErrUnimplemented = errors.New("not implemented")
	// ErrNotFinalSettlement is returned when attempting to finalize a non-FINAL settlement type.
	ErrNotFinalSettlement = errors.New("only FINAL settlement type runs can be finalized")
	// ErrRunNotCompleted is returned when attempting to finalize a run that is not completed.
	ErrRunNotCompleted = errors.New("settlement run must be in COMPLETED state to finalize")
	// ErrPositionLockFailed is returned when the position lock request to PK fails after retries.
	ErrPositionLockFailed = errors.New("failed to acquire position lock")
	// ErrEmptyOverrideReason is returned when override reason is empty.
	ErrEmptyOverrideReason = errors.New("override reason cannot be empty")
)
