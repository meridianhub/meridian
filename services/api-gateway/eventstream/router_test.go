package eventstream_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ConnectionRegistry tests ---

func TestConnectionRegistry_RegisterAndGet(t *testing.T) {
	reg := eventstream.NewConnectionRegistry()
	conn := newTestConnection(t, "conn-1", "tenant-A")

	reg.Register(conn)

	conns := reg.GetByTenant("tenant-A")
	require.Len(t, conns, 1)
	assert.Equal(t, "conn-1", conns[0].ID())
}

func TestConnectionRegistry_GetByTenant_IsolatesTenants(t *testing.T) {
	reg := eventstream.NewConnectionRegistry()
	connA := newTestConnection(t, "conn-a", "tenant-A")
	connB := newTestConnection(t, "conn-b", "tenant-B")

	reg.Register(connA)
	reg.Register(connB)

	connsA := reg.GetByTenant("tenant-A")
	require.Len(t, connsA, 1)
	assert.Equal(t, "conn-a", connsA[0].ID())

	connsB := reg.GetByTenant("tenant-B")
	require.Len(t, connsB, 1)
	assert.Equal(t, "conn-b", connsB[0].ID())
}

func TestConnectionRegistry_GetByTenant_UnknownTenant_ReturnsNil(t *testing.T) {
	reg := eventstream.NewConnectionRegistry()
	conns := reg.GetByTenant("nonexistent")
	assert.Empty(t, conns)
}

func TestConnectionRegistry_Unregister(t *testing.T) {
	reg := eventstream.NewConnectionRegistry()
	conn := newTestConnection(t, "conn-1", "tenant-A")

	reg.Register(conn)
	assert.Len(t, reg.GetByTenant("tenant-A"), 1)

	reg.Unregister("conn-1")
	assert.Empty(t, reg.GetByTenant("tenant-A"))
}

func TestConnectionRegistry_Unregister_NonExistent_NoPanic(t *testing.T) { //nolint:revive
	reg := eventstream.NewConnectionRegistry()
	// Should not panic
	reg.Unregister("nonexistent-conn-id")
}

func TestConnectionRegistry_MultipleSameTenant(t *testing.T) {
	reg := eventstream.NewConnectionRegistry()
	conn1 := newTestConnection(t, "conn-1", "tenant-A")
	conn2 := newTestConnection(t, "conn-2", "tenant-A")

	reg.Register(conn1)
	reg.Register(conn2)

	conns := reg.GetByTenant("tenant-A")
	assert.Len(t, conns, 2)
}

func TestConnectionRegistry_UnregisterOneOfMany(t *testing.T) {
	reg := eventstream.NewConnectionRegistry()
	conn1 := newTestConnection(t, "conn-1", "tenant-A")
	conn2 := newTestConnection(t, "conn-2", "tenant-A")

	reg.Register(conn1)
	reg.Register(conn2)

	reg.Unregister("conn-1")

	conns := reg.GetByTenant("tenant-A")
	require.Len(t, conns, 1)
	assert.Equal(t, "conn-2", conns[0].ID())
}

func TestConnectionRegistry_ConcurrentAccess(t *testing.T) { //nolint:revive
	reg := eventstream.NewConnectionRegistry()

	var wg sync.WaitGroup

	// Concurrent registers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			connID := "conn-" + string(rune('A'+idx%26))
			tenantID := "tenant-" + string(rune('A'+idx%5))
			conn := newTestConnection(t, connID, tenantID)
			reg.Register(conn)
		}(i)
	}

	// Concurrent unregisters
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			connID := "conn-" + string(rune('A'+idx%26))
			reg.Unregister(connID)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tenantID := "tenant-" + string(rune('A'+idx%5))
			reg.GetByTenant(tenantID)
		}(i)
	}

	wg.Wait()
}

// --- Router tests ---

func TestRouter_HandleEvent_DeliveredToMatchingTenant(t *testing.T) {
	fanOut, router := newTestRouter(t)
	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "payment-order.*")

	router.RegisterConnection(conn)

	event := eventstream.DomainEvent{
		EventID:   "evt-1",
		EventType: "payment_order.created.v1",
		Channel:   "payment-order.created",
		TenantID:  "tenant-A",
	}

	// Publish via the mock EventSource's handler
	err := fanOut.Publish(context.Background(), event)
	require.NoError(t, err)

	// The connection should receive a ServerMessage
	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return conn.ReceivedCount() > 0
	})
	require.NoError(t, err, "connection should have received a message")
}

func TestRouter_HandleEvent_TenantIsolation(t *testing.T) {
	fanOut, router := newTestRouter(t)

	connA := newTestConnectionWithSub(t, "conn-a", "tenant-A", "sub-1", "*")
	connB := newTestConnectionWithSub(t, "conn-b", "tenant-B", "sub-1", "*")

	router.RegisterConnection(connA)
	router.RegisterConnection(connB)

	// Publish event for tenant-A only
	eventA := eventstream.DomainEvent{
		EventID:   "evt-a",
		EventType: "payment_order.created.v1",
		Channel:   "payment-order.created",
		TenantID:  "tenant-A",
	}

	err := fanOut.Publish(context.Background(), eventA)
	require.NoError(t, err)

	// Only tenant-A connection should receive the event
	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return connA.ReceivedCount() > 0
	})
	require.NoError(t, err, "tenant-A connection should have received event")

	// Give some time to ensure tenant-B doesn't receive anything
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, connB.ReceivedCount(), "tenant-B connection must not receive tenant-A's event")
}

func TestRouter_HandleEvent_NoConnectionsForTenant(t *testing.T) {
	fanOut, _ := newTestRouter(t)

	event := eventstream.DomainEvent{
		EventID:   "evt-1",
		EventType: "payment_order.created.v1",
		Channel:   "payment-order.created",
		TenantID:  "tenant-nobody",
	}

	// Should not error even when no connections exist for the tenant
	err := fanOut.Publish(context.Background(), event)
	require.NoError(t, err)
}

func TestRouter_HandleEvent_SubscriptionFilterMatch(t *testing.T) {
	fanOut, router := newTestRouter(t)

	// Connection subscribed to payment-order.* but event is for position-keeping
	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "payment-order.*")
	router.RegisterConnection(conn)

	nonMatchingEvent := eventstream.DomainEvent{
		EventID:   "evt-1",
		EventType: "position_keeping.updated.v1",
		Channel:   "position-keeping.updated",
		TenantID:  "tenant-A",
	}

	err := fanOut.Publish(context.Background(), nonMatchingEvent)
	require.NoError(t, err)

	// Give time to confirm no delivery
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, conn.ReceivedCount(), "connection should not receive non-matching channel event")
}

func TestRouter_HandleEvent_MultipleConnectionsSameTenant(t *testing.T) {
	fanOut, router := newTestRouter(t)

	conn1 := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "*")
	conn2 := newTestConnectionWithSub(t, "conn-2", "tenant-A", "sub-2", "*")

	router.RegisterConnection(conn1)
	router.RegisterConnection(conn2)

	event := eventstream.DomainEvent{
		EventID:   "evt-1",
		EventType: "payment_order.created.v1",
		Channel:   "payment-order.created",
		TenantID:  "tenant-A",
	}

	err := fanOut.Publish(context.Background(), event)
	require.NoError(t, err)

	// Both connections should receive the event
	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return conn1.ReceivedCount() > 0 && conn2.ReceivedCount() > 0
	})
	require.NoError(t, err, "both connections should receive the event")
}

func TestRouter_UnregisterConnection_StopsDelivery(t *testing.T) {
	fanOut, router := newTestRouter(t)

	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "*")
	router.RegisterConnection(conn)

	// Unregister before publishing
	router.UnregisterConnection("conn-1")

	event := eventstream.DomainEvent{
		EventID:   "evt-1",
		EventType: "payment_order.created.v1",
		Channel:   "payment-order.created",
		TenantID:  "tenant-A",
	}

	err := fanOut.Publish(context.Background(), event)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, conn.ReceivedCount(), "unregistered connection should not receive events")
}

func TestRouter_GetConnectionsByTenant(t *testing.T) {
	_, router := newTestRouter(t)

	conn1 := newTestConnection(t, "conn-1", "tenant-A")
	conn2 := newTestConnection(t, "conn-2", "tenant-A")
	conn3 := newTestConnection(t, "conn-3", "tenant-B")

	router.RegisterConnection(conn1)
	router.RegisterConnection(conn2)
	router.RegisterConnection(conn3)

	connsA := router.GetConnectionsByTenant("tenant-A")
	assert.Len(t, connsA, 2)

	connsB := router.GetConnectionsByTenant("tenant-B")
	assert.Len(t, connsB, 1)

	connsC := router.GetConnectionsByTenant("tenant-C")
	assert.Empty(t, connsC)
}

func TestRouter_Start_ConsumesFromEventSource(t *testing.T) {
	src := &mockEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut)

	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "*")
	router.RegisterConnection(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		_ = router.Start(ctx)
	}()

	// Wait for Start to call EventSource.Start
	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return src.IsStarted()
	})
	require.NoError(t, err, "EventSource.Start should be called")

	// Deliver an event via the mock source handler
	event := eventstream.DomainEvent{
		EventID:   "evt-1",
		EventType: "payment_order.created.v1",
		Channel:   "payment-order.created",
		TenantID:  "tenant-A",
	}
	src.EmitEvent(context.Background(), event)

	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return conn.ReceivedCount() > 0
	})
	require.NoError(t, err, "connection should receive event from EventSource")

	cancel()
	<-startDone
}

func TestRouter_Shutdown_GracefulStop(t *testing.T) {
	src := &mockEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		_ = router.Start(ctx)
	}()

	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return src.IsStarted()
	})
	require.NoError(t, err)

	// Shutdown should stop the router
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	err = router.Shutdown(shutdownCtx)
	require.NoError(t, err)

	select {
	case <-startDone:
		// expected: Start returned after shutdown
	case <-time.After(2 * time.Second):
		t.Fatal("router.Start did not return after Shutdown")
	}
}

// --- helpers ---

// testConn is a lightweight test double for Connection that satisfies the
// ConnectionSender interface used by the router's fan-out handler.
type testConn struct {
	id       string
	tenantID string
	subs     []eventstream.Subscription
	received atomic.Int64
}

func (c *testConn) ID() string       { return c.id }
func (c *testConn) TenantID() string { return c.tenantID }
func (c *testConn) Send(_ eventstream.ServerMessage) bool {
	c.received.Add(1)
	return true
}

func (c *testConn) MatchesEvent(event eventstream.DomainEvent) []string {
	var matched []string
	for _, sub := range c.subs {
		if sub.Matches(event) {
			matched = append(matched, sub.ID)
		}
	}
	return matched
}
func (c *testConn) ReceivedCount() int { return int(c.received.Load()) }

func newTestConnection(t *testing.T, id, tenantID string) *testConn {
	t.Helper()
	return &testConn{id: id, tenantID: tenantID}
}

func newTestConnectionWithSub(t *testing.T, id, tenantID, subID string, pattern eventstream.ChannelPattern) *testConn {
	t.Helper()
	sub, err := eventstream.NewSubscription(subID, []eventstream.ChannelPattern{pattern}, eventstream.SubscriptionFilters{})
	require.NoError(t, err)
	return &testConn{
		id:       id,
		tenantID: tenantID,
		subs:     []eventstream.Subscription{sub},
	}
}

// newTestRouter creates an InProcessFanOut-backed router, starts its fanout
// subscription in the background, and returns both for direct publishing in tests.
func newTestRouter(t *testing.T) (*eventstream.InProcessFanOut, *eventstream.Router) {
	t.Helper()

	src := &mockEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		_ = router.Start(ctx)
	}()

	// Wait for the router to start
	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return src.IsStarted()
	})
	if err != nil {
		t.Fatalf("router did not start in time: %v", err)
	}

	return fanOut, router
}

// mockEventSource is a test EventSource that captures the handler and allows
// manual event emission.
type mockEventSource struct {
	mu      sync.Mutex
	handler eventstream.EventHandler
	started atomic.Bool
}

func (m *mockEventSource) Start(ctx context.Context, handler eventstream.EventHandler) error {
	m.mu.Lock()
	m.handler = handler
	m.mu.Unlock()
	m.started.Store(true)

	<-ctx.Done()
	return nil
}

func (m *mockEventSource) IsStarted() bool {
	return m.started.Load()
}

func (m *mockEventSource) EmitEvent(ctx context.Context, event eventstream.DomainEvent) {
	m.mu.Lock()
	h := m.handler
	m.mu.Unlock()
	if h != nil {
		_ = h(ctx, event)
	}
}
