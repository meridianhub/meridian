package accounttype

// BehaviorClass categorizes the accounting and operational behavior of an account type.
type BehaviorClass string

const (
	// BehaviorClassCustomer represents customer-facing accounts.
	BehaviorClassCustomer BehaviorClass = "CUSTOMER"

	// BehaviorClassClearing represents clearing accounts used to temporarily hold funds.
	BehaviorClassClearing BehaviorClass = "CLEARING"

	// BehaviorClassNostro represents nostro accounts (our account at another bank).
	BehaviorClassNostro BehaviorClass = "NOSTRO"

	// BehaviorClassVostro represents vostro accounts (another bank's account at us).
	BehaviorClassVostro BehaviorClass = "VOSTRO"

	// BehaviorClassHolding represents holding accounts for safekeeping of assets.
	BehaviorClassHolding BehaviorClass = "HOLDING"

	// BehaviorClassSuspense represents suspense accounts for unresolved transactions.
	BehaviorClassSuspense BehaviorClass = "SUSPENSE"

	// BehaviorClassRevenue represents revenue accounts for income tracking.
	BehaviorClassRevenue BehaviorClass = "REVENUE"

	// BehaviorClassExpense represents expense accounts for cost tracking.
	BehaviorClassExpense BehaviorClass = "EXPENSE"

	// BehaviorClassInventory represents inventory accounts for asset tracking.
	BehaviorClassInventory BehaviorClass = "INVENTORY"
)

// IsValid returns true if the behavior class is a recognized valid value.
func (b BehaviorClass) IsValid() bool {
	switch b {
	case BehaviorClassCustomer,
		BehaviorClassClearing,
		BehaviorClassNostro,
		BehaviorClassVostro,
		BehaviorClassHolding,
		BehaviorClassSuspense,
		BehaviorClassRevenue,
		BehaviorClassExpense,
		BehaviorClassInventory:
		return true
	default:
		return false
	}
}

// String returns the string representation of the behavior class.
func (b BehaviorClass) String() string {
	return string(b)
}
