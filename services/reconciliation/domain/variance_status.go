package domain

// VarianceStatus represents the resolution state of a variance.
type VarianceStatus string

// Supported variance statuses.
const (
	VarianceStatusDetected      VarianceStatus = "DETECTED"
	VarianceStatusValued        VarianceStatus = "VALUED"
	VarianceStatusOpen          VarianceStatus = "OPEN"
	VarianceStatusInvestigating VarianceStatus = "INVESTIGATING"
	VarianceStatusDisputed      VarianceStatus = "DISPUTED"
	VarianceStatusResolved      VarianceStatus = "RESOLVED"
	VarianceStatusAccepted      VarianceStatus = "ACCEPTED"
)

// IsValid checks if the variance status is a recognized value.
func (s VarianceStatus) IsValid() bool {
	switch s {
	case VarianceStatusDetected, VarianceStatusValued,
		VarianceStatusOpen, VarianceStatusInvestigating,
		VarianceStatusDisputed, VarianceStatusResolved,
		VarianceStatusAccepted:
		return true
	}
	return false
}

// String returns the string representation.
func (s VarianceStatus) String() string {
	return string(s)
}

// IsFinal checks if the status is a terminal state.
func (s VarianceStatus) IsFinal() bool {
	return s == VarianceStatusResolved || s == VarianceStatusAccepted
}

// CanTransitionTo checks if a transition to the target status is valid.
func (s VarianceStatus) CanTransitionTo(target VarianceStatus) bool {
	if s.IsFinal() {
		return false
	}

	validTransitions := map[VarianceStatus][]VarianceStatus{
		VarianceStatusDetected:      {VarianceStatusValued, VarianceStatusOpen, VarianceStatusResolved, VarianceStatusAccepted},
		VarianceStatusValued:        {VarianceStatusOpen, VarianceStatusInvestigating, VarianceStatusDisputed, VarianceStatusResolved, VarianceStatusAccepted},
		VarianceStatusOpen:          {VarianceStatusInvestigating, VarianceStatusDisputed, VarianceStatusResolved, VarianceStatusAccepted},
		VarianceStatusInvestigating: {VarianceStatusDisputed, VarianceStatusResolved, VarianceStatusAccepted},
		VarianceStatusDisputed:      {VarianceStatusInvestigating, VarianceStatusResolved, VarianceStatusAccepted},
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

// ParseVarianceStatus converts a string to VarianceStatus.
// Returns VarianceStatusOpen for unrecognized values.
func ParseVarianceStatus(s string) VarianceStatus {
	status := VarianceStatus(s)
	if status.IsValid() {
		return status
	}
	return VarianceStatusOpen
}
