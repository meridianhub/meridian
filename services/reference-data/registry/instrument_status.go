// Package registry provides the InstrumentRegistry implementation backed by PostgreSQL.
package registry

import (
	"errors"
	"fmt"
)

// Valid state transitions for instrument definitions:
//
//	DRAFT -> ACTIVE (activation)
//	ACTIVE -> DEPRECATED (deprecation)
//	DEPRECATED -> ACTIVE (reactivation via convergent manifest apply)
var validInstrumentStatusTransitions = map[Status]map[Status]bool{
	StatusDraft: {
		StatusActive: true,
	},
	StatusActive: {
		StatusDeprecated: true,
	},
	StatusDeprecated: {
		StatusActive: true, // Convergent apply: re-declare in manifest to reactivate
	},
}

// CanTransitionTo checks if a transition from the current status to the target status is valid.
func (s Status) CanTransitionTo(target Status) bool {
	if s == target {
		return false
	}
	allowed, exists := validInstrumentStatusTransitions[s]
	if !exists {
		return false
	}
	return allowed[target]
}

// ValidateStatusTransition checks if transitioning from one status to another is valid.
// Returns an error if the transition is not allowed.
func ValidateStatusTransition(from, to Status) error {
	if from == to {
		return fmt.Errorf("%w: source and target status are the same (%s)", ErrInvalidStateTransition, from)
	}
	if !from.CanTransitionTo(to) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidStateTransition, from, to)
	}
	return nil
}

// IsValid returns true if the status is a recognized valid value.
func (s Status) IsValid() bool {
	switch s {
	case StatusDraft, StatusActive, StatusDeprecated:
		return true
	default:
		return false
	}
}

// String returns the string representation of the status.
func (s Status) String() string {
	return string(s)
}

// ErrSuccessorWriteOnce is returned when attempting to change a successor_id that is already set.
var ErrSuccessorWriteOnce = errors.New("cannot modify successor_id once set (write-once semantics)")
