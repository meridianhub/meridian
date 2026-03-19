package saga

// Tests for error classification require dynamic errors to test pattern matching.
// This is intentional and cannot be avoided for comprehensive testing.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test sentinel errors.
var errTestNetworkTimeout = errors.New("network timeout")

// TestClassifyError_SentinelErrors verifies that known sentinel errors are classified correctly.
func TestClassifyError_SentinelErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ErrorCategory
	}{
		// FATAL sentinel errors
		{"ErrInsufficientFunds", ErrInsufficientFunds, ErrorCategoryFatal},
		{"ErrInsufficientBalance", ErrInsufficientBalance, ErrorCategoryFatal},
		{"ErrAccountClosed", ErrAccountClosed, ErrorCategoryFatal},
		{"ErrAccountFrozen", ErrAccountFrozen, ErrorCategoryFatal},
		{"ErrInvalidAmount", ErrInvalidAmount, ErrorCategoryFatal},
		{"ErrDuplicateTransaction", ErrDuplicateTransaction, ErrorCategoryFatal},
		{"ErrBusinessRuleViolation", ErrBusinessRuleViolation, ErrorCategoryFatal},
		{"ErrValidationFailed", ErrValidationFailed, ErrorCategoryFatal},
		{"ErrNotFound", ErrNotFound, ErrorCategoryFatal},
		{"ErrUnauthorized", ErrUnauthorized, ErrorCategoryFatal},
		{"ErrForbidden", ErrForbidden, ErrorCategoryFatal},
		// Handler validation errors from handlers.go
		{"ErrMissingParam", ErrMissingParam, ErrorCategoryFatal},
		{"ErrInvalidParamType", ErrInvalidParamType, ErrorCategoryFatal},
		{"ErrInvalidDirection", ErrInvalidDirection, ErrorCategoryFatal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ClassifyError(tc.err)
			assert.Equal(t, tc.expected, result, "Error %v should be classified as %s", tc.err, tc.expected)
		})
	}
}

// TestClassifyError_WrappedSentinelErrors verifies that wrapped sentinel errors are classified correctly.
func TestClassifyError_WrappedSentinelErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ErrorCategory
	}{
		{
			name:     "wrapped ErrInsufficientFunds",
			err:      fmt.Errorf("failed to process payment: %w", ErrInsufficientFunds),
			expected: ErrorCategoryFatal,
		},
		{
			name:     "double-wrapped ErrAccountClosed",
			err:      fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrAccountClosed)),
			expected: ErrorCategoryFatal,
		},
		{
			name:     "wrapped ErrValidationFailed",
			err:      fmt.Errorf("input validation: %w", ErrValidationFailed),
			expected: ErrorCategoryFatal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ClassifyError(tc.err)
			assert.Equal(t, tc.expected, result, "Wrapped error %v should be classified as %s", tc.err, tc.expected)
		})
	}
}

// TestClassifyError_PatternMatching verifies pattern-based classification.
func TestClassifyError_PatternMatching(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected ErrorCategory
	}{
		// TRANSIENT patterns
		{"network timeout", "network timeout after 30s", ErrorCategoryTransient},
		{"deadline exceeded", "context deadline exceeded", ErrorCategoryTransient},
		{"connection refused", "dial tcp: connection refused", ErrorCategoryTransient},
		{"connection reset", "read: connection reset by peer", ErrorCategoryTransient},
		{"temporarily unavailable", "service temporarily unavailable", ErrorCategoryTransient},
		{"retry", "please retry later", ErrorCategoryTransient},
		{"rate limit", "rate limit exceeded", ErrorCategoryTransient},
		{"deadlock", "database deadlock detected", ErrorCategoryTransient},
		{"lock timeout", "lock wait timeout exceeded", ErrorCategoryTransient},
		{"serialization failure", "could not serialize access", ErrorCategoryTransient},

		// FATAL patterns
		{"insufficient funds", "account has insufficient funds", ErrorCategoryFatal},
		{"validation error", "validation error: email format invalid", ErrorCategoryFatal},
		{"constraint violation", "unique constraint violation on key 'id'", ErrorCategoryFatal},
		{"foreign key constraint", "foreign key constraint violation", ErrorCategoryFatal},
		{"not found", "customer not found", ErrorCategoryFatal},
		{"does not exist", "resource does not exist", ErrorCategoryFatal},
		{"unauthorized", "unauthorized access", ErrorCategoryFatal},
		{"forbidden", "forbidden operation", ErrorCategoryFatal},
		{"permission denied", "permission denied for user", ErrorCategoryFatal},
		{"duplicate", "duplicate entry for key 'txn_id'", ErrorCategoryFatal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := errors.New(tc.errMsg)
			result := ClassifyError(err)
			assert.Equal(t, tc.expected, result, "Error message '%s' should be classified as %s", tc.errMsg, tc.expected)
		})
	}
}

// TestClassifyError_CaseInsensitive verifies pattern matching is case-insensitive.
func TestClassifyError_CaseInsensitive(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected ErrorCategory
	}{
		{"uppercase TIMEOUT", "NETWORK TIMEOUT", ErrorCategoryTransient},
		{"mixed case Timeout", "Connection Timeout Occurred", ErrorCategoryTransient},
		{"uppercase NOT FOUND", "RESOURCE NOT FOUND", ErrorCategoryFatal},
		{"mixed case Insufficient Funds", "Account Has Insufficient Funds", ErrorCategoryFatal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := errors.New(tc.errMsg)
			result := ClassifyError(err)
			assert.Equal(t, tc.expected, result, "Case-insensitive pattern matching failed for '%s'", tc.errMsg)
		})
	}
}

// TestClassifyError_NilError verifies nil error returns empty category.
func TestClassifyError_NilError(t *testing.T) {
	result := ClassifyError(nil)
	assert.Equal(t, ErrorCategory(""), result, "nil error should return empty category")

	// Also verify helper functions return false for nil
	assert.False(t, IsFatalError(nil), "IsFatalError(nil) should return false")
	assert.False(t, IsTransientError(nil), "IsTransientError(nil) should return false")
}

// TestClassifyError_UnknownError verifies unknown errors default to FATAL.
func TestClassifyError_UnknownError(t *testing.T) {
	unknownErr := errors.New("xyz123 strange error")
	result := ClassifyError(unknownErr)
	assert.Equal(t, ErrorCategoryFatal, result, "Unknown errors should default to FATAL for fail-safe behavior")
}

// TestClassifyError_ContextErrors verifies context errors are TRANSIENT.
func TestClassifyError_ContextErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ErrorCategory
	}{
		{"context.DeadlineExceeded", context.DeadlineExceeded, ErrorCategoryTransient},
		{"context.Canceled", context.Canceled, ErrorCategoryTransient},
		{"wrapped DeadlineExceeded", fmt.Errorf("timeout: %w", context.DeadlineExceeded), ErrorCategoryTransient},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ClassifyError(tc.err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestIsFatalError verifies the IsFatalError helper.
func TestIsFatalError(t *testing.T) {
	assert.True(t, IsFatalError(ErrInsufficientFunds))
	assert.True(t, IsFatalError(ErrValidationFailed))
	assert.False(t, IsFatalError(errTestNetworkTimeout))
	assert.False(t, IsFatalError(nil))
}

// TestIsTransientError verifies the IsTransientError helper.
func TestIsTransientError(t *testing.T) {
	assert.True(t, IsTransientError(errTestNetworkTimeout))
	assert.False(t, IsTransientError(nil)) // nil is not a transient error
	assert.False(t, IsTransientError(ErrInsufficientFunds))
}

// TestFatalErrorWrapper verifies NewFatalError creates a FATAL-classified error.
func TestFatalErrorWrapper(t *testing.T) {
	originalErr := errors.New("some error")
	fatalErr := NewFatalError(originalErr)

	// Should be classified as FATAL (via errors.Is(ErrBusinessRuleViolation))
	assert.True(t, errors.Is(fatalErr, ErrBusinessRuleViolation), "FatalError should match ErrBusinessRuleViolation")
	assert.True(t, IsFatalError(fatalErr), "FatalError should be classified as FATAL")

	// Should preserve original error
	assert.True(t, errors.Is(fatalErr, originalErr), "Should unwrap to original error")
	assert.Contains(t, fatalErr.Error(), originalErr.Error())
}

// TestTransientErrorWrapper verifies NewTransientError creates a TRANSIENT-classified error.
func TestTransientErrorWrapper(t *testing.T) {
	originalErr := errors.New("some error")
	transientErr := NewTransientError(originalErr)

	// Should be classified as TRANSIENT (contains "retryable" pattern in type name)
	assert.True(t, IsTransientError(transientErr), "TransientError should be classified as TRANSIENT")

	// Should preserve original error
	assert.True(t, errors.Is(transientErr, originalErr), "Should unwrap to original error")
	assert.Contains(t, transientErr.Error(), originalErr.Error())
}

// TestSagaStepResult_ErrorCategoryHelpers verifies helper methods on SagaStepResult.
func TestSagaStepResult_ErrorCategoryHelpers(t *testing.T) {
	t.Run("GetErrorCategory returns enum", func(t *testing.T) {
		fatal := string(ErrorCategoryFatal)
		result := &SagaStepResult{ErrorCategory: &fatal}
		assert.Equal(t, ErrorCategoryFatal, result.GetErrorCategory())
	})

	t.Run("GetErrorCategory returns empty for nil", func(t *testing.T) {
		result := &SagaStepResult{ErrorCategory: nil}
		assert.Equal(t, ErrorCategory(""), result.GetErrorCategory())
	})

	t.Run("IsFatal returns true for FATAL", func(t *testing.T) {
		fatal := string(ErrorCategoryFatal)
		result := &SagaStepResult{ErrorCategory: &fatal}
		assert.True(t, result.IsFatal())
		assert.False(t, result.IsTransient())
	})

	t.Run("IsTransient returns true for TRANSIENT", func(t *testing.T) {
		transient := string(ErrorCategoryTransient)
		result := &SagaStepResult{ErrorCategory: &transient}
		assert.True(t, result.IsTransient())
		assert.False(t, result.IsFatal())
	})

	t.Run("SetErrorCategory sets value", func(t *testing.T) {
		result := &SagaStepResult{}
		result.SetErrorCategory(ErrorCategoryFatal)
		assert.NotNil(t, result.ErrorCategory)
		assert.Equal(t, string(ErrorCategoryFatal), *result.ErrorCategory)
	})
}

// TestClassifyErrorString verifies the string helper.
func TestClassifyErrorString(t *testing.T) {
	assert.Equal(t, "FATAL", ClassifyErrorString(ErrInsufficientFunds))
	assert.Equal(t, "TRANSIENT", ClassifyErrorString(errTestNetworkTimeout))
}

// TestClassifyError_FatalPatternsPrecedence verifies FATAL patterns take precedence over TRANSIENT.
func TestClassifyError_FatalPatternsPrecedence(t *testing.T) {
	// An error message that contains both FATAL and TRANSIENT patterns
	// FATAL should take precedence because we check it first
	err := errors.New("timeout while checking insufficient funds")
	result := ClassifyError(err)
	assert.Equal(t, ErrorCategoryFatal, result, "FATAL pattern 'insufficient funds' should take precedence over TRANSIENT pattern 'timeout'")
}

// TestClassifyError_DatabaseErrors verifies database-specific error classification.
func TestClassifyError_DatabaseErrors(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected ErrorCategory
	}{
		// TRANSIENT database errors (can retry)
		{"deadlock", "ERROR: deadlock detected", ErrorCategoryTransient},
		{"lock timeout", "ERROR: lock wait timeout exceeded", ErrorCategoryTransient},
		{"serialization failure", "ERROR: could not serialize access due to concurrent update", ErrorCategoryTransient},

		// FATAL database errors (schema/constraint issues)
		{"unique constraint", "ERROR: duplicate key value violates unique constraint", ErrorCategoryFatal},
		{"foreign key", "ERROR: insert or update on table violates foreign key constraint", ErrorCategoryFatal},
		{"check constraint", "ERROR: new row for relation violates check constraint", ErrorCategoryFatal},
		{"null constraint", "ERROR: null value in column violates not-null constraint", ErrorCategoryFatal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := errors.New(tc.errMsg)
			result := ClassifyError(err)
			assert.Equal(t, tc.expected, result, "Database error '%s' should be classified as %s", tc.errMsg, tc.expected)
		})
	}
}

// TestClassifyError_HTTPErrors verifies HTTP-related error classification.
func TestClassifyError_HTTPErrors(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected ErrorCategory
	}{
		// TRANSIENT HTTP errors
		{"503 service unavailable", "HTTP 503: Service Unavailable", ErrorCategoryTransient},
		{"429 too many requests", "HTTP 429: Too Many Requests", ErrorCategoryTransient},
		{"connection reset", "http: server closed connection: connection reset", ErrorCategoryTransient},

		// FATAL HTTP errors
		{"404 not found", "HTTP 404: Resource not found", ErrorCategoryFatal},
		{"401 unauthorized", "HTTP 401: Unauthorized", ErrorCategoryFatal},
		{"403 forbidden", "HTTP 403: Forbidden", ErrorCategoryFatal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := errors.New(tc.errMsg)
			result := ClassifyError(err)
			assert.Equal(t, tc.expected, result, "HTTP error '%s' should be classified as %s", tc.errMsg, tc.expected)
		})
	}
}

// TestClassifyError_SyscallErrors verifies that syscall error patterns are correctly classified as TRANSIENT.
// These patterns must be lowercase since ClassifyError normalizes error strings to lowercase.
func TestClassifyError_SyscallErrors(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected ErrorCategory
	}{
		// Unix syscall errors - should be TRANSIENT (retriable)
		{"EAGAIN uppercase", "syscall error: EAGAIN", ErrorCategoryTransient},
		{"eagain lowercase", "resource temporarily unavailable: eagain", ErrorCategoryTransient},
		{"ETIMEDOUT uppercase", "connection ETIMEDOUT after 30s", ErrorCategoryTransient},
		{"etimedout lowercase", "socket etimedout", ErrorCategoryTransient},
		{"ECONNRESET uppercase", "read tcp: ECONNRESET by peer", ErrorCategoryTransient},
		{"econnreset lowercase", "connection econnreset", ErrorCategoryTransient},
		{"ECONNREFUSED uppercase", "dial tcp: ECONNREFUSED", ErrorCategoryTransient},
		{"econnrefused lowercase", "econnrefused on port 5432", ErrorCategoryTransient},
		{"EOF uppercase", "unexpected EOF", ErrorCategoryTransient},
		{"eof lowercase", "eof reached", ErrorCategoryTransient},
		{"broken pipe", "write: broken pipe", ErrorCategoryTransient},
		{"i/o timeout", "read i/o timeout", ErrorCategoryTransient},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := errors.New(tc.errMsg)
			result := ClassifyError(err)
			assert.Equal(t, tc.expected, result, "Syscall error '%s' should be classified as %s", tc.errMsg, tc.expected)
		})
	}
}

// TestFatalErrorNilWrapping verifies FatalError handles nil correctly.
func TestFatalErrorNilWrapping(t *testing.T) {
	fatalErr := NewFatalError(nil)
	assert.Equal(t, "fatal error", fatalErr.Error())

	var fe *FatalError
	require.True(t, errors.As(fatalErr, &fe))
	assert.Nil(t, fe.Unwrap())
}

// TestTransientErrorNilWrapping verifies TransientError handles nil correctly.
func TestTransientErrorNilWrapping(t *testing.T) {
	transientErr := NewTransientError(nil)
	assert.Equal(t, "transient error", transientErr.Error())

	var te *TransientError
	require.True(t, errors.As(transientErr, &te))
	assert.Nil(t, te.Unwrap())
}
