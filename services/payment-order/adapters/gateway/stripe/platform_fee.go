package stripe

import (
	"errors"

	"github.com/shopspring/decimal"
)

// PlatformFeeType specifies how the platform fee is calculated.
type PlatformFeeType string

const (
	// PlatformFeeTypePercentage calculates fee as a percentage of the payment amount.
	PlatformFeeTypePercentage PlatformFeeType = "percentage"
	// PlatformFeeTypeFlat uses a fixed amount in minor units (cents) as the fee.
	PlatformFeeTypeFlat PlatformFeeType = "flat"
)

// MaxPlatformFeePercent is the maximum allowed percentage fee to prevent misconfiguration.
const MaxPlatformFeePercent = 10

// Platform fee errors.
var (
	ErrInvalidFeeType    = errors.New("platform fee type must be 'percentage' or 'flat'")
	ErrNegativeFeeValue  = errors.New("platform fee value must be positive")
	ErrFeeExceedsMaximum = errors.New("platform fee percentage exceeds maximum allowed")
	ErrFeeExceedsAmount  = errors.New("platform fee exceeds payment amount")
)

// PlatformFeeConfig holds the platform fee configuration for a tenant.
type PlatformFeeConfig struct {
	// Type is the fee calculation method: "percentage" or "flat".
	Type PlatformFeeType
	// Value is the fee amount: percentage (e.g., 2.5 for 2.5%) or flat amount in minor units.
	Value decimal.Decimal
}

// Validate checks that the platform fee configuration is valid.
func (c PlatformFeeConfig) Validate() error {
	switch c.Type {
	case PlatformFeeTypePercentage, PlatformFeeTypeFlat:
		// valid type
	default:
		return ErrInvalidFeeType
	}

	if !c.Value.IsPositive() {
		return ErrNegativeFeeValue
	}

	if c.Type == PlatformFeeTypePercentage && c.Value.GreaterThan(decimal.NewFromInt(MaxPlatformFeePercent)) {
		return ErrFeeExceedsMaximum
	}

	return nil
}

// IsZero returns true if the fee config represents no fee.
func (c PlatformFeeConfig) IsZero() bool {
	return c.Value.IsZero()
}

// CalculateFee computes the platform fee in minor units for a given payment amount.
// For percentage fees: fee = amountMinor * value / 100 (truncated to integer).
// For flat fees: fee = value (the configured flat amount in minor units).
// Returns an error if the fee exceeds the payment amount.
func (c PlatformFeeConfig) CalculateFee(amountMinor int64) (int64, error) {
	if c.IsZero() {
		return 0, nil
	}

	if err := c.Validate(); err != nil {
		return 0, err
	}

	var fee int64

	switch c.Type {
	case PlatformFeeTypePercentage:
		amount := decimal.NewFromInt(amountMinor)
		feeDecimal := amount.Mul(c.Value).Div(decimal.NewFromInt(100))
		fee = feeDecimal.IntPart()
	case PlatformFeeTypeFlat:
		fee = c.Value.IntPart()
	default:
		return 0, ErrInvalidFeeType
	}

	if fee > amountMinor {
		return 0, ErrFeeExceedsAmount
	}

	return fee, nil
}
