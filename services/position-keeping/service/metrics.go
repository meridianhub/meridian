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

// ExposeMetricsForTesting provides access to the raw Prometheus metrics for testing.
// This should only be used in test code.
var ExposeMetricsForTesting = struct {
	MeasurementValidationFailuresTotal *prometheus.CounterVec
}{
	MeasurementValidationFailuresTotal: measurementValidationFailuresTotal,
}
