// Package observability provides Prometheus metrics and monitoring for the Market Information service.
// BIAN Service Domain: Market Information Management
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Error category constants for bounded label cardinality.
const (
	ErrorCategoryValidation = "validation"
	ErrorCategoryNotFound   = "not_found"
	ErrorCategoryInternal   = "internal"
	ErrorCategoryDatabase   = "database"
	ErrorCategoryExternal   = "external"
)

// Operation name constants for consistent metric labeling.
const (
	OperationDefineDataset     = "define_dataset"
	OperationRecordObservation = "record_observation"
	OperationQueryPrices       = "query_prices"
	OperationRetrieveDataset   = "retrieve_dataset"
)

// Status constants for operation outcomes.
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

var (
	// Operation duration metrics
	operationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "market_information_operation_duration_seconds",
			Help:    "Duration of market information operations in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"operation", "status"},
	)

	// Price benchmark metrics
	priceBenchmarkUpdatesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "market_information_price_benchmark_updates_total",
			Help: "Total number of price benchmark updates received",
		},
		[]string{"benchmark_type", "source"},
	)

	// Price query metrics
	priceQueriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "market_information_price_queries_total",
			Help: "Total number of price queries",
		},
		[]string{"query_type"},
	)

	// Data freshness gauge
	dataFreshnessSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "market_information_data_freshness_seconds",
			Help: "Age of the most recent data in seconds (lower is better)",
		},
		[]string{"data_type"},
	)

	// External service metrics for data feeds
	externalFeedErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "market_information_external_feed_errors_total",
			Help: "Total number of errors from external data feeds",
		},
		[]string{"feed_source", "error_type"},
	)

	// Health check metrics
	healthCheckTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "market_information_health_check_total",
			Help: "Total number of health checks by component and status",
		},
		[]string{"component", "status"},
	)

	// Error metrics
	errorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "market_information_errors_total",
			Help: "Total number of errors by category and operation",
		},
		[]string{"category", "operation"},
	)

	// In-flight operations gauge
	operationsInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "market_information_operations_in_flight",
			Help: "Number of operations currently being processed",
		},
		[]string{"operation"},
	)
)

// RecordOperationDuration records the duration of a market information operation
func RecordOperationDuration(operation, status string, duration time.Duration) {
	operationDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordPriceBenchmarkUpdate records a price benchmark update
func RecordPriceBenchmarkUpdate(benchmarkType, source string) {
	priceBenchmarkUpdatesTotal.WithLabelValues(benchmarkType, source).Inc()
}

// RecordPriceQuery records a price query
func RecordPriceQuery(queryType string) {
	priceQueriesTotal.WithLabelValues(queryType).Inc()
}

// RecordDataFreshness records the age of the most recent data
func RecordDataFreshness(dataType string, ageSeconds float64) {
	dataFreshnessSeconds.WithLabelValues(dataType).Set(ageSeconds)
}

// RecordExternalFeedError records an error from an external data feed
func RecordExternalFeedError(feedSource, errorType string) {
	externalFeedErrors.WithLabelValues(feedSource, errorType).Inc()
}

// RecordHealthCheck records a health check result.
func RecordHealthCheck(component, status string) {
	healthCheckTotal.WithLabelValues(component, status).Inc()
}

// RecordError records an error with category and operation context.
func RecordError(category, operation string) {
	errorsTotal.WithLabelValues(category, operation).Inc()
}

// IncOperationsInFlight increments the in-flight gauge for an operation.
func IncOperationsInFlight(operation string) {
	operationsInFlight.WithLabelValues(operation).Inc()
}

// DecOperationsInFlight decrements the in-flight gauge for an operation.
func DecOperationsInFlight(operation string) {
	operationsInFlight.WithLabelValues(operation).Dec()
}

// OperationTimer provides a convenient way to time operations and record metrics.
// It protects against double-observation which would cause incorrect gauge values.
type OperationTimer struct {
	operation string
	start     time.Time
	observed  bool
}

// NewOperationTimer creates a new timer and increments the in-flight gauge.
func NewOperationTimer(operation string) *OperationTimer {
	IncOperationsInFlight(operation)
	return &OperationTimer{
		operation: operation,
		start:     time.Now(),
		observed:  false,
	}
}

// ObserveSuccess records a successful operation and decrements in-flight gauge.
// Safe to call multiple times; only the first call has effect.
func (t *OperationTimer) ObserveSuccess() {
	if t.observed {
		return
	}
	t.observed = true
	DecOperationsInFlight(t.operation)
	RecordOperationDuration(t.operation, StatusSuccess, time.Since(t.start))
}

// ObserveError records a failed operation with error category and decrements in-flight gauge.
// Safe to call multiple times; only the first call has effect.
func (t *OperationTimer) ObserveError(errorCategory string) {
	if t.observed {
		return
	}
	t.observed = true
	DecOperationsInFlight(t.operation)
	RecordOperationDuration(t.operation, StatusError, time.Since(t.start))
	RecordError(errorCategory, t.operation)
}
