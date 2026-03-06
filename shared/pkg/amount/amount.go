// Package amount provides the shared dimension-agnostic Amount type for use across all services.
//
// Unlike the money package which is restricted to CURRENCY dimension, Amount accepts any
// valid dimension from quantity.ValidDimensions (CURRENCY, ENERGY, CARBON, COMPUTE, etc.).
// For CURRENCY instruments it delegates precision lookup to the currency package.
//
// # Usage
//
// Create Amount from an instrument code and dimension:
//
//	a, err := amount.NewFromInstrument("GBP", "CURRENCY", 2, 10000)  // £100.00
//	a, err := amount.NewFromInstrument("KWH", "ENERGY", 3, 1500)     // 1.500 KWH
//
// Create Amount from an existing instrument:
//
//	inst, _ := quantity.NewInstrument("KWH", 0, "ENERGY", 3)
//	a := amount.New(inst, 1500)  // 1.500 KWH
//
// Access values:
//
//	a.Amount()         // decimal.Decimal
//	a.Instrument()     // quantity.Instrument
//	a.InstrumentCode() // "KWH"
//	a.Dimension()      // "ENERGY"
package amount

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/meridianhub/meridian/shared/platform/quantity/currency"
)

// Sentinel errors.
var (
	// ErrInvalidDimension is returned when a dimension string is not recognized.
	ErrInvalidDimension = errors.New("invalid dimension")

	// ErrInstrumentMismatch is returned when arithmetic operations are attempted
	// on Amount values with different instruments.
	ErrInstrumentMismatch = quantity.ErrInstrumentMismatch

	// ErrAmountOverflow is returned when converting to minor units would overflow int64.
	ErrAmountOverflow = errors.New("amount overflow")
)

// Amount wraps a quantity value and provides dimension-agnostic constructors and accessors.
// Supports all dimensions in quantity.ValidDimensions.
//
// All operations return new Amount values rather than modifying the receiver.
type Amount struct {
	instrument quantity.Instrument
	amount     decimal.Decimal
}

// NewFromInstrument creates an Amount from persisted instrument_code, dimension, precision,
// and a minor-unit amount. For CURRENCY dimension, the precision parameter is ignored and
// the canonical precision from the currency registry is used instead.
//
// Returns ErrInvalidDimension if dimension is not recognized.
func NewFromInstrument(code, dimension string, precision int, amountMinorUnits int64) (Amount, error) {
	normalizedDim := strings.ToUpper(dimension)
	if !quantity.ValidDimensions[normalizedDim] {
		return Amount{}, fmt.Errorf("%w: %s", ErrInvalidDimension, dimension)
	}

	var inst quantity.Instrument
	if normalizedDim == quantity.DimensionCurrency {
		// For currency, look up canonical instrument (ignores caller-supplied precision).
		currInst, ok := currency.ByCode(strings.ToUpper(code)) //nolint:staticcheck // Will migrate to refdata.InstrumentResolver
		if !ok {
			return Amount{}, fmt.Errorf("%w: unrecognized currency code %s", ErrInvalidDimension, code)
		}
		inst = currInst
	} else {
		var err error
		inst, err = quantity.NewInstrument(strings.ToUpper(code), 0, normalizedDim, precision)
		if err != nil {
			return Amount{}, fmt.Errorf("%w: %w", ErrInvalidDimension, err)
		}
	}

	a := decimal.NewFromInt(amountMinorUnits).Shift(-int32(inst.Precision))
	return Amount{instrument: inst, amount: a}, nil
}

// New creates an Amount from an existing instrument and a minor-unit amount.
// The instrument is used as-is; no additional validation is performed.
func New(inst quantity.Instrument, amountMinorUnits int64) Amount {
	a := decimal.NewFromInt(amountMinorUnits).Shift(-int32(inst.Precision))
	return Amount{instrument: inst, amount: a}
}

// NewFromDecimal creates an Amount from an existing instrument and a decimal major-unit amount.
// This is useful when the amount is already in major units (e.g., from calculations).
func NewFromDecimal(inst quantity.Instrument, majorUnits decimal.Decimal) Amount {
	return Amount{instrument: inst, amount: majorUnits}
}

// Zero creates a zero Amount for the given instrument.
func Zero(inst quantity.Instrument) Amount {
	return Amount{instrument: inst, amount: decimal.Zero}
}

// Amount returns the decimal value of this amount.
func (a Amount) Amount() decimal.Decimal {
	return a.amount
}

// Instrument returns the full instrument metadata.
func (a Amount) Instrument() quantity.Instrument {
	return a.instrument
}

// InstrumentCode returns the instrument code as a plain string (e.g., "GBP", "KWH").
func (a Amount) InstrumentCode() string {
	return a.instrument.Code
}

// Dimension returns the dimension string (e.g., "CURRENCY", "ENERGY").
func (a Amount) Dimension() string {
	return a.instrument.Dimension
}

// Precision returns the number of decimal places for this instrument.
func (a Amount) Precision() int {
	return a.instrument.Precision
}

// Add adds two Amount values. Both must have the same instrument.
// Returns ErrInstrumentMismatch if instruments differ.
func (a Amount) Add(other Amount) (Amount, error) {
	if !a.instrument.Equal(other.instrument) {
		return Amount{}, fmt.Errorf("%w: cannot add %s and %s",
			ErrInstrumentMismatch, a.InstrumentCode(), other.InstrumentCode())
	}
	return Amount{instrument: a.instrument, amount: a.amount.Add(other.amount)}, nil
}

// Subtract subtracts another Amount from this one. Both must have the same instrument.
// Returns ErrInstrumentMismatch if instruments differ.
func (a Amount) Subtract(other Amount) (Amount, error) {
	if !a.instrument.Equal(other.instrument) {
		return Amount{}, fmt.Errorf("%w: cannot subtract %s from %s",
			ErrInstrumentMismatch, other.InstrumentCode(), a.InstrumentCode())
	}
	return Amount{instrument: a.instrument, amount: a.amount.Sub(other.amount)}, nil
}

// Negate returns the negation of this Amount.
func (a Amount) Negate() Amount {
	return Amount{instrument: a.instrument, amount: a.amount.Neg()}
}

// Multiply multiplies the Amount value by a decimal factor.
// Result is rounded using banker's rounding to the instrument's precision.
func (a Amount) Multiply(factor decimal.Decimal) Amount {
	result := a.amount.Mul(factor).RoundBank(int32(a.instrument.Precision))
	return Amount{instrument: a.instrument, amount: result}
}

// IsZero returns true if the amount is zero.
func (a Amount) IsZero() bool {
	return a.amount.IsZero()
}

// IsPositive returns true if the amount is greater than zero.
func (a Amount) IsPositive() bool {
	return a.amount.IsPositive()
}

// IsNegative returns true if the amount is less than zero.
func (a Amount) IsNegative() bool {
	return a.amount.IsNegative()
}

// Compare returns -1 if a < other, 0 if equal, 1 if a > other.
// Returns ErrInstrumentMismatch if instruments differ.
func (a Amount) Compare(other Amount) (int, error) {
	if !a.instrument.Equal(other.instrument) {
		return 0, fmt.Errorf("%w: cannot compare %s with %s",
			ErrInstrumentMismatch, a.InstrumentCode(), other.InstrumentCode())
	}
	return a.amount.Cmp(other.amount), nil
}

// Equals returns true if both Amount instances have the same value and instrument.
func (a Amount) Equals(other Amount) bool {
	return a.instrument.Equal(other.instrument) && a.amount.Equal(other.amount)
}

// ToMinorUnits converts the amount to minor units as int64.
// Returns ErrAmountOverflow if the resulting value would overflow int64.
func (a Amount) ToMinorUnits() (int64, error) {
	shifted := a.amount.Shift(int32(a.instrument.Precision))
	rounded := shifted.RoundBank(0)

	maxInt64 := decimal.NewFromInt(math.MaxInt64)
	minInt64 := decimal.NewFromInt(math.MinInt64)
	if rounded.GreaterThan(maxInt64) || rounded.LessThan(minInt64) {
		return 0, ErrAmountOverflow
	}
	return rounded.IntPart(), nil
}

// ToMinorUnitsUnchecked returns the amount in minor units without overflow checking.
// Use only when the caller can guarantee the amount is within int64 range.
func (a Amount) ToMinorUnitsUnchecked() int64 {
	shifted := a.amount.Shift(int32(a.instrument.Precision))
	return shifted.RoundBank(0).IntPart()
}

// String returns a human-readable representation of the Amount value.
func (a Amount) String() string {
	return fmt.Sprintf("%s %s", a.amount.StringFixed(int32(a.instrument.Precision)), a.instrument.Code)
}
