package scheduler

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus metrics for scheduler workers. Metrics are labeled by worker name
// to distinguish between different worker types sharing this lifecycle.
var (
	workerStartsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "worker_starts_total",
		Help:      "Total number of worker starts",
	}, []string{"worker"})

	workerStopsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "worker_stops_total",
		Help:      "Total number of worker stops",
	}, []string{"worker"})

	workerShutdownDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "shutdown_duration_seconds",
		Help:      "Duration of worker shutdown in seconds",
		Buckets:   []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"worker"})

	workerShutdownTimeouts = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "shutdown_timeouts_total",
		Help:      "Total number of worker shutdown timeouts",
	}, []string{"worker"})

	workerInFlightWork = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "in_flight_work",
		Help:      "Current number of in-flight guarded work items",
	}, []string{"worker"})

	workerPollTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "scheduler",
		Name:      "poll_total",
		Help:      "Total number of poll cycles executed",
	}, []string{"worker"})
)

// RecordWorkerStart increments the worker starts counter.
func RecordWorkerStart(worker string) {
	workerStartsTotal.WithLabelValues(worker).Inc()
}

// RecordWorkerStop increments the worker stops counter.
func RecordWorkerStop(worker string) {
	workerStopsTotal.WithLabelValues(worker).Inc()
}

// RecordShutdownDuration observes the shutdown duration for a worker.
func RecordShutdownDuration(worker string, seconds float64) {
	workerShutdownDuration.WithLabelValues(worker).Observe(seconds)
}

// RecordShutdownTimeout increments the shutdown timeout counter.
func RecordShutdownTimeout(worker string) {
	workerShutdownTimeouts.WithLabelValues(worker).Inc()
}

// RecordInFlightWork sets the current in-flight work gauge.
func RecordInFlightWork(worker string, count float64) {
	workerInFlightWork.WithLabelValues(worker).Set(count)
}

// RecordPoll increments the poll cycle counter.
func RecordPoll(worker string) {
	workerPollTotal.WithLabelValues(worker).Inc()
}
