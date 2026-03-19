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

func TestStrategyStatus_CanTransitionTo_UnknownSource(t *testing.T) {
	unknown := StrategyStatus("UNKNOWN")
	assert.False(t, unknown.CanTransitionTo(StrategyStatusActive))
	assert.False(t, unknown.CanTransitionTo(StrategyStatusDraft))
	assert.False(t, unknown.CanTransitionTo(StrategyStatusDeprecated))
}

func TestValidateStatusTransition_AllValid(t *testing.T) {
	validTransitions := []struct {
		from StrategyStatus
		to   StrategyStatus
	}{
		{StrategyStatusDraft, StrategyStatusActive},
		{StrategyStatusDraft, StrategyStatusDeprecated},
		{StrategyStatusActive, StrategyStatusDeprecated},
	}
	for _, tc := range validTransitions {
		t.Run(tc.from.String()+"_to_"+tc.to.String(), func(t *testing.T) {
			assert.NoError(t, ValidateStatusTransition(tc.from, tc.to))
		})
	}
}

func TestValidateStatusTransition_AllInvalid(t *testing.T) {
	invalidTransitions := []struct {
		from StrategyStatus
		to   StrategyStatus
	}{
		{StrategyStatusActive, StrategyStatusDraft},
		{StrategyStatusDeprecated, StrategyStatusDraft},
		{StrategyStatusDeprecated, StrategyStatusActive},
	}
	for _, tc := range invalidTransitions {
		t.Run(tc.from.String()+"_to_"+tc.to.String(), func(t *testing.T) {
			err := ValidateStatusTransition(tc.from, tc.to)
			assert.ErrorIs(t, err, ErrInvalidStatusTransition)
			assert.Contains(t, err.Error(), "cannot transition")
		})
	}
}

func TestValidateStatusTransition_UnknownStatus(t *testing.T) {
	err := ValidateStatusTransition(StrategyStatus("UNKNOWN"), StrategyStatusActive)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestStrategyStatus_String(t *testing.T) {
	assert.Equal(t, "DRAFT", StrategyStatusDraft.String())
	assert.Equal(t, "ACTIVE", StrategyStatusActive.String())
	assert.Equal(t, "DEPRECATED", StrategyStatusDeprecated.String())
}
