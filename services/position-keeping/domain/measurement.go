package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Measurement domain errors
var (
	// ErrInvalidMeasurementType is returned when the measurement type is invalid
	ErrInvalidMeasurementType = errors.New("invalid measurement type")
	// ErrNegativeMeasurementValue is returned when a negative measurement value is provided
	ErrNegativeMeasurementValue = errors.New("measurement value must be positive")
	// ErrInvalidUnit is returned when the measurement unit is invalid
	ErrInvalidUnit = errors.New("invalid measurement unit")
	// ErrFutureTimestamp is returned when the measurement timestamp is in the future
	ErrFutureTimestamp = errors.New("measurement timestamp cannot be in the future")
	// ErrEmptyPositionStateID is returned when position state ID is empty
	ErrEmptyPositionStateID = errors.New("position state ID cannot be empty")
	// ErrMeasurementTypeMismatch is returned when measurement type doesn't match position asset class
	ErrMeasurementTypeMismatch = errors.New("measurement type does not match position asset class")
)

// MeasurementType represents the type of physical measurement being recorded.
// These map to the multi-asset types supported by Meridian.
type MeasurementType string

// Valid measurement types aligned with the database constraint
const (
	MeasurementTypeKWh          MeasurementType = "kWh"
	MeasurementTypeGPUHours     MeasurementType = "GPU-Hours"
	MeasurementTypeCPUHours     MeasurementType = "CPU-Hours"
	MeasurementTypeStorageGB    MeasurementType = "Storage-GB"
	MeasurementTypeBandwidthGB  MeasurementType = "Bandwidth-GB"
	MeasurementTypeCarbonTonnes MeasurementType = "Carbon-Tonnes"
	MeasurementTypeWaterLitres  MeasurementType = "Water-Litres" //nolint:misspell // British spelling matches database constraint
	MeasurementTypeCustom       MeasurementType = "Custom"
)

// String returns the string representation of the measurement type.
func (mt MeasurementType) String() string {
	return string(mt)
}

// IsValid checks if the measurement type is a recognized type.
func (mt MeasurementType) IsValid() bool {
	switch mt {
	case MeasurementTypeKWh,
		MeasurementTypeGPUHours,
		MeasurementTypeCPUHours,
		MeasurementTypeStorageGB,
		MeasurementTypeBandwidthGB,
		MeasurementTypeCarbonTonnes,
		MeasurementTypeWaterLitres,
		MeasurementTypeCustom:
		return true
	default:
		return false
	}
}

// ParseMeasurementType parses a string into a MeasurementType.
// Returns the Custom type for unrecognized values.
func ParseMeasurementType(s string) MeasurementType {
	mt := MeasurementType(s)
	if mt.IsValid() {
		return mt
	}
	return MeasurementTypeCustom
}

// Measurement represents a physical measurement recorded against a position state.
// This is used for multi-asset tracking (energy, compute, carbon credits, etc.).
type Measurement struct {
	ID                     uuid.UUID
	FinancialPositionLogID uuid.UUID
	MeasurementType        MeasurementType
	Value                  decimal.Decimal
	Unit                   string
	Timestamp              time.Time
	Metadata               map[string]string
	// BucketID is the fungibility bucket key computed from measurement attributes.
	// Used for position aggregation - measurements with the same bucket_id can be
	// aggregated together. Empty string if no bucket key expression is defined
	// for the instrument.
	BucketID  string
	CreatedAt time.Time
	CreatedBy string
	UpdatedAt time.Time
	UpdatedBy string
}

// NewMeasurement creates a new Measurement with validation.
// Returns an error if:
//   - measurementType is not a valid type
//   - value is negative
//   - unit is empty
//   - timestamp is in the future
//   - positionLogID is the nil UUID
//
// The bucketID parameter is optional (can be empty string) - it represents
// the fungibility bucket computed from measurement attributes via CEL expression.
func NewMeasurement(
	positionLogID uuid.UUID,
	measurementType MeasurementType,
	value decimal.Decimal,
	unit string,
	timestamp time.Time,
	metadata map[string]string,
	bucketID string,
	createdBy string,
) (*Measurement, error) {
	// Validate position log ID
	if positionLogID == uuid.Nil {
		return nil, ErrEmptyPositionStateID
	}

	// Validate measurement type
	if !measurementType.IsValid() {
		return nil, ErrInvalidMeasurementType
	}

	// Validate value is positive
	if value.IsNegative() {
		return nil, ErrNegativeMeasurementValue
	}

	// Validate unit
	if unit == "" {
		return nil, ErrInvalidUnit
	}

	// Validate timestamp is not in the future (with 1-minute tolerance for clock skew)
	if timestamp.After(time.Now().UTC().Add(time.Minute)) {
		return nil, ErrFutureTimestamp
	}

	now := time.Now().UTC()
	return &Measurement{
		ID:                     uuid.New(),
		FinancialPositionLogID: positionLogID,
		MeasurementType:        measurementType,
		Value:                  value,
		Unit:                   unit,
		Timestamp:              timestamp,
		Metadata:               metadata,
		BucketID:               bucketID,
		CreatedAt:              now,
		CreatedBy:              createdBy,
		UpdatedAt:              now,
		UpdatedBy:              createdBy,
	}, nil
}
