package domain

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStrategyStatus_IsValid(t *testing.T) {
	tests := []struct {
		status StrategyStatus
		valid  bool
	}{
		{StrategyStatusDraft, true},
		{StrategyStatusActive, true},
		{StrategyStatusDeprecated, true},
		{StrategyStatus("UNKNOWN"), false},
		{StrategyStatus(""), false},
	}
	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			assert.Equal(t, tc.valid, tc.status.IsValid())
		})
	}
}

func TestStrategyStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name   string
		from   StrategyStatus
		to     StrategyStatus
		expect bool
	}{
		{"draft to active", StrategyStatusDraft, StrategyStatusActive, true},
		{"draft to deprecated", StrategyStatusDraft, StrategyStatusDeprecated, true},
		{"active to deprecated", StrategyStatusActive, StrategyStatusDeprecated, true},
		{"active to draft", StrategyStatusActive, StrategyStatusDraft, false},
		{"deprecated to active", StrategyStatusDeprecated, StrategyStatusActive, false},
		{"deprecated to draft", StrategyStatusDeprecated, StrategyStatusDraft, false},
		{"same status draft", StrategyStatusDraft, StrategyStatusDraft, false},
		{"same status active", StrategyStatusActive, StrategyStatusActive, false},
		{"same status deprecated", StrategyStatusDeprecated, StrategyStatusDeprecated, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, tc.from.CanTransitionTo(tc.to))
		})
	}
}

func TestValidateStatusTransition_SameStatus(t *testing.T) {
	err := ValidateStatusTransition(StrategyStatusDraft, StrategyStatusDraft)
	assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
}

func TestValidateStatusTransition_InvalidTransition(t *testing.T) {
	err := ValidateStatusTransition(StrategyStatusDeprecated, StrategyStatusActive)
	assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
}

func TestValidateStatusTransition_ValidTransition(t *testing.T) {
	err := ValidateStatusTransition(StrategyStatusDraft, StrategyStatusActive)
	assert.NoError(t, err)
}

func TestStrategyStatus_String(t *testing.T) {
	assert.Equal(t, "DRAFT", StrategyStatusDraft.String())
	assert.Equal(t, "ACTIVE", StrategyStatusActive.String())
	assert.Equal(t, "DEPRECATED", StrategyStatusDeprecated.String())
}
