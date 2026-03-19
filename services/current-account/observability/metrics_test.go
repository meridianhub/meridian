package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordOperationDuration(t *testing.T) {
	// Reset metrics before test
	operationDuration.Reset()

	// Record a metric
	RecordOperationDuration("test_operation", "success", 100*time.Millisecond)

	// Verify metric was recorded
	count := testutil.CollectAndCount(operationDuration)
	if count == 0 {
		t.Error("Expected operation duration metric to be recorded")
	}
}

func TestRecordDeposit(t *testing.T) {
	// Reset metrics before test
	depositsTotal.Reset()

	// Record a metric
	RecordDeposit("GBP")

	// Verify metric was recorded
	count := testutil.CollectAndCount(depositsTotal)
	if count == 0 {
		t.Error("Expected deposit metric to be recorded")
	}
}

func TestRecordBalance(t *testing.T) {
	// Reset metrics before test
	balanceGauge.Reset()

	// Record a metric
	RecordBalance(10000, "GBP")

	// Verify metric was recorded
	count := testutil.CollectAndCount(balanceGauge)
	if count == 0 {
		t.Error("Expected balance metric to be recorded")
	}
}

func TestRecordSagaFailure(t *testing.T) {
	// Reset metrics before test
	sagaFailuresTotal.Reset()

	// Record a metric
	RecordSagaFailure("deposit", "log_position")

	// Verify metric was recorded
	count := testutil.CollectAndCount(sagaFailuresTotal)
	if count == 0 {
		t.Error("Expected saga failure metric to be recorded")
	}
}

func TestRecordSagaCompensation(t *testing.T) {
	// Reset metrics before test
	sagaCompensationsTotal.Reset()

	// Record a metric
	RecordSagaCompensation("deposit", "log_position")

	// Verify metric was recorded
	count := testutil.CollectAndCount(sagaCompensationsTotal)
	if count == 0 {
		t.Error("Expected saga compensation metric to be recorded")
	}
}

func TestRecordSagaDuration(t *testing.T) {
	// Reset metrics before test
	sagaDuration.Reset()

	// Record a metric
	RecordSagaDuration("deposit", "success", 500*time.Millisecond)

	// Verify metric was recorded
	count := testutil.CollectAndCount(sagaDuration)
	if count == 0 {
		t.Error("Expected saga duration metric to be recorded")
	}
}

func TestRecordExternalServiceError(t *testing.T) {
	// Reset metrics before test
	externalServiceErrors.Reset()

	// Record a metric
	RecordExternalServiceError("position_keeping", "update_log")

	// Verify metric was recorded
	count := testutil.CollectAndCount(externalServiceErrors)
	if count == 0 {
		t.Error("Expected external service error metric to be recorded")
	}
}

func TestRecordCircuitBreakerState(t *testing.T) {
	// Reset metrics before test
	circuitBreakerState.Reset()

	tests := []struct {
		name     string
		service  string
		state    CircuitBreakerState
		expected float64
	}{
		{
			name:     "closed state",
			service:  "position-keeping",
			state:    CircuitBreakerStateClosed,
			expected: 0,
		},
		{
			name:     "half-open state",
			service:  "financial-accounting",
			state:    CircuitBreakerStateHalfOpen,
			expected: 1,
		},
		{
			name:     "open state",
			service:  "party",
			state:    CircuitBreakerStateOpen,
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RecordCircuitBreakerState(tt.service, tt.state)

			// Verify metric was recorded
			count := testutil.CollectAndCount(circuitBreakerState)
			if count == 0 {
				t.Error("Expected circuit breaker state metric to be recorded")
			}
		})
	}
}

func TestRecordCircuitBreakerStateChange(t *testing.T) {
	// Reset metrics before test
	circuitBreakerStateChanges.Reset()

	// Record a state change
	RecordCircuitBreakerStateChange("position-keeping", "closed", "open")

	// Verify metric was recorded
	count := testutil.CollectAndCount(circuitBreakerStateChanges)
	if count == 0 {
		t.Error("Expected circuit breaker state change metric to be recorded")
	}

	// Record another state change
	RecordCircuitBreakerStateChange("position-keeping", "open", "half-open")

	count = testutil.CollectAndCount(circuitBreakerStateChanges)
	if count < 2 {
		t.Errorf("Expected at least 2 circuit breaker state changes, got %d", count)
	}
}

func TestCircuitBreakerStateConstants(t *testing.T) {
	// Verify that state constants have expected values
	// These values map to Prometheus gauge values
	if CircuitBreakerStateClosed != 0 {
		t.Errorf("CircuitBreakerStateClosed should be 0, got %d", CircuitBreakerStateClosed)
	}
	if CircuitBreakerStateHalfOpen != 1 {
		t.Errorf("CircuitBreakerStateHalfOpen should be 1, got %d", CircuitBreakerStateHalfOpen)
	}
	if CircuitBreakerStateOpen != 2 {
		t.Errorf("CircuitBreakerStateOpen should be 2, got %d", CircuitBreakerStateOpen)
	}
}

// Tests for webhook delivery metrics (production readiness monitoring)

func TestRecordWebhookDeliveryAttempt(t *testing.T) {
	webhookDeliveryAttempts.Reset()

	RecordWebhookDeliveryAttempt(WebhookEventAccountFrozen, WebhookStatusSuccess)
	RecordWebhookDeliveryAttempt(WebhookEventAccountClosed, WebhookStatusFailed)
	RecordWebhookDeliveryAttempt(WebhookEventAccountFrozen, WebhookStatusSkipped)

	count := testutil.CollectAndCount(webhookDeliveryAttempts)
	if count == 0 {
		t.Error("Expected webhook delivery attempt metric to be recorded")
	}
}

func TestRecordWebhookDeliveryDuration(t *testing.T) {
	webhookDeliveryDuration.Reset()

	RecordWebhookDeliveryDuration(WebhookEventAccountFrozen, 500*time.Millisecond)
	RecordWebhookDeliveryDuration(WebhookEventAccountClosed, 2*time.Second)

	count := testutil.CollectAndCount(webhookDeliveryDuration)
	if count == 0 {
		t.Error("Expected webhook delivery duration metric to be recorded")
	}
}

func TestRecordWebhookDeliveryRetry(t *testing.T) {
	webhookDeliveryRetries.Reset()

	RecordWebhookDeliveryRetry(WebhookEventAccountFrozen)
	RecordWebhookDeliveryRetry(WebhookEventAccountClosed)

	count := testutil.CollectAndCount(webhookDeliveryRetries)
	if count == 0 {
		t.Error("Expected webhook delivery retry metric to be recorded")
	}
}

func TestRecordWebhookDeliveryExhausted(t *testing.T) {
	webhookDeliveryExhausted.Reset()

	RecordWebhookDeliveryExhausted(WebhookEventAccountFrozen)

	count := testutil.CollectAndCount(webhookDeliveryExhausted)
	if count == 0 {
		t.Error("Expected webhook delivery exhausted metric to be recorded")
	}
}

func TestWebhookEventTypeConstants(t *testing.T) {
	// Verify webhook event type constants are properly defined
	if WebhookEventAccountFrozen != "account_frozen" {
		t.Errorf("WebhookEventAccountFrozen should be 'account_frozen', got '%s'", WebhookEventAccountFrozen)
	}
	if WebhookEventAccountClosed != "account_closed" {
		t.Errorf("WebhookEventAccountClosed should be 'account_closed', got '%s'", WebhookEventAccountClosed)
	}
}

func TestWebhookStatusConstants(t *testing.T) {
	// Verify webhook status constants are properly defined
	if WebhookStatusSuccess != "success" {
		t.Errorf("WebhookStatusSuccess should be 'success', got '%s'", WebhookStatusSuccess)
	}
	if WebhookStatusFailed != "failed" {
		t.Errorf("WebhookStatusFailed should be 'failed', got '%s'", WebhookStatusFailed)
	}
	if WebhookStatusSkipped != "skipped" {
		t.Errorf("WebhookStatusSkipped should be 'skipped', got '%s'", WebhookStatusSkipped)
	}
}

func TestMetricsLabels(t *testing.T) {
	tests := []struct {
		name       string
		metricFunc func()
		metric     prometheus.Collector
	}{
		{
			name: "operation_duration_labels",
			metricFunc: func() {
				RecordOperationDuration("test_op", "success", 100*time.Millisecond)
			},
			metric: operationDuration,
		},
		{
			name: "deposit_labels",
			metricFunc: func() {
				RecordDeposit("USD")
			},
			metric: depositsTotal,
		},
		{
			name: "saga_failure_labels",
			metricFunc: func() {
				RecordSagaFailure("withdraw", "save_account")
			},
			metric: sagaFailuresTotal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset and record
			if resettable, ok := tt.metric.(interface{ Reset() }); ok {
				resettable.Reset()
			}
			tt.metricFunc()

			// Verify
			count := testutil.CollectAndCount(tt.metric)
			if count == 0 {
				t.Errorf("%s: expected metric to be recorded", tt.name)
			}
		})
	}
}

func TestRecordWithdrawal(t *testing.T) {
	withdrawalsTotal.Reset()

	RecordWithdrawal("GBP")

	count := testutil.CollectAndCount(withdrawalsTotal)
	if count == 0 {
		t.Error("Expected withdrawal metric to be recorded")
	}
}

func TestRecordPartyValidationDuration(t *testing.T) {
	partyValidationDuration.Reset()

	RecordPartyValidationDuration(100*time.Millisecond, true)
	RecordPartyValidationDuration(200*time.Millisecond, false)

	count := testutil.CollectAndCount(partyValidationDuration)
	if count == 0 {
		t.Error("Expected party validation duration metric to be recorded")
	}
}

func TestRecordInlineCompensationFailure(t *testing.T) {
	inlineCompensationFailuresTotal.Reset()

	RecordInlineCompensationFailure("deposit", "clearing_leg")

	count := testutil.CollectAndCount(inlineCompensationFailuresTotal)
	if count == 0 {
		t.Error("Expected inline compensation failure metric to be recorded")
	}
}

func TestSetNoopIdempotencyActive(t *testing.T) {
	noopIdempotencyActive.Set(0) // Reset

	SetNoopIdempotencyActive(true)
	val := testutil.ToFloat64(noopIdempotencyActive)
	if val != 1 {
		t.Errorf("Expected noop idempotency gauge to be 1 when active, got %f", val)
	}

	SetNoopIdempotencyActive(false)
	val = testutil.ToFloat64(noopIdempotencyActive)
	if val != 0 {
		t.Errorf("Expected noop idempotency gauge to be 0 when inactive, got %f", val)
	}
}

func TestRecordServiceDegradation(t *testing.T) {
	serviceDegradationEvents.Reset()

	RecordServiceDegradation(ComponentIdempotency, DegradationReasonStartupFallback)

	count := testutil.CollectAndCount(serviceDegradationEvents)
	if count == 0 {
		t.Error("Expected service degradation metric to be recorded")
	}
}

func TestRecordClearingAccountCacheHit(t *testing.T) {
	// Just verify it does not panic
	RecordClearingAccountCacheHit()
}

func TestRecordClearingAccountCacheMiss(t *testing.T) {
	RecordClearingAccountCacheMiss()
}

func TestRecordClearingAccountLookupDuration(t *testing.T) {
	RecordClearingAccountLookupDuration(50 * time.Millisecond)
}

func TestRecordClearingAccountLookupError(t *testing.T) {
	clearingAccountLookupErrors.Reset()

	RecordClearingAccountLookupError("deposit")

	count := testutil.CollectAndCount(clearingAccountLookupErrors)
	if count == 0 {
		t.Error("Expected clearing account lookup error metric to be recorded")
	}
}

func TestDegradationConstants(t *testing.T) {
	if ComponentIdempotency != "idempotency" {
		t.Errorf("ComponentIdempotency should be 'idempotency', got '%s'", ComponentIdempotency)
	}
	if DegradationReasonStartupFallback != "startup_fallback" {
		t.Errorf("DegradationReasonStartupFallback should be 'startup_fallback', got '%s'", DegradationReasonStartupFallback)
	}
}
