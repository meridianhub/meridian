// Package observability provides Prometheus metrics and monitoring for the FinancialAccounting service.
package observability

import (
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Error category constants for bounded label cardinality.
// Using constants prevents metric cardinality explosion from arbitrary error messages.
const (
	ErrorCategoryValidation     = "validation"
	ErrorCategoryNotFound       = "not_found"
	ErrorCategoryDuplicate      = "duplicate"
	ErrorCategoryInternal       = "internal"
	ErrorCategoryDatabase       = "database"
	ErrorCategoryEventPublisher = "event_publisher"
)

// Operation name constants for consistent metric labeling.
const (
	OperationCaptureLedgerPosting    = "capture_ledger_posting"
	OperationRetrieveLedgerPosting   = "retrieve_ledger_posting"
	OperationUpdateLedgerPosting     = "update_ledger_posting"
	OperationListLedgerPostings      = "list_ledger_postings"
	OperationProcessDeposit          = "process_deposit"
	OperationInitiateBookingLog      = "initiate_booking_log"
	OperationUpdateBookingLog        = "update_booking_log"
	OperationRetrieveBookingLog      = "retrieve_booking_log"
	OperationListBookingLogs         = "list_booking_logs"
	OperationValidateDoubleEntry     = "validate_double_entry"
	OperationSavePostingsTransaction = "save_postings_transaction"
)

// Status constants for operation outcomes.
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

// Posting direction constants for metric labels.
const (
	DirectionDebit  = "debit"
	DirectionCredit = "credit"
)

// Balance validation result constants for metric labels.
const (
	ValidationResultBalanced   = "balanced"
	ValidationResultUnbalanced = "unbalanced"
)

// CurrencyUnknown is used when currency cannot be determined (e.g., no postings).
const CurrencyUnknown = "UNKNOWN"

var (
	// Operation duration metrics
	operationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "financial_accounting_operation_duration_seconds",
			Help:    "Duration of financial accounting operations in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"operation", "status"},
	)

	// Ledger posting metrics
	postingsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_postings_total",
			Help: "Total number of ledger postings created",
		},
		[]string{"direction", "currency"},
	)

	postingAmountTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_posting_amount_cents_total",
			Help: "Total amount posted in cents by currency and direction",
		},
		[]string{"direction", "currency"},
	)

	// Booking log metrics
	bookingLogsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_booking_logs_total",
			Help: "Total number of booking logs created",
		},
		[]string{"status"},
	)

	// Double-entry validation metrics
	doubleEntryValidationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_double_entry_validations_total",
			Help: "Total number of double-entry balance validations",
		},
		[]string{"result", "currency"},
	)

	// Balance validation duration histogram (separate from generic operation duration)
	balanceValidationDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "financial_accounting_balance_validation_duration_seconds",
			Help:    "Duration of balance validation operations in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1},
		},
	)

	// Error metrics
	errorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_errors_total",
			Help: "Total number of errors by category and operation",
		},
		[]string{"category", "operation"},
	)

	// Deposit processing metrics (for Kafka consumer)
	depositsProcessedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_deposits_processed_total",
			Help: "Total number of deposit events processed",
		},
		[]string{"currency", "status"},
	)

	// Health check metrics
	healthCheckTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_health_check_total",
			Help: "Total number of health checks by component and status",
		},
		[]string{"component", "status"},
	)

	// External service error metrics
	externalServiceErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_external_service_errors_total",
			Help: "Total number of external service errors",
		},
		[]string{"service", "operation"},
	)

	// In-flight operations gauge
	operationsInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "financial_accounting_operations_in_flight",
			Help: "Number of operations currently being processed",
		},
		[]string{"operation"},
	)

	// Clearing account resolver metrics
	clearingAccountCacheHits = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "financial_accounting_clearing_account_cache_hits_total",
			Help: "Total number of clearing account cache hits",
		},
	)

	clearingAccountCacheMisses = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "financial_accounting_clearing_account_cache_misses_total",
			Help: "Total number of clearing account cache misses",
		},
	)

	clearingAccountLookupDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "financial_accounting_clearing_account_lookup_duration_seconds",
			Help:    "Duration of clearing account lookups from Internal Account service",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		},
	)

	clearingAccountLookupErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_clearing_account_lookup_errors_total",
			Help: "Total number of clearing account lookup errors",
		},
		[]string{"clearing_type"},
	)

	resolverFallbackTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_resolver_fallback_total",
			Help: "Total number of times the posting service fell back to static clearing account",
		},
		[]string{"instrument_code", "operation"},
	)

	// NoOp fallback metrics - indicates degraded service functionality
	// These metrics track when the service is running with fallback implementations
	// instead of production-ready dependencies (Redis for idempotency, Kafka for events)
	noopIdempotencyActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "financial_accounting_noop_idempotency_active",
			Help: "1 if NoOp idempotency service is active (production risk), 0 otherwise",
		},
	)

	noopEventPublisherActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "financial_accounting_noop_event_publisher_active",
			Help: "1 if NoOp event publisher is active (production risk), 0 otherwise",
		},
	)

	// Service degradation counter - tracks transitions to degraded mode
	serviceDegradationEvents = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_accounting_service_degradation_events_total",
			Help: "Total number of service degradation events by component",
		},
		[]string{"component", "reason"},
	)
)

// RecordOperationDuration records the duration of a financial accounting operation.
func RecordOperationDuration(operation, status string, duration time.Duration) {
	operationDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

// RecordPosting records a ledger posting creation with direction and currency.
func RecordPosting(direction, currency string) {
	postingsTotal.WithLabelValues(direction, currency).Inc()
}

// RecordPostingAmount records the amount of a posting in cents for tracking total volume.
func RecordPostingAmount(direction, currency string, amountCents int64) {
	postingAmountTotal.WithLabelValues(direction, currency).Add(float64(amountCents))
}

// RecordPostingAmountFloat records the amount of a posting as a float for tracking total volume.
// Used for asset-agnostic amounts where the unit may not be cents.
func RecordPostingAmountFloat(direction, instrumentCode string, amount float64) {
	postingAmountTotal.WithLabelValues(direction, instrumentCode).Add(amount)
}

// RecordBookingLog records a booking log creation with status.
func RecordBookingLog(status string) {
	bookingLogsTotal.WithLabelValues(status).Inc()
}

// RecordDoubleEntryValidation records the result of a double-entry balance validation.
// result should be ValidationResultBalanced or ValidationResultUnbalanced.
// currency should be the ISO 4217 currency code (e.g., "GBP", "USD").
func RecordDoubleEntryValidation(result, currency string) {
	doubleEntryValidationsTotal.WithLabelValues(result, currency).Inc()
}

// RecordBalanceValidationDuration records the duration of a balance validation operation.
func RecordBalanceValidationDuration(duration time.Duration) {
	balanceValidationDuration.Observe(duration.Seconds())
}

// LogBalanceValidationFailure logs a structured warning when balance validation fails.
// This provides detailed debugging information for investigating unbalanced postings.
func LogBalanceValidationFailure(bookingLogID, currency, debitTotal, creditTotal, imbalance string) {
	slog.Warn("balance validation failed: unbalanced postings detected",
		"booking_log_id", bookingLogID,
		"currency", currency,
		"debit_total", debitTotal,
		"credit_total", creditTotal,
		"imbalance", imbalance,
	)
}

// RecordError records an error with category and operation context.
func RecordError(category, operation string) {
	errorsTotal.WithLabelValues(category, operation).Inc()
}

// RecordDepositProcessed records processing of a deposit event.
func RecordDepositProcessed(currency, status string) {
	depositsProcessedTotal.WithLabelValues(currency, status).Inc()
}

// RecordHealthCheck records a health check result.
func RecordHealthCheck(component, status string) {
	healthCheckTotal.WithLabelValues(component, status).Inc()
}

// RecordExternalServiceError records an external service error.
func RecordExternalServiceError(service, operation string) {
	externalServiceErrors.WithLabelValues(service, operation).Inc()
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

// RecordClearingAccountCacheHit records a cache hit for clearing account resolution.
func RecordClearingAccountCacheHit() {
	clearingAccountCacheHits.Inc()
}

// RecordClearingAccountCacheMiss records a cache miss for clearing account resolution.
func RecordClearingAccountCacheMiss() {
	clearingAccountCacheMisses.Inc()
}

// RecordClearingAccountLookupDuration records the duration of a clearing account lookup.
func RecordClearingAccountLookupDuration(duration time.Duration) {
	clearingAccountLookupDuration.Observe(duration.Seconds())
}

// RecordClearingAccountLookupError records a clearing account lookup error.
func RecordClearingAccountLookupError(clearingType string) {
	clearingAccountLookupErrors.WithLabelValues(clearingType).Inc()
}

// RecordResolverFallback records when the posting service falls back to static clearing account.
func RecordResolverFallback(instrumentCode, operation string) {
	resolverFallbackTotal.WithLabelValues(instrumentCode, operation).Inc()
}

// Service component constants for degradation metrics.
const (
	ComponentIdempotency    = "idempotency"
	ComponentEventPublisher = "event_publisher"
	ComponentKafkaProducer  = "kafka_producer"
	ComponentRedis          = "redis"
)

// Degradation reason constants.
const (
	DegradationReasonUnavailable      = "unavailable"
	DegradationReasonConnectionFailed = "connection_failed"
	DegradationReasonStartupFallback  = "startup_fallback"
)

// SetNoopIdempotencyActive sets the gauge indicating whether NoOp idempotency is active.
// This metric MUST trigger a critical alert in production environments.
//
// ALERTING: This metric MUST have a Prometheus alert configured:
//
//	alert: NoopIdempotencyActiveInProduction
//	expr: financial_accounting_noop_idempotency_active == 1 AND environment == "production"
//	severity: critical
//	runbook: docs/runbooks/noop-fallback-active.md
func SetNoopIdempotencyActive(active bool) {
	if active {
		noopIdempotencyActive.Set(1)
	} else {
		noopIdempotencyActive.Set(0)
	}
}

// SetNoopEventPublisherActive sets the gauge indicating whether NoOp event publisher is active.
// This metric MUST trigger a critical alert in production environments.
//
// ALERTING: This metric MUST have a Prometheus alert configured:
//
//	alert: NoopEventPublisherActiveInProduction
//	expr: financial_accounting_noop_event_publisher_active == 1 AND environment == "production"
//	severity: critical
//	runbook: docs/runbooks/noop-fallback-active.md
func SetNoopEventPublisherActive(active bool) {
	if active {
		noopEventPublisherActive.Set(1)
	} else {
		noopEventPublisherActive.Set(0)
	}
}

// RecordServiceDegradation records a service degradation event.
// component should be one of the Component* constants.
// reason should be one of the DegradationReason* constants.
func RecordServiceDegradation(component, reason string) {
	serviceDegradationEvents.WithLabelValues(component, reason).Inc()
}
