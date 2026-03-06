package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a valid MarketPriceObservation for testing.
func createValidObservation(t *testing.T) MarketPriceObservation {
	t.Helper()
	sourceID := uuid.New()
	causationID := uuid.New()
	now := time.Now()
	obs, err := NewMarketPriceObservation(
		"LBMA_GOLD_PRICE",
		sourceID,
		"2024-01-15-GOLD-XAU",
		decimal.NewFromFloat(2025.50),
		"USD/oz",
		now,
		now,
		now.Add(24*time.Hour),
		causationID,
		QualityLevelActual,
		85,
		ObservationContext{},
	)
	require.NoError(t, err)
	return obs
}

func TestNewMarketPriceObservation_Success(t *testing.T) {
	sourceID := uuid.New()
	causationID := uuid.New()
	observedAt := time.Now()
	validFrom := observedAt
	validTo := observedAt.Add(24 * time.Hour)
	value := decimal.NewFromFloat(2025.50)

	beforeCreation := time.Now()

	obs, err := NewMarketPriceObservation(
		"LBMA_GOLD_PRICE",
		sourceID,
		"2024-01-15-GOLD-XAU",
		value,
		"USD/oz",
		observedAt,
		validFrom,
		validTo,
		causationID,
		QualityLevelActual,
		85,
		ObservationContext{},
	)

	require.NoError(t, err)

	// Verify ID is generated
	assert.NotEqual(t, uuid.Nil, obs.ID())

	// Verify all fields are set correctly
	assert.Equal(t, "LBMA_GOLD_PRICE", obs.DataSetCode())
	assert.Equal(t, sourceID, obs.SourceID())
	assert.Equal(t, "2024-01-15-GOLD-XAU", obs.ResolutionKey())
	assert.True(t, value.Equal(obs.Value()))
	assert.Equal(t, "USD/oz", obs.Unit())
	assert.Equal(t, observedAt, obs.ObservedAt())
	assert.Equal(t, validFrom, obs.ValidFrom())
	assert.Equal(t, validTo, obs.ValidTo())
	assert.Equal(t, causationID, obs.CausationID())
	assert.Equal(t, QualityLevelActual, obs.QualityLevel())
	assert.Equal(t, 85, obs.TrustLevel())

	// Verify timestamps
	assert.True(t, obs.CreatedAt().After(beforeCreation) || obs.CreatedAt().Equal(beforeCreation))
	assert.Nil(t, obs.SupersededAt())
	assert.Nil(t, obs.SupersededBy())
}

func TestNewMarketPriceObservation_AllQualityLevels(t *testing.T) {
	qualityLevels := []QualityLevel{
		QualityLevelEstimate,
		QualityLevelActual,
		QualityLevelVerified,
	}

	for _, level := range qualityLevels {
		t.Run(level.String(), func(t *testing.T) {
			obs, err := NewMarketPriceObservation(
				"TEST_DATASET",
				uuid.New(),
				"test-key",
				decimal.NewFromInt(100),
				"USD",
				time.Now(),
				time.Now(),
				time.Now().Add(time.Hour),
				uuid.New(),
				level,
				50,
				ObservationContext{},
			)

			require.NoError(t, err)
			assert.Equal(t, level, obs.QualityLevel())
		})
	}
}

func TestNewMarketPriceObservation_TrustLevelBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		trustLevel int
		expectErr  bool
	}{
		{"trust level 0", 0, false},
		{"trust level 50", 50, false},
		{"trust level 100", 100, false},
		{"trust level -1 invalid", -1, true},
		{"trust level 101 invalid", 101, true},
		{"trust level -100 invalid", -100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs, err := NewMarketPriceObservation(
				"TEST",
				uuid.New(),
				"key",
				decimal.NewFromInt(100),
				"USD",
				time.Now(),
				time.Now(),
				time.Now().Add(time.Hour),
				uuid.New(),
				QualityLevelActual,
				tt.trustLevel,
				ObservationContext{},
			)

			if tt.expectErr {
				assert.ErrorIs(t, err, ErrInvalidTrustLevel)
				assert.Equal(t, MarketPriceObservation{}, obs)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.trustLevel, obs.TrustLevel())
			}
		})
	}
}

func TestNewMarketPriceObservation_ValidationErrors(t *testing.T) {
	validSourceID := uuid.New()
	validCausationID := uuid.New()
	now := time.Now()
	validFrom := now
	validTo := now.Add(time.Hour)

	tests := []struct {
		name          string
		dataSetCode   string
		sourceID      uuid.UUID
		resolutionKey string
		value         decimal.Decimal
		unit          string
		observedAt    time.Time
		validFrom     time.Time
		validTo       time.Time
		causationID   uuid.UUID
		qualityLevel  QualityLevel
		trustLevel    int
		expectedErr   error
	}{
		{
			name:          "empty dataset code",
			dataSetCode:   "",
			sourceID:      validSourceID,
			resolutionKey: "key",
			value:         decimal.NewFromInt(100),
			unit:          "USD",
			observedAt:    now,
			validFrom:     validFrom,
			validTo:       validTo,
			causationID:   validCausationID,
			qualityLevel:  QualityLevelActual,
			trustLevel:    50,
			expectedErr:   ErrDataSetCodeRequired,
		},
		{
			name:          "nil source ID",
			dataSetCode:   "TEST",
			sourceID:      uuid.Nil,
			resolutionKey: "key",
			value:         decimal.NewFromInt(100),
			unit:          "USD",
			observedAt:    now,
			validFrom:     validFrom,
			validTo:       validTo,
			causationID:   validCausationID,
			qualityLevel:  QualityLevelActual,
			trustLevel:    50,
			expectedErr:   ErrSourceIDRequired,
		},
		{
			name:          "empty resolution key",
			dataSetCode:   "TEST",
			sourceID:      validSourceID,
			resolutionKey: "",
			value:         decimal.NewFromInt(100),
			unit:          "USD",
			observedAt:    now,
			validFrom:     validFrom,
			validTo:       validTo,
			causationID:   validCausationID,
			qualityLevel:  QualityLevelActual,
			trustLevel:    50,
			expectedErr:   ErrResolutionKeyRequired,
		},
		{
			name:          "empty unit",
			dataSetCode:   "TEST",
			sourceID:      validSourceID,
			resolutionKey: "key",
			value:         decimal.NewFromInt(100),
			unit:          "",
			observedAt:    now,
			validFrom:     validFrom,
			validTo:       validTo,
			causationID:   validCausationID,
			qualityLevel:  QualityLevelActual,
			trustLevel:    50,
			expectedErr:   ErrUnitRequired,
		},
		{
			name:          "nil causation ID",
			dataSetCode:   "TEST",
			sourceID:      validSourceID,
			resolutionKey: "key",
			value:         decimal.NewFromInt(100),
			unit:          "USD",
			observedAt:    now,
			validFrom:     validFrom,
			validTo:       validTo,
			causationID:   uuid.Nil,
			qualityLevel:  QualityLevelActual,
			trustLevel:    50,
			expectedErr:   ErrCausationIDRequired,
		},
		{
			name:          "invalid quality level",
			dataSetCode:   "TEST",
			sourceID:      validSourceID,
			resolutionKey: "key",
			value:         decimal.NewFromInt(100),
			unit:          "USD",
			observedAt:    now,
			validFrom:     validFrom,
			validTo:       validTo,
			causationID:   validCausationID,
			qualityLevel:  QualityLevel(99),
			trustLevel:    50,
			expectedErr:   ErrInvalidQualityLevel,
		},
		{
			name:          "trust level too high",
			dataSetCode:   "TEST",
			sourceID:      validSourceID,
			resolutionKey: "key",
			value:         decimal.NewFromInt(100),
			unit:          "USD",
			observedAt:    now,
			validFrom:     validFrom,
			validTo:       validTo,
			causationID:   validCausationID,
			qualityLevel:  QualityLevelActual,
			trustLevel:    101,
			expectedErr:   ErrInvalidTrustLevel,
		},
		{
			name:          "trust level negative",
			dataSetCode:   "TEST",
			sourceID:      validSourceID,
			resolutionKey: "key",
			value:         decimal.NewFromInt(100),
			unit:          "USD",
			observedAt:    now,
			validFrom:     validFrom,
			validTo:       validTo,
			causationID:   validCausationID,
			qualityLevel:  QualityLevelActual,
			trustLevel:    -1,
			expectedErr:   ErrInvalidTrustLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewMarketPriceObservation(
				tt.dataSetCode,
				tt.sourceID,
				tt.resolutionKey,
				tt.value,
				tt.unit,
				tt.observedAt,
				tt.validFrom,
				tt.validTo,
				tt.causationID,
				tt.qualityLevel,
				tt.trustLevel,
				ObservationContext{},
			)
			assert.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestNewMarketPriceObservation_TemporalBoundsValidation(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		validFrom time.Time
		validTo   time.Time
		expectErr bool
	}{
		{
			name:      "valid: validFrom before validTo",
			validFrom: now,
			validTo:   now.Add(time.Hour),
			expectErr: false,
		},
		{
			name:      "valid: 1 nanosecond difference",
			validFrom: now,
			validTo:   now.Add(time.Nanosecond),
			expectErr: false,
		},
		{
			name:      "invalid: validFrom equals validTo",
			validFrom: now,
			validTo:   now,
			expectErr: true,
		},
		{
			name:      "invalid: validFrom after validTo",
			validFrom: now.Add(time.Hour),
			validTo:   now,
			expectErr: true,
		},
		{
			name:      "invalid: validFrom 1 second after validTo",
			validFrom: now.Add(time.Second),
			validTo:   now,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs, err := NewMarketPriceObservation(
				"TEST",
				uuid.New(),
				"key",
				decimal.NewFromInt(100),
				"USD",
				now,
				tt.validFrom,
				tt.validTo,
				uuid.New(),
				QualityLevelActual,
				50,
				ObservationContext{},
			)

			if tt.expectErr {
				assert.ErrorIs(t, err, ErrInvalidTemporalBounds)
				assert.Equal(t, MarketPriceObservation{}, obs)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.validFrom, obs.ValidFrom())
				assert.Equal(t, tt.validTo, obs.ValidTo())
			}
		})
	}
}

func TestMarketPriceObservation_Supersede_Success(t *testing.T) {
	obs := createValidObservation(t)
	assert.False(t, obs.IsSuperseded())
	assert.Nil(t, obs.SupersededAt())
	assert.Nil(t, obs.SupersededBy())

	newObservationID := uuid.New()
	beforeSupersede := time.Now()

	superseded, err := obs.Supersede(newObservationID)

	require.NoError(t, err)
	assert.True(t, superseded.IsSuperseded())
	assert.NotNil(t, superseded.SupersededAt())
	assert.True(t, superseded.SupersededAt().After(beforeSupersede) || superseded.SupersededAt().Equal(beforeSupersede))
	assert.NotNil(t, superseded.SupersededBy())
	assert.Equal(t, newObservationID, *superseded.SupersededBy())

	// Original should be unchanged (immutability)
	assert.False(t, obs.IsSuperseded())
	assert.Nil(t, obs.SupersededAt())
	assert.Nil(t, obs.SupersededBy())
}

func TestMarketPriceObservation_Supersede_AlreadySuperseded_Fails(t *testing.T) {
	obs := createValidObservation(t)
	firstSupersederID := uuid.New()

	superseded, err := obs.Supersede(firstSupersederID)
	require.NoError(t, err)

	// Try to supersede again
	secondSupersederID := uuid.New()
	_, err = superseded.Supersede(secondSupersederID)

	assert.ErrorIs(t, err, ErrObservationAlreadySuperseded)
}

func TestMarketPriceObservation_Supersede_NilUUID_Fails(t *testing.T) {
	obs := createValidObservation(t)

	_, err := obs.Supersede(uuid.Nil)

	assert.ErrorIs(t, err, ErrInvalidSupersedeTarget)
	assert.False(t, obs.IsSuperseded()) // Original unchanged
}

func TestMarketPriceObservation_Supersede_SelfReference_Fails(t *testing.T) {
	obs := createValidObservation(t)

	// Try to supersede with own ID
	_, err := obs.Supersede(obs.ID())

	assert.ErrorIs(t, err, ErrInvalidSupersedeTarget)
	assert.False(t, obs.IsSuperseded()) // Original unchanged
}

func TestMarketPriceObservation_IsSuperseded(t *testing.T) {
	t.Run("new observation is not superseded", func(t *testing.T) {
		obs := createValidObservation(t)
		assert.False(t, obs.IsSuperseded())
	})

	t.Run("superseded observation returns true", func(t *testing.T) {
		obs := createValidObservation(t)
		superseded, err := obs.Supersede(uuid.New())
		require.NoError(t, err)
		assert.True(t, superseded.IsSuperseded())
	})

	t.Run("builder with only supersededBy set is considered superseded", func(t *testing.T) {
		// This tests the edge case where builder reconstruction sets only supersededBy
		// (e.g., from legacy data or incomplete persistence)
		supersededByID := uuid.New()
		obs := NewMarketPriceObservationBuilder().
			WithID(uuid.New()).
			WithDataSetCode("TEST").
			WithSourceID(uuid.New()).
			WithResolutionKey("key").
			WithValue(decimal.NewFromInt(100)).
			WithUnit("USD").
			WithObservedAt(time.Now()).
			WithValidFrom(time.Now()).
			WithValidTo(time.Now().Add(time.Hour)).
			WithCreatedAt(time.Now()).
			WithCausationID(uuid.New()).
			WithQualityLevel(QualityLevelActual).
			WithTrustLevel(50).
			WithSupersededBy(&supersededByID). // Only set supersededBy, not supersededAt
			Build()

		assert.True(t, obs.IsSuperseded())
	})
}

func TestMarketPriceObservation_QualityLevelOrdering(t *testing.T) {
	// Verify ESTIMATE < ACTUAL < VERIFIED ordering in observation context
	assert.Less(t, int(QualityLevelEstimate), int(QualityLevelActual))
	assert.Less(t, int(QualityLevelActual), int(QualityLevelVerified))
	assert.Less(t, int(QualityLevelEstimate), int(QualityLevelVerified))

	// Verify Supersedes method works correctly for observation quality comparison
	assert.True(t, QualityLevelActual.Supersedes(QualityLevelEstimate))
	assert.True(t, QualityLevelVerified.Supersedes(QualityLevelActual))
	assert.True(t, QualityLevelVerified.Supersedes(QualityLevelEstimate))

	assert.False(t, QualityLevelEstimate.Supersedes(QualityLevelActual))
	assert.False(t, QualityLevelActual.Supersedes(QualityLevelVerified))
	assert.False(t, QualityLevelEstimate.Supersedes(QualityLevelVerified))

	// Same level does not supersede
	assert.False(t, QualityLevelActual.Supersedes(QualityLevelActual))
}

func TestMarketPriceObservation_DecimalPrecision(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{
			name:     "simple integer",
			value:    "100",
			expected: "100",
		},
		{
			name:     "two decimal places",
			value:    "2025.50",
			expected: "2025.5",
		},
		{
			name:     "many decimal places",
			value:    "1234.567890123456789",
			expected: "1234.567890123456789",
		},
		{
			name:     "very small number",
			value:    "0.000000000001",
			expected: "0.000000000001",
		},
		{
			name:     "very large number",
			value:    "999999999999999999.999999999999",
			expected: "999999999999999999.999999999999",
		},
		{
			name:     "negative value",
			value:    "-1234.56",
			expected: "-1234.56",
		},
		{
			name:     "zero",
			value:    "0",
			expected: "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := decimal.NewFromString(tt.value)
			require.NoError(t, err)

			obs, err := NewMarketPriceObservation(
				"TEST",
				uuid.New(),
				"key",
				value,
				"USD",
				time.Now(),
				time.Now(),
				time.Now().Add(time.Hour),
				uuid.New(),
				QualityLevelActual,
				50,
				ObservationContext{},
			)

			require.NoError(t, err)

			expected, err := decimal.NewFromString(tt.expected)
			require.NoError(t, err)
			assert.True(t, expected.Equal(obs.Value()),
				"expected %s but got %s", expected.String(), obs.Value().String())
		})
	}
}

func TestMarketPriceObservation_Immutability(t *testing.T) {
	obs := createValidObservation(t)
	originalID := obs.ID()
	originalValue := obs.Value()
	originalQuality := obs.QualityLevel()

	// Supersede the observation
	superseded, _ := obs.Supersede(uuid.New())

	// Original should be completely unchanged
	assert.Equal(t, originalID, obs.ID())
	assert.True(t, originalValue.Equal(obs.Value()))
	assert.Equal(t, originalQuality, obs.QualityLevel())
	assert.False(t, obs.IsSuperseded())
	assert.Nil(t, obs.SupersededAt())
	assert.Nil(t, obs.SupersededBy())

	// Superseded version should have same core data but be marked superseded
	assert.Equal(t, originalID, superseded.ID())
	assert.True(t, originalValue.Equal(superseded.Value()))
	assert.Equal(t, originalQuality, superseded.QualityLevel())
	assert.True(t, superseded.IsSuperseded())
}

func TestMarketPriceObservation_UniqueIDs(t *testing.T) {
	ids := make(map[uuid.UUID]bool)

	for range 100 {
		obs, err := NewMarketPriceObservation(
			"TEST",
			uuid.New(),
			"key",
			decimal.NewFromInt(100),
			"USD",
			time.Now(),
			time.Now(),
			time.Now().Add(time.Hour),
			uuid.New(),
			QualityLevelActual,
			50,
			ObservationContext{},
		)
		require.NoError(t, err)
		assert.False(t, ids[obs.ID()], "Duplicate ID generated")
		ids[obs.ID()] = true
	}
}

func TestMarketPriceObservationBuilder_Reconstruction(t *testing.T) {
	id := uuid.New()
	sourceID := uuid.New()
	causationID := uuid.New()
	supersededBy := uuid.New()
	now := time.Now()
	supersededAt := now.Add(-time.Hour)
	value := decimal.NewFromFloat(2025.50)

	obs := NewMarketPriceObservationBuilder().
		WithID(id).
		WithDataSetCode("LBMA_GOLD_PRICE").
		WithSourceID(sourceID).
		WithResolutionKey("2024-01-15-GOLD-XAU").
		WithValue(value).
		WithUnit("USD/oz").
		WithObservedAt(now.Add(-2 * time.Hour)).
		WithValidFrom(now.Add(-3 * time.Hour)).
		WithValidTo(now.Add(-time.Hour)).
		WithCreatedAt(now.Add(-2 * time.Hour)).
		WithSupersededAt(&supersededAt).
		WithSupersededBy(&supersededBy).
		WithCausationID(causationID).
		WithQualityLevel(QualityLevelVerified).
		WithTrustLevel(95).
		Build()

	assert.Equal(t, id, obs.ID())
	assert.Equal(t, "LBMA_GOLD_PRICE", obs.DataSetCode())
	assert.Equal(t, sourceID, obs.SourceID())
	assert.Equal(t, "2024-01-15-GOLD-XAU", obs.ResolutionKey())
	assert.True(t, value.Equal(obs.Value()))
	assert.Equal(t, "USD/oz", obs.Unit())
	assert.Equal(t, causationID, obs.CausationID())
	assert.Equal(t, QualityLevelVerified, obs.QualityLevel())
	assert.Equal(t, 95, obs.TrustLevel())
	assert.NotNil(t, obs.SupersededAt())
	assert.Equal(t, supersededAt, *obs.SupersededAt())
	assert.NotNil(t, obs.SupersededBy())
	assert.Equal(t, supersededBy, *obs.SupersededBy())
	assert.True(t, obs.IsSuperseded())
}

func TestMarketPriceObservationBuilder_PartialReconstruction(t *testing.T) {
	id := uuid.New()

	obs := NewMarketPriceObservationBuilder().
		WithID(id).
		WithDataSetCode("TEST").
		WithQualityLevel(QualityLevelEstimate).
		Build()

	assert.Equal(t, id, obs.ID())
	assert.Equal(t, "TEST", obs.DataSetCode())
	assert.Equal(t, QualityLevelEstimate, obs.QualityLevel())
	assert.Equal(t, uuid.Nil, obs.SourceID())
	assert.Empty(t, obs.ResolutionKey())
	assert.Nil(t, obs.SupersededAt())
	assert.Nil(t, obs.SupersededBy())
	assert.False(t, obs.IsSuperseded())
}

func TestMarketPriceObservation_BiTemporalFields(t *testing.T) {
	// Test that all bi-temporal fields are correctly captured
	observedAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	validFrom := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC)

	obs, err := NewMarketPriceObservation(
		"LBMA_GOLD_PRICE",
		uuid.New(),
		"key",
		decimal.NewFromInt(2025),
		"USD/oz",
		observedAt,
		validFrom,
		validTo,
		uuid.New(),
		QualityLevelActual,
		85,
		ObservationContext{},
	)

	require.NoError(t, err)

	// Valid time (when the observation is effective)
	assert.Equal(t, validFrom, obs.ValidFrom())
	assert.Equal(t, validTo, obs.ValidTo())

	// Observation time (when measurement occurred)
	assert.Equal(t, observedAt, obs.ObservedAt())

	// Knowledge time (when we learned about it)
	assert.True(t, obs.CreatedAt().After(observedAt))

	// Initially not superseded
	assert.Nil(t, obs.SupersededAt())
	assert.Nil(t, obs.SupersededBy())
}

func TestMarketPriceObservation_LineageTracking(t *testing.T) {
	// Create an observation
	obs1, err := NewMarketPriceObservation(
		"TEST",
		uuid.New(),
		"key",
		decimal.NewFromInt(100),
		"USD",
		time.Now(),
		time.Now(),
		time.Now().Add(time.Hour),
		uuid.New(),
		QualityLevelEstimate,
		50,
		ObservationContext{},
	)
	require.NoError(t, err)

	// Create a second observation that supersedes the first
	obs2, err := NewMarketPriceObservation(
		"TEST",
		uuid.New(),
		"key",
		decimal.NewFromInt(105),
		"USD",
		time.Now(),
		time.Now(),
		time.Now().Add(time.Hour),
		uuid.New(),
		QualityLevelActual, // Higher quality
		75,
		ObservationContext{},
	)
	require.NoError(t, err)

	// Mark obs1 as superseded by obs2
	supersededObs1, err := obs1.Supersede(obs2.ID())
	require.NoError(t, err)

	// Verify the lineage chain
	assert.NotNil(t, supersededObs1.SupersededBy())
	assert.Equal(t, obs2.ID(), *supersededObs1.SupersededBy())
	assert.True(t, supersededObs1.IsSuperseded())
	assert.False(t, obs2.IsSuperseded()) // The newer observation is not superseded
}

func TestMarketPriceObservation_ZeroValue(t *testing.T) {
	// Verify that zero/negative values are allowed (no validation constraint)
	zeroValue := decimal.NewFromInt(0)
	negValue := decimal.NewFromFloat(-100.50)

	t.Run("zero value is allowed", func(t *testing.T) {
		obs, err := NewMarketPriceObservation(
			"TEST",
			uuid.New(),
			"key",
			zeroValue,
			"USD",
			time.Now(),
			time.Now(),
			time.Now().Add(time.Hour),
			uuid.New(),
			QualityLevelActual,
			50,
			ObservationContext{},
		)
		require.NoError(t, err)
		assert.True(t, zeroValue.Equal(obs.Value()))
	})

	t.Run("negative value is allowed", func(t *testing.T) {
		obs, err := NewMarketPriceObservation(
			"TEST",
			uuid.New(),
			"key",
			negValue,
			"USD",
			time.Now(),
			time.Now(),
			time.Now().Add(time.Hour),
			uuid.New(),
			QualityLevelActual,
			50,
			ObservationContext{},
		)
		require.NoError(t, err)
		assert.True(t, negValue.Equal(obs.Value()))
	})
}
