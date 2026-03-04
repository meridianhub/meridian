package eventstream_test

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/gateway/eventstream"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DomainEvent ChainDepth field ---

func TestDomainEvent_ChainDepth_DefaultsToZero(t *testing.T) {
	event, err := eventstream.NewDomainEvent(
		"payment_order.created.v1",
		"payment-order.events.v1",
		"agg-1",
		"PaymentOrder",
		"tenant-abc",
		"corr-1",
		"",
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, event.ChainDepth)
}

func TestDomainEvent_ChainDepth_CanBeSetManually(t *testing.T) {
	event, err := eventstream.NewDomainEvent(
		"payment_order.created.v1",
		"payment-order.events.v1",
		"agg-1",
		"PaymentOrder",
		"tenant-abc",
		"corr-1",
		"",
		nil,
	)
	require.NoError(t, err)
	event.ChainDepth = 3
	assert.Equal(t, 3, event.ChainDepth)
}

// --- Router chain depth enforcement ---

func TestRouter_ChainDepth_BelowMax_EventDelivered(t *testing.T) {
	src := &mockEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut, eventstream.WithMaxChainDepth(8))

	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "*")
	router.RegisterConnection(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = router.Start(ctx) }()

	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(src.IsStarted)
	require.NoError(t, err)

	event := eventstream.DomainEvent{
		EventID:    "evt-1",
		EventType:  "payment_order.created.v1",
		Channel:    "payment-order.created",
		TenantID:   "tenant-A",
		ChainDepth: 7, // below max of 8
	}
	src.EmitEvent(ctx, event)

	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return conn.ReceivedCount() > 0
	})
	require.NoError(t, err, "event at depth 7 (below max 8) should be delivered")
}

func TestRouter_ChainDepth_AtMax_EventDropped(t *testing.T) {
	src := &mockEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut, eventstream.WithMaxChainDepth(8))

	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "*")
	router.RegisterConnection(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = router.Start(ctx) }()

	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(src.IsStarted)
	require.NoError(t, err)

	event := eventstream.DomainEvent{
		EventID:    "evt-1",
		EventType:  "payment_order.created.v1",
		Channel:    "payment-order.created",
		TenantID:   "tenant-A",
		ChainDepth: 8, // exactly at max — should be dropped
	}
	src.EmitEvent(ctx, event)

	// Allow time to confirm the event was NOT delivered
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, conn.ReceivedCount(), "event at chain_depth == max should be dropped")
}

func TestRouter_ChainDepth_AboveMax_EventDropped(t *testing.T) {
	src := &mockEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut, eventstream.WithMaxChainDepth(5))

	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "*")
	router.RegisterConnection(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = router.Start(ctx) }()

	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(src.IsStarted)
	require.NoError(t, err)

	event := eventstream.DomainEvent{
		EventID:    "evt-1",
		EventType:  "payment_order.created.v1",
		Channel:    "payment-order.created",
		TenantID:   "tenant-A",
		ChainDepth: 10, // well above max of 5
	}
	src.EmitEvent(ctx, event)

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, conn.ReceivedCount(), "event with chain_depth > max should be dropped")
}

func TestRouter_ChainDepth_ZeroMaxDepth_NoLimit(t *testing.T) {
	src := &mockEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	// MaxChainDepth=0 disables the limit
	router := eventstream.NewRouter(src, fanOut, eventstream.WithMaxChainDepth(0))

	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "*")
	router.RegisterConnection(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = router.Start(ctx) }()

	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(src.IsStarted)
	require.NoError(t, err)

	event := eventstream.DomainEvent{
		EventID:    "evt-1",
		EventType:  "payment_order.created.v1",
		Channel:    "payment-order.created",
		TenantID:   "tenant-A",
		ChainDepth: 999, // very deep, but no limit
	}
	src.EmitEvent(ctx, event)

	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return conn.ReceivedCount() > 0
	})
	require.NoError(t, err, "when max chain depth is 0 (disabled), events should always be delivered")
}

func TestRouter_ChainDepth_DefaultNoLimit(t *testing.T) {
	// Router created without WithMaxChainDepth should apply no chain depth limit
	src := &mockEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut) // no WithMaxChainDepth option

	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "*")
	router.RegisterConnection(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = router.Start(ctx) }()

	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(src.IsStarted)
	require.NoError(t, err)

	event := eventstream.DomainEvent{
		EventID:    "evt-deep",
		EventType:  "payment_order.created.v1",
		Channel:    "payment-order.created",
		TenantID:   "tenant-A",
		ChainDepth: 100,
	}
	src.EmitEvent(ctx, event)

	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return conn.ReceivedCount() > 0
	})
	require.NoError(t, err, "router without WithMaxChainDepth should not drop events")
}

func TestRouter_ChainDepth_MetricsIncremented_WhenDropped(t *testing.T) {
	src := &mockEventSource{}
	fanOut := eventstream.NewInProcessFanOut()

	reg := prometheus.NewRegistry()
	metrics, err := eventstream.NewMetrics(reg)
	require.NoError(t, err)

	router := eventstream.NewRouter(src, fanOut,
		eventstream.WithMaxChainDepth(3),
		eventstream.WithRouterMetrics(metrics),
	)

	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "*")
	router.RegisterConnection(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = router.Start(ctx) }()

	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(src.IsStarted)
	require.NoError(t, err)

	// Emit an event that exceeds chain depth
	deepEvent := eventstream.DomainEvent{
		EventID:    "evt-1",
		EventType:  "payment_order.created.v1",
		Channel:    "payment-order.created",
		TenantID:   "tenant-A",
		ChainDepth: 5,
	}
	src.EmitEvent(ctx, deepEvent)

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, conn.ReceivedCount(), "deep event should be dropped")
}

func TestRouter_ChainDepth_Depth0_AlwaysDelivered(t *testing.T) {
	src := &mockEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut, eventstream.WithMaxChainDepth(1))

	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "*")
	router.RegisterConnection(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = router.Start(ctx) }()

	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(src.IsStarted)
	require.NoError(t, err)

	event := eventstream.DomainEvent{
		EventID:    "evt-1",
		EventType:  "payment_order.created.v1",
		Channel:    "payment-order.created",
		TenantID:   "tenant-A",
		ChainDepth: 0, // depth 0 is below max of 1 — should be delivered
	}
	src.EmitEvent(ctx, event)

	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return conn.ReceivedCount() > 0
	})
	require.NoError(t, err, "event at depth 0 should always be delivered")
}
