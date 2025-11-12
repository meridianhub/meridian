package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/position-keeping/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoOpEventPublisher_Publish(t *testing.T) {
	publisher := domain.NewNoOpEventPublisher()
	ctx := context.Background()

	event := &domain.TransactionCaptured{
		LogID:         uuid.New(),
		AccountID:     "ACC-123",
		TransactionID: uuid.New(),
		CorrelationID: "CORR-123",
		Timestamp:     time.Now().UTC(),
		Version:       1,
	}

	err := publisher.Publish(ctx, event)
	assert.NoError(t, err)
}

func TestNoOpEventPublisher_PublishBatch(t *testing.T) {
	publisher := domain.NewNoOpEventPublisher()
	ctx := context.Background()

	events := []domain.DomainEvent{
		&domain.TransactionCaptured{
			LogID:     uuid.New(),
			Timestamp: time.Now().UTC(),
		},
		&domain.TransactionAmended{
			LogID:     uuid.New(),
			Timestamp: time.Now().UTC(),
		},
	}

	err := publisher.PublishBatch(ctx, events)
	assert.NoError(t, err)
}

func TestInMemoryEventPublisher_Publish(t *testing.T) {
	publisher := domain.NewInMemoryEventPublisher()
	ctx := context.Background()

	event := &domain.TransactionCaptured{
		LogID:         uuid.New(),
		AccountID:     "ACC-123",
		TransactionID: uuid.New(),
		CorrelationID: "CORR-123",
		Timestamp:     time.Now().UTC(),
		Version:       1,
	}

	err := publisher.Publish(ctx, event)
	require.NoError(t, err)

	published := publisher.GetPublishedEvents()
	require.Len(t, published, 1)
	assert.Equal(t, event, published[0])
}

func TestInMemoryEventPublisher_PublishBatch(t *testing.T) {
	publisher := domain.NewInMemoryEventPublisher()
	ctx := context.Background()

	events := []domain.DomainEvent{
		&domain.TransactionCaptured{
			LogID:     uuid.New(),
			Timestamp: time.Now().UTC(),
		},
		&domain.TransactionAmended{
			LogID:     uuid.New(),
			Timestamp: time.Now().UTC(),
		},
	}

	err := publisher.PublishBatch(ctx, events)
	require.NoError(t, err)

	published := publisher.GetPublishedEvents()
	require.Len(t, published, 2)
	assert.Equal(t, events[0], published[0])
	assert.Equal(t, events[1], published[1])
}

func TestInMemoryEventPublisher_Clear(t *testing.T) {
	publisher := domain.NewInMemoryEventPublisher()
	ctx := context.Background()

	event := &domain.TransactionCaptured{
		LogID:     uuid.New(),
		Timestamp: time.Now().UTC(),
	}

	err := publisher.Publish(ctx, event)
	require.NoError(t, err)
	require.Len(t, publisher.GetPublishedEvents(), 1)

	publisher.Clear()
	assert.Empty(t, publisher.GetPublishedEvents())
}

func TestInMemoryEventPublisher_MultipleEvents(t *testing.T) {
	publisher := domain.NewInMemoryEventPublisher()
	ctx := context.Background()

	logID1 := uuid.New()
	logID2 := uuid.New()

	event1 := &domain.TransactionCaptured{
		LogID:     logID1,
		Timestamp: time.Now().UTC(),
	}

	event2 := &domain.TransactionReconciled{
		LogID:     logID2,
		Timestamp: time.Now().UTC(),
	}

	err := publisher.Publish(ctx, event1)
	require.NoError(t, err)

	err = publisher.Publish(ctx, event2)
	require.NoError(t, err)

	published := publisher.GetPublishedEvents()
	require.Len(t, published, 2)

	// Verify events maintain order
	captured, ok := published[0].(*domain.TransactionCaptured)
	require.True(t, ok)
	assert.Equal(t, logID1, captured.LogID)

	reconciled, ok := published[1].(*domain.TransactionReconciled)
	require.True(t, ok)
	assert.Equal(t, logID2, reconciled.LogID)
}

func TestInMemoryEventPublisher_ConcurrentPublish(t *testing.T) {
	publisher := domain.NewInMemoryEventPublisher()
	ctx := context.Background()

	const numGoroutines = 100
	const eventsPerGoroutine = 10

	// Launch multiple goroutines publishing events concurrently
	done := make(chan bool)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < eventsPerGoroutine; j++ {
				event := &domain.TransactionCaptured{
					LogID:     uuid.New(),
					AccountID: "ACC-WORKER",
					Timestamp: time.Now().UTC(),
				}
				err := publisher.Publish(ctx, event)
				require.NoError(t, err)
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify all events were published (no race condition data loss)
	published := publisher.GetPublishedEvents()
	assert.Equal(t, numGoroutines*eventsPerGoroutine, len(published),
		"all events should be published without race condition data loss")
}

func TestInMemoryEventPublisher_ConcurrentReadWrite(t *testing.T) {
	publisher := domain.NewInMemoryEventPublisher()
	ctx := context.Background()

	const numWriters = 50
	const numReaders = 50
	const eventsPerWriter = 10

	done := make(chan bool)

	// Launch writer goroutines
	for i := 0; i < numWriters; i++ {
		go func() {
			for j := 0; j < eventsPerWriter; j++ {
				event := &domain.TransactionPosted{
					LogID:     uuid.New(),
					Timestamp: time.Now().UTC(),
				}
				_ = publisher.Publish(ctx, event)
			}
			done <- true
		}()
	}

	// Launch reader goroutines (concurrent with writers)
	for i := 0; i < numReaders; i++ {
		go func() {
			_ = publisher.GetPublishedEvents()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < (numWriters + numReaders); i++ {
		<-done
	}

	// Verify final event count
	published := publisher.GetPublishedEvents()
	assert.Equal(t, numWriters*eventsPerWriter, len(published),
		"concurrent reads should not interfere with writes")
}
