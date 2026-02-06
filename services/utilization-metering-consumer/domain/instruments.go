// Package domain contains the core domain models for utilization metering.
//
// # System Tenant Instruments
//
// This file defines the standard instruments used for utilization metering across
// the Meridian platform. These instruments represent the "Commodity dimension" of
// the Universal Asset System - they track non-monetary quantities like transaction
// counts, API calls, storage usage, and compute time.
//
// All utilization instruments use version 1 and are designed for billing purposes
// in the system tenant (tenant-zero).
package domain

import (
	"strings"

	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// System tenant instrument definitions for utilization metering.
// These instruments are used to track usage quantities for billing.
//
// Instrument naming conventions:
//   - COUNT dimension: whole units, precision 0 (TRANSACTION, API_CALL, OPERATION)
//   - DATA dimension: fractional units for storage, precision 6 (STORAGE_GB_HOUR)
//   - COMPUTE dimension: fractional units for compute time, precision 6 (COMPUTE_HOUR)
var (
	// InstrumentTransaction is used for counting billable transactions.
	// Examples: account creation, payment processing, balance updates.
	// Dimension: COUNT, Precision: 0 (whole transactions only)
	InstrumentTransaction = mustInstrument("TRANSACTION", 1, "COUNT", 0)

	// InstrumentAPICall is used for counting API calls.
	// Examples: REST/gRPC requests to Meridian services.
	// Dimension: COUNT, Precision: 0 (whole API calls only)
	InstrumentAPICall = mustInstrument("API_CALL", 1, "COUNT", 0)

	// InstrumentOperation is used for counting generic operations.
	// This is the fallback instrument for operations that don't have a specific type.
	// Dimension: COUNT, Precision: 0 (whole operations only)
	InstrumentOperation = mustInstrument("OPERATION", 1, "COUNT", 0)

	// InstrumentStorageGBHour is used for storage usage billing.
	// Represents gigabyte-hours of storage consumption.
	// Dimension: DATA, Precision: 6 (supports fractional GB-hours)
	InstrumentStorageGBHour = mustInstrument("STORAGE_GB_HOUR", 1, "DATA", 6)

	// InstrumentComputeHour is used for compute resource billing.
	// Represents hours of compute resource usage (CPU, GPU, etc.).
	// Dimension: COMPUTE, Precision: 6 (supports fractional hours)
	InstrumentComputeHour = mustInstrument("COMPUTE_HOUR", 1, "COMPUTE", 6)
)

// instrumentsByMeasurementType maps string-based unit types to their corresponding instruments.
// This supports backward compatibility with legacy string-based unit specifications.
var instrumentsByMeasurementType = map[string]quantity.Instrument{
	"transaction":     InstrumentTransaction,
	"api_call":        InstrumentAPICall,
	"operation":       InstrumentOperation,
	"storage_gb_hour": InstrumentStorageGBHour,
	"compute_hour":    InstrumentComputeHour,
}

// mustInstrument creates an instrument, panicking on error.
// This is safe because all instrument definitions are compile-time constants
// and errors would indicate programming bugs that should be caught immediately.
func mustInstrument(code string, version uint32, dimension string, precision int) quantity.Instrument {
	inst, err := quantity.NewInstrument(code, version, dimension, precision)
	if err != nil {
		panic("invalid instrument definition: " + err.Error())
	}
	return inst
}

// InstrumentForMeasurementType returns the appropriate instrument for a given unit of measure.
// This maps legacy string-based unit types to typed instruments.
//
// Supported measurement types (case-insensitive):
//   - "transaction" -> InstrumentTransaction
//   - "api_call" -> InstrumentAPICall
//   - "operation" -> InstrumentOperation
//   - "storage_gb_hour" -> InstrumentStorageGBHour
//   - "compute_hour" -> InstrumentComputeHour
//
// If the measurement type is not recognized, InstrumentOperation is returned as a fallback.
func InstrumentForMeasurementType(unitOfMeasure string) quantity.Instrument {
	if inst, ok := instrumentsByMeasurementType[strings.ToLower(unitOfMeasure)]; ok {
		return inst
	}
	// Default to generic operation for unknown types
	return InstrumentOperation
}

// AllInstruments returns all defined utilization instruments.
// Useful for initialization, validation, or reporting purposes.
func AllInstruments() []quantity.Instrument {
	return []quantity.Instrument{
		InstrumentTransaction,
		InstrumentAPICall,
		InstrumentOperation,
		InstrumentStorageGBHour,
		InstrumentComputeHour,
	}
}

// supportedMeasurementTypes is a cached, sorted list of measurement type strings.
// Initialized once at package load to avoid repeated allocation and sorting.
var supportedMeasurementTypes = []string{
	"api_call",
	"compute_hour",
	"operation",
	"storage_gb_hour",
	"transaction",
}

// SupportedMeasurementTypes returns all supported measurement type strings in sorted order.
// These are the valid values for the unitOfMeasure parameter in InstrumentForMeasurementType.
// Returns a copy to prevent caller modification of the cached slice.
func SupportedMeasurementTypes() []string {
	result := make([]string, len(supportedMeasurementTypes))
	copy(result, supportedMeasurementTypes)
	return result
}
