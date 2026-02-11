package templates

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/forecasting/starlark"
)

// --- Mock implementations ---

type mockMISClient struct {
	observations map[string][]starlark.Observation
}

func (m *mockMISClient) FetchObservations(_ context.Context, datasetCode string, _ time.Time) ([]starlark.Observation, error) {
	return m.observations[datasetCode], nil
}

type mockRefDataClient struct {
	nodes map[string]*starlark.ReferenceData
}

func (m *mockRefDataClient) GetNodeByResolutionKey(_ context.Context, _, resolutionKey string) (*starlark.ReferenceData, error) {
	node, ok := m.nodes[resolutionKey]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", resolutionKey)
	}
	return node, nil
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func newTestRunner(t *testing.T, mis starlark.MISClient, ref starlark.RefDataClient) *starlark.ForecastRunner {
	t.Helper()
	runner, err := starlark.NewForecastRunner(starlark.ForecastRunnerConfig{
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

// --- Template loading tests ---

func TestLoad_AllTemplates(t *testing.T) {
	for _, name := range All() {
		t.Run(name, func(t *testing.T) {
			script, err := Load(name)
			require.NoError(t, err)
			assert.NotEmpty(t, script)
			assert.Contains(t, script, "def compute_forecast(ctx)")
		})
	}
}

func TestLoad_NonexistentTemplate(t *testing.T) {
	_, err := Load("does_not_exist.star")
	require.Error(t, err)
}

// --- Moving average template tests ---

func TestMovingAverage_SineWaveData(t *testing.T) {
	now := baseTime()

	// Generate 48 hours of sine wave data (amplitude 50, centered at 100)
	observations := make([]starlark.Observation, 48)
	for i := 0; i < 48; i++ {
		angle := float64(i) / 24.0 * 2 * math.Pi
		value := 100.0 + 50.0*math.Sin(angle)
		observations[i] = starlark.Observation{
			Timestamp: now.Add(-time.Duration(48-i) * time.Hour),
			Value:     decimal.NewFromFloat(value),
			Quality:   "ACTUAL",
		}
	}

	mis := &mockMISClient{
		observations: map[string][]starlark.Observation{
			"UTILIZATION": observations,
		},
	}
	ref := &mockRefDataClient{nodes: map[string]*starlark.ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script, err := Load(MovingAverage)
	require.NoError(t, err)

	points, err := runner.ExecuteStrategy(context.Background(), starlark.StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION"},
		OutputDatasetCode: "FORECAST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// Sine wave over full periods averages to the center value (100)
	// With 2 full periods, average should be very close to 100
	for i, p := range points {
		diff := p.Value.Sub(decimal.NewFromInt(100)).Abs()
		assert.True(t, diff.LessThan(decimal.NewFromFloat(1.0)),
			"point %d: expected ~100, got %s (diff %s)", i, p.Value, diff)
		assert.Equal(t, "moving_average", p.Metadata["algorithm"])
	}
}

func TestMovingAverage_ExponentialSmoothing(t *testing.T) {
	now := baseTime()

	// Data with a trend: 10, 20, 30, 40, 50
	observations := make([]starlark.Observation, 5)
	for i := 0; i < 5; i++ {
		observations[i] = starlark.Observation{
			Timestamp: now.Add(-time.Duration(5-i) * time.Hour),
			Value:     decimal.NewFromInt(int64((i + 1) * 10)),
			Quality:   "ACTUAL",
		}
	}

	mis := &mockMISClient{
		observations: map[string][]starlark.Observation{
			"UTILIZATION": observations,
		},
	}
	ref := &mockRefDataClient{
		nodes: map[string]*starlark.ReferenceData{
			"test-key": {
				NodeType:      "zone",
				ResolutionKey: "test-key",
				Attributes: map[string]any{
					"alpha": "0.5",
				},
			},
		},
	}
	runner := newTestRunner(t, mis, ref)

	script, err := Load(MovingAverage)
	require.NoError(t, err)

	points, err := runner.ExecuteStrategy(context.Background(), starlark.StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION"},
		OutputDatasetCode: "FORECAST",
		ResolutionKey:     "test-key",
		TenantID:          "tenant-1",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// EMA with alpha=0.5 on [10,20,30,40,50]:
	// EMA_0 = 10
	// EMA_1 = 0.5*20 + 0.5*10 = 15
	// EMA_2 = 0.5*30 + 0.5*15 = 22.5
	// EMA_3 = 0.5*40 + 0.5*22.5 = 31.25
	// EMA_4 = 0.5*50 + 0.5*31.25 = 40.625
	expected := decimal.NewFromFloat(40.625)
	for _, p := range points {
		assert.True(t, p.Value.Equal(expected),
			"expected %s, got %s", expected, p.Value)
	}
}

func TestMovingAverage_EmptyObservations(t *testing.T) {
	now := baseTime()

	mis := &mockMISClient{
		observations: map[string][]starlark.Observation{
			"UTILIZATION": {},
		},
	}
	ref := &mockRefDataClient{nodes: map[string]*starlark.ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script, err := Load(MovingAverage)
	require.NoError(t, err)

	points, err := runner.ExecuteStrategy(context.Background(), starlark.StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION"},
		OutputDatasetCode: "FORECAST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Empty(t, points)
}

// --- Seasonal decomposition template tests ---

func TestSeasonalDecomposition_PeakPattern(t *testing.T) {
	now := baseTime()

	// Create 7 days of hourly data with a clear peak at hour 14
	observations := make([]starlark.Observation, 0, 7*24)
	for day := 0; day < 7; day++ {
		for hour := 0; hour < 24; hour++ {
			ts := now.Add(-time.Duration(7*24-day*24-hour) * time.Hour)
			var value float64
			if hour == 14 {
				value = 200.0 // peak hour
			} else {
				value = 50.0 // base value
			}
			observations = append(observations, starlark.Observation{
				Timestamp: ts,
				Value:     decimal.NewFromFloat(value),
				Quality:   "ACTUAL",
			})
		}
	}

	mis := &mockMISClient{
		observations: map[string][]starlark.Observation{
			"UTILIZATION": observations,
		},
	}
	ref := &mockRefDataClient{nodes: map[string]*starlark.ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script, err := Load(SeasonalDecomposition)
	require.NoError(t, err)

	points, err := runner.ExecuteStrategy(context.Background(), starlark.StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION"},
		OutputDatasetCode: "FORECAST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// Point at hour 14 (index 13, since offset starts at hour 1) should be peak
	// Points are at hour 1, 2, ..., 24 (wraps to 0)
	for _, p := range points {
		hourStr := p.Metadata["hour"]
		assert.Equal(t, "seasonal_decomposition", p.Metadata["algorithm"])
		if hourStr == "14" {
			// Peak hour should have value 200
			assert.True(t, p.Value.Equal(decimal.NewFromFloat(200)),
				"peak hour 14: expected 200, got %s", p.Value)
		} else {
			// Non-peak hours should have value 50
			assert.True(t, p.Value.Equal(decimal.NewFromFloat(50)),
				"hour %s: expected 50, got %s", hourStr, p.Value)
		}
	}
}

// --- Capacity pricing template tests ---

func TestCapacityPricing_WithRefDataCapacity(t *testing.T) {
	now := baseTime()

	// avg=85 with capacity=100 -> utilization_ratio=0.85 -> peak tier
	observations := make([]starlark.Observation, 24)
	for i := 0; i < 24; i++ {
		observations[i] = starlark.Observation{
			Timestamp: now.Add(-time.Duration(24-i) * time.Hour),
			Value:     decimal.NewFromInt(85),
			Quality:   "ACTUAL",
		}
	}

	mis := &mockMISClient{
		observations: map[string][]starlark.Observation{
			"UTILIZATION": observations,
		},
	}
	ref := &mockRefDataClient{
		nodes: map[string]*starlark.ReferenceData{
			"zone-key": {
				NodeType:      "zone",
				ResolutionKey: "zone-key",
				Attributes: map[string]any{
					"capacity": "100",
				},
			},
		},
	}
	runner := newTestRunner(t, mis, ref)

	script, err := Load(CapacityPricing)
	require.NoError(t, err)

	points, err := runner.ExecuteStrategy(context.Background(), starlark.StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION"},
		OutputDatasetCode: "FORECAST_PRICE",
		ResolutionKey:     "zone-key",
		TenantID:          "tenant-1",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// 85/100 = 0.85 -> peak tier -> base_price(100) * peak_rate(2.0) = 200
	expectedPrice := decimal.NewFromFloat(200.0)
	for i, p := range points {
		assert.True(t, p.Value.Equal(expectedPrice),
			"point %d: expected %s, got %s", i, expectedPrice, p.Value)
		assert.Equal(t, "peak", p.Metadata["tier"])
		assert.Equal(t, "capacity_pricing", p.Metadata["algorithm"])
	}
}

func TestCapacityPricing_StandardTier(t *testing.T) {
	now := baseTime()

	// avg=60 with capacity=100 -> ratio=0.6 -> standard tier
	observations := make([]starlark.Observation, 10)
	for i := 0; i < 10; i++ {
		observations[i] = starlark.Observation{
			Timestamp: now.Add(-time.Duration(10-i) * time.Hour),
			Value:     decimal.NewFromInt(60),
			Quality:   "ACTUAL",
		}
	}

	mis := &mockMISClient{
		observations: map[string][]starlark.Observation{
			"UTILIZATION": observations,
		},
	}
	ref := &mockRefDataClient{
		nodes: map[string]*starlark.ReferenceData{
			"zone-key": {
				NodeType:      "zone",
				ResolutionKey: "zone-key",
				Attributes: map[string]any{
					"capacity": "100",
				},
			},
		},
	}
	runner := newTestRunner(t, mis, ref)

	script, err := Load(CapacityPricing)
	require.NoError(t, err)

	points, err := runner.ExecuteStrategy(context.Background(), starlark.StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION"},
		OutputDatasetCode: "FORECAST_PRICE",
		ResolutionKey:     "zone-key",
		TenantID:          "tenant-1",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// 60/100 = 0.6 -> standard tier -> 100 * 1.0 = 100
	expectedPrice := decimal.NewFromFloat(100.0)
	for _, p := range points {
		assert.True(t, p.Value.Equal(expectedPrice),
			"expected %s, got %s", expectedPrice, p.Value)
		assert.Equal(t, "standard", p.Metadata["tier"])
	}
}

func TestCapacityPricing_OffPeakTier(t *testing.T) {
	now := baseTime()

	// avg=30 with capacity=100 -> ratio=0.3 -> off_peak tier
	observations := make([]starlark.Observation, 10)
	for i := 0; i < 10; i++ {
		observations[i] = starlark.Observation{
			Timestamp: now.Add(-time.Duration(10-i) * time.Hour),
			Value:     decimal.NewFromInt(30),
			Quality:   "ACTUAL",
		}
	}

	mis := &mockMISClient{
		observations: map[string][]starlark.Observation{
			"UTILIZATION": observations,
		},
	}
	ref := &mockRefDataClient{
		nodes: map[string]*starlark.ReferenceData{
			"zone-key": {
				NodeType:      "zone",
				ResolutionKey: "zone-key",
				Attributes: map[string]any{
					"capacity": "100",
				},
			},
		},
	}
	runner := newTestRunner(t, mis, ref)

	script, err := Load(CapacityPricing)
	require.NoError(t, err)

	points, err := runner.ExecuteStrategy(context.Background(), starlark.StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION"},
		OutputDatasetCode: "FORECAST_PRICE",
		ResolutionKey:     "zone-key",
		TenantID:          "tenant-1",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// 30/100 = 0.3 -> off_peak tier -> 100 * 0.5 = 50
	expectedPrice := decimal.NewFromFloat(50.0)
	for _, p := range points {
		assert.True(t, p.Value.Equal(expectedPrice),
			"expected %s, got %s", expectedPrice, p.Value)
		assert.Equal(t, "off_peak", p.Metadata["tier"])
	}
}

// --- External blend template tests ---

func TestExternalBlend_MixedSources(t *testing.T) {
	now := baseTime()

	// Internal observations average to 100
	internalObs := make([]starlark.Observation, 10)
	for i := 0; i < 10; i++ {
		internalObs[i] = starlark.Observation{
			Timestamp: now.Add(-time.Duration(10-i) * time.Hour),
			Value:     decimal.NewFromInt(100),
			Quality:   "ACTUAL",
		}
	}

	// External observations average to 200
	externalObs := make([]starlark.Observation, 10)
	for i := 0; i < 10; i++ {
		externalObs[i] = starlark.Observation{
			Timestamp: now.Add(-time.Duration(10-i) * time.Hour),
			Value:     decimal.NewFromInt(200),
			Quality:   "ACTUAL",
		}
	}

	// First alphabetically = internal ("A_INTERNAL"), second = external ("B_EXTERNAL")
	mis := &mockMISClient{
		observations: map[string][]starlark.Observation{
			"A_INTERNAL": internalObs,
			"B_EXTERNAL": externalObs,
		},
	}
	ref := &mockRefDataClient{nodes: map[string]*starlark.ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script, err := Load(ExternalBlend)
	require.NoError(t, err)

	points, err := runner.ExecuteStrategy(context.Background(), starlark.StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"A_INTERNAL", "B_EXTERNAL"},
		OutputDatasetCode: "FORECAST_BLEND",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// Default: 70% internal (100) + 30% external (200) = 70 + 60 = 130
	expectedValue := decimal.NewFromFloat(130.0)
	for i, p := range points {
		assert.True(t, p.Value.Equal(expectedValue),
			"point %d: expected %s, got %s", i, expectedValue, p.Value)
		assert.Equal(t, "external_blend", p.Metadata["algorithm"])
	}
}

func TestExternalBlend_CustomWeight(t *testing.T) {
	now := baseTime()

	internalObs := []starlark.Observation{
		{Timestamp: now.Add(-time.Hour), Value: decimal.NewFromInt(100), Quality: "ACTUAL"},
	}
	externalObs := []starlark.Observation{
		{Timestamp: now.Add(-time.Hour), Value: decimal.NewFromInt(200), Quality: "ACTUAL"},
	}

	// First alphabetically = internal ("A_INTERNAL"), second = external ("B_EXTERNAL")
	mis := &mockMISClient{
		observations: map[string][]starlark.Observation{
			"A_INTERNAL": internalObs,
			"B_EXTERNAL": externalObs,
		},
	}
	ref := &mockRefDataClient{
		nodes: map[string]*starlark.ReferenceData{
			"blend-key": {
				NodeType:      "config",
				ResolutionKey: "blend-key",
				Attributes: map[string]any{
					"internal_weight": "0.5",
				},
			},
		},
	}
	runner := newTestRunner(t, mis, ref)

	script, err := Load(ExternalBlend)
	require.NoError(t, err)

	points, err := runner.ExecuteStrategy(context.Background(), starlark.StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"A_INTERNAL", "B_EXTERNAL"},
		OutputDatasetCode: "FORECAST_BLEND",
		ResolutionKey:     "blend-key",
		TenantID:          "tenant-1",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// 50% internal (100) + 50% external (200) = 50 + 100 = 150
	expectedValue := decimal.NewFromFloat(150.0)
	for _, p := range points {
		assert.True(t, p.Value.Equal(expectedValue),
			"expected %s, got %s", expectedValue, p.Value)
	}
}

func TestExternalBlend_SingleSourceFallback(t *testing.T) {
	now := baseTime()

	mis := &mockMISClient{
		observations: map[string][]starlark.Observation{
			"UTILIZATION": {
				{Timestamp: now.Add(-time.Hour), Value: decimal.NewFromInt(75), Quality: "ACTUAL"},
			},
		},
	}
	ref := &mockRefDataClient{nodes: map[string]*starlark.ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script, err := Load(ExternalBlend)
	require.NoError(t, err)

	points, err := runner.ExecuteStrategy(context.Background(), starlark.StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{"UTILIZATION"},
		OutputDatasetCode: "FORECAST",
		HorizonHours:      24,
		GranularityHours:  1,
		Now:               now,
	})

	require.NoError(t, err)
	assert.Len(t, points, 24)

	// Single source fallback: just uses the average of the single dataset
	for _, p := range points {
		assert.True(t, p.Value.Equal(decimal.NewFromInt(75)),
			"expected 75, got %s", p.Value)
	}
}
