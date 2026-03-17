package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func testHandler(_ context.Context, _ any) (any, error) {
	return "ok", nil
}

func testRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}

func TestInterceptor_AllowsUpToBurstSize(t *testing.T) {
	registry := testRegistry()
	metrics := NewMetrics("test", registry)

	config := Config{
		BurstSize:       5,
		RefillRate:      1 * time.Minute,
		CleanupInterval: 1 * time.Hour,
		IdleTimeout:     1 * time.Hour,
	}
	interceptor := NewInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("test_tenant"))
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Write"}
	handler := interceptor.UnaryServerInterceptor()

	for i := 0; i < 5; i++ {
		resp, err := handler(ctx, nil, info, testHandler)
		require.NoError(t, err, "request %d should succeed", i+1)
		assert.Equal(t, "ok", resp)
	}

	_, err := handler(ctx, nil, info, testHandler)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
	assert.Contains(t, st.Message(), "rate limit exceeded")
	assert.NotContains(t, st.Message(), "test_tenant", "tenant ID must not appear in client-facing error")
}

func TestInterceptor_PerTenantIsolation(t *testing.T) {
	registry := testRegistry()
	metrics := NewMetrics("test", registry)

	config := Config{
		BurstSize:  2,
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewInterceptor(config, metrics)
	defer interceptor.Stop()

	ctxA := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant_a"))
	ctxB := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant_b"))
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Write"}
	handler := interceptor.UnaryServerInterceptor()

	// Exhaust tenant A
	for i := 0; i < 2; i++ {
		_, err := handler(ctxA, nil, info, testHandler)
		require.NoError(t, err)
	}
	_, err := handler(ctxA, nil, info, testHandler)
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))

	// Tenant B unaffected
	for i := 0; i < 2; i++ {
		_, err := handler(ctxB, nil, info, testHandler)
		require.NoError(t, err, "tenant B request %d should succeed", i+1)
	}
}

func TestInterceptor_PerMethodIsolation(t *testing.T) {
	registry := testRegistry()
	metrics := NewMetrics("test", registry)

	config := Config{
		BurstSize:  2,
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("test_tenant"))
	infoA := &grpc.UnaryServerInfo{FullMethod: "/test.Service/MethodA"}
	infoB := &grpc.UnaryServerInfo{FullMethod: "/test.Service/MethodB"}
	handler := interceptor.UnaryServerInterceptor()

	// Exhaust method A
	for i := 0; i < 2; i++ {
		_, err := handler(ctx, nil, infoA, testHandler)
		require.NoError(t, err)
	}
	_, err := handler(ctx, nil, infoA, testHandler)
	require.Error(t, err)

	// Method B unaffected
	for i := 0; i < 2; i++ {
		_, err := handler(ctx, nil, infoB, testHandler)
		require.NoError(t, err, "method B request %d should succeed", i+1)
	}
}

func TestInterceptor_MethodFilter(t *testing.T) {
	registry := testRegistry()
	metrics := NewMetrics("test", registry)

	config := Config{
		BurstSize:  1,
		RefillRate: 1 * time.Hour,
		Methods:    []string{"/test.Service/Write"},
	}
	interceptor := NewInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("test_tenant"))
	handler := interceptor.UnaryServerInterceptor()

	// Non-configured method is never rate limited
	readInfo := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Read"}
	for i := 0; i < 100; i++ {
		_, err := handler(ctx, nil, readInfo, testHandler)
		require.NoError(t, err)
	}

	// Configured method is rate limited
	writeInfo := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Write"}
	_, err := handler(ctx, nil, writeInfo, testHandler)
	require.NoError(t, err)
	_, err = handler(ctx, nil, writeInfo, testHandler)
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
}

func TestInterceptor_NoTenantContext(t *testing.T) {
	registry := testRegistry()
	metrics := NewMetrics("test", registry)

	config := Config{
		BurstSize:  1,
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := context.Background()
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Write"}
	handler := interceptor.UnaryServerInterceptor()

	// Without tenant context, requests pass through
	for i := 0; i < 10; i++ {
		resp, err := handler(ctx, nil, info, testHandler)
		require.NoError(t, err)
		assert.Equal(t, "ok", resp)
	}
}

func TestInterceptor_ConcurrentAccess(t *testing.T) {
	registry := testRegistry()
	metrics := NewMetrics("test", registry)

	config := Config{
		BurstSize:  10,
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("concurrent_tenant"))
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Write"}
	handler := interceptor.UnaryServerInterceptor()

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
	assert.Equal(t, int32(10), allowed.Load())
	assert.Equal(t, int32(10), blocked.Load())
}

func TestInterceptor_CleanupIdleLimiters(t *testing.T) {
	registry := testRegistry()
	metrics := NewMetrics("test", registry)

	config := Config{
		BurstSize:       10,
		RefillRate:      1 * time.Hour,
		CleanupInterval: 50 * time.Millisecond,
		IdleTimeout:     100 * time.Millisecond,
	}
	interceptor := NewInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("cleanup_tenant"))
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Write"}
	handler := interceptor.UnaryServerInterceptor()

	_, err := handler(ctx, nil, info, testHandler)
	require.NoError(t, err)
	assert.Equal(t, 1, interceptor.ActiveLimiters())

	// Wait for cleanup to evict
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return interceptor.ActiveLimiters() == 0
		})
	require.NoError(t, err, "limiter should be evicted after idle timeout")
}

func TestInterceptor_Metrics(t *testing.T) {
	registry := testRegistry()
	metrics := NewMetrics("test", registry)

	config := Config{
		BurstSize:  2,
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewInterceptor(config, metrics)
	defer interceptor.Stop()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("metrics_tenant"))
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Write"}
	handler := interceptor.UnaryServerInterceptor()

	// 2 allowed, 1 blocked
	handler(ctx, nil, info, testHandler)
	handler(ctx, nil, info, testHandler)
	handler(ctx, nil, info, testHandler)

	tenantHash := hashTenantID("metrics_tenant")

	allowedMetric := &dto.Metric{}
	err := metrics.allowed.WithLabelValues(tenantHash, "/test.Service/Write").Write(allowedMetric)
	require.NoError(t, err)
	assert.Equal(t, float64(2), allowedMetric.Counter.GetValue())

	blockedMetric := &dto.Metric{}
	err = metrics.blocked.WithLabelValues(tenantHash, "/test.Service/Write").Write(blockedMetric)
	require.NoError(t, err)
	assert.Equal(t, float64(1), blockedMetric.Counter.GetValue())
}

func TestInterceptor_StopIsIdempotent(t *testing.T) {
	t.Parallel()

	registry := testRegistry()
	metrics := NewMetrics("test", registry)

	config := Config{
		BurstSize:       10,
		RefillRate:      1 * time.Hour,
		CleanupInterval: 1 * time.Hour,
	}
	interceptor := NewInterceptor(config, metrics)

	interceptor.Stop()
	interceptor.Stop()
	interceptor.Stop()
}

func TestDefaultConfig_Values(t *testing.T) {
	config := DefaultConfig()
	assert.Equal(t, 10, config.BurstSize)
	assert.Equal(t, 6*time.Second, config.RefillRate)
	assert.Equal(t, 5*time.Minute, config.CleanupInterval)
	assert.Equal(t, 1*time.Hour, config.IdleTimeout)
}

func TestInterceptor_NilMetrics(t *testing.T) {
	config := Config{
		BurstSize:  2,
		RefillRate: 1 * time.Hour,
	}
	interceptor := NewInterceptor(config, nil)
	defer interceptor.Stop()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("test_tenant"))
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Write"}
	handler := interceptor.UnaryServerInterceptor()

	// Should work without metrics
	_, err := handler(ctx, nil, info, testHandler)
	require.NoError(t, err)

	// Should still enforce rate limits without metrics
	_, err = handler(ctx, nil, info, testHandler)
	require.NoError(t, err)
	_, err = handler(ctx, nil, info, testHandler)
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
}
