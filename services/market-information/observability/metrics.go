// Package observability provides Prometheus metrics and monitoring for the Market Information service.
// BIAN Service Domain: Market Information Management
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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
