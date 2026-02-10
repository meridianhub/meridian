package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createValidStrategy(t *testing.T) ForecastingStrategy {
	t.Helper()
	s, err := NewForecastingStrategy(
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

func TestNewForecastingStrategy_Success(t *testing.T) {
	beforeCreation := time.Now()

	s, err := NewForecastingStrategy(
		"tenant-1",
		"Day-Ahead Price Forecast",
		"Generates 24-hour forward curve",
		`result = [42.0] * 24`,
		24,
		1,
		"0 16 * * *",
		[]string{"SPOT_PRICE", "WEATHER_FORECAST"},
		"FORWARD_CURVE_ELEC",
		"GB/SOUTH",
	)

	require.NoError(t, err)

	assert.NotEqual(t, uuid.Nil, s.ID())
	assert.Equal(t, "tenant-1", s.TenantID())
	assert.Equal(t, "Day-Ahead Price Forecast", s.Name())
	assert.Equal(t, "Generates 24-hour forward curve", s.Description())
	assert.Equal(t, `result = [42.0] * 24`, s.StarlarkCode())
	assert.Equal(t, 24, s.HorizonHours())
	assert.Equal(t, 1, s.GranularityHours())
	assert.Equal(t, "0 16 * * *", s.Schedule())
	assert.Equal(t, []string{"SPOT_PRICE", "WEATHER_FORECAST"}, s.InputDatasetCodes())
	assert.Equal(t, "FORWARD_CURVE_ELEC", s.OutputDatasetCode())
	assert.Equal(t, "GB/SOUTH", s.ReferenceDataResolutionKey())
	assert.Equal(t, StrategyStatusDraft, s.Status())
	assert.Equal(t, int64(1), s.Version())

	assert.True(t, s.CreatedAt().After(beforeCreation) || s.CreatedAt().Equal(beforeCreation))
	assert.Equal(t, s.CreatedAt(), s.UpdatedAt())
}

func TestNewForecastingStrategy_EmptyDescription(t *testing.T) {
	s, err := NewForecastingStrategy(
		"tenant-1",
		"Minimal Strategy",
		"", // empty description is allowed
		`result = [1.0]`,
		1,
		1,
		"0 0 * * *",
		[]string{"INPUT"},
		"OUTPUT",
		"",
	)

	require.NoError(t, err)
	assert.Equal(t, "", s.Description())
	assert.Equal(t, "", s.ReferenceDataResolutionKey())
}

func TestNewForecastingStrategy_EmptyTenantID(t *testing.T) {
	_, err := NewForecastingStrategy(
		"",
		"Name",
		"",
		`code`,
		24,
		1,
		"0 0 * * *",
		[]string{"IN"},
		"OUT",
		"",
	)
	assert.ErrorIs(t, err, ErrTenantIDRequired)
}

func TestNewForecastingStrategy_EmptyName(t *testing.T) {
	_, err := NewForecastingStrategy(
		"tenant-1",
		"",
		"",
		`code`,
		24,
		1,
		"0 0 * * *",
		[]string{"IN"},
		"OUT",
		"",
	)
	assert.ErrorIs(t, err, ErrNameRequired)
}

func TestNewForecastingStrategy_EmptyStarlarkCode(t *testing.T) {
	_, err := NewForecastingStrategy(
		"tenant-1",
		"Name",
		"",
		"",
		24,
		1,
		"0 0 * * *",
		[]string{"IN"},
		"OUT",
		"",
	)
	assert.ErrorIs(t, err, ErrStarlarkCodeRequired)
}

func TestNewForecastingStrategy_InvalidHorizonHours(t *testing.T) {
	tests := []struct {
		name    string
		horizon int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too large", 169},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewForecastingStrategy(
				"tenant-1", "Name", "", `code`,
				tc.horizon, 1, "0 0 * * *",
				[]string{"IN"}, "OUT", "",
			)
			assert.ErrorIs(t, err, ErrInvalidHorizonHours)
		})
	}
}

func TestNewForecastingStrategy_InvalidGranularityHours(t *testing.T) {
	tests := []struct {
		name        string
		horizon     int
		granularity int
	}{
		{"zero", 24, 0},
		{"negative", 24, -1},
		{"exceeds horizon", 24, 25},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewForecastingStrategy(
				"tenant-1", "Name", "", `code`,
				tc.horizon, tc.granularity, "0 0 * * *",
				[]string{"IN"}, "OUT", "",
			)
			assert.ErrorIs(t, err, ErrInvalidGranularityHours)
		})
	}
}

func TestNewForecastingStrategy_GranularityEqualsHorizon(t *testing.T) {
	s, err := NewForecastingStrategy(
		"tenant-1", "Name", "", `code`,
		24, 24, "0 0 * * *",
		[]string{"IN"}, "OUT", "",
	)
	require.NoError(t, err)
	assert.Equal(t, 24, s.GranularityHours())
}

func TestNewForecastingStrategy_MaxHorizon(t *testing.T) {
	s, err := NewForecastingStrategy(
		"tenant-1", "Name", "", `code`,
		168, 24, "0 0 * * *",
		[]string{"IN"}, "OUT", "",
	)
	require.NoError(t, err)
	assert.Equal(t, 168, s.HorizonHours())
}

func TestNewForecastingStrategy_EmptySchedule(t *testing.T) {
	_, err := NewForecastingStrategy(
		"tenant-1", "Name", "", `code`,
		24, 1, "",
		[]string{"IN"}, "OUT", "",
	)
	assert.ErrorIs(t, err, ErrScheduleRequired)
}

func TestNewForecastingStrategy_InvalidSchedule(t *testing.T) {
	_, err := NewForecastingStrategy(
		"tenant-1", "Name", "", `code`,
		24, 1, "not-a-cron",
		[]string{"IN"}, "OUT", "",
	)
	assert.ErrorIs(t, err, ErrInvalidSchedule)
}

func TestNewForecastingStrategy_EmptyInputDatasetCodes(t *testing.T) {
	_, err := NewForecastingStrategy(
		"tenant-1", "Name", "", `code`,
		24, 1, "0 0 * * *",
		[]string{}, "OUT", "",
	)
	assert.ErrorIs(t, err, ErrInputDatasetCodesRequired)
}

func TestNewForecastingStrategy_NilInputDatasetCodes(t *testing.T) {
	_, err := NewForecastingStrategy(
		"tenant-1", "Name", "", `code`,
		24, 1, "0 0 * * *",
		nil, "OUT", "",
	)
	assert.ErrorIs(t, err, ErrInputDatasetCodesRequired)
}

func TestNewForecastingStrategy_EmptyOutputDatasetCode(t *testing.T) {
	_, err := NewForecastingStrategy(
		"tenant-1", "Name", "", `code`,
		24, 1, "0 0 * * *",
		[]string{"IN"}, "", "",
	)
	assert.ErrorIs(t, err, ErrOutputDatasetCodeRequired)
}

func TestNewForecastingStrategy_InputSliceDefensiveCopy(t *testing.T) {
	inputs := []string{"A", "B"}
	s, err := NewForecastingStrategy(
		"tenant-1", "Name", "", `code`,
		24, 1, "0 0 * * *",
		inputs, "OUT", "",
	)
	require.NoError(t, err)

	// Mutating the original slice should not affect the strategy
	inputs[0] = "MUTATED"
	assert.Equal(t, "A", s.InputDatasetCodes()[0])
}

func TestForecastingStrategy_Activate(t *testing.T) {
	s := createValidStrategy(t)
	assert.Equal(t, StrategyStatusDraft, s.Status())

	activated, err := s.Activate()
	require.NoError(t, err)

	assert.Equal(t, StrategyStatusActive, activated.Status())
	assert.Equal(t, int64(2), activated.Version())
	assert.True(t, activated.UpdatedAt().After(s.UpdatedAt()) || activated.UpdatedAt().Equal(s.UpdatedAt()))

	// Original should be unchanged
	assert.Equal(t, StrategyStatusDraft, s.Status())
	assert.Equal(t, int64(1), s.Version())
}

func TestForecastingStrategy_Activate_AlreadyActive(t *testing.T) {
	s := createValidStrategy(t)
	activated, err := s.Activate()
	require.NoError(t, err)

	_, err = activated.Activate()
	assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
}

func TestForecastingStrategy_Deprecate_FromDraft(t *testing.T) {
	s := createValidStrategy(t)

	deprecated, err := s.Deprecate()
	require.NoError(t, err)

	assert.Equal(t, StrategyStatusDeprecated, deprecated.Status())
	assert.Equal(t, int64(2), deprecated.Version())
}

func TestForecastingStrategy_Deprecate_FromActive(t *testing.T) {
	s := createValidStrategy(t)
	activated, err := s.Activate()
	require.NoError(t, err)

	deprecated, err := activated.Deprecate()
	require.NoError(t, err)

	assert.Equal(t, StrategyStatusDeprecated, deprecated.Status())
	assert.Equal(t, int64(3), deprecated.Version())
}

func TestForecastingStrategy_Deprecate_AlreadyDeprecated(t *testing.T) {
	s := createValidStrategy(t)
	deprecated, err := s.Deprecate()
	require.NoError(t, err)

	_, err = deprecated.Deprecate()
	assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
}

func TestForecastingStrategy_Activate_FromDeprecated(t *testing.T) {
	s := createValidStrategy(t)
	deprecated, err := s.Deprecate()
	require.NoError(t, err)

	_, err = deprecated.Activate()
	assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
}

func TestForecastingStrategy_UpdateStarlarkCode(t *testing.T) {
	s := createValidStrategy(t)

	updated, err := s.UpdateStarlarkCode(`result = [99.0] * 24`)
	require.NoError(t, err)

	assert.Equal(t, `result = [99.0] * 24`, updated.StarlarkCode())
	assert.Equal(t, int64(2), updated.Version())

	// Original unchanged
	assert.Equal(t, `result = [42.0] * 24`, s.StarlarkCode())
}

func TestForecastingStrategy_UpdateStarlarkCode_EmptyCode(t *testing.T) {
	s := createValidStrategy(t)

	_, err := s.UpdateStarlarkCode("")
	assert.ErrorIs(t, err, ErrStarlarkCodeRequired)
}

func TestForecastingStrategy_UpdateStarlarkCode_Deprecated(t *testing.T) {
	s := createValidStrategy(t)
	deprecated, err := s.Deprecate()
	require.NoError(t, err)

	_, err = deprecated.UpdateStarlarkCode(`new code`)
	assert.ErrorIs(t, err, ErrStrategyDeprecated)
}

func TestForecastingStrategy_UpdateDescription(t *testing.T) {
	s := createValidStrategy(t)

	updated, err := s.UpdateDescription("New description")
	require.NoError(t, err)

	assert.Equal(t, "New description", updated.Description())
	assert.Equal(t, int64(2), updated.Version())
}

func TestForecastingStrategy_UpdateDescription_Deprecated(t *testing.T) {
	s := createValidStrategy(t)
	deprecated, err := s.Deprecate()
	require.NoError(t, err)

	_, err = deprecated.UpdateDescription("New")
	assert.ErrorIs(t, err, ErrStrategyDeprecated)
}

func TestForecastingStrategy_UpdateSchedule(t *testing.T) {
	s := createValidStrategy(t)

	updated, err := s.UpdateSchedule("30 8 * * *")
	require.NoError(t, err)

	assert.Equal(t, "30 8 * * *", updated.Schedule())
	assert.Equal(t, int64(2), updated.Version())
}

func TestForecastingStrategy_UpdateSchedule_InvalidCron(t *testing.T) {
	s := createValidStrategy(t)

	_, err := s.UpdateSchedule("invalid")
	assert.ErrorIs(t, err, ErrInvalidSchedule)
}

func TestForecastingStrategy_UpdateSchedule_Empty(t *testing.T) {
	s := createValidStrategy(t)

	_, err := s.UpdateSchedule("")
	assert.ErrorIs(t, err, ErrScheduleRequired)
}

func TestForecastingStrategy_UpdateSchedule_Deprecated(t *testing.T) {
	s := createValidStrategy(t)
	deprecated, err := s.Deprecate()
	require.NoError(t, err)

	_, err = deprecated.UpdateSchedule("0 0 * * *")
	assert.ErrorIs(t, err, ErrStrategyDeprecated)
}

func TestForecastingStrategy_VersionIncrementOnUpdate(t *testing.T) {
	s := createValidStrategy(t)
	assert.Equal(t, int64(1), s.Version())

	s, err := s.UpdateDescription("v2")
	require.NoError(t, err)
	assert.Equal(t, int64(2), s.Version())

	s, err = s.UpdateStarlarkCode("new code")
	require.NoError(t, err)
	assert.Equal(t, int64(3), s.Version())

	s, err = s.Activate()
	require.NoError(t, err)
	assert.Equal(t, int64(4), s.Version())

	s, err = s.Deprecate()
	require.NoError(t, err)
	assert.Equal(t, int64(5), s.Version())
}

func TestForecastingStrategyBuilder_Build(t *testing.T) {
	id := uuid.New()
	now := time.Now()

	s := NewForecastingStrategyBuilder().
		WithID(id).
		WithTenantID("tenant-1").
		WithName("Built Strategy").
		WithDescription("From builder").
		WithStarlarkCode("code here").
		WithHorizonHours(48).
		WithGranularityHours(2).
		WithSchedule("0 16 * * *").
		WithInputDatasetCodes([]string{"DS1", "DS2"}).
		WithOutputDatasetCode("OUT1").
		WithReferenceDataResolutionKey("GB/NORTH").
		WithStatus(StrategyStatusActive).
		WithVersion(5).
		WithCreatedAt(now).
		WithUpdatedAt(now).
		Build()

	assert.Equal(t, id, s.ID())
	assert.Equal(t, "tenant-1", s.TenantID())
	assert.Equal(t, "Built Strategy", s.Name())
	assert.Equal(t, "From builder", s.Description())
	assert.Equal(t, "code here", s.StarlarkCode())
	assert.Equal(t, 48, s.HorizonHours())
	assert.Equal(t, 2, s.GranularityHours())
	assert.Equal(t, "0 16 * * *", s.Schedule())
	assert.Equal(t, []string{"DS1", "DS2"}, s.InputDatasetCodes())
	assert.Equal(t, "OUT1", s.OutputDatasetCode())
	assert.Equal(t, "GB/NORTH", s.ReferenceDataResolutionKey())
	assert.Equal(t, StrategyStatusActive, s.Status())
	assert.Equal(t, int64(5), s.Version())
	assert.Equal(t, now, s.CreatedAt())
	assert.Equal(t, now, s.UpdatedAt())
}

func TestForecastingStrategyBuilder_InputSliceDefensiveCopy(t *testing.T) {
	codes := []string{"A", "B"}

	s := NewForecastingStrategyBuilder().
		WithInputDatasetCodes(codes).
		Build()

	// Mutating original should not affect built strategy
	codes[0] = "MUTATED"
	assert.Equal(t, "A", s.InputDatasetCodes()[0])
}
