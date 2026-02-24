package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

// SSETransport implements the Transport interface using HTTP Server-Sent Events.
// Clients connect via GET to receive SSE events, and POST JSON-RPC messages
// to a separate endpoint.
type SSETransport struct {
	logger  *slog.Logger
	inbox   chan *JSONRPCMessage
	clients map[string]*sseClient
	mu      sync.RWMutex
	closed  atomic.Bool
}

type sseClient struct {
	id     string
	events chan []byte
	done   chan struct{}
}

// NewSSETransport creates a new SSE transport.
func NewSSETransport(logger *slog.Logger) *SSETransport {
	return &SSETransport{
		logger:  logger,
		inbox:   make(chan *JSONRPCMessage, 64),
		clients: make(map[string]*sseClient),
	}
}

// ReadMessage reads the next JSON-RPC message received via HTTP POST.
func (t *SSETransport) ReadMessage(ctx context.Context) (*JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-t.inbox:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	}
}

// WriteMessage broadcasts a JSON-RPC message to all connected SSE clients.
func (t *SSETransport) WriteMessage(_ context.Context, msg *JSONRPCMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, client := range t.clients {
		select {
		case client.events <- data:
		default:
			t.logger.Warn("dropping message for slow SSE client", "client_id", client.id)
		}
	}

	return nil
}

// Close shuts down the SSE transport, disconnecting all clients.
func (t *SSETransport) Close() error {
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}

	// Close inbox under the write lock to prevent the race with HandleMessage.
	t.mu.Lock()
	close(t.inbox)

	for id, client := range t.clients {
		close(client.done)
		close(client.events)
		delete(t.clients, id)
	}
	t.mu.Unlock()

	return nil
}

// HandleSSE is the HTTP handler for SSE client connections (GET /sse).
func (t *SSETransport) HandleSSE(w http.ResponseWriter, r *http.Request) {
	if t.closed.Load() {
		http.Error(w, "transport closed", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	client := &sseClient{
		id:     uuid.New().String(),
		events: make(chan []byte, 64),
		done:   make(chan struct{}),
	}

	t.mu.Lock()
	t.clients[client.id] = client
	t.mu.Unlock()

	t.logger.Info("SSE client connected", "client_id", client.id)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send the endpoint event so the client knows where to POST messages
	_, _ = fmt.Fprintf(w, "event: endpoint\ndata: /message?sessionId=%s\n\n", client.id)
	flusher.Flush()

	defer func() {
		t.mu.Lock()
		delete(t.clients, client.id)
		t.mu.Unlock()
		t.logger.Info("SSE client disconnected", "client_id", client.id)
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-client.done:
			return
		case data, ok := <-client.events:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// HandleMessage is the HTTP handler for incoming JSON-RPC messages (POST /message).
func (t *SSETransport) HandleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, "missing sessionId parameter", http.StatusBadRequest)
		return
	}

	var msg JSONRPCMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Hold the read lock across both the session check and inbox send.
	// Close() holds the write lock when closing the inbox, so this
	// prevents the race between Close and send-on-closed-channel.
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.closed.Load() {
		http.Error(w, "transport closed", http.StatusServiceUnavailable)
		return
	}

	if _, exists := t.clients[sessionID]; !exists {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}

	select {
	case t.inbox <- &msg:
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "server busy", http.StatusServiceUnavailable)
	}
}

// ClientCount returns the number of connected SSE clients.
func (t *SSETransport) ClientCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.clients)
}
