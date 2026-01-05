// Package service provides gRPC service implementations for position keeping operations.
package service

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Validation failure reasons - bounded set to prevent cardinality explosion.
const (
	// ValidationFailureReasonCELRejected indicates the CEL expression returned false.
	ValidationFailureReasonCELRejected = "cel_rejected"
	// ValidationFailureReasonCELError indicates an error occurred during CEL evaluation.
	ValidationFailureReasonCELError = "cel_error"
	// ValidationFailureReasonInstrumentNotFound indicates the instrument was not found in cache.
	ValidationFailureReasonInstrumentNotFound = "instrument_not_found"
	// ValidationFailureReasonBucketKeyError indicates an error occurred during bucket key generation.
	ValidationFailureReasonBucketKeyError = "bucket_key_error"
)

// measurementValidationFailuresTotal counts validation failures for measurements.
// Labels:
//   - instrument_code: the instrument code (measurement_type) being validated
//   - failure_reason: bounded set (cel_rejected, cel_error, instrument_not_found)
var measurementValidationFailuresTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "position_keeping",
		Name:      "measurement_validation_failures_total",
		Help:      "Total number of measurement validation failures by instrument code and reason",
	},
	[]string{"instrument_code", "failure_reason"},
)

// RecordValidationFailure increments the validation failure counter.
func RecordValidationFailure(instrumentCode string, reason string) {
	measurementValidationFailuresTotal.WithLabelValues(instrumentCode, reason).Inc()
}

// bucketCardinalityViolationsTotal counts cardinality limit violations.
// Labels:
//   - instrument_code: the instrument code (measurement_type) being validated
//
// Note: We intentionally do NOT include account_id as a label because:
// 1. Account IDs can have high cardinality (millions of accounts)
// 2. Including them would cause Prometheus storage bloat
// 3. The instrument_code gives enough context for alerting and debugging
// For detailed per-account investigation, use structured logging or query the database.
var bucketCardinalityViolationsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "position_keeping",
		Name:      "bucket_cardinality_violations_total",
		Help:      "Total number of bucket cardinality limit violations by instrument code",
	},
	[]string{"instrument_code"},
)

// RecordCardinalityViolation increments the cardinality violation counter.
func RecordCardinalityViolation(instrumentCode string) {
	bucketCardinalityViolationsTotal.WithLabelValues(instrumentCode).Inc()
}

// ExposeMetricsForTesting provides access to the raw Prometheus metrics for testing.
// This should only be used in test code.
var ExposeMetricsForTesting = struct {
	MeasurementValidationFailuresTotal *prometheus.CounterVec
	BucketCardinalityViolationsTotal   *prometheus.CounterVec
}{
	MeasurementValidationFailuresTotal: measurementValidationFailuresTotal,
	BucketCardinalityViolationsTotal:   bucketCardinalityViolationsTotal,
}
