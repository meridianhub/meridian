package eventstream

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
)

// Connection lifecycle and buffer constants.
const (
	// BufferSize is the capacity of the per-connection send channel.
	BufferSize = 256

	// PingInterval is how often the server sends a WebSocket ping.
	PingInterval = 30 * time.Second

	// PongTimeout is the maximum time to wait for a pong response.
	PongTimeout = 30 * time.Second

	// JWTCheckInterval is how often the connection checks JWT expiry.
	JWTCheckInterval = 60 * time.Second

	// IdleTimeout is the maximum time a connection can remain without activity.
	IdleTimeout = 5 * time.Minute

	// maxReadSize limits the size of incoming WebSocket messages (64 KB).
	maxReadSize = 64 * 1024

	// CloseCodeTokenExpired is a custom WebSocket close code (4001) indicating
	// that the connection was closed because the JWT token expired. Codes
	// 4000-4999 are reserved for application use per RFC 6455.
	CloseCodeTokenExpired = websocket.StatusCode(4001)
)

// MessageHandler is a callback invoked when the connection receives a client message.
type MessageHandler func(conn *Connection, msg ClientMessage)

// Connection represents a single WebSocket client connection with per-connection
// state including subscriptions, backpressure management, and lifecycle control.
//
// Connection is safe for concurrent use. All subscription operations and Send
// calls are protected by a mutex.
type Connection struct {
	id       string
	tenantID string
	claims   *platformauth.Claims

	subscriptions map[string]Subscription
	mu            sync.RWMutex

	sendChan chan ServerMessage
	wsConn   *websocket.Conn

	ctx    context.Context
	cancel context.CancelFunc

	lastActivity time.Time
	activityMu   sync.RWMutex

	closeOnce    sync.Once
	msgHandler   MessageHandler
	logger       *slog.Logger
	droppedCount atomic.Int64
}

// NewConnection constructs a Connection. The wsConn may be nil for unit tests
// that only exercise subscription management and Send buffering (without a
// real write pump).
func NewConnection(id, tenantID string, claims *platformauth.Claims, wsConn *websocket.Conn) *Connection {
	ctx, cancel := context.WithCancel(context.Background())
	return &Connection{
		id:            id,
		tenantID:      tenantID,
		claims:        claims,
		subscriptions: make(map[string]Subscription),
		sendChan:      make(chan ServerMessage, BufferSize),
		wsConn:        wsConn,
		ctx:           ctx,
		cancel:        cancel,
		lastActivity:  time.Now(),
		logger:        slog.Default(),
	}
}

// ID returns the connection identifier.
func (c *Connection) ID() string { return c.id }

// TenantID returns the tenant identifier for this connection.
func (c *Connection) TenantID() string { return c.tenantID }

// Claims returns the JWT claims associated with this connection.
func (c *Connection) Claims() *platformauth.Claims { return c.claims }

// SetMessageHandler sets the callback invoked for each client message.
// Must be called before Start.
func (c *Connection) SetMessageHandler(h MessageHandler) {
	c.msgHandler = h
}

// Send enqueues a ServerMessage for delivery. It is non-blocking: if the
// buffer is full the message is dropped and false is returned.
func (c *Connection) Send(msg ServerMessage) bool {
	c.touchActivity()
	select {
	case c.sendChan <- msg:
		return true
	default:
		c.handleOverflow()
		return false
	}
}

// handleOverflow increments the dropped message counter. The write pump
// periodically checks and resets this counter to send BUFFER_OVERFLOW
// notifications to the client.
func (c *Connection) handleOverflow() {
	c.droppedCount.Add(1)
}

// AddSubscription registers a subscription for this connection.
// If a subscription with the same ID already exists, it is replaced.
func (c *Connection) AddSubscription(sub Subscription) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscriptions[sub.ID] = sub
}

// RemoveSubscription removes a subscription by ID. It is a no-op if
// the subscription does not exist.
func (c *Connection) RemoveSubscription(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subscriptions, id)
}

// HasSubscription reports whether a subscription with the given ID exists.
func (c *Connection) HasSubscription(id string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.subscriptions[id]
	return ok
}

// SubscriptionCount returns the number of active subscriptions.
func (c *Connection) SubscriptionCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.subscriptions)
}

// MatchesEvent returns the IDs of subscriptions that match the given event.
// Returns nil if no subscriptions match.
func (c *Connection) MatchesEvent(event DomainEvent) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var matched []string
	for _, sub := range c.subscriptions {
		if sub.Matches(event) {
			matched = append(matched, sub.ID)
		}
	}
	return matched
}

// CheckJWTExpiry reports whether the JWT token has expired.
// Returns false if no claims are associated with the connection.
func (c *Connection) CheckJWTExpiry() bool {
	if c.claims == nil {
		return false
	}
	return c.claims.IsExpired()
}

// LastActivity returns the time of the last activity on this connection.
func (c *Connection) LastActivity() time.Time {
	c.activityMu.RLock()
	defer c.activityMu.RUnlock()
	return c.lastActivity
}

func (c *Connection) touchActivity() {
	c.activityMu.Lock()
	defer c.activityMu.Unlock()
	c.lastActivity = time.Now()
}

// Start begins the connection lifecycle: read pump, write pump, and ping loop.
// It blocks until the connection is closed or the provided context is cancelled.
func (c *Connection) Start(ctx context.Context) {
	// Cancel the connection when the external context is done.
	go func() {
		select {
		case <-ctx.Done():
			c.Close(websocket.StatusGoingAway, "context cancelled")
		case <-c.ctx.Done():
		}
	}()

	if c.wsConn == nil {
		// No WebSocket connection; wait for cancellation only.
		<-c.ctx.Done()
		return
	}

	c.wsConn.SetReadLimit(maxReadSize)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() { //nolint:contextcheck // writePump uses c.ctx lifecycle context
		defer wg.Done()
		c.writePump()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.readPump()
	}()

	wg.Add(1)
	go func() { //nolint:contextcheck // pingLoop uses c.ctx lifecycle context
		defer wg.Done()
		c.pingLoop()
	}()

	wg.Wait()
}

// Close initiates a graceful shutdown of the connection. It is safe to call
// multiple times; only the first call takes effect.
func (c *Connection) Close(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		c.logger.Debug("closing connection",
			slog.String("conn_id", c.id),
			slog.String("tenant_id", c.tenantID),
			slog.String("reason", reason),
		)
		if c.wsConn != nil {
			_ = c.wsConn.Close(code, reason)
		}
		c.cancel()
	})
}

// writePump drains the send channel and writes messages to the WebSocket.
func (c *Connection) writePump() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case msg := <-c.sendChan:
			if err := c.writeMessage(msg); err != nil {
				c.logger.Debug("write error, closing connection",
					slog.String("conn_id", c.id),
					slog.String("error", err.Error()),
				)
				c.Close(websocket.StatusInternalError, "write error")
				return
			}
			// After each successful write, check if any messages were dropped.
			if dropped := c.droppedCount.Swap(0); dropped > 0 {
				overflowMsg := ServerMessage{
					Type:          ServerMessageTypeSystem,
					ErrorCode:     ErrorCodeBufferOverflow,
					SystemMessage: fmt.Sprintf("Dropped %d events. UI state may be stale.", dropped),
				}
				if err := c.writeMessage(overflowMsg); err != nil {
					c.Close(websocket.StatusInternalError, "write error")
					return
				}
			}
		}
	}
}

func (c *Connection) writeMessage(msg ServerMessage) error {
	data, err := msg.Serialize()
	if err != nil {
		return fmt.Errorf("serialize: %w", err)
	}
	writeCtx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	return c.wsConn.Write(writeCtx, websocket.MessageText, data)
}

// readPump reads messages from the WebSocket and dispatches them to the handler.
func (c *Connection) readPump() {
	for {
		_, data, err := c.wsConn.Read(c.ctx)
		if err != nil {
			// Connection closed or context cancelled
			c.Close(websocket.StatusGoingAway, "read error")
			return
		}

		c.touchActivity()

		msg, err := ParseClientMessage(data)
		if err != nil {
			c.logger.Debug("malformed client message",
				slog.String("conn_id", c.id),
				slog.String("error", err.Error()),
			)
			continue
		}

		if c.msgHandler != nil {
			c.msgHandler(c, msg)
		}
	}
}

// pingLoop periodically pings the client, checks JWT expiry, and enforces
// idle timeout.
func (c *Connection) pingLoop() {
	pingTicker := time.NewTicker(PingInterval)
	defer pingTicker.Stop()

	jwtTicker := time.NewTicker(JWTCheckInterval)
	defer jwtTicker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-pingTicker.C:
			// Check idle timeout before pinging.
			if time.Since(c.LastActivity()) > IdleTimeout {
				c.logger.Info("idle timeout, closing connection",
					slog.String("conn_id", c.id),
					slog.String("tenant_id", c.tenantID),
				)
				c.Close(websocket.StatusGoingAway, "idle timeout")
				return
			}

			pingCtx, cancel := context.WithTimeout(c.ctx, PongTimeout)
			err := c.wsConn.Ping(pingCtx)
			cancel()
			if err != nil {
				c.logger.Debug("ping failed, closing connection",
					slog.String("conn_id", c.id),
					slog.String("error", err.Error()),
				)
				c.Close(websocket.StatusGoingAway, "ping timeout")
				return
			}
			c.touchActivity()
		case <-jwtTicker.C:
			if c.CheckJWTExpiry() {
				c.logger.Info("JWT expired, closing connection",
					slog.String("conn_id", c.id),
					slog.String("tenant_id", c.tenantID),
				)
				c.Close(CloseCodeTokenExpired, "JWT expired")
				return
			}
		}
	}
}
