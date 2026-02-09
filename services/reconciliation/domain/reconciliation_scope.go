package domain

// ReconciliationScope defines what is being reconciled.
type ReconciliationScope string

// Supported reconciliation scopes.
const (
	ReconciliationScopeAccount    ReconciliationScope = "ACCOUNT"
	ReconciliationScopeInstrument ReconciliationScope = "INSTRUMENT"
	ReconciliationScopePortfolio  ReconciliationScope = "PORTFOLIO"
	ReconciliationScopeFull       ReconciliationScope = "FULL"
)

// IsValid checks if the scope is a recognized value.
func (s ReconciliationScope) IsValid() bool {
	switch s {
	case ReconciliationScopeAccount, ReconciliationScopeInstrument,
		ReconciliationScopePortfolio, ReconciliationScopeFull:
		return true
	}
	return false
}

// String returns the string representation.
func (s ReconciliationScope) String() string {
	return string(s)
}

// ParseReconciliationScope converts a string to ReconciliationScope.
// Returns ReconciliationScopeAccount for unrecognized values.
func ParseReconciliationScope(s string) ReconciliationScope {
	scope := ReconciliationScope(s)
	if scope.IsValid() {
		return scope
	}
	return ReconciliationScopeAccount
}
