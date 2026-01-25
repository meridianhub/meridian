// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"errors"
	"strings"
)

// Sentinel errors for FATAL business rule violations.
// These errors indicate non-retryable conditions where compensation should begin immediately.
var (
	// ErrInsufficientFunds indicates the account lacks required funds (business rule violation).
	ErrInsufficientFunds = errors.New("insufficient funds")

	// ErrInsufficientBalance indicates insufficient account balance (same semantic as ErrInsufficientFunds).
	ErrInsufficientBalance = errors.New("insufficient balance")

	// ErrAccountClosed indicates the account is closed and cannot process transactions.
	ErrAccountClosed = errors.New("account closed")

	// ErrAccountFrozen indicates the account is frozen and cannot process transactions.
	ErrAccountFrozen = errors.New("account frozen")

	// ErrInvalidAmount indicates the transaction amount is invalid (e.g., negative, zero).
	ErrInvalidAmount = errors.New("invalid amount")

	// ErrDuplicateTransaction indicates a duplicate idempotency key was detected.
	ErrDuplicateTransaction = errors.New("duplicate transaction")

	// ErrBusinessRuleViolation is a generic fatal error for business rule violations.
	ErrBusinessRuleViolation = errors.New("business rule violation")

	// ErrValidationFailed indicates input validation failed.
	ErrValidationFailed = errors.New("validation failed")

	// ErrNotFound indicates a required resource was not found.
	ErrNotFound = errors.New("not found")

	// ErrUnauthorized indicates the operation is not authorized.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden indicates the operation is forbidden.
	ErrForbidden = errors.New("forbidden")
)

// fatalErrors is the list of sentinel errors that are always classified as FATAL.
var fatalErrors = []error{
	ErrInsufficientFunds,
	ErrInsufficientBalance,
	ErrAccountClosed,
	ErrAccountFrozen,
	ErrInvalidAmount,
	ErrDuplicateTransaction,
	ErrBusinessRuleViolation,
	ErrValidationFailed,
	ErrNotFound,
	ErrUnauthorized,
	ErrForbidden,
	ErrMissingParam,
	ErrInvalidParamType,
	ErrInvalidDirection,
}

// fatalPatterns are string patterns in error messages that indicate FATAL errors.
var fatalPatterns = []string{
	"insufficient funds",
	"insufficient balance",
	"account closed",
	"account frozen",
	"invalid amount",
	"duplicate",
	"business rule",
	"validation failed",
	"validation error",
	"not found",
	"does not exist",
	"unauthorized",
	"forbidden",
	"permission denied",
	"access denied",
	"constraint violation",
	"unique constraint",
	"foreign key constraint",
	"check constraint",
	"null constraint",
}

// transientPatterns are string patterns in error messages that indicate TRANSIENT errors.
var transientPatterns = []string{
	"timeout",
	"deadline exceeded",
	"context canceled",
	"context cancelled",
	"connection refused",
	"connection reset",
	"connection closed",
	"temporary failure",
	"temporarily unavailable",
	"service unavailable",
	"unavailable",
	"retry",
	"retryable",
	"eagain",
	"etimedout",
	"econnreset",
	"econnrefused",
	"network error",
	"network unreachable",
	"host unreachable",
	"no route to host",
	"dns lookup failed",
	"too many requests",
	"rate limit",
	"throttle",
	"circuit breaker",
	"deadlock",
	"lock timeout",
	"could not serialize",
	"serialization failure",
	"i/o timeout",
	"eof",
	"broken pipe",
}

// ClassifyError determines the error category for a given error.
// It checks sentinel errors first using errors.Is, then falls back to pattern matching.
//
// Classification order:
//  1. Check if error is explicitly wrapped as TransientError or FatalError
//  2. Check if error is a known FATAL sentinel error
//  3. Check if error message matches FATAL patterns
//  4. Check if error message matches TRANSIENT patterns
//  5. Default to FATAL for unknown errors (fail-safe: don't retry unknowns)
//
// Returns ErrorCategoryFatal or ErrorCategoryTransient. Returns "" when err is nil.
func ClassifyError(err error) ErrorCategory {
	if err == nil {
		return ""
	}

	// Check for explicit wrapper types first (highest priority)
	var transientErr *TransientError
	if errors.As(err, &transientErr) {
		return ErrorCategoryTransient
	}

	var fatalErr *FatalError
	if errors.As(err, &fatalErr) {
		return ErrorCategoryFatal
	}

	// Check sentinel errors using errors.Is (supports wrapped errors)
	for _, sentinelErr := range fatalErrors {
		if errors.Is(err, sentinelErr) {
			return ErrorCategoryFatal
		}
	}

	// Pattern-based classification on error message
	errStr := strings.ToLower(err.Error())

	// Check for FATAL patterns first (business rule violations take precedence)
	for _, pattern := range fatalPatterns {
		if strings.Contains(errStr, pattern) {
			return ErrorCategoryFatal
		}
	}

	// Check for TRANSIENT patterns
	for _, pattern := range transientPatterns {
		if strings.Contains(errStr, pattern) {
			return ErrorCategoryTransient
		}
	}

	// Default to FATAL for unknown errors
	// Rationale: Unknown errors are more likely to be programming errors or
	// permanent conditions. Retrying them would waste resources and delay
	// compensation. Better to fail fast and let operators investigate.
	return ErrorCategoryFatal
}

// ClassifyErrorString returns the string representation of the error category.
// Convenience function that wraps ClassifyError.
func ClassifyErrorString(err error) string {
	return string(ClassifyError(err))
}

// IsFatalError returns true if the error is classified as FATAL.
// Returns false for nil errors.
func IsFatalError(err error) bool {
	return err != nil && ClassifyError(err) == ErrorCategoryFatal
}

// IsTransientError returns true if the error is classified as TRANSIENT.
// Returns false for nil errors.
func IsTransientError(err error) bool {
	return err != nil && ClassifyError(err) == ErrorCategoryTransient
}

// FatalError wraps an error to be classified as FATAL.
// Use this to explicitly mark an error as non-retryable.
type FatalError struct {
	Err error
}

func (e *FatalError) Error() string {
	if e.Err == nil {
		return "fatal error"
	}
	return e.Err.Error()
}

func (e *FatalError) Unwrap() error {
	return e.Err
}

// Is implements errors.Is for FatalError.
// Any FatalError is considered a business rule violation.
func (e *FatalError) Is(target error) bool {
	return errors.Is(target, ErrBusinessRuleViolation)
}

// NewFatalError wraps an error as a FATAL error.
func NewFatalError(err error) error {
	return &FatalError{Err: err}
}

// TransientError wraps an error to be classified as TRANSIENT.
// Use this to explicitly mark an error as retryable.
type TransientError struct {
	Err error
}

func (e *TransientError) Error() string {
	if e.Err == nil {
		return "transient error"
	}
	return e.Err.Error()
}

func (e *TransientError) Unwrap() error {
	return e.Err
}

// NewTransientError wraps an error as a TRANSIENT error.
func NewTransientError(err error) error {
	return &TransientError{Err: err}
}

// GetErrorCategory returns the error category from a SagaStepResult.
// Returns the ErrorCategory enum or empty string if not set.
func (r *SagaStepResult) GetErrorCategory() ErrorCategory {
	if r.ErrorCategory == nil {
		return ""
	}
	return ErrorCategory(*r.ErrorCategory)
}

// IsFatal returns true if this step result has a FATAL error.
func (r *SagaStepResult) IsFatal() bool {
	return r.GetErrorCategory() == ErrorCategoryFatal
}

// IsTransient returns true if this step result has a TRANSIENT error.
func (r *SagaStepResult) IsTransient() bool {
	return r.GetErrorCategory() == ErrorCategoryTransient
}

// SetErrorCategory sets the error category on a SagaStepResult.
func (r *SagaStepResult) SetErrorCategory(category ErrorCategory) {
	cat := string(category)
	r.ErrorCategory = &cat
}
