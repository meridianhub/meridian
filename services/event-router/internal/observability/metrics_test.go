package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordEventReceived(t *testing.T) {
	eventsReceivedTotal.Reset()

	RecordEventReceived("payments")
	RecordEventReceived("payments")
	RecordEventReceived("accounts")

	if got := testutil.ToFloat64(eventsReceivedTotal.WithLabelValues("payments")); got != 2.0 {
		t.Errorf("payments count = %v, want 2.0", got)
	}
	if got := testutil.ToFloat64(eventsReceivedTotal.WithLabelValues("accounts")); got != 1.0 {
		t.Errorf("accounts count = %v, want 1.0", got)
	}
}

func TestRecordSagaTriggered(t *testing.T) {
	sagasTriggeredTotal.Reset()

	RecordSagaTriggered("payment_saga", "payments")
	RecordSagaTriggered("payment_saga", "payments")
	RecordSagaTriggered("account_saga", "accounts")

	if got := testutil.ToFloat64(sagasTriggeredTotal.WithLabelValues("payment_saga", "payments")); got != 2.0 {
		t.Errorf("payment_saga/payments count = %v, want 2.0", got)
	}
	if got := testutil.ToFloat64(sagasTriggeredTotal.WithLabelValues("account_saga", "accounts")); got != 1.0 {
		t.Errorf("account_saga/accounts count = %v, want 1.0", got)
	}
}

func TestRecordFilterEvaluationDuration(_ *testing.T) {
	// Smoke test: verify no panic on observation.
	RecordFilterEvaluationDuration("payment_saga", 0.001)
	RecordFilterEvaluationDuration("payment_saga", 0.005)
}

func TestRecordFilterEvaluationError(t *testing.T) {
	filterEvaluationErrorsTotal.Reset()

	RecordFilterEvaluationError("broken_saga")
	RecordFilterEvaluationError("broken_saga")

	if got := testutil.ToFloat64(filterEvaluationErrorsTotal.WithLabelValues("broken_saga")); got != 2.0 {
		t.Errorf("broken_saga filter error count = %v, want 2.0", got)
	}
}

func TestRecordChainDepthExceeded(t *testing.T) {
	// Cannot reset a promauto.NewCounter — read delta instead.
	before := testutil.ToFloat64(chainDepthExceededTotal)
	RecordChainDepthExceeded()
	after := testutil.ToFloat64(chainDepthExceededTotal)
	if after-before != 1.0 {
		t.Errorf("chainDepthExceededTotal delta = %v, want 1.0", after-before)
	}
}

func TestRecordSagaTriggerFailure(t *testing.T) {
	sagaTriggerFailuresTotal.Reset()

	RecordSagaTriggerFailure("failing_saga", "payments")
	RecordSagaTriggerFailure("failing_saga", "payments")

	if got := testutil.ToFloat64(sagaTriggerFailuresTotal.WithLabelValues("failing_saga", "payments")); got != 2.0 {
		t.Errorf("failing_saga trigger failure count = %v, want 2.0", got)
	}
}

func TestRecordDuplicateEvent(t *testing.T) {
	duplicateEventsTotal.Reset()

	RecordDuplicateEvent("idempotent_saga")
	RecordDuplicateEvent("idempotent_saga")

	if got := testutil.ToFloat64(duplicateEventsTotal.WithLabelValues("idempotent_saga")); got != 2.0 {
		t.Errorf("idempotent_saga duplicate count = %v, want 2.0", got)
	}
}
