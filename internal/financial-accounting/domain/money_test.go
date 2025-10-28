package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestNewMoney(t *testing.T) {
	tests := []struct {
		name        string
		amount      string
		currency    Currency
		shouldError bool
	}{
		{
			name:        "valid GBP amount",
			amount:      "100.50",
			currency:    CurrencyGBP,
			shouldError: false,
		},
		{
			name:        "invalid currency",
			amount:      "100.00",
			currency:    Currency("XXX"),
			shouldError: true,
		},
		{
			name:        "negative amount",
			amount:      "-50.25",
			currency:    CurrencyUSD,
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, _ := decimal.NewFromString(tt.amount)
			money, err := NewMoney(amount, tt.currency)

			if tt.shouldError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if !money.Amount.Equal(amount) {
					t.Errorf("expected amount %v, got %v", amount, money.Amount)
				}
				if money.Currency != tt.currency {
					t.Errorf("expected currency %v, got %v", tt.currency, money.Currency)
				}
			}
		})
	}
}

func TestMoney_Add(t *testing.T) {
	gbp100, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	gbp50, _ := NewMoney(decimal.NewFromInt(50), CurrencyGBP)
	usd100, _ := NewMoney(decimal.NewFromInt(100), CurrencyUSD)

	result, err := gbp100.Add(gbp50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Amount.Equal(decimal.NewFromInt(150)) {
		t.Errorf("expected 150, got %v", result.Amount)
	}

	_, err = gbp100.Add(usd100)
	if err == nil {
		t.Error("expected error when adding different currencies")
	}
}

func TestMoney_Subtract(t *testing.T) {
	gbp100, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	gbp30, _ := NewMoney(decimal.NewFromInt(30), CurrencyGBP)

	result, err := gbp100.Subtract(gbp30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Amount.Equal(decimal.NewFromInt(70)) {
		t.Errorf("expected 70, got %v", result.Amount)
	}
}

func TestMoney_Predicates(t *testing.T) {
	zero, _ := NewMoney(decimal.Zero, CurrencyGBP)
	positive, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	negative, _ := NewMoney(decimal.NewFromInt(-50), CurrencyGBP)

	if !zero.IsZero() {
		t.Error("expected zero to be zero")
	}
	if !positive.IsPositive() {
		t.Error("expected positive to be positive")
	}
	if !negative.IsNegative() {
		t.Error("expected negative to be negative")
	}
}
