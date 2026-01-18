// Package domain contains the domain models for the Market Information service.
package domain

import (
	"errors"
	"fmt"
)

// ErrInvalidStatusTransition indicates an invalid dataset status transition was attempted.
var ErrInvalidStatusTransition = errors.New("invalid status transition")

// DataSetStatus represents the lifecycle state of a dataset definition.
type DataSetStatus string

// Dataset status constants.
const (
	// DataSetStatusDraft indicates the dataset is being configured and is not yet active.
	// This is the initial state for all new dataset definitions.
	DataSetStatusDraft DataSetStatus = "DRAFT"

	// DataSetStatusActive indicates the dataset is active and available for use.
	// Active datasets can receive and process data.
	DataSetStatusActive DataSetStatus = "ACTIVE"

	// DataSetStatusDeprecated indicates the dataset is deprecated and should no longer be used.
	// This is a terminal state - no further transitions are allowed.
	DataSetStatusDeprecated DataSetStatus = "DEPRECATED"
)

// Valid state transitions for dataset definitions:
//
//	DRAFT → ACTIVE (activation)
//	   │        │
//	   │        └───→ DEPRECATED (deprecation)
//	   │
//	   └────────────→ DEPRECATED (direct deprecation without activation)
//
// - DRAFT is the initial state for new datasets
// - DRAFT → ACTIVE: Dataset is ready for production use
// - DRAFT → DEPRECATED: Dataset was never used and is being discarded
// - ACTIVE → DEPRECATED: Dataset is being retired from use
// - DEPRECATED is a terminal state - no transitions are allowed from DEPRECATED

// validStatusTransitions defines the allowed state transitions.
// The map key is the source status, and the value is a set of valid target statuses.
var validStatusTransitions = map[DataSetStatus]map[DataSetStatus]bool{
	DataSetStatusDraft: {
		DataSetStatusActive:     true,
		DataSetStatusDeprecated: true,
	},
	DataSetStatusActive: {
		DataSetStatusDeprecated: true,
	},
	DataSetStatusDeprecated: {
		// No valid transitions from DEPRECATED - it's a terminal state
	},
}

// CanTransitionTo checks if a transition from the current status to the target status is valid.
func (s DataSetStatus) CanTransitionTo(target DataSetStatus) bool {
	if s == target {
		return false // Same status is not a transition
	}
	allowed, exists := validStatusTransitions[s]
	if !exists {
		return false
	}
	return allowed[target]
}

// ValidateStatusTransition checks if transitioning from one status to another is valid.
// Returns an error if the transition is not allowed.
func ValidateStatusTransition(from, to DataSetStatus) error {
	if from == to {
		return fmt.Errorf("%w: source and target status are the same", ErrInvalidStatusTransition)
	}
	if !from.CanTransitionTo(to) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidStatusTransition, from, to)
	}
	return nil
}

// IsValid returns true if the dataset status is a recognized valid type.
func (s DataSetStatus) IsValid() bool {
	switch s {
	case DataSetStatusDraft, DataSetStatusActive, DataSetStatusDeprecated:
		return true
	default:
		return false
	}
}

// String returns the string representation of the dataset status.
func (s DataSetStatus) String() string {
	return string(s)
}
