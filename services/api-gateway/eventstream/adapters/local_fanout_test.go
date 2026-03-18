package adapters_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream/adapters"
	"github.com/meridianhub/meridian/shared/platform/await"
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

// subscribeReady starts a Subscribe goroutine and waits until the subscriber is
// registered by publishing a probe event and waiting for the handler to be
// invoked at least once. The returned cancel stops the subscription and the
// errCh carries the Subscribe return value.
func subscribeReady(t *testing.T, fo *adapters.LocalFanOut, tenantID string, handler eventstream.EventHandler) (cancel context.CancelFunc, errCh <-chan error) {
	t.Helper()

	var called atomic.Bool
	wrappedHandler := func(ctx context.Context, ev eventstream.DomainEvent) error {
		called.Store(true)
		return handler(ctx, ev)
	}

	ctx, cancelFn := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() {
		ch <- fo.Subscribe(ctx, tenantID, wrappedHandler)
	}()

	// Probe: publish events until the handler is reached, confirming registration.
	// A distinct EventID prevents the probe from being confused with test events
	// that share the same EventID produced by makeEvent.
	probeEvent := makeEvent(tenantID)
	probeEvent.EventID = "probe-ready"
	probeErr := await.New().
		AtMost(time.Second).
		PollInterval(5 * time.Millisecond).
		Until(func() bool {
			_ = fo.Publish(context.Background(), probeEvent)
			return called.Load()
		})
	require.NoError(t, probeErr, "subscriber for %q did not become ready", tenantID)

	return cancelFn, ch
}

// TestLocalFanOut_NewLocalFanOut_NegativeBufferSize verifies that constructing
// a LocalFanOut with a negative buffer size returns an error.
func TestLocalFanOut_NewLocalFanOut_NegativeBufferSize(t *testing.T) {
	_, err := adapters.NewLocalFanOutE(-1)
	require.Error(t, err)
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

	received := make(chan eventstream.DomainEvent, 10)
	cancel, subscribeErr := subscribeReady(t, fo, "tenant-a", func(_ context.Context, ev eventstream.DomainEvent) error {
		received <- ev
		return nil
	})
	defer cancel()

	event := makeEvent("tenant-a")
	require.NoError(t, fo.Publish(context.Background(), event))

	// Drain probe events (EventID "probe-ready"); wait for the real event.
	err := await.New().
		AtMost(time.Second).
		Until(func() bool {
			select {
			case got := <-received:
				if got.EventID == event.EventID {
					return true
				}
			default:
			}
			return false
		})
	require.NoError(t, err, "event was not delivered to subscriber")

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

	// Use a counter so we can observe the count at a known point in time and
	// verify it does not increase after a tenant-a publish.
	var tenantBCount atomic.Int64
	cancel, _ := subscribeReady(t, fo, "tenant-b", func(_ context.Context, _ eventstream.DomainEvent) error {
		tenantBCount.Add(1)
		return nil
	})
	defer cancel()

	// Drain: wait until all probe events have been processed by waiting for the
	// count to stabilize (no new increments over a short window).
	var stable int64
	err := await.New().
		AtMost(time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			current := tenantBCount.Load()
			if current != stable {
				stable = current
				return false // still draining
			}
			return true // stable
		})
	require.NoError(t, err, "probe events did not drain in time")

	countAfterDrain := tenantBCount.Load()

	// Publish to tenant-a only.
	require.NoError(t, fo.Publish(context.Background(), makeEvent("tenant-a")))

	// tenant-b should receive nothing within a reasonable window.
	err = await.New().
		AtMost(100 * time.Millisecond).
		PollInterval(10 * time.Millisecond).
		Until(func() bool { return tenantBCount.Load() > countAfterDrain })
	assert.Error(t, err, "tenant-b should not have received an event for tenant-a")
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
			// Block until context cancelled to prevent channel from draining.
			<-ctx.Done()
			return nil
		})
	}()

	// Wait until the subscriber is registered. The subscriber map is private,
	// so we use a short sleep; 20ms is well within CI tolerances.
	time.Sleep(20 * time.Millisecond) //nolint:forbidigo // no observable state to poll before first event

	event := makeEvent("tenant-slow")

	// Fill buffer (bufferSize events), then one more to start the blocking handler.
	for i := 0; i < bufferSize+1; i++ {
		require.NoError(t, fo.Publish(context.Background(), event))
	}

	// Wait for handler to start blocking.
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler never started")
	}

	// These publishes should return immediately (buffer full, events dropped).
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

	time.Sleep(20 * time.Millisecond) //nolint:forbidigo // no observable state to poll before cancellation
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

	var count atomic.Int64
	cancel, subscribeErr := subscribeReady(t, fo, "tenant-unsub", func(_ context.Context, _ eventstream.DomainEvent) error {
		count.Add(1)
		return nil
	})
	defer cancel()

	countAfterProbe := count.Load()

	// Deliver one extra event and wait for it to land.
	require.NoError(t, fo.Publish(context.Background(), makeEvent("tenant-unsub")))
	err := await.New().
		AtMost(time.Second).
		Until(func() bool { return count.Load() > countAfterProbe })
	require.NoError(t, err, "event should have been delivered before unsubscribe")

	// Unsubscribe should cause Subscribe to return.
	require.NoError(t, fo.Unsubscribe(context.Background(), "tenant-unsub"))

	select {
	case err := <-subscribeErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Subscribe did not return after Unsubscribe")
	}

	countBeforeExtra := count.Load()

	// Further publishes should be no-ops.
	require.NoError(t, fo.Publish(context.Background(), makeEvent("tenant-unsub")))

	err = await.New().
		AtMost(50 * time.Millisecond).
		PollInterval(5 * time.Millisecond).
		Until(func() bool { return count.Load() > countBeforeExtra })
	assert.Error(t, err, "events should not be delivered after unsubscribe")
}

// TestLocalFanOut_Subscribe_ReplacesExisting verifies that subscribing twice
// for the same tenantID replaces the previous subscription.
func TestLocalFanOut_Subscribe_ReplacesExisting(t *testing.T) {
	fo := adapters.NewLocalFanOut(10)

	// First subscription — just a placeholder handler.
	cancel1, _ := subscribeReady(t, fo, "tenant-dup", func(_ context.Context, _ eventstream.DomainEvent) error {
		return nil
	})
	defer cancel1()

	var secondReceived atomic.Bool

	// Second subscription replaces the first via a new context.
	cancel2, _ := subscribeReady(t, fo, "tenant-dup", func(_ context.Context, _ eventstream.DomainEvent) error {
		secondReceived.Store(true)
		return nil
	})
	defer cancel2()

	require.NoError(t, fo.Publish(context.Background(), makeEvent("tenant-dup")))

	err := await.New().
		AtMost(time.Second).
		Until(func() bool { return secondReceived.Load() })
	require.NoError(t, err, "second subscriber should have received the event")
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
			time.Sleep(20 * time.Millisecond) //nolint:forbidigo // staggers unsubscribe relative to concurrent publish goroutines
			_ = fo.Unsubscribe(context.Background(), tenant)
		}()
	}

	// Let everything run, then cancel to unblock remaining subscribers.
	time.Sleep(100 * time.Millisecond) //nolint:forbidigo // allows concurrent goroutines to run before cancellation
	cancel()
	wg.Wait()
}
