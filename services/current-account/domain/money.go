package domain

import "errors"

// Money errors
var (
	ErrInvalidCurrency  = errors.New("currency cannot be empty")
	ErrAmountOverflow   = errors.New("amount overflow: operation would exceed int64 bounds")
	ErrCurrencyMismatch = errors.New("currency mismatch")
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
// Returns ErrAmountOverflow if the operation would exceed int64 bounds
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}

	// Detect signed integer overflow for addition:
	// Overflow occurs when:
	// - Adding two positive numbers gives a negative result
	// - Adding two negative numbers gives a positive result
	// When operands have different signs, overflow is impossible
	result := m.amountCents + other.amountCents

	if (other.amountCents > 0 && result < m.amountCents) ||
		(other.amountCents < 0 && result > m.amountCents) {
		return Money{}, ErrAmountOverflow
	}

	return Money{
		currency:    m.currency,
		amountCents: result,
	}, nil
}

// Subtract returns a new Money instance with the difference
// Returns ErrAmountOverflow if the operation would exceed int64 bounds
func (m Money) Subtract(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}

	// Detect signed integer overflow for subtraction:
	// Overflow occurs when:
	// - Subtracting a negative from a positive gives a negative result
	// - Subtracting a positive from a negative gives a positive result
	// When operands have the same sign, overflow is impossible
	result := m.amountCents - other.amountCents

	if (other.amountCents > 0 && result > m.amountCents) ||
		(other.amountCents < 0 && result < m.amountCents) {
		return Money{}, ErrAmountOverflow
	}

	return Money{
		currency:    m.currency,
		amountCents: result,
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
