// Package quantity provides the generic Qty[D] type for the Universal Asset System.
//
// # Generic Quantity Type
//
// Qty[D] represents an amount of a specific instrument with compile-time dimension safety.
// The type parameter D constrains the dimension type, preventing mixing of monetary
// and commodity quantities at compile time:
//
//	var money Money   // monetary quantity (USD, EUR)
//	var energy Asset  // commodity quantity (KWH, GPU_HOUR)
//	money = energy    // compile error!
//
// # Arithmetic Operations
//
// All arithmetic operations enforce same-instrument validation at runtime:
//   - Add, Subtract: both quantities must have the same instrument Code AND Version
//   - Multiply, Divide: multiply/divide by a scalar (no instrument required)
//
// # Type Aliases
//
// For convenience, type aliases are provided:
//   - Money = Qty[Monetary]   for currency quantities
//   - Asset = Qty[Commodity]  for non-monetary asset quantities
package quantity

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// Sentinel errors for quantity operations.
var (
	// ErrInstrumentMismatch is returned when attempting arithmetic operations
	// on quantities with different instruments.
	ErrInstrumentMismatch = errors.New("instrument mismatch: quantities must have the same instrument code and version")

	// ErrDivisionByZero is returned when attempting to divide by zero.
	ErrDivisionByZero = errors.New("division by zero")

	// ErrInvalidDecimalString is returned when a string cannot be parsed as a decimal.
	ErrInvalidDecimalString = errors.New("invalid decimal string")

	// ErrDimensionMismatch is returned when the instrument's dimension does not match
	// the expected type parameter dimension (Monetary vs Commodity).
	ErrDimensionMismatch = errors.New("dimension mismatch: instrument dimension does not match type parameter")
)

// Qty represents an amount of a specific instrument with compile-time dimension safety.
// The type parameter D constrains the dimension type (Monetary or Commodity).
//
// Qty is designed to be immutable - all operations return new Qty values
// rather than modifying the receiver.
//
// Note: Named Qty (not Quantity) to avoid conflict with the Quantity interface
// in interfaces.go which defines a more feature-rich contract for future use.
type Qty[D Dimension] struct {
	// Amount is the decimal value of this quantity.
	// Using shopspring/decimal for arbitrary precision arithmetic.
	Amount decimal.Decimal

	// Instrument identifies the asset type and provides compatibility information.
	Instrument Instrument
}

// New creates a new Qty with the given amount and instrument.
// The amount is a decimal.Decimal for arbitrary precision.
func New[D Dimension](amount decimal.Decimal, instrument Instrument) Qty[D] {
	return Qty[D]{
		Amount:     amount,
		Instrument: instrument,
	}
}

// NewFromString creates a new Qty by parsing the amount string.
// Returns an error if the amount string is not a valid decimal.
func NewFromString[D Dimension](amount string, instrument Instrument) (Qty[D], error) {
	d, err := decimal.NewFromString(amount)
	if err != nil {
		return Qty[D]{}, fmt.Errorf("%w: %w", ErrInvalidDecimalString, err)
	}
	return Qty[D]{
		Amount:     d,
		Instrument: instrument,
	}, nil
}

// NewFromInt creates a new Qty from an int64 amount.
func NewFromInt[D Dimension](amount int64, instrument Instrument) Qty[D] {
	return Qty[D]{
		Amount:     decimal.NewFromInt(amount),
		Instrument: instrument,
	}
}

// Zero creates a zero-valued Qty for the given instrument.
func Zero[D Dimension](instrument Instrument) Qty[D] {
	return Qty[D]{
		Amount:     decimal.Zero,
		Instrument: instrument,
	}
}

// NewQuantityValidated creates a new Qty with validation that the instrument's dimension
// matches the type parameter D.
//
// This function provides runtime validation that the instrument is appropriate for
// the requested dimension type:
//   - For Qty[Monetary]: instrument.Dimension must be "CURRENCY"
//   - For Qty[Commodity]: instrument.Dimension must NOT be "CURRENCY"
//
// Use this function when you have an instrument from an external source (database,
// proto, API) and want to ensure type safety before creating a typed quantity.
//
// Example:
//
//	inst, _ := NewInstrument("USD", 1, "CURRENCY", 2)
//	money, err := NewQuantityValidated[Monetary](amount, inst) // OK
//	asset, err := NewQuantityValidated[Commodity](amount, inst) // Error: dimension mismatch
//
// For cases where you want automatic dimension detection without type parameters,
// use ParseQuantity instead.
func NewQuantityValidated[D Dimension](amount decimal.Decimal, inst Instrument) (Qty[D], error) {
	var zero D
	switch any(zero).(type) {
	case Monetary:
		if inst.Dimension != DimensionCurrency {
			return Qty[D]{}, ErrDimensionMismatch
		}
	case Commodity:
		if inst.Dimension == DimensionCurrency {
			return Qty[D]{}, ErrDimensionMismatch
		}
	}
	return New[D](amount, inst), nil
}

// Add returns a new quantity that is the sum of q and other.
// Returns an error if the instruments do not match (different code or version).
func (q Qty[D]) Add(other Qty[D]) (Qty[D], error) {
	if !q.Instrument.Equal(other.Instrument) {
		return Qty[D]{}, fmt.Errorf("%w: cannot add %s to %s",
			ErrInstrumentMismatch, other.Instrument.String(), q.Instrument.String())
	}
	return Qty[D]{
		Amount:     q.Amount.Add(other.Amount),
		Instrument: q.Instrument,
	}, nil
}

// Subtract returns a new quantity that is q minus other.
// Returns an error if the instruments do not match (different code or version).
func (q Qty[D]) Subtract(other Qty[D]) (Qty[D], error) {
	if !q.Instrument.Equal(other.Instrument) {
		return Qty[D]{}, fmt.Errorf("%w: cannot subtract %s from %s",
			ErrInstrumentMismatch, other.Instrument.String(), q.Instrument.String())
	}
	return Qty[D]{
		Amount:     q.Amount.Sub(other.Amount),
		Instrument: q.Instrument,
	}, nil
}

// Multiply returns a new quantity with the amount multiplied by the given factor.
// The result is rounded using banker's rounding to the instrument's precision.
func (q Qty[D]) Multiply(factor decimal.Decimal) Qty[D] {
	result := q.Amount.Mul(factor)
	return Qty[D]{
		Amount:     result.RoundBank(int32(q.Instrument.Precision)),
		Instrument: q.Instrument,
	}
}

// MultiplyString returns a new quantity with the amount multiplied by the given factor string.
// Returns an error if the factor string is not a valid decimal.
func (q Qty[D]) MultiplyString(factor string) (Qty[D], error) {
	f, err := decimal.NewFromString(factor)
	if err != nil {
		return Qty[D]{}, fmt.Errorf("%w: %w", ErrInvalidDecimalString, err)
	}
	return q.Multiply(f), nil
}

// Divide returns a new quantity with the amount divided by the given divisor.
// The result is rounded using banker's rounding to the instrument's precision.
// Returns an error if divisor is zero.
func (q Qty[D]) Divide(divisor decimal.Decimal) (Qty[D], error) {
	if divisor.IsZero() {
		return Qty[D]{}, ErrDivisionByZero
	}
	result := q.Amount.Div(divisor)
	return Qty[D]{
		Amount:     result.RoundBank(int32(q.Instrument.Precision)),
		Instrument: q.Instrument,
	}, nil
}

// DivideString returns a new quantity with the amount divided by the given divisor string.
// Returns an error if the divisor string is not a valid decimal or if divisor is zero.
func (q Qty[D]) DivideString(divisor string) (Qty[D], error) {
	d, err := decimal.NewFromString(divisor)
	if err != nil {
		return Qty[D]{}, fmt.Errorf("%w: %w", ErrInvalidDecimalString, err)
	}
	return q.Divide(d)
}

// Negate returns a new quantity with the negated amount.
func (q Qty[D]) Negate() Qty[D] {
	return Qty[D]{
		Amount:     q.Amount.Neg(),
		Instrument: q.Instrument,
	}
}

// Abs returns a new quantity with the absolute value of the amount.
func (q Qty[D]) Abs() Qty[D] {
	return Qty[D]{
		Amount:     q.Amount.Abs(),
		Instrument: q.Instrument,
	}
}

// IsZero returns true if the amount is zero.
func (q Qty[D]) IsZero() bool {
	return q.Amount.IsZero()
}

// IsNegative returns true if the amount is negative.
func (q Qty[D]) IsNegative() bool {
	return q.Amount.IsNegative()
}

// IsPositive returns true if the amount is positive (greater than zero).
func (q Qty[D]) IsPositive() bool {
	return q.Amount.IsPositive()
}

// Compare compares q and other and returns:
//
//	-1 if q <  other
//	 0 if q == other
//	+1 if q >  other
//
// Returns an error if the instruments do not match.
func (q Qty[D]) Compare(other Qty[D]) (int, error) {
	if !q.Instrument.Equal(other.Instrument) {
		return 0, fmt.Errorf("%w: cannot compare %s with %s",
			ErrInstrumentMismatch, q.Instrument.String(), other.Instrument.String())
	}
	return q.Amount.Cmp(other.Amount), nil
}

// Equal returns true if q and other have the same amount and instrument.
// Returns false if instruments don't match (doesn't error, just returns false).
func (q Qty[D]) Equal(other Qty[D]) bool {
	return q.Instrument.Equal(other.Instrument) && q.Amount.Equal(other.Amount)
}

// LessThan returns true if q is less than other.
// Returns an error if the instruments do not match.
func (q Qty[D]) LessThan(other Qty[D]) (bool, error) {
	cmp, err := q.Compare(other)
	if err != nil {
		return false, err
	}
	return cmp < 0, nil
}

// LessThanOrEqual returns true if q is less than or equal to other.
// Returns an error if the instruments do not match.
func (q Qty[D]) LessThanOrEqual(other Qty[D]) (bool, error) {
	cmp, err := q.Compare(other)
	if err != nil {
		return false, err
	}
	return cmp <= 0, nil
}

// GreaterThan returns true if q is greater than other.
// Returns an error if the instruments do not match.
func (q Qty[D]) GreaterThan(other Qty[D]) (bool, error) {
	cmp, err := q.Compare(other)
	if err != nil {
		return false, err
	}
	return cmp > 0, nil
}

// GreaterThanOrEqual returns true if q is greater than or equal to other.
// Returns an error if the instruments do not match.
func (q Qty[D]) GreaterThanOrEqual(other Qty[D]) (bool, error) {
	cmp, err := q.Compare(other)
	if err != nil {
		return false, err
	}
	return cmp >= 0, nil
}

// Round returns a new quantity with the amount rounded to the instrument's precision.
// Uses banker's rounding (round half to even) for financial accuracy.
func (q Qty[D]) Round() Qty[D] {
	return Qty[D]{
		Amount:     q.Amount.RoundBank(int32(q.Instrument.Precision)),
		Instrument: q.Instrument,
	}
}

// String returns a human-readable representation of the quantity.
func (q Qty[D]) String() string {
	return fmt.Sprintf("%s %s", q.Amount.StringFixed(int32(q.Instrument.Precision)), q.Instrument.Code)
}

// =============================================================================
// Value interface implementation
// =============================================================================

// DimensionName returns the dimension string from the instrument.
// Implements Value.DimensionName.
func (q Qty[D]) DimensionName() string {
	return q.Instrument.Dimension
}

// GetAmount returns the decimal amount of this quantity.
// Implements Value.GetAmount.
func (q Qty[D]) GetAmount() decimal.Decimal {
	return q.Amount
}

// GetInstrument returns the instrument identifying this quantity's asset type.
// Implements Value.GetInstrument.
func (q Qty[D]) GetInstrument() Instrument {
	return q.Instrument
}

// AsMoney attempts to convert this quantity to a Money (Qty[Monetary]) type.
// Returns (value, true) if this is a monetary quantity (instrument.Dimension == "CURRENCY"),
// or (zero, false) if this is a commodity quantity or if dimension is empty/invalid.
// Implements Value.AsMoney.
func (q Qty[D]) AsMoney() (Money, bool) {
	if q.Instrument.Dimension == DimensionCurrency {
		return New[Monetary](q.Amount, q.Instrument), true
	}
	return Money{}, false
}

// AsAsset attempts to convert this quantity to an Asset (Qty[Commodity]) type.
// Returns (value, true) if this is a commodity quantity (instrument.Dimension != "CURRENCY"),
// or (zero, false) if this is a monetary quantity.
// Implements Value.AsAsset.
func (q Qty[D]) AsAsset() (Asset, bool) {
	if q.Instrument.Dimension != DimensionCurrency && q.Instrument.Dimension != "" {
		return New[Commodity](q.Amount, q.Instrument), true
	}
	return Asset{}, false
}

// Money is a type alias for monetary quantities (currencies like USD, EUR, GBP).
// Use this for all currency-denominated values.
type Money = Qty[Monetary]

// Asset is a type alias for commodity quantities (energy, compute, carbon credits).
// Use this for non-monetary asset values like KWH, GPU_HOUR, CARBON_CREDIT.
type Asset = Qty[Commodity]

// NewMoney creates a new Money quantity with the given amount and instrument.
func NewMoney(amount decimal.Decimal, instrument Instrument) Money {
	return New[Monetary](amount, instrument)
}

// NewMoneyFromString creates a new Money quantity by parsing the amount string.
func NewMoneyFromString(amount string, instrument Instrument) (Money, error) {
	return NewFromString[Monetary](amount, instrument)
}

// NewMoneyFromInt creates a new Money quantity from an int64 amount.
func NewMoneyFromInt(amount int64, instrument Instrument) Money {
	return NewFromInt[Monetary](amount, instrument)
}

// ZeroMoney creates a zero-valued Money quantity for the given instrument.
func ZeroMoney(instrument Instrument) Money {
	return Zero[Monetary](instrument)
}

// NewAsset creates a new Asset quantity with the given amount and instrument.
func NewAsset(amount decimal.Decimal, instrument Instrument) Asset {
	return New[Commodity](amount, instrument)
}

// NewAssetFromString creates a new Asset quantity by parsing the amount string.
func NewAssetFromString(amount string, instrument Instrument) (Asset, error) {
	return NewFromString[Commodity](amount, instrument)
}

// NewAssetFromInt creates a new Asset quantity from an int64 amount.
func NewAssetFromInt(amount int64, instrument Instrument) Asset {
	return NewFromInt[Commodity](amount, instrument)
}

// ZeroAsset creates a zero-valued Asset quantity for the given instrument.
func ZeroAsset(instrument Instrument) Asset {
	return Zero[Commodity](instrument)
}
