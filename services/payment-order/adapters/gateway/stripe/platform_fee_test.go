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
			name:   "valid percentage",
			config: PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromFloat(2.5)},
		},
		{
			name:   "valid flat fee",
			config: PlatformFeeConfig{Type: PlatformFeeTypeFlat, Value: decimal.NewFromInt(150)},
		},
		{
			name:   "max percentage is valid",
			config: PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromInt(MaxPlatformFeePercent)},
		},
		{
			name:    "invalid fee type",
			config:  PlatformFeeConfig{Type: "unknown", Value: decimal.NewFromInt(100)},
			wantErr: ErrInvalidFeeType,
		},
		{
			name:    "negative value",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromInt(-1)},
			wantErr: ErrNegativeFeeValue,
		},
		{
			name:    "zero value",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.Zero},
			wantErr: ErrNegativeFeeValue,
		},
		{
			name:    "percentage exceeds maximum",
			config:  PlatformFeeConfig{Type: PlatformFeeTypePercentage, Value: decimal.NewFromFloat(10.1)},
			wantErr: ErrFeeExceedsMaximum,
		},
		{
			name:   "flat fee has no maximum constraint",
			config: PlatformFeeConfig{Type: PlatformFeeTypeFlat, Value: decimal.NewFromInt(99999)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPlatformFeeConfig_CalculateFee_Percentage(t *testing.T) {
	tests := []struct {
		name        string
		percent     float64
		amountMinor int64
		expectedFee int64
	}{
		{"2.5% of 10000", 2.5, 10000, 250},
		{"2.5% of 9999", 2.5, 9999, 249},
		{"1% of 100", 1.0, 100, 1},
		{"5% of 20000", 5.0, 20000, 1000},
		{"0.5% of 1000", 0.5, 1000, 5},
		{"2.5% of 1", 2.5, 1, 0}, // truncated to 0
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := PlatformFeeConfig{
				Type:  PlatformFeeTypePercentage,
				Value: decimal.NewFromFloat(tt.percent),
			}
			fee, err := config.CalculateFee(tt.amountMinor)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedFee, fee)
		})
	}
}

func TestPlatformFeeConfig_CalculateFee_Flat(t *testing.T) {
	tests := []struct {
		name        string
		flatFee     int64
		amountMinor int64
		expectedFee int64
	}{
		{"150 cents flat on 10000", 150, 10000, 150},
		{"50 cents flat on 100", 50, 100, 50},
		{"1000 cents flat on 5000", 1000, 5000, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := PlatformFeeConfig{
				Type:  PlatformFeeTypeFlat,
				Value: decimal.NewFromInt(tt.flatFee),
			}
			fee, err := config.CalculateFee(tt.amountMinor)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedFee, fee)
		})
	}
}

func TestPlatformFeeConfig_CalculateFee_FlatExceedsAmount(t *testing.T) {
	config := PlatformFeeConfig{
		Type:  PlatformFeeTypeFlat,
		Value: decimal.NewFromInt(500),
	}
	_, err := config.CalculateFee(100)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFeeExceedsAmount)
}

func TestPlatformFeeConfig_CalculateFee_ZeroConfig(t *testing.T) {
	config := PlatformFeeConfig{
		Type:  PlatformFeeTypePercentage,
		Value: decimal.Zero,
	}
	fee, err := config.CalculateFee(10000)
	require.NoError(t, err)
	assert.Equal(t, int64(0), fee)
}

func TestPlatformFeeConfig_IsZero(t *testing.T) {
	assert.True(t, PlatformFeeConfig{Value: decimal.Zero}.IsZero())
	assert.False(t, PlatformFeeConfig{Value: decimal.NewFromInt(1)}.IsZero())
}
