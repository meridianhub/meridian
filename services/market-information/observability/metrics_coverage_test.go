package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecordPriceBenchmarkUpdate(t *testing.T) {
	// Call should not panic - verifies the metric is properly registered
	assert.NotPanics(t, func() {
		RecordPriceBenchmarkUpdate("gold_fix", "lbma")
	})

	assert.NotPanics(t, func() {
		RecordPriceBenchmarkUpdate("fx_rate", "ecb")
	})
}

func TestRecordPriceQuery(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordPriceQuery("spot")
	})

	assert.NotPanics(t, func() {
		RecordPriceQuery("historical")
	})
}

func TestRecordDataFreshness(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordDataFreshness("fx_rate", 30.5)
	})

	assert.NotPanics(t, func() {
		RecordDataFreshness("commodity_price", 0.0)
	})
}

func TestRecordExternalFeedError(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordExternalFeedError("ecb", "timeout")
	})

	assert.NotPanics(t, func() {
		RecordExternalFeedError("bloomberg", "connection_refused")
	})
}

func TestRecordHealthCheck(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordHealthCheck("database", "healthy")
	})

	assert.NotPanics(t, func() {
		RecordHealthCheck("ecb-api", "unhealthy")
	})
}

func TestRecordOperationDuration_DirectCall(t *testing.T) {
	// Verify the direct recording function works
	assert.NotPanics(t, func() {
		RecordOperationDuration(OperationDefineDataset, StatusSuccess, 100)
	})
}

func TestRecordError_DirectCall(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordError(ErrorCategoryValidation, OperationRecordObservation)
	})
}

func TestIncDecOperationsInFlight(t *testing.T) {
	assert.NotPanics(t, func() {
		IncOperationsInFlight(OperationQueryPrices)
		DecOperationsInFlight(OperationQueryPrices)
	})
}
