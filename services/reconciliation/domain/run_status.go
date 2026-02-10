package domain

// RunStatus represents the current state of a settlement run.
type RunStatus string

// Supported run statuses through the settlement run lifecycle.
const (
	RunStatusPending   RunStatus = "PENDING"
	RunStatusRunning   RunStatus = "RUNNING"
	RunStatusCompleted RunStatus = "COMPLETED"
	RunStatusFinalized RunStatus = "FINALIZED"
	RunStatusFailed    RunStatus = "FAILED"
	RunStatusCancelled RunStatus = "CANCELLED"
)

// IsValid checks if the run status is a recognized value.
func (s RunStatus) IsValid() bool {
	switch s {
	case RunStatusPending, RunStatusRunning, RunStatusCompleted,
		RunStatusFinalized, RunStatusFailed, RunStatusCancelled:
		return true
	}
	return false
}

// String returns the string representation.
func (s RunStatus) String() string {
	return string(s)
}

// IsFinal checks if the status is a terminal state.
func (s RunStatus) IsFinal() bool {
	return s == RunStatusFinalized || s == RunStatusFailed || s == RunStatusCancelled
}

// CanTransitionTo checks if a transition to the target status is valid.
func (s RunStatus) CanTransitionTo(target RunStatus) bool {
	if s.IsFinal() {
		return false
	}

	validTransitions := map[RunStatus][]RunStatus{
		RunStatusPending:   {RunStatusRunning, RunStatusCancelled},
		RunStatusRunning:   {RunStatusCompleted, RunStatusFailed, RunStatusCancelled},
		RunStatusCompleted: {RunStatusFinalized},
	}

	allowed, exists := validTransitions[s]
	if !exists {
		return false
	}

	for _, a := range allowed {
		if target == a {
			return true
		}
	}
	return false
}

// ParseRunStatus converts a string to RunStatus.
// Returns RunStatusPending for unrecognized values.
func ParseRunStatus(s string) RunStatus {
	status := RunStatus(s)
	if status.IsValid() {
		return status
	}
	return RunStatusPending
}
