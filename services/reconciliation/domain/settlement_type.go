package domain

// SettlementType defines the type of settlement being performed.
type SettlementType string

// Supported settlement types.
const (
	SettlementTypeDaily    SettlementType = "DAILY"
	SettlementTypeWeekly   SettlementType = "WEEKLY"
	SettlementTypeMonthly  SettlementType = "MONTHLY"
	SettlementTypeOnDemand SettlementType = "ON_DEMAND"
	SettlementTypeEndOfDay SettlementType = "END_OF_DAY"
	SettlementTypeRealTime SettlementType = "REAL_TIME"
)

// IsValid checks if the settlement type is a recognized value.
func (s SettlementType) IsValid() bool {
	switch s {
	case SettlementTypeDaily, SettlementTypeWeekly, SettlementTypeMonthly,
		SettlementTypeOnDemand, SettlementTypeEndOfDay, SettlementTypeRealTime:
		return true
	}
	return false
}

// String returns the string representation.
func (s SettlementType) String() string {
	return string(s)
}

// ParseSettlementType converts a string to SettlementType.
// Returns SettlementTypeOnDemand for unrecognized values.
func ParseSettlementType(s string) SettlementType {
	st := SettlementType(s)
	if st.IsValid() {
		return st
	}
	return SettlementTypeOnDemand
}
