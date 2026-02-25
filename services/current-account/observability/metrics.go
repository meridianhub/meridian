// Package observability provides Prometheus metrics and monitoring for the CurrentAccount service.
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
			Name:    "current_account_operation_duration_seconds",
			Help:    "Duration of current account operations in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"operation", "status"},
	)

	// Business metrics
	depositsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_deposits_total",
			Help: "Total number of deposit transactions",
		},
		[]string{"currency"},
	)

	withdrawalsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_withdrawals_total",
			Help: "Total number of withdrawal transactions",
		},
		[]string{"currency"},
	)

	balanceGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "current_account_balance_cents",
			Help: "Current account balance in cents",
		},
		[]string{"currency"},
	)

	// Saga metrics
	sagaFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_saga_failures_total",
			Help: "Total number of saga failures",
		},
		[]string{"operation", "failed_step"},
	)

	sagaCompensationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_saga_compensations_total",
			Help: "Total number of saga compensations executed",
		},
		[]string{"operation", "step"},
	)

	// Inline compensation metrics - for compensations that happen within a step
	// due to saga pattern limitations (step fails after side effects)
	inlineCompensationFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_inline_compensation_failures_total",
			Help: "Total number of inline compensation failures (requires manual intervention)",
		},
		[]string{"operation", "leg"},
	)

	sagaDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "current_account_saga_duration_seconds",
			Help:    "Duration of saga execution in seconds",
			Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"operation", "status"},
	)

	// External service error metrics
	externalServiceErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_external_service_errors_total",
			Help: "Total number of external service errors",
		},
		[]string{"service", "operation"},
	)

	// Party validation metrics
	partyValidationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "current_account_party_validation_duration_seconds",
			Help:    "Duration of party validation calls in seconds",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		},
		[]string{"success"},
	)

	// Circuit breaker metrics
	circuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "current_account_circuit_breaker_state",
			Help: "Current state of circuit breakers (0=closed, 1=half-open, 2=open)",
		},
		[]string{"service"},
	)

	circuitBreakerStateChanges = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_circuit_breaker_state_changes_total",
			Help: "Total number of circuit breaker state changes",
		},
		[]string{"service", "from_state", "to_state"},
	)

	// Clearing account resolver metrics
	clearingAccountCacheHits = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "current_account_clearing_account_cache_hits_total",
			Help: "Total number of clearing account cache hits",
		},
	)

	clearingAccountCacheMisses = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "current_account_clearing_account_cache_misses_total",
			Help: "Total number of clearing account cache misses",
		},
	)

	clearingAccountLookupDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "current_account_clearing_account_lookup_duration_seconds",
			Help:    "Duration of clearing account lookups from Internal Account service",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		},
	)

	clearingAccountLookupErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_clearing_account_lookup_errors_total",
			Help: "Total number of clearing account lookup errors",
		},
		[]string{"clearing_type"},
	)

	// NoOp fallback metrics - indicates degraded service functionality
	noopIdempotencyActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "current_account_noop_idempotency_active",
			Help: "1 if NoOp idempotency service is active (production risk), 0 otherwise",
		},
	)

	serviceDegradationEvents = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_service_degradation_events_total",
			Help: "Total number of service degradation events by component",
		},
		[]string{"component", "reason"},
	)

	// Webhook delivery metrics - tracks delivery attempts and failures for regulatory notifications
	webhookDeliveryAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_webhook_delivery_attempts_total",
			Help: "Total number of webhook delivery attempts by event type and status",
		},
		[]string{"event_type", "status"},
	)

	webhookDeliveryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "current_account_webhook_delivery_duration_seconds",
			Help:    "Time spent delivering webhooks by event type",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		},
		[]string{"event_type"},
	)

	webhookDeliveryRetries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_webhook_delivery_retries_total",
			Help: "Total number of webhook delivery retries by event type",
		},
		[]string{"event_type"},
	)

	webhookDeliveryExhausted = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "current_account_webhook_delivery_exhausted_total",
			Help: "Total number of webhook deliveries that failed after all retries (regulatory compliance risk)",
		},
		[]string{"event_type"},
	)
)

// RecordOperationDuration records the duration of a current account operation
func RecordOperationDuration(operation, status string, duration time.Duration) {
	operationDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordDeposit records a deposit transaction
func RecordDeposit(currency string) {
	depositsTotal.WithLabelValues(currency).Inc()
}

// RecordWithdrawal records a withdrawal transaction
func RecordWithdrawal(currency string) {
	withdrawalsTotal.WithLabelValues(currency).Inc()
}

// RecordBalance records the current account balance
func RecordBalance(balanceCents int64, currency string) {
	balanceGauge.WithLabelValues(currency).Set(float64(balanceCents))
}

// RecordSagaFailure records a saga failure
func RecordSagaFailure(operation, failedStep string) {
	sagaFailuresTotal.WithLabelValues(operation, failedStep).Inc()
}

// RecordSagaCompensation records a saga compensation
func RecordSagaCompensation(operation, step string) {
	sagaCompensationsTotal.WithLabelValues(operation, step).Inc()
}

// RecordInlineCompensationFailure records an inline compensation failure.
// These failures indicate that a compensating entry could not be created,
// requiring manual intervention to restore ledger integrity.
//
// ALERTING: This metric MUST have a Prometheus alert configured:
//
//	alert: InlineCompensationFailure
//	expr: increase(current_account_inline_compensation_failures_total[5m]) > 0
//	severity: critical
//	runbook: docs/runbooks/saga-failure-recovery.md
//
// See docs/runbooks/saga-failure-recovery.md for remediation steps.
func RecordInlineCompensationFailure(operation, leg string) {
	inlineCompensationFailuresTotal.WithLabelValues(operation, leg).Inc()
}

// RecordSagaDuration records the duration of a saga execution
func RecordSagaDuration(operation, status string, duration time.Duration) {
	sagaDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordExternalServiceError records an external service error
func RecordExternalServiceError(service, operation string) {
	externalServiceErrors.WithLabelValues(service, operation).Inc()
}

// RecordPartyValidationDuration records the duration of a party validation call
func RecordPartyValidationDuration(duration time.Duration, success bool) {
	successLabel := "false"
	if success {
		successLabel = "true"
	}
	partyValidationDuration.WithLabelValues(successLabel).Observe(duration.Seconds())
}

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState int

const (
	// CircuitBreakerStateClosed indicates the circuit is closed (healthy)
	CircuitBreakerStateClosed CircuitBreakerState = 0
	// CircuitBreakerStateHalfOpen indicates the circuit is testing recovery
	CircuitBreakerStateHalfOpen CircuitBreakerState = 1
	// CircuitBreakerStateOpen indicates the circuit is open (failing fast)
	CircuitBreakerStateOpen CircuitBreakerState = 2
)

// RecordCircuitBreakerState records the current state of a circuit breaker
func RecordCircuitBreakerState(service string, state CircuitBreakerState) {
	circuitBreakerState.WithLabelValues(service).Set(float64(state))
}

// RecordCircuitBreakerStateChange records a circuit breaker state transition
func RecordCircuitBreakerStateChange(service, fromState, toState string) {
	circuitBreakerStateChanges.WithLabelValues(service, fromState, toState).Inc()
}

// RecordClearingAccountCacheHit records a cache hit for clearing account resolution
func RecordClearingAccountCacheHit() {
	clearingAccountCacheHits.Inc()
}

// RecordClearingAccountCacheMiss records a cache miss for clearing account resolution
func RecordClearingAccountCacheMiss() {
	clearingAccountCacheMisses.Inc()
}

// RecordClearingAccountLookupDuration records the duration of a clearing account lookup
func RecordClearingAccountLookupDuration(duration time.Duration) {
	clearingAccountLookupDuration.Observe(duration.Seconds())
}

// RecordClearingAccountLookupError records a clearing account lookup error
func RecordClearingAccountLookupError(clearingType string) {
	clearingAccountLookupErrors.WithLabelValues(clearingType).Inc()
}

// Webhook delivery status constants for bounded cardinality.
const (
	WebhookStatusSuccess = "success"
	WebhookStatusFailed  = "failed"
	WebhookStatusSkipped = "skipped"
)

// Webhook event type constants for bounded cardinality.
const (
	WebhookEventAccountFrozen = "account_frozen"
	WebhookEventAccountClosed = "account_closed"
)

// RecordWebhookDeliveryAttempt records a webhook delivery attempt.
// eventType should be one of the WebhookEvent* constants.
// status should be one of the WebhookStatus* constants.
func RecordWebhookDeliveryAttempt(eventType, status string) {
	webhookDeliveryAttempts.WithLabelValues(eventType, status).Inc()
}

// RecordWebhookDeliveryDuration records the duration of a webhook delivery attempt.
func RecordWebhookDeliveryDuration(eventType string, duration time.Duration) {
	webhookDeliveryDuration.WithLabelValues(eventType).Observe(duration.Seconds())
}

// RecordWebhookDeliveryRetry records a webhook delivery retry attempt.
func RecordWebhookDeliveryRetry(eventType string) {
	webhookDeliveryRetries.WithLabelValues(eventType).Inc()
}

// Service component constants for degradation metrics.
const (
	ComponentIdempotency = "idempotency"
)

// Degradation reason constants.
const (
	DegradationReasonStartupFallback = "startup_fallback"
)

// SetNoopIdempotencyActive sets the gauge indicating whether NoOp idempotency is active.
// This metric MUST trigger a critical alert in production environments.
//
// ALERTING: This metric MUST have a Prometheus alert configured:
//
//	alert: NoopIdempotencyActiveInProduction
//	expr: current_account_noop_idempotency_active == 1 AND environment == "production"
//	severity: critical
//	runbook: docs/runbooks/noop-fallback-active.md
func SetNoopIdempotencyActive(active bool) {
	if active {
		noopIdempotencyActive.Set(1)
	} else {
		noopIdempotencyActive.Set(0)
	}
}

// RecordServiceDegradation records a service degradation event.
func RecordServiceDegradation(component, reason string) {
	serviceDegradationEvents.WithLabelValues(component, reason).Inc()
}

// RecordWebhookDeliveryExhausted records when webhook delivery retries are exhausted.
// This indicates a regulatory compliance risk - freeze/close notifications not delivered.
//
// ALERTING: This metric MUST have a Prometheus alert configured:
//
//	alert: WebhookDeliveryExhausted
//	expr: increase(current_account_webhook_delivery_exhausted_total[5m]) > 0
//	severity: critical
//	runbook: docs/runbooks/webhook-delivery-failure.md
func RecordWebhookDeliveryExhausted(eventType string) {
	webhookDeliveryExhausted.WithLabelValues(eventType).Inc()
}
