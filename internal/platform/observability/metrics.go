package observability

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

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
func NewMetricsCollector() *MetricsCollector {
	registry := prometheus.NewRegistry()

	mc := &MetricsCollector{
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),
		HTTPRequestDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		GRPCServerHandledTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "grpc_server_handled_total",
				Help: "Total number of gRPC requests handled by the server",
			},
			[]string{"grpc_service", "grpc_method", "grpc_code"},
		),
		DBQueryDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "db_query_duration_seconds",
				Help:    "Database query duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"operation", "table"},
		),
		KafkaMessagesPublishedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "kafka_messages_published_total",
				Help: "Total number of Kafka messages published",
			},
			[]string{"topic", "status"},
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

// RecordHTTPRequest records an HTTP request metric
func (mc *MetricsCollector) RecordHTTPRequest(method, path string, status int, duration time.Duration) {
	mc.HTTPRequestsTotal.WithLabelValues(method, path, fmt.Sprintf("%d", status)).Inc()
	mc.HTTPRequestDurationSeconds.WithLabelValues(method, path).Observe(duration.Seconds())
}

// RecordGRPCRequest records a gRPC request metric
func (mc *MetricsCollector) RecordGRPCRequest(service, method, code string) {
	mc.GRPCServerHandledTotal.WithLabelValues(service, method, code).Inc()
}

// RecordDBQuery records a database query metric
func (mc *MetricsCollector) RecordDBQuery(operation, table string, duration time.Duration) {
	mc.DBQueryDurationSeconds.WithLabelValues(operation, table).Observe(duration.Seconds())
}

// RecordKafkaPublish records a Kafka message publish metric
func (mc *MetricsCollector) RecordKafkaPublish(topic, status string) {
	mc.KafkaMessagesPublishedTotal.WithLabelValues(topic, status).Inc()
}

// HTTPMiddleware returns an HTTP middleware that records request metrics
func (mc *MetricsCollector) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response writer wrapper to capture the status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		mc.RecordHTTPRequest(r.Method, r.URL.Path, rw.statusCode, duration)
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
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			// Log error but can't do much else during shutdown
			_ = err
		}
	}()

	if err := server.ListenAndServe(); err != nil {
		return fmt.Errorf("metrics server failed: %w", err)
	}
	return nil
}
