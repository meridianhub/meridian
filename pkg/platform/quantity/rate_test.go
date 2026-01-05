package quantity

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test instruments for rate tests.
var (
	testUSD  = Instrument{Code: "USD", Version: 1, Dimension: DimensionCurrency, Precision: 2}
	testEUR  = Instrument{Code: "EUR", Version: 1, Dimension: DimensionCurrency, Precision: 2}
	testGBP  = Instrument{Code: "GBP", Version: 1, Dimension: DimensionCurrency, Precision: 2}
	testJPY  = Instrument{Code: "JPY", Version: 1, Dimension: DimensionCurrency, Precision: 0}
	testGold = Instrument{Code: "XAU", Version: 1, Dimension: DimensionCurrency, Precision: 4}
)

func TestNewRate_ValidRates(t *testing.T) {
	tests := []struct {
		name      string
		from      Instrument
		to        Instrument
		factor    decimal.Decimal
		validFrom time.Time
		validTo   time.Time
	}{
		{
			name:   "simple currency conversion",
			from:   testUSD,
			to:     testEUR,
			factor: decimal.NewFromFloat(0.85),
		},
		{
			name:      "with time bounds",
			from:      testUSD,
			to:        testGBP,
			factor:    decimal.NewFromFloat(0.78),
			validFrom: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			validTo:   time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
		},
		{
			name:      "only ValidFrom set",
			from:      testEUR,
			to:        testUSD,
			factor:    decimal.NewFromFloat(1.18),
			validFrom: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "only ValidTo set",
			from:    testGBP,
			to:      testEUR,
			factor:  decimal.NewFromFloat(1.16),
			validTo: time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
		},
		{
			name:      "same ValidFrom and ValidTo",
			from:      testUSD,
			to:        testEUR,
			factor:    decimal.NewFromFloat(0.85),
			validFrom: time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			validTo:   time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "very small factor",
			from:   testJPY,
			to:     testUSD,
			factor: decimal.NewFromFloat(0.0067),
		},
		{
			name:   "very large factor",
			from:   testUSD,
			to:     testJPY,
			factor: decimal.NewFromFloat(149.5),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, err := NewRate(tt.from, tt.to, tt.factor, tt.validFrom, tt.validTo)
			require.NoError(t, err)
			assert.Equal(t, tt.from, rate.From)
			assert.Equal(t, tt.to, rate.To)
			assert.True(t, tt.factor.Equal(rate.Factor))
			assert.Equal(t, tt.validFrom, rate.ValidFrom)
			assert.Equal(t, tt.validTo, rate.ValidTo)
		})
	}
}

func TestNewRate_InvalidRates(t *testing.T) {
	tests := []struct {
		name      string
		from      Instrument
		to        Instrument
		factor    decimal.Decimal
		validFrom time.Time
		validTo   time.Time
		wantErr   error
	}{
		{
			name:    "zero factor",
			from:    testUSD,
			to:      testEUR,
			factor:  decimal.Zero,
			wantErr: ErrRateFactorNotPositive,
		},
		{
			name:    "negative factor",
			from:    testUSD,
			to:      testEUR,
			factor:  decimal.NewFromFloat(-0.85),
			wantErr: ErrRateFactorNotPositive,
		},
		{
			name:    "same instrument with non-identity factor",
			from:    testUSD,
			to:      testUSD,
			factor:  decimal.NewFromFloat(1.5),
			wantErr: ErrRateFromToEqual,
		},
		{
			name:      "ValidFrom after ValidTo",
			from:      testUSD,
			to:        testEUR,
			factor:    decimal.NewFromFloat(0.85),
			validFrom: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
			validTo:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			wantErr:   ErrRateInvalidTimeRange,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRate(tt.from, tt.to, tt.factor, tt.validFrom, tt.validTo)
			require.Error(t, err)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestIdentityRate(t *testing.T) {
	rate := IdentityRate(testUSD)

	assert.Equal(t, testUSD, rate.From)
	assert.Equal(t, testUSD, rate.To)
	assert.True(t, decimal.NewFromInt(1).Equal(rate.Factor))
	assert.True(t, rate.ValidFrom.IsZero())
	assert.True(t, rate.ValidTo.IsZero())

	// Identity rate should pass validation
	err := rate.Validate()
	require.NoError(t, err)
}

func TestRate_Convert_ValidConversions(t *testing.T) {
	tests := []struct {
		name           string
		from           Instrument
		to             Instrument
		factor         string
		inputAmount    string
		expectedAmount string
	}{
		{
			name:           "simple USD to EUR",
			from:           testUSD,
			to:             testEUR,
			factor:         "0.85",
			inputAmount:    "100.00",
			expectedAmount: "85.00",
		},
		{
			name:           "EUR to USD",
			from:           testEUR,
			to:             testUSD,
			factor:         "1.18",
			inputAmount:    "100.00",
			expectedAmount: "118.00",
		},
		{
			name:           "USD to JPY (precision 0)",
			from:           testUSD,
			to:             testJPY,
			factor:         "149.5",
			inputAmount:    "100.00",
			expectedAmount: "14950",
		},
		{
			name:           "Gold to USD (precision 4 to 2)",
			from:           testGold,
			to:             testUSD,
			factor:         "2050.50",
			inputAmount:    "1.0000",
			expectedAmount: "2050.50",
		},
		{
			name:           "large amount conversion",
			from:           testUSD,
			to:             testEUR,
			factor:         "0.85",
			inputAmount:    "1000000.00",
			expectedAmount: "850000.00",
		},
		{
			name:           "small amount conversion",
			from:           testUSD,
			to:             testEUR,
			factor:         "0.85",
			inputAmount:    "0.01",
			expectedAmount: "0.01", // 0.0085 rounds to 0.01 with banker's rounding
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factor, err := decimal.NewFromString(tt.factor)
			require.NoError(t, err)

			rate, err := NewRate(tt.from, tt.to, factor, time.Time{}, time.Time{})
			require.NoError(t, err)

			inputAmount, err := decimal.NewFromString(tt.inputAmount)
			require.NoError(t, err)

			input := NewMoney(inputAmount, tt.from)
			result, err := rate.Convert(input)
			require.NoError(t, err)

			expectedAmount, err := decimal.NewFromString(tt.expectedAmount)
			require.NoError(t, err)

			assert.True(t, expectedAmount.Equal(result.Amount),
				"expected %s, got %s", tt.expectedAmount, result.Amount.String())
			assert.Equal(t, tt.to, result.Instrument)
		})
	}
}

func TestRate_Convert_BankersRounding(t *testing.T) {
	// Banker's rounding: ties round to nearest even
	tests := []struct {
		name           string
		factor         string
		inputAmount    string
		expectedAmount string
		description    string
	}{
		{
			name:           "0.535 rounds to 0.54 (round up to even)",
			factor:         "1",
			inputAmount:    "0.535",
			expectedAmount: "0.54",
			description:    "5 in third decimal with even second decimal rounds up",
		},
		{
			name:           "0.545 rounds to 0.54 (round down to even)",
			factor:         "1",
			inputAmount:    "0.545",
			expectedAmount: "0.54",
			description:    "5 in third decimal with odd second decimal rounds down",
		},
		{
			name:           "0.525 rounds to 0.52 (round down to even)",
			factor:         "1",
			inputAmount:    "0.525",
			expectedAmount: "0.52",
			description:    "5 in third decimal with even second decimal stays",
		},
		{
			name:           "0.555 rounds to 0.56 (round up to even)",
			factor:         "1",
			inputAmount:    "0.555",
			expectedAmount: "0.56",
			description:    "5 in third decimal with odd second decimal rounds up",
		},
		{
			name:           "2.5 stays as 2.50 (already at precision 2)",
			factor:         "1",
			inputAmount:    "2.5",
			expectedAmount: "2.50",
			description:    "2.5 has no digits beyond precision 2, stays 2.50",
		},
		{
			name:           "3.5 stays as 3.50 (already at precision 2)",
			factor:         "1",
			inputAmount:    "3.5",
			expectedAmount: "3.50",
			description:    "3.5 has no digits beyond precision 2, stays 3.50",
		},
		{
			name:           "multiplication result 0.535",
			factor:         "0.535",
			inputAmount:    "1.00",
			expectedAmount: "0.54",
			description:    "Factor produces 0.535 which rounds to 0.54",
		},
		{
			name:           "negative amount banker's rounding",
			factor:         "1",
			inputAmount:    "-0.535",
			expectedAmount: "-0.54",
			description:    "Negative values follow same rounding rules",
		},
	}

	// Use identity-like rate but with conversion to trigger rounding
	from := testUSD
	to := testEUR

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factor, err := decimal.NewFromString(tt.factor)
			require.NoError(t, err)

			// Create rate with From=USD, To=EUR (both precision 2)
			rate, err := NewRate(from, to, factor, time.Time{}, time.Time{})
			require.NoError(t, err)

			inputAmount, err := decimal.NewFromString(tt.inputAmount)
			require.NoError(t, err)

			input := NewMoney(inputAmount, from)
			result, err := rate.Convert(input)
			require.NoError(t, err)

			expectedAmount, err := decimal.NewFromString(tt.expectedAmount)
			require.NoError(t, err)

			assert.True(t, expectedAmount.Equal(result.Amount),
				"%s: expected %s, got %s", tt.description, tt.expectedAmount, result.Amount.String())
		})
	}
}

func TestRate_Convert_PrecisionTransitions(t *testing.T) {
	tests := []struct {
		name           string
		from           Instrument
		to             Instrument
		factor         string
		inputAmount    string
		expectedAmount string
		description    string
	}{
		{
			name:           "Gold (4) to USD (2)",
			from:           testGold,
			to:             testUSD,
			factor:         "2050.1234",
			inputAmount:    "1.5678",
			expectedAmount: "3214.18", // 1.5678 * 2050.1234 = 3214.1835... rounds to 3214.18
			description:    "1.5678 * 2050.1234 = 3214.1835... rounds to 3214.18",
		},
		{
			name:           "USD (2) to JPY (0)",
			from:           testUSD,
			to:             testJPY,
			factor:         "149.5",
			inputAmount:    "10.99",
			expectedAmount: "1643",
			description:    "10.99 * 149.5 = 1643.005 rounds to 1643 (banker's)",
		},
		{
			name:           "JPY (0) to USD (2)",
			from:           testJPY,
			to:             testUSD,
			factor:         "0.0067",
			inputAmount:    "10000",
			expectedAmount: "67.00",
			description:    "10000 * 0.0067 = 67.00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factor, err := decimal.NewFromString(tt.factor)
			require.NoError(t, err)

			rate, err := NewRate(tt.from, tt.to, factor, time.Time{}, time.Time{})
			require.NoError(t, err)

			inputAmount, err := decimal.NewFromString(tt.inputAmount)
			require.NoError(t, err)

			input := NewMoney(inputAmount, tt.from)
			result, err := rate.Convert(input)
			require.NoError(t, err)

			expectedAmount, err := decimal.NewFromString(tt.expectedAmount)
			require.NoError(t, err)

			assert.True(t, expectedAmount.Equal(result.Amount),
				"%s: expected %s, got %s", tt.description, tt.expectedAmount, result.Amount.String())
			assert.Equal(t, tt.to.Precision, result.Instrument.Precision)
		})
	}
}

func TestRate_Convert_InstrumentMismatch(t *testing.T) {
	rate, err := NewRate(testUSD, testEUR, decimal.NewFromFloat(0.85), time.Time{}, time.Time{})
	require.NoError(t, err)

	// Try to convert EUR (should fail - rate expects USD)
	eurAmount := NewMoney(decimal.NewFromFloat(100), testEUR)
	_, err = rate.Convert(eurAmount)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRateInstrumentMismatch)
	assert.Contains(t, err.Error(), "USD")
	assert.Contains(t, err.Error(), "EUR")
}

func TestRate_Convert_IdentityRate(t *testing.T) {
	rate := IdentityRate(testUSD)

	input := NewMoney(decimal.NewFromFloat(123.45), testUSD)
	result, err := rate.Convert(input)

	require.NoError(t, err)
	assert.True(t, input.Amount.Equal(result.Amount))
	assert.Equal(t, testUSD, result.Instrument)
}

func TestRate_Convert_DoesNotMutateInput(t *testing.T) {
	rate, err := NewRate(testUSD, testEUR, decimal.NewFromFloat(0.85), time.Time{}, time.Time{})
	require.NoError(t, err)

	input := NewMoney(decimal.NewFromFloat(100.00), testUSD)
	original := input.Amount

	_, err = rate.Convert(input)
	require.NoError(t, err)

	assert.True(t, original.Equal(input.Amount), "input should not be mutated")
}

func TestRate_Convert_ZeroAndSmallAmounts(t *testing.T) {
	rate, err := NewRate(testUSD, testEUR, decimal.NewFromFloat(0.85), time.Time{}, time.Time{})
	require.NoError(t, err)

	tests := []struct {
		name           string
		inputAmount    string
		expectedAmount string
	}{
		{
			name:           "zero amount",
			inputAmount:    "0.00",
			expectedAmount: "0.00",
		},
		{
			name:           "very small amount that stays non-zero",
			inputAmount:    "0.02",
			expectedAmount: "0.02", // 0.02 * 0.85 = 0.017 rounds to 0.02
		},
		{
			name:           "very small amount that rounds to zero",
			inputAmount:    "0.005",
			expectedAmount: "0.00", // 0.005 * 0.85 = 0.00425 rounds to 0.00
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputAmount, err := decimal.NewFromString(tt.inputAmount)
			require.NoError(t, err)

			input := NewMoney(inputAmount, testUSD)
			result, err := rate.Convert(input)
			require.NoError(t, err)

			expectedAmount, err := decimal.NewFromString(tt.expectedAmount)
			require.NoError(t, err)

			assert.True(t, expectedAmount.Equal(result.Amount),
				"expected %s, got %s", tt.expectedAmount, result.Amount.String())
		})
	}
}

func TestRate_IsValidAt(t *testing.T) {
	jan1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	jun15 := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	dec31 := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)

	tests := []struct {
		name      string
		validFrom time.Time
		validTo   time.Time
		checkTime time.Time
		expected  bool
	}{
		{
			name:      "within bounded range",
			validFrom: jan1,
			validTo:   dec31,
			checkTime: jun15,
			expected:  true,
		},
		{
			name:      "at start of range",
			validFrom: jan1,
			validTo:   dec31,
			checkTime: jan1,
			expected:  true,
		},
		{
			name:      "at end of range",
			validFrom: jan1,
			validTo:   dec31,
			checkTime: dec31,
			expected:  true,
		},
		{
			name:      "before start of range",
			validFrom: jan1,
			validTo:   dec31,
			checkTime: time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
			expected:  false,
		},
		{
			name:      "after end of range",
			validFrom: jan1,
			validTo:   dec31,
			checkTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			expected:  false,
		},
		{
			name:      "no bounds - always valid",
			validFrom: time.Time{},
			validTo:   time.Time{},
			checkTime: jun15,
			expected:  true,
		},
		{
			name:      "only lower bound - within",
			validFrom: jan1,
			validTo:   time.Time{},
			checkTime: jun15,
			expected:  true,
		},
		{
			name:      "only lower bound - before",
			validFrom: jan1,
			validTo:   time.Time{},
			checkTime: time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC),
			expected:  false,
		},
		{
			name:      "only upper bound - within",
			validFrom: time.Time{},
			validTo:   dec31,
			checkTime: jun15,
			expected:  true,
		},
		{
			name:      "only upper bound - after",
			validFrom: time.Time{},
			validTo:   dec31,
			checkTime: time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, err := NewRate(testUSD, testEUR, decimal.NewFromFloat(0.85), tt.validFrom, tt.validTo)
			require.NoError(t, err)

			result := rate.IsValidAt(tt.checkTime)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRate_Validate(t *testing.T) {
	// Test that Validate can be used on manually constructed rates
	t.Run("valid manually constructed rate", func(t *testing.T) {
		rate := Rate{
			From:   testUSD,
			To:     testEUR,
			Factor: decimal.NewFromFloat(0.85),
		}
		err := rate.Validate()
		require.NoError(t, err)
	})

	t.Run("invalid manually constructed rate", func(t *testing.T) {
		rate := Rate{
			From:   testUSD,
			To:     testEUR,
			Factor: decimal.Zero,
		}
		err := rate.Validate()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrRateFactorNotPositive)
	})
}

func TestRate_String(t *testing.T) {
	rate, err := NewRate(testUSD, testEUR, decimal.NewFromFloat(0.85), time.Time{}, time.Time{})
	require.NoError(t, err)

	str := rate.String()
	assert.Contains(t, str, "USD")
	assert.Contains(t, str, "EUR")
	assert.Contains(t, str, "0.85")
}

// Benchmarks

func BenchmarkRate_Convert(b *testing.B) {
	rate, _ := NewRate(testUSD, testEUR, decimal.NewFromFloat(0.85), time.Time{}, time.Time{})
	input := NewMoney(decimal.NewFromFloat(100.00), testUSD)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = rate.Convert(input)
	}
}

func BenchmarkRate_Convert_LargeAmount(b *testing.B) {
	rate, _ := NewRate(testUSD, testEUR, decimal.NewFromFloat(0.85), time.Time{}, time.Time{})
	input := NewMoney(decimal.NewFromFloat(999999999999.99), testUSD)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = rate.Convert(input)
	}
}

func BenchmarkRate_Convert_SmallAmount(b *testing.B) {
	rate, _ := NewRate(testUSD, testEUR, decimal.NewFromFloat(0.85), time.Time{}, time.Time{})
	input := NewMoney(decimal.NewFromFloat(0.01), testUSD)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = rate.Convert(input)
	}
}

func BenchmarkIdentityRate(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = IdentityRate(testUSD)
	}
}

func BenchmarkRate_IsValidAt(b *testing.B) {
	jan1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	dec31 := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)
	checkTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	rate, _ := NewRate(testUSD, testEUR, decimal.NewFromFloat(0.85), jan1, dec31)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rate.IsValidAt(checkTime)
	}
}

func BenchmarkNewRate(b *testing.B) {
	jan1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	dec31 := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)
	factor := decimal.NewFromFloat(0.85)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = NewRate(testUSD, testEUR, factor, jan1, dec31)
	}
}
