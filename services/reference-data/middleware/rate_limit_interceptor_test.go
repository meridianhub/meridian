package middleware

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testHandler is a simple gRPC handler for testing.
func testHandler(_ context.Context, _ any) (any, error) {
	return "ok", nil
}

// testRegistry creates an isolated Prometheus registry for testing.
func testRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}

func TestRateLimitInterceptor_AllowsUpToBurstSize(t *testing.T) {
	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	config := RateLimitInterceptorConfig{
		BurstSize:       10,
		RefillRate:      1 * time.Minute, // Very slow refill so we can test burst
		CleanupInterval: 1 * time.Hour,   // Don't cleanup during test
		IdleTimeout:     1 * time.Hour,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := testdb.ContextWithTenant(t, "test_tenant")
	info := &grpc.UnaryServerInfo{FullMethod: RegisterInstrumentMethod}
	handler := interceptor.UnaryServerInterceptor()

	// Should allow exactly 10 requests
	for i := 0; i < 10; i++ {
		resp, err := handler(ctx, nil, info, testHandler)
		require.NoError(t, err, "request %d should succeed", i+1)
		assert.Equal(t, "ok", resp)
	}

	// 11th request should be rate limited
	_, err := handler(ctx, nil, info, testHandler)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
	assert.Contains(t, st.Message(), "rate limit exceeded")
	assert.Contains(t, st.Message(), "test_tenant")
}

func TestRateLimitInterceptor_ReturnsResourceExhausted(t *testing.T) {
	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	config := RateLimitInterceptorConfig{
		BurstSize:  1, // Very small burst to trigger immediately
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := testdb.ContextWithTenant(t, "tenant_a")
	info := &grpc.UnaryServerInfo{FullMethod: RegisterInstrumentMethod}
	handler := interceptor.UnaryServerInterceptor()

	// First request succeeds
	_, err := handler(ctx, nil, info, testHandler)
	require.NoError(t, err)

	// Second request should fail with ResourceExhausted
	_, err = handler(ctx, nil, info, testHandler)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
}

func TestRateLimitInterceptor_BypassesReadOperations(t *testing.T) {
	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	config := RateLimitInterceptorConfig{
		BurstSize:  1, // Would trigger immediately if rate limited
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := testdb.ContextWithTenant(t, "read_tenant")
	handler := interceptor.UnaryServerInterceptor()

	// Read operations should never be rate limited
	readMethods := []string{
		"/meridian.reference_data.v1.ReferenceDataService/RetrieveInstrument",
		"/meridian.reference_data.v1.ReferenceDataService/ListInstruments",
		"/meridian.reference_data.v1.ReferenceDataService/GetAttributeSchema",
	}

	for _, method := range readMethods {
		info := &grpc.UnaryServerInfo{FullMethod: method}
		// Make many requests - none should be rate limited
		for i := 0; i < 100; i++ {
			resp, err := handler(ctx, nil, info, testHandler)
			require.NoError(t, err, "request %d to %s should succeed", i+1, method)
			assert.Equal(t, "ok", resp)
		}
	}
}

func TestRateLimitInterceptor_PerTenantIsolation(t *testing.T) {
	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	config := RateLimitInterceptorConfig{
		BurstSize:  2,
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)
	defer interceptor.Stop()

	ctxA := testdb.ContextWithTenant(t, "tenant_a")
	ctxB := testdb.ContextWithTenant(t, "tenant_b")
	info := &grpc.UnaryServerInfo{FullMethod: RegisterInstrumentMethod}
	handler := interceptor.UnaryServerInterceptor()

	// Exhaust tenant A's quota
	for i := 0; i < 2; i++ {
		_, err := handler(ctxA, nil, info, testHandler)
		require.NoError(t, err)
	}
	// Tenant A should be rate limited
	_, err := handler(ctxA, nil, info, testHandler)
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))

	// Tenant B should still have full quota
	for i := 0; i < 2; i++ {
		_, err := handler(ctxB, nil, info, testHandler)
		require.NoError(t, err, "tenant B request %d should succeed", i+1)
	}
}

func TestRateLimitInterceptor_NoTenantContext(t *testing.T) {
	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	config := RateLimitInterceptorConfig{
		BurstSize:  1,
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)
	defer interceptor.Stop()

	// Context without tenant
	ctx := context.Background()
	info := &grpc.UnaryServerInfo{FullMethod: RegisterInstrumentMethod}
	handler := interceptor.UnaryServerInterceptor()

	// Requests without tenant context should pass through
	// (will be rejected by auth layer if required)
	for i := 0; i < 10; i++ {
		resp, err := handler(ctx, nil, info, testHandler)
		require.NoError(t, err)
		assert.Equal(t, "ok", resp)
	}
}

func TestRateLimitInterceptor_TokenReplenishment(t *testing.T) {
	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	// Very fast refill for testing: 1 token every 50ms
	config := RateLimitInterceptorConfig{
		BurstSize:  2,
		RefillRate: 50 * time.Millisecond,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := testdb.ContextWithTenant(t, "replenish_tenant")
	info := &grpc.UnaryServerInfo{FullMethod: RegisterInstrumentMethod}
	handler := interceptor.UnaryServerInterceptor()

	// Use up the burst
	for i := 0; i < 2; i++ {
		_, err := handler(ctx, nil, info, testHandler)
		require.NoError(t, err)
	}

	// Should be rate limited now
	_, err := handler(ctx, nil, info, testHandler)
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))

	// Wait for token to replenish
	time.Sleep(60 * time.Millisecond) //nolint:forbidigo // triggers rate limit token replenishment

	// Should succeed again
	_, err = handler(ctx, nil, info, testHandler)
	require.NoError(t, err)
}

func TestRateLimitInterceptor_ConcurrentAccess(t *testing.T) {
	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	config := RateLimitInterceptorConfig{
		BurstSize:  10,
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := testdb.ContextWithTenant(t, "concurrent_tenant")
	info := &grpc.UnaryServerInfo{FullMethod: RegisterInstrumentMethod}
	handler := interceptor.UnaryServerInterceptor()

	// Launch 20 concurrent requests
	var wg sync.WaitGroup
	var allowed, blocked atomic.Int32

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := handler(ctx, nil, info, testHandler)
			if err == nil {
				allowed.Add(1)
			} else {
				blocked.Add(1)
			}
		}()
	}

	wg.Wait()

	// Exactly 10 should be allowed, 10 blocked
	assert.Equal(t, int32(10), allowed.Load(), "expected 10 allowed")
	assert.Equal(t, int32(10), blocked.Load(), "expected 10 blocked")
}

func TestRateLimitInterceptor_CleanupIdleLimiters(t *testing.T) {
	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	config := RateLimitInterceptorConfig{
		BurstSize:       10,
		RefillRate:      1 * time.Hour,
		CleanupInterval: 50 * time.Millisecond,
		IdleTimeout:     100 * time.Millisecond,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := testdb.ContextWithTenant(t, "cleanup_tenant")
	info := &grpc.UnaryServerInfo{FullMethod: RegisterInstrumentMethod}
	handler := interceptor.UnaryServerInterceptor()

	// Create a limiter
	_, err := handler(ctx, nil, info, testHandler)
	require.NoError(t, err)

	assert.Equal(t, 1, interceptor.ActiveLimiters())

	// Wait for cleanup to evict it
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return interceptor.ActiveLimiters() == 0
		})
	require.NoError(t, err, "idle limiter should be evicted by cleanup")

	assert.Equal(t, 0, interceptor.ActiveLimiters())
}

func TestRateLimitMetrics_Increment(t *testing.T) {
	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	config := RateLimitInterceptorConfig{
		BurstSize:  2,
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := testdb.ContextWithTenant(t, "metrics_tenant")
	info := &grpc.UnaryServerInfo{FullMethod: RegisterInstrumentMethod}
	handler := interceptor.UnaryServerInterceptor()

	// 2 allowed, 1 blocked
	handler(ctx, nil, info, testHandler)
	handler(ctx, nil, info, testHandler)
	handler(ctx, nil, info, testHandler)

	// Check allowed counter
	allowedMetric := &dto.Metric{}
	err := metrics.allowed.WithLabelValues("metrics_tenant").Write(allowedMetric)
	require.NoError(t, err)
	assert.Equal(t, float64(2), allowedMetric.Counter.GetValue())

	// Check blocked counter
	blockedMetric := &dto.Metric{}
	err = metrics.blocked.WithLabelValues("metrics_tenant").Write(blockedMetric)
	require.NoError(t, err)
	assert.Equal(t, float64(1), blockedMetric.Counter.GetValue())
}

func TestRateLimitMetrics_ActiveLimitersGauge(t *testing.T) {
	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	config := RateLimitInterceptorConfig{
		BurstSize:       10,
		RefillRate:      1 * time.Hour,
		CleanupInterval: 50 * time.Millisecond,
		IdleTimeout:     100 * time.Millisecond,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)
	defer interceptor.Stop()

	info := &grpc.UnaryServerInfo{FullMethod: RegisterInstrumentMethod}
	handler := interceptor.UnaryServerInterceptor()

	// Create limiters for 3 tenants
	for i := 0; i < 3; i++ {
		ctx := testdb.ContextWithTenant(t, "tenant_"+string(rune('a'+i)))
		handler(ctx, nil, info, testHandler)
	}

	// Check gauge
	gaugeMetric := &dto.Metric{}
	err := metrics.active.Write(gaugeMetric)
	require.NoError(t, err)
	assert.Equal(t, float64(3), gaugeMetric.Gauge.GetValue())

	// Wait for cleanup
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			m := &dto.Metric{}
			_ = metrics.active.Write(m)
			return m.Gauge.GetValue() == 0
		})
	require.NoError(t, err, "active limiters gauge should reach 0 after cleanup")

	err = metrics.active.Write(gaugeMetric)
	require.NoError(t, err)
	assert.Equal(t, float64(0), gaugeMetric.Gauge.GetValue())
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	assert.Equal(t, 10, config.BurstSize)
	assert.Equal(t, 6*time.Second, config.RefillRate)
	assert.Equal(t, 5*time.Minute, config.CleanupInterval)
	assert.Equal(t, 1*time.Hour, config.IdleTimeout)
}

func TestRateLimitInterceptor_StopIsIdempotent(t *testing.T) {
	t.Parallel()

	registry := testRegistry()
	metrics := NewRateLimitMetrics(registry)

	config := RateLimitInterceptorConfig{
		BurstSize:       10,
		RefillRate:      1 * time.Hour,
		CleanupInterval: 1 * time.Hour,
	}
	interceptor := NewRateLimitInterceptor(config, metrics)

	// Stop should be safe to call multiple times without panicking
	interceptor.Stop()
	interceptor.Stop()
	interceptor.Stop()
}
