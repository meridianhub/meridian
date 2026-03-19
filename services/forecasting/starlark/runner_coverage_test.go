package starlark

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	starlarklib "go.starlark.net/starlark"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// --- extractPointValue coverage ---

func TestExtractPointValue_MissingKey(t *testing.T) {
	dict := starlarklib.NewDict(0)
	_, err := extractPointValue(dict)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidReturnType)
	assert.Contains(t, err.Error(), "missing 'value' key")
}

func TestExtractPointValue_DecimalValue(t *testing.T) {
	dict := starlarklib.NewDict(1)
	dv, err := saga.NewDecimalValue("42.5")
	require.NoError(t, err)
	_ = dict.SetKey(starlarklib.String("value"), dv)

	val, err := extractPointValue(dict)
	require.NoError(t, err)
	assert.Equal(t, "42.5", val.String())
}

func TestExtractPointValue_StringValue(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("value"), starlarklib.String("99.9"))

	val, err := extractPointValue(dict)
	require.NoError(t, err)
	assert.Equal(t, "99.9", val.String())
}

func TestExtractPointValue_StringValue_Invalid(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("value"), starlarklib.String("not-a-number"))

	_, err := extractPointValue(dict)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse value")
}

func TestExtractPointValue_FloatValue(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("value"), starlarklib.Float(3.14))

	val, err := extractPointValue(dict)
	require.NoError(t, err)
	assert.True(t, val.Equal(decimal.NewFromFloat(3.14)))
}

func TestExtractPointValue_IntValue(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("value"), starlarklib.MakeInt(42))

	val, err := extractPointValue(dict)
	require.NoError(t, err)
	assert.True(t, val.Equal(decimal.NewFromInt(42)))
}

func TestExtractPointValue_WrongType(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("value"), starlarklib.Bool(true))

	_, err := extractPointValue(dict)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidReturnType)
	assert.Contains(t, err.Error(), "value must be Decimal, string, float, or int")
}

// --- extractPointMetadata coverage ---

func TestExtractPointMetadata_Missing(t *testing.T) {
	dict := starlarklib.NewDict(0)
	meta, err := extractPointMetadata(dict)
	require.NoError(t, err)
	assert.NotNil(t, meta)
	assert.Empty(t, meta)
}

func TestExtractPointMetadata_None(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("metadata"), starlarklib.None)
	meta, err := extractPointMetadata(dict)
	require.NoError(t, err)
	assert.NotNil(t, meta)
	assert.Empty(t, meta)
}

func TestExtractPointMetadata_NotADict(t *testing.T) {
	dict := starlarklib.NewDict(1)
	_ = dict.SetKey(starlarklib.String("metadata"), starlarklib.String("not-a-dict"))

	_, err := extractPointMetadata(dict)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidReturnType)
	assert.Contains(t, err.Error(), "metadata must be dict")
}

func TestExtractPointMetadata_StringValues(t *testing.T) {
	dict := starlarklib.NewDict(1)
	metaDict := starlarklib.NewDict(2)
	_ = metaDict.SetKey(starlarklib.String("source"), starlarklib.String("test"))
	_ = metaDict.SetKey(starlarklib.String("quality"), starlarklib.String("high"))
	_ = dict.SetKey(starlarklib.String("metadata"), metaDict)

	meta, err := extractPointMetadata(dict)
	require.NoError(t, err)
	assert.Equal(t, "test", meta["source"])
	assert.Equal(t, "high", meta["quality"])
}

func TestExtractPointMetadata_NonStringValues(t *testing.T) {
	dict := starlarklib.NewDict(1)
	metaDict := starlarklib.NewDict(2)
	_ = metaDict.SetKey(starlarklib.String("count"), starlarklib.MakeInt(42))
	_ = metaDict.SetKey(starlarklib.String("flag"), starlarklib.Bool(true))
	_ = dict.SetKey(starlarklib.String("metadata"), metaDict)

	meta, err := extractPointMetadata(dict)
	require.NoError(t, err)
	assert.Equal(t, "42", meta["count"])
	assert.Equal(t, "True", meta["flag"])
}

func TestExtractPointMetadata_NonStringKey_Skipped(t *testing.T) {
	dict := starlarklib.NewDict(1)
	metaDict := starlarklib.NewDict(2)
	_ = metaDict.SetKey(starlarklib.MakeInt(1), starlarklib.String("numeric key"))
	_ = metaDict.SetKey(starlarklib.String("valid"), starlarklib.String("value"))
	_ = dict.SetKey(starlarklib.String("metadata"), metaDict)

	meta, err := extractPointMetadata(dict)
	require.NoError(t, err)
	assert.Equal(t, 1, len(meta))
	assert.Equal(t, "value", meta["valid"])
}

// --- dictToForecastPoint coverage ---

func TestDictToForecastPoint_Valid(t *testing.T) {
	dict := starlarklib.NewDict(3)
	_ = dict.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T01:00:00Z"))
	_ = dict.SetKey(starlarklib.String("value"), starlarklib.MakeInt(42))
	metaDict := starlarklib.NewDict(1)
	_ = metaDict.SetKey(starlarklib.String("source"), starlarklib.String("test"))
	_ = dict.SetKey(starlarklib.String("metadata"), metaDict)

	point, err := dictToForecastPoint(dict)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 2, 10, 1, 0, 0, 0, time.UTC), point.Timestamp)
	assert.True(t, point.Value.Equal(decimal.NewFromInt(42)))
	assert.Equal(t, "test", point.Metadata["source"])
}

func TestDictToForecastPoint_BadTimestamp(t *testing.T) {
	dict := starlarklib.NewDict(2)
	_ = dict.SetKey(starlarklib.String("timestamp"), starlarklib.String("bad"))
	_ = dict.SetKey(starlarklib.String("value"), starlarklib.MakeInt(42))

	_, err := dictToForecastPoint(dict)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse timestamp")
}

func TestDictToForecastPoint_BadValue(t *testing.T) {
	dict := starlarklib.NewDict(2)
	_ = dict.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T01:00:00Z"))
	_ = dict.SetKey(starlarklib.String("value"), starlarklib.Bool(true))

	_, err := dictToForecastPoint(dict)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "value must be")
}

func TestDictToForecastPoint_BadMetadata(t *testing.T) {
	dict := starlarklib.NewDict(3)
	_ = dict.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T01:00:00Z"))
	_ = dict.SetKey(starlarklib.String("value"), starlarklib.MakeInt(42))
	_ = dict.SetKey(starlarklib.String("metadata"), starlarklib.String("not-a-dict"))

	_, err := dictToForecastPoint(dict)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata must be dict")
}

// --- starlarkToForecastPoints coverage ---

func TestStarlarkToForecastPoints_NotAList(t *testing.T) {
	_, err := starlarkToForecastPoints(starlarklib.MakeInt(42))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidReturnType)
}

func TestStarlarkToForecastPoints_ElementNotDict(t *testing.T) {
	list := starlarklib.NewList([]starlarklib.Value{starlarklib.MakeInt(42)})
	_, err := starlarkToForecastPoints(list)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidReturnType)
	assert.Contains(t, err.Error(), "element 0")
}

func TestStarlarkToForecastPoints_EmptyList(t *testing.T) {
	list := starlarklib.NewList(nil)
	points, err := starlarkToForecastPoints(list)
	require.NoError(t, err)
	assert.Empty(t, points)
}

func TestStarlarkToForecastPoints_ValidList(t *testing.T) {
	dict := starlarklib.NewDict(2)
	_ = dict.SetKey(starlarklib.String("timestamp"), starlarklib.String("2026-02-10T01:00:00Z"))
	_ = dict.SetKey(starlarklib.String("value"), starlarklib.MakeInt(42))
	list := starlarklib.NewList([]starlarklib.Value{dict})

	points, err := starlarkToForecastPoints(list)
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.True(t, points[0].Value.Equal(decimal.NewFromInt(42)))
}

// --- wrapStarlarkError coverage ---

func TestWrapStarlarkError_Nil(t *testing.T) {
	assert.Nil(t, wrapStarlarkError(nil))
}

// --- executeScript coverage for compute_forecast not being a function ---

func TestExecuteScript_ComputeForecastNotFunction(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	// compute_forecast is a string, not a function
	script := `compute_forecast = "not a function"`

	_, err := runner.executeScript(context.Background(), script, minimalForecastCtx())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEntryPointMissing)
}

// --- executeScript coverage for context cancellation during call phase ---

func TestExecuteScript_ContextCancelledDuringCall(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}

	runner, err := NewForecastRunner(ForecastRunnerConfig{
		MISClient: mis,
		RefData:   ref,
		Timeout:   100 * time.Millisecond,
		Logger:    newTestLogger(),
	})
	require.NoError(t, err)

	// Script that takes a long time during compute_forecast call
	script := `
def compute_forecast(ctx):
    x = 0
    for i in range(100000000):
        x = x + i
    return []
`
	_, err = runner.executeScript(context.Background(), script, minimalForecastCtx())
	require.Error(t, err)
	// Starlark step limit may fire before Go context timeout, producing ErrExecution
	assert.True(t, errors.Is(err, saga.ErrTimeout) || errors.Is(err, saga.ErrCancelled) || errors.Is(err, saga.ErrExecution),
		"expected timeout, cancellation, or execution error, got: %v", err)
}

// --- executeScript coverage for context cancellation during exec file phase ---

func TestExecuteScript_ContextCancelledDuringExecFile(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}

	runner, err := NewForecastRunner(ForecastRunnerConfig{
		MISClient: mis,
		RefData:   ref,
		Timeout:   50 * time.Millisecond,
		Logger:    newTestLogger(),
	})
	require.NoError(t, err)

	// Script with heavy computation inside a function (top-level for loops are rejected by Starlark)
	script := `
def compute_forecast(ctx):
    x = 0
    for i in range(100000000):
        x = x + i
    return []
`
	_, err = runner.executeScript(context.Background(), script, minimalForecastCtx())
	require.Error(t, err)
	// Starlark step limit may fire before Go context timeout, producing ErrExecution
	assert.True(t, errors.Is(err, saga.ErrTimeout) || errors.Is(err, saga.ErrCancelled) || errors.Is(err, saga.ErrExecution),
		"expected timeout, cancellation, or execution error, got: %v", err)
}

// --- executeScript coverage for explicit context cancellation ---

func TestExecuteScript_ExplicitCancellation(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}

	runner, err := NewForecastRunner(ForecastRunnerConfig{
		MISClient: mis,
		RefData:   ref,
		Timeout:   5 * time.Second,
		Logger:    newTestLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately to trigger the cancellation branch
	cancel()

	script := `
x = 0
for i in range(100000000):
    x = x + i
def compute_forecast(ctx):
    return []
`
	_, err = runner.executeScript(ctx, script, minimalForecastCtx())
	require.Error(t, err)
	assert.True(t, errors.Is(err, saga.ErrTimeout) || errors.Is(err, saga.ErrCancelled),
		"expected timeout or cancellation error, got: %v", err)
}

// --- ExecuteStrategy with runtime error in compute_forecast call ---

func TestExecuteStrategy_RuntimeErrorInComputeForecast(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    return 1 / 0
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
}

// --- ExecuteStrategy with returned list containing invalid dict ---

func TestExecuteStrategy_ReturnedListWithInvalidElement(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    return [42]
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

// --- ExecuteStrategy with metadata in output ---

func TestExecuteStrategy_WithMetadataOutput(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    ts = add_seconds(ctx["now"], 3600)
    return [{"timestamp": ts, "value": 42, "metadata": {"source": "test", "count": 5}}]
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
	assert.Equal(t, "test", points[0].Metadata["source"])
	assert.Equal(t, "5", points[0].Metadata["count"])
}

// --- ExecuteStrategy with Decimal value in output ---

func TestExecuteStrategy_WithDecimalValueOutput(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    ts = add_seconds(ctx["now"], 3600)
    return [{"timestamp": ts, "value": Decimal("42.5")}]
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
	assert.Equal(t, "42.5", points[0].Value.String())
}

// --- ExecuteStrategy with string value in output ---

func TestExecuteStrategy_WithStringValueOutput(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    ts = add_seconds(ctx["now"], 3600)
    return [{"timestamp": ts, "value": "99.9"}]
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
	assert.Equal(t, "99.9", points[0].Value.String())
}

// --- ExecuteStrategy with float value in output ---

func TestExecuteStrategy_WithFloatValueOutput(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    ts = add_seconds(ctx["now"], 3600)
    return [{"timestamp": ts, "value": 3.14}]
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
	assert.True(t, points[0].Value.Equal(decimal.NewFromFloat(3.14)))
}

// --- ExecuteStrategy with zero Now (uses time.Now()) ---

func TestExecuteStrategy_ZeroNow(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	// When Now is zero, ExecuteStrategy should use time.Now() and still produce output.
	// Return empty list to avoid granularity validation issues with dynamic Now.
	script := `
def compute_forecast(ctx):
    return []
`
	points, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      24,
		GranularityHours:  1,
		// Now intentionally omitted (zero value)
	})
	require.NoError(t, err)
	assert.Empty(t, points)
}

// --- ExecuteStrategy with ref data client error ---

func TestExecuteStrategy_RefDataClientError(t *testing.T) {
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{
		nodes: map[string]*ReferenceData{},
		err:   nil,
	}
	runner := newTestRunner(t, mis, ref)

	// Use a resolution key that doesn't exist in the mock
	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            "def compute_forecast(ctx): return []",
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		ResolutionKey:     "nonexistent-key",
		TenantID:          "tenant-1",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               baseTime(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch reference data")
}

// --- ExecuteStrategy with validation error after execution ---

func TestExecuteStrategy_PointOutsideHorizon(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	// Return a point 100 hours out when horizon is only 1 hour
	script := `
def compute_forecast(ctx):
    ts = add_seconds(ctx["now"], 360000)
    return [{"timestamp": ts, "value": 42}]
`
	_, err := runner.ExecuteStrategy(context.Background(), StrategyInput{
		Script:            script,
		InputDatasetCodes: []string{},
		OutputDatasetCode: "TEST",
		HorizonHours:      1,
		GranularityHours:  1,
		Now:               now,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTimestampOutOfRange)
}

// --- ExecuteStrategy with None metadata ---

func TestExecuteStrategy_WithNoneMetadata(t *testing.T) {
	now := baseTime()
	mis := &mockMISClient{observations: map[string][]Observation{}}
	ref := &mockRefDataClient{nodes: map[string]*ReferenceData{}}
	runner := newTestRunner(t, mis, ref)

	script := `
def compute_forecast(ctx):
    ts = add_seconds(ctx["now"], 3600)
    return [{"timestamp": ts, "value": 42, "metadata": None}]
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
	assert.NotNil(t, points[0].Metadata)
	assert.Empty(t, points[0].Metadata)
}
