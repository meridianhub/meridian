// Package middleware provides gRPC interceptors for the reference-data service.
package middleware

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// RegisterInstrumentMethod is the full gRPC method name for rate limiting.
	RegisterInstrumentMethod = "/meridian.reference_data.v1.ReferenceDataService/RegisterInstrument"

	// DefaultBurstSize is the maximum number of tokens in the bucket (burst capacity).
	DefaultBurstSize = 10

	// DefaultRefillRate is how often a token is added (1 token per 6 seconds = 10 per minute).
	DefaultRefillRate = 6 * time.Second

	// DefaultCleanupInterval is how often to check for idle limiters.
	DefaultCleanupInterval = 5 * time.Minute

	// DefaultIdleTimeout is how long a limiter can be idle before eviction.
	DefaultIdleTimeout = 1 * time.Hour
)

// tenantLimiter wraps a rate.Limiter with last-used timestamp for cleanup.
type tenantLimiter struct {
	limiter  *rate.Limiter
	lastUsed time.Time
	mu       sync.Mutex
}

// RateLimitInterceptorConfig configures the rate limiter behavior.
type RateLimitInterceptorConfig struct {
	// BurstSize is the maximum tokens in the bucket.
	BurstSize int
	// RefillRate is the duration between adding tokens.
	RefillRate time.Duration
	// CleanupInterval is how often to run cleanup.
	CleanupInterval time.Duration
	// IdleTimeout is how long before an idle limiter is evicted.
	IdleTimeout time.Duration
	// Logger for rate limit events.
	Logger *slog.Logger
}

// DefaultConfig returns the default rate limiter configuration.
func DefaultConfig() RateLimitInterceptorConfig {
	return RateLimitInterceptorConfig{
		BurstSize:       DefaultBurstSize,
		RefillRate:      DefaultRefillRate,
		CleanupInterval: DefaultCleanupInterval,
		IdleTimeout:     DefaultIdleTimeout,
	}
}

// RateLimitMetrics holds Prometheus metrics for rate limiting.
type RateLimitMetrics struct {
	allowed *prometheus.CounterVec
	blocked *prometheus.CounterVec
	active  prometheus.Gauge
}

// NewRateLimitMetrics creates metrics and registers them with the given registry.
// If registry is nil, metrics are registered with the default registry.
func NewRateLimitMetrics(registry prometheus.Registerer) *RateLimitMetrics {
	if registry == nil {
		registry = prometheus.DefaultRegisterer
	}

	m := &RateLimitMetrics{
		allowed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "reference_data",
				Subsystem: "rate_limit",
				Name:      "requests_allowed_total",
				Help:      "Total number of requests allowed by the rate limiter",
			},
			[]string{"tenant"},
		),
		blocked: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "reference_data",
				Subsystem: "rate_limit",
				Name:      "requests_blocked_total",
				Help:      "Total number of requests blocked by the rate limiter",
			},
			[]string{"tenant", "method"},
		),
		active: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "reference_data",
				Subsystem: "rate_limit",
				Name:      "active_limiters",
				Help:      "Number of active per-tenant rate limiters in memory",
			},
		),
	}

	registry.MustRegister(m.allowed, m.blocked, m.active)
	return m
}

// RateLimitInterceptor provides per-tenant rate limiting for gRPC endpoints.
type RateLimitInterceptor struct {
	limiters sync.Map // map[string]*tenantLimiter
	config   RateLimitInterceptorConfig
	metrics  *RateLimitMetrics
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewRateLimitInterceptor creates a new rate limit interceptor with the given config.
func NewRateLimitInterceptor(config RateLimitInterceptorConfig, metrics *RateLimitMetrics) *RateLimitInterceptor {
	if config.BurstSize == 0 {
		config.BurstSize = DefaultBurstSize
	}
	if config.RefillRate == 0 {
		config.RefillRate = DefaultRefillRate
	}
	if config.CleanupInterval == 0 {
		config.CleanupInterval = DefaultCleanupInterval
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = DefaultIdleTimeout
	}

	r := &RateLimitInterceptor{
		config:  config,
		metrics: metrics,
		stopCh:  make(chan struct{}),
	}

	// Start cleanup goroutine
	r.wg.Add(1)
	go r.cleanupLoop()

	return r
}

// Stop gracefully stops the interceptor's background cleanup goroutine.
func (r *RateLimitInterceptor) Stop() {
	close(r.stopCh)
	r.wg.Wait()
}

// cleanupLoop periodically removes idle limiters to prevent memory leaks.
func (r *RateLimitInterceptor) cleanupLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(r.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.cleanupIdleLimiters()
		}
	}
}

// cleanupIdleLimiters removes limiters that haven't been used recently.
func (r *RateLimitInterceptor) cleanupIdleLimiters() {
	now := time.Now()
	var activeCount int

	r.limiters.Range(func(key, value any) bool {
		tl, ok := value.(*tenantLimiter)
		if !ok {
			return true // Skip malformed entry
		}
		tl.mu.Lock()
		idleDuration := now.Sub(tl.lastUsed)
		idle := idleDuration > r.config.IdleTimeout
		tl.mu.Unlock()

		if idle {
			r.limiters.Delete(key)
			if r.config.Logger != nil {
				r.config.Logger.Debug("evicted idle rate limiter",
					"tenant", key,
					"idle_duration", idleDuration)
			}
		} else {
			activeCount++
		}
		return true
	})

	if r.metrics != nil {
		r.metrics.active.Set(float64(activeCount))
	}
}

// getOrCreateLimiter returns the rate limiter for a tenant, creating one if needed.
func (r *RateLimitInterceptor) getOrCreateLimiter(tenantID string) *tenantLimiter {
	if existing, ok := r.limiters.Load(tenantID); ok {
		tl, typeOK := existing.(*tenantLimiter)
		if typeOK {
			tl.mu.Lock()
			tl.lastUsed = time.Now()
			tl.mu.Unlock()
			return tl
		}
		// Type assertion failed, fall through to create new limiter
	}

	// Create new limiter: rate.Every(6s) = ~0.167 tokens/sec, burst of 10
	tl := &tenantLimiter{
		limiter:  rate.NewLimiter(rate.Every(r.config.RefillRate), r.config.BurstSize),
		lastUsed: time.Now(),
	}

	// LoadOrStore handles race conditions
	actual, loaded := r.limiters.LoadOrStore(tenantID, tl)
	if loaded {
		// Another goroutine created the limiter first, use that one
		if actualTL, typeOK := actual.(*tenantLimiter); typeOK {
			return actualTL
		}
	}

	// We created a new limiter, update the gauge
	if r.metrics != nil {
		// Count active limiters
		var count int
		r.limiters.Range(func(_, _ any) bool {
			count++
			return true
		})
		r.metrics.active.Set(float64(count))
	}

	return tl
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that enforces
// per-tenant rate limiting on the RegisterInstrument endpoint.
func (r *RateLimitInterceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		// Only rate limit RegisterInstrument, not read operations
		if info.FullMethod != RegisterInstrumentMethod {
			return handler(ctx, req)
		}

		// Extract tenant ID from context
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			// No tenant context - allow the request through
			// The request will fail authorization elsewhere if required
			return handler(ctx, req)
		}

		tenantStr := tenantID.String()
		tl := r.getOrCreateLimiter(tenantStr)

		if !tl.limiter.Allow() {
			// Rate limit exceeded
			if r.metrics != nil {
				r.metrics.blocked.WithLabelValues(tenantStr, info.FullMethod).Inc()
			}
			if r.config.Logger != nil {
				r.config.Logger.Warn("rate limit exceeded",
					"tenant", tenantStr,
					"method", info.FullMethod)
			}
			return nil, status.Errorf(codes.ResourceExhausted,
				"rate limit exceeded for tenant %s: max %d requests per minute",
				tenantStr, r.config.BurstSize)
		}

		// Request allowed
		if r.metrics != nil {
			r.metrics.allowed.WithLabelValues(tenantStr).Inc()
		}

		return handler(ctx, req)
	}
}

// ActiveLimiters returns the number of active per-tenant rate limiters.
// Useful for testing.
func (r *RateLimitInterceptor) ActiveLimiters() int {
	var count int
	r.limiters.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
