package domain_test

// Amount type integration tests for position-keeping.
//
// These tests verify that the Amount type re-exported from shared/pkg/amount can be used
// to represent multi-dimension positions (KWH, CARBON_CREDIT, GPU_HOUR) alongside the
// existing Money type (currency-only) within the position-keeping domain.
//
// The Position domain already supports multi-dimension tracking via string InstrumentCode
// and Dimension fields. The Amount type provides a strongly-typed, dimension-aware value
// object for use when crossing service boundaries or computing aggregated position values.

import (
	"testing"

	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAmountType_KWHPosition(t *testing.T) {
	// Create an energy instrument (KWH) using NewInstrument
	inst, err := domain.NewInstrument("KWH", 0, "ENERGY", 3)
	require.NoError(t, err)

	// Create an Amount representing 1.500 KWH (1500 milli-KWH minor units)
	amt := domain.NewAmount(inst, 1500)

	assert.Equal(t, "KWH", amt.InstrumentCode())
	assert.Equal(t, "ENERGY", amt.Dimension())
	assert.Equal(t, 3, amt.Precision())
	assert.True(t, decimal.NewFromFloat(1.5).Equal(amt.Amount()))

	// Minor units round-trip
	minor, err := amt.ToMinorUnits()
	require.NoError(t, err)
	assert.Equal(t, int64(1500), minor)
}

func TestAmountType_CarbonCreditPosition(t *testing.T) {
	// Create a carbon instrument (CARBON_CREDIT) using NewInstrument
	inst, err := domain.NewInstrument("CARBON_CREDIT", 0, "CARBON", 2)
	require.NoError(t, err)

	// Create an Amount representing 10.50 carbon credits (1050 minor units)
	amt := domain.NewAmount(inst, 1050)

	assert.Equal(t, "CARBON_CREDIT", amt.InstrumentCode())
	assert.Equal(t, "CARBON", amt.Dimension())
	assert.Equal(t, 2, amt.Precision())
	assert.True(t, decimal.NewFromFloat(10.5).Equal(amt.Amount()))
}

func TestAmountType_FromDecimal_KWH(t *testing.T) {
	inst, err := domain.NewInstrument("KWH", 0, "ENERGY", 3)
	require.NoError(t, err)

	// Create from a major-unit decimal (e.g., computed result)
	amt := domain.NewAmountFromDecimal(inst, decimal.NewFromFloat(2.750))

	assert.True(t, decimal.NewFromFloat(2.750).Equal(amt.Amount()))
	assert.Equal(t, "KWH", amt.InstrumentCode())
}

func TestAmountType_FromInstrument_CurrencyDelegates(t *testing.T) {
	// For CURRENCY dimension, precision should be resolved from currency registry
	amt, err := domain.NewAmountFromInstrument("GBP", "CURRENCY", 99 /* ignored */, 10000)
	require.NoError(t, err)

	// GBP has canonical precision 2, so 10000 minor units = 100.00
	assert.Equal(t, "GBP", amt.InstrumentCode())
	assert.Equal(t, "CURRENCY", amt.Dimension())
	assert.Equal(t, 2, amt.Precision())
	assert.True(t, decimal.NewFromFloat(100.0).Equal(amt.Amount()))
}

func TestAmountType_FromInstrument_EnergyPrecision(t *testing.T) {
	// For non-currency dimensions, caller-supplied precision is used
	amt, err := domain.NewAmountFromInstrument("KWH", "ENERGY", 3, 1500)
	require.NoError(t, err)

	assert.Equal(t, "KWH", amt.InstrumentCode())
	assert.Equal(t, "ENERGY", amt.Dimension())
	assert.Equal(t, 3, amt.Precision())
	assert.True(t, decimal.NewFromFloat(1.5).Equal(amt.Amount()))
}

func TestAmountType_FromInstrument_InvalidDimension(t *testing.T) {
	// Unknown dimension should return ErrInvalidDimension
	_, err := domain.NewAmountFromInstrument("XYZ", "UNKNOWN_DIM", 2, 100)
	require.Error(t, err)
}

func TestAmountType_Add_KWH(t *testing.T) {
	inst, err := domain.NewInstrument("KWH", 0, "ENERGY", 3)
	require.NoError(t, err)

	a := domain.NewAmount(inst, 1000) // 1.000 KWH
	b := domain.NewAmount(inst, 500)  // 0.500 KWH

	sum, err := a.Add(b)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(1.5).Equal(sum.Amount()))
	assert.Equal(t, "KWH", sum.InstrumentCode())
}

func TestAmountType_Add_MismatchedInstruments(t *testing.T) {
	instKWH, err := domain.NewInstrument("KWH", 0, "ENERGY", 3)
	require.NoError(t, err)

	instCarbon, err := domain.NewInstrument("CARBON_CREDIT", 0, "CARBON", 2)
	require.NoError(t, err)

	kwh := domain.NewAmount(instKWH, 1000)
	carbon := domain.NewAmount(instCarbon, 100)

	_, err = kwh.Add(carbon)
	require.Error(t, err, "expected error when adding KWH and CARBON_CREDIT amounts")
}

func TestAmountType_ZeroAmount(t *testing.T) {
	inst, err := domain.NewInstrument("KWH", 0, "ENERGY", 3)
	require.NoError(t, err)

	zero := domain.ZeroAmount(inst)
	assert.True(t, zero.IsZero())
	assert.Equal(t, "KWH", zero.InstrumentCode())
}

func TestAmountType_GPUHourCompute(t *testing.T) {
	// Verify GPU_HOUR compute positions can be tracked
	inst, err := domain.NewInstrument("GPU_HOUR", 0, "COMPUTE", 4)
	require.NoError(t, err)

	// 2.5000 GPU hours = 25000 minor units
	amt := domain.NewAmount(inst, 25000)

	assert.Equal(t, "GPU_HOUR", amt.InstrumentCode())
	assert.Equal(t, "COMPUTE", amt.Dimension())
	assert.True(t, decimal.NewFromFloat(2.5).Equal(amt.Amount()))
	assert.False(t, amt.IsZero())
	assert.True(t, amt.IsPositive())
}

func TestAmountType_PositionCompatibility(t *testing.T) {
	// Verify that Amount values can be used alongside Position records
	// by deriving Position fields from Amount metadata
	inst, err := domain.NewInstrument("KWH", 0, "ENERGY", 3)
	require.NoError(t, err)

	amt := domain.NewAmountFromDecimal(inst, decimal.NewFromFloat(1500.750))

	// Derive Position fields from Amount (typical cross-service pattern)
	assert.Equal(t, "KWH", amt.InstrumentCode())
	assert.Equal(t, "ENERGY", amt.Dimension())

	// Verify the amount value maps to what a Position would store
	assert.True(t, decimal.NewFromFloat(1500.750).Equal(amt.Amount()))
}
