package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountStatus_ValidTransitions(t *testing.T) {
	tests := []struct {
		name   string
		from   AccountStatus
		to     AccountStatus
		reason string
	}{
		{
			name:   "ACTIVE to SUSPENDED",
			from:   AccountStatusActive,
			to:     AccountStatusSuspended,
			reason: "Suspending active account should be allowed",
		},
		{
			name:   "SUSPENDED to ACTIVE",
			from:   AccountStatusSuspended,
			to:     AccountStatusActive,
			reason: "Reactivating suspended account should be allowed",
		},
		{
			name:   "ACTIVE to CLOSED",
			from:   AccountStatusActive,
			to:     AccountStatusClosed,
			reason: "Closing active account should be allowed",
		},
		{
			name:   "SUSPENDED to CLOSED",
			from:   AccountStatusSuspended,
			to:     AccountStatusClosed,
			reason: "Closing suspended account should be allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test CanTransitionTo method
			assert.True(t, tt.from.CanTransitionTo(tt.to), tt.reason)

			// Test ValidateTransition function
			err := ValidateTransition(tt.from, tt.to)
			assert.NoError(t, err, tt.reason)
		})
	}
}

func TestAccountStatus_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name   string
		from   AccountStatus
		to     AccountStatus
		reason string
	}{
		{
			name:   "CLOSED to ACTIVE",
			from:   AccountStatusClosed,
			to:     AccountStatusActive,
			reason: "Reactivating closed account should not be allowed",
		},
		{
			name:   "CLOSED to SUSPENDED",
			from:   AccountStatusClosed,
			to:     AccountStatusSuspended,
			reason: "Suspending closed account should not be allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test CanTransitionTo method
			assert.False(t, tt.from.CanTransitionTo(tt.to), tt.reason)

			// Test ValidateTransition function
			err := ValidateTransition(tt.from, tt.to)
			assert.Error(t, err, tt.reason)
			assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
		})
	}
}

func TestAccountStatus_TerminalState(t *testing.T) {
	// CLOSED is a terminal state - no transitions should be allowed from it
	closedStatus := AccountStatusClosed

	t.Run("cannot transition to ACTIVE", func(t *testing.T) {
		assert.False(t, closedStatus.CanTransitionTo(AccountStatusActive))
		err := ValidateTransition(closedStatus, AccountStatusActive)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
	})

	t.Run("cannot transition to SUSPENDED", func(t *testing.T) {
		assert.False(t, closedStatus.CanTransitionTo(AccountStatusSuspended))
		err := ValidateTransition(closedStatus, AccountStatusSuspended)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
	})

	t.Run("cannot transition to CLOSED (same state)", func(t *testing.T) {
		assert.False(t, closedStatus.CanTransitionTo(AccountStatusClosed))
		err := ValidateTransition(closedStatus, AccountStatusClosed)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
		assert.Contains(t, err.Error(), "source and target status are the same")
	})
}

func TestAccountStatus_SameStatusTransition(t *testing.T) {
	// Transitioning to the same status should be rejected
	tests := []struct {
		name   string
		status AccountStatus
	}{
		{"ACTIVE to ACTIVE", AccountStatusActive},
		{"SUSPENDED to SUSPENDED", AccountStatusSuspended},
		{"CLOSED to CLOSED", AccountStatusClosed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, tt.status.CanTransitionTo(tt.status))
			err := ValidateTransition(tt.status, tt.status)
			assert.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
			assert.Contains(t, err.Error(), "source and target status are the same")
		})
	}
}

func TestStatusChange_AuditFields(t *testing.T) {
	now := time.Now()

	statusChange := StatusChange{
		From:      AccountStatusActive,
		To:        AccountStatusSuspended,
		Reason:    "Suspicious activity detected",
		Timestamp: now,
		ChangedBy: "compliance-officer@example.com",
	}

	// Verify all fields are populated correctly
	require.Equal(t, AccountStatusActive, statusChange.From, "From field should be set")
	require.Equal(t, AccountStatusSuspended, statusChange.To, "To field should be set")
	require.Equal(t, "Suspicious activity detected", statusChange.Reason, "Reason field should be set")
	require.Equal(t, now, statusChange.Timestamp, "Timestamp field should be set")
	require.Equal(t, "compliance-officer@example.com", statusChange.ChangedBy, "ChangedBy field should be set")

	// Additional assertions to verify field values are not empty/zero
	assert.NotEmpty(t, string(statusChange.From), "From should not be empty")
	assert.NotEmpty(t, string(statusChange.To), "To should not be empty")
	assert.NotEmpty(t, statusChange.Reason, "Reason should not be empty")
	assert.False(t, statusChange.Timestamp.IsZero(), "Timestamp should not be zero")
	assert.NotEmpty(t, statusChange.ChangedBy, "ChangedBy should not be empty")
}

func TestStatusChange_MultipleTranitionsAuditTrail(t *testing.T) {
	// Simulate a series of status changes that would be recorded
	baseTime := time.Now()

	changes := []StatusChange{
		{
			From:      AccountStatusActive,
			To:        AccountStatusSuspended,
			Reason:    "Investigation required",
			Timestamp: baseTime,
			ChangedBy: "compliance@example.com",
		},
		{
			From:      AccountStatusSuspended,
			To:        AccountStatusActive,
			Reason:    "Investigation cleared",
			Timestamp: baseTime.Add(24 * time.Hour),
			ChangedBy: "manager@example.com",
		},
		{
			From:      AccountStatusActive,
			To:        AccountStatusClosed,
			Reason:    "Account closed by customer request",
			Timestamp: baseTime.Add(48 * time.Hour),
			ChangedBy: "customer-service@example.com",
		},
	}

	// Verify the audit trail
	require.Len(t, changes, 3, "Should have 3 status changes")

	// First change: ACTIVE -> SUSPENDED
	assert.Equal(t, AccountStatusActive, changes[0].From)
	assert.Equal(t, AccountStatusSuspended, changes[0].To)

	// Second change: SUSPENDED -> ACTIVE
	assert.Equal(t, AccountStatusSuspended, changes[1].From)
	assert.Equal(t, AccountStatusActive, changes[1].To)

	// Third change: ACTIVE -> CLOSED (terminal)
	assert.Equal(t, AccountStatusActive, changes[2].From)
	assert.Equal(t, AccountStatusClosed, changes[2].To)

	// Verify timestamps are in order
	assert.True(t, changes[1].Timestamp.After(changes[0].Timestamp))
	assert.True(t, changes[2].Timestamp.After(changes[1].Timestamp))
}

func TestAccountStatus_UnknownStatus(t *testing.T) {
	// Test behavior with an unknown/invalid status
	unknownStatus := AccountStatus("UNKNOWN")

	t.Run("unknown status cannot transition to valid status", func(t *testing.T) {
		assert.False(t, unknownStatus.CanTransitionTo(AccountStatusActive))
		assert.False(t, unknownStatus.CanTransitionTo(AccountStatusSuspended))
		assert.False(t, unknownStatus.CanTransitionTo(AccountStatusClosed))
	})

	t.Run("valid status cannot transition to unknown status", func(t *testing.T) {
		// These should return false because the unknown status is not in the valid transitions
		assert.False(t, AccountStatusActive.CanTransitionTo(unknownStatus))
		assert.False(t, AccountStatusSuspended.CanTransitionTo(unknownStatus))
	})
}

func TestAccountStatus_Constants(t *testing.T) {
	// Verify the constant values are as expected
	assert.Equal(t, AccountStatus("ACTIVE"), AccountStatusActive)
	assert.Equal(t, AccountStatus("SUSPENDED"), AccountStatusSuspended)
	assert.Equal(t, AccountStatus("CLOSED"), AccountStatusClosed)
}

func TestAccountStatus_AllValidTransitionsMatrix(t *testing.T) {
	// Comprehensive matrix test for all possible transitions
	statuses := []AccountStatus{
		AccountStatusActive,
		AccountStatusSuspended,
		AccountStatusClosed,
	}

	expectedTransitions := map[AccountStatus]map[AccountStatus]bool{
		AccountStatusActive: {
			AccountStatusActive:    false, // same status
			AccountStatusSuspended: true,
			AccountStatusClosed:    true,
		},
		AccountStatusSuspended: {
			AccountStatusActive:    true,
			AccountStatusSuspended: false, // same status
			AccountStatusClosed:    true,
		},
		AccountStatusClosed: {
			AccountStatusActive:    false,
			AccountStatusSuspended: false,
			AccountStatusClosed:    false, // same status
		},
	}

	for _, from := range statuses {
		for _, to := range statuses {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				expected := expectedTransitions[from][to]
				actual := from.CanTransitionTo(to)
				assert.Equal(t, expected, actual,
					"unexpected transition result from %s to %s", from, to)
			})
		}
	}
}

func TestValidateTransition_ErrorMessages(t *testing.T) {
	// Test that error messages are informative
	t.Run("same status error contains both statuses", func(t *testing.T) {
		err := ValidateTransition(AccountStatusActive, AccountStatusActive)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "same")
	})

	t.Run("invalid transition error contains both statuses", func(t *testing.T) {
		err := ValidateTransition(AccountStatusClosed, AccountStatusActive)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CLOSED")
		assert.Contains(t, err.Error(), "ACTIVE")
	})
}

func TestStatusChange_ZeroValues(t *testing.T) {
	// Test StatusChange with zero values
	var change StatusChange

	assert.Empty(t, string(change.From))
	assert.Empty(t, string(change.To))
	assert.Empty(t, change.Reason)
	assert.True(t, change.Timestamp.IsZero())
	assert.Empty(t, change.ChangedBy)
}

func TestStatusChange_PartialPopulation(t *testing.T) {
	// Test StatusChange with only some fields populated
	change := StatusChange{
		From: AccountStatusActive,
		To:   AccountStatusSuspended,
	}

	assert.Equal(t, AccountStatusActive, change.From)
	assert.Equal(t, AccountStatusSuspended, change.To)
	assert.Empty(t, change.Reason)
	assert.True(t, change.Timestamp.IsZero())
	assert.Empty(t, change.ChangedBy)
}

func TestAccountStatus_BidirectionalTransitions(t *testing.T) {
	// Verify that ACTIVE <-> SUSPENDED is truly bidirectional
	assert.True(t, AccountStatusActive.CanTransitionTo(AccountStatusSuspended),
		"ACTIVE should be able to transition to SUSPENDED")
	assert.True(t, AccountStatusSuspended.CanTransitionTo(AccountStatusActive),
		"SUSPENDED should be able to transition to ACTIVE")
}

func TestAccountStatus_TerminalStateCannotTransition(t *testing.T) {
	// CLOSED is terminal - verify it can't go anywhere
	allStatuses := []AccountStatus{
		AccountStatusActive,
		AccountStatusSuspended,
		AccountStatusClosed,
	}

	for _, target := range allStatuses {
		t.Run("CLOSED cannot transition to "+string(target), func(t *testing.T) {
			assert.False(t, AccountStatusClosed.CanTransitionTo(target),
				"CLOSED should not be able to transition to %s", target)
		})
	}
}

func TestAccountStatus_AllPathsToClosed(t *testing.T) {
	// Verify both ACTIVE and SUSPENDED can reach CLOSED
	t.Run("ACTIVE can reach CLOSED", func(t *testing.T) {
		assert.True(t, AccountStatusActive.CanTransitionTo(AccountStatusClosed))
	})

	t.Run("SUSPENDED can reach CLOSED", func(t *testing.T) {
		assert.True(t, AccountStatusSuspended.CanTransitionTo(AccountStatusClosed))
	})
}

func TestValidateTransition_ReturnsNilOnSuccess(t *testing.T) {
	// Verify that valid transitions return nil error
	validTransitions := []struct {
		from AccountStatus
		to   AccountStatus
	}{
		{AccountStatusActive, AccountStatusSuspended},
		{AccountStatusActive, AccountStatusClosed},
		{AccountStatusSuspended, AccountStatusActive},
		{AccountStatusSuspended, AccountStatusClosed},
	}

	for _, tt := range validTransitions {
		t.Run(string(tt.from)+"_to_"+string(tt.to), func(t *testing.T) {
			err := ValidateTransition(tt.from, tt.to)
			assert.NoError(t, err, "expected no error for valid transition from %s to %s", tt.from, tt.to)
		})
	}
}
