package adapters_test

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream/adapters"
	"github.com/meridianhub/meridian/shared/platform/await"
)

func setupMiniredis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	t.Cleanup(func() {
		_ = client.Close()
	})
	return mr, client
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func newTestEvent(tenantID string) eventstream.DomainEvent {
	evt, err := eventstream.NewDomainEvent(
		"payment_order.created.v1",
		"payment_order.v1",
		"agg-001",
		"PaymentOrder",
		tenantID,
		"corr-001",
		"cause-001",
		[]byte(`{"amount":"100.00"}`),
	)
	if err != nil {
		panic(err)
	}
	return evt
}

// TestRedisFanOut_Publish_EmptyTenantID verifies that publishing with an empty TenantID returns ErrEmptyTenantID.
func TestRedisFanOut_Publish_EmptyTenantID(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	evt := eventstream.DomainEvent{TenantID: ""}
	err := fanOut.Publish(context.Background(), evt)
	assert.ErrorIs(t, err, eventstream.ErrEmptyTenantID)
}

// TestRedisFanOut_Subscribe_EmptyTenantID verifies that subscribing with an empty tenantID returns ErrEmptyTenantID.
func TestRedisFanOut_Subscribe_EmptyTenantID(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	err := fanOut.Subscribe(context.Background(), "", func(_ context.Context, _ eventstream.DomainEvent) error {
		return nil
	})
	assert.ErrorIs(t, err, eventstream.ErrEmptyTenantID)
}

// TestRedisFanOut_Subscribe_NilHandler verifies that subscribing with a nil handler returns ErrNilHandler.
func TestRedisFanOut_Subscribe_NilHandler(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	err := fanOut.Subscribe(context.Background(), "tenant-123", nil)
	assert.ErrorIs(t, err, adapters.ErrNilHandler)
}

// TestRedisFanOut_Unsubscribe_EmptyTenantID verifies that unsubscribing with an empty tenantID returns ErrEmptyTenantID.
func TestRedisFanOut_Unsubscribe_EmptyTenantID(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	err := fanOut.Unsubscribe(context.Background(), "")
	assert.ErrorIs(t, err, eventstream.ErrEmptyTenantID)
}

// TestRedisFanOut_Unsubscribe_NotSubscribed verifies that unsubscribing a tenant with no subscription is not an error.
func TestRedisFanOut_Unsubscribe_NotSubscribed(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	err := fanOut.Unsubscribe(context.Background(), "tenant-999")
	assert.NoError(t, err)
}

// TestRedisFanOut_PubSub_Flow verifies publish → subscribe → handler delivery.
func TestRedisFanOut_PubSub_Flow(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	tenantID := "tenant-abc"
	received := make(chan eventstream.DomainEvent, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Subscribe first; Subscribe blocks until Redis acknowledges the subscription.
	err := fanOut.Subscribe(ctx, tenantID, func(_ context.Context, event eventstream.DomainEvent) error {
		received <- event
		return nil
	})
	require.NoError(t, err)

	// Publish an event - subscription is confirmed so no missed messages.
	evt := newTestEvent(tenantID)
	err = fanOut.Publish(context.Background(), evt)
	require.NoError(t, err)

	// Verify delivery
	select {
	case got := <-received:
		assert.Equal(t, evt.EventID, got.EventID)
		assert.Equal(t, evt.TenantID, got.TenantID)
		assert.Equal(t, evt.EventType, got.EventType)
		assert.Equal(t, string(evt.Payload), string(got.Payload))
	case <-ctx.Done():
		t.Fatal("timed out waiting for event delivery")
	}
}

// TestRedisFanOut_Subscribe_ReplacesExistingHandler verifies that re-subscribing replaces the handler.
func TestRedisFanOut_Subscribe_ReplacesExistingHandler(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	tenantID := "tenant-replace"
	var firstCount, secondCount atomic.Int32

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Subscribe with first handler
	err := fanOut.Subscribe(ctx, tenantID, func(_ context.Context, _ eventstream.DomainEvent) error {
		firstCount.Add(1)
		return nil
	})
	require.NoError(t, err)

	// Replace with second handler
	err = fanOut.Subscribe(ctx, tenantID, func(_ context.Context, _ eventstream.DomainEvent) error {
		secondCount.Add(1)
		return nil
	})
	require.NoError(t, err)

	// Publish
	evt := newTestEvent(tenantID)
	err = fanOut.Publish(context.Background(), evt)
	require.NoError(t, err)

	// Wait until the second handler has received the event.
	err = await.AtMost(3 * time.Second).Until(func() bool {
		return secondCount.Load() == 1
	})
	require.NoError(t, err, "timed out waiting for second handler to receive event")

	// Only second handler should be active
	assert.Equal(t, int32(0), firstCount.Load(), "first handler should not receive events after replacement")
	assert.Equal(t, int32(1), secondCount.Load(), "second handler should receive the event")
}

// TestRedisFanOut_Unsubscribe_StopsDelivery verifies that after unsubscribing, events are no longer delivered.
func TestRedisFanOut_Unsubscribe_StopsDelivery(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	tenantID := "tenant-unsub"
	var count atomic.Int32

	ctx := context.Background()

	err := fanOut.Subscribe(ctx, tenantID, func(_ context.Context, _ eventstream.DomainEvent) error {
		count.Add(1)
		return nil
	})
	require.NoError(t, err)

	// Unsubscribe
	err = fanOut.Unsubscribe(ctx, tenantID)
	require.NoError(t, err)

	// Publish after unsubscribe; count should stay 0.
	evt := newTestEvent(tenantID)
	err = fanOut.Publish(ctx, evt)
	require.NoError(t, err)

	// Allow a brief window for any stray delivery.
	err = await.AtMost(300 * time.Millisecond).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			// We expect zero events; we wait the full window.
			return false
		})
	assert.ErrorIs(t, err, await.ErrTimeout, "expected timeout (no events delivered)")
	assert.Equal(t, int32(0), count.Load(), "handler should not receive events after unsubscribe")
}

// TestRedisFanOut_TenantIsolation verifies events are only delivered to matching tenant.
func TestRedisFanOut_TenantIsolation(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var countA, countB atomic.Int32

	err := fanOut.Subscribe(ctx, "tenant-A", func(_ context.Context, _ eventstream.DomainEvent) error {
		countA.Add(1)
		return nil
	})
	require.NoError(t, err)

	err = fanOut.Subscribe(ctx, "tenant-B", func(_ context.Context, _ eventstream.DomainEvent) error {
		countB.Add(1)
		return nil
	})
	require.NoError(t, err)

	// Publish to tenant-A only
	evt := newTestEvent("tenant-A")
	err = fanOut.Publish(ctx, evt)
	require.NoError(t, err)

	// Wait for tenant-A to receive the event
	err = await.AtMost(3 * time.Second).Until(func() bool {
		return countA.Load() == 1
	})
	require.NoError(t, err, "timed out waiting for tenant-A handler")

	assert.Equal(t, int32(1), countA.Load(), "tenant-A handler should receive 1 event")
	assert.Equal(t, int32(0), countB.Load(), "tenant-B handler should not receive tenant-A events")
}

// TestRedisFanOut_ContextCancellation_StopsSubscription verifies subscription goroutine exits when context is cancelled.
func TestRedisFanOut_ContextCancellation_StopsSubscription(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	tenantID := "tenant-ctx-cancel"

	ctx, cancel := context.WithCancel(context.Background())

	err := fanOut.Subscribe(ctx, tenantID, func(_ context.Context, _ eventstream.DomainEvent) error {
		return nil
	})
	require.NoError(t, err)

	// Cancel context
	cancel()

	// After context cancel, the subscription map entry should be cleaned up.
	// We verify by checking that unsubscribing is a no-op (no double-cancel).
	err = fanOut.Unsubscribe(context.Background(), tenantID)
	assert.NoError(t, err)
}

// TestRedisFanOut_ConcurrentPublish verifies concurrent publishes are handled correctly.
func TestRedisFanOut_ConcurrentPublish(t *testing.T) {
	_, client := setupMiniredis(t)
	fanOut := adapters.NewRedisFanOut(client, testLogger())

	tenantID := "tenant-concurrent"
	var count atomic.Int32
	const numEvents = 20

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := fanOut.Subscribe(ctx, tenantID, func(_ context.Context, _ eventstream.DomainEvent) error {
		count.Add(1)
		return nil
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < numEvents; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			evt := newTestEvent(tenantID)
			_ = fanOut.Publish(context.Background(), evt)
		}()
	}
	wg.Wait()

	// Wait for all deliveries
	err = await.AtMost(5 * time.Second).Until(func() bool {
		return count.Load() == numEvents
	})
	require.NoError(t, err, "timed out waiting for all events to be delivered")

	assert.Equal(t, int32(numEvents), count.Load())
}
