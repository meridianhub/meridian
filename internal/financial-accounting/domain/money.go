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
	Amount   decimal.Decimal
	Currency Currency
}

// NewMoney creates a new Money instance with validation.
func NewMoney(amount decimal.Decimal, currency Currency) (Money, error) {
	if !currency.IsValid() {
		return Money{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, currency)
	}
	return Money{
		Amount:   amount,
		Currency: currency,
	}, nil
}

// Add adds two Money values. They must have the same currency.
func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("%w: cannot add %s and %s",
			ErrCurrencyMismatch, m.Currency, other.Currency)
	}
	return Money{
		Amount:   m.Amount.Add(other.Amount),
		Currency: m.Currency,
	}, nil
}

// Subtract subtracts another Money value from this one. They must have the same currency.
func (m Money) Subtract(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("%w: cannot subtract %s and %s",
			ErrCurrencyMismatch, m.Currency, other.Currency)
	}
	return Money{
		Amount:   m.Amount.Sub(other.Amount),
		Currency: m.Currency,
	}, nil
}

// IsZero checks if the amount is zero.
func (m Money) IsZero() bool {
	return m.Amount.IsZero()
}

// IsPositive checks if the amount is positive.
func (m Money) IsPositive() bool {
	return m.Amount.GreaterThan(decimal.Zero)
}

// IsNegative checks if the amount is negative.
func (m Money) IsNegative() bool {
	return m.Amount.LessThan(decimal.Zero)
}

// String returns a string representation of the Money.
func (m Money) String() string {
	return fmt.Sprintf("%s %s", m.Amount.StringFixed(2), m.Currency)
}
