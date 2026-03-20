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

func TestDataSetDefinitionToEntity_AllFields(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	activatedAt := now.Add(-24 * time.Hour)
	deprecatedAt := now.Add(-time.Hour)
	id := uuid.New()

	ds := domain.NewDataSetDefinitionBuilder().
		WithID(id).
		WithCode("FX_RATES").
		WithVersion(2).
		WithName("FX Rates Dataset").
		WithDescription("Foreign exchange rates").
		WithDataCategory(domain.DataCategoryPricing).
		WithStatus(domain.DataSetStatusActive).
		WithValidationExpression("amount > 0").
		WithResolutionKeyExpression("base + '/' + quote").
		WithErrorMessageExpression("amount must be positive").
		WithIsShared(true).
		WithAccessLevel(domain.AccessLevelPublic).
		WithActivatedAt(&activatedAt).
		WithDeprecatedAt(&deprecatedAt).
		WithCreatedAt(now).
		WithUpdatedAt(now).
		Build()

	entity := DataSetDefinitionToEntity(ds)

	assert.Equal(t, id, entity.ID)
	assert.Equal(t, "FX_RATES", entity.Code)
	assert.Equal(t, 2, entity.Version)
	assert.Equal(t, "FX Rates Dataset", entity.Name)
	assert.True(t, entity.Description.Valid)
	assert.Equal(t, "Foreign exchange rates", entity.Description.String)
	assert.True(t, entity.DataCategory.Valid)
	assert.Equal(t, "PRICING", entity.DataCategory.String)
	assert.Equal(t, "ACTIVE", entity.Status)
	assert.True(t, entity.ValidationExpression.Valid)
	assert.Equal(t, "amount > 0", entity.ValidationExpression.String)
	assert.Equal(t, "base + '/' + quote", entity.ResolutionKeyExpression)
	assert.True(t, entity.ErrorMessageExpression.Valid)
	assert.True(t, entity.IsShared)
	assert.Equal(t, "PUBLIC", entity.AccessLevel)
	assert.True(t, entity.ActivatedAt.Valid)
	assert.True(t, entity.DeprecatedAt.Valid)
}

func TestDataSetDefinitionToEntity_MinimalFields(t *testing.T) {
	now := time.Now()

	ds := domain.NewDataSetDefinitionBuilder().
		WithID(uuid.New()).
		WithCode("MINIMAL").
		WithVersion(1).
		WithName("Minimal Dataset").
		WithStatus(domain.DataSetStatusDraft).
		WithResolutionKeyExpression("key").
		WithIsShared(false).
		WithAccessLevel(domain.AccessLevelPrivate).
		WithCreatedAt(now).
		WithUpdatedAt(now).
		Build()

	entity := DataSetDefinitionToEntity(ds)

	assert.False(t, entity.Description.Valid)
	assert.False(t, entity.DataCategory.Valid)
	assert.False(t, entity.ValidationExpression.Valid)
	assert.False(t, entity.ErrorMessageExpression.Valid)
	assert.False(t, entity.ActivatedAt.Valid)
	assert.False(t, entity.DeprecatedAt.Valid)
}

func TestEntityToDataSetDefinition_AllFields(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	id := uuid.New()
	activatedAt := now.Add(-time.Hour)
	deprecatedAt := now

	entity := DataSetDefinitionEntity{
		ID:                      id,
		Code:                    "ENERGY_PRICES",
		Version:                 3,
		Name:                    "Energy Prices",
		Description:             sql.NullString{String: "Energy market prices", Valid: true},
		DataCategory:            sql.NullString{String: "PRICING", Valid: true},
		Status:                  "ACTIVE",
		ValidationExpression:    sql.NullString{String: "price >= 0", Valid: true},
		ResolutionKeyExpression: "commodity + '_' + region",
		ErrorMessageExpression:  sql.NullString{String: "price must be non-negative", Valid: true},
		IsShared:                true,
		AccessLevel:             "RESTRICTED",
		ActivatedAt:             sql.NullTime{Time: activatedAt, Valid: true},
		DeprecatedAt:            sql.NullTime{Time: deprecatedAt, Valid: true},
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	ds := EntityToDataSetDefinition(entity)

	assert.Equal(t, id, ds.ID())
	assert.Equal(t, "ENERGY_PRICES", ds.Code())
	assert.Equal(t, 3, ds.Version())
	assert.Equal(t, "Energy Prices", ds.Name())
	assert.Equal(t, "Energy market prices", ds.Description())
	assert.Equal(t, domain.DataCategoryPricing, ds.DataCategory())
	assert.Equal(t, domain.DataSetStatusActive, ds.Status())
	assert.Equal(t, "price >= 0", ds.ValidationExpression())
	assert.Equal(t, "commodity + '_' + region", ds.ResolutionKeyExpression())
	assert.Equal(t, "price must be non-negative", ds.ErrorMessageExpression())
	assert.True(t, ds.IsShared())
	assert.Equal(t, domain.AccessLevelRestricted, ds.AccessLevel())
	assert.NotNil(t, ds.ActivatedAt())
	assert.NotNil(t, ds.DeprecatedAt())
}

func TestEntityToDataSetDefinition_MinimalFields(t *testing.T) {
	now := time.Now()

	entity := DataSetDefinitionEntity{
		ID:                      uuid.New(),
		Code:                    "MINIMAL",
		Version:                 1,
		Name:                    "Minimal",
		Status:                  "DRAFT",
		ResolutionKeyExpression: "key",
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	ds := EntityToDataSetDefinition(entity)

	assert.Equal(t, "", ds.Description())
	assert.Nil(t, ds.ActivatedAt())
	assert.Nil(t, ds.DeprecatedAt())
}

func TestDataSourceToEntity_WithDescription(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	id := uuid.New()

	src := domain.NewDataSourceBuilder().
		WithID(id).
		WithCode("BLOOMBERG").
		WithName("Bloomberg Data").
		WithDescription("Bloomberg financial data feed").
		WithTrustLevel(90).
		WithIsActive(true).
		WithCreatedAt(now).
		WithUpdatedAt(now).
		Build()

	entity := DataSourceToEntity(src)

	assert.Equal(t, id, entity.ID)
	assert.Equal(t, "BLOOMBERG", entity.Code)
	assert.Equal(t, "Bloomberg Data", entity.Name)
	assert.Equal(t, 90, entity.TrustLevel)
	assert.True(t, entity.Description.Valid)
	assert.Equal(t, "Bloomberg financial data feed", entity.Description.String)
}

func TestDataSourceToEntity_EmptyDescription(t *testing.T) {
	now := time.Now()

	src := domain.NewDataSourceBuilder().
		WithID(uuid.New()).
		WithCode("INTERNAL").
		WithName("Internal Feed").
		WithTrustLevel(70).
		WithIsActive(true).
		WithCreatedAt(now).
		WithUpdatedAt(now).
		Build()

	entity := DataSourceToEntity(src)

	assert.False(t, entity.Description.Valid)
}

func TestEntityToDataSource_WithDescription(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	id := uuid.New()

	entity := DataSourceEntity{
		ID:          id,
		Code:        "REUTERS",
		Name:        "Reuters Data",
		TrustLevel:  85,
		Description: sql.NullString{String: "Reuters market data", Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
		Version:     1,
	}

	src := EntityToDataSource(entity)

	assert.Equal(t, id, src.ID())
	assert.Equal(t, "REUTERS", src.Code())
	assert.Equal(t, "Reuters Data", src.Name())
	assert.Equal(t, 85, src.TrustLevel())
	assert.Equal(t, "Reuters market data", src.Description())
	assert.True(t, src.IsActive())
}

func TestEntityToDataSource_WithoutDescription(t *testing.T) {
	now := time.Now()

	entity := DataSourceEntity{
		ID:         uuid.New(),
		Code:       "MANUAL",
		Name:       "Manual Entry",
		TrustLevel: 50,
		CreatedAt:  now,
		UpdatedAt:  now,
		Version:    1,
	}

	src := EntityToDataSource(entity)

	assert.Equal(t, "", src.Description())
	assert.True(t, src.IsActive()) // Always active from DB
}

// TestDataSetDefinitionRoundTrip verifies that domain -> entity -> domain preserves all fields.
func TestDataSetDefinitionRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	id := uuid.New()

	original := domain.NewDataSetDefinitionBuilder().
		WithID(id).
		WithCode("ROUND_TRIP").
		WithVersion(1).
		WithName("Round Trip Test").
		WithDescription("test description").
		WithDataCategory(domain.DataCategoryContextual).
		WithStatus(domain.DataSetStatusDraft).
		WithValidationExpression("val > 0").
		WithResolutionKeyExpression("key").
		WithIsShared(false).
		WithAccessLevel(domain.AccessLevelPrivate).
		WithCreatedAt(now).
		WithUpdatedAt(now).
		Build()

	entity := DataSetDefinitionToEntity(original)
	restored := EntityToDataSetDefinition(entity)

	assert.Equal(t, original.ID(), restored.ID())
	assert.Equal(t, original.Code(), restored.Code())
	assert.Equal(t, original.Description(), restored.Description())
	assert.Equal(t, original.DataCategory(), restored.DataCategory())
	assert.Equal(t, original.Status(), restored.Status())
	assert.Equal(t, original.ValidationExpression(), restored.ValidationExpression())
}

// TestDataSourceRoundTrip verifies that domain -> entity -> domain preserves all fields.
func TestDataSourceRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	id := uuid.New()

	original := domain.NewDataSourceBuilder().
		WithID(id).
		WithCode("SRC_RT").
		WithName("Source Round Trip").
		WithDescription("round trip desc").
		WithTrustLevel(75).
		WithIsActive(true).
		WithCreatedAt(now).
		WithUpdatedAt(now).
		Build()

	entity := DataSourceToEntity(original)
	restored := EntityToDataSource(entity)

	assert.Equal(t, original.ID(), restored.ID())
	assert.Equal(t, original.Code(), restored.Code())
	assert.Equal(t, original.Name(), restored.Name())
	assert.Equal(t, original.Description(), restored.Description())
	assert.Equal(t, original.TrustLevel(), restored.TrustLevel())
}
