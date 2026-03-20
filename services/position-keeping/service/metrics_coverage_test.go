package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/position-keeping/service"
)

func TestRecordValidationFailure(t *testing.T) {
	// Should not panic when called with valid arguments
	require.NotPanics(t, func() {
		service.RecordValidationFailure("KWH", service.ValidationFailureReasonCELRejected)
	})
	require.NotPanics(t, func() {
		service.RecordValidationFailure("GBP", service.ValidationFailureReasonCELError)
	})
	require.NotPanics(t, func() {
		service.RecordValidationFailure("USD", service.ValidationFailureReasonInstrumentNotFound)
	})
	require.NotPanics(t, func() {
		service.RecordValidationFailure("EUR", service.ValidationFailureReasonBucketKeyError)
	})
}

func TestRecordCardinalityViolation(t *testing.T) {
	require.NotPanics(t, func() {
		service.RecordCardinalityViolation("KWH")
	})
}

func TestRecordOpeningBalanceValidationFailure(t *testing.T) {
	require.NotPanics(t, func() {
		service.RecordOpeningBalanceValidationFailure("GBP", service.ValidationFailureReasonCELRejected)
	})
}

func TestExposeMetricsForTesting(t *testing.T) {
	metrics := service.ExposeMetricsForTesting
	assert.NotNil(t, metrics.MeasurementValidationFailuresTotal)
	assert.NotNil(t, metrics.BucketCardinalityViolationsTotal)
	assert.NotNil(t, metrics.OpeningBalanceValidationFailuresTotal)
}

func TestValidationFailureReasonConstants(t *testing.T) {
	assert.Equal(t, "cel_rejected", service.ValidationFailureReasonCELRejected)
	assert.Equal(t, "cel_error", service.ValidationFailureReasonCELError)
	assert.Equal(t, "instrument_not_found", service.ValidationFailureReasonInstrumentNotFound)
	assert.Equal(t, "bucket_key_error", service.ValidationFailureReasonBucketKeyError)
}
