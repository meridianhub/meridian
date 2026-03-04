package eventstream

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ConnectionSender is the interface the Router uses to interact with a client
// connection. It is satisfied by *Connection and by test doubles.
type ConnectionSender interface {
	// ID returns the connection identifier.
	ID() string
	// TenantID returns the tenant that owns this connection.
	TenantID() string
	// Send enqueues a ServerMessage for delivery. Returns false if the buffer is full.
	Send(msg ServerMessage) bool
	// MatchesEvent returns the subscription IDs that match the given event.
	// Returns nil if no subscriptions match.
	MatchesEvent(event DomainEvent) []string
}

// ConnectionRegistry stores active connections indexed by tenant and connection ID.
// It is safe for concurrent use from multiple goroutines.
type ConnectionRegistry struct {
	mu       sync.RWMutex
	byTenant map[string]map[string]ConnectionSender
	byID     map[string]ConnectionSender
}

// NewConnectionRegistry creates an empty ConnectionRegistry.
func NewConnectionRegistry() *ConnectionRegistry {
	return &ConnectionRegistry{
		byTenant: make(map[string]map[string]ConnectionSender),
		byID:     make(map[string]ConnectionSender),
	}
}

// Register adds conn to the registry, indexed by its tenant and ID.
// If a connection with the same ID is already registered it is replaced.
func (r *ConnectionRegistry) Register(conn ConnectionSender) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove previous registration for this ID if it exists.
	if existing, ok := r.byID[conn.ID()]; ok {
		tenantMap := r.byTenant[existing.TenantID()]
		delete(tenantMap, existing.ID())
		if len(tenantMap) == 0 {
			delete(r.byTenant, existing.TenantID())
		}
	}

	r.byID[conn.ID()] = conn

	tenantMap, ok := r.byTenant[conn.TenantID()]
	if !ok {
		tenantMap = make(map[string]ConnectionSender)
		r.byTenant[conn.TenantID()] = tenantMap
	}
	tenantMap[conn.ID()] = conn
}

// Unregister removes the connection with connID from the registry.
// It is a no-op if connID is not registered.
func (r *ConnectionRegistry) Unregister(connID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	conn, ok := r.byID[connID]
	if !ok {
		return
	}

	delete(r.byID, connID)

	tenantMap := r.byTenant[conn.TenantID()]
	delete(tenantMap, connID)
	if len(tenantMap) == 0 {
		delete(r.byTenant, conn.TenantID())
	}
}

// UnregisterByID atomically removes and returns the connection with connID.
// Returns nil if connID is not registered. Using a single lock acquisition
// avoids the TOCTOU race between lookup and removal.
func (r *ConnectionRegistry) UnregisterByID(connID string) ConnectionSender {
	r.mu.Lock()
	defer r.mu.Unlock()

	conn, ok := r.byID[connID]
	if !ok {
		return nil
	}

	delete(r.byID, connID)

	tenantMap := r.byTenant[conn.TenantID()]
	delete(tenantMap, connID)
	if len(tenantMap) == 0 {
		delete(r.byTenant, conn.TenantID())
	}
	return conn
}

// GetByTenant returns a snapshot of all connections for tenantID.
// Returns nil if no connections exist for the tenant.
func (r *ConnectionRegistry) GetByTenant(tenantID string) []ConnectionSender {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tenantMap, ok := r.byTenant[tenantID]
	if !ok {
		return nil
	}

	result := make([]ConnectionSender, 0, len(tenantMap))
	for _, conn := range tenantMap {
		result = append(result, conn)
	}
	return result
}

// InProcessFanOut is a FanOut backed by direct in-process handler dispatch.
// It is used for single-instance deployments and for testing. Each tenant maps
// to exactly one handler. Concurrent Publish calls are safe.
type InProcessFanOut struct {
	mu       sync.RWMutex
	handlers map[string]EventHandler
}

// NewInProcessFanOut returns an empty InProcessFanOut.
func NewInProcessFanOut() *InProcessFanOut {
	return &InProcessFanOut{
		handlers: make(map[string]EventHandler),
	}
}

// Compile-time assertion.
var _ FanOut = (*InProcessFanOut)(nil)

// Publish delivers event to the handler registered for event.TenantID.
// Returns ErrEmptyTenantID if event.TenantID is empty.
// If no handler is registered the event is silently dropped.
func (f *InProcessFanOut) Publish(ctx context.Context, event DomainEvent) error {
	if event.TenantID == "" {
		return ErrEmptyTenantID
	}

	f.mu.RLock()
	h, ok := f.handlers[event.TenantID]
	f.mu.RUnlock()

	if ok {
		if err := h(ctx, event); err != nil {
			slog.Warn("fan-out handler returned error",
				slog.String("tenant_id", event.TenantID),
				slog.String("event_id", event.EventID),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// Subscribe registers handler for tenantID. Replaces any existing handler.
// Returns ErrEmptyTenantID if tenantID is empty.
func (f *InProcessFanOut) Subscribe(_ context.Context, tenantID string, handler EventHandler) error {
	if tenantID == "" {
		return ErrEmptyTenantID
	}

	f.mu.Lock()
	f.handlers[tenantID] = handler
	f.mu.Unlock()
	return nil
}

// Unsubscribe removes the handler for tenantID.
// Returns ErrEmptyTenantID if tenantID is empty.
func (f *InProcessFanOut) Unsubscribe(_ context.Context, tenantID string) error {
	if tenantID == "" {
		return ErrEmptyTenantID
	}

	f.mu.Lock()
	delete(f.handlers, tenantID)
	f.mu.Unlock()
	return nil
}

// Router orchestrates event delivery from an EventSource to registered connections
// via a FanOut. It maintains a ConnectionRegistry partitioned by tenant.
//
// Flow:
//  1. Start consumes events from EventSource and publishes them to FanOut.
//  2. When a connection is Registered, the Router subscribes to the FanOut for
//     that tenant (if not already subscribed).
//  3. The FanOut calls the per-tenant handler, which fans the event out to all
//     matching connections for that tenant.
//
// Router is safe for concurrent use from multiple goroutines.
type Router struct {
	source   EventSource
	fanOut   FanOut
	registry *ConnectionRegistry

	mu              sync.Mutex
	tenantSubCounts map[string]int // reference counts for FanOut subscriptions

	ctx    context.Context
	cancel context.CancelFunc

	logger        *slog.Logger
	metrics       *Metrics
	maxChainDepth int // 0 means no limit
}

// RouterOption is a functional option for configuring a Router.
type RouterOption func(*Router)

// WithRouterMetrics attaches a Metrics instance to the Router.
// When set, the Router records connection and event delivery metrics.
func WithRouterMetrics(m *Metrics) RouterOption {
	return func(r *Router) {
		r.metrics = m
	}
}

// WithMaxChainDepth sets the maximum allowed saga event chain depth. Events with a
// ChainDepth greater than or equal to maxDepth are dropped with a warning log entry
// rather than published to the FanOut. A value of 0 disables the limit.
func WithMaxChainDepth(maxDepth int) RouterOption {
	return func(r *Router) {
		r.maxChainDepth = maxDepth
	}
}

// NewRouter creates a Router backed by the given EventSource and FanOut.
func NewRouter(source EventSource, fanOut FanOut, opts ...RouterOption) *Router {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Router{
		source:          source,
		fanOut:          fanOut,
		registry:        NewConnectionRegistry(),
		tenantSubCounts: make(map[string]int),
		ctx:             ctx,
		cancel:          cancel,
		logger:          slog.Default(),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RegisterConnection adds conn to the registry and ensures a FanOut subscription
// exists for the connection's tenant.
func (r *Router) RegisterConnection(conn ConnectionSender) {
	r.registry.Register(conn)

	tenantID := conn.TenantID()
	r.mu.Lock()
	count := r.tenantSubCounts[tenantID]
	r.tenantSubCounts[tenantID] = count + 1
	isFirst := count == 0
	r.mu.Unlock()

	if isFirst {
		if err := r.fanOut.Subscribe(r.ctx, tenantID, r.makeTenantHandler(tenantID)); err != nil {
			r.logger.Error("failed to subscribe to fanout for tenant",
				slog.String("tenant_id", tenantID),
				slog.String("error", err.Error()),
			)
		}
	}
}

// UnregisterConnection removes the connection with connID from the registry and
// unsubscribes from the FanOut when the last connection for a tenant departs.
func (r *Router) UnregisterConnection(connID string) {
	// Atomically remove the connection and get its tenant in one lock acquisition.
	existing := r.registry.UnregisterByID(connID)
	if existing == nil {
		return
	}
	tenantID := existing.TenantID()

	r.mu.Lock()
	count := r.tenantSubCounts[tenantID]
	if count > 0 {
		count--
		r.tenantSubCounts[tenantID] = count
	}
	isLast := count == 0
	if isLast {
		delete(r.tenantSubCounts, tenantID)
	}
	r.mu.Unlock()

	if isLast {
		if err := r.fanOut.Unsubscribe(r.ctx, tenantID); err != nil {
			r.logger.Error("failed to unsubscribe from fanout for tenant",
				slog.String("tenant_id", tenantID),
				slog.String("error", err.Error()),
			)
		}
	}
}

// GetConnectionsByTenant returns a snapshot of all connections for tenantID.
func (r *Router) GetConnectionsByTenant(tenantID string) []ConnectionSender {
	return r.registry.GetByTenant(tenantID)
}

// HandleEvent delivers event to all matching connections for the event's tenant.
func (r *Router) HandleEvent(_ context.Context, event DomainEvent) error {
	conns := r.registry.GetByTenant(event.TenantID)
	for _, conn := range conns {
		subIDs := conn.MatchesEvent(event)
		if len(subIDs) == 0 {
			continue
		}
		payload := NewEventPayload(event)
		for _, subID := range subIDs {
			msg := ServerMessage{
				Type:           ServerMessageTypeEvent,
				SubscriptionID: subID,
				Channel:        event.Channel,
				Event:          &payload,
			}
			r.deliverMessage(conn, msg, event)
		}
	}
	return nil
}

// deliverMessage sends msg to conn and records delivery metrics.
func (r *Router) deliverMessage(conn ConnectionSender, msg ServerMessage, event DomainEvent) {
	if !conn.Send(msg) {
		r.logger.Warn("dropped event: connection buffer full",
			slog.String("conn_id", conn.ID()),
			slog.String("tenant_id", event.TenantID),
			slog.String("event_id", event.EventID),
		)
		if r.metrics != nil {
			r.metrics.IncEventDropped("buffer_full")
		}
		return
	}
	if r.metrics == nil {
		return
	}
	r.metrics.IncEventDelivered(event.TenantID, event.Channel)
	if !event.Timestamp.IsZero() {
		if latency := time.Since(event.Timestamp); latency > 0 {
			r.metrics.ObserveLatency(latency)
		}
	}
}

// Start begins consuming events from the EventSource and publishing them to the
// FanOut. It blocks until ctx is cancelled or a fatal error from the EventSource
// occurs. The EventSource handler publishes each event to the FanOut.
func (r *Router) Start(ctx context.Context) error {
	// Merge external cancellation with internal lifecycle.
	go func() {
		select {
		case <-ctx.Done():
			r.cancel()
		case <-r.ctx.Done():
		}
	}()

	return r.source.Start(r.ctx, func(handlerCtx context.Context, event DomainEvent) error { //nolint:contextcheck // r.ctx merges the external ctx via the goroutine above
		if r.maxChainDepth > 0 && event.ChainDepth >= r.maxChainDepth {
			r.logger.Warn("event chain depth limit exceeded, dropping event",
				slog.Int("chain_depth", event.ChainDepth),
				slog.Int("max_allowed", r.maxChainDepth),
				slog.String("event_type", event.EventType),
				slog.String("correlation_id", event.CorrelationID),
				slog.String("event_id", event.EventID),
			)
			if r.metrics != nil {
				r.metrics.IncEventDropped("chain_depth_exceeded")
			}
			return nil
		}
		return r.fanOut.Publish(handlerCtx, event)
	})
}

// Shutdown cancels internal operations, causing Start to return once the
// EventSource acknowledges cancellation.
func (r *Router) Shutdown(_ context.Context) error {
	r.cancel()
	return nil
}

// makeTenantHandler returns an EventHandler that fans an event out to all
// matching connections for the tenant.
func (r *Router) makeTenantHandler(_ string) EventHandler {
	return func(ctx context.Context, event DomainEvent) error {
		return r.HandleEvent(ctx, event)
	}
}
