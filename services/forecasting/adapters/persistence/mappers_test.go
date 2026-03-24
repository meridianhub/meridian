package persistence

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/forecasting/domain"
)

func makeTestStrategy(t *testing.T) domain.ForecastingStrategy {
	t.Helper()
	s, err := domain.NewForecastingStrategy(
		"tenant-1",
		"Day-Ahead Price Forecast",
		"Generates 24-hour forward curve for electricity prices",
		`result = [42.0] * 24`,
		24,
		1,
		"0 16 * * *",
		[]string{"SPOT_PRICE", "WEATHER_FORECAST"},
		"FORWARD_CURVE_ELEC",
		"GB/SOUTH",
	)
	require.NoError(t, err)
	return s
}

func makeTestStrategyNoOptionals(t *testing.T) domain.ForecastingStrategy {
	t.Helper()
	s, err := domain.NewForecastingStrategy(
		"tenant-2",
		"Minimal Strategy",
		"", // no description
		`result = [1.0]`,
		1,
		1,
		"0 0 * * *",
		[]string{"INPUT"},
		"OUTPUT",
		"", // no resolution key
	)
	require.NoError(t, err)
	return s
}

// --- StrategyToEntity tests ---

func TestStrategyToEntity_FullStrategy(t *testing.T) {
	s := makeTestStrategy(t)

	entity := StrategyToEntity(s)

	assert.Equal(t, s.ID(), entity.ID)
	assert.Equal(t, "tenant-1", entity.TenantID)
	assert.Equal(t, "Day-Ahead Price Forecast", entity.Name)
	assert.Equal(t, `result = [42.0] * 24`, entity.StarlarkCode)
	assert.Equal(t, 24, entity.HorizonHours)
	assert.Equal(t, 1, entity.GranularityHours)
	assert.Equal(t, "0 16 * * *", entity.Schedule)
	assert.Equal(t, []string{"SPOT_PRICE", "WEATHER_FORECAST"}, entity.InputDatasetCodes)
	assert.Equal(t, "FORWARD_CURVE_ELEC", entity.OutputDatasetCode)
	assert.Equal(t, "DRAFT", entity.Status)
	assert.Equal(t, int64(1), entity.Version)

	// Optional fields present
	assert.True(t, entity.Description.Valid)
	assert.Equal(t, "Generates 24-hour forward curve for electricity prices", entity.Description.String)
	assert.True(t, entity.ReferenceDataResolutionKey.Valid)
	assert.Equal(t, "GB/SOUTH", entity.ReferenceDataResolutionKey.String)
}

func TestStrategyToEntity_EmptyOptionals(t *testing.T) {
	s := makeTestStrategyNoOptionals(t)

	entity := StrategyToEntity(s)

	assert.Equal(t, "tenant-2", entity.TenantID)
	assert.Equal(t, "Minimal Strategy", entity.Name)

	// Optional fields absent
	assert.False(t, entity.Description.Valid)
	assert.False(t, entity.ReferenceDataResolutionKey.Valid)
}

func TestStrategyToEntity_ActiveStatus(t *testing.T) {
	s := makeTestStrategy(t)
	activated, err := s.Activate()
	require.NoError(t, err)

	entity := StrategyToEntity(activated)

	assert.Equal(t, "ACTIVE", entity.Status)
}

func TestStrategyToEntity_DeprecatedStatus(t *testing.T) {
	s := makeTestStrategy(t)
	activated, err := s.Activate()
	require.NoError(t, err)
	deprecated, err := activated.Deprecate()
	require.NoError(t, err)

	entity := StrategyToEntity(deprecated)

	assert.Equal(t, "DEPRECATED", entity.Status)
}

func TestStrategyToEntity_TimestampsPreserved(t *testing.T) {
	s := makeTestStrategy(t)
	entity := StrategyToEntity(s)

	assert.Equal(t, s.CreatedAt().UTC(), entity.CreatedAt.UTC())
	assert.Equal(t, s.UpdatedAt().UTC(), entity.UpdatedAt.UTC())
}

// --- EntityToStrategy tests ---

func TestEntityToStrategy_FullEntity(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)

	entity := ForecastingStrategyEntity{
		ID:                         id,
		TenantID:                   "tenant-3",
		Name:                       "Energy Forecast",
		Description:                sql.NullString{String: "Detailed energy forecast", Valid: true},
		StarlarkCode:               `result = [10.0] * 24`,
		HorizonHours:               24,
		GranularityHours:           1,
		Schedule:                   "0 8 * * *",
		InputDatasetCodes:          []string{"ENERGY_PRICES"},
		OutputDatasetCode:          "FORECAST_ENERGY",
		ReferenceDataResolutionKey: sql.NullString{String: "US/EAST", Valid: true},
		Status:                     "ACTIVE",
		Version:                    5,
		CreatedAt:                  now,
		UpdatedAt:                  now,
	}

	strategy := EntityToStrategy(entity)

	assert.Equal(t, id, strategy.ID())
	assert.Equal(t, "tenant-3", strategy.TenantID())
	assert.Equal(t, "Energy Forecast", strategy.Name())
	assert.Equal(t, "Detailed energy forecast", strategy.Description())
	assert.Equal(t, `result = [10.0] * 24`, strategy.StarlarkCode())
	assert.Equal(t, 24, strategy.HorizonHours())
	assert.Equal(t, 1, strategy.GranularityHours())
	assert.Equal(t, "0 8 * * *", strategy.Schedule())
	assert.Equal(t, []string{"ENERGY_PRICES"}, strategy.InputDatasetCodes())
	assert.Equal(t, "FORECAST_ENERGY", strategy.OutputDatasetCode())
	assert.Equal(t, "US/EAST", strategy.ReferenceDataResolutionKey())
	assert.Equal(t, domain.StrategyStatusActive, strategy.Status())
	assert.Equal(t, int64(5), strategy.Version())
	assert.Equal(t, now, strategy.CreatedAt())
	assert.Equal(t, now, strategy.UpdatedAt())
}

func TestEntityToStrategy_EmptyOptionals(t *testing.T) {
	entity := ForecastingStrategyEntity{
		ID:                uuid.New(),
		TenantID:          "tenant-4",
		Name:              "Minimal",
		StarlarkCode:      `result = []`,
		HorizonHours:      1,
		GranularityHours:  1,
		Schedule:          "0 0 * * *",
		InputDatasetCodes: []string{"IN"},
		OutputDatasetCode: "OUT",
		Status:            "DRAFT",
		Version:           1,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		// Description and ReferenceDataResolutionKey are zero values (Invalid=false)
	}

	strategy := EntityToStrategy(entity)

	assert.Equal(t, "", strategy.Description())
	assert.Equal(t, "", strategy.ReferenceDataResolutionKey())
	assert.Equal(t, domain.StrategyStatusDraft, strategy.Status())
}

func TestEntityToStrategy_DeprecatedStatus(t *testing.T) {
	entity := ForecastingStrategyEntity{
		ID:                uuid.New(),
		TenantID:          "tenant-1",
		Name:              "Old Strategy",
		StarlarkCode:      `result = []`,
		HorizonHours:      1,
		GranularityHours:  1,
		Schedule:          "0 0 * * *",
		InputDatasetCodes: []string{"IN"},
		OutputDatasetCode: "OUT",
		Status:            "DEPRECATED",
		Version:           2,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	strategy := EntityToStrategy(entity)

	assert.Equal(t, domain.StrategyStatusDeprecated, strategy.Status())
}

func TestEntityToStrategy_UnknownStatusDefaultsToDraft(t *testing.T) {
	entity := ForecastingStrategyEntity{
		ID:                uuid.New(),
		TenantID:          "tenant-1",
		Name:              "Strategy",
		StarlarkCode:      `result = []`,
		HorizonHours:      1,
		GranularityHours:  1,
		Schedule:          "0 0 * * *",
		InputDatasetCodes: []string{"IN"},
		OutputDatasetCode: "OUT",
		Status:            "UNKNOWN_STATUS",
		Version:           1,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	strategy := EntityToStrategy(entity)

	assert.Equal(t, domain.StrategyStatusDraft, strategy.Status())
}

// --- Domain/Entity roundtrip tests ---

func TestStrategyToEntity_EntityToStrategy_Roundtrip(t *testing.T) {
	original := makeTestStrategy(t)

	entity := StrategyToEntity(original)
	reconstructed := EntityToStrategy(entity)

	assert.Equal(t, original.ID(), reconstructed.ID())
	assert.Equal(t, original.TenantID(), reconstructed.TenantID())
	assert.Equal(t, original.Name(), reconstructed.Name())
	assert.Equal(t, original.Description(), reconstructed.Description())
	assert.Equal(t, original.StarlarkCode(), reconstructed.StarlarkCode())
	assert.Equal(t, original.HorizonHours(), reconstructed.HorizonHours())
	assert.Equal(t, original.GranularityHours(), reconstructed.GranularityHours())
	assert.Equal(t, original.Schedule(), reconstructed.Schedule())
	assert.Equal(t, original.InputDatasetCodes(), reconstructed.InputDatasetCodes())
	assert.Equal(t, original.OutputDatasetCode(), reconstructed.OutputDatasetCode())
	assert.Equal(t, original.ReferenceDataResolutionKey(), reconstructed.ReferenceDataResolutionKey())
	assert.Equal(t, original.Status(), reconstructed.Status())
	assert.Equal(t, original.Version(), reconstructed.Version())
}

func TestStrategyToEntity_EntityToStrategy_Roundtrip_NoOptionals(t *testing.T) {
	original := makeTestStrategyNoOptionals(t)

	entity := StrategyToEntity(original)
	reconstructed := EntityToStrategy(entity)

	assert.Equal(t, original.ID(), reconstructed.ID())
	assert.Equal(t, "", reconstructed.Description())
	assert.Equal(t, "", reconstructed.ReferenceDataResolutionKey())
}
