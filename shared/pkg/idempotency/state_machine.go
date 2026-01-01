// Package idempotency provides distributed idempotency checking and locking capabilities.
// This file implements a state machine for idempotency key lifecycle management.
package idempotency

import (
	"errors"
	"fmt"
	"time"
)

// State represents the lifecycle state of an idempotency key.
// States form a directed graph with enforced transition rules.
type State string

const (
	// StateNone indicates no idempotency record exists for this key.
	// This is the initial state before any operation starts.
	StateNone State = "none"

	// StatePending indicates the operation is currently in progress.
	// Only one request can hold the pending state for a given key.
	StatePending State = "pending"

	// StateCompleted indicates the operation finished successfully.
	// The cached result can be returned for duplicate requests.
	StateCompleted State = "completed"

	// StateFailed indicates the operation failed.
	// A new attempt may be allowed depending on failure semantics.
	StateFailed State = "failed"
)

// State machine error types
var (
	// ErrInvalidTransition indicates an attempted state transition that violates
	// the idempotency state machine rules. This occurs when:
	// - Attempting PENDING -> PENDING (duplicate concurrent request)
	// - Attempting COMPLETED -> any other state (terminal state)
	// - Attempting to transition from StateNone to anything except PENDING
	ErrInvalidTransition = errors.New("invalid state transition")

	// ErrStaleKey indicates an idempotency key has been in PENDING state
	// longer than the configured timeout threshold. This suggests the original
	// request failed without completing or failing cleanly.
	ErrStaleKey = errors.New("stale idempotency key detected")
)

// Config holds configuration for the state machine.
type Config struct {
	// StaleKeyTimeout is the duration after which a PENDING key is considered stale.
	// Stale keys should be cleaned up to prevent permanent blocking.
	// Default: 15 minutes.
	StaleKeyTimeout time.Duration
}

// DefaultConfig returns the default state machine configuration.
func DefaultConfig() Config {
	return Config{
		StaleKeyTimeout: 15 * time.Minute,
	}
}

// StateMachine validates and enforces idempotency key lifecycle transitions.
//
// Valid transitions:
//   - NONE -> PENDING: Start processing a new request
//   - PENDING -> COMPLETED: Operation succeeded
//   - PENDING -> FAILED: Operation failed
//
// Invalid transitions (return ErrInvalidTransition):
//   - PENDING -> PENDING: Duplicate concurrent request (race condition)
//   - COMPLETED -> PENDING: Cannot reprocess completed operation
//   - COMPLETED -> FAILED: Terminal state cannot change
//   - COMPLETED -> COMPLETED: Already terminal
//   - FAILED -> PENDING: Must wait for stale key cleanup or use new key
//   - FAILED -> COMPLETED: Cannot change outcome after failure
//   - NONE -> COMPLETED: Must go through PENDING first
//   - NONE -> FAILED: Must go through PENDING first
type StateMachine struct {
	config Config
}

// NewStateMachine creates a new state machine with the given configuration.
// If config is nil, DefaultConfig() is used.
func NewStateMachine(config *Config) *StateMachine {
	if config == nil {
		c := DefaultConfig()
		config = &c
	}
	return &StateMachine{config: *config}
}

// ValidateTransition checks if a state transition is allowed according to the
// idempotency state machine rules.
//
// Parameters:
//   - from: The current state of the idempotency key
//   - to: The desired target state
//
// Returns nil if the transition is valid, ErrInvalidTransition otherwise.
func (sm *StateMachine) ValidateTransition(from, to State) error {
	if isValidTransition(from, to) {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}

// isValidTransition returns true if the transition from -> to is allowed.
func isValidTransition(from, to State) bool {
	switch from {
	case StateNone:
		// From NONE, can only transition to PENDING (start processing)
		return to == StatePending

	case StatePending:
		// From PENDING, can transition to COMPLETED or FAILED (finish processing)
		// PENDING -> PENDING is NOT allowed (would indicate duplicate concurrent request)
		return to == StateCompleted || to == StateFailed

	case StateCompleted, StateFailed:
		// Terminal states - no transitions allowed
		return false

	default:
		// Unknown state - reject transition
		return false
	}
}

// IsTerminal returns true if the state is a terminal state (COMPLETED or FAILED).
// Terminal states cannot transition to any other state.
func (sm *StateMachine) IsTerminal(state State) bool {
	return state == StateCompleted || state == StateFailed
}

// IsPending returns true if the state is PENDING.
func (sm *StateMachine) IsPending(state State) bool {
	return state == StatePending
}

// IsStale checks if a PENDING key has exceeded the stale timeout threshold.
// Returns true if the key should be considered stale and eligible for cleanup.
//
// Parameters:
//   - pendingSince: The time when the key entered PENDING state
//   - now: The current time (for testability)
func (sm *StateMachine) IsStale(pendingSince, now time.Time) bool {
	if pendingSince.IsZero() {
		return false
	}
	return now.Sub(pendingSince) > sm.config.StaleKeyTimeout
}

// StaleKeyTimeout returns the configured timeout for stale PENDING keys.
func (sm *StateMachine) StaleKeyTimeout() time.Duration {
	return sm.config.StaleKeyTimeout
}

// StatusToState converts an OperationStatus to its corresponding State.
// This bridges the existing OperationStatus type with the new State type.
func StatusToState(status OperationStatus) State {
	switch status {
	case StatusPending:
		return StatePending
	case StatusCompleted:
		return StateCompleted
	case StatusFailed:
		return StateFailed
	default:
		return StateNone
	}
}

// StateToStatus converts a State to its corresponding OperationStatus.
// StateNone returns an empty OperationStatus (not a valid status).
func StateToStatus(state State) OperationStatus {
	switch state {
	case StatePending:
		return StatusPending
	case StateCompleted:
		return StatusCompleted
	case StateFailed:
		return StatusFailed
	case StateNone:
		return ""
	default:
		// Unknown state - return empty status
		return ""
	}
}

// TransitionResult contains the outcome of a state transition attempt.
type TransitionResult struct {
	// Allowed indicates whether the transition was permitted
	Allowed bool

	// PreviousState is the state before the transition attempt
	PreviousState State

	// NewState is the resulting state (same as PreviousState if not allowed)
	NewState State

	// Error contains the reason if the transition was not allowed
	Error error
}

// AttemptTransition validates and returns the result of a state transition.
// This is a convenience method that wraps ValidateTransition with additional context.
func (sm *StateMachine) AttemptTransition(from, to State) TransitionResult {
	err := sm.ValidateTransition(from, to)
	if err != nil {
		return TransitionResult{
			Allowed:       false,
			PreviousState: from,
			NewState:      from, // Stay in current state on failure
			Error:         err,
		}
	}
	return TransitionResult{
		Allowed:       true,
		PreviousState: from,
		NewState:      to,
		Error:         nil,
	}
}
