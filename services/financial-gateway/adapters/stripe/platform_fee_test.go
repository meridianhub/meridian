package stripe

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlatformFeeConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  PlatformFeeConfig
		wantErr error
	}{
		{
			name:   "valid percentage fee",
			config: PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromFloat(2.5)},
		},
		{
			name:   "valid flat fee",
			config: PlatformFeeConfig{Type: PlatformFeeTypeFlat, Value: decimal.NewFromInt(500)},
		},
		{
			name:   "percentage at maximum (10%)",
			config: PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromInt(MaxPlatformFeePercent)},
		},
		{
			name:    "invalid fee type",
			config:  PlatformFeeConfig{Type: "bogus", Value: decimal.NewFromInt(5)},
			wantErr: ErrInvalidFeeType,
		},
		{
			name:    "empty fee type",
			config:  PlatformFeeConfig{Type: "", Value: decimal.NewFromInt(5)},
			wantErr: ErrInvalidFeeType,
		},
		{
			name:    "zero fee value",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.Zero},
			wantErr: ErrNegativeFeeValue,
		},
		{
			name:    "negative fee value",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromInt(-1)},
			wantErr: ErrNegativeFeeValue,
		},
		{
			name:    "percentage exceeds maximum",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromFloat(10.01)},
			wantErr: ErrFeeExceedsMaximum,
		},
		{
			name:   "flat fee above 10 is allowed (max only applies to percentage)",
			config: PlatformFeeConfig{Type: PlatformFeeTypeFlat, Value: decimal.NewFromInt(50000)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPlatformFeeConfig_IsZero(t *testing.T) {
	assert.True(t, PlatformFeeConfig{Value: decimal.Zero}.IsZero())
	assert.True(t, PlatformFeeConfig{}.IsZero())
	assert.False(t, PlatformFeeConfig{Value: decimal.NewFromInt(1)}.IsZero())
}

func TestPlatformFeeConfig_CalculateFee(t *testing.T) {
	tests := []struct {
		name    string
		config  PlatformFeeConfig
		amount  int64
		wantFee int64
		wantErr error
	}{
		// Happy path: percentage
		{
			name:    "2.5% of 10000 = 250",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromFloat(2.5)},
			amount:  10000,
			wantFee: 250,
		},
		{
			name:    "10% of 10000 = 1000",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromInt(10)},
			amount:  10000,
			wantFee: 1000,
		},
		{
			name:    "1% of 100 = 1",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromInt(1)},
			amount:  100,
			wantFee: 1,
		},

		// Happy path: flat
		{
			name:    "flat 500 on 10000",
			config:  PlatformFeeConfig{Type: PlatformFeeTypeFlat, Value: decimal.NewFromInt(500)},
			amount:  10000,
			wantFee: 500,
		},
		{
			name:    "flat fee equals amount (boundary)",
			config:  PlatformFeeConfig{Type: PlatformFeeTypeFlat, Value: decimal.NewFromInt(100)},
			amount:  100,
			wantFee: 100,
		},

		// Rounding behavior: percentage fees use round-half-up to prevent
		// systematic revenue leakage from truncation.
		{
			name:    "1% of 33 = 0 (0.33 rounds down)",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromInt(1)},
			amount:  33,
			wantFee: 0,
		},
		{
			name:    "2.5% of 1 = 0 (0.025 rounds down)",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromFloat(2.5)},
			amount:  1,
			wantFee: 0,
		},
		{
			name:    "2.5% of 1001 = 25 (25.025 rounds down)",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromFloat(2.5)},
			amount:  1001,
			wantFee: 25,
		},
		{
			name:    "3% of 199 = 6 (5.97 rounds up)",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromInt(3)},
			amount:  199,
			wantFee: 6,
		},
		{
			name:    "2.5% of 100 = 3 (2.5 rounds up — half-up rule)",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromFloat(2.5)},
			amount:  100,
			wantFee: 3,
		},

		// Zero amount: fee should be zero
		{
			name:    "zero config returns zero immediately",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.Zero},
			amount:  10000,
			wantFee: 0,
		},
		{
			name:    "percentage on zero amount",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromFloat(2.5)},
			amount:  0,
			wantFee: 0,
		},

		// Error: flat fee exceeds payment amount
		{
			name:    "flat fee exceeds amount",
			config:  PlatformFeeConfig{Type: PlatformFeeTypeFlat, Value: decimal.NewFromInt(200)},
			amount:  100,
			wantErr: ErrFeeExceedsAmount,
		},

		// Error: validation failures propagate
		{
			name:    "invalid fee type",
			config:  PlatformFeeConfig{Type: "bogus", Value: decimal.NewFromInt(5)},
			amount:  1000,
			wantErr: ErrInvalidFeeType,
		},
		{
			name:    "negative fee value",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromInt(-1)},
			amount:  1000,
			wantErr: ErrNegativeFeeValue,
		},
		{
			name:    "percentage exceeds maximum",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromFloat(15)},
			amount:  1000,
			wantErr: ErrFeeExceedsMaximum,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fee, err := tt.config.CalculateFee(tt.amount)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Equal(t, int64(0), fee)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantFee, fee, "fee mismatch for amount=%d", tt.amount)
			}
		})
	}
}

// TestPlatformFeeConfig_CalculateFee_DecimalPrecision verifies that decimal
// arithmetic avoids floating-point drift. These specific values would produce
// wrong results with float64 math.
func TestPlatformFeeConfig_CalculateFee_DecimalPrecision(t *testing.T) {
	cfg := PlatformFeeConfig{
		Type:  PlatformFeeTypePercentage,
		Value: decimal.NewFromFloat(2.5),
	}

	// 2.5% of 999999 = 24999.975 → rounds to 25000
	// float64 might give 24999.974999... or 25000.000...001
	fee, err := cfg.CalculateFee(999999)
	require.NoError(t, err)
	assert.Equal(t, int64(25000), fee)

	// 2.5% of 4 = 0.1 → rounds to 0
	fee, err = cfg.CalculateFee(4)
	require.NoError(t, err)
	assert.Equal(t, int64(0), fee)
}
