// Package quantity provides rate conversion for the Universal Asset System.
package quantity

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Sentinel errors for rate operations.
var (
	// ErrRateFromToEqual is returned when From equals To but Factor is not 1.
	ErrRateFromToEqual = errors.New("rate: From and To instruments are equal but Factor is not 1")

	// ErrRateFactorNotPositive is returned when Factor is zero or negative.
	ErrRateFactorNotPositive = errors.New("rate: Factor must be positive and non-zero")

	// ErrRateInvalidTimeRange is returned when ValidFrom is after ValidTo.
	ErrRateInvalidTimeRange = errors.New("rate: ValidFrom must not be after ValidTo")

	// ErrRateInstrumentMismatch is returned when converting a quantity with mismatched From instrument.
	ErrRateInstrumentMismatch = errors.New("rate: quantity instrument does not match rate From instrument")
)

// Rate represents a conversion rate between two instruments.
// It contains the factor to multiply by when converting from the From instrument
// to the To instrument, along with temporal validity bounds.
//
// Rate is designed to be immutable after creation. Use NewRate to create
// validated instances.
type Rate struct {
	// From is the source instrument for this rate.
	From Instrument

	// To is the target instrument for this rate.
	To Instrument

	// Factor is the conversion multiplier. To convert a quantity,
	// multiply its amount by this factor.
	Factor decimal.Decimal

	// ValidFrom is the start of the validity period (inclusive).
	// A zero time means no lower bound.
	ValidFrom time.Time

	// ValidTo is the end of the validity period (inclusive).
	// A zero time means no upper bound.
	ValidTo time.Time
}

// NewRate creates a validated Rate instance.
//
// Validation rules:
//   - Factor must be positive and non-zero
//   - If From equals To, Factor must be 1 (identity rate)
//   - If both ValidFrom and ValidTo are set (non-zero), ValidFrom <= ValidTo
func NewRate(from, to Instrument, factor decimal.Decimal, validFrom, validTo time.Time) (Rate, error) {
	r := Rate{
		From:      from,
		To:        to,
		Factor:    factor,
		ValidFrom: validFrom,
		ValidTo:   validTo,
	}

	if err := r.Validate(); err != nil {
		return Rate{}, err
	}

	return r, nil
}

// IdentityRate creates an identity rate (1:1) for an instrument.
// This can be used when a quantity needs to be "converted" but actually
// remains in the same instrument.
func IdentityRate(inst Instrument) Rate {
	return Rate{
		From:   inst,
		To:     inst,
		Factor: decimal.NewFromInt(1),
	}
}

// Validate checks that the rate is valid.
// This can be used to validate rates that were created without using NewRate
// (e.g., deserialized from storage or received over the wire).
func (r Rate) Validate() error {
	// Factor must be positive and non-zero
	if r.Factor.LessThanOrEqual(decimal.Zero) {
		return ErrRateFactorNotPositive
	}

	// If From equals To, Factor must be 1
	if r.From.Equal(r.To) && !r.Factor.Equal(decimal.NewFromInt(1)) {
		return ErrRateFromToEqual
	}

	// If both time bounds are set, From must not be after To
	if !r.ValidFrom.IsZero() && !r.ValidTo.IsZero() && r.ValidFrom.After(r.ValidTo) {
		return ErrRateInvalidTimeRange
	}

	return nil
}

// Convert applies this rate to convert a Money quantity from the From instrument
// to the To instrument. The result is rounded using Banker's rounding to the
// target instrument's precision.
//
// Returns an error if the quantity's instrument does not match the rate's From instrument.
func (r Rate) Convert(q Money) (Money, error) {
	// Validate instrument match
	if !q.Instrument.Equal(r.From) {
		return Money{}, fmt.Errorf("%w: expected %s, got %s",
			ErrRateInstrumentMismatch, r.From.String(), q.Instrument.String())
	}

	// Multiply by factor
	resultAmount := q.Amount.Mul(r.Factor)

	// Round to target precision using Banker's rounding
	resultAmount = resultAmount.RoundBank(int32(r.To.Precision))

	return Money{
		Amount:     resultAmount,
		Instrument: r.To,
	}, nil
}

// IsValidAt returns true if this rate is valid at the given time.
// A rate is valid if the time falls within [ValidFrom, ValidTo] (inclusive).
// Zero time bounds are treated as unbounded.
func (r Rate) IsValidAt(t time.Time) bool {
	if !r.ValidFrom.IsZero() && t.Before(r.ValidFrom) {
		return false
	}
	if !r.ValidTo.IsZero() && t.After(r.ValidTo) {
		return false
	}
	return true
}

// String returns a human-readable representation of the rate.
func (r Rate) String() string {
	return fmt.Sprintf("%s -> %s @ %s", r.From.String(), r.To.String(), r.Factor.String())
}
