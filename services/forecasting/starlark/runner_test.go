package starlark

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	starlarklib "go.starlark.net/starlark"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// --- Mock implementations ---

type mockMISClient struct {
	observations map[string][]Observation
	err          error
}

func (m *mockMISClient) FetchObservations(_ context.Context, datasetCode string, _ time.Time) ([]Observation, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.observations[datasetCode], nil
}

type mockRefDataClient struct {
	nodes map[string]*ReferenceData
	err   error
}

func (m *mockRefDataClient) GetNodeByResolutionKey(_ context.Context, _, resolutionKey string) (*ReferenceData, error) {
	if m.err != nil {
		return nil, m.err
	}
	node, ok := m.nodes[resolutionKey]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", resolutionKey)
	}
	return node, nil
}

// --- Test helpers ---

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func newTestRunner(t *testing.T, mis MISClient, ref RefDataClient) *ForecastRunner {
	t.Helper()
	runner, err := NewForecastRunner(ForecastRunnerConfig{
		MISClient: mis,
		RefData:   ref,
		Timeout:   5 * time.Second,
		Logger:    newTestLogger(),
	})
	require.NoError(t, err)
	return runner
}

func baseTime() time.Time {
	return time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)
}

// --- Constructor tests ---

func TestNewForecastRunner(t *testing.T) {
	t.Run("requires MIS client", func(t *testing.T) {
		_, err := NewForecastRunner(ForecastRunnerConfig{
			RefData: &mockRefDataClient{},
		})
		require.ErrorIs(t, err, ErrMISClientRequired)
	})

	t.Run("requires ref data client", func(t *testing.T) {
		_, err := NewForecastRunner(ForecastRunnerConfig{
			MISClient: &mockMISClient{},
		})
		require.ErrorIs(t, err, ErrRefDataRequired)
	})

	t.Run("succeeds with valid config", func(t *testing.T) {
		runner, err := NewForecastRunner(ForecastRunnerConfig{
			MISClient: &mockMISClient{},
			RefData:   &mockRefDataClient{},
		})
		require.NoError(t, err)
		require.NotNil(t, runner)
		assert.Equal(t, DefaultTimeout, runner.timeout)
	})

	t.Run("applies custom timeout", func(t *testing.T) {
		runner, err := NewForecastRunner(ForecastRunnerConfig{
			MISClient: &mockMISClient{},
			RefData:   &mockRefDataClient{},
			Timeout:   10 * time.Second,
		})
		require.NoError(t, err)
		assert.Equal(t, 10*time.Second, runner.timeout)
	})
}

// --- Simple moving average test (24h horizon, 1h granularity) ---

func TestExecuteStrategy_SimpleMovingAverage(t *testing.T) {
	now := baseTime()

	// Build 24 hours of historical observations
	observations := make([]Observation, 24)
	for i := 0; i < 24; i++ {
		observations[i] = Observation{
			Timestamp: now.Add(-time.Duration(24-i) * time.Hour),
			Value:     decimal.NewFromInt(int64(100 + i)),
			Quality:   "QUALITY_LEVEL_ACTUAL",
		}
	}

	mis := &mockMISClient{
		observations: map[string][]Observation{
			"UTILIZATION_COMPUTE_HOUR": observations,
		},
	}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}

	runner := newTestRunner(t, mis, ref)

	// Simple moving average strategy using add_seconds builtin
	script := `
def compute_forecast(ctx):
    obs = ctx["observations"]["UTILIZATION_COMPUTE_HOUR"]

    # Extract values and compute simple moving average
    values = [float(o["value"]) for o in obs]
    avg_val = 0.0
    for v in values:
        avg_val = avg_val + v
    avg_val = avg_val / len(values)

    now = ctx["now"]
    granularity = int(ctx["granularity_seconds"])
    horizon = int(ctx["horizon_seconds"])
    num_points = horizon // granularity

    points = []
    for i in range(num_points):
        offset = (i + 1) * granularity
        ts = add_seconds(now, offset)
        points.append({
            "timestamp": ts,
            "value": avg_val,
        })
    return points
`

	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION_COMPUTE_HOUR"},
		OutputDatasetCode: "FORECAST_UTILIZATION",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// All points should have the same average value
	// avg(100..123) = (100+101+...+123)/24 = 2676/24 = 111.5
	expectedAvg := decimal.NewFromFloat(111.5)
	for i, p := range points {
		assert.True(t, p.Value.Equal(expectedAvg), "point %d: expected %s, got %s", i, expectedAvg, p.Value)
		expectedTS := now.Add(time.Duration(i+1) * time.Hour)
		assert.Equal(t, expectedTS, p.Timestamp, "point %d timestamp mismatch", i)
	}
}

// --- Reference data access test ---

func TestExecuteStrategy_AccessesReferenceData(t *testing.T) {
	now := baseTime()

	mis := &mockMISClient{
		observations: map[string][]Observation{
			"UTILIZATION_COMPUTE_HOUR": {
				{Timestamp: now.Add(-time.Hour), Value: decimal.NewFromInt(100), Quality: "ACTUAL"},
			},
		},
	}
	ref := &mockRefDataClient{
		nodes: map[string]*ReferenceData{
			"region:us-east-1/zone:us-east-1a": {
				NodeType:      "zone",
				ResolutionKey: "region:us-east-1/zone:us-east-1a",
				Attributes: map[string]any{
					"capacity_factor": "0.85",
					"region_name":     "US East 1a",
				},
			},
		},
	}

	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    ref = ctx["reference_data"]
    capacity = float(ref["attributes"]["capacity_factor"])

    obs = ctx["observations"]["UTILIZATION_COMPUTE_HOUR"]
    base_value = float(obs[0]["value"]) * capacity

    now = ctx["now"]
    granularity = int(ctx["granularity_seconds"])
    horizon = int(ctx["horizon_seconds"])
    num_points = horizon // granularity

    points = []
    for i in range(num_points):
        offset = (i + 1) * granularity
        ts = add_seconds(now, offset)
        points.append({
            "timestamp": ts,
            "value": base_value,
            "metadata": {"source": "capacity_adjusted"},
        })
    return points
`

	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION_COMPUTE_HOUR"},
		OutputDatasetCode: "FORECAST_UTILIZATION",
		ResolutionKey:     "region:us-east-1/zone:us-east-1a",
		TenantID:          "tenant-1",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// 100 * 0.85 = 85
	expectedValue := decimal.NewFromFloat(85)
	assert.True(t, points[0].Value.Equal(expectedValue), "expected %s, got %s", expectedValue, points[0].Value)
	assert.Equal(t, "capacity_adjusted", points[0].Metadata["source"])
}

// --- Builtin tests ---

func TestBuiltins_Avg(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    result = avg([Decimal("1"), Decimal("2"), Decimal("3")])
    ts = add_seconds(ctx["now"], 3600)
    return [{"timestamp": ts, "value": str(result)}]
`

	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.True(t, points[0].Value.Equal(decimal.NewFromInt(2)), "avg([1,2,3]) should be 2, got %s", points[0].Value)
}

func TestBuiltins_Sum(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    result = sum([Decimal("10"), Decimal("20"), Decimal("30")])
    ts = add_seconds(ctx["now"], 3600)
    return [{"timestamp": ts, "value": str(result)}]
`

	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.True(t, points[0].Value.Equal(decimal.NewFromInt(60)), "sum([10,20,30]) should be 60, got %s", points[0].Value)
}

func TestBuiltins_Percentile(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    vals = [Decimal("10"), Decimal("20"), Decimal("30"), Decimal("40"), Decimal("50")]
    p50 = percentile(vals, 50)
    ts = add_seconds(ctx["now"], 3600)
    return [{"timestamp": ts, "value": str(p50)}]
`

	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.True(t, points[0].Value.Equal(decimal.NewFromInt(30)), "percentile([10..50], 50) should be 30, got %s", points[0].Value)
}

func TestBuiltins_Duration(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    d = duration(hours=1)
    ts = add_seconds(ctx["now"], 3600)
    return [{"timestamp": ts, "value": d}]
`

	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.True(t, points[0].Value.Equal(decimal.NewFromInt(3600)), "duration(hours=1) should be 3600, got %s", points[0].Value)
}

func TestBuiltins_AddSeconds(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    ts = add_seconds(ctx["now"], 7200)  # +2 hours
    return [{"timestamp": ts, "value": 42}]
`

	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	require.Len(t, points, 1)

	expected := now.Add(2 * time.Hour)
	assert.Equal(t, expected, points[0].Timestamp)
}

func TestBuiltins_FilterByHour(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    # Create observations at different hours
    obs = [
        {"timestamp": "2026-02-10T10:00:00Z", "value": "100"},
        {"timestamp": "2026-02-10T10:30:00Z", "value": "110"},
        {"timestamp": "2026-02-10T11:00:00Z", "value": "120"},
        {"timestamp": "2026-02-10T12:00:00Z", "value": "130"},
    ]

    # Filter for hour 10
    filtered = filter_by_hour(obs, 10)
    count = len(filtered)  # Should be 2

    ts = add_seconds(ctx["now"], 3600)
    return [{"timestamp": ts, "value": count}]
`

	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.True(t, points[0].Value.Equal(decimal.NewFromInt(2)), "filter_by_hour should return 2 obs for hour 10, got %s", points[0].Value)
}

func TestBuiltins_GroupByHour(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    obs = [
        {"timestamp": "2026-02-10T10:00:00Z", "value": "100"},
        {"timestamp": "2026-02-10T10:30:00Z", "value": "110"},
        {"timestamp": "2026-02-10T11:00:00Z", "value": "120"},
    ]

    groups = group_by_hour(obs)
    # groups[10] should have 2 items, groups[11] should have 1
    count_10 = len(groups[10])
    count_11 = len(groups[11])

    ts = add_seconds(ctx["now"], 3600)
    return [
        {"timestamp": ts, "value": count_10, "metadata": {"hour": "10"}},
    ]
`

	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.True(t, points[0].Value.Equal(decimal.NewFromInt(2)), "group_by_hour[10] should have 2 items, got %s", points[0].Value)
}

// --- Safety tests ---

func TestExecuteStrategy_TimeoutEnforcement(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}

	runner, err := NewForecastRunner(ForecastRunnerConfig{
		MISClient: mis,
		RefData:   ref,
		Timeout:   200 * time.Millisecond,
		Logger:    newTestLogger(),
	})
	require.NoError(t, err)

	// Script with long computation (large for loop)
	script := `
def compute_forecast(ctx):
    result = 0
    for i in range(100000000):
        result = result + i
    return []
`

	_, err = runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               baseTime(),
	})

	require.Error(t, err)
	assert.True(t,
		errors.Is(err, saga.ErrTimeout) || strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "cancelled"),
		"expected timeout error, got: %v", err)
}

func TestExecuteStrategy_SyntaxError(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx)
    return []
`

	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               baseTime(),
	})

	require.Error(t, err)
	assert.True(t,
		errors.Is(err, ErrValidation) || strings.Contains(err.Error(), "syntax") || strings.Contains(err.Error(), "got newline"),
		"expected validation/syntax error, got: %v", err)
}

func TestExecuteStrategy_MissingEntryPoint(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
x = 42
`

	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               baseTime(),
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEntryPointMissing)
}

func TestExecuteStrategy_EmptyScript(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            "",
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               baseTime(),
	})

	require.ErrorIs(t, err, ErrScriptRequired)
}

func TestExecuteStrategy_InvalidHorizon(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            "def compute_forecast(ctx): return []",
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      0,
		GranularityHours:  1,
		Now:               baseTime(),
	})

	require.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "horizon_hours")
}

func TestExecuteStrategy_InvalidGranularity(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            "def compute_forecast(ctx): return []",
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      24,
		GranularityHours:  0,
		Now:               baseTime(),
	})

	require.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "granularity_hours")
}

func TestExecuteStrategy_PartialRefDataConfig(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	// ResolutionKey set but TenantID empty
	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            "def compute_forecast(ctx): return []",
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		ResolutionKey:     "region:us-east-1",
		TenantID:          "",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               baseTime(),
	})

	require.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "resolution_key and tenant_id must both be set")
}

// --- Validation tests ---

func TestValidateForecastPoints_OutOfRange(t *testing.T) {
	now := baseTime()
	horizon := 24 * time.Hour
	granularity := time.Hour

	points := []ForecastPoint{
		{
			Timestamp: now.Add(-time.Hour), // before now
			Value:     decimal.NewFromInt(1),
		},
	}

	err := validateForecastPoints(points, now, horizon, granularity)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTimestampOutOfRange)
}

func TestValidateForecastPoints_NonMonotonic(t *testing.T) {
	now := baseTime()
	horizon := 24 * time.Hour
	granularity := time.Hour

	points := []ForecastPoint{
		{Timestamp: now.Add(2 * time.Hour), Value: decimal.NewFromInt(1)},
		{Timestamp: now.Add(1 * time.Hour), Value: decimal.NewFromInt(2)}, // out of order
	}

	err := validateForecastPoints(points, now, horizon, granularity)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNonMonotonic)
}

func TestValidateForecastPoints_GranularityMismatch(t *testing.T) {
	now := baseTime()
	horizon := 24 * time.Hour
	granularity := time.Hour

	points := []ForecastPoint{
		{Timestamp: now.Add(30 * time.Minute), Value: decimal.NewFromInt(1)}, // 30min not aligned to 1h
	}

	err := validateForecastPoints(points, now, horizon, granularity)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGranularityMismatch)
}

func TestValidateForecastPoints_Valid(t *testing.T) {
	now := baseTime()
	horizon := 24 * time.Hour
	granularity := time.Hour

	points := make([]ForecastPoint, 24)
	for i := 0; i < 24; i++ {
		points[i] = ForecastPoint{
			Timestamp: now.Add(time.Duration(i+1) * time.Hour),
			Value:     decimal.NewFromInt(int64(i)),
		}
	}

	err := validateForecastPoints(points, now, horizon, granularity)
	require.NoError(t, err)
}

// --- Empty data cold start handling ---

func TestExecuteStrategy_EmptyObservations(t *testing.T) {
	now := baseTime()

	mis := &mockMISClient{
		observations: map[string][]Observation{
			"UTILIZATION_COMPUTE_HOUR": {}, // empty - cold start
		},
	}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    obs = ctx["observations"]["UTILIZATION_COMPUTE_HOUR"]
    if len(obs) == 0:
        default_value = 0.0
    else:
        total = 0.0
        for o in obs:
            total = total + float(o["value"])
        default_value = total / len(obs)

    now = ctx["now"]
    granularity = int(ctx["granularity_seconds"])
    horizon = int(ctx["horizon_seconds"])
    num_points = horizon // granularity

    points = []
    for i in range(num_points):
        offset = (i + 1) * granularity
        ts = add_seconds(now, offset)
        points.append({"timestamp": ts, "value": default_value})
    return points
`

	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION_COMPUTE_HOUR"},
		OutputDatasetCode: "FORECAST_UTILIZATION",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// All points should be zero (cold start)
	for i, p := range points {
		assert.True(t, p.Value.IsZero(), "point %d should be zero, got %s", i, p.Value)
	}
}

// --- MIS client error test ---

func TestExecuteStrategy_MISClientError(t *testing.T) {
	mis := &mockMISClient{err: errors.New("connection refused")}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    return []
`

	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION_COMPUTE_HOUR"},
		OutputDatasetCode: "TEST",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               baseTime(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch observations")
	assert.Contains(t, err.Error(), "connection refused")
}

// --- Invalid return type test ---

func TestExecuteStrategy_InvalidReturnType(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    return "not a list"
`

	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               baseTime(),
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidReturnType)
}

// --- ForecastContext serialization ---

func TestForecastContextToStarlark(t *testing.T) {
	now := baseTime()

	fc := &ForecastContext{
		Observations: map[string][]Observation{
			"DATASET_A": {
				{Timestamp: now, Value: decimal.NewFromInt(42), Quality: "ACTUAL"},
			},
		},
		ReferenceData: &ReferenceData{
			NodeType:      "zone",
			ResolutionKey: "region:us-east-1",
			Attributes:    map[string]any{"capacity": "100"},
		},
		Horizon:     24 * time.Hour,
		Granularity: time.Hour,
		Now:         now,
	}

	dict := forecastContextToStarlark(fc)

	// Verify it's frozen
	err := dict.SetKey(starlarklib.String("test"), starlarklib.None)
	assert.Error(t, err, "frozen dict should reject mutation")

	// Verify observations key exists
	obsVal, found, err := dict.Get(starlarklib.String("observations"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.NotNil(t, obsVal)

	// Verify reference_data key exists
	refVal, found, err := dict.Get(starlarklib.String("reference_data"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.NotEqual(t, starlarklib.None, refVal)

	// Verify now
	nowVal, found, err := dict.Get(starlarklib.String("now"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, starlarklib.String(now.Format(time.RFC3339)), nowVal)
}

// --- Security hardening tests ---

func TestExecuteStrategy_ScriptSizeLimit(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	// Create a script that exceeds MaxScriptSize (64KB)
	largeScript := "def compute_forecast(ctx):\n    return []\n" + strings.Repeat("# padding\n", 7000)
	require.Greater(t, len(largeScript), MaxScriptSize, "test script must exceed MaxScriptSize")

	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            largeScript,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               baseTime(),
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrScriptTooLarge)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestExecuteStrategy_StepLimitEnforced(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	// Script with a loop that exceeds MaxStepsPerExecution (1M steps)
	script := `
def compute_forecast(ctx):
    result = 0
    for i in range(2000000):
        result = result + i
    return []
`

	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               baseTime(),
	})

	require.Error(t, err)
	// Step limit exceeded produces either a timeout or execution error
	assert.True(t,
		strings.Contains(err.Error(), "too many steps") ||
			strings.Contains(err.Error(), "exceeded") ||
			errors.Is(err, saga.ErrExecution) ||
			errors.Is(err, saga.ErrTimeout),
		"expected step limit or execution error, got: %v", err)
}

func TestDefaultTimeout_Is10Seconds(t *testing.T) {
	assert.Equal(t, 10*time.Second, DefaultTimeout, "DefaultTimeout should be 10s")
}

func TestForecastContextToStarlark_NilReferenceData(t *testing.T) {
	fc := &ForecastContext{
		Observations:  map[string][]Observation{},
		ReferenceData: nil,
		Horizon:       time.Hour,
		Granularity:   time.Hour,
		Now:           baseTime(),
	}

	dict := forecastContextToStarlark(fc)

	refVal, found, err := dict.Get(starlarklib.String("reference_data"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, starlarklib.None, refVal)
}
