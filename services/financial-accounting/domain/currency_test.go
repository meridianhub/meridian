package domain

import "testing"

func TestCurrency_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		currency Currency
		expected bool
	}{
		{"valid GBP", CurrencyGBP, true},
		{"valid USD", CurrencyUSD, true},
		{"valid EUR", CurrencyEUR, true},
		{"invalid XXX", Currency("XXX"), false},
		{"empty string", Currency(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.currency.IsValid(); got != tt.expected {
				t.Errorf("IsValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseCurrency(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    Currency
		shouldError bool
	}{
		{"valid GBP", "GBP", CurrencyGBP, false},
		{"valid USD", "USD", CurrencyUSD, false},
		{"invalid XXX", "XXX", "", true},
		{"lowercase gbp", "gbp", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCurrency(tt.input)
			if tt.shouldError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if got != tt.expected {
					t.Errorf("ParseCurrency() = %v, want %v", got, tt.expected)
				}
			}
		})
	}
}
