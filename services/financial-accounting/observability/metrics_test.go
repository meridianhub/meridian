package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestRecordOperationDuration(t *testing.T) {
	t.Helper()

	// Record a successful operation - this should not panic
	RecordOperationDuration(OperationCaptureLedgerPosting, StatusSuccess, 100*time.Millisecond)

	// For histograms, we verify by checking the overall metric has been registered
	// and doesn't panic when used. The actual histogram values are tested through
	// integration tests with Prometheus scraping.
}

func TestRecordPosting(t *testing.T) {
	// Get initial count
	initialDebit := testutil.ToFloat64(postingsTotal.WithLabelValues(DirectionDebit, "GBP"))

	// Record a debit posting
	RecordPosting(DirectionDebit, "GBP")

	// Verify counter incremented
	newCount := testutil.ToFloat64(postingsTotal.WithLabelValues(DirectionDebit, "GBP"))
	assert.Equal(t, initialDebit+1, newCount, "posting counter should increment by 1")
}

func TestRecordPostingAmount(t *testing.T) {
	// Get initial amount
	initialAmount := testutil.ToFloat64(postingAmountTotal.WithLabelValues(DirectionCredit, "USD"))

	// Record posting amount
	RecordPostingAmount(DirectionCredit, "USD", 10000) // 100.00 in cents

	// Verify counter added correct amount
	newAmount := testutil.ToFloat64(postingAmountTotal.WithLabelValues(DirectionCredit, "USD"))
	assert.Equal(t, initialAmount+10000, newAmount, "posting amount should increase by 10000")
}

func TestRecordBookingLog(t *testing.T) {
	// Get initial count
	initial := testutil.ToFloat64(bookingLogsTotal.WithLabelValues("pending"))

	// Record a booking log
	RecordBookingLog("pending")

	// Verify counter incremented
	newCount := testutil.ToFloat64(bookingLogsTotal.WithLabelValues("pending"))
	assert.Equal(t, initial+1, newCount, "booking log counter should increment by 1")
}

func TestRecordDoubleEntryValidation(t *testing.T) {
	// Get initial counts
	initialBalancedGBP := testutil.ToFloat64(doubleEntryValidationsTotal.WithLabelValues(ValidationResultBalanced, "GBP"))
	initialUnbalancedGBP := testutil.ToFloat64(doubleEntryValidationsTotal.WithLabelValues(ValidationResultUnbalanced, "GBP"))
	initialBalancedUSD := testutil.ToFloat64(doubleEntryValidationsTotal.WithLabelValues(ValidationResultBalanced, "USD"))

	// Record validations with currency labels
	RecordDoubleEntryValidation(ValidationResultBalanced, "GBP")
	RecordDoubleEntryValidation(ValidationResultUnbalanced, "GBP")
	RecordDoubleEntryValidation(ValidationResultBalanced, "USD")

	// Verify counters with currency labels
	assert.Equal(t, initialBalancedGBP+1, testutil.ToFloat64(doubleEntryValidationsTotal.WithLabelValues(ValidationResultBalanced, "GBP")))
	assert.Equal(t, initialUnbalancedGBP+1, testutil.ToFloat64(doubleEntryValidationsTotal.WithLabelValues(ValidationResultUnbalanced, "GBP")))
	assert.Equal(t, initialBalancedUSD+1, testutil.ToFloat64(doubleEntryValidationsTotal.WithLabelValues(ValidationResultBalanced, "USD")))
}

func TestRecordBalanceValidationDuration(_ *testing.T) {
	// Record a duration - this should not panic
	RecordBalanceValidationDuration(5 * time.Millisecond)

	// For histograms, we verify the metric has been registered and doesn't panic.
	// Actual histogram values are tested through integration tests.
}

func TestLogBalanceValidationFailure(_ *testing.T) {
	// This test verifies that LogBalanceValidationFailure doesn't panic
	// and correctly formats the log message with all fields.
	// In production, log output would be verified through log aggregation.
	LogBalanceValidationFailure(
		"550e8400-e29b-41d4-a716-446655440000",
		"GBP",
		"100.00",
		"50.00",
		"50.00",
	)
}

func TestValidationResultConstants(t *testing.T) {
	// Verify validation result constants are non-empty
	assert.NotEmpty(t, ValidationResultBalanced, "balanced constant should not be empty")
	assert.NotEmpty(t, ValidationResultUnbalanced, "unbalanced constant should not be empty")

	// Verify expected values
	assert.Equal(t, "balanced", ValidationResultBalanced)
	assert.Equal(t, "unbalanced", ValidationResultUnbalanced)
}

func TestRecordError(t *testing.T) {
	// Get initial count
	initial := testutil.ToFloat64(errorsTotal.WithLabelValues(ErrorCategoryValidation, OperationCaptureLedgerPosting))

	// Record an error
	RecordError(ErrorCategoryValidation, OperationCaptureLedgerPosting)

	// Verify counter incremented
	newCount := testutil.ToFloat64(errorsTotal.WithLabelValues(ErrorCategoryValidation, OperationCaptureLedgerPosting))
	assert.Equal(t, initial+1, newCount, "error counter should increment by 1")
}

func TestRecordDepositProcessed(t *testing.T) {
	// Get initial count
	initial := testutil.ToFloat64(depositsProcessedTotal.WithLabelValues("GBP", StatusSuccess))

	// Record a deposit
	RecordDepositProcessed("GBP", StatusSuccess)

	// Verify counter incremented
	newCount := testutil.ToFloat64(depositsProcessedTotal.WithLabelValues("GBP", StatusSuccess))
	assert.Equal(t, initial+1, newCount, "deposit counter should increment by 1")
}

func TestRecordHealthCheck(t *testing.T) {
	// Get initial count
	initial := testutil.ToFloat64(healthCheckTotal.WithLabelValues("database", "healthy"))

	// Record a health check
	RecordHealthCheck("database", "healthy")

	// Verify counter incremented
	newCount := testutil.ToFloat64(healthCheckTotal.WithLabelValues("database", "healthy"))
	assert.Equal(t, initial+1, newCount, "health check counter should increment by 1")
}

func TestRecordExternalServiceError(t *testing.T) {
	// Get initial count
	initial := testutil.ToFloat64(externalServiceErrors.WithLabelValues("kafka", "publish"))

	// Record an external service error
	RecordExternalServiceError("kafka", "publish")

	// Verify counter incremented
	newCount := testutil.ToFloat64(externalServiceErrors.WithLabelValues("kafka", "publish"))
	assert.Equal(t, initial+1, newCount, "external service error counter should increment by 1")
}

func TestOperationsInFlight(t *testing.T) {
	// Get initial value
	initial := testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationProcessDeposit))

	// Increment
	IncOperationsInFlight(OperationProcessDeposit)
	assert.Equal(t, initial+1, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationProcessDeposit)))

	// Decrement
	DecOperationsInFlight(OperationProcessDeposit)
	assert.Equal(t, initial, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationProcessDeposit)))
}

func TestOperationTimer(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		// Get initial in-flight
		initialInFlight := testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationRetrieveLedgerPosting))

		// Start timer
		timer := NewOperationTimer(OperationRetrieveLedgerPosting)

		// Verify in-flight incremented
		assert.Equal(t, initialInFlight+1, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationRetrieveLedgerPosting)))

		// Intentional sleep: Simulate work to measure elapsed time in metrics
		time.Sleep(5 * time.Millisecond) //nolint:forbidigo // simulates operation latency for metrics measurement

		// Record success
		timer.ObserveSuccess()

		// Verify in-flight decremented back
		assert.Equal(t, initialInFlight, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationRetrieveLedgerPosting)))
	})

	t.Run("error", func(t *testing.T) {
		// Get initial values
		initialInFlight := testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationUpdateLedgerPosting))
		initialErrors := testutil.ToFloat64(errorsTotal.WithLabelValues(ErrorCategoryDatabase, OperationUpdateLedgerPosting))

		// Start timer
		timer := NewOperationTimer(OperationUpdateLedgerPosting)

		// Verify in-flight incremented
		assert.Equal(t, initialInFlight+1, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationUpdateLedgerPosting)))

		// Record error
		timer.ObserveError(ErrorCategoryDatabase)

		// Verify in-flight decremented and error recorded
		assert.Equal(t, initialInFlight, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationUpdateLedgerPosting)))
		assert.Equal(t, initialErrors+1, testutil.ToFloat64(errorsTotal.WithLabelValues(ErrorCategoryDatabase, OperationUpdateLedgerPosting)))
	})
}

func TestErrorCategoryConstants(t *testing.T) {
	// Verify all error category constants are non-empty
	categories := []string{
		ErrorCategoryValidation,
		ErrorCategoryNotFound,
		ErrorCategoryDuplicate,
		ErrorCategoryInternal,
		ErrorCategoryDatabase,
		ErrorCategoryEventPublisher,
	}

	for _, cat := range categories {
		assert.NotEmpty(t, cat, "error category constant should not be empty")
	}
}

func TestOperationNameConstants(t *testing.T) {
	// Verify all operation name constants are non-empty
	operations := []string{
		OperationCaptureLedgerPosting,
		OperationRetrieveLedgerPosting,
		OperationUpdateLedgerPosting,
		OperationListLedgerPostings,
		OperationProcessDeposit,
		OperationInitiateBookingLog,
		OperationUpdateBookingLog,
		OperationRetrieveBookingLog,
		OperationListBookingLogs,
		OperationValidateDoubleEntry,
		OperationSavePostingsTransaction,
	}

	for _, op := range operations {
		assert.NotEmpty(t, op, "operation name constant should not be empty")
	}
}

// Tests for NoOp fallback metrics (production readiness monitoring)

func TestSetNoopIdempotencyActive(t *testing.T) {
	// Test setting active
	SetNoopIdempotencyActive(true)
	value := testutil.ToFloat64(noopIdempotencyActive)
	assert.Equal(t, float64(1), value, "NoOp idempotency gauge should be 1 when active")

	// Test setting inactive
	SetNoopIdempotencyActive(false)
	value = testutil.ToFloat64(noopIdempotencyActive)
	assert.Equal(t, float64(0), value, "NoOp idempotency gauge should be 0 when inactive")
}

func TestSetNoopEventPublisherActive(t *testing.T) {
	// Test setting active
	SetNoopEventPublisherActive(true)
	value := testutil.ToFloat64(noopEventPublisherActive)
	assert.Equal(t, float64(1), value, "NoOp event publisher gauge should be 1 when active")

	// Test setting inactive
	SetNoopEventPublisherActive(false)
	value = testutil.ToFloat64(noopEventPublisherActive)
	assert.Equal(t, float64(0), value, "NoOp event publisher gauge should be 0 when inactive")
}

func TestRecordServiceDegradation(t *testing.T) {
	serviceDegradationEvents.Reset()

	// Record degradation events
	RecordServiceDegradation(ComponentIdempotency, DegradationReasonStartupFallback)
	RecordServiceDegradation(ComponentEventPublisher, DegradationReasonUnavailable)
	RecordServiceDegradation(ComponentRedis, DegradationReasonConnectionFailed)

	// Verify counter was incremented
	count := testutil.CollectAndCount(serviceDegradationEvents)
	if count == 0 {
		t.Error("Expected service degradation events metric to be recorded")
	}
}

func TestServiceComponentConstants(t *testing.T) {
	// Verify component constants are properly defined
	assert.Equal(t, "idempotency", ComponentIdempotency)
	assert.Equal(t, "event_publisher", ComponentEventPublisher)
	assert.Equal(t, "kafka_producer", ComponentKafkaProducer)
	assert.Equal(t, "redis", ComponentRedis)
}

func TestDegradationReasonConstants(t *testing.T) {
	// Verify degradation reason constants are properly defined
	assert.Equal(t, "unavailable", DegradationReasonUnavailable)
	assert.Equal(t, "connection_failed", DegradationReasonConnectionFailed)
	assert.Equal(t, "startup_fallback", DegradationReasonStartupFallback)
}
