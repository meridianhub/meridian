package domain

import "errors"

// Money errors
var (
	ErrInvalidCurrency = errors.New("currency cannot be empty")
)

// Money represents an immutable monetary amount with currency
// All fields are unexported to enforce immutability
// Use NewMoney constructor and methods that return new instances
type Money struct {
	amountCents int64
	currency    string
}

// NewMoney creates a new Money instance with validation
func NewMoney(currency string, amountCents int64) (Money, error) {
	if currency == "" {
		return Money{}, ErrInvalidCurrency
	}

	return Money{
		currency:    currency,
		amountCents: amountCents,
	}, nil
}

// Currency returns the currency code
func (m Money) Currency() string {
	return m.currency
}

// AmountCents returns the amount in cents
func (m Money) AmountCents() int64 {
	return m.amountCents
}

// Add returns a new Money instance with the sum
// Value receiver ensures immutability
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}

	return Money{
		currency:    m.currency,
		amountCents: m.amountCents + other.amountCents,
	}, nil
}

// Subtract returns a new Money instance with the difference
func (m Money) Subtract(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}

	return Money{
		currency:    m.currency,
		amountCents: m.amountCents - other.amountCents,
	}, nil
}

// IsPositive returns true if amount is greater than zero
func (m Money) IsPositive() bool {
	return m.amountCents > 0
}

// IsZero returns true if amount is zero
func (m Money) IsZero() bool {
	return m.amountCents == 0
}

// Equals returns true if both money instances have the same amount and currency
func (m Money) Equals(other Money) bool {
	return m.currency == other.currency && m.amountCents == other.amountCents
}
