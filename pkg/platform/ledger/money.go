package ledger

import "github.com/meridianhub/meridian/pkg/platform/types"

// Money is a type alias for Quantity[CurrencyUnit], providing
// backward compatibility with existing code that uses Money types.
type Money = Quantity[CurrencyUnit]

// NewMoney creates a new Money (currency quantity) with the given currency code
// and amount in minor units (e.g., cents for USD).
func NewMoney(currency string, amountCents int64) types.Result[Money] {
	return NewQuantity(CurrencyUnit(currency), amountCents)
}

// NewMoneyFromMajor creates a new Money from a major unit value.
// For example, NewMoneyFromMajor("USD", 100.50) creates $100.50.
func NewMoneyFromMajor(currency string, majorAmount float64) types.Result[Money] {
	return NewQuantityFromMajor(CurrencyUnit(currency), majorAmount)
}

// ZeroMoney returns a zero Money for the given currency.
func ZeroMoney(currency string) Money {
	return Zero(CurrencyUnit(currency))
}

// Currency returns the currency code of a Money value.
func Currency(m Money) string {
	return string(m.Unit())
}
