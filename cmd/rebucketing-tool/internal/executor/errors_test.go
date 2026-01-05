package executor

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test sentinel errors for error wrapping tests
var (
	errTestDatabaseConnection  = errors.New("database connection lost")
	errTestConstraintViolation = errors.New("unique constraint violation")
)

func TestTransactionRollbackError(t *testing.T) {
	cause := errTestDatabaseConnection
	err := &TransactionRollbackError{
		Cause:              cause,
		PositionsProcessed: 1500,
		BatchNumber:        4,
	}

	t.Run("Error message includes cause", func(t *testing.T) {
		assert.Contains(t, err.Error(), "transaction rolled back")
		assert.Contains(t, err.Error(), "database connection lost")
	})

	t.Run("Unwrap returns cause", func(t *testing.T) {
		assert.Equal(t, cause, err.Unwrap())
	})

	t.Run("Is matches ErrTransactionRollback", func(t *testing.T) {
		assert.True(t, errors.Is(err, ErrTransactionRollback))
	})

	t.Run("Is does not match other errors", func(t *testing.T) {
		assert.False(t, errors.Is(err, ErrAuditLogWrite))
	})

	t.Run("Fields are accessible", func(t *testing.T) {
		assert.Equal(t, int64(1500), err.PositionsProcessed)
		assert.Equal(t, 4, err.BatchNumber)
	})
}

func TestAuditLogWriteError(t *testing.T) {
	cause := errTestConstraintViolation
	err := &AuditLogWriteError{
		Cause:      cause,
		PositionID: "pos-123",
		Operation:  "SOFT_DELETE",
	}

	t.Run("Error message includes context", func(t *testing.T) {
		assert.Contains(t, err.Error(), "audit log write failed")
		assert.Contains(t, err.Error(), "pos-123")
		assert.Contains(t, err.Error(), "unique constraint violation")
	})

	t.Run("Unwrap returns cause", func(t *testing.T) {
		assert.Equal(t, cause, err.Unwrap())
	})

	t.Run("Is matches ErrAuditLogWrite", func(t *testing.T) {
		assert.True(t, errors.Is(err, ErrAuditLogWrite))
	})

	t.Run("Is does not match other errors", func(t *testing.T) {
		assert.False(t, errors.Is(err, ErrTransactionRollback))
	})

	t.Run("Fields are accessible", func(t *testing.T) {
		assert.Equal(t, "pos-123", err.PositionID)
		assert.Equal(t, "SOFT_DELETE", err.Operation)
	})
}

func TestUnauthorizedError(t *testing.T) {
	err := &UnauthorizedError{
		UserID:       "user-456",
		RequiredRole: "admin",
		ActualRoles:  []string{"operator", "auditor"},
	}

	t.Run("Error message includes user and role", func(t *testing.T) {
		assert.Contains(t, err.Error(), "user-456")
		assert.Contains(t, err.Error(), "admin")
	})

	t.Run("Is matches ErrUnauthorized", func(t *testing.T) {
		assert.True(t, errors.Is(err, ErrUnauthorized))
	})

	t.Run("Is does not match other errors", func(t *testing.T) {
		assert.False(t, errors.Is(err, ErrTransactionRollback))
	})

	t.Run("Fields are accessible", func(t *testing.T) {
		assert.Equal(t, "user-456", err.UserID)
		assert.Equal(t, "admin", err.RequiredRole)
		assert.Equal(t, []string{"operator", "auditor"}, err.ActualRoles)
	})
}

func TestErrorConstants(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrUnauthorized", ErrUnauthorized},
		{"ErrMissingClaims", ErrMissingClaims},
		{"ErrTransactionRollback", ErrTransactionRollback},
		{"ErrAuditLogWrite", ErrAuditLogWrite},
		{"ErrPositionSoftDelete", ErrPositionSoftDelete},
		{"ErrPositionInsert", ErrPositionInsert},
		{"ErrEmptyPlan", ErrEmptyPlan},
		{"ErrInvalidBatchSize", ErrInvalidBatchSize},
		{"ErrBatchSizeTooLarge", ErrBatchSizeTooLarge},
		{"ErrNilPool", ErrNilPool},
		{"ErrNilPlan", ErrNilPlan},
		{"ErrMissingInstrumentVersion", ErrMissingInstrumentVersion},
		{"ErrInvalidBucketMapping", ErrInvalidBucketMapping},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotNil(t, tt.err)
			assert.NotEmpty(t, tt.err.Error())
		})
	}
}
