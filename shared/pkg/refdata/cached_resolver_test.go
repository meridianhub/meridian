package refdata

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSource is a mock DataSource for testing.
type testSource struct {
	instruments map[string]InstrumentProperties
	fetchCount  atomic.Int64
	listCount   atomic.Int64
}

func newTestSource(instruments ...InstrumentProperties) *testSource {
	m := make(map[string]InstrumentProperties)
	for _, inst := range instruments {
		m[inst.Code] = inst
	}
	return &testSource{instruments: m}
}

func (s *testSource) FetchInstrument(_ context.Context, code string) (InstrumentProperties, error) {
	s.fetchCount.Add(1)
	props, ok := s.instruments[code]
	if !ok {
		return InstrumentProperties{}, ErrUnknownInstrument
	}
	return props, nil
}

func (s *testSource) FetchAllActive(_ context.Context) ([]InstrumentProperties, error) {
	s.listCount.Add(1)
	result := make([]InstrumentProperties, 0, len(s.instruments))
	for _, props := range s.instruments {
		result = append(result, props)
	}
	return result, nil
}

var (
	usd = InstrumentProperties{Code: "USD", Dimension: "MONETARY", Precision: 2, RoundingMode: "HALF_EVEN"}
	kwh = InstrumentProperties{Code: "KWH", Dimension: "ENERGY", Precision: 4, RoundingMode: "HALF_UP"}
)

func TestCachedResolver_Resolve_CacheMiss(t *testing.T) {
	source := newTestSource(usd)
	resolver := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})

	props, err := resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, "USD", props.Code)
	assert.Equal(t, "MONETARY", props.Dimension)
	assert.Equal(t, 2, props.Precision)
	assert.Equal(t, int64(1), source.fetchCount.Load())
	assert.Equal(t, int64(0), resolver.Metrics.Hits.Load())
	assert.Equal(t, int64(1), resolver.Metrics.Misses.Load())
}

func TestCachedResolver_Resolve_CacheHit(t *testing.T) {
	source := newTestSource(usd)
	resolver := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})

	// First call populates cache
	_, err := resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)

	// Second call hits cache
	props, err := resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, "USD", props.Code)
	assert.Equal(t, int64(1), source.fetchCount.Load())
	assert.Equal(t, int64(1), resolver.Metrics.Hits.Load())
	assert.Equal(t, int64(1), resolver.Metrics.Misses.Load())
}

func TestCachedResolver_Resolve_TTLExpiry(t *testing.T) {
	source := newTestSource(usd)
	resolver := NewCachedResolver(source, CachedResolverConfig{TTL: time.Millisecond})

	// Populate cache
	_, err := resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)

	// Wait for TTL to expire
	time.Sleep(5 * time.Millisecond)

	// Should fetch from source again
	props, err := resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, "USD", props.Code)
	assert.Equal(t, int64(2), source.fetchCount.Load())
	assert.Equal(t, int64(2), resolver.Metrics.Misses.Load())
}

func TestCachedResolver_Resolve_UnknownInstrument(t *testing.T) {
	source := newTestSource(usd)
	resolver := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})

	_, err := resolver.Resolve(context.Background(), "UNKNOWN")
	assert.ErrorIs(t, err, ErrUnknownInstrument)
}

func TestCachedResolver_Preload(t *testing.T) {
	source := newTestSource(usd, kwh)
	resolver := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})

	err := resolver.Preload(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), source.listCount.Load())

	// Both instruments should be cached
	props, err := resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, "USD", props.Code)

	props, err = resolver.Resolve(context.Background(), "KWH")
	require.NoError(t, err)
	assert.Equal(t, "KWH", props.Code)

	// No individual fetches should have been made
	assert.Equal(t, int64(0), source.fetchCount.Load())
	assert.Equal(t, int64(2), resolver.Metrics.Hits.Load())
}

func TestCachedResolver_Invalidate(t *testing.T) {
	source := newTestSource(usd)
	resolver := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})

	// Populate cache
	_, err := resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, int64(1), source.fetchCount.Load())

	// Invalidate
	resolver.Invalidate("USD")

	// Next resolve should fetch from source
	_, err = resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, int64(2), source.fetchCount.Load())
}

func TestCachedResolver_InvalidateAll(t *testing.T) {
	source := newTestSource(usd, kwh)
	resolver := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})

	// Populate cache
	err := resolver.Preload(context.Background())
	require.NoError(t, err)

	// Invalidate all
	resolver.InvalidateAll()

	// Next resolves should fetch from source
	_, err = resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)
	_, err = resolver.Resolve(context.Background(), "KWH")
	require.NoError(t, err)
	assert.Equal(t, int64(2), source.fetchCount.Load())
}

func TestCachedResolver_DefaultTTL(t *testing.T) {
	source := newTestSource(usd)
	resolver := NewCachedResolver(source, CachedResolverConfig{})

	assert.Equal(t, defaultCacheTTL, resolver.ttl)
}

func TestCachedResolver_ConcurrentAccess(t *testing.T) {
	source := newTestSource(usd, kwh)
	resolver := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes := []string{"USD", "KWH"}
			for _, code := range codes {
				_, _ = resolver.Resolve(context.Background(), code)
			}
		}()
	}
	wg.Wait()

	// Verify no panics and metrics are consistent
	total := resolver.Metrics.Hits.Load() + resolver.Metrics.Misses.Load()
	assert.Equal(t, int64(200), total)
}
