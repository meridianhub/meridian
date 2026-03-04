package dispatch

import (
	"errors"
	"time"
)

// ErrInvalidThreshold is returned when a failure threshold of zero or less is used.
var ErrInvalidThreshold = errors.New("threshold must be greater than zero")

// CircuitBreaker implements a per-connection circuit breaker state machine.
// It tracks consecutive failures and transitions between closed, open, and half-open
// states to protect downstream systems from cascading failures.
//
// CircuitBreaker is NOT safe for concurrent use. Callers must ensure that all
// method calls for a given instance are serialized (e.g., by the owning worker
// goroutine that processes instructions for a single connection).
//
// State transitions:
//
//	CLOSED → OPEN: when failure count reaches threshold
//	OPEN → HALF_OPEN: via AttemptReset (allows a probe request)
//	HALF_OPEN → CLOSED: on successful probe (RecordSuccess)
//	HALF_OPEN → OPEN: on failed probe (RecordFailure)
type CircuitBreaker struct {
	state        CircuitState
	openedAt     *time.Time
	failureCount int
	successCount int
	lastUpdated  time.Time
}

// NewCircuitBreaker creates a new CircuitBreaker in the closed state.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		state:       CircuitStateClosed,
		lastUpdated: time.Now().UTC(),
	}
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() CircuitState {
	return cb.state
}

// OpenedAt returns the time the circuit was opened, or nil if it has not been tripped.
func (cb *CircuitBreaker) OpenedAt() *time.Time {
	return cb.openedAt
}

// FailureCount returns the count of consecutive failures since the last success.
func (cb *CircuitBreaker) FailureCount() int {
	return cb.failureCount
}

// SuccessCount returns the total number of recorded successes.
func (cb *CircuitBreaker) SuccessCount() int {
	return cb.successCount
}

// LastUpdated returns the time of the most recent state change.
func (cb *CircuitBreaker) LastUpdated() time.Time {
	return cb.lastUpdated
}

// RecordSuccess records a successful request. When the circuit is closed or half-open,
// it resets the failure count. In half-open state, a success closes the circuit
// (confirming recovery).
func (cb *CircuitBreaker) RecordSuccess() {
	cb.successCount++
	switch cb.state {
	case CircuitStateHalfOpen:
		cb.state = CircuitStateClosed
		cb.failureCount = 0
		cb.openedAt = nil
	case CircuitStateClosed:
		cb.failureCount = 0
	case CircuitStateOpen:
		// Success during open state is unexpected (IsAvailable returns false).
		// Record it but do not change circuit state; use AttemptReset first.
	}
	cb.lastUpdated = time.Now().UTC()
}

// RecordFailure records a failed request and trips the circuit breaker if the failure
// count reaches the given threshold. In half-open state, any failure immediately
// re-trips the circuit. Returns ErrInvalidThreshold if threshold <= 0.
func (cb *CircuitBreaker) RecordFailure(threshold int) error {
	if threshold <= 0 {
		return ErrInvalidThreshold
	}
	cb.failureCount++
	switch cb.state {
	case CircuitStateClosed:
		if cb.failureCount >= threshold {
			cb.TripCircuit()
			return nil
		}
	case CircuitStateHalfOpen:
		cb.TripCircuit()
		return nil
	case CircuitStateOpen:
		// Circuit already open; failure is recorded but no additional state change needed.
	}
	cb.lastUpdated = time.Now().UTC()
	return nil
}

// TripCircuit transitions the circuit breaker to the open state, blocking further requests.
// If the circuit is already open, OpenedAt is preserved so the open duration is measured
// from the original trip time.
func (cb *CircuitBreaker) TripCircuit() {
	now := time.Now().UTC()
	cb.state = CircuitStateOpen
	if cb.openedAt == nil {
		cb.openedAt = &now
	}
	cb.lastUpdated = now
}

// AttemptReset transitions the circuit breaker from open to half-open, allowing a probe
// request to test recovery. Calling AttemptReset when closed or already half-open is a no-op.
func (cb *CircuitBreaker) AttemptReset() {
	if cb.state == CircuitStateOpen {
		cb.state = CircuitStateHalfOpen
		cb.lastUpdated = time.Now().UTC()
	}
}

// IsAvailable returns true when the circuit breaker permits sending requests
// (closed or half-open for a probe attempt).
func (cb *CircuitBreaker) IsAvailable() bool {
	return cb.state == CircuitStateClosed || cb.state == CircuitStateHalfOpen
}
