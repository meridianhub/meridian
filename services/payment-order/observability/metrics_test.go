package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordPaymentOrder(t *testing.T) {
	paymentOrdersTotal.Reset()

	RecordPaymentOrder("completed")

	count := testutil.CollectAndCount(paymentOrdersTotal)
	if count == 0 {
		t.Error("Expected payment order metric to be recorded")
	}
}

func TestRecordOperationDuration(t *testing.T) {
	operationDuration.Reset()

	RecordOperationDuration("initiate", "success", 100*time.Millisecond)

	count := testutil.CollectAndCount(operationDuration)
	if count == 0 {
		t.Error("Expected operation duration metric to be recorded")
	}
}

func TestRecordPaymentOrderDuration(t *testing.T) {
	paymentOrderDuration.Reset()

	RecordPaymentOrderDuration("initiate", "success", 100*time.Millisecond)

	count := testutil.CollectAndCount(paymentOrderDuration)
	if count == 0 {
		t.Error("Expected payment order duration metric to be recorded")
	}
}

func TestRecordGatewayCallback(t *testing.T) {
	gatewayCallbacksTotal.Reset()

	RecordGatewayCallback("SETTLED", "success")

	count := testutil.CollectAndCount(gatewayCallbacksTotal)
	if count == 0 {
		t.Error("Expected gateway callback metric to be recorded")
	}
}

func TestRecordCompletion(t *testing.T) {
	completionsTotal.Reset()

	RecordCompletion("GBP")

	count := testutil.CollectAndCount(completionsTotal)
	if count == 0 {
		t.Error("Expected completion metric to be recorded")
	}
}

func TestRecordRejection(t *testing.T) {
	rejectionsTotal.Reset()

	RecordRejection("GBP", ErrorCategoryGatewayRejected)

	count := testutil.CollectAndCount(rejectionsTotal)
	if count == 0 {
		t.Error("Expected rejection metric to be recorded")
	}
}

func TestRecordLienExecution(t *testing.T) {
	lienExecutionsTotal.Reset()

	RecordLienExecution("success")

	count := testutil.CollectAndCount(lienExecutionsTotal)
	if count == 0 {
		t.Error("Expected lien execution metric to be recorded")
	}
}

func TestRecordPaymentAmount(t *testing.T) {
	paymentAmountTotal.Reset()

	RecordPaymentAmount("GBP", "completed", 10000)

	count := testutil.CollectAndCount(paymentAmountTotal)
	if count == 0 {
		t.Error("Expected payment amount metric to be recorded")
	}
}

func TestRecordSagaFailure(t *testing.T) {
	sagaFailuresTotal.Reset()

	RecordSagaFailure("reserve_funds")

	count := testutil.CollectAndCount(sagaFailuresTotal)
	if count == 0 {
		t.Error("Expected saga failure metric to be recorded")
	}
}

func TestRecordSagaDuration(t *testing.T) {
	sagaDuration.Reset()

	RecordSagaDuration("success", 500*time.Millisecond)

	count := testutil.CollectAndCount(sagaDuration)
	if count == 0 {
		t.Error("Expected saga duration metric to be recorded")
	}
}

func TestRecordSagaStageDuration(t *testing.T) {
	sagaStageDuration.Reset()

	RecordSagaStageDuration("reserve_funds", "success", 250*time.Millisecond)

	count := testutil.CollectAndCount(sagaStageDuration)
	if count == 0 {
		t.Error("Expected saga stage duration metric to be recorded")
	}
}

func TestRecordSagaCompensation(t *testing.T) {
	sagaCompensationsTotal.Reset()

	RecordSagaCompensation("gateway_rejected")

	count := testutil.CollectAndCount(sagaCompensationsTotal)
	if count == 0 {
		t.Error("Expected saga compensation metric to be recorded")
	}
}

func TestRecordIdempotentRequest(t *testing.T) {
	idempotentRequestsTotal.Reset()

	RecordIdempotentRequest("update_payment_order")

	count := testutil.CollectAndCount(idempotentRequestsTotal)
	if count == 0 {
		t.Error("Expected idempotent request metric to be recorded")
	}
}

func TestRecordLienOperation(t *testing.T) {
	lienOperationsTotal.Reset()

	RecordLienOperation("initiate", "success")

	count := testutil.CollectAndCount(lienOperationsTotal)
	if count == 0 {
		t.Error("Expected lien operation metric to be recorded")
	}
}

func TestRecordLienOperationDuration(t *testing.T) {
	lienOperationDuration.Reset()

	RecordLienOperationDuration("initiate", 50*time.Millisecond)

	count := testutil.CollectAndCount(lienOperationDuration)
	if count == 0 {
		t.Error("Expected lien operation duration metric to be recorded")
	}
}

func TestRecordGatewayLatency(t *testing.T) {
	gatewayRequestDuration.Reset()
	gatewayRequestsTotal.Reset()

	RecordGatewayLatency("accepted", 500*time.Millisecond)

	durationCount := testutil.CollectAndCount(gatewayRequestDuration)
	if durationCount == 0 {
		t.Error("Expected gateway request duration metric to be recorded")
	}

	totalCount := testutil.CollectAndCount(gatewayRequestsTotal)
	if totalCount == 0 {
		t.Error("Expected gateway requests total metric to be recorded")
	}
}

func TestRecordExternalServiceError(t *testing.T) {
	externalServiceErrors.Reset()

	RecordExternalServiceError("current_account", "initiate_lien")

	count := testutil.CollectAndCount(externalServiceErrors)
	if count == 0 {
		t.Error("Expected external service error metric to be recorded")
	}
}

func TestPaymentOrdersInFlight(t *testing.T) {
	// Reset the gauge
	paymentOrdersInFlight.Set(0)

	IncPaymentOrdersInFlight()
	IncPaymentOrdersInFlight()

	// Get the current value
	ch := make(chan prometheus.Metric, 1)
	paymentOrdersInFlight.Collect(ch)
	metric := <-ch

	if metric == nil {
		t.Error("Expected in-flight gauge to be recorded")
	}

	DecPaymentOrdersInFlight()
}

// Tests for bucket evaluation metrics (production readiness monitoring)

func TestRecordBucketEvaluationFailure(t *testing.T) {
	bucketEvaluationFailures.Reset()

	RecordBucketEvaluationFailure(BucketEvalErrCELEvaluation)

	count := testutil.CollectAndCount(bucketEvaluationFailures)
	if count == 0 {
		t.Error("Expected bucket evaluation failure metric to be recorded")
	}
}

func TestRecordBucketEvaluationDuration(t *testing.T) {
	// Note: Histograms don't have Reset(), but we can verify recording doesn't panic
	RecordBucketEvaluationDuration(50 * time.Millisecond)

	count := testutil.CollectAndCount(bucketEvaluationDuration)
	if count == 0 {
		t.Error("Expected bucket evaluation duration metric to be recorded")
	}
}

func TestRecordBucketEvaluation(t *testing.T) {
	bucketEvaluationsTotal.Reset()

	RecordBucketEvaluation(BucketEvalStatusSuccess)
	RecordBucketEvaluation(BucketEvalStatusFallback)
	RecordBucketEvaluation(BucketEvalStatusSkipped)

	count := testutil.CollectAndCount(bucketEvaluationsTotal)
	if count == 0 {
		t.Error("Expected bucket evaluation total metric to be recorded")
	}
}

func TestBucketEvaluationErrorTypes(t *testing.T) {
	bucketEvaluationFailures.Reset()

	// Test all error type constants are valid
	errorTypes := []string{
		BucketEvalErrNoClient,
		BucketEvalErrNoEvaluator,
		BucketEvalErrInstrumentFetch,
		BucketEvalErrCELEvaluation,
	}

	for _, errType := range errorTypes {
		RecordBucketEvaluationFailure(errType)
	}

	count := testutil.CollectAndCount(bucketEvaluationFailures)
	if count != len(errorTypes) {
		t.Errorf("Expected %d bucket evaluation failure metrics, got count result", len(errorTypes))
	}
}

// Tests for lien execution retry metrics (production readiness monitoring)

func TestRecordLienExecutionRetry(t *testing.T) {
	lienExecutionRetries.Reset()

	RecordLienExecutionRetry(LienRetryOutcomeAttempt)
	RecordLienExecutionRetry(LienRetryOutcomeFailed)
	RecordLienExecutionRetry(LienRetryOutcomeSuccess)
	RecordLienExecutionRetry(LienRetryOutcomeExhausted)

	count := testutil.CollectAndCount(lienExecutionRetries)
	if count == 0 {
		t.Error("Expected lien execution retry metric to be recorded")
	}
}

func TestRecordLienExecutionRetriesExhausted(t *testing.T) {
	// Note: prometheus.Counter doesn't have Reset(), but we can verify recording doesn't panic
	RecordLienExecutionRetriesExhausted()

	count := testutil.CollectAndCount(lienExecutionRetriesExhausted)
	if count == 0 {
		t.Error("Expected lien execution retries exhausted metric to be recorded")
	}
}

func TestMetricsLabels(t *testing.T) {
	tests := []struct {
		name       string
		metricFunc func()
		metric     prometheus.Collector
	}{
		{
			name: "payment_order_status_labels",
			metricFunc: func() {
				RecordPaymentOrder("initiated")
			},
			metric: paymentOrdersTotal,
		},
		{
			name: "operation_duration_labels",
			metricFunc: func() {
				RecordOperationDuration("update", "success", 100*time.Millisecond)
			},
			metric: operationDuration,
		},
		{
			name: "payment_order_duration_labels",
			metricFunc: func() {
				RecordPaymentOrderDuration("update", "success", 100*time.Millisecond)
			},
			metric: paymentOrderDuration,
		},
		{
			name: "saga_stage_labels",
			metricFunc: func() {
				RecordSagaStageDuration("send_to_gateway", "failure", 1*time.Second)
			},
			metric: sagaStageDuration,
		},
		{
			name: "lien_operation_labels",
			metricFunc: func() {
				RecordLienOperation("execute", "success")
			},
			metric: lienOperationsTotal,
		},
		{
			name: "gateway_latency_labels",
			metricFunc: func() {
				RecordGatewayLatency("rejected", 2*time.Second)
			},
			metric: gatewayRequestDuration,
		},
		{
			name: "gateway_callback_labels",
			metricFunc: func() {
				RecordGatewayCallback("REJECTED", "error")
			},
			metric: gatewayCallbacksTotal,
		},
		{
			name: "rejection_labels",
			metricFunc: func() {
				RecordRejection("USD", ErrorCategoryInsufficientFunds)
			},
			metric: rejectionsTotal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if resettable, ok := tt.metric.(interface{ Reset() }); ok {
				resettable.Reset()
			}
			tt.metricFunc()

			count := testutil.CollectAndCount(tt.metric)
			if count == 0 {
				t.Errorf("%s: expected metric to be recorded", tt.name)
			}
		})
	}
}

// =============================================================================
// Tests for previously uncovered metric functions
// =============================================================================

func TestSetNoopIdempotencyActive(t *testing.T) {
	SetNoopIdempotencyActive(true)
	count := testutil.CollectAndCount(noopIdempotencyActive)
	if count == 0 {
		t.Error("Expected noop idempotency active metric to be recorded")
	}

	SetNoopIdempotencyActive(false)
	count = testutil.CollectAndCount(noopIdempotencyActive)
	if count == 0 {
		t.Error("Expected noop idempotency active metric to be recorded after deactivation")
	}
}

func TestRecordServiceDegradation(t *testing.T) {
	serviceDegradationEvents.Reset()

	RecordServiceDegradation("idempotency", "noop_fallback")

	count := testutil.CollectAndCount(serviceDegradationEvents)
	if count == 0 {
		t.Error("Expected service degradation metric to be recorded")
	}
}

func TestRecordLienExecutionStatusUpdateExhausted(t *testing.T) {
	RecordLienExecutionStatusUpdateExhausted()

	count := testutil.CollectAndCount(lienExecutionStatusUpdateExhausted)
	if count == 0 {
		t.Error("Expected lien execution status update exhausted metric to be recorded")
	}
}

func TestRecordClearingAccountCacheHit(t *testing.T) {
	RecordClearingAccountCacheHit()

	count := testutil.CollectAndCount(clearingAccountCacheHits)
	if count == 0 {
		t.Error("Expected clearing account cache hit metric to be recorded")
	}
}

func TestRecordClearingAccountCacheMiss(t *testing.T) {
	RecordClearingAccountCacheMiss()

	count := testutil.CollectAndCount(clearingAccountCacheMisses)
	if count == 0 {
		t.Error("Expected clearing account cache miss metric to be recorded")
	}
}

func TestRecordClearingAccountLookupDuration(t *testing.T) {
	RecordClearingAccountLookupDuration(100 * time.Millisecond)

	count := testutil.CollectAndCount(clearingAccountLookupDuration)
	if count == 0 {
		t.Error("Expected clearing account lookup duration metric to be recorded")
	}
}

func TestRecordClearingAccountLookupError(t *testing.T) {
	clearingAccountLookupErrors.Reset()

	RecordClearingAccountLookupError("settlement")

	count := testutil.CollectAndCount(clearingAccountLookupErrors)
	if count == 0 {
		t.Error("Expected clearing account lookup error metric to be recorded")
	}
}

func TestRecordLienExecutionLockContention(t *testing.T) {
	RecordLienExecutionLockContention()

	count := testutil.CollectAndCount(lienExecutionLockContentions)
	if count == 0 {
		t.Error("Expected lien execution lock contention metric to be recorded")
	}
}

func TestRecordLienExecutionLockWaitDuration(t *testing.T) {
	RecordLienExecutionLockWaitDuration(0.5)

	count := testutil.CollectAndCount(lienExecutionLockWaitDuration)
	if count == 0 {
		t.Error("Expected lien execution lock wait duration metric to be recorded")
	}
}
