package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// tenantUnknown is the label value used when tenant context is missing.
const tenantUnknown = "unknown"

// MetricsCollector holds all Prometheus metrics collectors
type MetricsCollector struct {
	// HTTP metrics
	HTTPRequestsTotal          *prometheus.CounterVec
	HTTPRequestDurationSeconds *prometheus.HistogramVec

	// gRPC metrics
	GRPCServerHandledTotal *prometheus.CounterVec

	// Database metrics
	DBQueryDurationSeconds *prometheus.HistogramVec

	// Kafka metrics
	KafkaMessagesPublishedTotal *prometheus.CounterVec

	registry *prometheus.Registry
}

// NewMetricsCollector creates a new metrics collector with all standard metrics
//
// All metrics include an "tenant" label for multi-tenant observability.
// This allows filtering and grouping metrics by organization in Grafana dashboards.
func NewMetricsCollector() *MetricsCollector {
	registry := prometheus.NewRegistry()

	mc := &MetricsCollector{
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "path", "status", "tenant"},
		),
		HTTPRequestDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path", "tenant"},
		),
		GRPCServerHandledTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "grpc_server_handled_total",
				Help: "Total number of gRPC requests handled by the server",
			},
			[]string{"grpc_service", "grpc_method", "grpc_code", "tenant"},
		),
		DBQueryDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "db_query_duration_seconds",
				Help:    "Database query duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"operation", "table", "tenant"},
		),
		KafkaMessagesPublishedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "kafka_messages_published_total",
				Help: "Total number of Kafka messages published",
			},
			[]string{"topic", "status", "tenant"},
		),
		registry: registry,
	}

	// Register all metrics
	registry.MustRegister(
		mc.HTTPRequestsTotal,
		mc.HTTPRequestDurationSeconds,
		mc.GRPCServerHandledTotal,
		mc.DBQueryDurationSeconds,
		mc.KafkaMessagesPublishedTotal,
	)

	return mc
}

// Handler returns an HTTP handler for the Prometheus metrics endpoint
func (mc *MetricsCollector) Handler() http.Handler {
	return promhttp.HandlerFor(mc.registry, promhttp.HandlerOpts{})
}

// RecordHTTPRequest records an HTTP request metric with tenant context
//
// IMPORTANT: The 'path' parameter should be a route pattern (e.g., "/accounts/{id}")
// rather than the actual request path (e.g., "/accounts/123") to prevent cardinality
// explosion. High cardinality labels can cause excessive memory usage in Prometheus.
//
// When using with a router, pass the matched route pattern instead of r.URL.Path.
// The organization is extracted from the context; if not present, "unknown" is used.
func (mc *MetricsCollector) RecordHTTPRequest(ctx context.Context, method, path string, status int, duration time.Duration) {
	org := getOrganizationLabel(ctx)
	mc.HTTPRequestsTotal.WithLabelValues(method, path, fmt.Sprintf("%d", status), org).Inc()
	mc.HTTPRequestDurationSeconds.WithLabelValues(method, path, org).Observe(duration.Seconds())
}

// RecordGRPCRequest records a gRPC request metric with tenant context
//
// The organization is extracted from the context; if not present, "unknown" is used.
func (mc *MetricsCollector) RecordGRPCRequest(ctx context.Context, service, method, code string) {
	org := getOrganizationLabel(ctx)
	mc.GRPCServerHandledTotal.WithLabelValues(service, method, code, org).Inc()
}

// RecordDBQuery records a database query metric with tenant context
//
// The organization is extracted from the context; if not present, "unknown" is used.
func (mc *MetricsCollector) RecordDBQuery(ctx context.Context, operation, table string, duration time.Duration) {
	org := getOrganizationLabel(ctx)
	mc.DBQueryDurationSeconds.WithLabelValues(operation, table, org).Observe(duration.Seconds())
}

// RecordKafkaPublish records a Kafka message publish metric with tenant context
//
// The organization is extracted from the context; if not present, "unknown" is used.
func (mc *MetricsCollector) RecordKafkaPublish(ctx context.Context, topic, status string) {
	org := getOrganizationLabel(ctx)
	mc.KafkaMessagesPublishedTotal.WithLabelValues(topic, status, org).Inc()
}

// getOrganizationLabel extracts the tenant ID from context for use as a metric label.
// Returns "unknown" if tenant context is missing.
func getOrganizationLabel(ctx context.Context) string {
	if ctx == nil {
		return tenantUnknown
	}
	orgID, ok := tenant.FromContext(ctx)
	if !ok || orgID.IsEmpty() {
		return tenantUnknown
	}
	return orgID.String()
}

// HTTPMiddleware returns an HTTP middleware that records request metrics
//
// WARNING: This middleware uses r.URL.Path which can cause cardinality explosion
// if your routes contain variable path segments (e.g., /accounts/123, /accounts/456).
// For production use, consider extracting the route pattern from your router and
// passing it to RecordHTTPRequest instead of using this generic middleware.
//
// The organization is extracted from the request context for metric labeling.
//
// Example with chi router:
//
//	rctx := chi.RouteContext(r.Context())
//	routePattern := rctx.RoutePattern()  // Returns "/accounts/{id}" instead of "/accounts/123"
//	mc.RecordHTTPRequest(r.Context(), r.Method, routePattern, rw.statusCode, duration)
func (mc *MetricsCollector) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response writer wrapper to capture the status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		mc.RecordHTTPRequest(r.Context(), r.Method, r.URL.Path, rw.statusCode, duration)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// StartMetricsServer starts an HTTP server to expose Prometheus metrics
func (mc *MetricsCollector) StartMetricsServer(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", mc.Handler())

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second, // Prevent Slowloris attacks
	}

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		// Use Background() instead of ctx to avoid immediate cancellation
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck // Intentionally using fresh context for shutdown grace period
			// Log error but can't do much else during shutdown
			_ = err
		}
	}()

	if err := server.ListenAndServe(); err != nil {
		// http.ErrServerClosed is returned on graceful shutdown, not an error
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("metrics server failed: %w", err)
	}
	return nil
}
