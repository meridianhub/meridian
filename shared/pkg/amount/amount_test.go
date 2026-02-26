package amount_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/amount"
	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// =============================================================================
// Fixtures
// =============================================================================

// mustInstrument creates a quantity.Instrument, panicking on error.
// Safe for use in test fixtures where codes and dimensions are compile-time constants.
func mustInstrument(code string, version uint32, dimension string, precision int) quantity.Instrument {
	inst, err := quantity.NewInstrument(code, version, dimension, precision)
	if err != nil {
		panic("invalid test instrument: " + err.Error())
	}
	return inst
}

var (
	instGBP = mustInstrument("GBP", 0, "CURRENCY", 2)
	instJPY = mustInstrument("JPY", 0, "CURRENCY", 0)
	instKWH = mustInstrument("KWH", 0, "ENERGY", 3)
	instCC  = mustInstrument("CARBON_CREDIT", 0, "CARBON", 4)
)

// =============================================================================
// NewFromInstrument
// =============================================================================

func TestNewFromInstrument_Currency_GBP(t *testing.T) {
	a, err := amount.NewFromInstrument("GBP", "CURRENCY", 2, 10000)
	require.NoError(t, err)
	assert.Equal(t, "100.00", a.Amount().StringFixed(2))
	assert.Equal(t, "GBP", a.InstrumentCode())
	assert.Equal(t, "CURRENCY", a.Dimension())
	assert.Equal(t, 2, a.Precision())
}

func TestNewFromInstrument_Currency_USD(t *testing.T) {
	a, err := amount.NewFromInstrument("USD", "CURRENCY", 2, 5050)
	require.NoError(t, err)
	assert.Equal(t, "50.50", a.Amount().StringFixed(2))
	assert.Equal(t, "USD", a.InstrumentCode())
}

func TestNewFromInstrument_Currency_EUR(t *testing.T) {
	a, err := amount.NewFromInstrument("EUR", "CURRENCY", 2, 9999)
	require.NoError(t, err)
	assert.Equal(t, "99.99", a.Amount().String())
}

func TestNewFromInstrument_Currency_JPY_ZeroPrecision(t *testing.T) {
	// JPY has precision=0 so minor units == major units
	a, err := amount.NewFromInstrument("JPY", "CURRENCY", 0, 1000)
	require.NoError(t, err)
	assert.Equal(t, "1000", a.Amount().String())
	assert.Equal(t, 0, a.Precision())
	assert.Equal(t, "JPY", a.InstrumentCode())
}

func TestNewFromInstrument_Currency_UnrecognizedCode(t *testing.T) {
	_, err := amount.NewFromInstrument("XYZ", "CURRENCY", 2, 100)
	require.Error(t, err)
	assert.ErrorIs(t, err, amount.ErrInvalidDimension)
}

func TestNewFromInstrument_Energy_KWH(t *testing.T) {
	// 1500 minor units at precision=3 → 1.500 KWH
	a, err := amount.NewFromInstrument("KWH", "ENERGY", 3, 1500)
	require.NoError(t, err)
	assert.Equal(t, "1.500", a.Amount().StringFixed(3))
	assert.Equal(t, "KWH", a.InstrumentCode())
	assert.Equal(t, "ENERGY", a.Dimension())
	assert.Equal(t, 3, a.Precision())
}

func TestNewFromInstrument_Carbon_CarbonCredit(t *testing.T) {
	a, err := amount.NewFromInstrument("CARBON_CREDIT", "CARBON", 4, 25000)
	require.NoError(t, err)
	assert.Equal(t, "2.5000", a.Amount().StringFixed(4))
	assert.Equal(t, "CARBON_CREDIT", a.InstrumentCode())
	assert.Equal(t, "CARBON", a.Dimension())
}

func TestNewFromInstrument_Compute_GPUHour(t *testing.T) {
	a, err := amount.NewFromInstrument("GPU_HOUR", "COMPUTE", 6, 2000000)
	require.NoError(t, err)
	assert.Equal(t, "2.000000", a.Amount().StringFixed(6))
	assert.Equal(t, "GPU_HOUR", a.InstrumentCode())
	assert.Equal(t, "COMPUTE", a.Dimension())
}

func TestNewFromInstrument_InvalidDimension(t *testing.T) {
	_, err := amount.NewFromInstrument("FOO", "INVALID_DIM", 2, 100)
	require.Error(t, err)
	assert.ErrorIs(t, err, amount.ErrInvalidDimension)
}

func TestNewFromInstrument_LowercaseDimension(t *testing.T) {
	// dimension is normalized to uppercase
	a, err := amount.NewFromInstrument("KWH", "energy", 3, 1000)
	require.NoError(t, err)
	assert.Equal(t, "ENERGY", a.Dimension())
}

// =============================================================================
// New and Zero
// =============================================================================

func TestNew_FromInstrument(t *testing.T) {
	a := amount.New(instGBP, 5000)
	assert.Equal(t, "50.00", a.Amount().StringFixed(2))
	assert.Equal(t, "GBP", a.InstrumentCode())
}

func TestZero(t *testing.T) {
	a := amount.Zero(instKWH)
	assert.True(t, a.IsZero())
	assert.Equal(t, "KWH", a.InstrumentCode())
	assert.Equal(t, "ENERGY", a.Dimension())
}

func TestZero_Currency(t *testing.T) {
	a := amount.Zero(instGBP)
	assert.True(t, a.IsZero())
	assert.Equal(t, "GBP", a.InstrumentCode())
}

// =============================================================================
// Arithmetic: Add
// =============================================================================

func TestAdd_SameInstrument(t *testing.T) {
	a := amount.New(instGBP, 10000) // £100.00
	b := amount.New(instGBP, 5000)  // £50.00
	result, err := a.Add(b)
	require.NoError(t, err)
	assert.Equal(t, "150.00", result.Amount().StringFixed(2))
	assert.Equal(t, "GBP", result.InstrumentCode())
}

func TestAdd_EnergyInstrument(t *testing.T) {
	a := amount.New(instKWH, 3000) // 3.000 KWH
	b := amount.New(instKWH, 1500) // 1.500 KWH
	result, err := a.Add(b)
	require.NoError(t, err)
	assert.Equal(t, "4.500", result.Amount().StringFixed(3))
}

func TestAdd_MismatchedInstruments(t *testing.T) {
	a := amount.New(instGBP, 10000)
	b := amount.New(instKWH, 1000)
	_, err := a.Add(b)
	require.Error(t, err)
	assert.ErrorIs(t, err, amount.ErrInstrumentMismatch)
}

// =============================================================================
// Arithmetic: Subtract
// =============================================================================

func TestSubtract_SameInstrument(t *testing.T) {
	a := amount.New(instGBP, 10000) // £100.00
	b := amount.New(instGBP, 3000)  // £30.00
	result, err := a.Subtract(b)
	require.NoError(t, err)
	assert.Equal(t, "70.00", result.Amount().StringFixed(2))
}

func TestSubtract_MismatchedInstruments(t *testing.T) {
	a := amount.New(instGBP, 10000)
	b := amount.New(instCC, 1000)
	_, err := a.Subtract(b)
	require.Error(t, err)
	assert.ErrorIs(t, err, amount.ErrInstrumentMismatch)
}

func TestSubtract_ResultNegative(t *testing.T) {
	a := amount.New(instGBP, 3000)  // £30.00
	b := amount.New(instGBP, 10000) // £100.00
	result, err := a.Subtract(b)
	require.NoError(t, err)
	assert.True(t, result.IsNegative())
	assert.Equal(t, "-70.00", result.Amount().StringFixed(2))
}

// =============================================================================
// Negate
// =============================================================================

func TestNegate(t *testing.T) {
	a := amount.New(instGBP, 10000) // £100.00
	neg := a.Negate()
	assert.True(t, neg.IsNegative())
	assert.Equal(t, "-100.00", neg.Amount().StringFixed(2))
}

// =============================================================================
// Multiply
// =============================================================================

func TestMultiply(t *testing.T) {
	a := amount.New(instKWH, 1000) // 1.000 KWH
	result := a.Multiply(decimal.NewFromFloat(2.5))
	assert.Equal(t, "2.500", result.Amount().StringFixed(3))
}

// =============================================================================
// Predicates
// =============================================================================

func TestIsZero(t *testing.T) {
	assert.True(t, amount.Zero(instGBP).IsZero())
	assert.False(t, amount.New(instGBP, 1).IsZero())
}

func TestIsPositive(t *testing.T) {
	assert.True(t, amount.New(instGBP, 100).IsPositive())
	assert.False(t, amount.New(instGBP, 0).IsPositive())
	assert.False(t, amount.New(instGBP, -100).IsPositive())
}

func TestIsNegative(t *testing.T) {
	assert.True(t, amount.New(instGBP, -100).IsNegative())
	assert.False(t, amount.New(instGBP, 0).IsNegative())
	assert.False(t, amount.New(instGBP, 100).IsNegative())
}

// =============================================================================
// Compare and Equals
// =============================================================================

func TestCompare_SameInstrument(t *testing.T) {
	a := amount.New(instGBP, 10000)
	b := amount.New(instGBP, 5000)
	c := amount.New(instGBP, 10000)

	cmp, err := a.Compare(b)
	require.NoError(t, err)
	assert.Equal(t, 1, cmp) // a > b

	cmp, err = b.Compare(a)
	require.NoError(t, err)
	assert.Equal(t, -1, cmp) // b < a

	cmp, err = a.Compare(c)
	require.NoError(t, err)
	assert.Equal(t, 0, cmp) // a == c
}

func TestCompare_MismatchedInstruments(t *testing.T) {
	a := amount.New(instGBP, 10000)
	// Use a different version to trigger mismatch
	instGBPv2 := mustInstrument("GBP", 2, "CURRENCY", 2)
	bv2 := amount.New(instGBPv2, 10000)
	_, err := a.Compare(bv2)
	require.Error(t, err)
	assert.ErrorIs(t, err, amount.ErrInstrumentMismatch)
}

func TestEquals(t *testing.T) {
	a := amount.New(instGBP, 10000)
	b := amount.New(instGBP, 10000)
	c := amount.New(instGBP, 5000)
	d := amount.New(instKWH, 10000)

	assert.True(t, a.Equals(b))
	assert.False(t, a.Equals(c))
	assert.False(t, a.Equals(d))
}

// =============================================================================
// ToMinorUnits
// =============================================================================

func TestToMinorUnits_Currency(t *testing.T) {
	a := amount.New(instGBP, 10000) // £100.00
	minor, err := a.ToMinorUnits()
	require.NoError(t, err)
	assert.Equal(t, int64(10000), minor)
}

func TestToMinorUnits_Energy(t *testing.T) {
	a := amount.New(instKWH, 1500) // 1.500 KWH
	minor, err := a.ToMinorUnits()
	require.NoError(t, err)
	assert.Equal(t, int64(1500), minor)
}

func TestToMinorUnits_JPY(t *testing.T) {
	a := amount.New(instJPY, 10000) // ¥10000
	minor, err := a.ToMinorUnits()
	require.NoError(t, err)
	assert.Equal(t, int64(10000), minor)
}

func TestToMinorUnits_Overflow(t *testing.T) {
	// At precision=2, int64 max is ~9.2*10^18. Store 10^17 major units which after
	// shifting by 2 = 10^19, exceeding int64 max (~9.2*10^18).
	inst := mustInstrument("GBP", 0, "CURRENCY", 2)
	// Construct via decimal directly to avoid int64 overflow in New()
	hugeDecimal := decimal.NewFromInt(1e17) // 100,000,000,000,000,000 → *100 = 10^19 overflows
	hugeAmount := amount.NewFromDecimal(inst, hugeDecimal)
	_, err := hugeAmount.ToMinorUnits()
	require.Error(t, err)
	assert.ErrorIs(t, err, amount.ErrAmountOverflow)
}

func TestToMinorUnitsUnchecked(t *testing.T) {
	a := amount.New(instGBP, 9999) // £99.99
	assert.Equal(t, int64(9999), a.ToMinorUnitsUnchecked())
}

// =============================================================================
// String
// =============================================================================

func TestString_Currency(t *testing.T) {
	a := amount.New(instGBP, 10000)
	assert.Equal(t, "100.00 GBP", a.String())
}

func TestString_Energy(t *testing.T) {
	a := amount.New(instKWH, 1500)
	assert.Equal(t, "1.500 KWH", a.String())
}

func TestString_JPY(t *testing.T) {
	a := amount.New(instJPY, 1000)
	assert.Equal(t, "1000 JPY", a.String())
}

func TestString_Carbon(t *testing.T) {
	a := amount.New(instCC, 25000)
	assert.Equal(t, "2.5000 CARBON_CREDIT", a.String())
}

// =============================================================================
// NewFromInstrument precision override for CURRENCY
// =============================================================================

func TestNewFromInstrument_Currency_PrecisionOverrideIgnored(t *testing.T) {
	// Caller passes precision=5 but GBP canonical precision=2 should be used
	a, err := amount.NewFromInstrument("GBP", "CURRENCY", 5, 10000)
	require.NoError(t, err)
	// canonical GBP precision=2 → 10000 minor units = £100.00
	assert.Equal(t, 2, a.Precision())
	assert.Equal(t, "100.00", a.Amount().StringFixed(2))
}
