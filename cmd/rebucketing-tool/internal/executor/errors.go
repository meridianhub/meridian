package executor

import "errors"

var (
	// ErrUnauthorized is returned when a user lacks admin role for rebucketing
	ErrUnauthorized = errors.New("unauthorized: admin role required for rebucketing operations")

	// ErrMissingClaims is returned when auth claims are not found in context
	ErrMissingClaims = errors.New("missing authentication claims in context")

	// ErrTransactionRollback is returned when a transaction fails and is rolled back
	ErrTransactionRollback = errors.New("transaction rolled back due to partial failure")

	// ErrAuditLogWrite is returned when audit log entries cannot be written
	ErrAuditLogWrite = errors.New("failed to write audit log entries")

	// ErrPositionSoftDelete is returned when soft-deleting a position fails
	ErrPositionSoftDelete = errors.New("failed to soft-delete position")

	// ErrPositionInsert is returned when inserting a new position fails
	ErrPositionInsert = errors.New("failed to insert new position")

	// ErrEmptyPlan is returned when an empty rebucketing plan is provided
	ErrEmptyPlan = errors.New("rebucketing plan has no affected positions")

	// ErrInvalidBatchSize is returned when batch size is not positive
	ErrInvalidBatchSize = errors.New("batch size must be greater than 0")

	// ErrBatchSizeTooLarge is returned when batch size exceeds maximum
	ErrBatchSizeTooLarge = errors.New("batch size exceeds maximum of 10000")

	// ErrNilPool is returned when a nil database pool is provided
	ErrNilPool = errors.New("database pool cannot be nil")

	// ErrNilPlan is returned when a nil rebucketing plan is provided
	ErrNilPlan = errors.New("rebucketing plan cannot be nil")

	// ErrMissingInstrumentVersion is returned when instrument version is empty
	ErrMissingInstrumentVersion = errors.New("instrument version cannot be empty")

	// ErrInvalidBucketMapping is returned when a bucket mapping is invalid
	ErrInvalidBucketMapping = errors.New("invalid bucket mapping: old and new bucket keys required")
)

// TransactionRollbackError wraps the underlying error with rollback context.
type TransactionRollbackError struct {
	// Cause is the underlying error that triggered the rollback
	Cause error

	// PositionsProcessed is the count of positions processed before failure
	PositionsProcessed int64

	// BatchNumber is the batch number where the failure occurred
	BatchNumber int
}

// Error implements the error interface.
func (e *TransactionRollbackError) Error() string {
	if e.Cause == nil {
		return "transaction rolled back"
	}
	return "transaction rolled back: " + e.Cause.Error()
}

// Unwrap implements the errors.Unwrap interface.
func (e *TransactionRollbackError) Unwrap() error {
	return e.Cause
}

// Is implements the errors.Is interface to match ErrTransactionRollback.
func (e *TransactionRollbackError) Is(target error) bool {
	return target == ErrTransactionRollback
}

// AuditLogWriteError wraps the underlying error with audit context.
type AuditLogWriteError struct {
	// Cause is the underlying error
	Cause error

	// PositionID is the position that failed to audit
	PositionID string

	// Operation is the audit operation that failed
	Operation string
}

// Error implements the error interface.
func (e *AuditLogWriteError) Error() string {
	if e.Cause == nil {
		return "audit log write failed for position " + e.PositionID
	}
	return "audit log write failed for position " + e.PositionID + ": " + e.Cause.Error()
}

// Unwrap implements the errors.Unwrap interface.
func (e *AuditLogWriteError) Unwrap() error {
	return e.Cause
}

// Is implements the errors.Is interface to match ErrAuditLogWrite.
func (e *AuditLogWriteError) Is(target error) bool {
	return target == ErrAuditLogWrite
}

// UnauthorizedError provides details about authorization failure.
type UnauthorizedError struct {
	// UserID is the user who attempted the operation
	UserID string

	// RequiredRole is the role that was required
	RequiredRole string

	// ActualRoles are the roles the user has
	ActualRoles []string
}

// Error implements the error interface.
func (e *UnauthorizedError) Error() string {
	return "user " + e.UserID + " lacks required role: " + e.RequiredRole
}

// Is implements the errors.Is interface to match ErrUnauthorized.
func (e *UnauthorizedError) Is(target error) bool {
	return target == ErrUnauthorized
}
