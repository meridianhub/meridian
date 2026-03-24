package service_test

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/position-keeping/service"
)

func TestRecordValidationFailure_IncrementsByCaller(t *testing.T) {
	metrics := service.ExposeMetricsForTesting

	// Gather current state
	before, err := metrics.MeasurementValidationFailuresTotal.GetMetricWithLabelValues("TEST_INST_INC", service.ValidationFailureReasonCELRejected)
	require.NoError(t, err)
	beforeMetric := &dto.Metric{}
	require.NoError(t, before.Write(beforeMetric))
	var beforeVal float64
	if beforeMetric.Counter != nil && beforeMetric.Counter.Value != nil {
		beforeVal = *beforeMetric.Counter.Value
	}

	service.RecordValidationFailure("TEST_INST_INC", service.ValidationFailureReasonCELRejected)
	service.RecordValidationFailure("TEST_INST_INC", service.ValidationFailureReasonCELRejected)

	after, err := metrics.MeasurementValidationFailuresTotal.GetMetricWithLabelValues("TEST_INST_INC", service.ValidationFailureReasonCELRejected)
	require.NoError(t, err)
	afterMetric := &dto.Metric{}
	require.NoError(t, after.Write(afterMetric))
	var afterVal float64
	if afterMetric.Counter != nil && afterMetric.Counter.Value != nil {
		afterVal = *afterMetric.Counter.Value
	}

	assert.Equal(t, beforeVal+2, afterVal, "counter should increment by 2")
}

func TestRecordCardinalityViolation_Increments(t *testing.T) {
	metrics := service.ExposeMetricsForTesting

	before, err := metrics.BucketCardinalityViolationsTotal.GetMetricWithLabelValues("CARD_TEST_INST")
	require.NoError(t, err)
	beforeMetric := &dto.Metric{}
	require.NoError(t, before.Write(beforeMetric))
	var beforeVal float64
	if beforeMetric.Counter != nil && beforeMetric.Counter.Value != nil {
		beforeVal = *beforeMetric.Counter.Value
	}

	service.RecordCardinalityViolation("CARD_TEST_INST")

	after, err := metrics.BucketCardinalityViolationsTotal.GetMetricWithLabelValues("CARD_TEST_INST")
	require.NoError(t, err)
	afterMetric := &dto.Metric{}
	require.NoError(t, after.Write(afterMetric))
	var afterVal float64
	if afterMetric.Counter != nil && afterMetric.Counter.Value != nil {
		afterVal = *afterMetric.Counter.Value
	}

	assert.Equal(t, beforeVal+1, afterVal, "cardinality counter should increment by 1")
}

func TestRecordOpeningBalanceValidationFailure_Increments(t *testing.T) {
	metrics := service.ExposeMetricsForTesting

	before, err := metrics.OpeningBalanceValidationFailuresTotal.GetMetricWithLabelValues("OB_TEST_INST", service.ValidationFailureReasonInstrumentNotFound)
	require.NoError(t, err)
	beforeMetric := &dto.Metric{}
	require.NoError(t, before.Write(beforeMetric))
	var beforeVal float64
	if beforeMetric.Counter != nil && beforeMetric.Counter.Value != nil {
		beforeVal = *beforeMetric.Counter.Value
	}

	service.RecordOpeningBalanceValidationFailure("OB_TEST_INST", service.ValidationFailureReasonInstrumentNotFound)
	service.RecordOpeningBalanceValidationFailure("OB_TEST_INST", service.ValidationFailureReasonInstrumentNotFound)
	service.RecordOpeningBalanceValidationFailure("OB_TEST_INST", service.ValidationFailureReasonInstrumentNotFound)

	after, err := metrics.OpeningBalanceValidationFailuresTotal.GetMetricWithLabelValues("OB_TEST_INST", service.ValidationFailureReasonInstrumentNotFound)
	require.NoError(t, err)
	afterMetric := &dto.Metric{}
	require.NoError(t, after.Write(afterMetric))
	var afterVal float64
	if afterMetric.Counter != nil && afterMetric.Counter.Value != nil {
		afterVal = *afterMetric.Counter.Value
	}

	assert.Equal(t, beforeVal+3, afterVal, "opening balance counter should increment by 3")
}

func TestValidationFailure_DifferentLabelsCounted_Separately(t *testing.T) {
	metrics := service.ExposeMetricsForTesting

	// Read initial values for two distinct label pairs
	getVal := func(inst, reason string) float64 {
		m, err := metrics.MeasurementValidationFailuresTotal.GetMetricWithLabelValues(inst, reason)
		require.NoError(t, err)
		metric := &dto.Metric{}
		require.NoError(t, m.Write(metric))
		if metric.Counter != nil && metric.Counter.Value != nil {
			return *metric.Counter.Value
		}
		return 0
	}

	beforeA := getVal("INST_A_SEPARATE", service.ValidationFailureReasonCELError)
	beforeB := getVal("INST_B_SEPARATE", service.ValidationFailureReasonCELRejected)

	service.RecordValidationFailure("INST_A_SEPARATE", service.ValidationFailureReasonCELError)

	afterA := getVal("INST_A_SEPARATE", service.ValidationFailureReasonCELError)
	afterB := getVal("INST_B_SEPARATE", service.ValidationFailureReasonCELRejected)

	assert.Equal(t, beforeA+1, afterA, "INST_A counter should increment")
	assert.Equal(t, beforeB, afterB, "INST_B counter should not change")
}
