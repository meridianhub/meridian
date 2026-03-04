package cel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gatewaycache "github.com/meridianhub/meridian/services/api-gateway/cache"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// stubSource implements cache.Source for testing.
type stubSource struct {
	obs        map[string]*gatewaycache.Observation
	rangeObs   map[string][]*gatewaycache.Observation
	calls      int64
	rangeCalls int64
	err        error
}

func newStubSource() *stubSource {
	return &stubSource{
		obs:      make(map[string]*gatewaycache.Observation),
		rangeObs: make(map[string][]*gatewaycache.Observation),
	}
}

func (s *stubSource) GetForwardPrice(_ context.Context, resolutionKey string, ts time.Time) (*gatewaycache.Observation, error) {
	atomic.AddInt64(&s.calls, 1)
	if s.err != nil {
		return nil, s.err
	}
	key := resolutionKey + ":" + ts.Truncate(time.Hour).Format(time.RFC3339)
	obs, ok := s.obs[key]
	if !ok {
		return nil, gatewaycache.ErrObservationNotFound
	}
	return obs, nil
}

func (s *stubSource) GetForwardPriceRange(_ context.Context, resolutionKey string, start, end time.Time) ([]*gatewaycache.Observation, error) {
	atomic.AddInt64(&s.rangeCalls, 1)
	if s.err != nil {
		return nil, s.err
	}
	key := resolutionKey + ":" + start.Format(time.RFC3339) + "-" + end.Format(time.RFC3339)
	obs, ok := s.rangeObs[key]
	if !ok {
		return nil, nil
	}
	return obs, nil
}

func (s *stubSource) addObs(resolutionKey string, ts time.Time, value string) {
	hour := ts.Truncate(time.Hour)
	key := resolutionKey + ":" + hour.Format(time.RFC3339)
	s.obs[key] = &gatewaycache.Observation{
		Value:       decimal.RequireFromString(value),
		Unit:        "GBP/kWh",
		Quality:     "ESTIMATE",
		ObservedAt:  time.Date(2026, 1, 14, 8, 0, 0, 0, time.UTC),
		ValidFrom:   hour,
		ValidTo:     hour.Add(time.Hour),
		DataSetCode: "ELEC_FORWARD",
		SourceID:    "test-source-id",
		Metadata: map[string]string{
			"region": "UK",
		},
	}
}

func tenantCtx(t *testing.T) context.Context {
	t.Helper()
	return tenant.WithTenant(context.Background(), "test-tenant")
}

func TestForwardPrice_Compile_Evaluate(t *testing.T) {
	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	source.addObs("ELEC_PEAK", ts, "45.50")

	cache, err := gatewaycache.NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)
	env, err := NewForwardCurveEnv(ctx, cache)
	require.NoError(t, err)

	ast, issues := env.Compile(`forward_price("ELEC_PEAK", timestamp)`)
	require.Nil(t, issues, "compilation failed: %v", issues)

	prg, err := env.Program(ast)
	require.NoError(t, err)

	out, _, err := prg.Eval(map[string]interface{}{
		"timestamp": ts,
	})
	require.NoError(t, err)

	assert.InDelta(t, 45.50, out.Value().(float64), 0.001)
}

func TestForwardPrice_NonExistentResolutionKey(t *testing.T) {
	source := newStubSource()

	cache, err := gatewaycache.NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)
	env, err := NewForwardCurveEnv(ctx, cache)
	require.NoError(t, err)

	ast, issues := env.Compile(`forward_price("NONEXISTENT", timestamp)`)
	require.Nil(t, issues)

	prg, err := env.Program(ast)
	require.NoError(t, err)

	_, _, err = prg.Eval(map[string]interface{}{
		"timestamp": time.Now(),
	})
	// CEL wraps errors in ref.Val, so Eval returns error for forward_price errors
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forward_price")
}

func TestForwardMetadata(t *testing.T) {
	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	source.addObs("ELEC_PEAK", ts, "45.50")

	cache, err := gatewaycache.NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)
	env, err := NewForwardCurveEnv(ctx, cache)
	require.NoError(t, err)

	ast, issues := env.Compile(`forward_metadata("ELEC_PEAK", timestamp)`)
	require.Nil(t, issues)

	prg, err := env.Program(ast)
	require.NoError(t, err)

	out, _, err := prg.Eval(map[string]interface{}{
		"timestamp": ts,
	})
	require.NoError(t, err)

	// Result should be a map
	result := out.Value().(map[string]string)
	assert.Equal(t, "GBP/kWh", result["unit"])
	assert.Equal(t, "ESTIMATE", result["quality"])
	assert.Equal(t, "ELEC_FORWARD", result["dataset_code"])
	assert.Equal(t, "test-source-id", result["source_id"])
	assert.Equal(t, "UK", result["region"])
}

func TestAvgForwardPrice_24hWindow(t *testing.T) {
	source := newStubSource()
	start := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)

	// Create 24 hourly observations (values from 40.0 to 63.0)
	var rangeObs []*gatewaycache.Observation
	for i := 0; i < 24; i++ {
		hour := start.Add(time.Duration(i) * time.Hour)
		value := decimal.NewFromFloat(40.0 + float64(i))
		obs := &gatewaycache.Observation{
			Value:       value,
			Unit:        "GBP/kWh",
			Quality:     "ESTIMATE",
			ObservedAt:  time.Date(2026, 1, 14, 8, 0, 0, 0, time.UTC),
			ValidFrom:   hour,
			ValidTo:     hour.Add(time.Hour),
			DataSetCode: "ELEC_FORWARD",
			SourceID:    "test-source-id",
		}
		rangeObs = append(rangeObs, obs)

		// Also add individually for L1 cache
		key := "ELEC_PEAK:" + hour.Format(time.RFC3339)
		source.obs[key] = obs
	}

	// Register range obs
	rangeKey := "ELEC_PEAK:" + start.Format(time.RFC3339) + "-" + end.Add(time.Hour).Format(time.RFC3339)
	source.rangeObs[rangeKey] = rangeObs

	cache, err := gatewaycache.NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)
	env, err := cel.NewEnv(
		cel.Variable("start_time", cel.TimestampType),
		cel.Variable("end_time", cel.TimestampType),
		ForwardCurveLib(ctx, cache),
	)
	require.NoError(t, err)

	ast, issues := env.Compile(`avg_forward_price("ELEC_PEAK", start_time, end_time)`)
	require.Nil(t, issues)

	prg, err := env.Program(ast)
	require.NoError(t, err)

	out, _, err := prg.Eval(map[string]interface{}{
		"start_time": start,
		"end_time":   end,
	})
	require.NoError(t, err)

	// Average of 40..63 = (40+63)/2 = 51.5
	assert.InDelta(t, 51.5, out.Value().(float64), 0.001)
}

func TestAvgForwardPrice_StartAfterEnd(t *testing.T) {
	source := newStubSource()

	cache, err := gatewaycache.NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)
	env, err := cel.NewEnv(
		cel.Variable("start_time", cel.TimestampType),
		cel.Variable("end_time", cel.TimestampType),
		ForwardCurveLib(ctx, cache),
	)
	require.NoError(t, err)

	ast, issues := env.Compile(`avg_forward_price("ELEC_PEAK", start_time, end_time)`)
	require.Nil(t, issues)

	prg, err := env.Program(ast)
	require.NoError(t, err)

	end := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)

	_, _, err = prg.Eval(map[string]interface{}{
		"start_time": start,
		"end_time":   end,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "start must be before end")
}

func TestForwardCurveLib_LibraryName(t *testing.T) {
	lib := &ForwardCurveLibrary{}
	assert.Equal(t, "meridian.ForwardCurve", lib.LibraryName())
}

func TestForwardCurveLib_ProgramOptions(t *testing.T) {
	lib := &ForwardCurveLibrary{}
	assert.Nil(t, lib.ProgramOptions())
}

func TestSetContext_UpdatesTenantContext(t *testing.T) {
	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	source.addObs("ELEC_PEAK", ts, "45.50")

	cache, err := gatewaycache.NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	// Create library with initial context
	initialCtx := tenantCtx(t)
	lib := NewForwardCurveLibrary(initialCtx, cache)

	env, err := cel.NewEnv(
		cel.Variable("timestamp", cel.TimestampType),
		lib.EnvOption(),
	)
	require.NoError(t, err)

	ast, issues := env.Compile(`forward_price("ELEC_PEAK", timestamp)`)
	require.Nil(t, issues)

	prg, err := env.Program(ast)
	require.NoError(t, err)

	// Evaluate with initial context
	out, _, err := prg.Eval(map[string]interface{}{
		"timestamp": ts,
	})
	require.NoError(t, err)
	assert.InDelta(t, 45.50, out.Value().(float64), 0.001)

	// Update context to a different tenant and reuse the same program
	newCtx := tenant.WithTenant(context.Background(), "other-tenant")
	lib.SetContext(newCtx)

	// Same program, different tenant context - should still work
	out, _, err = prg.Eval(map[string]interface{}{
		"timestamp": ts,
	})
	require.NoError(t, err)
	assert.InDelta(t, 45.50, out.Value().(float64), 0.001)
}

func TestForwardPrice_WithMockedCache(t *testing.T) {
	source := newStubSource()
	ts := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)

	// Add observations at different price points
	source.addObs("GAS_FORWARD", ts, "2.75")
	source.addObs("ELEC_PEAK", ts, "85.00")

	cache, err := gatewaycache.NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)
	env, err := cel.NewEnv(
		cel.Variable("timestamp", cel.TimestampType),
		cel.Variable("quantity", cel.DoubleType),
		ForwardCurveLib(ctx, cache),
	)
	require.NoError(t, err)

	// Test a pricing rule: quantity * forward_price
	ast, issues := env.Compile(`quantity * forward_price("ELEC_PEAK", timestamp)`)
	require.Nil(t, issues)

	prg, err := env.Program(ast)
	require.NoError(t, err)

	out, _, err := prg.Eval(map[string]interface{}{
		"timestamp": ts,
		"quantity":  100.0,
	})
	require.NoError(t, err)

	// 100 * 85.00 = 8500.00
	assert.InDelta(t, 8500.0, out.Value().(float64), 0.001)
}

func TestNewForwardCurveEnv(t *testing.T) {
	source := newStubSource()
	cache, err := gatewaycache.NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)
	env, err := NewForwardCurveEnv(ctx, cache)
	require.NoError(t, err)

	// Verify environment has standard pricing variables
	ast, issues := env.Compile(`amount + quantity`)
	require.Nil(t, issues)

	prg, err := env.Program(ast)
	require.NoError(t, err)

	out, _, err := prg.Eval(map[string]interface{}{
		"amount":     10.0,
		"quantity":   5.0,
		"unit":       "kWh",
		"timestamp":  time.Now(),
		"attributes": map[string]string{},
	})
	require.NoError(t, err)
	assert.InDelta(t, 15.0, out.Value().(float64), 0.001)
}

func TestForwardMetadata_NonExistent(t *testing.T) {
	source := newStubSource()

	cache, err := gatewaycache.NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)
	env, err := NewForwardCurveEnv(ctx, cache)
	require.NoError(t, err)

	ast, issues := env.Compile(`forward_metadata("NONEXISTENT", timestamp)`)
	require.Nil(t, issues)

	prg, err := env.Program(ast)
	require.NoError(t, err)

	_, _, err = prg.Eval(map[string]interface{}{
		"timestamp": time.Now(),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forward_metadata")
}
