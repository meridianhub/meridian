// Package domain re-exports the quantity-based Money type for current-account service.
//
// This file provides backward-compatible aliases and constructors for migrating
// from the previous currency-based Money implementation to the instrument-based
// Qty[Monetary] implementation. The Currency() and AmountCents() methods are
// preserved for API compatibility during the migration.
package domain

import (
	"errors"
	"fmt"

	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/shopspring/decimal"
)

// Re-export errors - map to quantity package errors where possible
var (
	// ErrInvalidCurrency is returned when a currency code is not recognized.
	ErrInvalidCurrency = errors.New("invalid currency")

	// ErrCurrencyMismatch is returned when arithmetic operations are attempted
	// on Money values with different instruments.
	ErrCurrencyMismatch = quantity.ErrInstrumentMismatch

	// ErrAmountOverflow is kept for compatibility but decimal-based arithmetic
	// doesn't typically overflow.
	ErrAmountOverflow = errors.New("amount overflow")
)

// Currency represents an ISO 4217 currency code.
// This is a compatibility type that maps to instrument codes.
type Currency string

// Currency constants for supported ISO 4217 currencies.
const (
	CurrencyGBP Currency = "GBP"
	CurrencyUSD Currency = "USD"
	CurrencyEUR Currency = "EUR"
	CurrencyJPY Currency = "JPY"
	CurrencyCHF Currency = "CHF"
	CurrencyCAD Currency = "CAD"
	CurrencyAUD Currency = "AUD"
)

// currencyPrecision returns the decimal precision for a currency.
// Most currencies use 2 decimal places, but JPY uses 0.
func currencyPrecision(code string) int {
	switch code {
	case "JPY":
		return 0
	default:
		return 2
	}
}

// currencyInstrument creates an Instrument for the given currency code.
// Returns an error if the currency is not supported.
func currencyInstrument(code string) (quantity.Instrument, error) {
	precision := currencyPrecision(code)
	switch code {
	case "GBP", "USD", "EUR", "JPY", "CHF", "CAD", "AUD":
		return quantity.NewInstrument(code, 1, quantity.DimensionCurrency, precision)
	default:
		return quantity.Instrument{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, code)
	}
}

// Instrument is re-exported from the quantity package.
type Instrument = quantity.Instrument

// Money wraps quantity.Money (Qty[Monetary]) and provides backward-compatible
// methods for the current-account domain.
//
// This type maintains the existing API contract (Currency(), AmountCents(), etc.)
// while using the new instrument-based quantity system internally.
type Money struct {
	qty quantity.Money
}

// NewMoney creates a new Money instance from a currency string and amount in minor units (cents).
// This preserves backward compatibility with the previous int64-based API.
//
// Example: NewMoney("GBP", 10000) creates £100.00
func NewMoney(currency string, amountCents int64) (Money, error) {
	inst, err := currencyInstrument(currency)
	if err != nil {
		return Money{}, err
	}
	// Convert cents to major units based on currency precision
	precision := currencyPrecision(currency)
	amount := decimal.NewFromInt(amountCents).Shift(-int32(precision))
	return Money{
		qty: quantity.NewMoney(amount, inst),
	}, nil
}

// NewMoneyFromInstrument creates Money from persisted instrument_code + dimension and minor-unit amount.
// This is used by the persistence layer to reconstruct Money without losing the stored dimension.
func NewMoneyFromInstrument(instrumentCode, dimension string, amountCents int64) (Money, error) {
	precision := currencyPrecision(instrumentCode)
	inst, err := quantity.NewInstrument(instrumentCode, 1, dimension, precision)
	if err != nil {
		return Money{}, fmt.Errorf("invalid instrument %s/%s: %w", instrumentCode, dimension, err)
	}
	amount := decimal.NewFromInt(amountCents).Shift(-int32(precision))
	return Money{
		qty: quantity.NewMoney(amount, inst),
	}, nil
}

// NewMoneyFromMajorUnits creates Money from major units (pounds, dollars, etc.).
// This is the preferred constructor for new code.
//
// Example: NewMoneyFromMajorUnits("GBP", 100) creates £100.00
func NewMoneyFromMajorUnits(currency string, amount int64) (Money, error) {
	inst, err := currencyInstrument(currency)
	if err != nil {
		return Money{}, err
	}
	return Money{
		qty: quantity.NewMoneyFromInt(amount, inst),
	}, nil
}

// NewMoneyDecimal creates Money from a decimal amount and Currency type.
// This provides compatibility with services using the decimal-based API.
//
// Example: NewMoneyDecimal(decimal.NewFromInt(100), CurrencyGBP) creates £100.00
func NewMoneyDecimal(amount decimal.Decimal, currency Currency) (Money, error) {
	inst, err := currencyInstrument(string(currency))
	if err != nil {
		return Money{}, err
	}
	return Money{
		qty: quantity.NewMoney(amount, inst),
	}, nil
}

// MustNewMoneyDecimal creates Money from a decimal, panicking on invalid currency.
// Use only in tests or when currency is known valid.
func MustNewMoneyDecimal(amount decimal.Decimal, currency Currency) Money {
	m, err := NewMoneyDecimal(amount, currency)
	if err != nil {
		panic(err)
	}
	return m
}

// NewMoneyFromQuantity creates a Money wrapper from a quantity.Money value.
// This is used when converting from the lower-level quantity package.
func NewMoneyFromQuantity(qty quantity.Money) Money {
	return Money{qty: qty}
}

// ZeroMoney creates a zero Money value for the given currency.
func ZeroMoney(currency string) (Money, error) {
	return NewMoney(currency, 0)
}

// Quantity returns the underlying quantity.Money value.
// This provides access to the full quantity API when needed.
func (m Money) Quantity() quantity.Money {
	return m.qty
}

// Amount returns the monetary amount as a decimal.
func (m Money) Amount() decimal.Decimal {
	return m.qty.Amount
}

// Currency returns the currency of the monetary amount.
func (m Money) Currency() Currency {
	return Currency(m.qty.Instrument.Code)
}

// CurrencyCode returns the currency code as a string.
func (m Money) CurrencyCode() string {
	return m.qty.Instrument.Code
}

// AmountCents returns the amount in minor units (cents, pence, etc.) as int64.
// Uses the instrument's precision to determine the shift.
//
// Deprecated: Prefer using Amount() with explicit conversion for new code.
func (m Money) AmountCents() int64 {
	precision := m.qty.Instrument.Precision
	shifted := m.qty.Amount.Shift(int32(precision))
	return shifted.RoundBank(0).IntPart()
}

// Add adds two Money values. They must have the same currency/instrument.
func (m Money) Add(other Money) (Money, error) {
	result, err := m.qty.Add(other.qty)
	if err != nil {
		// Map instrument mismatch to currency mismatch for backward compatibility
		return Money{}, fmt.Errorf("%w: cannot add %s and %s",
			ErrCurrencyMismatch, m.CurrencyCode(), other.CurrencyCode())
	}
	return Money{qty: result}, nil
}

// Subtract subtracts another Money value from this one. They must have the same currency.
func (m Money) Subtract(other Money) (Money, error) {
	result, err := m.qty.Subtract(other.qty)
	if err != nil {
		return Money{}, fmt.Errorf("%w: cannot subtract %s and %s",
			ErrCurrencyMismatch, m.CurrencyCode(), other.CurrencyCode())
	}
	return Money{qty: result}, nil
}

// Negate returns the negation of this Money value.
func (m Money) Negate() Money {
	return Money{qty: m.qty.Negate()}
}

// Abs returns the absolute value of this Money.
func (m Money) Abs() Money {
	return Money{qty: m.qty.Abs()}
}

// Multiply multiplies the Money amount by a decimal factor.
func (m Money) Multiply(factor decimal.Decimal) Money {
	return Money{qty: m.qty.Multiply(factor)}
}

// Divide divides the Money amount by a decimal divisor.
// Returns an error if the divisor is zero.
func (m Money) Divide(divisor decimal.Decimal) (Money, error) {
	result, err := m.qty.Divide(divisor)
	if err != nil {
		return Money{}, err
	}
	return Money{qty: result}, nil
}

// IsZero returns true if the amount is zero.
func (m Money) IsZero() bool {
	return m.qty.IsZero()
}

// IsPositive returns true if the amount is greater than zero.
func (m Money) IsPositive() bool {
	return m.qty.IsPositive()
}

// IsNegative returns true if the amount is less than zero.
func (m Money) IsNegative() bool {
	return m.qty.IsNegative()
}

// Equals returns true if both Money instances have the same amount and currency.
func (m Money) Equals(other Money) bool {
	return m.qty.Equal(other.qty)
}

// Compare returns -1 if m < other, 0 if m == other, 1 if m > other.
// Returns an error if currencies don't match.
func (m Money) Compare(other Money) (int, error) {
	return m.qty.Compare(other.qty)
}

// String returns a string representation of the Money value.
func (m Money) String() string {
	return m.qty.String()
}

// ToMinorUnits returns the amount in minor units (cents, pence, etc.) as int64.
// Returns an error if the resulting value would overflow int64.
// This method is kept for backward compatibility with existing persistence layer code.
func (m Money) ToMinorUnits() (int64, error) {
	precision := m.qty.Instrument.Precision
	shifted := m.qty.Amount.Shift(int32(precision))
	rounded := shifted.RoundBank(0)

	// Check for overflow - int64 max is approximately 9.2e18
	if rounded.Abs().GreaterThan(decimal.NewFromInt(9223372036854775807)) {
		return 0, ErrAmountOverflow
	}
	return rounded.IntPart(), nil
}

// ToMinorUnitsUnchecked returns the amount in minor units without overflow checking.
// Use only when the caller can guarantee the amount is within int64 range.
// Panics if overflow would occur (caught by decimal's IntPart).
//
// This is safe for persistence layer use because:
// - Domain layer validates amounts before persistence
// - Reasonable monetary amounts (<= 92 quadrillion cents) cannot overflow
func (m Money) ToMinorUnitsUnchecked() int64 {
	precision := m.qty.Instrument.Precision
	shifted := m.qty.Amount.Shift(int32(precision))
	return shifted.RoundBank(0).IntPart()
}
