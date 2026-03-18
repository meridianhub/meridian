package observability

import (
	"testing"
	"time"
)

func TestRecordPayment(t *testing.T) {
	// Verify no panic
	RecordPayment("tenant-a", "stripe", "DELIVERED")
	RecordPayment("tenant-a", "stripe", "FAILED")
}

func TestRecordDispatchDuration(t *testing.T) {
	RecordDispatchDuration("tenant-a", "stripe", 150*time.Millisecond)
	RecordDispatchDuration("tenant-a", "stripe", 2*time.Second)
}

func TestRecordDispatchAttempt(t *testing.T) {
	RecordDispatchAttempt("tenant-a", "stripe", DispatchOutcomeSuccess)
	RecordDispatchAttempt("tenant-a", "stripe", DispatchOutcomeRetry)
	RecordDispatchAttempt("tenant-a", "stripe", DispatchOutcomeFailure)
	RecordDispatchAttempt("tenant-a", "stripe", DispatchOutcomeCircuitOpen)
	RecordDispatchAttempt("tenant-a", "stripe", "bogus_outcome") // unknown → mapped to "unknown"
}

func TestRecordCircuitBreakerState(t *testing.T) {
	RecordCircuitBreakerState("tenant-a", "stripe", "closed")
	RecordCircuitBreakerState("tenant-a", "stripe", "half_open")
	RecordCircuitBreakerState("tenant-a", "stripe", "open")
	RecordCircuitBreakerState("tenant-a", "stripe", "invalid_state") // silently ignored
}

func TestSetActiveDispatches(t *testing.T) {
	SetActiveDispatches("tenant-a", "DISPATCHING", 5)
	SetActiveDispatches("tenant-a", "DISPATCHING", 0)
	SetActiveDispatches("tenant-a", "DISPATCHING", -1) // clamped to 0
}
