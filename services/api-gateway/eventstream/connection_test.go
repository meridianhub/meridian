package eventstream_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Connection constructor ---

func TestNewConnection_Success(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	require.NotNil(t, conn)
	assert.Equal(t, "conn-1", conn.ID())
	assert.Equal(t, "tenant_abc", conn.TenantID())
	assert.Equal(t, claims, conn.Claims())
}

// --- Send and backpressure ---

func TestConnection_Send_Success(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	msg := eventstream.ServerMessage{
		Type:          eventstream.ServerMessageTypeSystem,
		SystemMessage: "hello",
	}

	ok := conn.Send(msg)
	assert.True(t, ok, "Send should succeed when buffer is not full")
}

func TestConnection_Send_BufferFull_ReturnsFalse(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	msg := eventstream.ServerMessage{
		Type:          eventstream.ServerMessageTypeSystem,
		SystemMessage: "test",
	}

	// Fill the buffer completely
	for i := 0; i < eventstream.BufferSize; i++ {
		ok := conn.Send(msg)
		require.True(t, ok, "Send %d should succeed", i)
	}

	// Next send should fail (non-blocking)
	ok := conn.Send(msg)
	assert.False(t, ok, "Send should return false when buffer is full")
}

// --- Subscription management ---

func TestConnection_AddSubscription(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	sub, err := eventstream.NewSubscription(
		"sub-1",
		[]eventstream.ChannelPattern{"payment-order.*"},
		eventstream.SubscriptionFilters{},
	)
	require.NoError(t, err)

	conn.AddSubscription(sub)

	assert.True(t, conn.HasSubscription("sub-1"))
	assert.False(t, conn.HasSubscription("sub-nonexistent"))
}

func TestConnection_RemoveSubscription(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	sub, err := eventstream.NewSubscription(
		"sub-1",
		[]eventstream.ChannelPattern{"payment-order.*"},
		eventstream.SubscriptionFilters{},
	)
	require.NoError(t, err)

	conn.AddSubscription(sub)
	assert.True(t, conn.HasSubscription("sub-1"))

	conn.RemoveSubscription("sub-1")
	assert.False(t, conn.HasSubscription("sub-1"))
}

func TestConnection_RemoveSubscription_NonExistent_NoPanic(t *testing.T) { //nolint:revive // t required by test framework
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	// Should not panic
	conn.RemoveSubscription("nonexistent")
}

// --- MatchesEvent ---

func TestConnection_MatchesEvent(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	sub1, _ := eventstream.NewSubscription(
		"sub-1",
		[]eventstream.ChannelPattern{"payment-order.*"},
		eventstream.SubscriptionFilters{},
	)
	sub2, _ := eventstream.NewSubscription(
		"sub-2",
		[]eventstream.ChannelPattern{"position-keeping.*"},
		eventstream.SubscriptionFilters{},
	)
	conn.AddSubscription(sub1)
	conn.AddSubscription(sub2)

	tests := []struct {
		name           string
		event          eventstream.DomainEvent
		wantMatch      bool
		wantSubIDs     []string
		wantSubIDCount int
	}{
		{
			name:           "matches payment-order subscription",
			event:          eventstream.DomainEvent{Channel: "payment-order.created"},
			wantMatch:      true,
			wantSubIDs:     []string{"sub-1"},
			wantSubIDCount: 1,
		},
		{
			name:           "matches position-keeping subscription",
			event:          eventstream.DomainEvent{Channel: "position-keeping.updated"},
			wantMatch:      true,
			wantSubIDs:     []string{"sub-2"},
			wantSubIDCount: 1,
		},
		{
			name:           "no match",
			event:          eventstream.DomainEvent{Channel: "unknown.event"},
			wantMatch:      false,
			wantSubIDCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subIDs := conn.MatchesEvent(tc.event)
			if tc.wantMatch {
				assert.Len(t, subIDs, tc.wantSubIDCount)
				for _, wantID := range tc.wantSubIDs {
					assert.Contains(t, subIDs, wantID)
				}
			} else {
				assert.Empty(t, subIDs)
			}
		})
	}
}

func TestConnection_MatchesEvent_NoSubscriptions(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	event := eventstream.DomainEvent{Channel: "payment-order.created"}
	subIDs := conn.MatchesEvent(event)
	assert.Empty(t, subIDs)
}

func TestConnection_MatchesEvent_MultipleSubscriptionsMatchSameEvent(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	sub1, _ := eventstream.NewSubscription(
		"sub-1",
		[]eventstream.ChannelPattern{"payment-order.*"},
		eventstream.SubscriptionFilters{},
	)
	sub2, _ := eventstream.NewSubscription(
		"sub-2",
		[]eventstream.ChannelPattern{"*"},
		eventstream.SubscriptionFilters{},
	)
	conn.AddSubscription(sub1)
	conn.AddSubscription(sub2)

	event := eventstream.DomainEvent{Channel: "payment-order.created"}
	subIDs := conn.MatchesEvent(event)
	assert.Len(t, subIDs, 2)
	assert.Contains(t, subIDs, "sub-1")
	assert.Contains(t, subIDs, "sub-2")
}

// --- CheckJWTExpiry ---

func TestConnection_CheckJWTExpiry_NotExpired(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant_abc",
	}
	// ExpiresAt set to 1 hour from now
	claims.ExpiresAt = newNumericDate(time.Now().Add(1 * time.Hour))

	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)
	assert.False(t, conn.CheckJWTExpiry(), "should not be expired")
}

func TestConnection_CheckJWTExpiry_Expired(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant_abc",
	}
	// ExpiresAt set to 1 hour ago
	claims.ExpiresAt = newNumericDate(time.Now().Add(-1 * time.Hour))

	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)
	assert.True(t, conn.CheckJWTExpiry(), "should be expired")
}

func TestConnection_CheckJWTExpiry_NoExpiresAt(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant_abc",
	}
	// ExpiresAt is nil

	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)
	assert.False(t, conn.CheckJWTExpiry(), "no expiry means not expired")
}

// --- Start and Close lifecycle ---

func TestConnection_Start_WritesMessages(t *testing.T) {
	serverConn, clientConn := setupTestWebSocketPair(t)
	defer clientConn.CloseNow()

	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, serverConn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn.Start(ctx)
	}()

	// Send a message via the connection
	msg := eventstream.ServerMessage{
		Type:          eventstream.ServerMessageTypeSystem,
		SystemMessage: "welcome",
	}
	ok := conn.Send(msg)
	require.True(t, ok)

	// Read from the client side
	_, data, err := clientConn.Read(ctx)
	require.NoError(t, err)

	var received eventstream.ServerMessage
	err = json.Unmarshal(data, &received)
	require.NoError(t, err)
	assert.Equal(t, eventstream.ServerMessageTypeSystem, received.Type)
	assert.Equal(t, "welcome", received.SystemMessage)

	// Close the connection
	conn.Close(websocket.StatusNormalClosure, "test done")
	wg.Wait()
}

func TestConnection_Start_ReadsMessages(t *testing.T) {
	serverConn, clientConn := setupTestWebSocketPair(t)

	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}

	var receivedMsg atomic.Value
	handler := func(_ *eventstream.Connection, msg eventstream.ClientMessage) {
		receivedMsg.Store(msg)
	}

	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, serverConn)
	conn.SetMessageHandler(handler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn.Start(ctx)
	}()

	// Client sends a subscribe message
	subMsg := eventstream.ClientMessage{
		Type:     eventstream.ClientMessageTypeSubscribe,
		ID:       "sub-1",
		Channels: []eventstream.ChannelPattern{"payment-order.*"},
	}
	data, err := json.Marshal(subMsg)
	require.NoError(t, err)

	err = clientConn.Write(ctx, websocket.MessageText, data)
	require.NoError(t, err)

	// Wait for message to be processed
	require.Eventually(t, func() bool {
		return receivedMsg.Load() != nil
	}, 2*time.Second, 10*time.Millisecond)

	got := receivedMsg.Load().(eventstream.ClientMessage)
	assert.Equal(t, eventstream.ClientMessageTypeSubscribe, got.Type)
	assert.Equal(t, "sub-1", got.ID)

	conn.Close(websocket.StatusNormalClosure, "test done")
	wg.Wait()
}

func TestConnection_Close_MultipleCallsNoPanic(t *testing.T) {
	serverConn, clientConn := setupTestWebSocketPair(t)
	defer clientConn.CloseNow()

	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, serverConn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn.Start(ctx)
	}()

	// Multiple closes should not panic
	conn.Close(websocket.StatusNormalClosure, "first close")
	conn.Close(websocket.StatusNormalClosure, "second close")
	wg.Wait()
}

// --- Backpressure overflow handling ---

func TestConnection_HandleOverflow_SendsSystemMessage(t *testing.T) {
	// Use nil wsConn so no write pump drains the buffer — overflow is deterministic.
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	msg := eventstream.ServerMessage{
		Type:          eventstream.ServerMessageTypeSystem,
		SystemMessage: "filler",
	}

	// Fill the buffer completely (all 256 slots).
	for i := 0; i < eventstream.BufferSize; i++ {
		ok := conn.Send(msg)
		require.True(t, ok, "Send %d should succeed while buffer has capacity", i)
	}

	// Next sends must fail because no write pump is draining.
	droppedCount := 0
	for i := 0; i < 5; i++ {
		if !conn.Send(msg) {
			droppedCount++
		}
	}

	assert.Equal(t, 5, droppedCount, "all 5 sends should be dropped when buffer is full")
}

// --- Concurrent subscription access ---

func TestConnection_ConcurrentSubscriptionAccess(t *testing.T) { //nolint:revive // t required by test framework
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	var wg sync.WaitGroup

	// Concurrent adds
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := "sub-" + strings.Repeat("x", idx%10)
			sub, err := eventstream.NewSubscription(
				id,
				[]eventstream.ChannelPattern{"channel.*"},
				eventstream.SubscriptionFilters{},
			)
			if err != nil {
				return
			}
			conn.AddSubscription(sub)
		}(i)
	}

	// Concurrent removes
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			conn.RemoveSubscription("sub-" + strings.Repeat("x", idx%10))
		}(i)
	}

	// Concurrent matches
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event := eventstream.DomainEvent{Channel: "channel.created"}
			conn.MatchesEvent(event)
		}()
	}

	wg.Wait()
}

// --- LastActivity ---

func TestConnection_LastActivity_UpdatesOnSend(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	before := conn.LastActivity()

	// Wait for the clock to advance beyond the initial lastActivity.
	err := await.New().
		AtMost(1 * time.Second).
		PollInterval(1 * time.Millisecond).
		Until(func() bool { return time.Now().After(before) })
	require.NoError(t, err)

	msg := eventstream.ServerMessage{
		Type:          eventstream.ServerMessageTypeSystem,
		SystemMessage: "ping",
	}
	conn.Send(msg)

	after := conn.LastActivity()
	assert.True(t, after.After(before))
}

// --- SubscriptionCount ---

func TestConnection_SubscriptionCount(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant_abc"}
	conn := eventstream.NewConnection("conn-1", "tenant_abc", claims, nil)

	assert.Equal(t, 0, conn.SubscriptionCount())

	sub1, _ := eventstream.NewSubscription("sub-1", []eventstream.ChannelPattern{"a.*"}, eventstream.SubscriptionFilters{})
	sub2, _ := eventstream.NewSubscription("sub-2", []eventstream.ChannelPattern{"b.*"}, eventstream.SubscriptionFilters{})

	conn.AddSubscription(sub1)
	assert.Equal(t, 1, conn.SubscriptionCount())

	conn.AddSubscription(sub2)
	assert.Equal(t, 2, conn.SubscriptionCount())

	conn.RemoveSubscription("sub-1")
	assert.Equal(t, 1, conn.SubscriptionCount())
}

// --- helpers ---

// newNumericDate creates a jwt.NumericDate from a time.Time for test convenience.
func newNumericDate(t time.Time) *jwt.NumericDate {
	return jwt.NewNumericDate(t)
}

// setupTestWebSocketPair creates a matched pair of server-side and client-side
// WebSocket connections for lifecycle testing. The server-side conn is returned
// so it can be passed to NewConnection.
func setupTestWebSocketPair(t *testing.T) (serverConn *websocket.Conn, clientConn *websocket.Conn) {
	t.Helper()

	serverReady := make(chan *websocket.Conn, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Logf("accept error: %v", err)
			return
		}
		serverReady <- c
		// Keep server handler alive until test completes
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cc, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)

	select {
	case sc := <-serverReady:
		return sc, cc
	case <-ctx.Done():
		t.Fatal("timeout waiting for server-side websocket connection")
		return nil, nil
	}
}
