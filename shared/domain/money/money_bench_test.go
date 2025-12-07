package money_test

import (
	"testing"

	"github.com/meridianhub/meridian/shared/domain/money"
	"github.com/shopspring/decimal"
)

func BenchmarkMoneyCreation(b *testing.B) {
	amount := decimal.NewFromInt(100)
	currency := money.CurrencyGBP

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = money.New(amount, currency)
	}
}

func BenchmarkMoneyAdd(b *testing.B) {
	money1, _ := money.New(decimal.NewFromInt(100), money.CurrencyGBP)
	money2, _ := money.New(decimal.NewFromInt(50), money.CurrencyGBP)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = money1.Add(money2)
	}
}

func BenchmarkMoneySubtract(b *testing.B) {
	money1, _ := money.New(decimal.NewFromInt(100), money.CurrencyGBP)
	money2, _ := money.New(decimal.NewFromInt(50), money.CurrencyGBP)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = money1.Subtract(money2)
	}
}

func BenchmarkMoneyToMinorUnits(b *testing.B) {
	tests := []struct {
		name     string
		currency money.Currency
	}{
		{"GBP_2dp", money.CurrencyGBP},
		{"USD_2dp", money.CurrencyUSD},
		{"JPY_0dp", money.CurrencyJPY},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			m, _ := money.New(decimal.NewFromFloat(123.45), tt.currency)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = m.ToMinorUnits()
			}
		})
	}
}

func BenchmarkMoneyString(b *testing.B) {
	m, _ := money.New(decimal.NewFromFloat(123.45), money.CurrencyGBP)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.String()
	}
}

func BenchmarkMoneyAddChain(b *testing.B) {
	money1, _ := money.New(decimal.NewFromInt(100), money.CurrencyGBP)
	money2, _ := money.New(decimal.NewFromInt(50), money.CurrencyGBP)
	money3, _ := money.New(decimal.NewFromInt(25), money.CurrencyGBP)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, _ := money1.Add(money2)
		_, _ = result.Add(money3)
	}
}

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
			m, _ := money.New(tt.amount, money.CurrencyGBP)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.IsPositive()
				_ = m.IsNegative()
				_ = m.IsZero()
			}
		})
	}
}

func BenchmarkCurrencyValidation(b *testing.B) {
	currency := money.CurrencyGBP

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = currency.IsValid()
	}
}

func BenchmarkCurrencyDecimalPlaces(b *testing.B) {
	tests := []struct {
		name     string
		currency money.Currency
	}{
		{"GBP", money.CurrencyGBP},
		{"USD", money.CurrencyUSD},
		{"JPY", money.CurrencyJPY},
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
