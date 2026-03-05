package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// mockDispatcher records calls and returns a canned response.
type mockDispatcher struct {
	lastMsg  *JSONRPCMessage
	response *JSONRPCMessage
}

func (d *mockDispatcher) Dispatch(_ context.Context, msg *JSONRPCMessage) *JSONRPCMessage {
	d.lastMsg = msg
	return d.response
}

func newInitializeResponse() *JSONRPCMessage {
	resp, _ := NewResponse(json.RawMessage(`1`), map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"serverInfo":      map[string]any{"name": "test", "version": "1.0"},
	})
	return resp
}

func TestStreamableHTTP_Initialize(t *testing.T) {
	d := &mockDispatcher{response: newInitializeResponse()}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	sessionID := rec.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("expected Mcp-Session-Id header in response")
	}

	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type=application/json, got %s", rec.Header().Get("Content-Type"))
	}

	var resp JSONRPCMessage
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Result == nil {
		t.Error("expected result in response")
	}
	if h.SessionCount() != 1 {
		t.Errorf("expected 1 session, got %d", h.SessionCount())
	}
}

func TestStreamableHTTP_Initialize_FailedDispatch_NoSession(t *testing.T) {
	errResp := NewErrorResponse(json.RawMessage(`1`), CodeInternalError, "something broke")
	d := &mockDispatcher{response: errResp}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Header().Get("Mcp-Session-Id") != "" {
		t.Error("expected no Mcp-Session-Id header when initialize fails")
	}
	if h.SessionCount() != 0 {
		t.Errorf("expected 0 sessions after failed initialize, got %d", h.SessionCount())
	}
}

func TestStreamableHTTP_ToolsList_WithSession(t *testing.T) {
	toolsResp, _ := NewResponse(json.RawMessage(`2`), map[string]any{"tools": []any{}})
	d := &mockDispatcher{response: toolsResp}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	// First initialize to get a session.
	d.response = newInitializeResponse()
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	initReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initBody))
	initReq.Header.Set("Content-Type", "application/json")
	initRec := httptest.NewRecorder()
	h.ServeHTTP(initRec, initReq)

	sessionID := initRec.Header().Get("Mcp-Session-Id")

	// Now send tools/list with the session.
	d.response = toolsResp
	listBody := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	listReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(listBody))
	listReq.Header.Set("Content-Type", "application/json")
	listReq.Header.Set("Mcp-Session-Id", sessionID)
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, listRec.Code)
	}
	if d.lastMsg.Method != "tools/list" {
		t.Errorf("expected dispatched method=tools/list, got %s", d.lastMsg.Method)
	}
}

func TestStreamableHTTP_MissingSessionHeader(t *testing.T) {
	d := &mockDispatcher{}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestStreamableHTTP_UnknownSession(t *testing.T) {
	d := &mockDispatcher{}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", "nonexistent-session")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestStreamableHTTP_Notification(t *testing.T) {
	d := &mockDispatcher{response: nil} // Notifications return nil.
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	// Create a session first.
	d.response = newInitializeResponse()
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	initReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initBody))
	initReq.Header.Set("Content-Type", "application/json")
	initRec := httptest.NewRecorder()
	h.ServeHTTP(initRec, initReq)
	sessionID := initRec.Header().Get("Mcp-Session-Id")

	// Send a notification (no id field).
	d.response = nil
	notifBody := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	notifReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(notifBody))
	notifReq.Header.Set("Content-Type", "application/json")
	notifReq.Header.Set("Mcp-Session-Id", sessionID)
	notifRec := httptest.NewRecorder()
	h.ServeHTTP(notifRec, notifReq)

	if notifRec.Code != http.StatusAccepted {
		t.Errorf("expected status %d, got %d", http.StatusAccepted, notifRec.Code)
	}
	if notifRec.Body.Len() != 0 {
		t.Errorf("expected empty body for notification, got %q", notifRec.Body.String())
	}
}

func TestStreamableHTTP_InvalidJSON(t *testing.T) {
	d := &mockDispatcher{}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestStreamableHTTP_WrongContentType(t *testing.T) {
	d := &mockDispatcher{}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected status %d, got %d", http.StatusUnsupportedMediaType, rec.Code)
	}
}

func TestStreamableHTTP_ContentTypeWithCharset(t *testing.T) {
	d := &mockDispatcher{response: newInitializeResponse()}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d for Content-Type with charset, got %d", http.StatusOK, rec.Code)
	}
}

func TestStreamableHTTP_DeleteSession(t *testing.T) {
	d := &mockDispatcher{response: newInitializeResponse()}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	// Create a session.
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	initReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initBody))
	initReq.Header.Set("Content-Type", "application/json")
	initRec := httptest.NewRecorder()
	h.ServeHTTP(initRec, initReq)
	sessionID := initRec.Header().Get("Mcp-Session-Id")

	if h.SessionCount() != 1 {
		t.Fatalf("expected 1 session, got %d", h.SessionCount())
	}

	// Delete the session.
	delReq := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	delReq.Header.Set("Mcp-Session-Id", sessionID)
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, delReq)

	if delRec.Code != http.StatusAccepted {
		t.Errorf("expected status %d, got %d", http.StatusAccepted, delRec.Code)
	}
	if h.SessionCount() != 0 {
		t.Errorf("expected 0 sessions after delete, got %d", h.SessionCount())
	}
}

func TestStreamableHTTP_DeleteUnknownSession(t *testing.T) {
	d := &mockDispatcher{}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set("Mcp-Session-Id", "nonexistent")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestStreamableHTTP_GetMethodNotAllowed(t *testing.T) {
	d := &mockDispatcher{}
	h := NewStreamableHTTPHandler(d, testLogger())
	defer h.Close()

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}
