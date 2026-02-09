package domain

import "testing"

func TestSettlementType_IsValid(t *testing.T) {
	tests := []struct {
		name string
		st   SettlementType
		want bool
	}{
		{"valid daily", SettlementTypeDaily, true},
		{"valid weekly", SettlementTypeWeekly, true},
		{"valid monthly", SettlementTypeMonthly, true},
		{"valid on demand", SettlementTypeOnDemand, true},
		{"valid end of day", SettlementTypeEndOfDay, true},
		{"valid real time", SettlementTypeRealTime, true},
		{"invalid", SettlementType("INVALID"), false},
		{"empty", SettlementType(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.st.IsValid(); got != tt.want {
				t.Errorf("SettlementType.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseSettlementType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected SettlementType
	}{
		{"valid daily", "DAILY", SettlementTypeDaily},
		{"valid weekly", "WEEKLY", SettlementTypeWeekly},
		{"valid real time", "REAL_TIME", SettlementTypeRealTime},
		{"invalid defaults to on demand", "INVALID", SettlementTypeOnDemand},
		{"empty defaults to on demand", "", SettlementTypeOnDemand},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseSettlementType(tt.input); got != tt.expected {
				t.Errorf("ParseSettlementType(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
