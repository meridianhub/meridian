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
