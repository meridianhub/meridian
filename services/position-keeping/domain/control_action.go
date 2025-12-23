package domain

import (
	"errors"
	"time"
)

var (
	// ErrInvalidControlAction is returned when an invalid control action is attempted
	ErrInvalidControlAction = errors.New("invalid control action")
	// ErrCannotSuspend is returned when suspension is not allowed in current state
	ErrCannotSuspend = errors.New("cannot suspend log in current state")
	// ErrCannotResume is returned when resumption is not allowed in current state
	ErrCannotResume = errors.New("cannot resume log in current state")
	// ErrCannotTerminate is returned when termination is not allowed in current state
	ErrCannotTerminate = errors.New("cannot terminate log in current state")
	// ErrAlreadyTerminated is returned when attempting to modify a terminated log
	ErrAlreadyTerminated = errors.New("log already terminated")
	// ErrEmptyOperatorID is returned when operator ID is required but empty
	ErrEmptyOperatorID = errors.New("operator ID cannot be empty")
)

// ControlAction represents the control operations for log lifecycle management.
type ControlAction string

// Control actions for log lifecycle management.
const (
	ControlActionUnspecified ControlAction = ""          // No action specified
	ControlActionSuspend     ControlAction = "SUSPEND"   // Temporarily suspend log processing
	ControlActionResume      ControlAction = "RESUME"    // Resume a suspended log
	ControlActionTerminate   ControlAction = "TERMINATE" // Permanently terminate the log
)

// IsValid checks if the control action is valid.
func (c ControlAction) IsValid() bool {
	switch c {
	case ControlActionSuspend, ControlActionResume, ControlActionTerminate:
		return true
	case ControlActionUnspecified:
		return false
	}
	return false
}

// String returns the string representation of the control action.
func (c ControlAction) String() string {
	return string(c)
}

// StatusChangeEntry represents a single entry in the status change history.
// This provides an audit trail for compliance tracking of all status changes.
type StatusChangeEntry struct {
	// PreviousStatus is the status before the change
	PreviousStatus TransactionStatus
	// NewStatus is the status after the change
	NewStatus TransactionStatus
	// Timestamp is when the status change occurred
	Timestamp time.Time
	// Reason provides context for the status change
	Reason string
	// OperatorID identifies who performed the status change
	OperatorID string
	// Action is the control action that triggered the change (if applicable)
	Action ControlAction
}

// NewStatusChangeEntry creates a new StatusChangeEntry with validated fields.
func NewStatusChangeEntry(
	previousStatus TransactionStatus,
	newStatus TransactionStatus,
	reason string,
	operatorID string,
	action ControlAction,
) *StatusChangeEntry {
	return &StatusChangeEntry{
		PreviousStatus: previousStatus,
		NewStatus:      newStatus,
		Timestamp:      time.Now().UTC(),
		Reason:         reason,
		OperatorID:     operatorID,
		Action:         action,
	}
}
