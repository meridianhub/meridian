package adapters_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/gateway/eventstream"
	"github.com/meridianhub/meridian/services/gateway/eventstream/adapters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEvent(tenantID string) eventstream.DomainEvent {
	return eventstream.DomainEvent{
		EventID:   "evt-001",
		EventType: "payment.created.v1",
		Topic:     "payment.events.v1",
		Channel:   "payment.events",
		TenantID:  tenantID,
		Timestamp: time.Now().UTC(),
	}
}

// TestLocalFanOut_Publish_EmptyTenantID verifies that publishing an event with
// an empty TenantID returns ErrEmptyTenantID.
func TestLocalFanOut_Publish_EmptyTenantID(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)
	event := makeEvent("")

	err := fo.Publish(context.Background(), event)
	require.Error(t, err)
	assert.True(t, errors.Is(err, eventstream.ErrEmptyTenantID))
}

// TestLocalFanOut_Subscribe_EmptyTenantID verifies that subscribing with an
// empty tenantID returns ErrEmptyTenantID.
func TestLocalFanOut_Subscribe_EmptyTenantID(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := fo.Subscribe(ctx, "", func(_ context.Context, _ eventstream.DomainEvent) error { return nil })
	require.Error(t, err)
	assert.True(t, errors.Is(err, eventstream.ErrEmptyTenantID))
}

// TestLocalFanOut_Unsubscribe_EmptyTenantID verifies that unsubscribing with an
// empty tenantID returns ErrEmptyTenantID.
func TestLocalFanOut_Unsubscribe_EmptyTenantID(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)

	err := fo.Unsubscribe(context.Background(), "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, eventstream.ErrEmptyTenantID))
}

// TestLocalFanOut_Unsubscribe_NoopWhenNotSubscribed verifies that unsubscribing
// a tenantID that has no registered handler is not an error.
func TestLocalFanOut_Unsubscribe_NoopWhenNotSubscribed(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)

	err := fo.Unsubscribe(context.Background(), "tenant-x")
	require.NoError(t, err)
}

// TestLocalFanOut_Publish_DeliveredToSubscriber verifies that a published event
// is forwarded to the subscribed handler.
func TestLocalFanOut_Publish_DeliveredToSubscriber(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan eventstream.DomainEvent, 1)
	subscribeErr := make(chan error, 1)

	go func() {
		err := fo.Subscribe(ctx, "tenant-a", func(_ context.Context, ev eventstream.DomainEvent) error {
			received <- ev
			return nil
		})
		subscribeErr <- err
	}()

	// Give Subscribe goroutine time to register
	time.Sleep(10 * time.Millisecond)

	event := makeEvent("tenant-a")
	require.NoError(t, fo.Publish(context.Background(), event))

	select {
	case got := <-received:
		assert.Equal(t, event.EventID, got.EventID)
		assert.Equal(t, event.TenantID, got.TenantID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event delivery")
	}

	cancel()
	select {
	case err := <-subscribeErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Subscribe did not return after context cancellation")
	}
}

// TestLocalFanOut_Publish_NoSubscriber verifies that publishing to a tenantID
// with no subscriber succeeds silently (drop).
func TestLocalFanOut_Publish_NoSubscriber(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)

	event := makeEvent("tenant-nobody")
	err := fo.Publish(context.Background(), event)
	require.NoError(t, err)
}

// TestLocalFanOut_Publish_TenantIsolation verifies that events for tenant-a
// are not delivered to tenant-b's subscriber.
func TestLocalFanOut_Publish_TenantIsolation(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receivedB := make(chan eventstream.DomainEvent, 5)

	go func() {
		_ = fo.Subscribe(ctx, "tenant-b", func(_ context.Context, ev eventstream.DomainEvent) error {
			receivedB <- ev
			return nil
		})
	}()

	time.Sleep(10 * time.Millisecond)

	// Publish to tenant-a only
	require.NoError(t, fo.Publish(context.Background(), makeEvent("tenant-a")))

	// tenant-b should receive nothing
	select {
	case got := <-receivedB:
		t.Fatalf("tenant-b unexpectedly received event: %+v", got)
	case <-time.After(50 * time.Millisecond):
		// expected: no delivery
	}
}

// TestLocalFanOut_Publish_BufferFull_DropWithoutBlocking verifies that when the
// subscriber channel is full, Publish drops the event and returns without blocking.
func TestLocalFanOut_Publish_BufferFull_DropWithoutBlocking(t *testing.T) {
	bufferSize := 2
	fo := adapters.NewLocalFanOut(bufferSize)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Slow handler that blocks after being called the first time, simulating a
	// slow consumer that causes the channel to fill up.
	started := make(chan struct{})
	var startOnce sync.Once
	go func() {
		_ = fo.Subscribe(ctx, "tenant-slow", func(_ context.Context, _ eventstream.DomainEvent) error {
			startOnce.Do(func() { close(started) })
			// block until context cancelled to stop the channel from draining
			<-ctx.Done()
			return nil
		})
	}()

	time.Sleep(10 * time.Millisecond)

	event := makeEvent("tenant-slow")

	// Fill the buffer (bufferSize events), then one more to trigger the slow handler
	for i := 0; i < bufferSize+1; i++ {
		require.NoError(t, fo.Publish(context.Background(), event))
	}

	// Wait for handler to start blocking
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler never started")
	}

	// These publishes should return immediately (buffer full, events dropped)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			_ = fo.Publish(context.Background(), event)
		}
		close(done)
	}()

	select {
	case <-done:
		// good - did not block
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on full channel")
	}
}

// TestLocalFanOut_Subscribe_ContextCancellation verifies that Subscribe returns
// nil when its context is cancelled.
func TestLocalFanOut_Subscribe_ContextCancellation(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- fo.Subscribe(ctx, "tenant-cancel", func(_ context.Context, _ eventstream.DomainEvent) error {
			return nil
		})
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Subscribe did not return after context cancellation")
	}
}

// TestLocalFanOut_Unsubscribe_StopsDelivery verifies that after Unsubscribe is
// called, events for that tenantID are no longer delivered.
func TestLocalFanOut_Unsubscribe_StopsDelivery(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	count := 0
	var mu sync.Mutex

	subscribeErr := make(chan error, 1)
	go func() {
		subscribeErr <- fo.Subscribe(ctx, "tenant-unsub", func(_ context.Context, _ eventstream.DomainEvent) error {
			mu.Lock()
			count++
			mu.Unlock()
			return nil
		})
	}()

	time.Sleep(10 * time.Millisecond)

	// Deliver one event before unsubscribe
	require.NoError(t, fo.Publish(context.Background(), makeEvent("tenant-unsub")))
	time.Sleep(20 * time.Millisecond)

	// Unsubscribe (this should cause Subscribe to return)
	require.NoError(t, fo.Unsubscribe(context.Background(), "tenant-unsub"))

	select {
	case err := <-subscribeErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Subscribe did not return after Unsubscribe")
	}

	mu.Lock()
	countBefore := count
	mu.Unlock()

	// Any further publishes should be no-ops (no subscriber)
	require.NoError(t, fo.Publish(context.Background(), makeEvent("tenant-unsub")))
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	countAfter := count
	mu.Unlock()

	assert.Equal(t, countBefore, countAfter, "events should not be delivered after unsubscribe")
}

// TestLocalFanOut_Subscribe_ReplacesExisting verifies that subscribing twice
// for the same tenantID replaces the previous subscription.
func TestLocalFanOut_Subscribe_ReplacesExisting(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstReceived := make(chan struct{}, 5)
	secondReceived := make(chan struct{}, 5)

	// First subscription (runs until replaced)
	go func() {
		_ = fo.Subscribe(ctx, "tenant-dup", func(_ context.Context, _ eventstream.DomainEvent) error {
			firstReceived <- struct{}{}
			return nil
		})
	}()

	time.Sleep(10 * time.Millisecond)

	// Second subscription replaces the first
	go func() {
		_ = fo.Subscribe(ctx, "tenant-dup", func(_ context.Context, _ eventstream.DomainEvent) error {
			secondReceived <- struct{}{}
			return nil
		})
	}()

	time.Sleep(10 * time.Millisecond)

	require.NoError(t, fo.Publish(context.Background(), makeEvent("tenant-dup")))

	select {
	case <-secondReceived:
		// second handler received the event - correct
	case <-time.After(time.Second):
		t.Fatal("second subscriber did not receive event")
	}
}

// TestLocalFanOut_ConcurrentSubscribeUnsubscribePublish verifies that the
// implementation is safe for concurrent use.
func TestLocalFanOut_ConcurrentSubscribeUnsubscribePublish(t *testing.T) {
	t.Parallel()

	fo := adapters.NewLocalFanOut(50)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	tenants := []string{"t1", "t2", "t3", "t4", "t5"}

	// Concurrent subscribers
	for _, tenant := range tenants {
		tenant := tenant
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = fo.Subscribe(ctx, tenant, func(_ context.Context, _ eventstream.DomainEvent) error {
				return nil
			})
		}()
	}

	// Concurrent publishers
	for _, tenant := range tenants {
		tenant := tenant
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_ = fo.Publish(context.Background(), makeEvent(tenant))
			}
		}()
	}

	// Concurrent unsubscribers
	for _, tenant := range tenants {
		tenant := tenant
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(20 * time.Millisecond)
			_ = fo.Unsubscribe(context.Background(), tenant)
		}()
	}

	// Let everything run, then cancel to unblock remaining subscribers
	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()
}
