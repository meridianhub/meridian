// Package observability provides Prometheus metrics and monitoring for the FinancialAccounting service.
package observability

import (
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
		[]string{"result"},
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

// RecordBookingLog records a booking log creation with status.
func RecordBookingLog(status string) {
	bookingLogsTotal.WithLabelValues(status).Inc()
}

// RecordDoubleEntryValidation records the result of a double-entry balance validation.
// result should be "balanced" or "unbalanced".
func RecordDoubleEntryValidation(result string) {
	doubleEntryValidationsTotal.WithLabelValues(result).Inc()
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
type OperationTimer struct {
	operation string
	start     time.Time
}

// NewOperationTimer creates a new timer and increments the in-flight gauge.
func NewOperationTimer(operation string) *OperationTimer {
	IncOperationsInFlight(operation)
	return &OperationTimer{
		operation: operation,
		start:     time.Now(),
	}
}

// ObserveSuccess records a successful operation and decrements in-flight gauge.
func (t *OperationTimer) ObserveSuccess() {
	DecOperationsInFlight(t.operation)
	RecordOperationDuration(t.operation, StatusSuccess, time.Since(t.start))
}

// ObserveError records a failed operation with error category and decrements in-flight gauge.
func (t *OperationTimer) ObserveError(errorCategory string) {
	DecOperationsInFlight(t.operation)
	RecordOperationDuration(t.operation, StatusError, time.Since(t.start))
	RecordError(errorCategory, t.operation)
}
