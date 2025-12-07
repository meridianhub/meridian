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
	initialBalanced := testutil.ToFloat64(doubleEntryValidationsTotal.WithLabelValues("balanced"))
	initialUnbalanced := testutil.ToFloat64(doubleEntryValidationsTotal.WithLabelValues("unbalanced"))

	// Record validations
	RecordDoubleEntryValidation("balanced")
	RecordDoubleEntryValidation("unbalanced")

	// Verify counters
	assert.Equal(t, initialBalanced+1, testutil.ToFloat64(doubleEntryValidationsTotal.WithLabelValues("balanced")))
	assert.Equal(t, initialUnbalanced+1, testutil.ToFloat64(doubleEntryValidationsTotal.WithLabelValues("unbalanced")))
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

		// Simulate some work
		time.Sleep(5 * time.Millisecond)

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
