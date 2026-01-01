// Package idempotency provides distributed idempotency checking and locking capabilities.
// This file provides Prometheus metrics for idempotency monitoring and observability.
package idempotency

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Label value constants for bounded cardinality.
const (
	// Status labels
	MetricStatusPending   = "pending"
	MetricStatusCompleted = "completed"
	MetricStatusFailed    = "failed"

	// Failure reason labels
	MetricReasonTimeout    = "timeout"
	MetricReasonValidation = "validation"
	MetricReasonInternal   = "internal"
)

var (
	// idempotencyKeysPendingTotal tracks the number of keys that entered PENDING state.
	// Labels: service (originating service), operation (type of operation)
	idempotencyKeysPendingTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "idempotency_keys_pending_total",
			Help: "Total number of idempotency keys that entered PENDING state",
		},
		[]string{"service", "operation"},
	)

	// idempotencyKeysCompletedTotal tracks the number of keys that completed successfully.
	// Labels: service (originating service), operation (type of operation)
	idempotencyKeysCompletedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "idempotency_keys_completed_total",
			Help: "Total number of idempotency keys that completed successfully",
		},
		[]string{"service", "operation"},
	)

	// idempotencyKeysFailedTotal tracks the number of keys that failed.
	// Labels: service, operation, reason (bounded set: timeout, validation, internal)
	idempotencyKeysFailedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "idempotency_keys_failed_total",
			Help: "Total number of idempotency keys that failed",
		},
		[]string{"service", "operation", "reason"},
	)

	// idempotencyKeysCleanedUpTotal tracks the number of stale keys cleaned up.
	// Labels: service (service namespace from the key)
	idempotencyKeysCleanedUpTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "idempotency_keys_cleaned_up_total",
			Help: "Total number of stale PENDING idempotency keys cleaned up",
		},
		[]string{"service"},
	)

	// idempotencyKeyPendingDuration measures time from MarkPending to StoreResult.
	// This helps identify operations that take too long and may need tuning.
	idempotencyKeyPendingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "idempotency_key_pending_duration_seconds",
			Help: "Duration keys spend in PENDING state before completion or failure",
			Buckets: []float64{
				0.1, // 100ms - fast operations
				0.5, // 500ms - typical operations
				1,   // 1s - moderate operations
				5,   // 5s - slow operations
				10,  // 10s - very slow operations
				30,  // 30s - long-running operations
				60,  // 1min - extended operations
				300, // 5min - batch operations
				900, // 15min - stale threshold default
			},
		},
		[]string{"service", "operation"},
	)

	// idempotencyKeysStalePendingTotal is a gauge for currently stale PENDING keys.
	// Updated by the cleanup worker during scan operations.
	idempotencyKeysStalePendingTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "idempotency_keys_stale_pending_total",
			Help: "Current number of stale PENDING idempotency keys detected",
		},
		[]string{"service"},
	)
)

// MetricsCollector provides methods to record idempotency metrics.
// It wraps the Prometheus metrics to allow for dependency injection and testing.
type MetricsCollector struct {
	// ServiceName identifies the service using idempotency.
	// This is used as the "service" label in all metrics.
	ServiceName string
}

// NewMetricsCollector creates a new metrics collector for the given service.
func NewMetricsCollector(serviceName string) *MetricsCollector {
	return &MetricsCollector{
		ServiceName: serviceName,
	}
}

// RecordPending increments the pending counter when a key enters PENDING state.
func (m *MetricsCollector) RecordPending(operation string) {
	idempotencyKeysPendingTotal.WithLabelValues(m.ServiceName, operation).Inc()
}

// RecordCompleted increments the completed counter when an operation succeeds.
func (m *MetricsCollector) RecordCompleted(operation string) {
	idempotencyKeysCompletedTotal.WithLabelValues(m.ServiceName, operation).Inc()
}

// RecordFailed increments the failed counter when an operation fails.
// reason should be one of the MetricReason* constants for bounded cardinality.
func (m *MetricsCollector) RecordFailed(operation, reason string) {
	idempotencyKeysFailedTotal.WithLabelValues(m.ServiceName, operation, reason).Inc()
}

// RecordCleanedUp increments the cleanup counter when a stale key is processed.
func (m *MetricsCollector) RecordCleanedUp(service string) {
	idempotencyKeysCleanedUpTotal.WithLabelValues(service).Inc()
}

// RecordPendingDuration records how long a key was in PENDING state.
func (m *MetricsCollector) RecordPendingDuration(operation string, duration time.Duration) {
	idempotencyKeyPendingDuration.WithLabelValues(m.ServiceName, operation).Observe(duration.Seconds())
}

// SetStalePendingCount sets the gauge for currently stale PENDING keys.
// This should be called during each cleanup scan iteration.
func (m *MetricsCollector) SetStalePendingCount(service string, count int) {
	idempotencyKeysStalePendingTotal.WithLabelValues(service).Set(float64(count))
}

// Global convenience functions for direct metric access (useful when collector is not available)

// RecordIdempotencyPending records a key entering PENDING state.
func RecordIdempotencyPending(service, operation string) {
	idempotencyKeysPendingTotal.WithLabelValues(service, operation).Inc()
}

// RecordIdempotencyCompleted records a key completing successfully.
func RecordIdempotencyCompleted(service, operation string) {
	idempotencyKeysCompletedTotal.WithLabelValues(service, operation).Inc()
}

// RecordIdempotencyFailed records a key failing.
func RecordIdempotencyFailed(service, operation, reason string) {
	idempotencyKeysFailedTotal.WithLabelValues(service, operation, reason).Inc()
}

// RecordIdempotencyCleanedUp records a stale key being cleaned up.
func RecordIdempotencyCleanedUp(service string) {
	idempotencyKeysCleanedUpTotal.WithLabelValues(service).Inc()
}

// RecordIdempotencyPendingDuration records the pending duration for a key.
func RecordIdempotencyPendingDuration(service, operation string, duration time.Duration) {
	idempotencyKeyPendingDuration.WithLabelValues(service, operation).Observe(duration.Seconds())
}

// SetIdempotencyStalePendingCount sets the stale pending gauge.
func SetIdempotencyStalePendingCount(service string, count int) {
	idempotencyKeysStalePendingTotal.WithLabelValues(service).Set(float64(count))
}

// ExposeMetricsForTesting provides access to the raw Prometheus metrics for testing.
// This should only be used in test code.
var ExposeMetricsForTesting = struct {
	KeysPendingTotal      *prometheus.CounterVec
	KeysCompletedTotal    *prometheus.CounterVec
	KeysFailedTotal       *prometheus.CounterVec
	KeysCleanedUpTotal    *prometheus.CounterVec
	KeyPendingDuration    *prometheus.HistogramVec
	KeysStalePendingTotal *prometheus.GaugeVec
}{
	KeysPendingTotal:      idempotencyKeysPendingTotal,
	KeysCompletedTotal:    idempotencyKeysCompletedTotal,
	KeysFailedTotal:       idempotencyKeysFailedTotal,
	KeysCleanedUpTotal:    idempotencyKeysCleanedUpTotal,
	KeyPendingDuration:    idempotencyKeyPendingDuration,
	KeysStalePendingTotal: idempotencyKeysStalePendingTotal,
}
