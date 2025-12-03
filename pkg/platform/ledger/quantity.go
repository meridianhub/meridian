package ledger

import (
	"errors"
	"fmt"
	"math"

	"github.com/meridianhub/meridian/pkg/platform/types"
)

// Errors for quantity operations.
var (
	ErrAmountOverflow   = errors.New("amount overflow")
	ErrAmountUnderflow  = errors.New("amount underflow")
	ErrUnitMismatch     = errors.New("unit mismatch")
	ErrDivisionByZero   = errors.New("division by zero")
	ErrNegativeQuantity = errors.New("negative quantity not allowed")
)

// Quantity represents a numeric value with a specific unit type.
// The type parameter U provides compile-time safety against mixing
// incompatible units (e.g., USD cannot be added to air miles).
type Quantity[U UnitMarker] struct {
	amount int64 // Stored in minor units (e.g., cents for USD)
	unit   U
}

// NewQuantity creates a new Quantity with the given unit and amount in minor units.
func NewQuantity[U UnitMarker](unit U, amount int64) types.Result[Quantity[U]] {
	return types.Ok(Quantity[U]{amount: amount, unit: unit})
}

// NewQuantityFromMajor creates a new Quantity from a major unit value.
// For example, NewQuantityFromMajor(USD, 100.50) creates $100.50.
func NewQuantityFromMajor[U UnitMarker](unit U, majorAmount float64) types.Result[Quantity[U]] {
	multiplier := math.Pow10(int(unit.DecimalPlaces()))
	amount := int64(math.Round(majorAmount * multiplier))
	return NewQuantity(unit, amount)
}

// Zero returns a zero Quantity for the given unit.
func Zero[U UnitMarker](unit U) Quantity[U] {
	return Quantity[U]{amount: 0, unit: unit}
}

// Amount returns the amount in minor units.
func (q Quantity[U]) Amount() int64 {
	return q.amount
}

// Unit returns the unit type.
func (q Quantity[U]) Unit() U {
	return q.unit
}

// MajorAmount returns the amount in major units as a float64.
func (q Quantity[U]) MajorAmount() float64 {
	divisor := math.Pow10(int(q.unit.DecimalPlaces()))
	return float64(q.amount) / divisor
}

// IsZero returns true if the amount is zero.
func (q Quantity[U]) IsZero() bool {
	return q.amount == 0
}

// IsNegative returns true if the amount is negative.
func (q Quantity[U]) IsNegative() bool {
	return q.amount < 0
}

// IsPositive returns true if the amount is positive.
func (q Quantity[U]) IsPositive() bool {
	return q.amount > 0
}

// Add adds two quantities of the same unit type.
// Returns an error if overflow would occur.
func (q Quantity[U]) Add(other Quantity[U]) types.Result[Quantity[U]] {
	// Overflow detection: if signs are same, result should have same sign
	result := q.amount + other.amount
	if (other.amount > 0 && result < q.amount) || (other.amount < 0 && result > q.amount) {
		return types.Err[Quantity[U]](ErrAmountOverflow)
	}
	return types.Ok(Quantity[U]{amount: result, unit: q.unit})
}

// Sub subtracts another quantity from this one.
// Returns an error if underflow would occur.
func (q Quantity[U]) Sub(other Quantity[U]) types.Result[Quantity[U]] {
	// Underflow detection: subtraction can overflow in opposite direction
	result := q.amount - other.amount
	if (other.amount > 0 && result > q.amount) || (other.amount < 0 && result < q.amount) {
		return types.Err[Quantity[U]](ErrAmountUnderflow)
	}
	return types.Ok(Quantity[U]{amount: result, unit: q.unit})
}

// Mul multiplies the quantity by a scalar value.
func (q Quantity[U]) Mul(scalar int64) types.Result[Quantity[U]] {
	if scalar == 0 {
		return types.Ok(Zero(q.unit))
	}
	// Check for overflow
	if q.amount != 0 {
		if scalar > 0 {
			if q.amount > 0 && q.amount > math.MaxInt64/scalar {
				return types.Err[Quantity[U]](ErrAmountOverflow)
			}
			if q.amount < 0 && q.amount < math.MinInt64/scalar {
				return types.Err[Quantity[U]](ErrAmountUnderflow)
			}
		} else {
			if q.amount > 0 && q.amount > math.MinInt64/scalar {
				return types.Err[Quantity[U]](ErrAmountUnderflow)
			}
			if q.amount < 0 && q.amount < math.MaxInt64/scalar {
				return types.Err[Quantity[U]](ErrAmountOverflow)
			}
		}
	}
	return types.Ok(Quantity[U]{amount: q.amount * scalar, unit: q.unit})
}

// Div divides the quantity by a scalar value.
// Uses integer division (truncates toward zero).
func (q Quantity[U]) Div(scalar int64) types.Result[Quantity[U]] {
	if scalar == 0 {
		return types.Err[Quantity[U]](ErrDivisionByZero)
	}
	// Special case: MinInt64 / -1 would overflow
	if q.amount == math.MinInt64 && scalar == -1 {
		return types.Err[Quantity[U]](ErrAmountOverflow)
	}
	return types.Ok(Quantity[U]{amount: q.amount / scalar, unit: q.unit})
}

// Negate returns a quantity with the opposite sign.
func (q Quantity[U]) Negate() types.Result[Quantity[U]] {
	if q.amount == math.MinInt64 {
		return types.Err[Quantity[U]](ErrAmountOverflow)
	}
	return types.Ok(Quantity[U]{amount: -q.amount, unit: q.unit})
}

// Abs returns the absolute value of the quantity.
func (q Quantity[U]) Abs() types.Result[Quantity[U]] {
	if q.amount >= 0 {
		return types.Ok(q)
	}
	return q.Negate()
}

// Equal returns true if two quantities are equal.
func (q Quantity[U]) Equal(other Quantity[U]) bool {
	return q.amount == other.amount
}

// Less returns true if this quantity is less than another.
func (q Quantity[U]) Less(other Quantity[U]) bool {
	return q.amount < other.amount
}

// LessOrEqual returns true if this quantity is less than or equal to another.
func (q Quantity[U]) LessOrEqual(other Quantity[U]) bool {
	return q.amount <= other.amount
}

// Greater returns true if this quantity is greater than another.
func (q Quantity[U]) Greater(other Quantity[U]) bool {
	return q.amount > other.amount
}

// GreaterOrEqual returns true if this quantity is greater than or equal to another.
func (q Quantity[U]) GreaterOrEqual(other Quantity[U]) bool {
	return q.amount >= other.amount
}

// Compare returns -1 if q < other, 0 if q == other, 1 if q > other.
func (q Quantity[U]) Compare(other Quantity[U]) int {
	switch {
	case q.amount < other.amount:
		return -1
	case q.amount > other.amount:
		return 1
	default:
		return 0
	}
}

// Min returns the smaller of two quantities.
func (q Quantity[U]) Min(other Quantity[U]) Quantity[U] {
	if q.amount <= other.amount {
		return q
	}
	return other
}

// Max returns the larger of two quantities.
func (q Quantity[U]) Max(other Quantity[U]) Quantity[U] {
	if q.amount >= other.amount {
		return q
	}
	return other
}

// String returns a string representation of the quantity.
func (q Quantity[U]) String() string {
	decimals := q.unit.DecimalPlaces()
	if decimals == 0 {
		return fmt.Sprintf("%d %v", q.amount, q.unit)
	}
	divisor := math.Pow10(int(decimals))
	return fmt.Sprintf("%."+fmt.Sprintf("%d", decimals)+"f %v", float64(q.amount)/divisor, q.unit)
}

// Split divides a quantity into n equal parts, with any remainder
// added to the first part.
func (q Quantity[U]) Split(n int64) types.Result[[]Quantity[U]] {
	if n <= 0 {
		return types.Err[[]Quantity[U]](ErrDivisionByZero)
	}
	base := q.amount / n
	remainder := q.amount % n
	parts := make([]Quantity[U], n)
	for i := range parts {
		parts[i] = Quantity[U]{amount: base, unit: q.unit}
	}
	// Add remainder to first part
	if remainder != 0 {
		parts[0].amount += remainder
	}
	return types.Ok(parts)
}

// Allocate distributes a quantity according to the given ratios.
// For example, Allocate(100, []int64{1, 2, 1}) returns [25, 50, 25].
// Any remainder is distributed to the first parts.
func (q Quantity[U]) Allocate(ratios []int64) types.Result[[]Quantity[U]] {
	if len(ratios) == 0 {
		return types.Err[[]Quantity[U]](ErrDivisionByZero)
	}
	var total int64
	for _, r := range ratios {
		if r < 0 {
			return types.Err[[]Quantity[U]](ErrNegativeQuantity)
		}
		total += r
	}
	if total == 0 {
		return types.Err[[]Quantity[U]](ErrDivisionByZero)
	}
	parts := make([]Quantity[U], len(ratios))
	var allocated int64
	for i, ratio := range ratios {
		parts[i] = Quantity[U]{
			amount: q.amount * ratio / total,
			unit:   q.unit,
		}
		allocated += parts[i].amount
	}
	// Distribute remainder
	remainder := q.amount - allocated
	for i := 0; remainder != 0 && i < len(parts); i++ {
		if remainder > 0 {
			parts[i].amount++
			remainder--
		} else {
			parts[i].amount--
			remainder++
		}
	}
	return types.Ok(parts)
}
