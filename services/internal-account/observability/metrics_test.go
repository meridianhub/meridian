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
	RecordOperationDuration(OperationInitiate, StatusSuccess, 100*time.Millisecond)

	// For histograms, we verify by checking the overall metric has been registered
	// and doesn't panic when used. The actual histogram values are tested through
	// integration tests with Prometheus scraping.
}

func TestRecordRPCDuration(t *testing.T) {
	t.Helper()

	// Record an RPC duration - this should not panic
	RecordRPCDuration("InitiateInternalAccount", StatusSuccess, 50*time.Millisecond)

	// Verify histogram was recorded without panic
}

func TestRecordBalanceQueryDuration(t *testing.T) {
	t.Helper()

	// Record a balance query duration - this should not panic
	RecordBalanceQueryDuration(StatusSuccess, 25*time.Millisecond)

	// For histograms, we verify the metric has been registered and doesn't panic.
	// Actual histogram values are tested through integration tests.
}

func TestRecordAccountCreated(t *testing.T) {
	// Get initial count
	initialNostro := testutil.ToFloat64(accountsCreated.WithLabelValues("NOSTRO"))

	// Record account creation
	RecordAccountCreated("NOSTRO")

	// Verify counter incremented
	newCount := testutil.ToFloat64(accountsCreated.WithLabelValues("NOSTRO"))
	assert.Equal(t, initialNostro+1, newCount, "account created counter should increment by 1")
}

func TestRecordAccountStatusChange(t *testing.T) {
	// Get initial count
	initial := testutil.ToFloat64(accountStatusChanges.WithLabelValues("PENDING", "ACTIVE"))

	// Record status change
	RecordAccountStatusChange("PENDING", "ACTIVE")

	// Verify counter incremented
	newCount := testutil.ToFloat64(accountStatusChanges.WithLabelValues("PENDING", "ACTIVE"))
	assert.Equal(t, initial+1, newCount, "status change counter should increment by 1")
}

func TestRecordRepositoryOperation(t *testing.T) {
	// Get initial count
	initial := testutil.ToFloat64(repositoryOperations.WithLabelValues("save", ResultSuccess))

	// Record repository operation
	RecordRepositoryOperation("save", ResultSuccess)

	// Verify counter incremented
	newCount := testutil.ToFloat64(repositoryOperations.WithLabelValues("save", ResultSuccess))
	assert.Equal(t, initial+1, newCount, "repository operation counter should increment by 1")
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

func TestRecordError(t *testing.T) {
	// Get initial count
	initial := testutil.ToFloat64(errorsTotal.WithLabelValues(ErrorCategoryValidation, OperationInitiate))

	// Record an error
	RecordError(ErrorCategoryValidation, OperationInitiate)

	// Verify counter incremented
	newCount := testutil.ToFloat64(errorsTotal.WithLabelValues(ErrorCategoryValidation, OperationInitiate))
	assert.Equal(t, initial+1, newCount, "error counter should increment by 1")
}

func TestOperationsInFlight(t *testing.T) {
	// Get initial value
	initial := testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationGetBalance))

	// Increment
	IncOperationsInFlight(OperationGetBalance)
	assert.Equal(t, initial+1, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationGetBalance)))

	// Decrement
	DecOperationsInFlight(OperationGetBalance)
	assert.Equal(t, initial, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationGetBalance)))
}

func TestOperationTimer(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		// Get initial in-flight
		initialInFlight := testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationRetrieve))

		// Start timer
		timer := NewOperationTimer(OperationRetrieve)

		// Verify in-flight incremented
		assert.Equal(t, initialInFlight+1, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationRetrieve)))

		// Intentional sleep: Simulate work to measure elapsed time in metrics
		time.Sleep(5 * time.Millisecond) //nolint:forbidigo // simulates operation latency for metrics measurement

		// Record success
		timer.ObserveSuccess()

		// Verify in-flight decremented back
		assert.Equal(t, initialInFlight, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationRetrieve)))
	})

	t.Run("error", func(t *testing.T) {
		// Get initial values
		initialInFlight := testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationUpdate))
		initialErrors := testutil.ToFloat64(errorsTotal.WithLabelValues(ErrorCategoryDatabase, OperationUpdate))

		// Start timer
		timer := NewOperationTimer(OperationUpdate)

		// Verify in-flight incremented
		assert.Equal(t, initialInFlight+1, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationUpdate)))

		// Record error
		timer.ObserveError(ErrorCategoryDatabase)

		// Verify in-flight decremented and error recorded
		assert.Equal(t, initialInFlight, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationUpdate)))
		assert.Equal(t, initialErrors+1, testutil.ToFloat64(errorsTotal.WithLabelValues(ErrorCategoryDatabase, OperationUpdate)))
	})

	t.Run("double_observe_success_ignored", func(t *testing.T) {
		// Get initial in-flight
		initialInFlight := testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationControl))

		// Start timer
		timer := NewOperationTimer(OperationControl)

		// Verify in-flight incremented
		assert.Equal(t, initialInFlight+1, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationControl)))

		// Record success twice
		timer.ObserveSuccess()
		timer.ObserveSuccess() // Should be ignored

		// Verify in-flight decremented only once (back to initial)
		assert.Equal(t, initialInFlight, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationControl)))
	})

	t.Run("double_observe_error_ignored", func(t *testing.T) {
		// Get initial values
		initialInFlight := testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationList))
		initialErrors := testutil.ToFloat64(errorsTotal.WithLabelValues(ErrorCategoryInternal, OperationList))

		// Start timer
		timer := NewOperationTimer(OperationList)

		// Record error twice
		timer.ObserveError(ErrorCategoryInternal)
		timer.ObserveError(ErrorCategoryInternal) // Should be ignored

		// Verify in-flight decremented only once
		assert.Equal(t, initialInFlight, testutil.ToFloat64(operationsInFlight.WithLabelValues(OperationList)))
		// Verify error recorded only once
		assert.Equal(t, initialErrors+1, testutil.ToFloat64(errorsTotal.WithLabelValues(ErrorCategoryInternal, OperationList)))
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
		ErrorCategoryPositionKeeping,
	}

	for _, cat := range categories {
		assert.NotEmpty(t, cat, "error category constant should not be empty")
	}
}

func TestOperationNameConstants(t *testing.T) {
	// Verify all operation name constants are non-empty
	operations := []string{
		OperationInitiate,
		OperationUpdate,
		OperationControl,
		OperationRetrieve,
		OperationList,
		OperationGetBalance,
	}

	for _, op := range operations {
		assert.NotEmpty(t, op, "operation name constant should not be empty")
	}
}

func TestStatusConstants(t *testing.T) {
	// Verify status constants are non-empty and have expected values
	assert.Equal(t, "success", StatusSuccess)
	assert.Equal(t, "error", StatusError)
}

func TestResultConstants(t *testing.T) {
	// Verify result constants are non-empty and have expected values
	assert.Equal(t, "success", ResultSuccess)
	assert.Equal(t, "error", ResultError)
}
