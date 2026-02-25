// Package domain contains the core business logic for internal accounts.
package domain

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidStatusTransition indicates an invalid account status transition was attempted.
var ErrInvalidStatusTransition = errors.New("invalid status transition")

// AccountStatus represents the lifecycle state of an internal account.
type AccountStatus string

// Account status constants.
const (
	AccountStatusActive    AccountStatus = "ACTIVE"
	AccountStatusSuspended AccountStatus = "SUSPENDED"
	AccountStatusClosed    AccountStatus = "CLOSED"
)

// Valid state transitions for internal accounts:
//
//	ACTIVE ↔ SUSPENDED (bidirectional)
//	   │         │
//	   └────┬────┘
//	        ↓
//	     CLOSED (terminal)
//
// - ACTIVE is the initial state for new accounts
// - ACTIVE ↔ SUSPENDED transitions are bidirectional
// - CLOSED is a terminal state - no transitions are allowed from CLOSED

// validTransitions defines the allowed state transitions.
// The map key is the source status, and the value is a set of valid target statuses.
var validTransitions = map[AccountStatus]map[AccountStatus]bool{
	AccountStatusActive: {
		AccountStatusSuspended: true,
		AccountStatusClosed:    true,
	},
	AccountStatusSuspended: {
		AccountStatusActive: true,
		AccountStatusClosed: true,
	},
	AccountStatusClosed: {
		// No valid transitions from CLOSED - it's a terminal state
	},
}

// CanTransitionTo checks if a transition from the current status to the target status is valid.
func (s AccountStatus) CanTransitionTo(target AccountStatus) bool {
	if s == target {
		return false // Same status is not a transition
	}
	allowed, exists := validTransitions[s]
	if !exists {
		return false
	}
	return allowed[target]
}

// ValidateTransition checks if transitioning from one status to another is valid.
// Returns an error if the transition is not allowed.
func ValidateTransition(from, to AccountStatus) error {
	if from == to {
		return fmt.Errorf("%w: source and target status are the same", ErrInvalidStatusTransition)
	}
	if !from.CanTransitionTo(to) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidStatusTransition, from, to)
	}
	return nil
}

// StatusChange represents a recorded state transition for audit purposes.
type StatusChange struct {
	From      AccountStatus
	To        AccountStatus
	Reason    string
	Timestamp time.Time
	ChangedBy string
}
