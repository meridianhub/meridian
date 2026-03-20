package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMeasurementType_String(t *testing.T) {
	assert.Equal(t, "kWh", domain.MeasurementTypeKWh.String())
	assert.Equal(t, "GPU-Hours", domain.MeasurementTypeGPUHours.String())
	assert.Equal(t, "Custom", domain.MeasurementTypeCustom.String())
}

func TestMeasurementType_IsValid(t *testing.T) {
	validTypes := []domain.MeasurementType{
		domain.MeasurementTypeKWh,
		domain.MeasurementTypeGPUHours,
		domain.MeasurementTypeCPUHours,
		domain.MeasurementTypeStorageGB,
		domain.MeasurementTypeBandwidthGB,
		domain.MeasurementTypeCarbonTonnes,
		domain.MeasurementTypeWaterLitres,
		domain.MeasurementTypeCustom,
	}
	for _, mt := range validTypes {
		assert.True(t, mt.IsValid(), "expected %q to be valid", mt)
	}

	assert.False(t, domain.MeasurementType("invalid").IsValid())
	assert.False(t, domain.MeasurementType("").IsValid())
}

func TestParseMeasurementType(t *testing.T) {
	assert.Equal(t, domain.MeasurementTypeKWh, domain.ParseMeasurementType("kWh"))
	assert.Equal(t, domain.MeasurementTypeGPUHours, domain.ParseMeasurementType("GPU-Hours"))
	assert.Equal(t, domain.MeasurementTypeCustom, domain.ParseMeasurementType("unknown-type"))
	assert.Equal(t, domain.MeasurementTypeCustom, domain.ParseMeasurementType(""))
}

func TestNewMeasurement(t *testing.T) {
	validLogID := uuid.New()
	pastTime := time.Now().UTC().Add(-1 * time.Hour)

	t.Run("valid measurement", func(t *testing.T) {
		m, err := domain.NewMeasurement(
			validLogID,
			domain.MeasurementTypeKWh,
			decimal.NewFromFloat(42.5),
			"kWh",
			pastTime,
			map[string]string{"source": "meter"},
			"bucket-1",
			"test-user",
		)
		require.NoError(t, err)
		require.NotNil(t, m)

		assert.Equal(t, validLogID, m.FinancialPositionLogID)
		assert.Equal(t, domain.MeasurementTypeKWh, m.MeasurementType)
		assert.True(t, decimal.NewFromFloat(42.5).Equal(m.Value))
		assert.Equal(t, "kWh", m.Unit)
		assert.Equal(t, pastTime, m.Timestamp)
		assert.Equal(t, "meter", m.Metadata["source"])
		assert.Equal(t, "bucket-1", m.BucketID)
		assert.Equal(t, "test-user", m.CreatedBy)
		assert.Equal(t, "test-user", m.UpdatedBy)
		assert.NotEqual(t, uuid.Nil, m.ID)
		assert.False(t, m.CreatedAt.IsZero())
	})

	t.Run("zero value is valid", func(t *testing.T) {
		m, err := domain.NewMeasurement(
			validLogID,
			domain.MeasurementTypeKWh,
			decimal.Zero,
			"kWh",
			pastTime,
			nil,
			"",
			"system",
		)
		require.NoError(t, err)
		assert.True(t, m.Value.IsZero())
		assert.Empty(t, m.BucketID)
	})

	t.Run("nil position log ID returns error", func(t *testing.T) {
		_, err := domain.NewMeasurement(
			uuid.Nil,
			domain.MeasurementTypeKWh,
			decimal.NewFromFloat(1.0),
			"kWh",
			pastTime,
			nil,
			"",
			"user",
		)
		assert.ErrorIs(t, err, domain.ErrEmptyPositionStateID)
	})

	t.Run("invalid measurement type returns error", func(t *testing.T) {
		_, err := domain.NewMeasurement(
			validLogID,
			domain.MeasurementType("bogus"),
			decimal.NewFromFloat(1.0),
			"kWh",
			pastTime,
			nil,
			"",
			"user",
		)
		assert.ErrorIs(t, err, domain.ErrInvalidMeasurementType)
	})

	t.Run("negative value returns error", func(t *testing.T) {
		_, err := domain.NewMeasurement(
			validLogID,
			domain.MeasurementTypeKWh,
			decimal.NewFromFloat(-1.0),
			"kWh",
			pastTime,
			nil,
			"",
			"user",
		)
		assert.ErrorIs(t, err, domain.ErrNegativeMeasurementValue)
	})

	t.Run("empty unit returns error", func(t *testing.T) {
		_, err := domain.NewMeasurement(
			validLogID,
			domain.MeasurementTypeKWh,
			decimal.NewFromFloat(1.0),
			"",
			pastTime,
			nil,
			"",
			"user",
		)
		assert.ErrorIs(t, err, domain.ErrInvalidUnit)
	})

	t.Run("future timestamp returns error", func(t *testing.T) {
		futureTime := time.Now().UTC().Add(10 * time.Minute)
		_, err := domain.NewMeasurement(
			validLogID,
			domain.MeasurementTypeKWh,
			decimal.NewFromFloat(1.0),
			"kWh",
			futureTime,
			nil,
			"",
			"user",
		)
		assert.ErrorIs(t, err, domain.ErrFutureTimestamp)
	})
}
