// Package ratelimit provides per-tenant, per-method rate limiting for gRPC services.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// DefaultBurstSize is the maximum number of tokens in the bucket (burst capacity).
	DefaultBurstSize = 10

	// DefaultRefillRate is how often a token is added (1 token per 6 seconds = 10 per minute).
	DefaultRefillRate = 6 * time.Second

	// DefaultCleanupInterval is how often to check for idle limiters.
	DefaultCleanupInterval = 5 * time.Minute

	// DefaultIdleTimeout is how long a limiter can be idle before eviction.
	DefaultIdleTimeout = 1 * time.Hour
)

// tenantMethodLimiter wraps a rate.Limiter with last-used timestamp for cleanup.
type tenantMethodLimiter struct {
	limiter  *rate.Limiter
	lastUsed time.Time
	mu       sync.Mutex
}

// Config configures the rate limiter behavior.
type Config struct {
	// BurstSize is the maximum tokens in the bucket.
	BurstSize int
	// RefillRate is the duration between adding tokens.
	RefillRate time.Duration
	// CleanupInterval is how often to run cleanup.
	CleanupInterval time.Duration
	// IdleTimeout is how long before an idle limiter is evicted.
	IdleTimeout time.Duration
	// Methods to rate limit. If empty, all methods are rate limited.
	Methods []string
	// Logger for rate limit events.
	Logger *slog.Logger
}

// DefaultConfig returns the default rate limiter configuration.
func DefaultConfig() Config {
	return Config{
		BurstSize:       DefaultBurstSize,
		RefillRate:      DefaultRefillRate,
		CleanupInterval: DefaultCleanupInterval,
		IdleTimeout:     DefaultIdleTimeout,
	}
}

// Metrics holds Prometheus metrics for rate limiting.
type Metrics struct {
	allowed *prometheus.CounterVec
	blocked *prometheus.CounterVec
	active  prometheus.Gauge
}

// NewMetrics creates metrics and registers them with the given registry.
// If registry is nil, metrics are registered with the default registry.
func NewMetrics(namespace string, registry prometheus.Registerer) *Metrics {
	if registry == nil {
		registry = prometheus.DefaultRegisterer
	}
	if namespace == "" {
		namespace = "grpc"
	}

	m := &Metrics{
		allowed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "rate_limit",
				Name:      "requests_allowed_total",
				Help:      "Total number of requests allowed by the rate limiter",
			},
			[]string{"tenant", "method"},
		),
		blocked: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "rate_limit",
				Name:      "requests_blocked_total",
				Help:      "Total number of requests blocked by the rate limiter",
			},
			[]string{"tenant", "method"},
		),
		active: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "rate_limit",
				Name:      "active_limiters",
				Help:      "Number of active per-tenant rate limiters in memory",
			},
		),
	}

	for _, collector := range []prometheus.Collector{m.allowed, m.blocked, m.active} {
		if err := registry.Register(collector); err != nil {
			var alreadyRegistered prometheus.AlreadyRegisteredError
			if !errors.As(err, &alreadyRegistered) {
				panic(err)
			}
		}
	}
	return m
}

// Interceptor provides per-tenant, per-method rate limiting for gRPC endpoints.
type Interceptor struct {
	limiters    sync.Map // map[string]*tenantMethodLimiter (key: "tenantID:method")
	activeCount atomic.Int64
	config      Config
	metrics     *Metrics
	methods     map[string]struct{} // nil means all methods
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

// NewInterceptor creates a new rate limit interceptor with the given config.
func NewInterceptor(config Config, metrics *Metrics) *Interceptor {
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

	var methods map[string]struct{}
	if len(config.Methods) > 0 {
		methods = make(map[string]struct{}, len(config.Methods))
		for _, m := range config.Methods {
			methods[m] = struct{}{}
		}
	}

	r := &Interceptor{
		config:  config,
		metrics: metrics,
		methods: methods,
		stopCh:  make(chan struct{}),
	}

	r.wg.Add(1)
	go r.cleanupLoop()

	return r
}

// Stop gracefully stops the interceptor's background cleanup goroutine.
// Safe to call multiple times.
func (r *Interceptor) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	r.wg.Wait()
}

// cleanupLoop periodically removes idle limiters to prevent memory leaks.
func (r *Interceptor) cleanupLoop() {
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
func (r *Interceptor) cleanupIdleLimiters() {
	now := time.Now()

	r.limiters.Range(func(key, value any) bool {
		tl, ok := value.(*tenantMethodLimiter)
		if !ok {
			return true
		}
		tl.mu.Lock()
		idle := now.Sub(tl.lastUsed) > r.config.IdleTimeout
		tl.mu.Unlock()

		if idle {
			r.limiters.Delete(key)
			r.activeCount.Add(-1)
			if r.config.Logger != nil {
				r.config.Logger.Debug("evicted idle rate limiter",
					"key", key)
			}
		}
		return true
	})

	if r.metrics != nil {
		r.metrics.active.Set(float64(r.activeCount.Load()))
	}
}

// hashTenantID creates a short, privacy-preserving hash for logging.
func hashTenantID(tenantID string) string {
	h := sha256.Sum256([]byte(tenantID))
	return hex.EncodeToString(h[:8])
}

// limiterKey returns the composite key for per-tenant, per-method rate limiting.
func limiterKey(tenantID, method string) string {
	return tenantID + ":" + method
}

// getOrCreateLimiter returns the rate limiter for a tenant+method, creating one if needed.
func (r *Interceptor) getOrCreateLimiter(key string) *tenantMethodLimiter {
	if existing, ok := r.limiters.Load(key); ok {
		tl, typeOK := existing.(*tenantMethodLimiter)
		if typeOK {
			tl.mu.Lock()
			tl.lastUsed = time.Now()
			tl.mu.Unlock()
			return tl
		}
	}

	tl := &tenantMethodLimiter{
		limiter:  rate.NewLimiter(rate.Every(r.config.RefillRate), r.config.BurstSize),
		lastUsed: time.Now(),
	}

	actual, loaded := r.limiters.LoadOrStore(key, tl)
	if loaded {
		if actualTL, typeOK := actual.(*tenantMethodLimiter); typeOK {
			actualTL.mu.Lock()
			actualTL.lastUsed = time.Now()
			actualTL.mu.Unlock()
			return actualTL
		}
	}

	count := r.activeCount.Add(1)
	if r.metrics != nil {
		r.metrics.active.Set(float64(count))
	}

	return tl
}

// shouldLimit returns true if the given method should be rate limited.
func (r *Interceptor) shouldLimit(method string) bool {
	if r.methods == nil {
		return true // All methods
	}
	_, ok := r.methods[method]
	return ok
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that enforces
// per-tenant, per-method rate limiting.
func (r *Interceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if !r.shouldLimit(info.FullMethod) {
			return handler(ctx, req)
		}

		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			return handler(ctx, req)
		}

		tenantStr := tenantID.String()
		key := limiterKey(tenantStr, info.FullMethod)
		tl := r.getOrCreateLimiter(key)

		if !tl.limiter.Allow() {
			if r.metrics != nil {
				r.metrics.blocked.WithLabelValues(tenantStr, info.FullMethod).Inc()
			}
			if r.config.Logger != nil {
				r.config.Logger.Warn("rate limit exceeded",
					"tenant_hash", hashTenantID(tenantStr),
					"method", info.FullMethod)
			}
			return nil, status.Errorf(codes.ResourceExhausted,
				"rate limit exceeded: burst capacity %d exhausted on %s, try again later",
				r.config.BurstSize, info.FullMethod)
		}

		if r.metrics != nil {
			r.metrics.allowed.WithLabelValues(tenantStr, info.FullMethod).Inc()
		}

		return handler(ctx, req)
	}
}

// ActiveLimiters returns the number of active per-tenant rate limiters.
func (r *Interceptor) ActiveLimiters() int {
	return int(r.activeCount.Load())
}
