package domain

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// ErrCurrencyMismatch is returned when operations are attempted on different currencies.
var ErrCurrencyMismatch = errors.New("currency mismatch")

// Money represents a monetary amount with currency.
// It uses decimal.Decimal for precise arithmetic operations.
type Money struct {
	amount   decimal.Decimal
	currency Currency
}

// NewMoney creates a new Money instance with validation.
func NewMoney(amount decimal.Decimal, currency Currency) (Money, error) {
	if !currency.IsValid() {
		return Money{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, currency)
	}
	return Money{
		amount:   amount,
		currency: currency,
	}, nil
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

// Amount returns the monetary amount.
func (m Money) Amount() decimal.Decimal {
	return m.amount
}

// Currency returns the currency of the monetary amount.
func (m Money) Currency() Currency {
	return m.currency
}
