package integration

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedamount "github.com/meridianhub/meridian/shared/pkg/amount"
	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// TestMultiAsset_AccountCreationWithDifferentDimensions verifies that domain-level
// account creation works with all supported instrument dimensions, not just CURRENCY.
// This is the core multi-asset purity contract: any valid dimension + code + precision
// combination must be accepted by the domain constructors.
func TestMultiAsset_AccountCreationWithDifferentDimensions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	instruments := []struct {
		name      string
		code      string
		dimension string
		precision int
	}{
		{"currency_GBP", "GBP", "CURRENCY", 2},
		{"currency_JPY", "JPY", "CURRENCY", 0},
		{"energy_KWH", "KWH", "ENERGY", 3},
		{"energy_MWH", "MWH", "ENERGY", 6},
		{"compute_GPU_HOUR", "GPU_HOUR", "COMPUTE", 4},
		{"carbon_CARBON_CREDIT", "CARBON_CREDIT", "CARBON", 0},
		{"data_GB", "GB", "DATA", 2},
		{"count_VOUCHER", "VOUCHER", "COUNT", 0},
	}

	for _, tc := range instruments {
		t.Run(tc.name, func(t *testing.T) {
			inst, err := quantity.NewInstrument(tc.code, 0, tc.dimension, tc.precision)
			require.NoError(t, err, "failed to create instrument %s/%s", tc.code, tc.dimension)

			assert.Equal(t, tc.code, inst.Code)
			assert.Equal(t, tc.dimension, inst.Dimension)
			assert.Equal(t, tc.precision, inst.Precision)

			// Verify zero amount creation
			zero := sharedamount.Zero(inst)
			assert.True(t, zero.Amount().IsZero())
			assert.Equal(t, tc.code, zero.InstrumentCode())
			assert.Equal(t, tc.dimension, zero.Dimension())

			// Verify amount creation with minor units
			amt := sharedamount.New(inst, 1500)
			assert.False(t, amt.Amount().IsZero())
			assert.Equal(t, tc.code, amt.InstrumentCode())
		})
	}
}

// TestMultiAsset_AmountArithmeticAcrossDimensions verifies that arithmetic operations
// (add, subtract, compare) work correctly for non-currency instruments and that
// cross-dimension operations are rejected.
func TestMultiAsset_AmountArithmeticAcrossDimensions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	kwhInst, err := quantity.NewInstrument("KWH", 0, "ENERGY", 3)
	require.NoError(t, err)
	gpuInst, err := quantity.NewInstrument("GPU_HOUR", 0, "COMPUTE", 4)
	require.NoError(t, err)

	t.Run("same_instrument_addition", func(t *testing.T) {
		a := sharedamount.New(kwhInst, 1500) // 1.500 KWH
		b := sharedamount.New(kwhInst, 2500) // 2.500 KWH

		sum, err := a.Add(b)
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(4).Equal(sum.Amount()), "expected 4, got %s", sum.Amount())
		assert.Equal(t, "KWH", sum.InstrumentCode())
	})

	t.Run("same_instrument_subtraction", func(t *testing.T) {
		a := sharedamount.New(kwhInst, 5000) // 5.000 KWH
		b := sharedamount.New(kwhInst, 2000) // 2.000 KWH

		diff, err := a.Subtract(b)
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(3).Equal(diff.Amount()), "expected 3, got %s", diff.Amount())
	})

	t.Run("same_instrument_comparison", func(t *testing.T) {
		a := sharedamount.New(kwhInst, 5000)
		b := sharedamount.New(kwhInst, 3000)

		cmp, err := a.Compare(b)
		require.NoError(t, err)
		assert.Equal(t, 1, cmp) // a > b
	})

	t.Run("cross_dimension_addition_rejected", func(t *testing.T) {
		kwh := sharedamount.New(kwhInst, 1500)
		gpu := sharedamount.New(gpuInst, 1000)

		_, err := kwh.Add(gpu)
		require.Error(t, err)
		assert.ErrorIs(t, err, sharedamount.ErrInstrumentMismatch)
	})

	t.Run("cross_dimension_subtraction_rejected", func(t *testing.T) {
		kwh := sharedamount.New(kwhInst, 5000)
		gpu := sharedamount.New(gpuInst, 1000)

		_, err := kwh.Subtract(gpu)
		require.Error(t, err)
		assert.ErrorIs(t, err, sharedamount.ErrInstrumentMismatch)
	})
}

// TestMultiAsset_PrecisionHandling verifies that precision is respected correctly
// for instruments with different decimal places.
func TestMultiAsset_PrecisionHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tests := []struct {
		name       string
		code       string
		dimension  string
		precision  int
		minorUnits int64
		expected   string // expected major-unit decimal string
	}{
		{"GBP_2dp", "GBP", "CURRENCY", 2, 10050, "100.50"},
		{"JPY_0dp", "JPY", "CURRENCY", 0, 1000, "1000"},
		{"KWH_3dp", "KWH", "ENERGY", 3, 1500, "1.500"},
		{"MWH_6dp", "MWH", "ENERGY", 6, 1500000, "1.500000"},
		{"GPU_4dp", "GPU_HOUR", "COMPUTE", 4, 25000, "2.5000"},
		{"CARBON_0dp", "CARBON_CREDIT", "CARBON", 0, 100, "100"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst, err := quantity.NewInstrument(tc.code, 0, tc.dimension, tc.precision)
			require.NoError(t, err)

			amt := sharedamount.New(inst, tc.minorUnits)
			assert.Equal(t, tc.expected, amt.Amount().StringFixed(int32(tc.precision)))
		})
	}
}

// TestMultiAsset_InvalidDimensionRejected verifies that unrecognized dimensions
// are rejected at instrument construction time.
func TestMultiAsset_InvalidDimensionRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	_, err := quantity.NewInstrument("UNOBTAINIUM", 0, "FANTASY", 2)
	require.Error(t, err)
}

// TestMultiAsset_NewFromInstrumentCurrencyPath verifies that the NewFromInstrument
// constructor for CURRENCY dimension still works via the legacy registry path
// (this is the backward-compatible path used by persistence layer reconstruction).
func TestMultiAsset_NewFromInstrumentCurrencyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// CURRENCY path uses the currency registry for precision lookup
	amt, err := sharedamount.NewFromInstrument("GBP", "CURRENCY", 2, 10000)
	require.NoError(t, err)
	assert.Equal(t, "100.00", amt.Amount().StringFixed(2))
	assert.Equal(t, "GBP", amt.InstrumentCode())

	// Non-CURRENCY path uses caller-provided precision directly
	kwh, err := sharedamount.NewFromInstrument("KWH", "ENERGY", 3, 1500)
	require.NoError(t, err)
	assert.Equal(t, "1.500", kwh.Amount().StringFixed(3))
	assert.Equal(t, "KWH", kwh.InstrumentCode())
	assert.Equal(t, "ENERGY", kwh.Dimension())
}
