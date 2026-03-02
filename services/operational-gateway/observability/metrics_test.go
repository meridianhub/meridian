package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordInstruction(t *testing.T) {
	instructionsTotal.Reset()

	RecordInstruction("tenant-a", "PAYMENT_INITIATION", "ACKNOWLEDGED")
	RecordInstruction("tenant-a", "PAYMENT_INITIATION", "FAILED")
	RecordInstruction("tenant-b", "REFUND", "ACKNOWLEDGED")

	count := testutil.CollectAndCount(instructionsTotal)
	if count != 3 {
		t.Errorf("expected 3 series, got %d", count)
	}
}

func TestRecordDispatchDuration(t *testing.T) {
	dispatchDuration.Reset()

	RecordDispatchDuration("tenant-a", "acme-bank", 150*time.Millisecond)
	RecordDispatchDuration("tenant-a", "acme-bank", 300*time.Millisecond)
	RecordDispatchDuration("tenant-b", "energy-co", 50*time.Millisecond)

	count := testutil.CollectAndCount(dispatchDuration)
	if count == 0 {
		t.Error("expected dispatch duration metrics to be recorded")
	}
}

func TestRecordDispatchAttempt(t *testing.T) {
	dispatchAttemptsTotal.Reset()

	RecordDispatchAttempt("tenant-a", "acme-bank", DispatchOutcomeSuccess)
	RecordDispatchAttempt("tenant-a", "acme-bank", DispatchOutcomeRetry)
	RecordDispatchAttempt("tenant-a", "acme-bank", DispatchOutcomeFailure)
	RecordDispatchAttempt("tenant-b", "energy-co", DispatchOutcomeCircuitOpen)

	count := testutil.CollectAndCount(dispatchAttemptsTotal)
	if count != 4 {
		t.Errorf("expected 4 series, got %d", count)
	}
}

func TestRecordCircuitBreakerState(t *testing.T) {
	circuitBreakerState.Reset()

	tests := []struct {
		name        string
		state       CircuitBreakerStateValue
		activeLabel string
		inactiveA   string
		inactiveB   string
	}{
		{
			name:        "closed state",
			state:       CircuitBreakerClosed,
			activeLabel: "closed",
			inactiveA:   "half_open",
			inactiveB:   "open",
		},
		{
			name:        "half-open state",
			state:       CircuitBreakerHalfOpen,
			activeLabel: "half_open",
			inactiveA:   "closed",
			inactiveB:   "open",
		},
		{
			name:        "open state",
			state:       CircuitBreakerOpen,
			activeLabel: "open",
			inactiveA:   "closed",
			inactiveB:   "half_open",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			circuitBreakerState.Reset()
			RecordCircuitBreakerState("tenant-a", "conn-1", tt.state)

			// Verify the active label has value 1
			active := testutil.ToFloat64(circuitBreakerState.WithLabelValues("tenant-a", "conn-1", tt.activeLabel))
			if active != 1 {
				t.Errorf("expected %s gauge to be 1, got %f", tt.activeLabel, active)
			}

			// Verify inactive labels are set to 0
			inactiveAVal := testutil.ToFloat64(circuitBreakerState.WithLabelValues("tenant-a", "conn-1", tt.inactiveA))
			if inactiveAVal != 0 {
				t.Errorf("expected %s gauge to be 0, got %f", tt.inactiveA, inactiveAVal)
			}

			inactiveBVal := testutil.ToFloat64(circuitBreakerState.WithLabelValues("tenant-a", "conn-1", tt.inactiveB))
			if inactiveBVal != 0 {
				t.Errorf("expected %s gauge to be 0, got %f", tt.inactiveB, inactiveBVal)
			}
		})
	}
}

func TestSetActiveInstructions(t *testing.T) {
	activeInstructions.Reset()

	SetActiveInstructions("tenant-a", "PENDING", 10)
	SetActiveInstructions("tenant-a", "DISPATCHING", 5)
	SetActiveInstructions("tenant-b", "RETRYING", 2)

	pendingVal := testutil.ToFloat64(activeInstructions.WithLabelValues("tenant-a", "PENDING"))
	if pendingVal != 10 {
		t.Errorf("expected PENDING gauge to be 10, got %f", pendingVal)
	}

	dispatchingVal := testutil.ToFloat64(activeInstructions.WithLabelValues("tenant-a", "DISPATCHING"))
	if dispatchingVal != 5 {
		t.Errorf("expected DISPATCHING gauge to be 5, got %f", dispatchingVal)
	}
}

func TestIncrDecrActiveInstructions(t *testing.T) {
	activeInstructions.Reset()

	IncrActiveInstructions("tenant-a", "PENDING")
	IncrActiveInstructions("tenant-a", "PENDING")
	IncrActiveInstructions("tenant-a", "PENDING")

	val := testutil.ToFloat64(activeInstructions.WithLabelValues("tenant-a", "PENDING"))
	if val != 3 {
		t.Errorf("expected gauge to be 3 after 3 increments, got %f", val)
	}

	DecrActiveInstructions("tenant-a", "PENDING")

	val = testutil.ToFloat64(activeInstructions.WithLabelValues("tenant-a", "PENDING"))
	if val != 2 {
		t.Errorf("expected gauge to be 2 after decrement, got %f", val)
	}
}

func TestDispatchOutcomeConstants(t *testing.T) {
	if DispatchOutcomeSuccess != "success" {
		t.Errorf("DispatchOutcomeSuccess should be 'success', got %q", DispatchOutcomeSuccess)
	}
	if DispatchOutcomeRetry != "retry" {
		t.Errorf("DispatchOutcomeRetry should be 'retry', got %q", DispatchOutcomeRetry)
	}
	if DispatchOutcomeFailure != "failure" {
		t.Errorf("DispatchOutcomeFailure should be 'failure', got %q", DispatchOutcomeFailure)
	}
	if DispatchOutcomeCircuitOpen != "circuit_open" {
		t.Errorf("DispatchOutcomeCircuitOpen should be 'circuit_open', got %q", DispatchOutcomeCircuitOpen)
	}
}

func TestCircuitBreakerStateValueConstants(t *testing.T) {
	if CircuitBreakerClosed != 0 {
		t.Errorf("CircuitBreakerClosed should be 0, got %v", CircuitBreakerClosed)
	}
	if CircuitBreakerHalfOpen != 1 {
		t.Errorf("CircuitBreakerHalfOpen should be 1, got %v", CircuitBreakerHalfOpen)
	}
	if CircuitBreakerOpen != 2 {
		t.Errorf("CircuitBreakerOpen should be 2, got %v", CircuitBreakerOpen)
	}
}
