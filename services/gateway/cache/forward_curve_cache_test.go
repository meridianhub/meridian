package cache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// stubSource implements Source for testing.
type stubSource struct {
	obs        map[string]*Observation
	rangeObs   map[string][]*Observation
	calls      int64
	rangeCalls int64
	err        error
}

func newStubSource() *stubSource {
	return &stubSource{
		obs:      make(map[string]*Observation),
		rangeObs: make(map[string][]*Observation),
	}
}

func (s *stubSource) GetForwardPrice(_ context.Context, resolutionKey string, ts time.Time) (*Observation, error) {
	atomic.AddInt64(&s.calls, 1)
	if s.err != nil {
		return nil, s.err
	}
	key := resolutionKey + ":" + ts.Truncate(time.Hour).Format(time.RFC3339)
	obs, ok := s.obs[key]
	if !ok {
		return nil, ErrObservationNotFound
	}
	return obs, nil
}

func (s *stubSource) GetForwardPriceRange(_ context.Context, resolutionKey string, start, end time.Time) ([]*Observation, error) {
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
	s.obs[key] = &Observation{
		Value:       decimal.RequireFromString(value),
		Unit:        "GBP/kWh",
		Quality:     "ESTIMATE",
		ObservedAt:  time.Now(),
		ValidFrom:   hour,
		ValidTo:     hour.Add(time.Hour),
		DataSetCode: "ELEC_FORWARD",
		SourceID:    "test-source",
	}
}

func tenantCtx(t *testing.T) context.Context {
	t.Helper()
	return tenant.WithTenant(context.Background(), "test-tenant")
}

func TestForwardCurveCache_L1Hit(t *testing.T) {
	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	source.addObs("ELEC_PEAK", ts, "45.50")

	cache, err := NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)

	// First call: L3 miss -> populates L1
	obs1, err := cache.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, "45.5", obs1.Value.String())
	assert.Equal(t, int64(1), atomic.LoadInt64(&source.calls))

	// Second call: L1 hit -> no source call
	obs2, err := cache.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, "45.5", obs2.Value.String())
	assert.Equal(t, int64(1), atomic.LoadInt64(&source.calls))

	stats := cache.Stats()
	assert.Equal(t, int64(1), stats.L1Hits)
	assert.Equal(t, int64(1), stats.SourceLoads)
}

func TestForwardCurveCache_L2Hit(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	source.addObs("ELEC_PEAK", ts, "45.50")

	cache1, err := NewForwardCurveCache(source, rdb, WithL1TTL(50*time.Millisecond, 0))
	require.NoError(t, err)

	ctx := tenantCtx(t)

	// First call: L3 -> L2 + L1
	obs, err := cache1.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, "45.5", obs.Value.String())

	// Wait for L1 to expire
	time.Sleep(60 * time.Millisecond)

	// Second call: L2 hit (L1 expired)
	obs2, err := cache1.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, "45.5", obs2.Value.String())

	// Source should only be called once
	assert.Equal(t, int64(1), atomic.LoadInt64(&source.calls))

	stats := cache1.Stats()
	assert.Equal(t, int64(1), stats.L2Hits)
}

func TestForwardCurveCache_L3Miss(t *testing.T) {
	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	cache, err := NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)

	// No observation in source -> ErrObservationNotFound
	_, err = cache.Get(ctx, "NONEXISTENT", ts)
	assert.ErrorIs(t, err, ErrObservationNotFound)
}

func TestForwardCurveCache_TenantContextRequired(t *testing.T) {
	source := newStubSource()
	cache, err := NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	// No tenant in context
	_, err = cache.Get(context.Background(), "ELEC_PEAK", time.Now())
	assert.ErrorIs(t, err, ErrTenantContextRequired)
}

func TestForwardCurveCache_L1TTLExpiry(t *testing.T) {
	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	source.addObs("ELEC_PEAK", ts, "45.50")

	// Short TTL for testing
	cache, err := NewForwardCurveCache(source, nil, WithL1TTL(50*time.Millisecond, 0))
	require.NoError(t, err)

	ctx := tenantCtx(t)

	// First call -> populates L1
	_, err = cache.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&source.calls))

	// Wait for TTL expiry
	time.Sleep(60 * time.Millisecond)

	// Should re-query source after TTL expiry
	_, err = cache.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, int64(2), atomic.LoadInt64(&source.calls))
}

func TestForwardCurveCache_RedisFailureGraceful(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	source.addObs("ELEC_PEAK", ts, "45.50")

	cache, err := NewForwardCurveCache(source, rdb, WithL1TTL(50*time.Millisecond, 0))
	require.NoError(t, err)

	ctx := tenantCtx(t)

	// First call -> L3 + populate L2
	obs, err := cache.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, "45.5", obs.Value.String())

	// Shut down Redis
	mr.Close()

	// Wait for L1 to expire
	time.Sleep(60 * time.Millisecond)

	// Should degrade gracefully: L2 fails -> L3
	obs2, err := cache.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, "45.5", obs2.Value.String())

	// Source called twice (initial + after Redis failure)
	assert.Equal(t, int64(2), atomic.LoadInt64(&source.calls))
}

func TestForwardCurveCache_HourTruncation(t *testing.T) {
	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	source.addObs("ELEC_PEAK", ts, "45.50")

	cache, err := NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)

	// Query at 10:15 and 10:45 should both resolve to the 10:00 bucket
	obs1, err := cache.Get(ctx, "ELEC_PEAK", ts.Add(15*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, "45.5", obs1.Value.String())

	obs2, err := cache.Get(ctx, "ELEC_PEAK", ts.Add(45*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, "45.5", obs2.Value.String())

	// Only one source call for the same hour bucket
	assert.Equal(t, int64(1), atomic.LoadInt64(&source.calls))
}

func TestForwardCurveCache_Invalidate(t *testing.T) {
	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	source.addObs("ELEC_PEAK", ts, "45.50")

	cache, err := NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)

	// Populate cache
	_, err = cache.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&source.calls))

	// Invalidate
	cache.Invalidate("test-tenant", "ELEC_PEAK", ts)

	// Should re-query source
	_, err = cache.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, int64(2), atomic.LoadInt64(&source.calls))
}

func TestForwardCurveCache_SourceError(t *testing.T) {
	source := newStubSource()
	source.err = errors.New("MDS unavailable")

	cache, err := NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)

	_, err = cache.Get(ctx, "ELEC_PEAK", time.Now())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "MDS unavailable")
}

func TestForwardCurveCache_L2KeyFormat(t *testing.T) {
	source := newStubSource()
	cache, err := NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	key := cache.l2Key("tenant-1", "ELEC_PEAK", 1705312800)
	assert.Equal(t, "fwd:tenant-1:ELEC_PEAK:1705312800", key)
}

func TestForwardCurveCache_NilL2(t *testing.T) {
	source := newStubSource()
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	source.addObs("ELEC_PEAK", ts, "45.50")

	// nil L2 should work (L1 -> L3 only)
	cache, err := NewForwardCurveCache(source, nil)
	require.NoError(t, err)

	ctx := tenantCtx(t)
	obs, err := cache.Get(ctx, "ELEC_PEAK", ts)
	require.NoError(t, err)
	assert.Equal(t, "45.5", obs.Value.String())
}

func TestTruncateToHour(t *testing.T) {
	ts := time.Date(2026, 1, 15, 10, 45, 30, 123, time.UTC)
	expected := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, expected, truncateToHour(ts))
}
