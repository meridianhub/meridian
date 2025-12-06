// Package domain re-exports the shared Money type for payment-order service.
//
// This replaces the previous cross-service import from current-account/domain,
// providing a unified Money type that all services share.
package domain

import (
	"github.com/meridianhub/meridian/shared/domain/money"
	"github.com/shopspring/decimal"
)

// Re-export errors from shared money package
var (
	ErrInvalidCurrency  = money.ErrInvalidCurrency
	ErrCurrencyMismatch = money.ErrCurrencyMismatch
	ErrOverflow         = money.ErrOverflow
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

// NewMoney creates a new Money instance from a currency string and amount in minor units (cents).
func NewMoney(currency string, amountCents int64) (Money, error) {
	cur, err := money.ParseCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	return money.NewFromMinorUnits(amountCents, cur)
}

// ParseCurrency converts a string to a Currency type with validation.
func ParseCurrency(s string) (Currency, error) {
	return money.ParseCurrency(s)
}

// NewMoneyDecimal creates Money from a decimal amount and Currency type.
// This provides compatibility with services using the decimal-based API
// (position-keeping, financial-accounting).
//
// Example: NewMoneyDecimal(decimal.NewFromInt(100), CurrencyGBP) creates £100.00
func NewMoneyDecimal(amount decimal.Decimal, currency Currency) (Money, error) {
	return money.New(amount, currency)
}

// MustNewMoneyDecimal creates Money from a decimal, panicking on invalid currency.
// Use only in tests or when currency is known valid.
func MustNewMoneyDecimal(amount decimal.Decimal, currency Currency) Money {
	return money.MustNew(amount, currency)
}
