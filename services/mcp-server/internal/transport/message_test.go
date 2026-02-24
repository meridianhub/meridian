package transport

import (
	"encoding/json"
	"testing"
)

func TestJSONRPCMessage_IsRequest(t *testing.T) {
	msg := &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
	}
	if !msg.IsRequest() {
		t.Error("expected IsRequest to return true for message with method and id")
	}
	if msg.IsNotification() {
		t.Error("expected IsNotification to return false for request")
	}
	if msg.IsResponse() {
		t.Error("expected IsResponse to return false for request")
	}
}

func TestJSONRPCMessage_IsNotification(t *testing.T) {
	msg := &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		Method:  "notifications/initialized",
	}
	if !msg.IsNotification() {
		t.Error("expected IsNotification to return true for message with method and no id")
	}
	if msg.IsRequest() {
		t.Error("expected IsRequest to return false for notification")
	}
}

func TestJSONRPCMessage_IsResponse(t *testing.T) {
	msg := &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Result:  json.RawMessage(`{}`),
	}
	if !msg.IsResponse() {
		t.Error("expected IsResponse to return true for message with result")
	}
	if msg.IsRequest() {
		t.Error("expected IsRequest to return false for response")
	}
}

func TestNewResponse(t *testing.T) {
	id := json.RawMessage(`42`)
	result := map[string]string{"status": "ok"}

	msg, err := NewResponse(id, result)
	if err != nil {
		t.Fatalf("NewResponse error: %v", err)
	}

	if msg.JSONRPC != JSONRPCVersion {
		t.Errorf("expected jsonrpc=%q, got %q", JSONRPCVersion, msg.JSONRPC)
	}
	if string(msg.ID) != `42` {
		t.Errorf("expected id=42, got %s", msg.ID)
	}

	var parsed map[string]string
	if err := json.Unmarshal(msg.Result, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if parsed["status"] != "ok" {
		t.Errorf("expected status=ok, got %s", parsed["status"])
	}
}

func TestNewErrorResponse(t *testing.T) {
	id := json.RawMessage(`1`)
	msg := NewErrorResponse(id, CodeMethodNotFound, "method not found")

	if msg.JSONRPC != JSONRPCVersion {
		t.Errorf("expected jsonrpc=%q, got %q", JSONRPCVersion, msg.JSONRPC)
	}
	if msg.Error == nil {
		t.Fatal("expected error to be set")
	}
	if msg.Error.Code != CodeMethodNotFound {
		t.Errorf("expected code=%d, got %d", CodeMethodNotFound, msg.Error.Code)
	}
	if msg.Error.Message != "method not found" {
		t.Errorf("expected message=%q, got %q", "method not found", msg.Error.Message)
	}
}

func TestJSONRPCMessage_RoundTrip(t *testing.T) {
	original := &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  "tools/list",
		Params:  json.RawMessage(`{}`),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded JSONRPCMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.JSONRPC != original.JSONRPC {
		t.Errorf("jsonrpc mismatch: %q vs %q", decoded.JSONRPC, original.JSONRPC)
	}
	if decoded.Method != original.Method {
		t.Errorf("method mismatch: %q vs %q", decoded.Method, original.Method)
	}
	if string(decoded.ID) != string(original.ID) {
		t.Errorf("id mismatch: %s vs %s", decoded.ID, original.ID)
	}
}
