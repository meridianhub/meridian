// Package domain re-exports the shared Money type for position-keeping service.
package domain

import (
	"github.com/meridianhub/meridian/shared/domain/money"
	"github.com/shopspring/decimal"
)

// Re-export errors from shared money package
var (
	ErrCurrencyMismatch = money.ErrCurrencyMismatch
	ErrInvalidCurrency  = money.ErrInvalidCurrency
)

// Money is an alias for the shared money.Money type.
type Money = money.Money

// Currency is an alias for the shared money.Currency type.
type Currency = money.Currency

// Currency constants
const (
	CurrencyGBP = money.CurrencyGBP
	CurrencyUSD = money.CurrencyUSD
	CurrencyEUR = money.CurrencyEUR
	CurrencyJPY = money.CurrencyJPY
	CurrencyCHF = money.CurrencyCHF
	CurrencyCAD = money.CurrencyCAD
	CurrencyAUD = money.CurrencyAUD
)

// NewMoney creates a new Money instance with the given decimal amount and currency.
// This is the same signature as the previous implementation.
func NewMoney(amount decimal.Decimal, currency Currency) (Money, error) {
	return money.New(amount, money.Currency(currency))
}

// MustNewMoney creates a Money instance, panicking on invalid currency.
// Use only in tests or when currency is known valid.
func MustNewMoney(amount decimal.Decimal, currency Currency) Money {
	return money.MustNew(amount, money.Currency(currency))
}

// Zero returns a zero Money value for the given currency.
func Zero(currency Currency) (Money, error) {
	return money.Zero(money.Currency(currency))
}
