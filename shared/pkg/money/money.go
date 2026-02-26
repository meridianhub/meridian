// Package money provides the shared instrument-aware Money type for use across all services.
//
// This package consolidates the Money type that was previously defined locally in
// services/current-account/domain. It wraps shared/platform/quantity.Money (Qty[Monetary])
// and provides backward-compatible constructors and accessors.
//
// # Usage
//
// Create Money from a currency code and minor units:
//
//	m, err := money.New("GBP", 10000)  // £100.00
//
// Create Money from decimal and currency:
//
//	m, err := money.NewFromDecimal(decimal.NewFromInt(100), money.CurrencyGBP)
//
// Access values:
//
//	m.Amount()      // decimal.Decimal
//	m.Currency()    // money.Currency ("GBP")
//	m.Instrument()  // quantity.Instrument (full instrument metadata)
package money

import (
	"errors"
	"fmt"
	"strings"

	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/meridianhub/meridian/shared/platform/quantity/currency"
	"github.com/shopspring/decimal"
)

// Sentinel errors.
var (
	// ErrInvalidCurrency is returned when a currency code is not recognized.
	ErrInvalidCurrency = errors.New("invalid currency")

	// ErrCurrencyMismatch is returned when arithmetic operations are attempted
	// on Money values with different instruments.
	ErrCurrencyMismatch = quantity.ErrInstrumentMismatch

	// ErrAmountOverflow is returned when converting to minor units would overflow int64.
	ErrAmountOverflow = errors.New("amount overflow")
)

// Currency represents an ISO 4217 currency code.
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

// String returns the string representation of the currency code.
func (c Currency) String() string {
	return string(c)
}

// Money wraps quantity.Money (Qty[Monetary]) and provides instrument-aware constructors
// and backward-compatible accessors.
//
// All operations return new Money values rather than modifying the receiver.
type Money struct {
	qty quantity.Money
}

// instrumentForCurrency returns the quantity.Instrument for the given currency code.
// Returns an error if the currency is not recognized.
func instrumentForCurrency(code string) (quantity.Instrument, error) {
	inst, ok := currency.ByCode(strings.ToUpper(code))
	if !ok {
		return quantity.Instrument{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, code)
	}
	return inst, nil
}

// New creates a Money instance from a currency code and amount in minor units (cents/pence).
//
// Example: New("GBP", 10000) creates £100.00
func New(currencyCode string, amountMinorUnits int64) (Money, error) {
	inst, err := instrumentForCurrency(currencyCode)
	if err != nil {
		return Money{}, err
	}
	amount := decimal.NewFromInt(amountMinorUnits).Shift(-int32(inst.Precision))
	return Money{qty: quantity.NewMoney(amount, inst)}, nil
}

// NewFromDecimal creates a Money instance from a decimal amount in major units and a Currency.
//
// Example: NewFromDecimal(decimal.NewFromInt(100), CurrencyGBP) creates £100.00
func NewFromDecimal(amount decimal.Decimal, curr Currency) (Money, error) {
	inst, err := instrumentForCurrency(string(curr))
	if err != nil {
		return Money{}, err
	}
	return Money{qty: quantity.NewMoney(amount, inst)}, nil
}

// MustNewFromDecimal creates Money from a decimal and Currency, panicking on invalid currency.
// Use only in tests or when the currency is known valid.
func MustNewFromDecimal(amount decimal.Decimal, curr Currency) Money {
	m, err := NewFromDecimal(amount, curr)
	if err != nil {
		panic(err)
	}
	return m
}

// NewFromMajorUnits creates Money from a major-unit int64 amount and a currency code.
//
// Example: NewFromMajorUnits("GBP", 100) creates £100.00
func NewFromMajorUnits(currencyCode string, amount int64) (Money, error) {
	inst, err := instrumentForCurrency(currencyCode)
	if err != nil {
		return Money{}, err
	}
	return Money{qty: quantity.NewMoneyFromInt(amount, inst)}, nil
}

// NewFromQuantity creates a Money wrapper from an existing quantity.Money value.
func NewFromQuantity(qty quantity.Money) Money {
	return Money{qty: qty}
}

// NewFromInstrument creates Money from persisted instrument_code + dimension and minor-unit amount.
// Returns ErrInvalidCurrency if the dimension is not "CURRENCY".
func NewFromInstrument(instrumentCode, dimension string, amountMinorUnits int64) (Money, error) {
	if strings.ToUpper(dimension) != quantity.DimensionCurrency {
		return Money{}, fmt.Errorf("%w: only CURRENCY dimension is supported, got %s",
			ErrInvalidCurrency, dimension)
	}
	return New(instrumentCode, amountMinorUnits)
}

// Zero creates a zero Money value for the given currency code.
func Zero(currencyCode string) (Money, error) {
	return New(currencyCode, 0)
}

// Quantity returns the underlying quantity.Money value.
func (m Money) Quantity() quantity.Money {
	return m.qty
}

// Amount returns the monetary amount as a decimal.Decimal.
func (m Money) Amount() decimal.Decimal {
	return m.qty.Amount
}

// Instrument returns the full instrument metadata (code, dimension, precision, version).
func (m Money) Instrument() quantity.Instrument {
	return m.qty.Instrument
}

// Currency returns the currency code as a Currency type.
func (m Money) Currency() Currency {
	return Currency(m.qty.Instrument.Code)
}

// CurrencyCode returns the currency code as a plain string.
func (m Money) CurrencyCode() string {
	return m.qty.Instrument.Code
}

// AmountCents returns the amount in minor units (cents, pence, etc.) as int64.
// Uses banker's rounding (round-half-to-even).
//
// Deprecated: Prefer ToMinorUnits() for new code which provides overflow checking.
func (m Money) AmountCents() int64 {
	return m.ToMinorUnitsUnchecked()
}

// Add adds two Money values. Both must have the same currency/instrument.
func (m Money) Add(other Money) (Money, error) {
	result, err := m.qty.Add(other.qty)
	if err != nil {
		return Money{}, fmt.Errorf("%w: cannot add %s and %s",
			ErrCurrencyMismatch, m.CurrencyCode(), other.CurrencyCode())
	}
	return Money{qty: result}, nil
}

// Subtract subtracts another Money value from this one. Both must have the same currency.
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

// ToMinorUnits converts the amount to minor units (cents, pence, etc.) as int64.
// Returns an error if the resulting value would overflow int64.
func (m Money) ToMinorUnits() (int64, error) {
	precision := m.qty.Instrument.Precision
	shifted := m.qty.Amount.Shift(int32(precision))
	rounded := shifted.RoundBank(0)

	if rounded.Abs().GreaterThan(decimal.NewFromInt(9223372036854775807)) {
		return 0, ErrAmountOverflow
	}
	return rounded.IntPart(), nil
}

// ToMinorUnitsUnchecked returns the amount in minor units without overflow checking.
// Use only when the caller can guarantee the amount is within int64 range.
func (m Money) ToMinorUnitsUnchecked() int64 {
	precision := m.qty.Instrument.Precision
	shifted := m.qty.Amount.Shift(int32(precision))
	return shifted.RoundBank(0).IntPart()
}
