package domain

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// ErrCurrencyMismatch is returned when operations are attempted on different currencies.
var ErrCurrencyMismatch = errors.New("currency mismatch")

// Currency represents an ISO 4217 currency code.
type Currency string

// Supported currencies for Position Keeping.
const (
	CurrencyGBP Currency = "GBP"
	CurrencyUSD Currency = "USD"
	CurrencyEUR Currency = "EUR"
	CurrencyJPY Currency = "JPY"
	CurrencyCHF Currency = "CHF"
	CurrencyCAD Currency = "CAD"
	CurrencyAUD Currency = "AUD"
)

// ErrInvalidCurrency is returned when an invalid currency is provided.
var ErrInvalidCurrency = errors.New("invalid currency")

// IsValid checks if the currency is valid.
func (c Currency) IsValid() bool {
	switch c {
	case CurrencyGBP, CurrencyUSD, CurrencyEUR, CurrencyJPY, CurrencyCHF, CurrencyCAD, CurrencyAUD:
		return true
	}
	return false
}

// String returns the string representation of the currency.
func (c Currency) String() string {
	return string(c)
}

// Money represents a monetary amount with currency.
// It uses decimal.Decimal for precise arithmetic operations.
type Money struct {
	amount   decimal.Decimal
	currency Currency
}

// NewMoney creates a Money value with the given amount and currency.
// It returns an error wrapping ErrInvalidCurrency that includes the invalid currency if the currency is not supported.
func NewMoney(amount decimal.Decimal, currency Currency) (Money, error) {
	if !currency.IsValid() {
		return Money{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, currency)
	}
	return Money{
		amount:   amount,
		currency: currency,
	}, nil
}

// Amount returns the monetary amount.
func (m Money) Amount() decimal.Decimal {
	return m.amount
}

// Currency returns the currency of the monetary amount.
func (m Money) Currency() Currency {
	return m.currency
}

// Add adds two Money values. They must have the same currency.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("%w: cannot add %s and %s",
			ErrCurrencyMismatch, m.currency, other.currency)
	}
	return Money{
		amount:   m.amount.Add(other.amount),
		currency: m.currency,
	}, nil
}

// Subtract subtracts another Money value from this one. They must have the same currency.
func (m Money) Subtract(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("%w: cannot subtract %s and %s",
			ErrCurrencyMismatch, m.currency, other.currency)
	}
	return Money{
		amount:   m.amount.Sub(other.amount),
		currency: m.currency,
	}, nil
}

// IsZero checks if the amount is zero.
func (m Money) IsZero() bool {
	return m.amount.IsZero()
}

// IsPositive checks if the amount is positive.
func (m Money) IsPositive() bool {
	return m.amount.GreaterThan(decimal.Zero)
}

// IsNegative checks if the amount is negative.
func (m Money) IsNegative() bool {
	return m.amount.LessThan(decimal.Zero)
}

// String returns a string representation of the Money.
func (m Money) String() string {
	return fmt.Sprintf("%s %s", m.amount.StringFixed(2), m.currency)
}