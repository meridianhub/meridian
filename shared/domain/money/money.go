// Package money provides a unified Money type for monetary amounts across all services.
//
// Deprecated: This package is superseded by the Universal Asset System. Use
// shared/pkg/refdata.InstrumentResolver for instrument lookup and
// shared/platform/quantity for dimensioned quantities. The money package only
// supports a hardcoded set of fiat currencies and cannot represent non-monetary
// assets (energy, compute, carbon). It will be removed in a future release.
package money

import (
	"errors"
	"fmt"
	"math"

	"github.com/shopspring/decimal"
)

// Errors for Money operations.
//
// Deprecated: Use shared/platform/quantity error types instead.
var (
	ErrCurrencyMismatch = errors.New("currency mismatch")
	ErrInvalidCurrency  = errors.New("invalid currency")
	ErrOverflow         = errors.New("overflow: value exceeds int64 bounds")
	ErrDivisionByZero   = errors.New("division by zero")
)

// Currency represents an ISO 4217 currency code.
//
// Deprecated: Use shared/pkg/refdata.InstrumentResolver to resolve instrument
// properties by code. Currency is a string typedef with a hardcoded validation
// set that cannot represent non-monetary instruments.
type Currency string

// Supported currencies following ISO 4217 standard.
//
// Deprecated: Use shared/pkg/refdata.InstrumentResolver for dynamic instrument lookup.
const (
	CurrencyGBP Currency = "GBP" // British Pound Sterling
	CurrencyUSD Currency = "USD" // United States Dollar
	CurrencyEUR Currency = "EUR" // Euro
	CurrencyJPY Currency = "JPY" // Japanese Yen
	CurrencyCHF Currency = "CHF" // Swiss Franc
	CurrencyCAD Currency = "CAD" // Canadian Dollar
	CurrencyAUD Currency = "AUD" // Australian Dollar
)

// IsValid checks if the currency is a supported ISO 4217 code.
//
// Deprecated: Use shared/pkg/refdata.InstrumentResolver.Resolve() which validates
// against the Reference Data service rather than a hardcoded set.
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

// DecimalPlaces returns the number of decimal places for the currency.
// Most currencies use 2 decimal places, but some (like JPY) use 0.
//
// Deprecated: Use shared/pkg/refdata.InstrumentProperties.Precision instead.
func (c Currency) DecimalPlaces() int32 {
	switch c {
	case CurrencyJPY:
		return 0
	case CurrencyGBP, CurrencyUSD, CurrencyEUR, CurrencyCHF, CurrencyCAD, CurrencyAUD:
		return 2
	default:
		return 2
	}
}

// ParseCurrency converts a string to a Currency type with validation.
//
// Deprecated: Use shared/pkg/refdata.InstrumentResolver.Resolve() instead.
func ParseCurrency(s string) (Currency, error) {
	c := Currency(s)
	if !c.IsValid() {
		return "", fmt.Errorf("%w: %s", ErrInvalidCurrency, s)
	}
	return c, nil
}

// Money represents an immutable monetary amount with currency.
// It uses decimal.Decimal for precise arithmetic operations.
//
// Deprecated: Use shared/platform/quantity.Money (Qty[Monetary]) which supports
// any instrument dimension, not just currencies.
type Money struct {
	amount   decimal.Decimal
	currency Currency
}

// New creates a Money value with the given amount and currency.
// Returns an error if the currency is not supported.
//
// Deprecated: Use quantity.NewMoney(amount, instrument) instead.
func New(amount decimal.Decimal, currency Currency) (Money, error) {
	if !currency.IsValid() {
		return Money{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, currency)
	}
	return Money{
		amount:   amount,
		currency: currency,
	}, nil
}

// MustNew creates a Money value, panicking if the currency is invalid.
// Use only in tests or when the currency is known to be valid at compile time.
//
// Deprecated: Use quantity.NewMoney(amount, instrument) instead.
func MustNew(amount decimal.Decimal, currency Currency) Money {
	m, err := New(amount, currency)
	if err != nil {
		panic(err)
	}
	return m
}

// NewFromInt64 creates Money from an int64 amount in the currency's major units.
// For example, NewFromInt64(100, CurrencyGBP) creates £100.00.
//
// Deprecated: Use quantity.NewMoney(decimal.NewFromInt(amount), instrument) instead.
func NewFromInt64(amount int64, currency Currency) (Money, error) {
	return New(decimal.NewFromInt(amount), currency)
}

// NewFromMinorUnits creates Money from minor units (cents, pence, etc.).
// For example, NewFromMinorUnits(10000, CurrencyGBP) creates £100.00.
//
// Deprecated: Use quantity.NewMoney with decimal shifted by instrument precision.
func NewFromMinorUnits(minorUnits int64, currency Currency) (Money, error) {
	if !currency.IsValid() {
		return Money{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, currency)
	}
	decimalPlaces := currency.DecimalPlaces()
	amount := decimal.NewFromInt(minorUnits).Shift(-decimalPlaces)
	return Money{
		amount:   amount,
		currency: currency,
	}, nil
}

// Zero returns a zero Money value for the given currency.
//
// Deprecated: Use quantity.NewMoney(decimal.Zero, instrument) instead.
func Zero(currency Currency) (Money, error) {
	return New(decimal.Zero, currency)
}

// Amount returns the monetary amount as a decimal.
func (m Money) Amount() decimal.Decimal {
	return m.amount
}

// Currency returns the currency of the monetary amount.
func (m Money) Currency() Currency {
	return m.currency
}

// CurrencyCode returns the currency code as a string.
// This is a convenience method for interoperability with code expecting string currencies.
func (m Money) CurrencyCode() string {
	return string(m.currency)
}

// AmountCents returns the amount in minor units (cents, pence, etc.) as int64.
// This method uses ToMinorUnitsUnchecked internally - use ToMinorUnits() if you need
// overflow checking for very large values.
//
// Deprecated: Prefer ToMinorUnits() for new code which provides overflow checking.
func (m Money) AmountCents() int64 {
	return m.ToMinorUnitsUnchecked()
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

// Negate returns the negation of this Money value.
func (m Money) Negate() Money {
	return Money{
		amount:   m.amount.Neg(),
		currency: m.currency,
	}
}

// Abs returns the absolute value of this Money.
func (m Money) Abs() Money {
	return Money{
		amount:   m.amount.Abs(),
		currency: m.currency,
	}
}

// Multiply multiplies the Money amount by a decimal factor.
// This is useful for applying rates, percentages, or quantities.
// For example: price.Multiply(decimal.NewFromFloat(1.20)) for a 20% markup.
func (m Money) Multiply(factor decimal.Decimal) Money {
	return Money{
		amount:   m.amount.Mul(factor),
		currency: m.currency,
	}
}

// Divide divides the Money amount by a decimal divisor.
// Returns an error if the divisor is zero.
// For example: total.Divide(decimal.NewFromInt(3)) to split evenly.
func (m Money) Divide(divisor decimal.Decimal) (Money, error) {
	if divisor.IsZero() {
		return Money{}, ErrDivisionByZero
	}
	return Money{
		amount:   m.amount.Div(divisor),
		currency: m.currency,
	}, nil
}

// IsZero returns true if the amount is zero.
func (m Money) IsZero() bool {
	return m.amount.IsZero()
}

// IsPositive returns true if the amount is greater than zero.
func (m Money) IsPositive() bool {
	return m.amount.GreaterThan(decimal.Zero)
}

// IsNegative returns true if the amount is less than zero.
func (m Money) IsNegative() bool {
	return m.amount.LessThan(decimal.Zero)
}

// Equals returns true if both Money instances have the same amount and currency.
func (m Money) Equals(other Money) bool {
	return m.currency == other.currency && m.amount.Equal(other.amount)
}

// Compare returns -1 if m < other, 0 if m == other, 1 if m > other.
// Returns an error if currencies don't match.
func (m Money) Compare(other Money) (int, error) {
	if m.currency != other.currency {
		return 0, fmt.Errorf("%w: cannot compare %s and %s",
			ErrCurrencyMismatch, m.currency, other.currency)
	}
	return m.amount.Cmp(other.amount), nil
}

// String returns a string representation of the Money value.
// Format: "123.45 GBP" (always 2 decimal places for display consistency).
func (m Money) String() string {
	return fmt.Sprintf("%s %s", m.amount.StringFixed(2), m.currency)
}

// StringWithPrecision returns a string with the currency's natural precision.
// For example, JPY shows no decimals: "1000 JPY".
func (m Money) StringWithPrecision() string {
	return fmt.Sprintf("%s %s", m.amount.StringFixed(m.currency.DecimalPlaces()), m.currency)
}

// ToMinorUnits converts the Money amount to minor units (cents, pence, sen, etc.).
// This is currency-aware: JPY returns the amount as-is (no decimals), while
// GBP/USD/EUR multiply by 100 to convert to cents/pence.
//
// Uses banker's rounding (round-half-to-even) to handle fractional minor units,
// which reduces cumulative rounding bias in financial calculations.
// For example: 100.995 GBP rounds to 10100 pence (up), 100.985 GBP rounds to 10098 pence (down).
//
// Returns an error if the result would overflow int64.
func (m Money) ToMinorUnits() (int64, error) {
	decimalPlaces := m.currency.DecimalPlaces()
	shifted := m.amount.Shift(decimalPlaces)

	// Round to nearest integer using banker's rounding (round-half-to-even)
	// This is the standard rounding method for financial calculations
	rounded := shifted.RoundBank(0)

	// Check for overflow before converting to int64
	if rounded.GreaterThan(decimal.NewFromInt(math.MaxInt64)) ||
		rounded.LessThan(decimal.NewFromInt(math.MinInt64)) {
		return 0, fmt.Errorf("%w: %s minor units", ErrOverflow, rounded.String())
	}

	return rounded.IntPart(), nil
}

// ToMinorUnitsUnchecked converts to minor units without overflow checking.
// Uses banker's rounding (round-half-to-even) for fractional minor units.
// Use only when you're certain the value won't overflow (e.g., validated input).
func (m Money) ToMinorUnitsUnchecked() int64 {
	decimalPlaces := m.currency.DecimalPlaces()
	return m.amount.Shift(decimalPlaces).RoundBank(0).IntPart()
}
