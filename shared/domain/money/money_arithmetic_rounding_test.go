package money

import (
	"testing"

	"github.com/shopspring/decimal"
)

// TestMoney_Arithmetic_BankersRounding verifies that all arithmetic operations
// consistently use banker's rounding (IEEE 754 round half to even) when the
// results are converted to minor units for storage or comparison.
//
// Banker's rounding reduces cumulative bias in financial calculations by
// rounding .5 values to the nearest even number:
//   - 2.5 → 2 (even)
//   - 3.5 → 4 (even)
//   - 4.5 → 4 (even)
//   - 5.5 → 6 (even)
//
// This test suite ensures consistent rounding behavior across all money operations.

func TestMoney_Add_BankersRounding(t *testing.T) {
	tests := []struct {
		name          string
		amount1       string
		amount2       string
		currency      Currency
		expectedMinor int64
		description   string
	}{
		{
			name:          "add resulting in .995 rounds to even (10100)",
			amount1:       "50.495",
			amount2:       "50.500",
			currency:      CurrencyGBP,
			expectedMinor: 10100,
			description:   "50.495 + 50.500 = 100.995 → 10100 pence (even)",
		},
		{
			name:          "add resulting in .985 rounds to even (10098)",
			amount1:       "50.485",
			amount2:       "50.500",
			currency:      CurrencyGBP,
			expectedMinor: 10098,
			description:   "50.485 + 50.500 = 100.985 → 10098 pence (even)",
		},
		{
			name:          "add with precise result needs no rounding",
			amount1:       "50.25",
			amount2:       "50.25",
			currency:      CurrencyGBP,
			expectedMinor: 10050,
			description:   "50.25 + 50.25 = 100.50 → 10050 pence (exact)",
		},
		{
			name:          "add small amounts resulting in .5",
			amount1:       "0.245",
			amount2:       "0.250",
			currency:      CurrencyGBP,
			expectedMinor: 50,
			description:   "0.245 + 0.250 = 0.495 → 50 pence (even)",
		},
		{
			name:          "JPY add with .5 rounds to even",
			amount1:       "500.5",
			amount2:       "500.0",
			currency:      CurrencyJPY,
			expectedMinor: 1000,
			description:   "500.5 + 500.0 = 1000.5 → 1000 (even)",
		},
		{
			name:          "JPY add with .5 rounds to even (odd result)",
			amount1:       "500.5",
			amount2:       "501.0",
			currency:      CurrencyJPY,
			expectedMinor: 1002,
			description:   "500.5 + 501.0 = 1001.5 → 1002 (even)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amt1, _ := decimal.NewFromString(tt.amount1)
			amt2, _ := decimal.NewFromString(tt.amount2)

			m1, err := New(amt1, tt.currency)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			m2, err := New(amt2, tt.currency)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			result, err := m1.Add(m2)
			if err != nil {
				t.Fatalf("Add() error = %v", err)
			}

			got, err := result.ToMinorUnits()
			if err != nil {
				t.Fatalf("ToMinorUnits() error = %v", err)
			}

			if got != tt.expectedMinor {
				t.Errorf("%s: ToMinorUnits() = %v, want %v", tt.description, got, tt.expectedMinor)
			}
		})
	}
}

func TestMoney_Subtract_BankersRounding(t *testing.T) {
	tests := []struct {
		name          string
		amount1       string
		amount2       string
		currency      Currency
		expectedMinor int64
		description   string
	}{
		{
			name:          "subtract resulting in .995 rounds to even (10100)",
			amount1:       "200.995",
			amount2:       "100.000",
			currency:      CurrencyGBP,
			expectedMinor: 10100,
			description:   "200.995 - 100.000 = 100.995 → 10100 pence (even)",
		},
		{
			name:          "subtract resulting in .985 rounds to even (10098)",
			amount1:       "200.985",
			amount2:       "100.000",
			currency:      CurrencyGBP,
			expectedMinor: 10098,
			description:   "200.985 - 100.000 = 100.985 → 10098 pence (even)",
		},
		{
			name:          "subtract resulting in negative .995",
			amount1:       "100.000",
			amount2:       "200.995",
			currency:      CurrencyGBP,
			expectedMinor: -10100,
			description:   "100.000 - 200.995 = -100.995 → -10100 pence (even)",
		},
		{
			name:          "subtract with precise result",
			amount1:       "150.75",
			amount2:       "50.25",
			currency:      CurrencyGBP,
			expectedMinor: 10050,
			description:   "150.75 - 50.25 = 100.50 → 10050 pence (exact)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amt1, _ := decimal.NewFromString(tt.amount1)
			amt2, _ := decimal.NewFromString(tt.amount2)

			m1, err := New(amt1, tt.currency)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			m2, err := New(amt2, tt.currency)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			result, err := m1.Subtract(m2)
			if err != nil {
				t.Fatalf("Subtract() error = %v", err)
			}

			got, err := result.ToMinorUnits()
			if err != nil {
				t.Fatalf("ToMinorUnits() error = %v", err)
			}

			if got != tt.expectedMinor {
				t.Errorf("%s: ToMinorUnits() = %v, want %v", tt.description, got, tt.expectedMinor)
			}
		})
	}
}

func TestMoney_Multiply_BankersRounding(t *testing.T) {
	tests := []struct {
		name          string
		amount        string
		factor        string
		currency      Currency
		expectedMinor int64
		description   string
	}{
		{
			name:          "multiply resulting in .995 rounds to even (10100)",
			amount:        "33.665",
			factor:        "3",
			currency:      CurrencyGBP,
			expectedMinor: 10100,
			description:   "33.665 * 3 = 100.995 → 10100 pence (even)",
		},
		{
			name:          "multiply resulting in .985000001 rounds up",
			amount:        "33.661666667",
			factor:        "3",
			currency:      CurrencyGBP,
			expectedMinor: 10099,
			description:   "33.661666667 * 3 = 100.985000001 → 10099 pence (just over .5)",
		},
		{
			name:          "multiply by 1.5 gives exact result",
			amount:        "67.3",
			factor:        "1.5",
			currency:      CurrencyGBP,
			expectedMinor: 10095,
			description:   "67.3 * 1.5 = 100.95 → 10095 pence (exact)",
		},
		{
			name:          "multiply by fraction resulting in .995",
			amount:        "100",
			factor:        "1.00995",
			currency:      CurrencyGBP,
			expectedMinor: 10100,
			description:   "100 * 1.00995 = 100.995 → 10100 pence (even)",
		},
		{
			name:          "multiply percentage rate with .5 rounding",
			amount:        "100.33",
			factor:        "0.01",
			currency:      CurrencyGBP,
			expectedMinor: 100,
			description:   "100.33 * 0.01 = 1.0033 → 100 pence",
		},
		{
			name:          "JPY multiply with .5 rounds to even",
			amount:        "999",
			factor:        "1.5015015",
			currency:      CurrencyJPY,
			expectedMinor: 1500,
			description:   "999 * 1.5015015 = 1500.000015 → 1500 (even)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amt, _ := decimal.NewFromString(tt.amount)
			factor, _ := decimal.NewFromString(tt.factor)

			money, err := New(amt, tt.currency)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			result := money.Multiply(factor)

			got, err := result.ToMinorUnits()
			if err != nil {
				t.Fatalf("ToMinorUnits() error = %v", err)
			}

			if got != tt.expectedMinor {
				t.Errorf("%s: ToMinorUnits() = %v, want %v", tt.description, got, tt.expectedMinor)
			}
		})
	}
}

func TestMoney_Divide_BankersRounding(t *testing.T) {
	tests := []struct {
		name          string
		amount        string
		divisor       string
		currency      Currency
		expectedMinor int64
		description   string
	}{
		{
			name:          "divide resulting in .995 rounds to even (10100)",
			amount:        "302.985",
			divisor:       "3",
			currency:      CurrencyGBP,
			expectedMinor: 10100,
			description:   "302.985 / 3 = 100.995 → 10100 pence (even)",
		},
		{
			name:          "divide resulting in .985 rounds to even (10098)",
			amount:        "302.955",
			divisor:       "3",
			currency:      CurrencyGBP,
			expectedMinor: 10098,
			description:   "302.955 / 3 = 100.985 → 10098 pence (even)",
		},
		{
			name:          "divide by 3 (common splitting scenario)",
			amount:        "100.00",
			divisor:       "3",
			currency:      CurrencyGBP,
			expectedMinor: 3333,
			description:   "100.00 / 3 = 33.333... → 3333 pence",
		},
		{
			name:          "divide exact amount",
			amount:        "150.00",
			divisor:       "3",
			currency:      CurrencyGBP,
			expectedMinor: 5000,
			description:   "150.00 / 3 = 50.00 → 5000 pence (exact)",
		},
		{
			name:          "divide with .5 intermediate result",
			amount:        "100.05",
			divisor:       "2",
			currency:      CurrencyGBP,
			expectedMinor: 5002,
			description:   "100.05 / 2 = 50.025 → 5002 pence (even)",
		},
		{
			name:          "JPY divide with .5 rounds to even",
			amount:        "1003",
			divisor:       "2",
			currency:      CurrencyJPY,
			expectedMinor: 502,
			description:   "1003 / 2 = 501.5 → 502 (even)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amt, _ := decimal.NewFromString(tt.amount)
			divisor, _ := decimal.NewFromString(tt.divisor)

			money, err := New(amt, tt.currency)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			result, err := money.Divide(divisor)
			if err != nil {
				t.Fatalf("Divide() error = %v", err)
			}

			got, err := result.ToMinorUnits()
			if err != nil {
				t.Fatalf("ToMinorUnits() error = %v", err)
			}

			if got != tt.expectedMinor {
				t.Errorf("%s: ToMinorUnits() = %v, want %v", tt.description, got, tt.expectedMinor)
			}
		})
	}
}

func TestMoney_CompoundOperations_BankersRounding(t *testing.T) {
	// Test compound operations to ensure rounding consistency
	// across multiple arithmetic operations
	tests := []struct {
		name          string
		operations    func() (Money, error)
		expectedMinor int64
		description   string
	}{
		{
			name: "add then multiply with .5 rounding",
			operations: func() (Money, error) {
				m1, _ := New(decimal.NewFromFloat(50.25), CurrencyGBP)
				m2, _ := New(decimal.NewFromFloat(50.25), CurrencyGBP)
				sum, err := m1.Add(m2)
				if err != nil {
					return Money{}, err
				}
				return sum.Multiply(decimal.NewFromFloat(1.00995)), nil
			},
			expectedMinor: 10150,
			description:   "(50.25 + 50.25) * 1.00995 = 101.50... → 10150 pence",
		},
		{
			name: "multiply then divide with .995 result",
			operations: func() (Money, error) {
				m, _ := New(decimal.NewFromFloat(33.665), CurrencyGBP)
				mult := m.Multiply(decimal.NewFromInt(3))
				return mult.Divide(decimal.NewFromInt(1))
			},
			expectedMinor: 10100,
			description:   "(33.665 * 3) / 1 = 100.995 → 10100 pence (even)",
		},
		{
			name: "add subtract then multiply",
			operations: func() (Money, error) {
				m1, _ := New(decimal.NewFromFloat(100.00), CurrencyGBP)
				m2, _ := New(decimal.NewFromFloat(0.33167), CurrencyGBP)
				m3, _ := New(decimal.NewFromFloat(0.00167), CurrencyGBP)
				sum, err := m1.Add(m2)
				if err != nil {
					return Money{}, err
				}
				diff, err := sum.Subtract(m3)
				if err != nil {
					return Money{}, err
				}
				return diff.Multiply(decimal.NewFromInt(1)), nil
			},
			expectedMinor: 10033,
			description:   "(100.00 + 0.33167 - 0.00167) * 1 = 100.33 → 10033 pence",
		},
		{
			name: "three-way split with rounding",
			operations: func() (Money, error) {
				total, _ := New(decimal.NewFromFloat(100.00), CurrencyGBP)
				return total.Divide(decimal.NewFromInt(3))
			},
			expectedMinor: 3333,
			description:   "100.00 / 3 = 33.333... → 3333 pence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.operations()
			if err != nil {
				t.Fatalf("operation error = %v", err)
			}

			got, err := result.ToMinorUnits()
			if err != nil {
				t.Fatalf("ToMinorUnits() error = %v", err)
			}

			if got != tt.expectedMinor {
				t.Errorf("%s: ToMinorUnits() = %v, want %v", tt.description, got, tt.expectedMinor)
			}
		})
	}
}

func TestMoney_EdgeCases_BankersRounding(t *testing.T) {
	// Test edge cases specific to banker's rounding behavior
	tests := []struct {
		name          string
		amount        string
		currency      Currency
		expectedMinor int64
		description   string
	}{
		{
			name:          "exactly .5 with odd minor unit (rounds up)",
			amount:        "1.015",
			currency:      CurrencyGBP,
			expectedMinor: 102,
			description:   "1.015 → 102 pence (102 is even, 101 is odd)",
		},
		{
			name:          "exactly .5 with even minor unit (rounds down)",
			amount:        "1.005",
			currency:      CurrencyGBP,
			expectedMinor: 100,
			description:   "1.005 → 100 pence (100 is even, 101 is odd)",
		},
		{
			name:          "very small amount with .5",
			amount:        "0.015",
			currency:      CurrencyGBP,
			expectedMinor: 2,
			description:   "0.015 → 2 pence (2 is even)",
		},
		{
			name:          "negative with .5 (rounds to even magnitude)",
			amount:        "-1.015",
			currency:      CurrencyGBP,
			expectedMinor: -102,
			description:   "-1.015 → -102 pence (102 is even)",
		},
		{
			name:          "JPY with exactly .5",
			amount:        "123.5",
			currency:      CurrencyJPY,
			expectedMinor: 124,
			description:   "123.5 → 124 (124 is even)",
		},
		{
			name:          "many decimal places just over .5",
			amount:        "1.0050000000001",
			currency:      CurrencyGBP,
			expectedMinor: 101,
			description:   "1.0050000000001 → 101 pence (just over .5 rounds up)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amt, _ := decimal.NewFromString(tt.amount)
			money, err := New(amt, tt.currency)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			got, err := money.ToMinorUnits()
			if err != nil {
				t.Fatalf("ToMinorUnits() error = %v", err)
			}

			if got != tt.expectedMinor {
				t.Errorf("%s: ToMinorUnits() = %v, want %v", tt.description, got, tt.expectedMinor)
			}
		})
	}
}

func TestMoney_RoundingConsistency(t *testing.T) {
	// Verify that ToMinorUnits() and ToMinorUnitsUnchecked() produce identical results
	// for banker's rounding across various scenarios
	tests := []struct {
		name     string
		amount   string
		currency Currency
	}{
		{"typical .995 case", "100.995", CurrencyGBP},
		{"typical .985 case", "100.985", CurrencyGBP},
		{"exactly .5 (even)", "1.005", CurrencyGBP},
		{"exactly .5 (odd)", "1.015", CurrencyGBP},
		{"negative .995", "-100.995", CurrencyGBP},
		{"JPY with .5", "1234.5", CurrencyJPY},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amt, _ := decimal.NewFromString(tt.amount)
			money, err := New(amt, tt.currency)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			checked, err := money.ToMinorUnits()
			if err != nil {
				t.Fatalf("ToMinorUnits() error = %v", err)
			}

			unchecked := money.ToMinorUnitsUnchecked()

			if checked != unchecked {
				t.Errorf("Rounding inconsistency: ToMinorUnits()=%v, ToMinorUnitsUnchecked()=%v",
					checked, unchecked)
			}
		})
	}
}
