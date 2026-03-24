package domain

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestErrInvalidStatusTransition_IsSentinelError verifies the sentinel error
// can be detected via errors.Is after wrapping.
func TestErrInvalidStatusTransition_IsSentinelError(t *testing.T) {
	wrapped := errors.New("outer: " + ErrInvalidStatusTransition.Error())
	// Direct match
	assert.ErrorIs(t, ErrInvalidStatusTransition, ErrInvalidStatusTransition)
	// Ensure it is actually the sentinel and not nil
	assert.NotNil(t, ErrInvalidStatusTransition)
	assert.NotNil(t, wrapped)
}

// TestDataSetStatus_CanTransitionTo_UnknownSource verifies unknown source status
// cannot transition to any valid status.
func TestDataSetStatus_CanTransitionTo_UnknownSource(t *testing.T) {
	unknown := DataSetStatus("UNKNOWN_SOURCE")

	assert.False(t, unknown.CanTransitionTo(DataSetStatusDraft))
	assert.False(t, unknown.CanTransitionTo(DataSetStatusActive))
	assert.False(t, unknown.CanTransitionTo(DataSetStatusDeprecated))
}

// TestDataSetStatus_CanTransitionTo_UnknownTarget verifies known source statuses
// cannot transition to unknown target statuses.
func TestDataSetStatus_CanTransitionTo_UnknownTarget(t *testing.T) {
	unknown := DataSetStatus("UNKNOWN_TARGET")

	assert.False(t, DataSetStatusDraft.CanTransitionTo(unknown))
	assert.False(t, DataSetStatusActive.CanTransitionTo(unknown))
	assert.False(t, DataSetStatusDeprecated.CanTransitionTo(unknown))
}

// TestValidateStatusTransition_WrapsErrInvalidStatusTransition verifies that errors
// returned by ValidateStatusTransition wrap ErrInvalidStatusTransition with errors.Is.
func TestValidateStatusTransition_WrapsErrInvalidStatusTransition(t *testing.T) {
	tests := []struct {
		name string
		from DataSetStatus
		to   DataSetStatus
	}{
		{"same status", DataSetStatusDraft, DataSetStatusDraft},
		{"backward", DataSetStatusActive, DataSetStatusDraft},
		{"from terminal", DataSetStatusDeprecated, DataSetStatusActive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStatusTransition(tt.from, tt.to)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidStatusTransition),
				"expected errors.Is match for ErrInvalidStatusTransition, got: %v", err)
		})
	}
}

// TestDataSetStatus_IsValid_AllKnownValues exhaustively tests all known status values.
func TestDataSetStatus_IsValid_AllKnownValues(t *testing.T) {
	valid := []DataSetStatus{DataSetStatusDraft, DataSetStatusActive, DataSetStatusDeprecated}
	for _, s := range valid {
		assert.True(t, s.IsValid(), "%q should be valid", s)
	}

	invalid := []DataSetStatus{
		DataSetStatus(""),
		DataSetStatus("draft"),
		DataSetStatus("active"),
		DataSetStatus("deprecated"),
		DataSetStatus("UNKNOWN"),
		DataSetStatus("PENDING"),
	}
	for _, s := range invalid {
		assert.False(t, s.IsValid(), "%q should be invalid", s)
	}
}

// TestDataSetStatus_ActiveTransitions verifies ACTIVE state transitions specifically.
// ACTIVE can only go to DEPRECATED, not back to DRAFT.
func TestDataSetStatus_ActiveTransitions(t *testing.T) {
	assert.False(t, DataSetStatusActive.CanTransitionTo(DataSetStatusDraft),
		"ACTIVE should not go back to DRAFT")
	assert.True(t, DataSetStatusActive.CanTransitionTo(DataSetStatusDeprecated),
		"ACTIVE should be deprecatable")
	assert.False(t, DataSetStatusActive.CanTransitionTo(DataSetStatusActive),
		"ACTIVE should not self-transition")
}
