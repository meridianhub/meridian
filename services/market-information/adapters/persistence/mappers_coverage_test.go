package persistence

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestObservationToEntity(t *testing.T) {
	now := time.Now()
	sourceID := uuid.New()
	causationID := uuid.New()
	dataSetDefID := uuid.New()

	obs, err := domain.NewMarketPriceObservation(
		"TEST_DATASET",
		sourceID,
		"EUR/USD",
		decimal.NewFromFloat(1.1234),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		causationID,
		domain.QualityLevelActual,
		80,
		domain.NewObservationContext(map[string]string{"base_code": "EUR"}),
	)
	assert.NoError(t, err)

	entity := ObservationToEntity(obs, dataSetDefID)

	assert.Equal(t, obs.ID(), entity.ID)
	assert.Equal(t, dataSetDefID, entity.DataSetDefinitionID)
	assert.Equal(t, sourceID, entity.DataSourceID)
	assert.Equal(t, "EUR/USD", entity.ResolutionKey)
	assert.True(t, entity.NumericValue.Valid)
	assert.True(t, entity.NumericValue.Decimal.Equal(decimal.NewFromFloat(1.1234)))
	assert.True(t, entity.ValidFrom.Valid)
	assert.True(t, entity.ValidTo.Valid)
	assert.True(t, entity.CausationID.Valid)
	assert.Equal(t, causationID, entity.CausationID.UUID)
	assert.False(t, entity.SupersededBy.Valid) // Not superseded
	assert.Equal(t, domain.QualityLevelActual.Int(), entity.Quality)
}

func TestObservationToEntity_WithSupersededBy(t *testing.T) {
	now := time.Now()
	supersededByID := uuid.New()

	obs := domain.NewMarketPriceObservationBuilder().
		WithID(uuid.New()).
		WithDataSetCode("DS").
		WithSourceID(uuid.New()).
		WithResolutionKey("key").
		WithValue(decimal.NewFromFloat(1.0)).
		WithObservedAt(now).
		WithValidFrom(now).
		WithValidTo(now.Add(time.Hour)).
		WithCreatedAt(now).
		WithCausationID(uuid.New()).
		WithQualityLevel(domain.QualityLevelEstimate).
		WithTrustLevel(50).
		WithSupersededBy(&supersededByID).
		Build()

	entity := ObservationToEntity(obs, uuid.New())
	assert.True(t, entity.SupersededBy.Valid)
	assert.Equal(t, supersededByID, entity.SupersededBy.UUID)
}

func TestObservationToEntity_ZeroValidTimes(t *testing.T) {
	// Build observation with zero time values for ValidFrom/ValidTo
	obs := domain.NewMarketPriceObservationBuilder().
		WithID(uuid.New()).
		WithDataSetCode("DS").
		WithSourceID(uuid.New()).
		WithResolutionKey("key").
		WithValue(decimal.NewFromFloat(1.0)).
		WithObservedAt(time.Now()).
		WithCreatedAt(time.Now()).
		WithCausationID(uuid.Nil). // zero causation
		WithQualityLevel(domain.QualityLevelEstimate).
		Build()

	entity := ObservationToEntity(obs, uuid.New())
	assert.False(t, entity.ValidFrom.Valid)
	assert.False(t, entity.ValidTo.Valid)
	assert.False(t, entity.CausationID.Valid) // Nil UUID should be invalid
}

func TestEntityToObservation(t *testing.T) {
	now := time.Now()
	obsID := uuid.New()
	sourceID := uuid.New()
	supersededByID := uuid.New()
	causationID := uuid.New()

	entity := MarketPriceObservationEntity{
		ID:                  obsID,
		DataSetDefinitionID: uuid.New(),
		DataSourceID:        sourceID,
		ResolutionKey:       "GBP/USD",
		ObservedAt:          now,
		ValidFrom:           sql.NullTime{Time: now, Valid: true},
		ValidTo:             sql.NullTime{Time: now.Add(24 * time.Hour), Valid: true},
		CreatedAt:           now,
		Quality:             2, // ACTUAL
		NumericValue:        decimal.NullDecimal{Decimal: decimal.NewFromFloat(1.25), Valid: true},
		SupersededBy:        uuid.NullUUID{UUID: supersededByID, Valid: true},
		CausationID:         uuid.NullUUID{UUID: causationID, Valid: true},
		ObservationContext:  []byte(`{"attributes":{"base_code":"GBP"}}`),
	}

	obs := EntityToObservation(entity, "FX_RATES", 85)

	assert.Equal(t, obsID, obs.ID())
	assert.Equal(t, "FX_RATES", obs.DataSetCode())
	assert.Equal(t, sourceID, obs.SourceID())
	assert.Equal(t, "GBP/USD", obs.ResolutionKey())
	assert.True(t, obs.Value().Equal(decimal.NewFromFloat(1.25)))
	assert.Equal(t, domain.QualityLevel(2), obs.QualityLevel())
	assert.Equal(t, 85, obs.TrustLevel())
	assert.NotNil(t, obs.SupersededBy())
	assert.Equal(t, supersededByID, *obs.SupersededBy())
	assert.Equal(t, causationID, obs.CausationID())
	assert.Equal(t, "GBP", obs.ObservationContext().Attributes["base_code"])
}

func TestEntityToObservation_MinimalFields(t *testing.T) {
	now := time.Now()
	entity := MarketPriceObservationEntity{
		ID:                  uuid.New(),
		DataSetDefinitionID: uuid.New(),
		DataSourceID:        uuid.New(),
		ResolutionKey:       "default",
		ObservedAt:          now,
		CreatedAt:           now,
		Quality:             1,
		NumericValue:        decimal.NullDecimal{Valid: false},
		SupersededBy:        uuid.NullUUID{Valid: false},
		CausationID:         uuid.NullUUID{Valid: false},
		ObservationContext:  nil,
	}

	obs := EntityToObservation(entity, "DS", 50)

	assert.Nil(t, obs.SupersededBy())
	assert.Equal(t, uuid.Nil, obs.CausationID())
	assert.True(t, obs.ObservationContext().IsEmpty())
}

func TestParseDataSetStatus_AllBranches(t *testing.T) {
	tests := []struct {
		input    string
		expected domain.DataSetStatus
	}{
		{"DRAFT", domain.DataSetStatusDraft},
		{"ACTIVE", domain.DataSetStatusActive},
		{"DEPRECATED", domain.DataSetStatusDeprecated},
		{"UNKNOWN", domain.DataSetStatusDraft}, // default
		{"", domain.DataSetStatusDraft},        // default
		{"draft", domain.DataSetStatusDraft},   // case-sensitive, falls to default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseDataSetStatus(tt.input))
		})
	}
}

func TestParseAccessLevel_AllBranches(t *testing.T) {
	tests := []struct {
		input    string
		expected domain.DataAccessLevel
	}{
		{"PUBLIC", domain.AccessLevelPublic},
		{"PRIVATE", domain.AccessLevelPrivate},
		{"RESTRICTED", domain.AccessLevelRestricted},
		{"UNKNOWN", domain.AccessLevelPrivate}, // invalid, default to PRIVATE
		{"", domain.AccessLevelPrivate},        // invalid, default to PRIVATE
		{"public", domain.AccessLevelPrivate},  // case-sensitive, invalid
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseAccessLevel(tt.input))
		})
	}
}
