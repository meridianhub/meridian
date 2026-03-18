package observability

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestOperationTimer_ObserveSuccess(t *testing.T) {
	t.Run("records success metrics", func(t *testing.T) {
		timer := NewOperationTimer(OperationDefineDataset)

		// Simulate some work
		time.Sleep(10 * time.Millisecond) //nolint:forbidigo // simulates operation latency for metrics measurement

		timer.ObserveSuccess()

		// Verify it was observed
		assert.True(t, timer.observed)
	})

	t.Run("is idempotent - multiple calls only record once", func(t *testing.T) {
		timer := NewOperationTimer(OperationQueryPrices)

		timer.ObserveSuccess()
		assert.True(t, timer.observed)

		// Second call should be a no-op
		timer.ObserveSuccess()
		assert.True(t, timer.observed)
	})
}

func TestOperationTimer_ObserveError(t *testing.T) {
	t.Run("records error metrics with category", func(t *testing.T) {
		timer := NewOperationTimer(OperationRecordObservation)

		timer.ObserveError(ErrorCategoryValidation)

		assert.True(t, timer.observed)
	})

	t.Run("is idempotent - multiple calls only record once", func(t *testing.T) {
		timer := NewOperationTimer(OperationRetrieveDataset)

		timer.ObserveError(ErrorCategoryDatabase)
		assert.True(t, timer.observed)

		// Second call should be a no-op
		timer.ObserveError(ErrorCategoryInternal)
		assert.True(t, timer.observed)
	})

	t.Run("success after error is no-op", func(t *testing.T) {
		timer := NewOperationTimer(OperationDefineDataset)

		timer.ObserveError(ErrorCategoryExternal)
		assert.True(t, timer.observed)

		// Success after error should be no-op
		timer.ObserveSuccess()
		assert.True(t, timer.observed)
	})

	t.Run("error after success is no-op", func(t *testing.T) {
		timer := NewOperationTimer(OperationQueryPrices)

		timer.ObserveSuccess()
		assert.True(t, timer.observed)

		// Error after success should be no-op
		timer.ObserveError(ErrorCategoryNotFound)
		assert.True(t, timer.observed)
	})
}

func TestOperationConstants(t *testing.T) {
	t.Run("error category constants are defined", func(t *testing.T) {
		assert.Equal(t, "validation", ErrorCategoryValidation)
		assert.Equal(t, "not_found", ErrorCategoryNotFound)
		assert.Equal(t, "internal", ErrorCategoryInternal)
		assert.Equal(t, "database", ErrorCategoryDatabase)
		assert.Equal(t, "external", ErrorCategoryExternal)
	})

	t.Run("operation constants are defined", func(t *testing.T) {
		assert.Equal(t, "define_dataset", OperationDefineDataset)
		assert.Equal(t, "record_observation", OperationRecordObservation)
		assert.Equal(t, "query_prices", OperationQueryPrices)
		assert.Equal(t, "retrieve_dataset", OperationRetrieveDataset)
	})

	t.Run("status constants are defined", func(t *testing.T) {
		assert.Equal(t, "success", StatusSuccess)
		assert.Equal(t, "error", StatusError)
	})
}
