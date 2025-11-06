package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestNewMoney(t *testing.T) {
	tests := []struct {
		name        string
		amount      decimal.Decimal
		currency    Currency
		wantErr     bool
		expectedErr error
	}{
		{
			name:     "valid GBP money",
			amount:   decimal.NewFromInt(100),
			currency: CurrencyGBP,
			wantErr:  false,
		},
		{
			name:     "valid USD money",
			amount:   decimal.NewFromFloat(123.45),
			currency: CurrencyUSD,
			wantErr:  false,
		},
		{
			name:        "invalid currency",
			amount:      decimal.NewFromInt(100),
			currency:    Currency("INVALID"),
			wantErr:     true,
			expectedErr: ErrInvalidCurrency,
		},
		{
			name:     "zero amount",
			amount:   decimal.Zero,
			currency: CurrencyEUR,
			wantErr:  false,
		},
		{
			name:     "negative amount",
			amount:   decimal.NewFromInt(-100),
			currency: CurrencyGBP,
			wantErr:  false, // Money can be negative for balances
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := NewMoney(tt.amount, tt.currency)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !money.Amount.Equal(tt.amount) {
				t.Errorf("Expected amount %v, got %v", tt.amount, money.Amount)
			}

			if money.Currency != tt.currency {
				t.Errorf("Expected currency %v, got %v", tt.currency, money.Currency)
			}
		})
	}
}

func TestMoney_Add(t *testing.T) {
	gbp100, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	gbp50, _ := NewMoney(decimal.NewFromInt(50), CurrencyGBP)
	usd100, _ := NewMoney(decimal.NewFromInt(100), CurrencyUSD)

	tests := []struct {
		name           string
		money          Money
		other          Money
		wantErr        bool
		expectedAmount decimal.Decimal
	}{
		{
			name:           "add same currency",
			money:          gbp100,
			other:          gbp50,
			wantErr:        false,
			expectedAmount: decimal.NewFromInt(150),
		},
		{
			name:    "add different currency",
			money:   gbp100,
			other:   usd100,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.money.Add(tt.other)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !result.Amount.Equal(tt.expectedAmount) {
				t.Errorf("Expected amount %v, got %v", tt.expectedAmount, result.Amount)
			}
		})
	}
}

func TestMoney_Subtract(t *testing.T) {
	gbp100, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	gbp50, _ := NewMoney(decimal.NewFromInt(50), CurrencyGBP)
	usd100, _ := NewMoney(decimal.NewFromInt(100), CurrencyUSD)

	tests := []struct {
		name           string
		money          Money
		other          Money
		wantErr        bool
		expectedAmount decimal.Decimal
	}{
		{
			name:           "subtract same currency",
			money:          gbp100,
			other:          gbp50,
			wantErr:        false,
			expectedAmount: decimal.NewFromInt(50),
		},
		{
			name:    "subtract different currency",
			money:   gbp100,
			other:   usd100,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.money.Subtract(tt.other)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !result.Amount.Equal(tt.expectedAmount) {
				t.Errorf("Expected amount %v, got %v", tt.expectedAmount, result.Amount)
			}
		})
	}
}

func TestMoney_IsZero(t *testing.T) {
	zero, _ := NewMoney(decimal.Zero, CurrencyGBP)
	positive, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	negative, _ := NewMoney(decimal.NewFromInt(-100), CurrencyGBP)

	if !zero.IsZero() {
		t.Error("Expected IsZero to be true for zero amount")
	}

	if positive.IsZero() {
		t.Error("Expected IsZero to be false for positive amount")
	}

	if negative.IsZero() {
		t.Error("Expected IsZero to be false for negative amount")
	}
}

func TestMoney_IsPositive(t *testing.T) {
	zero, _ := NewMoney(decimal.Zero, CurrencyGBP)
	positive, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	negative, _ := NewMoney(decimal.NewFromInt(-100), CurrencyGBP)

	if zero.IsPositive() {
		t.Error("Expected IsPositive to be false for zero amount")
	}

	if !positive.IsPositive() {
		t.Error("Expected IsPositive to be true for positive amount")
	}

	if negative.IsPositive() {
		t.Error("Expected IsPositive to be false for negative amount")
	}
}

func TestMoney_IsNegative(t *testing.T) {
	zero, _ := NewMoney(decimal.Zero, CurrencyGBP)
	positive, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	negative, _ := NewMoney(decimal.NewFromInt(-100), CurrencyGBP)

	if zero.IsNegative() {
		t.Error("Expected IsNegative to be false for zero amount")
	}

	if positive.IsNegative() {
		t.Error("Expected IsNegative to be false for positive amount")
	}

	if !negative.IsNegative() {
		t.Error("Expected IsNegative to be true for negative amount")
	}
}

func TestCurrency_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		currency Currency
		want     bool
	}{
		{"valid GBP", CurrencyGBP, true},
		{"valid USD", CurrencyUSD, true},
		{"valid EUR", CurrencyEUR, true},
		{"valid JPY", CurrencyJPY, true},
		{"valid CHF", CurrencyCHF, true},
		{"valid CAD", CurrencyCAD, true},
		{"valid AUD", CurrencyAUD, true},
		{"invalid currency", Currency("INVALID"), false},
		{"empty currency", Currency(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.currency.IsValid(); got != tt.want {
				t.Errorf("Currency.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}
