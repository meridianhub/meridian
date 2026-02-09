package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Run type constants for metric labels.
const (
	RunTypeScheduled = "scheduled"
	RunTypeManual    = "manual"
	RunTypeRerun     = "rerun"
)

// Variance reason constants for metric labels.
const (
	ReasonAmountMismatch    = "amount_mismatch"
	ReasonMissingEntry      = "missing_entry"
	ReasonQualityUpgrade    = "quality_upgrade"
	ReasonExternalMismatch  = "external_mismatch"
	ReasonCorrectionApplied = "correction_applied"
)

// Run status constants for metric labels.
const (
	RunStatusPending   = "PENDING"
	RunStatusRunning   = "RUNNING"
	RunStatusCompleted = "COMPLETED"
	RunStatusFailed    = "FAILED"
	RunStatusFinalized = "FINALIZED"
)

var (
	// ReconciliationRunDuration measures the duration of reconciliation runs.
	ReconciliationRunDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "run_duration_seconds",
			Help:      "Duration of reconciliation runs in seconds.",
			Buckets:   []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800},
		},
		[]string{"run_type", "asset_code"},
	)

	// SnapshotsCreatedTotal counts the total number of snapshots created.
	SnapshotsCreatedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "snapshots_created_total",
			Help:      "Total number of settlement snapshots created.",
		},
	)

	// VariancesDetectedTotal counts variances detected by reason.
	VariancesDetectedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "variances_detected_total",
			Help:      "Total number of variances detected, labeled by reason.",
		},
		[]string{"reason"},
	)

	// VarianceValueGauge tracks the current total variance value.
	VarianceValueGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "variance_value",
			Help:      "Current total absolute variance value across all open variances.",
		},
	)

	// DisputesPendingGauge tracks the number of pending disputes.
	DisputesPendingGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "disputes_pending_total",
			Help:      "Current number of pending (unresolved) disputes.",
		},
	)

	// BalanceImbalanceGauge tracks the current imbalance amount per instrument code.
	// In a healthy system, this gauge should always be 0.
	// A non-zero value indicates a P1/Critical ledger integrity violation.
	BalanceImbalanceGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "balance_imbalance_amount",
			Help:      "Current imbalance amount per instrument code. Should always be 0 in a healthy system.",
		},
		[]string{"instrument_code"},
	)

	// RunStatusGauge tracks the status of the most recent reconciliation run.
	// Values: 0=PENDING, 1=RUNNING, 2=COMPLETED, 3=FAILED, 4=FINALIZED.
	RunStatusGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "run_status",
			Help:      "Status of the most recent reconciliation run (0=PENDING, 1=RUNNING, 2=COMPLETED, 3=FAILED, 4=FINALIZED).",
		},
	)

	// BalanceAssertionTotal counts the total number of balance assertions by status.
	BalanceAssertionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "balance_assertion_total",
			Help:      "Total number of balance assertions executed, labeled by result status.",
		},
		[]string{"status", "scope"},
	)

	// PersistentImbalanceGauge tracks the number of consecutive days of persistent imbalance.
	PersistentImbalanceGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "persistent_imbalance_days",
			Help:      "Number of consecutive days of imbalance per instrument code.",
		},
		[]string{"instrument_code"},
	)

	// SettlementFinalityTotal counts the total number of settlement finality operations by result.
	SettlementFinalityTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "settlement_finality_total",
			Help:      "Total number of settlement finality operations, labeled by result status.",
		},
		[]string{"status"},
	)

	// PositionLockAttemptTotal counts position lock attempts by outcome.
	PositionLockAttemptTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "position_lock_attempt_total",
			Help:      "Total number of position lock attempts, labeled by outcome.",
		},
		[]string{"outcome"},
	)

	// KafkaPublishTotal counts Kafka publish attempts by topic and outcome.
	KafkaPublishTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "kafka_publish_total",
			Help:      "Total number of Kafka publish attempts, labeled by topic and outcome.",
		},
		[]string{"topic", "outcome"},
	)

	// KafkaPublishDuration measures Kafka publish latency.
	KafkaPublishDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "kafka_publish_duration_seconds",
			Help:      "Duration of Kafka publish operations in seconds.",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		},
	)

	// CircuitBreakerStateGauge tracks circuit breaker state per upstream service.
	// Values: 0=closed, 1=half-open, 2=open.
	CircuitBreakerStateGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "circuit_breaker_state",
			Help:      "Current circuit breaker state per upstream service (0=closed, 1=half-open, 2=open).",
		},
		[]string{"service"},
	)

	// CircuitBreakerTripsTotal counts circuit breaker state transitions.
	CircuitBreakerTripsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "circuit_breaker_trips_total",
			Help:      "Total number of circuit breaker state transitions.",
		},
		[]string{"service", "from", "to"},
	)

	// ConsecutiveFailuresGauge tracks consecutive run failures for alerting.
	ConsecutiveFailuresGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "consecutive_failures",
			Help:      "Number of consecutive reconciliation run failures.",
		},
	)
)

// RecordRunDuration records the duration of a reconciliation run.
func RecordRunDuration(runType, assetCode string, duration time.Duration) {
	ReconciliationRunDuration.WithLabelValues(runType, assetCode).Observe(duration.Seconds())
}

// RecordSnapshotsCreated increments the snapshot counter by the given count.
// count must be non-negative; negative values are ignored.
func RecordSnapshotsCreated(count int) {
	if count <= 0 {
		return
	}
	SnapshotsCreatedTotal.Add(float64(count))
}

// RecordVarianceDetected increments the variance counter for a given reason.
func RecordVarianceDetected(reason string) {
	VariancesDetectedTotal.WithLabelValues(reason).Inc()
}

// SetVarianceValue sets the current total variance value.
func SetVarianceValue(value float64) {
	VarianceValueGauge.Set(value)
}

// SetDisputesPending sets the current pending disputes count.
func SetDisputesPending(count int) {
	DisputesPendingGauge.Set(float64(count))
}

// SetRunStatus sets the status gauge for the most recent run.
func SetRunStatus(status string) {
	switch status {
	case RunStatusPending:
		RunStatusGauge.Set(0)
	case RunStatusRunning:
		RunStatusGauge.Set(1)
	case RunStatusCompleted:
		RunStatusGauge.Set(2)
	case RunStatusFailed:
		RunStatusGauge.Set(3)
	case RunStatusFinalized:
		RunStatusGauge.Set(4)
	}
}

// RecordKafkaPublish records a Kafka publish attempt.
func RecordKafkaPublish(topic, outcome string, duration time.Duration) {
	KafkaPublishTotal.WithLabelValues(topic, outcome).Inc()
	KafkaPublishDuration.Observe(duration.Seconds())
}

// SetCircuitBreakerState sets the circuit breaker state for an upstream service.
func SetCircuitBreakerState(service string, state int) {
	CircuitBreakerStateGauge.WithLabelValues(service).Set(float64(state))
}

// RecordCircuitBreakerTrip records a circuit breaker state transition.
func RecordCircuitBreakerTrip(service, from, to string) {
	CircuitBreakerTripsTotal.WithLabelValues(service, from, to).Inc()
}

// SetConsecutiveFailures sets the current consecutive failure count.
func SetConsecutiveFailures(count int) {
	ConsecutiveFailuresGauge.Set(float64(count))
}
