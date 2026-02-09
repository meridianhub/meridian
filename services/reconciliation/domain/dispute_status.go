package domain

// DisputeStatus represents the state of a formal dispute.
type DisputeStatus string

// Supported dispute statuses.
const (
	DisputeStatusOpen        DisputeStatus = "OPEN"
	DisputeStatusUnderReview DisputeStatus = "UNDER_REVIEW"
	DisputeStatusEscalated   DisputeStatus = "ESCALATED"
	DisputeStatusResolved    DisputeStatus = "RESOLVED"
	DisputeStatusRejected    DisputeStatus = "REJECTED"
)

// IsValid checks if the dispute status is a recognized value.
func (s DisputeStatus) IsValid() bool {
	switch s {
	case DisputeStatusOpen, DisputeStatusUnderReview,
		DisputeStatusEscalated, DisputeStatusResolved,
		DisputeStatusRejected:
		return true
	}
	return false
}

// String returns the string representation.
func (s DisputeStatus) String() string {
	return string(s)
}

// IsFinal checks if the status is a terminal state.
func (s DisputeStatus) IsFinal() bool {
	return s == DisputeStatusResolved || s == DisputeStatusRejected
}

// CanTransitionTo checks if a transition to the target status is valid.
func (s DisputeStatus) CanTransitionTo(target DisputeStatus) bool {
	if s.IsFinal() {
		return false
	}

	validTransitions := map[DisputeStatus][]DisputeStatus{
		DisputeStatusOpen:        {DisputeStatusUnderReview, DisputeStatusResolved, DisputeStatusRejected},
		DisputeStatusUnderReview: {DisputeStatusEscalated, DisputeStatusResolved, DisputeStatusRejected},
		DisputeStatusEscalated:   {DisputeStatusUnderReview, DisputeStatusResolved, DisputeStatusRejected},
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

// ParseDisputeStatus converts a string to DisputeStatus.
// Returns DisputeStatusOpen for unrecognized values.
func ParseDisputeStatus(s string) DisputeStatus {
	status := DisputeStatus(s)
	if status.IsValid() {
		return status
	}
	return DisputeStatusOpen
}
