package accounttype

// NormalBalance defines the expected sign of the balance for an account type
// under normal operating conditions.
type NormalBalance string

const (
	// NormalBalanceDebit indicates the account type normally carries a debit balance (assets, expenses).
	NormalBalanceDebit NormalBalance = "DEBIT"

	// NormalBalanceCredit indicates the account type normally carries a credit balance (liabilities, revenue, equity).
	NormalBalanceCredit NormalBalance = "CREDIT"
)

// IsValid returns true if the normal balance is a recognized valid value.
func (n NormalBalance) IsValid() bool {
	switch n {
	case NormalBalanceDebit, NormalBalanceCredit:
		return true
	default:
		return false
	}
}

// String returns the string representation of the normal balance.
func (n NormalBalance) String() string {
	return string(n)
}
