package domain

// AssertionScope defines the scope of a balance assertion check.
type AssertionScope string

const (
	// AssertionScopePositionLedger verifies a single account's position ledger.
	AssertionScopePositionLedger AssertionScope = "POSITION_LEDGER"
	// AssertionScopeCrossAccount performs system-wide balance verification per instrument.
	AssertionScopeCrossAccount AssertionScope = "CROSS_ACCOUNT"
	// AssertionScopeNostroVostro verifies nostro/vostro account reconciliation.
	AssertionScopeNostroVostro AssertionScope = "NOSTRO_VOSTRO"
)

// IsValid checks if the assertion scope is a recognized value.
func (s AssertionScope) IsValid() bool {
	switch s {
	case AssertionScopePositionLedger, AssertionScopeCrossAccount, AssertionScopeNostroVostro:
		return true
	}
	return false
}

// String returns the string representation.
func (s AssertionScope) String() string {
	return string(s)
}

// ParseAssertionScope converts a string to AssertionScope.
// Returns AssertionScopePositionLedger for unrecognized values.
func ParseAssertionScope(s string) AssertionScope {
	scope := AssertionScope(s)
	if scope.IsValid() {
		return scope
	}
	return AssertionScopePositionLedger
}
