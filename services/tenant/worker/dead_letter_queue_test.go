package worker

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAlertDeadLetterQueue(t *testing.T) {
	t.Run("creates with defaults", func(t *testing.T) {
		dlq := NewAlertDeadLetterQueue()
		require.NotNil(t, dlq)
		assert.Equal(t, 1000, dlq.maxSize)
		assert.Equal(t, 0, dlq.Len())
	})

	t.Run("creates with custom max size", func(t *testing.T) {
		dlq := NewAlertDeadLetterQueue(WithDLQMaxSize(50))
		assert.Equal(t, 50, dlq.maxSize)
	})
}

func TestAlertDeadLetterQueue_Enqueue(t *testing.T) {
	dlq := NewAlertDeadLetterQueue()

	alert := FailedAlert{
		AlertType:      AlertTypePagerDuty,
		TenantID:       "tenant-1",
		Payload:        AlertPayload{Summary: "test alert"},
		ErrorMessage:   "connection timeout",
		FirstAttemptAt: time.Now().Add(-5 * time.Minute),
		LastAttemptAt:  time.Now(),
		AttemptCount:   4,
	}

	dlq.Enqueue(alert)

	assert.Equal(t, 1, dlq.Len())

	alerts := dlq.List()
	require.Len(t, alerts, 1)
	assert.Equal(t, "tenant-1", alerts[0].TenantID)
	assert.Equal(t, "connection timeout", alerts[0].ErrorMessage)
	assert.Equal(t, 4, alerts[0].AttemptCount)
}

func TestAlertDeadLetterQueue_EnqueueMaxSize(t *testing.T) {
	dlq := NewAlertDeadLetterQueue(WithDLQMaxSize(3))

	// Add 5 alerts
	for i := 1; i <= 5; i++ {
		dlq.Enqueue(FailedAlert{
			TenantID: string(rune('a' - 1 + i)),
		})
	}

	// Should only have last 3
	assert.Equal(t, 3, dlq.Len())

	alerts := dlq.List()
	// Oldest (a, b) should have been removed, leaving c, d, e
	assert.Equal(t, "c", alerts[0].TenantID)
	assert.Equal(t, "d", alerts[1].TenantID)
	assert.Equal(t, "e", alerts[2].TenantID)
}

func TestAlertDeadLetterQueue_EnqueueCallback(t *testing.T) {
	var receivedAlert FailedAlert
	callbackCalled := make(chan struct{})

	dlq := NewAlertDeadLetterQueue(WithDLQEnqueueCallback(func(alert FailedAlert) {
		receivedAlert = alert
		close(callbackCalled)
	}))

	alert := FailedAlert{
		TenantID:     "callback-tenant",
		ErrorMessage: "test error",
	}

	dlq.Enqueue(alert)

	// Wait for callback (it's called in a goroutine)
	select {
	case <-callbackCalled:
		assert.Equal(t, "callback-tenant", receivedAlert.TenantID)
	case <-time.After(1 * time.Second):
		t.Fatal("callback was not called within timeout")
	}
}

func TestAlertDeadLetterQueue_List(t *testing.T) {
	dlq := NewAlertDeadLetterQueue()

	// Add multiple alerts
	dlq.Enqueue(FailedAlert{TenantID: "a"})
	dlq.Enqueue(FailedAlert{TenantID: "b"})
	dlq.Enqueue(FailedAlert{TenantID: "c"})

	alerts := dlq.List()
	require.Len(t, alerts, 3)

	// Verify order (FIFO)
	assert.Equal(t, "a", alerts[0].TenantID)
	assert.Equal(t, "b", alerts[1].TenantID)
	assert.Equal(t, "c", alerts[2].TenantID)

	// Verify List returns a copy (modifying doesn't affect queue)
	alerts[0].TenantID = "modified"
	freshAlerts := dlq.List()
	assert.Equal(t, "a", freshAlerts[0].TenantID)
}

func TestAlertDeadLetterQueue_Pop(t *testing.T) {
	dlq := NewAlertDeadLetterQueue()

	// Pop from empty queue
	alert := dlq.Pop()
	assert.Nil(t, alert)

	// Add alerts
	dlq.Enqueue(FailedAlert{TenantID: "first"})
	dlq.Enqueue(FailedAlert{TenantID: "second"})

	// Pop returns oldest first
	alert = dlq.Pop()
	require.NotNil(t, alert)
	assert.Equal(t, "first", alert.TenantID)
	assert.Equal(t, 1, dlq.Len())

	alert = dlq.Pop()
	require.NotNil(t, alert)
	assert.Equal(t, "second", alert.TenantID)
	assert.Equal(t, 0, dlq.Len())
}

func TestAlertDeadLetterQueue_Clear(t *testing.T) {
	dlq := NewAlertDeadLetterQueue()

	dlq.Enqueue(FailedAlert{TenantID: "a"})
	dlq.Enqueue(FailedAlert{TenantID: "b"})
	assert.Equal(t, 2, dlq.Len())

	dlq.Clear()
	assert.Equal(t, 0, dlq.Len())
}

func TestAlertDeadLetterQueue_Remove(t *testing.T) {
	dlq := NewAlertDeadLetterQueue()

	dlq.Enqueue(FailedAlert{TenantID: "a"})
	dlq.Enqueue(FailedAlert{TenantID: "b"})
	dlq.Enqueue(FailedAlert{TenantID: "c"})

	// Remove middle element
	removed := dlq.Remove(1)
	assert.True(t, removed)
	assert.Equal(t, 2, dlq.Len())

	alerts := dlq.List()
	assert.Equal(t, "a", alerts[0].TenantID)
	assert.Equal(t, "c", alerts[1].TenantID)

	// Remove out of bounds
	removed = dlq.Remove(5)
	assert.False(t, removed)

	removed = dlq.Remove(-1)
	assert.False(t, removed)
}

func TestAlertDeadLetterQueue_ConcurrentAccess(t *testing.T) {
	dlq := NewAlertDeadLetterQueue(WithDLQMaxSize(1000))

	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			dlq.Enqueue(FailedAlert{TenantID: string(rune('a' + id%26))})
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = dlq.List()
			_ = dlq.Len()
		}()
	}

	wg.Wait()

	// Queue should have received all writes (no panics, race conditions)
	assert.Equal(t, 100, dlq.Len())
}

func TestFailedAlert_Fields(t *testing.T) {
	// Test that all fields are properly stored and retrieved
	now := time.Now()
	firstAttempt := now.Add(-1 * time.Minute)

	payload := AlertPayload{
		Summary:  "Test summary",
		DedupKey: "dedup-123",
		Severity: "critical",
		CustomDetails: map[string]any{
			"key": "value",
		},
	}

	alert := FailedAlert{
		AlertType:      AlertTypePagerDuty,
		TenantID:       "tenant-abc",
		Payload:        payload,
		ErrorMessage:   "service unavailable",
		FirstAttemptAt: firstAttempt,
		LastAttemptAt:  now,
		AttemptCount:   5,
	}

	dlq := NewAlertDeadLetterQueue()
	dlq.Enqueue(alert)

	retrieved := dlq.List()[0]
	assert.Equal(t, AlertTypePagerDuty, retrieved.AlertType)
	assert.Equal(t, "tenant-abc", retrieved.TenantID)
	assert.Equal(t, "Test summary", retrieved.Payload.Summary)
	assert.Equal(t, "dedup-123", retrieved.Payload.DedupKey)
	assert.Equal(t, "critical", retrieved.Payload.Severity)
	assert.Equal(t, "value", retrieved.Payload.CustomDetails["key"])
	assert.Equal(t, "service unavailable", retrieved.ErrorMessage)
	assert.WithinDuration(t, firstAttempt, retrieved.FirstAttemptAt, time.Second)
	assert.WithinDuration(t, now, retrieved.LastAttemptAt, time.Second)
	assert.Equal(t, 5, retrieved.AttemptCount)
}
