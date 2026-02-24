package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestSSETransport_HandleMessage_PostsToInbox(t *testing.T) {
	tr := NewSSETransport(testLogger())
	defer tr.Close()

	// Simulate a connected client
	tr.mu.Lock()
	tr.clients["test-session"] = &sseClient{
		id:     "test-session",
		events: make(chan []byte, 64),
		done:   make(chan struct{}),
	}
	tr.mu.Unlock()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	req := httptest.NewRequest(http.MethodPost, "/message?sessionId=test-session", strings.NewReader(body))
	rec := httptest.NewRecorder()

	tr.HandleMessage(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	msg, err := tr.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}
	if msg.Method != "initialize" {
		t.Errorf("expected method=initialize, got %s", msg.Method)
	}
}

func TestSSETransport_HandleMessage_RejectsUnknownSession(t *testing.T) {
	tr := NewSSETransport(testLogger())
	defer tr.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	req := httptest.NewRequest(http.MethodPost, "/message?sessionId=nonexistent", strings.NewReader(body))
	rec := httptest.NewRecorder()

	tr.HandleMessage(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestSSETransport_HandleMessage_RejectsMissingSessionId(t *testing.T) {
	tr := NewSSETransport(testLogger())
	defer tr.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	req := httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(body))
	rec := httptest.NewRecorder()

	tr.HandleMessage(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestSSETransport_HandleMessage_RejectsGetMethod(t *testing.T) {
	tr := NewSSETransport(testLogger())
	defer tr.Close()

	req := httptest.NewRequest(http.MethodGet, "/message?sessionId=test", nil)
	rec := httptest.NewRecorder()

	tr.HandleMessage(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestSSETransport_HandleMessage_RejectsInvalidJSON(t *testing.T) {
	tr := NewSSETransport(testLogger())
	defer tr.Close()

	tr.mu.Lock()
	tr.clients["test-session"] = &sseClient{
		id:     "test-session",
		events: make(chan []byte, 64),
		done:   make(chan struct{}),
	}
	tr.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/message?sessionId=test-session", strings.NewReader("not json"))
	rec := httptest.NewRecorder()

	tr.HandleMessage(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestSSETransport_WriteMessage_BroadcastsToClients(t *testing.T) {
	tr := NewSSETransport(testLogger())
	defer tr.Close()

	events := make(chan []byte, 64)
	tr.mu.Lock()
	tr.clients["client-1"] = &sseClient{
		id:     "client-1",
		events: events,
		done:   make(chan struct{}),
	}
	tr.mu.Unlock()

	msg := &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Result:  json.RawMessage(`{"status":"ok"}`),
	}

	if err := tr.WriteMessage(context.Background(), msg); err != nil {
		t.Fatalf("WriteMessage error: %v", err)
	}

	select {
	case data := <-events:
		var decoded JSONRPCMessage
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("failed to decode broadcast: %v", err)
		}
		if decoded.JSONRPC != "2.0" {
			t.Errorf("expected jsonrpc=2.0, got %s", decoded.JSONRPC)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestSSETransport_ClientCount(t *testing.T) {
	tr := NewSSETransport(testLogger())
	defer tr.Close()

	if tr.ClientCount() != 0 {
		t.Errorf("expected 0 clients, got %d", tr.ClientCount())
	}

	tr.mu.Lock()
	tr.clients["a"] = &sseClient{id: "a", events: make(chan []byte, 1), done: make(chan struct{})}
	tr.clients["b"] = &sseClient{id: "b", events: make(chan []byte, 1), done: make(chan struct{})}
	tr.mu.Unlock()

	if tr.ClientCount() != 2 {
		t.Errorf("expected 2 clients, got %d", tr.ClientCount())
	}
}

func TestSSETransport_ReadMessage_ContextCancelled(t *testing.T) {
	tr := NewSSETransport(testLogger())
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := tr.ReadMessage(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestSSETransport_HandleSSE_StreamsEvents(t *testing.T) {
	tr := NewSSETransport(testLogger())
	defer tr.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", tr.HandleSSE)
	mux.HandleFunc("/message", tr.HandleMessage)

	server := httptest.NewServer(mux)
	defer server.Close()

	// Connect SSE client with context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/sse", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect error: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected Content-Type=text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}

	// Read the endpoint event
	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("failed to read SSE endpoint event: %v", err)
	}
	endpointEvent := string(buf[:n])
	if !strings.Contains(endpointEvent, "event: endpoint") {
		t.Errorf("expected endpoint event, got: %s", endpointEvent)
	}
	if !strings.Contains(endpointEvent, "/message?sessionId=") {
		t.Errorf("expected sessionId in endpoint data, got: %s", endpointEvent)
	}

	// Extract session ID from the endpoint event
	sessionID := ""
	for _, line := range strings.Split(endpointEvent, "\n") {
		if strings.HasPrefix(line, "data: /message?sessionId=") {
			sessionID = strings.TrimPrefix(line, "data: /message?sessionId=")
			break
		}
	}
	if sessionID == "" {
		t.Fatal("failed to extract sessionId from endpoint event")
	}

	// Post a message to the session
	msgBody := `{"jsonrpc":"2.0","id":1,"method":"test"}`
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/message?sessionId="+sessionID, bytes.NewReader([]byte(msgBody)))
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	postReq.Header.Set("Content-Type", "application/json")

	postResp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatalf("POST /message error: %v", err)
	}
	postResp.Body.Close()

	if postResp.StatusCode != http.StatusAccepted {
		t.Errorf("expected POST status %d, got %d", http.StatusAccepted, postResp.StatusCode)
	}

	// Verify the message arrived in the inbox
	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()

	msg, err := tr.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}
	if msg.Method != "test" {
		t.Errorf("expected method=test, got %s", msg.Method)
	}
}

func TestSSETransport_Close_DisconnectsClients(t *testing.T) {
	tr := NewSSETransport(testLogger())

	done := make(chan struct{})
	tr.mu.Lock()
	tr.clients["c1"] = &sseClient{id: "c1", events: make(chan []byte, 1), done: done}
	tr.mu.Unlock()

	if err := tr.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	// Verify done channel was closed
	select {
	case <-done:
		// expected
	default:
		t.Error("expected client done channel to be closed")
	}

	if tr.ClientCount() != 0 {
		t.Errorf("expected 0 clients after close, got %d", tr.ClientCount())
	}
}
