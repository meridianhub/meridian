// Package domain contains the Instruction aggregate root and related domain logic
// for orchestrating instruction delivery workflows.
package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Constructor Tests ---

func TestNewInstruction_Success(t *testing.T) {
	tenantID := uuid.New()
	payload := map[string]any{"key": "value"}

	instr, err := NewInstruction(tenantID, "DISPATCH_ORDER", "conn-123", payload)

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, instr.ID)
	assert.Equal(t, tenantID, instr.TenantID)
	assert.Equal(t, "DISPATCH_ORDER", instr.InstructionType)
	assert.Equal(t, "conn-123", instr.ProviderConnectionID)
	assert.Equal(t, payload, instr.Payload)
	assert.Equal(t, PriorityNormal, instr.Priority)
	assert.Equal(t, InstructionStatusPending, instr.Status)
	assert.Equal(t, 3, instr.MaxAttempts)
	assert.Equal(t, 0, instr.AttemptCount)
	assert.Empty(t, instr.Attempts)
	assert.NotZero(t, instr.CreatedAt)
	assert.NotZero(t, instr.UpdatedAt)
}

func TestNewInstruction_MissingInstructionType_Fails(t *testing.T) {
	tenantID := uuid.New()
	payload := map[string]any{"key": "value"}

	_, err := NewInstruction(tenantID, "", "conn-123", payload)

	assert.ErrorIs(t, err, ErrMissingInstructionType)
}

func TestNewInstruction_MissingProviderConnectionID_Fails(t *testing.T) {
	tenantID := uuid.New()
	payload := map[string]any{"key": "value"}

	_, err := NewInstruction(tenantID, "DISPATCH_ORDER", "", payload)

	assert.ErrorIs(t, err, ErrMissingProviderConnectionID)
}

func TestNewInstruction_NilTenantID_Fails(t *testing.T) {
	payload := map[string]any{"key": "value"}

	_, err := NewInstruction(uuid.Nil, "DISPATCH_ORDER", "conn-123", payload)

	assert.ErrorIs(t, err, ErrMissingTenantID)
}

func TestNewInstruction_NilPayload_Fails(t *testing.T) {
	tenantID := uuid.New()

	_, err := NewInstruction(tenantID, "DISPATCH_ORDER", "conn-123", nil)

	assert.ErrorIs(t, err, ErrMissingPayload)
}

func TestNewInstruction_WithOptions(t *testing.T) {
	tenantID := uuid.New()
	payload := map[string]any{"key": "value"}
	scheduledAt := time.Now().Add(time.Hour)
	expiresAt := time.Now().Add(24 * time.Hour)
	meta := map[string]string{"env": "prod"}

	instr, err := NewInstruction(
		tenantID, "DISPATCH_ORDER", "conn-123", payload,
		WithPriority(PriorityHigh),
		WithScheduledAt(scheduledAt),
		WithExpiresAt(expiresAt),
		WithMetadata(meta),
		WithCorrelationID("corr-001"),
		WithCausationID("cause-001"),
		WithMaxAttempts(5),
	)

	require.NoError(t, err)
	assert.Equal(t, PriorityHigh, instr.Priority)
	assert.Equal(t, meta, instr.Metadata)
	assert.Equal(t, "corr-001", instr.CorrelationID)
	assert.Equal(t, "cause-001", instr.CausationID)
	assert.Equal(t, 5, instr.MaxAttempts)
	assert.NotNil(t, instr.ScheduledAt)
	assert.NotNil(t, instr.ExpiresAt)
}

// --- State Machine: MarkDispatching ---

func TestInstruction_MarkDispatching_FromPending(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusPending)

	err := instr.MarkDispatching()

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusDispatching, instr.Status)
	assert.NotNil(t, instr.DispatchedAt)
}

func TestInstruction_MarkDispatching_FromRetrying(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusRetrying)

	err := instr.MarkDispatching()

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusDispatching, instr.Status)
}

func TestInstruction_MarkDispatching_FromDelivered_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDelivered)

	err := instr.MarkDispatching()

	assert.ErrorIs(t, err, ErrInvalidInstructionTransition)
	assert.Equal(t, InstructionStatusDelivered, instr.Status)
}

func TestInstruction_MarkDispatching_FromFailed_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusFailed)

	err := instr.MarkDispatching()

	assert.ErrorIs(t, err, ErrInvalidInstructionTransition)
}

func TestInstruction_MarkDispatching_FromAcknowledged_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusAcknowledged)

	err := instr.MarkDispatching()

	assert.ErrorIs(t, err, ErrInvalidInstructionTransition)
}

// --- State Machine: MarkDelivered ---

func TestInstruction_MarkDelivered_FromDispatching(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDispatching)

	err := instr.MarkDelivered()

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusDelivered, instr.Status)
}

func TestInstruction_MarkDelivered_FromPending_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusPending)

	err := instr.MarkDelivered()

	assert.ErrorIs(t, err, ErrInvalidInstructionTransition)
}

func TestInstruction_MarkDelivered_FromFailed_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusFailed)

	err := instr.MarkDelivered()

	assert.ErrorIs(t, err, ErrInvalidInstructionTransition)
}

// --- State Machine: MarkAcknowledged ---

func TestInstruction_MarkAcknowledged_FromDelivered(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDelivered)

	err := instr.MarkAcknowledged()

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusAcknowledged, instr.Status)
	assert.NotNil(t, instr.CompletedAt)
}

func TestInstruction_MarkAcknowledged_FromPending_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusPending)

	err := instr.MarkAcknowledged()

	assert.ErrorIs(t, err, ErrInvalidInstructionTransition)
}

func TestInstruction_MarkAcknowledged_FromDispatching_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDispatching)

	err := instr.MarkAcknowledged()

	assert.ErrorIs(t, err, ErrInvalidInstructionTransition)
}

// --- State Machine: MarkRetrying ---

func TestInstruction_MarkRetrying_FromDispatching(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDispatching)
	instr.MaxAttempts = 3
	instr.AttemptCount = 1

	err := instr.MarkRetrying("timeout", "TIMEOUT")

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusRetrying, instr.Status)
	assert.Equal(t, 1, len(instr.Attempts))
	assert.Equal(t, "timeout", instr.Attempts[0].FailureReason)
}

func TestInstruction_MarkRetrying_ExhaustedAttempts_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDispatching)
	instr.MaxAttempts = 3
	instr.AttemptCount = 3

	err := instr.MarkRetrying("timeout", "TIMEOUT")

	assert.ErrorIs(t, err, ErrMaxAttemptsExhausted)
}

func TestInstruction_MarkRetrying_MissingReason_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDispatching)
	instr.MaxAttempts = 3
	instr.AttemptCount = 0

	err := instr.MarkRetrying("", "TIMEOUT")

	assert.ErrorIs(t, err, ErrMissingFailureReason)
}

func TestInstruction_MarkRetrying_FromPending_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusPending)

	err := instr.MarkRetrying("reason", "CODE")

	assert.ErrorIs(t, err, ErrInvalidInstructionTransition)
}

// --- State Machine: MarkFailed ---

func TestInstruction_MarkFailed_FromDispatching(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDispatching)

	err := instr.MarkFailed("provider rejected", "REJECTED")

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusFailed, instr.Status)
	assert.NotNil(t, instr.CompletedAt)
	assert.Equal(t, "provider rejected", instr.FailureReason)
}

func TestInstruction_MarkFailed_FromRetrying(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusRetrying)

	err := instr.MarkFailed("max retries exceeded", "MAX_RETRIES")

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusFailed, instr.Status)
}

func TestInstruction_MarkFailed_Idempotent(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDispatching)

	err := instr.MarkFailed("first failure", "FIRST")
	require.NoError(t, err)

	err = instr.MarkFailed("second failure", "SECOND")
	assert.NoError(t, err)
	// Original reason preserved
	assert.Equal(t, "first failure", instr.FailureReason)
}

func TestInstruction_MarkFailed_MissingReason_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDispatching)

	err := instr.MarkFailed("", "CODE")

	assert.ErrorIs(t, err, ErrMissingFailureReason)
}

func TestInstruction_MarkFailed_FromAcknowledged_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusAcknowledged)

	err := instr.MarkFailed("cannot fail", "CODE")

	assert.ErrorIs(t, err, ErrInstructionTerminal)
}

// --- State Machine: MarkExpired ---

func TestInstruction_MarkExpired_FromPending(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusPending)

	err := instr.MarkExpired()

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusExpired, instr.Status)
	assert.NotNil(t, instr.CompletedAt)
}

func TestInstruction_MarkExpired_FromRetrying(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusRetrying)

	err := instr.MarkExpired()

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusExpired, instr.Status)
}

func TestInstruction_MarkExpired_Idempotent(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusPending)

	err := instr.MarkExpired()
	require.NoError(t, err)

	err = instr.MarkExpired()
	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusExpired, instr.Status)
}

func TestInstruction_MarkExpired_FromAcknowledged_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusAcknowledged)

	err := instr.MarkExpired()

	assert.ErrorIs(t, err, ErrInstructionTerminal)
}

// --- State Machine: Cancel ---

func TestInstruction_Cancel_FromPending(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusPending)

	err := instr.Cancel()

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusCancelled, instr.Status)
	assert.NotNil(t, instr.CompletedAt)
}

func TestInstruction_Cancel_FromRetrying(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusRetrying)

	err := instr.Cancel()

	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusCancelled, instr.Status)
}

func TestInstruction_Cancel_Idempotent(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusPending)

	err := instr.Cancel()
	require.NoError(t, err)

	err = instr.Cancel()
	assert.NoError(t, err)
	assert.Equal(t, InstructionStatusCancelled, instr.Status)
}

func TestInstruction_Cancel_FromDispatching_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDispatching)

	err := instr.Cancel()

	assert.ErrorIs(t, err, ErrInstructionNotCancellable)
}

func TestInstruction_Cancel_FromDelivered_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusDelivered)

	err := instr.Cancel()

	assert.ErrorIs(t, err, ErrInstructionNotCancellable)
}

func TestInstruction_Cancel_FromAcknowledged_Fails(t *testing.T) {
	instr := createTestInstruction(t, InstructionStatusAcknowledged)

	err := instr.Cancel()

	assert.ErrorIs(t, err, ErrInstructionNotCancellable)
}

// --- Helper Methods ---

func TestInstruction_IsTerminal(t *testing.T) {
	tests := []struct {
		status   InstructionStatus
		expected bool
	}{
		{InstructionStatusPending, false},
		{InstructionStatusDispatching, false},
		{InstructionStatusDelivered, false},
		{InstructionStatusRetrying, false},
		{InstructionStatusAcknowledged, true},
		{InstructionStatusFailed, true},
		{InstructionStatusExpired, true},
		{InstructionStatusCancelled, true},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			instr := createTestInstruction(t, tc.status)
			assert.Equal(t, tc.expected, instr.IsTerminal())
		})
	}
}

func TestInstruction_CanCancel(t *testing.T) {
	tests := []struct {
		status   InstructionStatus
		expected bool
	}{
		{InstructionStatusPending, true},
		{InstructionStatusDispatching, false},
		{InstructionStatusDelivered, false},
		{InstructionStatusRetrying, true},
		{InstructionStatusAcknowledged, false},
		{InstructionStatusFailed, false},
		{InstructionStatusExpired, false},
		{InstructionStatusCancelled, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			instr := createTestInstruction(t, tc.status)
			assert.Equal(t, tc.expected, instr.CanCancel())
		})
	}
}

func TestInstruction_CanRetry(t *testing.T) {
	t.Run("dispatching with attempts remaining", func(t *testing.T) {
		instr := createTestInstruction(t, InstructionStatusDispatching)
		instr.MaxAttempts = 3
		instr.AttemptCount = 1
		assert.True(t, instr.CanRetry())
	})

	t.Run("dispatching with exhausted attempts", func(t *testing.T) {
		instr := createTestInstruction(t, InstructionStatusDispatching)
		instr.MaxAttempts = 3
		instr.AttemptCount = 3
		assert.False(t, instr.CanRetry())
	})

	t.Run("pending cannot retry", func(t *testing.T) {
		instr := createTestInstruction(t, InstructionStatusPending)
		instr.MaxAttempts = 3
		instr.AttemptCount = 0
		assert.False(t, instr.CanRetry())
	})

	t.Run("failed cannot retry", func(t *testing.T) {
		instr := createTestInstruction(t, InstructionStatusFailed)
		instr.MaxAttempts = 3
		instr.AttemptCount = 1
		assert.False(t, instr.CanRetry())
	})
}

func TestInstruction_NeedsDispatch(t *testing.T) {
	t.Run("pending with no schedule returns true", func(t *testing.T) {
		instr := createTestInstruction(t, InstructionStatusPending)
		instr.ScheduledAt = nil
		assert.True(t, instr.NeedsDispatch())
	})

	t.Run("pending with past schedule returns true", func(t *testing.T) {
		instr := createTestInstruction(t, InstructionStatusPending)
		past := time.Now().Add(-time.Hour)
		instr.ScheduledAt = &past
		assert.True(t, instr.NeedsDispatch())
	})

	t.Run("pending with future schedule returns false", func(t *testing.T) {
		instr := createTestInstruction(t, InstructionStatusPending)
		future := time.Now().Add(time.Hour)
		instr.ScheduledAt = &future
		assert.False(t, instr.NeedsDispatch())
	})

	t.Run("retrying needs dispatch", func(t *testing.T) {
		instr := createTestInstruction(t, InstructionStatusRetrying)
		instr.ScheduledAt = nil
		assert.True(t, instr.NeedsDispatch())
	})

	t.Run("dispatching does not need dispatch", func(t *testing.T) {
		instr := createTestInstruction(t, InstructionStatusDispatching)
		assert.False(t, instr.NeedsDispatch())
	})

	t.Run("terminal does not need dispatch", func(t *testing.T) {
		for _, status := range []InstructionStatus{
			InstructionStatusAcknowledged,
			InstructionStatusFailed,
			InstructionStatusExpired,
			InstructionStatusCancelled,
		} {
			instr := createTestInstruction(t, status)
			assert.False(t, instr.NeedsDispatch(), "expected NeedsDispatch=false for status %s", status)
		}
	})
}

// --- Happy Path End-to-End ---

func TestInstruction_HappyPath(t *testing.T) {
	tenantID := uuid.New()
	payload := map[string]any{"order_id": "ord-001"}

	instr, err := NewInstruction(tenantID, "DISPATCH_ORDER", "conn-123", payload)
	require.NoError(t, err)
	assert.Equal(t, InstructionStatusPending, instr.Status)

	err = instr.MarkDispatching()
	require.NoError(t, err)
	assert.Equal(t, InstructionStatusDispatching, instr.Status)

	err = instr.MarkDelivered()
	require.NoError(t, err)
	assert.Equal(t, InstructionStatusDelivered, instr.Status)

	err = instr.MarkAcknowledged()
	require.NoError(t, err)
	assert.Equal(t, InstructionStatusAcknowledged, instr.Status)
	assert.True(t, instr.IsTerminal())
	assert.NotNil(t, instr.CompletedAt)
}

func TestInstruction_RetryPath(t *testing.T) {
	tenantID := uuid.New()
	payload := map[string]any{"order_id": "ord-002"}

	instr, err := NewInstruction(tenantID, "DISPATCH_ORDER", "conn-123", payload,
		WithMaxAttempts(3),
	)
	require.NoError(t, err)

	// First dispatch attempt
	err = instr.MarkDispatching()
	require.NoError(t, err)
	instr.AttemptCount++

	// Retry after failure
	err = instr.MarkRetrying("timeout", "TIMEOUT")
	require.NoError(t, err)
	assert.Equal(t, InstructionStatusRetrying, instr.Status)
	assert.Equal(t, 1, len(instr.Attempts))

	// Second dispatch attempt
	err = instr.MarkDispatching()
	require.NoError(t, err)
	instr.AttemptCount++

	// Success
	err = instr.MarkDelivered()
	require.NoError(t, err)

	err = instr.MarkAcknowledged()
	require.NoError(t, err)
	assert.Equal(t, InstructionStatusAcknowledged, instr.Status)
	assert.True(t, instr.IsTerminal())
}

// --- helpers ---

func createTestInstruction(t *testing.T, status InstructionStatus) *Instruction {
	t.Helper()
	now := time.Now()
	return &Instruction{
		ID:                   uuid.New(),
		TenantID:             uuid.New(),
		InstructionType:      "TEST_TYPE",
		ProviderConnectionID: "conn-test",
		Payload:              map[string]any{"key": "val"},
		Priority:             PriorityNormal,
		Status:               status,
		MaxAttempts:          3,
		AttemptCount:         0,
		Attempts:             []InstructionAttempt{},
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}
