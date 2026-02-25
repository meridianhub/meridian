// Package domain contains the core business logic for internal accounts.
package domain

// AccountType represents the type of internal account.
// Internal accounts are used for the bank's own operations, not customer accounts.
type AccountType string

// Account type constants for internal accounts.
// These represent different operational purposes within the bank's own accounting.
const (
	// AccountTypeClearing is used for settling transactions between accounts.
	AccountTypeClearing AccountType = "CLEARING"

	// AccountTypeNostro is an account held at another bank (our account at their bank).
	// Requires a correspondent bank relationship.
	AccountTypeNostro AccountType = "NOSTRO"

	// AccountTypeVostro is an account held for another bank (their account at our bank).
	// Requires a correspondent bank relationship.
	AccountTypeVostro AccountType = "VOSTRO"

	// AccountTypeHolding is used for temporarily holding funds during processing.
	AccountTypeHolding AccountType = "HOLDING"

	// AccountTypeSuspense is used for transactions that cannot be immediately categorized.
	AccountTypeSuspense AccountType = "SUSPENSE"

	// AccountTypeRevenue is used for tracking income and revenue streams.
	AccountTypeRevenue AccountType = "REVENUE"

	// AccountTypeExpense is used for tracking operational expenses.
	AccountTypeExpense AccountType = "EXPENSE"
)

// validAccountTypes contains all valid account types for efficient lookup.
var validAccountTypes = map[AccountType]bool{
	AccountTypeClearing: true,
	AccountTypeNostro:   true,
	AccountTypeVostro:   true,
	AccountTypeHolding:  true,
	AccountTypeSuspense: true,
	AccountTypeRevenue:  true,
	AccountTypeExpense:  true,
}

// IsValid returns true if the account type is a recognized valid type.
func (t AccountType) IsValid() bool {
	return validAccountTypes[t]
}

// String returns the string representation of the account type.
func (t AccountType) String() string {
	return string(t)
}

// RequiresCorrespondent returns true if the account type requires a correspondent bank relationship.
// This applies to NOSTRO and VOSTRO accounts which represent inter-bank relationships.
func (t AccountType) RequiresCorrespondent() bool {
	return t == AccountTypeNostro || t == AccountTypeVostro
}
