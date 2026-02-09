package domain

// AssertionStatus represents the result of a balance assertion check.
type AssertionStatus string

// Supported assertion statuses.
const (
	AssertionStatusPending  AssertionStatus = "PENDING"
	AssertionStatusPassed   AssertionStatus = "PASSED"
	AssertionStatusFailed   AssertionStatus = "FAILED"
	AssertionStatusOverride AssertionStatus = "OVERRIDE"
)

// IsValid checks if the assertion status is a recognized value.
func (s AssertionStatus) IsValid() bool {
	switch s {
	case AssertionStatusPending, AssertionStatusPassed,
		AssertionStatusFailed, AssertionStatusOverride:
		return true
	}
	return false
}

// String returns the string representation.
func (s AssertionStatus) String() string {
	return string(s)
}

// IsFinal checks if the status is a terminal state.
func (s AssertionStatus) IsFinal() bool {
	return s == AssertionStatusPassed || s == AssertionStatusFailed || s == AssertionStatusOverride
}

// CanTransitionTo checks if a transition to the target status is valid.
func (s AssertionStatus) CanTransitionTo(target AssertionStatus) bool {
	if s.IsFinal() {
		return false
	}

	validTransitions := map[AssertionStatus][]AssertionStatus{
		AssertionStatusPending: {AssertionStatusPassed, AssertionStatusFailed},
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

// ParseAssertionStatus converts a string to AssertionStatus.
// Returns AssertionStatusPending for unrecognized values.
func ParseAssertionStatus(s string) AssertionStatus {
	status := AssertionStatus(s)
	if status.IsValid() {
		return status
	}
	return AssertionStatusPending
}
