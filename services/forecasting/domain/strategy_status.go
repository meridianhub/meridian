// Package domain contains the domain models for the Forecasting service.
package domain

import (
	"errors"
	"fmt"
)

// ErrInvalidStatusTransition indicates an invalid strategy status transition was attempted.
var ErrInvalidStatusTransition = errors.New("invalid status transition")

// StrategyStatus represents the lifecycle state of a forecasting strategy.
type StrategyStatus string

// Strategy status constants.
const (
	// StrategyStatusDraft indicates the strategy is being configured and is not yet active.
	StrategyStatusDraft StrategyStatus = "DRAFT"

	// StrategyStatusActive indicates the strategy is active and scheduled for execution.
	StrategyStatusActive StrategyStatus = "ACTIVE"

	// StrategyStatusDeprecated indicates the strategy is deprecated and should no longer run.
	// This is a terminal state - no further transitions are allowed.
	StrategyStatusDeprecated StrategyStatus = "DEPRECATED"
)

// Valid state transitions for forecasting strategies:
//
//	DRAFT -> ACTIVE (activation)
//	   |        |
//	   |        +---> DEPRECATED (deprecation)
//	   |
//	   +------------> DEPRECATED (direct deprecation without activation)
//
// - DRAFT is the initial state for new strategies
// - DRAFT -> ACTIVE: Strategy is ready for scheduled execution
// - DRAFT -> DEPRECATED: Strategy was never used and is being discarded
// - ACTIVE -> DEPRECATED: Strategy is being retired
// - DEPRECATED is a terminal state

// validStatusTransitions defines the allowed state transitions.
var validStatusTransitions = map[StrategyStatus]map[StrategyStatus]bool{
	StrategyStatusDraft: {
		StrategyStatusActive:     true,
		StrategyStatusDeprecated: true,
	},
	StrategyStatusActive: {
		StrategyStatusDeprecated: true,
	},
	StrategyStatusDeprecated: {},
}

// CanTransitionTo checks if a transition from the current status to the target status is valid.
func (s StrategyStatus) CanTransitionTo(target StrategyStatus) bool {
	if s == target {
		return false
	}
	allowed, exists := validStatusTransitions[s]
	if !exists {
		return false
	}
	return allowed[target]
}

// ValidateStatusTransition checks if transitioning from one status to another is valid.
func ValidateStatusTransition(from, to StrategyStatus) error {
	if from == to {
		return fmt.Errorf("%w: source and target status are the same", ErrInvalidStatusTransition)
	}
	if !from.CanTransitionTo(to) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidStatusTransition, from, to)
	}
	return nil
}

// IsValid returns true if the strategy status is a recognized valid type.
func (s StrategyStatus) IsValid() bool {
	switch s {
	case StrategyStatusDraft, StrategyStatusActive, StrategyStatusDeprecated:
		return true
	default:
		return false
	}
}

// String returns the string representation of the strategy status.
func (s StrategyStatus) String() string {
	return string(s)
}
