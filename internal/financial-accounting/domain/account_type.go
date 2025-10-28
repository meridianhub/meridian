package domain

// AccountType represents different types of financial accounts.
type AccountType string

// Supported account types for the financial accounting system.
const (
	AccountTypeDebit    AccountType = "DEBIT"    // Debit account (assets, expenses)
	AccountTypeCredit   AccountType = "CREDIT"   // Credit account (liabilities, income)
	AccountTypeVostro   AccountType = "VOSTRO"   // Our account at another bank
	AccountTypeNostro   AccountType = "NOSTRO"   // Another bank's account with us
	AccountTypeCurrent  AccountType = "CURRENT"  // Current account
	AccountTypeSavings  AccountType = "SAVINGS"  // Savings account
)

// IsValid checks if the account type is valid.
func (a AccountType) IsValid() bool {
	switch a {
	case AccountTypeDebit, AccountTypeCredit, AccountTypeVostro, 
	     AccountTypeNostro, AccountTypeCurrent, AccountTypeSavings:
		return true
	}
	return false
}

// String returns the string representation of the account type.
func (a AccountType) String() string {
	return string(a)
}
