// Package domain re-exports the shared Money type for current-account service.
//
// This file provides backward-compatible aliases and constructors for migrating
// from the previous int64-based Money implementation to the shared decimal-based
// implementation. The AmountCents() method is preserved for API compatibility.
package domain

import (
	"github.com/meridianhub/meridian/shared/domain/money"
)

// Re-export errors from shared money package
var (
	ErrInvalidCurrency  = money.ErrInvalidCurrency
	ErrCurrencyMismatch = money.ErrCurrencyMismatch
	ErrAmountOverflow   = money.ErrOverflow
)

// Money is an alias for the shared money.Money type.
// This provides a unified Money type across all services.
type Money = money.Money

// NewMoney creates a new Money instance from a currency string and amount in minor units (cents).
// This preserves backward compatibility with the previous int64-based API.
//
// Example: NewMoney("GBP", 10000) creates £100.00
func NewMoney(currency string, amountCents int64) (Money, error) {
	cur, err := money.ParseCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	return money.NewFromMinorUnits(amountCents, cur)
}

// NewMoneyFromMajorUnits creates Money from major units (pounds, dollars, etc.).
// This is the preferred constructor for new code.
//
// Example: NewMoneyFromMajorUnits("GBP", 100) creates £100.00
func NewMoneyFromMajorUnits(currency string, amount int64) (Money, error) {
	cur, err := money.ParseCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	return money.NewFromInt64(amount, cur)
}

// Currency is an alias for the shared money.Currency type.
type Currency = money.Currency

// Currency constants for supported ISO 4217 currencies.
const (
	CurrencyGBP = money.CurrencyGBP
	CurrencyUSD = money.CurrencyUSD
	CurrencyEUR = money.CurrencyEUR
	CurrencyJPY = money.CurrencyJPY
	CurrencyCHF = money.CurrencyCHF
	CurrencyCAD = money.CurrencyCAD
	CurrencyAUD = money.CurrencyAUD
)
