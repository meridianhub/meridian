package validation

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// validationTotal tracks total saga validations by outcome.
	// Labels: saga_name (saga identifier), status ("success" or "failed")
	validationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "saga_validation_total",
			Help: "Total saga validations performed",
		},
		[]string{"saga_name", "status"},
	)

	// validationErrorsTotal tracks validation errors by category.
	// Labels: saga_name, error_category (SYNTAX, UNDEFINED_HANDLER, TYPE_MISMATCH, RUNTIME, TIMEOUT)
	validationErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "saga_validation_errors_total",
			Help: "Total validation errors by category",
		},
		[]string{"saga_name", "error_category"},
	)

	// complexityScore tracks the distribution of saga complexity scores (0-10).
	complexityScore = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "saga_complexity_score",
			Help:    "Complexity score distribution (0-10)",
			Buckets: []float64{1, 2, 3, 5, 7, 10},
		},
		[]string{"saga_name"},
	)

	// handlerCallCount tracks handler calls per saga validation.
	handlerCallCount = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "saga_handler_call_count",
			Help:    "Handler calls per saga validation",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		},
		[]string{"saga_name"},
	)
)

// RecordValidation records Prometheus metrics for a validation result.
func RecordValidation(sagaName string, result *ValidationResult) {
	if result == nil {
		return
	}

	status := "success"
	if !result.Success {
		status = "failed"
	}
	validationTotal.WithLabelValues(sagaName, status).Inc()

	for _, err := range result.Errors {
		validationErrorsTotal.WithLabelValues(sagaName, string(err.Category)).Inc()
	}

	complexityScore.WithLabelValues(sagaName).Observe(float64(calculateComplexityScore(result.Metrics.HandlerCallCount)))

	handlerCallCount.WithLabelValues(sagaName).Observe(float64(result.Metrics.HandlerCallCount))
}

// ExposeValidationMetricsForTesting provides access to raw Prometheus metrics for testing.
var ExposeValidationMetricsForTesting = struct {
	ValidationTotal  *prometheus.CounterVec
	ErrorsTotal      *prometheus.CounterVec
	ComplexityScore  *prometheus.HistogramVec
	HandlerCallCount *prometheus.HistogramVec
}{
	ValidationTotal:  validationTotal,
	ErrorsTotal:      validationErrorsTotal,
	ComplexityScore:  complexityScore,
	HandlerCallCount: handlerCallCount,
}
