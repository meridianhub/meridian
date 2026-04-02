package registry_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstrumentStatus_IsValid(t *testing.T) {
	assert.True(t, registry.StatusDraft.IsValid())
	assert.True(t, registry.StatusActive.IsValid())
	assert.True(t, registry.StatusDeprecated.IsValid())
	assert.False(t, registry.Status("UNKNOWN").IsValid())
	assert.False(t, registry.Status("").IsValid())
}

func TestInstrumentStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name   string
		from   registry.Status
		to     registry.Status
		expect bool
	}{
		{"DRAFT to ACTIVE", registry.StatusDraft, registry.StatusActive, true},
		{"DRAFT to DEPRECATED", registry.StatusDraft, registry.StatusDeprecated, false},
		{"DRAFT to DRAFT", registry.StatusDraft, registry.StatusDraft, false},
		{"ACTIVE to DEPRECATED", registry.StatusActive, registry.StatusDeprecated, true},
		{"ACTIVE to DRAFT", registry.StatusActive, registry.StatusDraft, false},
		{"ACTIVE to ACTIVE", registry.StatusActive, registry.StatusActive, false},
		{"DEPRECATED to DRAFT", registry.StatusDeprecated, registry.StatusDraft, false},
		{"DEPRECATED to ACTIVE", registry.StatusDeprecated, registry.StatusActive, true},
		{"DEPRECATED to DEPRECATED", registry.StatusDeprecated, registry.StatusDeprecated, false},
		{"unknown to ACTIVE", registry.Status("UNKNOWN"), registry.StatusActive, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, tt.from.CanTransitionTo(tt.to))
		})
	}
}

func TestValidateStatusTransition(t *testing.T) {
	t.Run("valid DRAFT to ACTIVE", func(t *testing.T) {
		err := registry.ValidateStatusTransition(registry.StatusDraft, registry.StatusActive)
		require.NoError(t, err)
	})

	t.Run("valid ACTIVE to DEPRECATED", func(t *testing.T) {
		err := registry.ValidateStatusTransition(registry.StatusActive, registry.StatusDeprecated)
		require.NoError(t, err)
	})

	t.Run("same status returns error", func(t *testing.T) {
		err := registry.ValidateStatusTransition(registry.StatusDraft, registry.StatusDraft)
		require.ErrorIs(t, err, registry.ErrInvalidStateTransition)
		assert.Contains(t, err.Error(), "same")
	})

	t.Run("invalid transition returns error", func(t *testing.T) {
		err := registry.ValidateStatusTransition(registry.StatusDraft, registry.StatusDeprecated)
		require.ErrorIs(t, err, registry.ErrInvalidStateTransition)
		assert.Contains(t, err.Error(), "cannot transition")
	})

	t.Run("DEPRECATED to ACTIVE is valid (reactivation)", func(t *testing.T) {
		err := registry.ValidateStatusTransition(registry.StatusDeprecated, registry.StatusActive)
		require.NoError(t, err)
	})
}

func TestInstrumentStatus_String(t *testing.T) {
	assert.Equal(t, "DRAFT", registry.StatusDraft.String())
	assert.Equal(t, "ACTIVE", registry.StatusActive.String())
	assert.Equal(t, "DEPRECATED", registry.StatusDeprecated.String())
	assert.Equal(t, "UNKNOWN", registry.Status("UNKNOWN").String())
}
