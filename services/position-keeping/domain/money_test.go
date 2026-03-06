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

			if MoneyCurrency(money) != tt.currency {
				t.Errorf("Expected currency %v, got %v", tt.currency, MoneyCurrency(money))
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

func TestCurrency_DecimalPlaces(t *testing.T) {
	tests := []struct {
		name     string
		currency Currency
		want     int32
	}{
		{"GBP uses 2 decimal places", CurrencyGBP, 2},
		{"USD uses 2 decimal places", CurrencyUSD, 2},
		{"EUR uses 2 decimal places", CurrencyEUR, 2},
		{"JPY uses 0 decimal places", CurrencyJPY, 0},
		{"CHF uses 2 decimal places", CurrencyCHF, 2},
		{"CAD uses 2 decimal places", CurrencyCAD, 2},
		{"AUD uses 2 decimal places", CurrencyAUD, 2},
		{"Unknown currency defaults to 2 decimal places", Currency("XYZ"), 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.currency.DecimalPlaces(); got != tt.want {
				t.Errorf("Currency.DecimalPlaces() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMoney_String(t *testing.T) {
	tests := []struct {
		name     string
		amount   decimal.Decimal
		currency Currency
		expected string
	}{
		{"GBP with decimals", decimal.NewFromFloat(123.45), CurrencyGBP, "123.45 GBP"},
		{"USD whole number", decimal.NewFromInt(100), CurrencyUSD, "100.00 USD"},
		// JPY has 0 decimal places, so no decimals are shown (correct behavior)
		{"JPY zero decimals", decimal.NewFromInt(10000), CurrencyJPY, "10000 JPY"},
		{"negative amount", decimal.NewFromFloat(-50.99), CurrencyEUR, "-50.99 EUR"},
		{"zero amount", decimal.Zero, CurrencyGBP, "0.00 GBP"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := NewMoney(tt.amount, tt.currency)
			if err != nil {
				t.Fatalf("Failed to create money: %v", err)
			}

			result := money.String()
			if result != tt.expected {
				t.Errorf("Expected String() to return %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestMoney_ToMinorUnits(t *testing.T) {
	tests := []struct {
		name     string
		amount   decimal.Decimal
		currency Currency
		want     int64
	}{
		{
			name:     "GBP 100.00 = 10000 pence",
			amount:   decimal.NewFromInt(100),
			currency: CurrencyGBP,
			want:     10000,
		},
		{
			name:     "USD 123.45 = 12345 cents",
			amount:   decimal.NewFromFloat(123.45),
			currency: CurrencyUSD,
			want:     12345,
		},
		{
			name:     "EUR 0.01 = 1 cent",
			amount:   decimal.NewFromFloat(0.01),
			currency: CurrencyEUR,
			want:     1,
		},
		{
			name:     "JPY 1000 = 1000 (no decimals)",
			amount:   decimal.NewFromInt(1000),
			currency: CurrencyJPY,
			want:     1000,
		},
		{
			name:     "JPY 1234.56 rounds to 1235 (no decimals)",
			amount:   decimal.NewFromFloat(1234.56),
			currency: CurrencyJPY,
			want:     1235,
		},
		{
			name:     "negative GBP -50.25 = -5025 pence",
			amount:   decimal.NewFromFloat(-50.25),
			currency: CurrencyGBP,
			want:     -5025,
		},
		{
			name:     "zero amount",
			amount:   decimal.Zero,
			currency: CurrencyUSD,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := NewMoney(tt.amount, tt.currency)
			if err != nil {
				t.Fatalf("NewMoney() error = %v", err)
			}
			got, err := MoneyToMinorUnits(money)
			if err != nil {
				t.Fatalf("MoneyToMinorUnits() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("MoneyToMinorUnits() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewMoneyFromInstrumentCode(t *testing.T) {
	tests := []struct {
		name           string
		amount         decimal.Decimal
		code           string
		wantErr        bool
		expectedCode   string
		expectedAmount decimal.Decimal
	}{
		{
			name:           "valid currency GBP",
			amount:         decimal.NewFromFloat(100.50),
			code:           "GBP",
			expectedCode:   "GBP",
			expectedAmount: decimal.NewFromFloat(100.50),
		},
		{
			name:           "valid currency USD",
			amount:         decimal.NewFromFloat(42.00),
			code:           "USD",
			expectedCode:   "USD",
			expectedAmount: decimal.NewFromFloat(42.00),
		},
		{
			name:           "non-currency instrument KWH",
			amount:         decimal.NewFromFloat(8.54),
			code:           "KWH",
			expectedCode:   "KWH",
			expectedAmount: decimal.NewFromFloat(8.54),
		},
		{
			name:           "non-currency instrument GPU_HOUR",
			amount:         decimal.NewFromFloat(1.25),
			code:           "GPU_HOUR",
			expectedCode:   "GPU_HOUR",
			expectedAmount: decimal.NewFromFloat(1.25),
		},
		{
			name:    "empty code",
			amount:  decimal.NewFromInt(100),
			code:    "",
			wantErr: true,
		},
		{
			name:           "zero amount with KWH",
			amount:         decimal.Zero,
			code:           "KWH",
			expectedCode:   "KWH",
			expectedAmount: decimal.Zero,
		},
		{
			name:           "negative amount with KWH",
			amount:         decimal.NewFromFloat(-5.00),
			code:           "KWH",
			expectedCode:   "KWH",
			expectedAmount: decimal.NewFromFloat(-5.00),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := NewMoneyFromInstrumentCode(tt.amount, tt.code)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if money.Instrument.Code != tt.expectedCode {
				t.Errorf("Expected instrument code %q, got %q", tt.expectedCode, money.Instrument.Code)
			}

			if !money.Amount.Equal(tt.expectedAmount) {
				t.Errorf("Expected amount %v, got %v", tt.expectedAmount, money.Amount)
			}
		})
	}
}

func TestNewMoneyFromInstrumentCode_RoundTrip(t *testing.T) {
	// Verify that KWH values round-trip through the same paths as GBP
	kwhMoney, err := NewMoneyFromInstrumentCode(decimal.NewFromFloat(8.54), "KWH")
	if err != nil {
		t.Fatalf("NewMoneyFromInstrumentCode() error = %v", err)
	}

	// The instrument code should be accessible via MoneyCurrency
	if MoneyCurrency(kwhMoney) != Currency("KWH") {
		t.Errorf("MoneyCurrency() = %q, want %q", MoneyCurrency(kwhMoney), "KWH")
	}

	// IsPositive should work
	if !kwhMoney.IsPositive() {
		t.Error("Expected positive KWH amount")
	}

	// IsZero should work on zero
	zeroKWH, err := NewMoneyFromInstrumentCode(decimal.Zero, "KWH")
	if err != nil {
		t.Fatalf("NewMoneyFromInstrumentCode() error = %v", err)
	}
	if !zeroKWH.IsZero() {
		t.Error("Expected zero KWH amount")
	}
}
