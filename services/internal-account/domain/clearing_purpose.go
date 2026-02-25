// Package domain contains the core business logic for internal accounts.
package domain

// ClearingPurpose represents the specific purpose of a clearing account.
// Only applicable to accounts of type CLEARING.
type ClearingPurpose string

// Clearing purpose constants for internal accounts.
// These represent different operational purposes for clearing accounts.
const (
	// ClearingPurposeUnspecified indicates no specific clearing purpose.
	// This is the default value for non-CLEARING accounts.
	ClearingPurposeUnspecified ClearingPurpose = "CLEARING_PURPOSE_UNSPECIFIED"

	// ClearingPurposeDeposit is used for clearing incoming deposits.
	ClearingPurposeDeposit ClearingPurpose = "CLEARING_PURPOSE_DEPOSIT"

	// ClearingPurposeWithdrawal is used for clearing outgoing withdrawals.
	ClearingPurposeWithdrawal ClearingPurpose = "CLEARING_PURPOSE_WITHDRAWAL"

	// ClearingPurposeSettlement is used for settling transactions between parties.
	ClearingPurposeSettlement ClearingPurpose = "CLEARING_PURPOSE_SETTLEMENT"

	// ClearingPurposeGeneral is used for general clearing operations.
	ClearingPurposeGeneral ClearingPurpose = "CLEARING_PURPOSE_GENERAL"
)

// validClearingPurposes contains all valid clearing purposes for efficient lookup.
var validClearingPurposes = map[ClearingPurpose]bool{
	ClearingPurposeUnspecified: true,
	ClearingPurposeDeposit:     true,
	ClearingPurposeWithdrawal:  true,
	ClearingPurposeSettlement:  true,
	ClearingPurposeGeneral:     true,
}

// IsValid returns true if the clearing purpose is a recognized valid value.
func (p ClearingPurpose) IsValid() bool {
	return validClearingPurposes[p]
}

// String returns the string representation of the clearing purpose.
func (p ClearingPurpose) String() string {
	return string(p)
}
