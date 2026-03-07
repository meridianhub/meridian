package eventstream_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetrics_Handler_ConnectionLifecycle verifies that WithHandlerMetrics causes the
// Handler to increment the active-connections gauge on open and decrement on close.
func TestMetrics_Handler_ConnectionLifecycle(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := eventstream.NewMetrics(reg)
	require.NoError(t, err)

	router := eventstream.NewRouter(&stubEventSource{}, eventstream.NewInProcessFanOut())
	h := eventstream.NewHandler(router, nil, eventstream.WithHandlerMetrics(m))

	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-metrics",
		Roles:    []string{"ops:admin"},
	}

	srv := httptest.NewServer(injectClaimsMiddleware(claims, h))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)

	// Wait for connection to be registered.
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-metrics")) > 0
		})
	require.NoError(t, err)

	// Active count should be 1.
	activeVal := testutil.ToFloat64(m.ActiveConnectionsForTenant("tenant-metrics"))
	assert.InDelta(t, 1.0, activeVal, 0.001, "expected 1 active connection after open")

	// Close the connection and wait for deregistration.
	clientConn.Close(websocket.StatusNormalClosure, "done")

	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-metrics")) == 0
		})
	require.NoError(t, err)

	// Active count should be 0 after close.
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return testutil.ToFloat64(m.ActiveConnectionsForTenant("tenant-metrics")) < 0.001
		})
	require.NoError(t, err, "expected 0 active connections after close")
}

// TestMetrics_Router_EventDelivery verifies that WithRouterMetrics causes the Router
// to increment the events-delivered counter when an event is successfully sent.
func TestMetrics_Router_EventDelivery(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := eventstream.NewMetrics(reg)
	require.NoError(t, err)

	fanOut, router := newTestRouterWithMetrics(t, m)

	conn := newTestConnectionWithSub(t, "conn-1", "tenant-A", "sub-1", "payment-order.*")
	router.RegisterConnection(conn)

	event := eventstream.DomainEvent{
		EventID:   "evt-1",
		EventType: "payment_order.created",
		Topic:     "payment-order.created.v1",
		Channel:   "payment-order.created",
		TenantID:  "tenant-A",
		Timestamp: time.Now().Add(-50 * time.Millisecond),
	}

	err = fanOut.Publish(context.Background(), event)
	require.NoError(t, err)

	// Wait for the event to be processed.
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return testutil.CollectAndCount(m.EventsDelivered()) > 0
		})
	require.NoError(t, err)

	count := testutil.CollectAndCount(m.EventsDelivered())
	assert.GreaterOrEqual(t, count, 1, "expected at least one delivered event recorded")
}

// TestMetrics_Router_DroppedEvent verifies that WithRouterMetrics causes the Router
// to increment the events-dropped counter when the connection buffer is full.
func TestMetrics_Router_DroppedEvent(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := eventstream.NewMetrics(reg)
	require.NoError(t, err)

	fanOut, router := newTestRouterWithMetrics(t, m)

	// Use a blocked connection that always returns false from Send.
	blockedConn := &blockedTestConn{id: "conn-blocked", tenantID: "tenant-B"}
	blockedConn.addSub("sub-2", "payment-order.*")
	router.RegisterConnection(blockedConn)

	event := eventstream.DomainEvent{
		EventID:   "evt-drop",
		EventType: "payment_order.created",
		Topic:     "payment-order.created.v1",
		Channel:   "payment-order.created",
		TenantID:  "tenant-B",
		Timestamp: time.Now(),
	}

	err = fanOut.Publish(context.Background(), event)
	require.NoError(t, err)

	// Wait for the drop to be recorded.
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return testutil.CollectAndCount(m.EventsDropped()) > 0
		})
	require.NoError(t, err)

	count := testutil.CollectAndCount(m.EventsDropped())
	assert.GreaterOrEqual(t, count, 1, "expected at least one dropped event recorded")
}

// newTestRouterWithMetrics creates a Router wired with the given Metrics and a FanOut
// that routes directly to HandleEvent (mirrors the InProcessFanOut → Router path).
func newTestRouterWithMetrics(t *testing.T, m *eventstream.Metrics) (*eventstream.InProcessFanOut, *eventstream.Router) {
	t.Helper()
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(&stubEventSource{}, fanOut, eventstream.WithRouterMetrics(m))
	return fanOut, router
}

// blockedTestConn is a ConnectionSender whose Send always returns false (simulates a full buffer).
type blockedTestConn struct {
	id       string
	tenantID string
	subs     map[string]eventstream.ChannelPattern
}

func (b *blockedTestConn) ID() string       { return b.id }
func (b *blockedTestConn) TenantID() string { return b.tenantID }
func (b *blockedTestConn) Send(_ eventstream.ServerMessage) bool {
	return false // always drops
}

func (b *blockedTestConn) MatchesEvent(event eventstream.DomainEvent) []string {
	var matched []string
	for subID, pattern := range b.subs {
		if pattern.Matches(event.Channel) {
			matched = append(matched, subID)
		}
	}
	return matched
}

func (b *blockedTestConn) addSub(id string, pattern eventstream.ChannelPattern) {
	if b.subs == nil {
		b.subs = make(map[string]eventstream.ChannelPattern)
	}
	b.subs[id] = pattern
}
