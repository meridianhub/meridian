// Package accounttype provides the domain model for AccountTypeDefinitions,
// which define the structural and behavioral characteristics of account types
// within the ledger system.
package accounttype

// Status represents the lifecycle status of an AccountTypeDefinition.
type Status string

const (
	// StatusDraft indicates an account type that is not yet active and can be modified.
	StatusDraft Status = "DRAFT"

	// StatusActive indicates an account type that is in use and immutable.
	StatusActive Status = "ACTIVE"

	// StatusDeprecated indicates an account type that should no longer be used for new accounts.
	StatusDeprecated Status = "DEPRECATED"
)

// Valid state transitions for account type definitions:
//
//	DRAFT -> ACTIVE (activation)
//	ACTIVE -> DEPRECATED (deprecation)
//	DEPRECATED is terminal - no transitions allowed
var validStatusTransitions = map[Status]map[Status]bool{
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

// IsValid returns true if the status is a recognized valid value.
func (s Status) IsValid() bool {
	switch s {
	case StatusDraft, StatusActive, StatusDeprecated:
		return true
	default:
		return false
	}
}

// CanTransitionTo checks if a transition from the current status to the target status is valid.
func (s Status) CanTransitionTo(target Status) bool {
	if s == target {
		return false
	}
	allowed, exists := validStatusTransitions[s]
	if !exists {
		return false
	}
	return allowed[target]
}

// String returns the string representation of the status.
func (s Status) String() string {
	return string(s)
}
