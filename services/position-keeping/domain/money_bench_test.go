// Package domain_test provides performance benchmarks for Money operations.
//
// These benchmarks measure hot-path operations to ensure sub-microsecond performance
// for financial calculations. Target metrics:
//   - Money creation: <10 ns/op with 0 allocations
//   - Arithmetic operations: <100 ns/op with minimal allocations
//   - Currency conversions: <50 ns/op
//
// Run with: go test -bench=BenchmarkMoney -benchmem -benchtime=10s
package domain_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
)

// BenchmarkMoneyCreation benchmarks the creation of Money instances.
func BenchmarkMoneyCreation(b *testing.B) {
	amount := decimal.NewFromInt(100)
	currency := domain.CurrencyGBP

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = domain.NewMoney(amount, currency)
	}
}

// BenchmarkMoneyAdd benchmarks adding two Money values.
func BenchmarkMoneyAdd(b *testing.B) {
	money1, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	money2, _ := domain.NewMoney(decimal.NewFromInt(50), domain.CurrencyGBP)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = money1.Add(money2)
	}
}

// BenchmarkMoneySubtract benchmarks subtracting two Money values.
func BenchmarkMoneySubtract(b *testing.B) {
	money1, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	money2, _ := domain.NewMoney(decimal.NewFromInt(50), domain.CurrencyGBP)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = money1.Subtract(money2)
	}
}

// BenchmarkMoneyToMinorUnits benchmarks converting Money to minor units.
func BenchmarkMoneyToMinorUnits(b *testing.B) {
	tests := []struct {
		name     string
		currency domain.Currency
	}{
		{"GBP_2dp", domain.CurrencyGBP},
		{"USD_2dp", domain.CurrencyUSD},
		{"JPY_0dp", domain.CurrencyJPY},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			money, _ := domain.NewMoney(decimal.NewFromFloat(123.45), tt.currency)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = money.ToMinorUnits()
			}
		})
	}
}

// BenchmarkMoneyString benchmarks converting Money to string.
func BenchmarkMoneyString(b *testing.B) {
	money, _ := domain.NewMoney(decimal.NewFromFloat(123.45), domain.CurrencyGBP)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = money.String()
	}
}

// BenchmarkMoneyAddChain benchmarks a chain of Money additions.
func BenchmarkMoneyAddChain(b *testing.B) {
	money1, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	money2, _ := domain.NewMoney(decimal.NewFromInt(50), domain.CurrencyGBP)
	money3, _ := domain.NewMoney(decimal.NewFromInt(25), domain.CurrencyGBP)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, _ := money1.Add(money2)
		_, _ = result.Add(money3)
	}
}

// BenchmarkMoneyValidation benchmarks validation methods.
func BenchmarkMoneyValidation(b *testing.B) {
	tests := []struct {
		name   string
		amount decimal.Decimal
	}{
		{"positive", decimal.NewFromInt(100)},
		{"negative", decimal.NewFromInt(-100)},
		{"zero", decimal.Zero},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			money, _ := domain.NewMoney(tt.amount, domain.CurrencyGBP)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = money.IsPositive()
				_ = money.IsNegative()
				_ = money.IsZero()
			}
		})
	}
}

// BenchmarkCurrencyValidation benchmarks currency validation.
func BenchmarkCurrencyValidation(b *testing.B) {
	currency := domain.CurrencyGBP

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = currency.IsValid()
	}
}

// BenchmarkCurrencyDecimalPlaces benchmarks getting decimal places for currencies.
func BenchmarkCurrencyDecimalPlaces(b *testing.B) {
	tests := []struct {
		name     string
		currency domain.Currency
	}{
		{"GBP", domain.CurrencyGBP},
		{"USD", domain.CurrencyUSD},
		{"JPY", domain.CurrencyJPY},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tt.currency.DecimalPlaces()
			}
		})
	}
}
