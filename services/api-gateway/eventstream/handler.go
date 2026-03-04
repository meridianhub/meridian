package eventstream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
)

// claimsContextKey is the context key used by ContextWithClaims.
// Using a private struct avoids collisions with other packages.
type claimsContextKey struct{}

// ContextWithClaims returns a derived context carrying the given claims under
// the eventstream-specific key. The integration layer (e.g., gateway server
// wiring) must call this as an adapter after the auth middleware has injected
// claims — see services/api-gateway/auth.GetClaimsFromContext.
//
// In tests, call this directly to simulate what the auth middleware does in
// production.
func ContextWithClaims(ctx context.Context, claims *platformauth.Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// ClaimsFromContext retrieves Claims stored by ContextWithClaims.
// Returns nil, false if no claims are present.
func ClaimsFromContext(ctx context.Context) (*platformauth.Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey{}).(*platformauth.Claims)
	if !ok || claims == nil {
		return nil, false
	}
	return claims, true
}

// Sentinel errors returned by AuthorizeChannels.
var (
	// ErrUnauthorizedNoClaims is returned when AuthorizeChannels is called with nil claims.
	ErrUnauthorizedNoClaims = errors.New("eventstream: unauthorized: no claims")

	// ErrUnauthorizedChannel is returned when a requested channel is not permitted by the caller's roles.
	ErrUnauthorizedChannel = errors.New("eventstream: unauthorized: channel not permitted")
)

// RoleChannelAccess maps role names to the channel patterns they are permitted
// to subscribe to. A pattern of "*" grants access to all channels.
type RoleChannelAccess map[string][]string

// DefaultRoleAccess defines the built-in role-to-channel access matrix.
var DefaultRoleAccess = RoleChannelAccess{
	"ops:admin":    {"*"},
	"ops:accounts": {"current-account.*", "party.*", "audit.events.party.*"},
	"ops:payments": {"payment-order.*", "audit.events.payment-order.*"},
	"ops:finance":  {"financial-accounting.*", "position-keeping.*", "reconciliation.*"},
	"ops:audit":    {"audit.*"},
}

// AuthorizeChannels checks whether all requested channel patterns are permitted
// for at least one of the roles in claims. Returns nil if authorized or if
// channels is empty and claims are present.
//
// Returns an error if:
//   - claims is nil (regardless of whether channels is empty)
//   - any channel pattern is not covered by the union of patterns allowed for
//     the claims' roles
func AuthorizeChannels(claims *platformauth.Claims, roleAccess RoleChannelAccess, channels []string) error {
	if claims == nil {
		return ErrUnauthorizedNoClaims
	}
	if len(channels) == 0 {
		return nil
	}

	// Build the union of allowed patterns across all roles held by this principal.
	var allowed []ChannelPattern
	for _, role := range claims.GetRoles() {
		if patterns, ok := roleAccess[role]; ok {
			for _, p := range patterns {
				allowed = append(allowed, ChannelPattern(p))
			}
		}
	}

	for _, ch := range channels {
		if !channelPermitted(ch, allowed) {
			return fmt.Errorf("%w: %s", ErrUnauthorizedChannel, ch)
		}
	}
	return nil
}

// channelPermitted reports whether ch is permitted by at least one of the given patterns.
// It delegates to ChannelPattern.Matches so that authorization uses the same semantics
// as subscription matching.
func channelPermitted(ch string, patterns []ChannelPattern) bool {
	for _, p := range patterns {
		if p.Matches(ch) {
			return true
		}
	}
	return false
}

// Handler is the HTTP handler that upgrades connections to WebSocket,
// validates auth claims, and registers the connection with the Router.
type Handler struct {
	router     *Router
	logger     *slog.Logger
	upgrader   websocket.AcceptOptions
	roleAccess RoleChannelAccess
	metrics    *Metrics
}

// HandlerOption is a functional option for configuring a Handler.
type HandlerOption func(*Handler)

// copyRoleChannelAccess returns a deep copy of a RoleChannelAccess map so that
// the Handler's internal state cannot be mutated by the caller after construction.
func copyRoleChannelAccess(src RoleChannelAccess) RoleChannelAccess {
	dst := make(RoleChannelAccess, len(src))
	for role, patterns := range src {
		cp := make([]string, len(patterns))
		copy(cp, patterns)
		dst[role] = cp
	}
	return dst
}

// WithRoleChannelAccess sets a custom role-to-channel access map.
// A defensive copy is made so that subsequent mutations to the caller's map
// do not affect the Handler.
func WithRoleChannelAccess(roleAccess RoleChannelAccess) HandlerOption {
	return func(h *Handler) {
		h.roleAccess = copyRoleChannelAccess(roleAccess)
	}
}

// WithAcceptOptions overrides the websocket.AcceptOptions used when upgrading
// connections. This is primarily for tests or deployments that need to configure
// allowed origins, compression, or other upgrade parameters.
func WithAcceptOptions(opts websocket.AcceptOptions) HandlerOption {
	return func(h *Handler) {
		h.upgrader = opts
	}
}

// WithHandlerMetrics attaches a Metrics instance to the Handler.
// When set, the Handler records connection open/close metrics.
func WithHandlerMetrics(m *Metrics) HandlerOption {
	return func(h *Handler) {
		h.metrics = m
	}
}

// NewHandler creates a Handler backed by the given Router.
// If logger is nil, slog.Default() is used.
func NewHandler(router *Router, logger *slog.Logger, opts ...HandlerOption) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Handler{
		router:     router,
		logger:     logger,
		roleAccess: copyRoleChannelAccess(DefaultRoleAccess),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// ServeHTTP upgrades the HTTP request to a WebSocket connection if the request
// carries valid auth claims with a non-empty tenant ID. The connection is
// registered with the Router and runs until the client disconnects or the
// request context is cancelled.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok || claims == nil {
		h.logger.Debug("websocket upgrade rejected: no claims in context")
		writeJSONUnauthorized(w, "unauthorized")
		return
	}

	if claims.TenantID == "" {
		h.logger.Debug("websocket upgrade rejected: empty tenant ID in claims",
			slog.String("user_id", claims.UserID),
		)
		writeJSONUnauthorized(w, "unauthorized: missing tenant ID")
		return
	}

	wsConn, err := websocket.Accept(w, r, &h.upgrader)
	if err != nil {
		h.logger.Debug("websocket accept failed", slog.String("error", err.Error()))
		return
	}

	connID := uuid.New().String()
	conn := NewConnection(connID, claims.TenantID, claims, wsConn)
	conn.SetMessageHandler(h.makeMessageHandler(claims))

	if h.metrics != nil {
		h.metrics.IncConnectionOpened(claims.TenantID)
	}
	h.router.RegisterConnection(conn)
	defer func() {
		h.router.UnregisterConnection(connID)
		if h.metrics != nil {
			h.metrics.IncConnectionClosed(claims.TenantID, "disconnect")
		}
	}()

	h.logger.Info("websocket connection established",
		slog.String("conn_id", connID),
		slog.String("tenant_id", claims.TenantID),
		slog.String("user_id", claims.UserID),
	)

	conn.Start(r.Context())

	h.logger.Info("websocket connection closed",
		slog.String("conn_id", connID),
		slog.String("tenant_id", claims.TenantID),
	)
}

// makeMessageHandler returns a MessageHandler that processes client subscribe/unsubscribe messages.
func (h *Handler) makeMessageHandler(claims *platformauth.Claims) MessageHandler {
	return func(conn *Connection, msg ClientMessage) {
		switch msg.Type {
		case ClientMessageTypeSubscribe:
			h.handleSubscribe(conn, claims, msg)
		case ClientMessageTypeUnsubscribe:
			h.handleUnsubscribe(conn, msg)
		default:
			h.logger.Debug("unknown client message type",
				slog.String("conn_id", conn.ID()),
				slog.String("type", string(msg.Type)),
			)
		}
	}
}

// handleSubscribe validates channel authorization, creates a Subscription, and
// either confirms via a "subscribed" message or replies with an error message.
func (h *Handler) handleSubscribe(conn *Connection, claims *platformauth.Claims, msg ClientMessage) {
	// Convert ChannelPattern slice to string slice for authorization check.
	channelStrs := make([]string, len(msg.Channels))
	for i, ch := range msg.Channels {
		channelStrs[i] = string(ch)
	}

	if err := AuthorizeChannels(claims, h.roleAccess, channelStrs); err != nil {
		h.logger.Debug("subscribe rejected: unauthorized channel",
			slog.String("conn_id", conn.ID()),
			slog.String("subscription_id", msg.ID),
			slog.Any("channels", msg.Channels),
		)
		conn.Send(ServerMessage{
			Type:           ServerMessageTypeError,
			SubscriptionID: msg.ID,
			ErrorCode:      ErrorCodeUnauthorizedChannel,
			ErrorMessage:   "one or more requested channels are not permitted for your role",
		})
		return
	}

	sub, err := NewSubscription(msg.ID, msg.Channels, msg.Filters)
	if err != nil {
		h.logger.Debug("subscribe rejected: invalid subscription",
			slog.String("conn_id", conn.ID()),
			slog.String("subscription_id", msg.ID),
			slog.String("error", err.Error()),
		)
		conn.Send(ServerMessage{
			Type:           ServerMessageTypeError,
			SubscriptionID: msg.ID,
			ErrorCode:      ErrorCodeInvalidChannel,
			ErrorMessage:   err.Error(),
		})
		return
	}

	conn.AddSubscription(sub)

	conn.Send(ServerMessage{
		Type:           ServerMessageTypeSubscribed,
		SubscriptionID: msg.ID,
	})

	h.logger.Debug("subscription created",
		slog.String("conn_id", conn.ID()),
		slog.String("subscription_id", msg.ID),
		slog.Any("channels", msg.Channels),
	)
}

// handleUnsubscribe removes the subscription identified by msg.ID from the connection.
func (h *Handler) handleUnsubscribe(conn *Connection, msg ClientMessage) {
	conn.RemoveSubscription(msg.ID)
	h.logger.Debug("subscription removed",
		slog.String("conn_id", conn.ID()),
		slog.String("subscription_id", msg.ID),
	)
}

// writeJSONUnauthorized writes a 401 Unauthorized JSON response.
// It sets Content-Type: application/json so clients can parse the error body.
func writeJSONUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, message)
}
