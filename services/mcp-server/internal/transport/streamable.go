package transport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Dispatcher handles a single JSON-RPC message and returns the response.
// Notifications return nil (no response required).
type Dispatcher interface {
	Dispatch(ctx context.Context, msg *JSONRPCMessage) *JSONRPCMessage
}

// StreamableHTTPHandler implements the MCP streamable HTTP transport
// (spec 2025-03-26). Clients POST JSON-RPC messages to a single endpoint
// and receive synchronous JSON responses.
type StreamableHTTPHandler struct {
	dispatcher Dispatcher
	sessions   map[string]*streamSession
	mu         sync.RWMutex
	logger     *slog.Logger
}

type streamSession struct {
	id       string
	created  time.Time
	lastUsed time.Time
}

// NewStreamableHTTPHandler creates a handler for the MCP streamable HTTP transport.
func NewStreamableHTTPHandler(dispatcher Dispatcher, logger *slog.Logger) *StreamableHTTPHandler {
	return &StreamableHTTPHandler{
		dispatcher: dispatcher,
		sessions:   make(map[string]*streamSession),
		logger:     logger,
	}
}

// ServeHTTP routes requests by HTTP method.
func (h *StreamableHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handlePost(w, r)
	case http.MethodDelete:
		h.handleDelete(w, r)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *StreamableHTTPHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if ct != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	var msg JSONRPCMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		writeJSONError(w, nil, CodeParseError, "invalid JSON")
		return
	}

	// For initialize requests, create a new session.
	if msg.Method == "initialize" {
		h.handleInitialize(w, r, &msg)
		return
	}

	// All other messages require a valid session.
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "Mcp-Session-Id header required", http.StatusBadRequest)
		return
	}

	h.mu.RLock()
	sess, exists := h.sessions[sessionID]
	h.mu.RUnlock()

	if !exists {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}

	h.mu.Lock()
	sess.lastUsed = time.Now()
	h.mu.Unlock()

	resp := h.dispatcher.Dispatch(r.Context(), &msg)
	if resp == nil {
		// Notification — no response body.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	writeJSON(w, resp)
}

func (h *StreamableHTTPHandler) handleInitialize(w http.ResponseWriter, r *http.Request, msg *JSONRPCMessage) {
	sessionID := generateSessionID()

	h.mu.Lock()
	now := time.Now()
	h.sessions[sessionID] = &streamSession{
		id:       sessionID,
		created:  now,
		lastUsed: now,
	}
	h.mu.Unlock()

	h.logger.Info("streamable HTTP session created", "session_id", sessionID)

	resp := h.dispatcher.Dispatch(r.Context(), msg)

	w.Header().Set("Mcp-Session-Id", sessionID)
	writeJSON(w, resp)
}

func (h *StreamableHTTPHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "Mcp-Session-Id header required", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	_, exists := h.sessions[sessionID]
	if exists {
		delete(h.sessions, sessionID)
	}
	h.mu.Unlock()

	if !exists {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}

	h.logger.Info("streamable HTTP session terminated", "session_id", sessionID)
	w.WriteHeader(http.StatusAccepted)
}

// SessionCount returns the number of active sessions.
func (h *StreamableHTTPHandler) SessionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

func writeJSON(w http.ResponseWriter, msg *JSONRPCMessage) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(msg); err != nil {
		// Headers already sent; log but can't send error response.
		_ = err
	}
}

func writeJSONError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	resp := NewErrorResponse(id, code, message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(resp)
}

func generateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
