// Package worker implements background workers for tenant provisioning.
package worker

import (
	"sync"
	"time"
)

// FailedAlert represents an alert that failed after max retries.
// Stored in the DLQ for manual review and reprocessing.
type FailedAlert struct {
	// AlertType identifies the type of alert (e.g., "pagerduty", "slack").
	AlertType string

	// TenantID is the tenant this alert relates to.
	TenantID string

	// Payload contains the alert data that was to be sent.
	Payload AlertPayload

	// ErrorMessage is the last error encountered when sending.
	ErrorMessage string

	// FirstAttemptAt is when the first attempt was made.
	FirstAttemptAt time.Time

	// LastAttemptAt is when the final (failed) attempt was made.
	LastAttemptAt time.Time

	// AttemptCount is the total number of attempts made.
	AttemptCount int
}

// AlertPayload contains the data for an alert to be sent.
type AlertPayload struct {
	// Summary is the alert summary/title.
	Summary string

	// DedupKey is used for deduplication.
	DedupKey string

	// Severity is the alert severity level.
	Severity string

	// CustomDetails contains additional context.
	CustomDetails map[string]any
}

// AlertDeadLetterQueue stores alerts that failed after max retries.
// Thread-safe for concurrent access from multiple workers.
type AlertDeadLetterQueue struct {
	mu     sync.RWMutex
	alerts []FailedAlert

	// maxSize limits the DLQ size to prevent unbounded growth.
	// When exceeded, oldest entries are removed.
	maxSize int

	// onEnqueue is called when an alert is added to the DLQ.
	// Useful for metrics and alerting.
	onEnqueue func(alert FailedAlert)
}

// DLQOption configures the AlertDeadLetterQueue.
type DLQOption func(*AlertDeadLetterQueue)

// WithDLQMaxSize sets the maximum number of entries in the DLQ.
func WithDLQMaxSize(size int) DLQOption {
	return func(q *AlertDeadLetterQueue) {
		if size > 0 {
			q.maxSize = size
		}
	}
}

// WithDLQEnqueueCallback sets a callback for when alerts are added to the DLQ.
func WithDLQEnqueueCallback(fn func(alert FailedAlert)) DLQOption {
	return func(q *AlertDeadLetterQueue) {
		q.onEnqueue = fn
	}
}

// NewAlertDeadLetterQueue creates a new dead-letter queue for failed alerts.
func NewAlertDeadLetterQueue(opts ...DLQOption) *AlertDeadLetterQueue {
	q := &AlertDeadLetterQueue{
		alerts:  make([]FailedAlert, 0),
		maxSize: 1000, // Default max size
	}

	for _, opt := range opts {
		opt(q)
	}

	return q
}

// Enqueue adds a failed alert to the dead-letter queue.
func (q *AlertDeadLetterQueue) Enqueue(alert FailedAlert) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// If at max capacity, remove oldest entry
	if len(q.alerts) >= q.maxSize {
		q.alerts = q.alerts[1:]
	}

	q.alerts = append(q.alerts, alert)

	// Invoke callback outside of lock to prevent deadlocks.
	// Pass a copy of the alert to avoid race conditions with slice operations.
	if q.onEnqueue != nil {
		alertCopy := alert
		go q.onEnqueue(alertCopy)
	}
}

// List returns all alerts currently in the DLQ.
// Returns a copy to prevent external modification.
func (q *AlertDeadLetterQueue) List() []FailedAlert {
	q.mu.RLock()
	defer q.mu.RUnlock()

	result := make([]FailedAlert, len(q.alerts))
	copy(result, q.alerts)
	return result
}

// Len returns the number of alerts in the DLQ.
func (q *AlertDeadLetterQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.alerts)
}

// Pop removes and returns the oldest alert from the DLQ.
// Returns nil if the queue is empty.
func (q *AlertDeadLetterQueue) Pop() *FailedAlert {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.alerts) == 0 {
		return nil
	}

	alert := q.alerts[0]
	q.alerts = q.alerts[1:]
	return &alert
}

// Clear removes all alerts from the DLQ.
func (q *AlertDeadLetterQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.alerts = make([]FailedAlert, 0)
}

// Remove removes an alert at the specified index.
// Returns true if removed, false if index out of bounds.
func (q *AlertDeadLetterQueue) Remove(index int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if index < 0 || index >= len(q.alerts) {
		return false
	}

	q.alerts = append(q.alerts[:index], q.alerts[index+1:]...)
	return true
}
