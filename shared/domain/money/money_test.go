package money

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
)

func TestNew(t *testing.T) {
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
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := New(tt.amount, tt.currency)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !money.Amount().Equal(tt.amount) {
				t.Errorf("Expected amount %v, got %v", tt.amount, money.Amount())
			}

			if money.Currency() != tt.currency {
				t.Errorf("Expected currency %v, got %v", tt.currency, money.Currency())
			}
		})
	}
}

func TestMustNew(t *testing.T) {
	t.Run("valid currency succeeds", func(t *testing.T) {
		m := MustNew(decimal.NewFromInt(100), CurrencyGBP)
		if !m.Amount().Equal(decimal.NewFromInt(100)) {
			t.Error("MustNew failed to create money")
		}
	})

	t.Run("invalid currency panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("MustNew should panic on invalid currency")
			}
		}()
		MustNew(decimal.NewFromInt(100), Currency("INVALID"))
	})
}

func TestNewFromInt64(t *testing.T) {
	m, err := NewFromInt64(100, CurrencyGBP)
	if err != nil {
		t.Fatalf("NewFromInt64 failed: %v", err)
	}
	if !m.Amount().Equal(decimal.NewFromInt(100)) {
		t.Errorf("Expected 100, got %v", m.Amount())
	}
}

func TestNewFromMinorUnits(t *testing.T) {
	tests := []struct {
		name       string
		minorUnits int64
		currency   Currency
		wantAmount string
	}{
		{"GBP 10000 pence = £100.00", 10000, CurrencyGBP, "100"},
		{"USD 12345 cents = $123.45", 12345, CurrencyUSD, "123.45"},
		{"JPY 1000 = ¥1000", 1000, CurrencyJPY, "1000"},
		{"negative pence", -5025, CurrencyGBP, "-50.25"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewFromMinorUnits(tt.minorUnits, tt.currency)
			if err != nil {
				t.Fatalf("NewFromMinorUnits failed: %v", err)
			}
			expected, _ := decimal.NewFromString(tt.wantAmount)
			if !m.Amount().Equal(expected) {
				t.Errorf("Expected %s, got %v", tt.wantAmount, m.Amount())
			}
		})
	}
}

func TestMoney_Add(t *testing.T) {
	gbp100, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	gbp50, _ := New(decimal.NewFromInt(50), CurrencyGBP)
	usd100, _ := New(decimal.NewFromInt(100), CurrencyUSD)

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

			if !result.Amount().Equal(tt.expectedAmount) {
				t.Errorf("Expected amount %v, got %v", tt.expectedAmount, result.Amount())
			}
		})
	}
}

func TestMoney_Subtract(t *testing.T) {
	gbp100, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	gbp50, _ := New(decimal.NewFromInt(50), CurrencyGBP)
	usd100, _ := New(decimal.NewFromInt(100), CurrencyUSD)

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
		{
			name:           "subtract resulting in negative",
			money:          gbp50,
			other:          gbp100,
			wantErr:        false,
			expectedAmount: decimal.NewFromInt(-50),
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

			if !result.Amount().Equal(tt.expectedAmount) {
				t.Errorf("Expected amount %v, got %v", tt.expectedAmount, result.Amount())
			}
		})
	}
}

func TestMoney_Negate(t *testing.T) {
	positive, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	negative, _ := New(decimal.NewFromInt(-100), CurrencyGBP)
	zero, _ := Zero(CurrencyGBP)

	if !positive.Negate().Amount().Equal(decimal.NewFromInt(-100)) {
		t.Error("Negate of positive should be negative")
	}
	if !negative.Negate().Amount().Equal(decimal.NewFromInt(100)) {
		t.Error("Negate of negative should be positive")
	}
	if !zero.Negate().IsZero() {
		t.Error("Negate of zero should be zero")
	}
}

func TestMoney_Abs(t *testing.T) {
	positive, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	negative, _ := New(decimal.NewFromInt(-100), CurrencyGBP)

	if !positive.Abs().Amount().Equal(decimal.NewFromInt(100)) {
		t.Error("Abs of positive should be positive")
	}
	if !negative.Abs().Amount().Equal(decimal.NewFromInt(100)) {
		t.Error("Abs of negative should be positive")
	}
}

func TestMoney_IsZero(t *testing.T) {
	zero, _ := Zero(CurrencyGBP)
	positive, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	negative, _ := New(decimal.NewFromInt(-100), CurrencyGBP)

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
	zero, _ := Zero(CurrencyGBP)
	positive, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	negative, _ := New(decimal.NewFromInt(-100), CurrencyGBP)

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
	zero, _ := Zero(CurrencyGBP)
	positive, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	negative, _ := New(decimal.NewFromInt(-100), CurrencyGBP)

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

func TestMoney_Equals(t *testing.T) {
	gbp100a, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	gbp100b, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	gbp50, _ := New(decimal.NewFromInt(50), CurrencyGBP)
	usd100, _ := New(decimal.NewFromInt(100), CurrencyUSD)

	if !gbp100a.Equals(gbp100b) {
		t.Error("Same amounts and currency should be equal")
	}
	if gbp100a.Equals(gbp50) {
		t.Error("Different amounts should not be equal")
	}
	if gbp100a.Equals(usd100) {
		t.Error("Different currencies should not be equal")
	}
}

func TestMoney_Compare(t *testing.T) {
	gbp100, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	gbp50, _ := New(decimal.NewFromInt(50), CurrencyGBP)
	gbp100b, _ := New(decimal.NewFromInt(100), CurrencyGBP)
	usd100, _ := New(decimal.NewFromInt(100), CurrencyUSD)

	cmp, err := gbp100.Compare(gbp50)
	if err != nil || cmp != 1 {
		t.Error("100 > 50")
	}

	cmp, err = gbp50.Compare(gbp100)
	if err != nil || cmp != -1 {
		t.Error("50 < 100")
	}

	cmp, err = gbp100.Compare(gbp100b)
	if err != nil || cmp != 0 {
		t.Error("100 == 100")
	}

	_, err = gbp100.Compare(usd100)
	if err == nil {
		t.Error("Should error on currency mismatch")
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

func TestParseCurrency(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Currency
		wantErr bool
	}{
		{"valid GBP", "GBP", CurrencyGBP, false},
		{"valid USD", "USD", CurrencyUSD, false},
		{"invalid", "INVALID", "", true},
		{"empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCurrency(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCurrency() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseCurrency() = %v, want %v", got, tt.want)
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
		{"JPY zero decimals", decimal.NewFromInt(10000), CurrencyJPY, "10000.00 JPY"},
		{"negative amount", decimal.NewFromFloat(-50.99), CurrencyEUR, "-50.99 EUR"},
		{"zero amount", decimal.Zero, CurrencyGBP, "0.00 GBP"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := New(tt.amount, tt.currency)
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

func TestMoney_StringWithPrecision(t *testing.T) {
	tests := []struct {
		name     string
		amount   decimal.Decimal
		currency Currency
		expected string
	}{
		{"GBP with decimals", decimal.NewFromFloat(123.45), CurrencyGBP, "123.45 GBP"},
		{"JPY no decimals", decimal.NewFromInt(10000), CurrencyJPY, "10000 JPY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := New(tt.amount, tt.currency)
			if err != nil {
				t.Fatalf("Failed to create money: %v", err)
			}

			result := money.StringWithPrecision()
			if result != tt.expected {
				t.Errorf("Expected StringWithPrecision() to return %q, got %q", tt.expected, result)
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
		wantErr  bool
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
			name:     "JPY 1234.56 rounds to 1234 (no decimals)",
			amount:   decimal.NewFromFloat(1234.56),
			currency: CurrencyJPY,
			want:     1234,
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
			money, err := New(tt.amount, tt.currency)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			got, err := money.ToMinorUnits()
			if (err != nil) != tt.wantErr {
				t.Errorf("ToMinorUnits() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ToMinorUnits() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMoney_ToMinorUnits_Overflow(t *testing.T) {
	// Create a very large amount that would overflow when converted to minor units
	largeAmount := decimal.NewFromInt(math.MaxInt64).Div(decimal.NewFromInt(10))
	money, err := New(largeAmount, CurrencyGBP)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = money.ToMinorUnits()
	if err == nil {
		t.Error("Expected overflow error for very large amount")
	}

	// Test negative overflow
	negLargeAmount := decimal.NewFromInt(math.MinInt64).Div(decimal.NewFromInt(10))
	money2, err := New(negLargeAmount, CurrencyGBP)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = money2.ToMinorUnits()
	if err == nil {
		t.Error("Expected overflow error for very large negative amount")
	}
}

func TestZero(t *testing.T) {
	z, err := Zero(CurrencyGBP)
	if err != nil {
		t.Fatalf("Zero() error = %v", err)
	}
	if !z.IsZero() {
		t.Error("Zero should return zero amount")
	}
	if z.Currency() != CurrencyGBP {
		t.Error("Zero should preserve currency")
	}

	_, err = Zero(Currency("INVALID"))
	if err == nil {
		t.Error("Zero should reject invalid currency")
	}
}
