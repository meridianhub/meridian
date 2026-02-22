package eventstream_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/meridianhub/meridian/services/gateway/eventstream"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Context helpers ---

// TestContextWithClaims_RoundTrip verifies that ClaimsFromContext retrieves the
// exact claims stored by ContextWithClaims. In production, the gateway wiring
// layer calls ContextWithClaims as an adapter after the auth middleware has
// injected claims; tests use it directly to simulate that wiring.
func TestContextWithClaims_RoundTrip(t *testing.T) {
	claims := &platformauth.Claims{UserID: "user-1", TenantID: "tenant-abc"}

	ctx := eventstream.ContextWithClaims(context.Background(), claims)
	got, ok := eventstream.ClaimsFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, claims, got)
}

// TestClaimsFromContext_NoClaims_ReturnsFalse verifies that ClaimsFromContext
// returns false when no claims are present in the context.
func TestClaimsFromContext_NoClaims_ReturnsFalse(t *testing.T) {
	_, ok := eventstream.ClaimsFromContext(context.Background())
	assert.False(t, ok)
}

// --- Handler construction ---

func TestNewHandler_DefaultRoleAccess(t *testing.T) {
	router := eventstream.NewRouter(
		&stubEventSource{},
		eventstream.NewInProcessFanOut(),
	)
	h := eventstream.NewHandler(router, nil)
	require.NotNil(t, h)
}

func TestNewHandler_CustomRoleAccess(t *testing.T) {
	router := eventstream.NewRouter(
		&stubEventSource{},
		eventstream.NewInProcessFanOut(),
	)
	roleAccess := eventstream.RoleChannelAccess{
		"custom:role": {"my-channel.*"},
	}
	h := eventstream.NewHandler(router, nil, eventstream.WithRoleChannelAccess(roleAccess))
	require.NotNil(t, h)
}

// TestWithRoleChannelAccess_DefensiveCopy verifies that mutating the caller's
// map after passing it to WithRoleChannelAccess does not affect the handler.
func TestWithRoleChannelAccess_DefensiveCopy(t *testing.T) {
	router := eventstream.NewRouter(
		&stubEventSource{},
		eventstream.NewInProcessFanOut(),
	)
	roleAccess := eventstream.RoleChannelAccess{
		"ops:audit": {"audit.*"},
	}
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:audit"},
	}

	h := eventstream.NewHandler(router, nil, eventstream.WithRoleChannelAccess(roleAccess))

	// Mutate the original map after handler construction.
	delete(roleAccess, "ops:audit")

	// The handler should still authorize the channel using the snapshot taken at construction.
	srv := httptest.NewServer(injectClaimsMiddleware(claims, h))
	defer srv.Close()

	ctx := context.Background()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	clientConn, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer clientConn.CloseNow()

	subMsg := eventstream.ClientMessage{
		Type:     eventstream.ClientMessageTypeSubscribe,
		ID:       "sub-copy",
		Channels: []eventstream.ChannelPattern{"audit.events"},
	}
	data, _ := json.Marshal(subMsg)
	require.NoError(t, clientConn.Write(ctx, websocket.MessageText, data))

	// Expect a "subscribed" confirmation (not an error), proving the copy was used.
	var confirmed eventstream.ServerMessage
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			_, msgData, readErr := clientConn.Read(ctx)
			if readErr != nil {
				return false
			}
			return json.Unmarshal(msgData, &confirmed) == nil
		})
	require.NoError(t, err)
	assert.Equal(t, eventstream.ServerMessageTypeSubscribed, confirmed.Type)
}

// --- HTTP upgrade ---

func TestHandler_ServeHTTP_MissingClaims_Returns401(t *testing.T) {
	router := eventstream.NewRouter(
		&stubEventSource{},
		eventstream.NewInProcessFanOut(),
	)
	h := eventstream.NewHandler(router, nil)

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// Attempt WebSocket connection with no claims in context (no auth middleware)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.Error(t, err, "expected connection to be rejected")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestHandler_ServeHTTP_MissingTenantID_Returns401(t *testing.T) {
	router := eventstream.NewRouter(
		&stubEventSource{},
		eventstream.NewInProcessFanOut(),
	)
	h := eventstream.NewHandler(router, nil)

	// Inject claims with no tenant ID
	claimsHandler := injectClaimsMiddleware(
		&platformauth.Claims{UserID: "user-1", TenantID: ""},
		h,
	)

	srv := httptest.NewServer(claimsHandler)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.Error(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestHandler_ServeHTTP_ValidClaims_UpgradesSuccessfully(t *testing.T) {
	router := eventstream.NewRouter(
		&stubEventSource{},
		eventstream.NewInProcessFanOut(),
	)
	h := eventstream.NewHandler(router, nil)

	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:admin"},
	}
	authHandler := injectClaimsMiddleware(claims, h)

	srv := httptest.NewServer(authHandler)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err, "WebSocket upgrade should succeed with valid claims")
	defer clientConn.CloseNow()

	// Connection registered in router
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-abc")) > 0
		})
	require.NoError(t, err, "connection should be registered in router")

	clientConn.Close(websocket.StatusNormalClosure, "test done")
}

// --- Connection registration and deregistration ---

func TestHandler_ServeHTTP_ConnectionRegisteredAndDeregistered(t *testing.T) {
	router := eventstream.NewRouter(
		&stubEventSource{},
		eventstream.NewInProcessFanOut(),
	)
	h := eventstream.NewHandler(router, nil)

	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:admin"},
	}
	authHandler := injectClaimsMiddleware(claims, h)

	srv := httptest.NewServer(authHandler)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)

	// Wait for registration
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-abc")) > 0
		})
	require.NoError(t, err, "connection should register")

	// Close client side
	clientConn.Close(websocket.StatusNormalClosure, "done")

	// Wait for deregistration
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-abc")) == 0
		})
	require.NoError(t, err, "connection should be deregistered after close")
}

// --- Role-based channel authorization ---

func TestAuthorizeChannels_AdminRole_AllowsAll(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:admin"},
	}
	err := eventstream.AuthorizeChannels(claims, eventstream.DefaultRoleAccess, []string{
		"current-account.created",
		"payment-order.*",
		"audit.events.party.updated",
	})
	assert.NoError(t, err)
}

func TestAuthorizeChannels_AccountsRole_AllowedChannels(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:accounts"},
	}
	err := eventstream.AuthorizeChannels(claims, eventstream.DefaultRoleAccess, []string{
		"current-account.created",
		"party.updated",
	})
	assert.NoError(t, err)
}

func TestAuthorizeChannels_AccountsRole_DisallowedChannel(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:accounts"},
	}
	err := eventstream.AuthorizeChannels(claims, eventstream.DefaultRoleAccess, []string{
		"payment-order.created", // not allowed for ops:accounts
	})
	assert.Error(t, err)
}

func TestAuthorizeChannels_PaymentsRole_AllowedChannels(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:payments"},
	}
	err := eventstream.AuthorizeChannels(claims, eventstream.DefaultRoleAccess, []string{
		"payment-order.created",
		"audit.events.payment-order.updated",
	})
	assert.NoError(t, err)
}

func TestAuthorizeChannels_NoRoles_DeniesAll(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{},
	}
	err := eventstream.AuthorizeChannels(claims, eventstream.DefaultRoleAccess, []string{
		"current-account.created",
	})
	assert.Error(t, err)
}

func TestAuthorizeChannels_MultipleRoles_UnionOfAllowed(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:accounts", "ops:payments"},
	}
	// Both channels individually allowed by respective roles
	err := eventstream.AuthorizeChannels(claims, eventstream.DefaultRoleAccess, []string{
		"current-account.created",
		"payment-order.created",
	})
	assert.NoError(t, err)
}

func TestAuthorizeChannels_FinanceRole_AllowedChannels(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:finance"},
	}
	err := eventstream.AuthorizeChannels(claims, eventstream.DefaultRoleAccess, []string{
		"financial-accounting.entry.created",
		"position-keeping.updated",
		"reconciliation.completed",
	})
	assert.NoError(t, err)
}

func TestAuthorizeChannels_AuditRole_AllowsAuditChannels(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:audit"},
	}
	err := eventstream.AuthorizeChannels(claims, eventstream.DefaultRoleAccess, []string{
		"audit.events.party.created",
		"audit.events.payment-order.updated",
	})
	assert.NoError(t, err)
}

func TestAuthorizeChannels_NilClaims_DeniesAll(t *testing.T) {
	err := eventstream.AuthorizeChannels(nil, eventstream.DefaultRoleAccess, []string{
		"current-account.created",
	})
	assert.Error(t, err)
}

func TestAuthorizeChannels_EmptyChannels_NoError(t *testing.T) {
	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:accounts"},
	}
	err := eventstream.AuthorizeChannels(claims, eventstream.DefaultRoleAccess, []string{})
	assert.NoError(t, err)
}

func TestAuthorizeChannels_NilClaims_EmptyChannels_ReturnsError(t *testing.T) {
	// nil claims should error even when channels is empty, per the function contract.
	err := eventstream.AuthorizeChannels(nil, eventstream.DefaultRoleAccess, []string{})
	assert.ErrorIs(t, err, eventstream.ErrUnauthorizedNoClaims)
}

// --- Subscribe message handling via WebSocket ---

func TestHandler_Subscribe_AuthorizedChannel_SendsSubscribed(t *testing.T) {
	router := eventstream.NewRouter(
		&stubEventSource{},
		eventstream.NewInProcessFanOut(),
	)
	h := eventstream.NewHandler(router, nil)

	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:payments"},
	}
	authHandler := injectClaimsMiddleware(claims, h)

	srv := httptest.NewServer(authHandler)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	defer clientConn.CloseNow()

	// Wait for connection to be established
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-abc")) > 0
		})
	require.NoError(t, err)

	// Send subscribe message for an authorized channel
	subMsg := eventstream.ClientMessage{
		Type:     eventstream.ClientMessageTypeSubscribe,
		ID:       "sub-1",
		Channels: []eventstream.ChannelPattern{"payment-order.*"},
	}
	data, err := json.Marshal(subMsg)
	require.NoError(t, err)
	err = clientConn.Write(ctx, websocket.MessageText, data)
	require.NoError(t, err)

	// Read subscription confirmation
	var confirmed eventstream.ServerMessage
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		UntilNoError(func() error {
			_, msgData, readErr := clientConn.Read(ctx)
			if readErr != nil {
				return readErr
			}
			return json.Unmarshal(msgData, &confirmed)
		})
	require.NoError(t, err)
	assert.Equal(t, eventstream.ServerMessageTypeSubscribed, confirmed.Type)
	assert.Equal(t, "sub-1", confirmed.SubscriptionID)
}

func TestHandler_Subscribe_UnauthorizedChannel_SendsError(t *testing.T) {
	router := eventstream.NewRouter(
		&stubEventSource{},
		eventstream.NewInProcessFanOut(),
	)
	h := eventstream.NewHandler(router, nil)

	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:payments"}, // payments role cannot subscribe to current-account
	}
	authHandler := injectClaimsMiddleware(claims, h)

	srv := httptest.NewServer(authHandler)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	defer clientConn.CloseNow()

	// Wait for connection to be established
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-abc")) > 0
		})
	require.NoError(t, err)

	// Send subscribe message for an unauthorized channel
	subMsg := eventstream.ClientMessage{
		Type:     eventstream.ClientMessageTypeSubscribe,
		ID:       "sub-1",
		Channels: []eventstream.ChannelPattern{"current-account.created"},
	}
	data, err := json.Marshal(subMsg)
	require.NoError(t, err)
	err = clientConn.Write(ctx, websocket.MessageText, data)
	require.NoError(t, err)

	// Read error response
	var errMsg eventstream.ServerMessage
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		UntilNoError(func() error {
			_, msgData, readErr := clientConn.Read(ctx)
			if readErr != nil {
				return readErr
			}
			return json.Unmarshal(msgData, &errMsg)
		})
	require.NoError(t, err)
	assert.Equal(t, eventstream.ServerMessageTypeError, errMsg.Type)
	assert.Equal(t, eventstream.ErrorCodeUnauthorizedChannel, errMsg.ErrorCode)
}

func TestHandler_Unsubscribe_RemovesSubscription(t *testing.T) {
	router := eventstream.NewRouter(
		&stubEventSource{},
		eventstream.NewInProcessFanOut(),
	)
	h := eventstream.NewHandler(router, nil)

	claims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "tenant-abc",
		Roles:    []string{"ops:admin"},
	}
	authHandler := injectClaimsMiddleware(claims, h)

	srv := httptest.NewServer(authHandler)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	defer clientConn.CloseNow()

	// Wait for connection to be established
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-abc")) > 0
		})
	require.NoError(t, err)

	// Subscribe first
	subMsg := eventstream.ClientMessage{
		Type:     eventstream.ClientMessageTypeSubscribe,
		ID:       "sub-1",
		Channels: []eventstream.ChannelPattern{"payment-order.*"},
	}
	data, _ := json.Marshal(subMsg)
	require.NoError(t, clientConn.Write(ctx, websocket.MessageText, data))

	// Wait for subscribed confirmation
	var confirmed eventstream.ServerMessage
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		UntilNoError(func() error {
			_, msgData, readErr := clientConn.Read(ctx)
			if readErr != nil {
				return readErr
			}
			return json.Unmarshal(msgData, &confirmed)
		})
	require.NoError(t, err)
	require.Equal(t, eventstream.ServerMessageTypeSubscribed, confirmed.Type)

	// Unsubscribe
	unsubMsg := eventstream.ClientMessage{
		Type: eventstream.ClientMessageTypeUnsubscribe,
		ID:   "sub-1",
	}
	data, _ = json.Marshal(unsubMsg)
	require.NoError(t, clientConn.Write(ctx, websocket.MessageText, data))

	// Verify subscription is removed (check via connection in router)
	conns := router.GetConnectionsByTenant("tenant-abc")
	require.Len(t, conns, 1)

	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			conns := router.GetConnectionsByTenant("tenant-abc")
			if len(conns) == 0 {
				return false
			}
			return len(conns[0].MatchesEvent(eventstream.DomainEvent{
				TenantID: "tenant-abc",
				Channel:  "payment-order.created",
			})) == 0
		})
	require.NoError(t, err, "subscription should be removed after unsubscribe")
}

// --- helpers ---

// injectClaimsMiddleware wraps a handler to inject platform claims into context.
// This simulates what the JWT auth middleware does in production.
func injectClaimsMiddleware(claims *platformauth.Claims, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = eventstream.ContextWithClaims(ctx, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// stubEventSource is a no-op EventSource for handler tests.
type stubEventSource struct{}

func (s *stubEventSource) Start(_ context.Context, _ eventstream.EventHandler) error {
	return nil
}
