package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/quantity"
)

func TestInstrumentTransaction_Valid(t *testing.T) {
	inst := InstrumentTransaction

	assert.Equal(t, "TRANSACTION", inst.Code)
	assert.Equal(t, uint32(1), inst.Version)
	assert.Equal(t, "COUNT", inst.Dimension)
	assert.Equal(t, 0, inst.Precision)

	// Validate the instrument
	err := inst.Validate()
	require.NoError(t, err)

	// Should be a commodity, not monetary
	assert.True(t, inst.IsCommodity())
	assert.False(t, inst.IsMonetary())
}

func TestInstrumentAPICall_Valid(t *testing.T) {
	inst := InstrumentAPICall

	assert.Equal(t, "API_CALL", inst.Code)
	assert.Equal(t, uint32(1), inst.Version)
	assert.Equal(t, "COUNT", inst.Dimension)
	assert.Equal(t, 0, inst.Precision)

	err := inst.Validate()
	require.NoError(t, err)

	assert.True(t, inst.IsCommodity())
	assert.False(t, inst.IsMonetary())
}

func TestInstrumentOperation_Valid(t *testing.T) {
	inst := InstrumentOperation

	assert.Equal(t, "OPERATION", inst.Code)
	assert.Equal(t, uint32(1), inst.Version)
	assert.Equal(t, "COUNT", inst.Dimension)
	assert.Equal(t, 0, inst.Precision)

	err := inst.Validate()
	require.NoError(t, err)

	assert.True(t, inst.IsCommodity())
	assert.False(t, inst.IsMonetary())
}

func TestInstrumentStorageGBHour_Valid(t *testing.T) {
	inst := InstrumentStorageGBHour

	assert.Equal(t, "STORAGE_GB_HOUR", inst.Code)
	assert.Equal(t, uint32(1), inst.Version)
	assert.Equal(t, "DATA", inst.Dimension)
	assert.Equal(t, 6, inst.Precision)

	err := inst.Validate()
	require.NoError(t, err)

	assert.True(t, inst.IsCommodity())
	assert.False(t, inst.IsMonetary())
}

func TestInstrumentComputeHour_Valid(t *testing.T) {
	inst := InstrumentComputeHour

	assert.Equal(t, "COMPUTE_HOUR", inst.Code)
	assert.Equal(t, uint32(1), inst.Version)
	assert.Equal(t, "COMPUTE", inst.Dimension)
	assert.Equal(t, 6, inst.Precision)

	err := inst.Validate()
	require.NoError(t, err)

	assert.True(t, inst.IsCommodity())
	assert.False(t, inst.IsMonetary())
}

func TestAllInstruments_Valid(t *testing.T) {
	instruments := AllInstruments()

	// Should return all 5 defined instruments
	assert.Len(t, instruments, 5)

	// Each instrument should be valid
	for _, inst := range instruments {
		err := inst.Validate()
		require.NoError(t, err, "instrument %s should be valid", inst.Code)

		// All utilization instruments are commodities
		assert.True(t, inst.IsCommodity(), "instrument %s should be a commodity", inst.Code)
		assert.False(t, inst.IsMonetary(), "instrument %s should not be monetary", inst.Code)
	}

	// Verify all expected instruments are present
	codes := make(map[string]bool)
	for _, inst := range instruments {
		codes[inst.Code] = true
	}
	assert.True(t, codes["TRANSACTION"])
	assert.True(t, codes["API_CALL"])
	assert.True(t, codes["OPERATION"])
	assert.True(t, codes["STORAGE_GB_HOUR"])
	assert.True(t, codes["COMPUTE_HOUR"])
}

func TestInstrumentForMeasurementType_KnownTypes(t *testing.T) {
	tests := []struct {
		measurementType string
		expectedCode    string
	}{
		{"transaction", "TRANSACTION"},
		{"api_call", "API_CALL"},
		{"operation", "OPERATION"},
		{"storage_gb_hour", "STORAGE_GB_HOUR"},
		{"compute_hour", "COMPUTE_HOUR"},
	}

	for _, tt := range tests {
		t.Run(tt.measurementType, func(t *testing.T) {
			inst := InstrumentForMeasurementType(tt.measurementType)
			assert.Equal(t, tt.expectedCode, inst.Code)
		})
	}
}

func TestInstrumentForMeasurementType_UnknownTypes_FallbackToOperation(t *testing.T) {
	unknownTypes := []string{
		"unknown",
		"custom_metric",
		"",
		"api-call", // wrong separator
	}

	for _, unknownType := range unknownTypes {
		t.Run(unknownType, func(t *testing.T) {
			inst := InstrumentForMeasurementType(unknownType)
			// Should fallback to InstrumentOperation
			assert.Equal(t, "OPERATION", inst.Code)
			assert.Equal(t, InstrumentOperation, inst)
		})
	}
}

func TestInstrumentForMeasurementType_CaseInsensitive(t *testing.T) {
	// Mapping should be case-insensitive for convenience
	tests := []struct {
		input    string
		expected quantity.Instrument
	}{
		{"transaction", InstrumentTransaction},
		{"TRANSACTION", InstrumentTransaction},
		{"Transaction", InstrumentTransaction},
		{"API_CALL", InstrumentAPICall},
		{"Api_Call", InstrumentAPICall},
		{"STORAGE_GB_HOUR", InstrumentStorageGBHour},
		{"Compute_Hour", InstrumentComputeHour},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			inst := InstrumentForMeasurementType(tt.input)
			assert.Equal(t, tt.expected, inst)
		})
	}
}

func TestSupportedMeasurementTypes(t *testing.T) {
	types := SupportedMeasurementTypes()

	// Should return all 5 supported types
	assert.Len(t, types, 5)

	// Convert to map for easy checking
	typeMap := make(map[string]bool)
	for _, typ := range types {
		typeMap[typ] = true
	}

	assert.True(t, typeMap["transaction"])
	assert.True(t, typeMap["api_call"])
	assert.True(t, typeMap["operation"])
	assert.True(t, typeMap["storage_gb_hour"])
	assert.True(t, typeMap["compute_hour"])
}

func TestInstrumentForMeasurementType_ReturnsEqualInstrument(t *testing.T) {
	// Verify that the returned instruments are equal to the defined constants
	assert.Equal(t, InstrumentTransaction, InstrumentForMeasurementType("transaction"))
	assert.Equal(t, InstrumentAPICall, InstrumentForMeasurementType("api_call"))
	assert.Equal(t, InstrumentOperation, InstrumentForMeasurementType("operation"))
	assert.Equal(t, InstrumentStorageGBHour, InstrumentForMeasurementType("storage_gb_hour"))
	assert.Equal(t, InstrumentComputeHour, InstrumentForMeasurementType("compute_hour"))
}

func TestInstruments_UsableInQuantityOperations(t *testing.T) {
	// Verify that instruments can be used to create quantities
	txnQty := quantity.NewAssetFromInt(100, InstrumentTransaction)
	assert.Equal(t, "100 TRANSACTION", txnQty.String())

	apiQty := quantity.NewAssetFromInt(500, InstrumentAPICall)
	assert.Equal(t, "500 API_CALL", apiQty.String())

	// Storage with precision 6
	storageQty := quantity.NewAssetFromInt(25, InstrumentStorageGBHour)
	assert.Equal(t, "25.000000 STORAGE_GB_HOUR", storageQty.String())

	// Compute with precision 6
	computeQty := quantity.NewAssetFromInt(8, InstrumentComputeHour)
	assert.Equal(t, "8.000000 COMPUTE_HOUR", computeQty.String())
}

func TestInstruments_SameInstrumentAddition(t *testing.T) {
	// Quantities with same instrument can be added
	qty1 := quantity.NewAssetFromInt(100, InstrumentTransaction)
	qty2 := quantity.NewAssetFromInt(50, InstrumentTransaction)

	result, err := qty1.Add(qty2)
	require.NoError(t, err)
	assert.Equal(t, int64(150), result.Amount.IntPart())
}

func TestInstruments_DifferentInstrumentsCannotBeAdded(t *testing.T) {
	// Quantities with different instruments cannot be added
	txnQty := quantity.NewAssetFromInt(100, InstrumentTransaction)
	apiQty := quantity.NewAssetFromInt(50, InstrumentAPICall)

	_, err := txnQty.Add(apiQty)
	require.Error(t, err)
	assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)
}

func TestInstruments_CountDimensionHasZeroPrecision(t *testing.T) {
	// COUNT dimension instruments should have precision 0 (whole units only)
	countInstruments := []quantity.Instrument{
		InstrumentTransaction,
		InstrumentAPICall,
		InstrumentOperation,
	}

	for _, inst := range countInstruments {
		assert.Equal(t, 0, inst.Precision, "COUNT dimension instrument %s should have precision 0", inst.Code)
		assert.Equal(t, "COUNT", inst.Dimension)
	}
}

func TestInstruments_FractionalDimensionsHaveHigherPrecision(t *testing.T) {
	// DATA and COMPUTE dimensions should have precision 6 for fractional values
	fractionalInstruments := []quantity.Instrument{
		InstrumentStorageGBHour,
		InstrumentComputeHour,
	}

	for _, inst := range fractionalInstruments {
		assert.Equal(t, 6, inst.Precision, "fractional instrument %s should have precision 6", inst.Code)
	}
}

func TestInstruments_Version(t *testing.T) {
	// All instruments should use version 1
	for _, inst := range AllInstruments() {
		assert.Equal(t, uint32(1), inst.Version, "instrument %s should have version 1", inst.Code)
	}
}
