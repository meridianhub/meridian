package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStdioTransport_ReadMessage(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	reader := strings.NewReader(input)
	writer := &bytes.Buffer{}

	tr := NewStdioTransport(reader, writer)
	defer tr.Close()

	msg, err := tr.ReadMessage(context.Background())
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}

	if msg.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc=2.0, got %s", msg.JSONRPC)
	}
	if msg.Method != "initialize" {
		t.Errorf("expected method=initialize, got %s", msg.Method)
	}
	if string(msg.ID) != "1" {
		t.Errorf("expected id=1, got %s", msg.ID)
	}
}

func TestStdioTransport_WriteMessage(t *testing.T) {
	reader := strings.NewReader("")
	writer := &bytes.Buffer{}

	tr := NewStdioTransport(reader, writer)
	defer tr.Close()

	msg := &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Result:  json.RawMessage(`{"capabilities":{}}`),
	}

	if err := tr.WriteMessage(context.Background(), msg); err != nil {
		t.Fatalf("WriteMessage error: %v", err)
	}

	output := writer.String()
	if !strings.HasSuffix(output, "\n") {
		t.Error("expected output to end with newline")
	}

	var decoded JSONRPCMessage
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("failed to decode written message: %v", err)
	}
	if decoded.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc=2.0 in output, got %s", decoded.JSONRPC)
	}
}

func TestStdioTransport_ReadWriteRoundTrip(t *testing.T) {
	// Pipe: write to one end, read from the other
	buf := &bytes.Buffer{}

	writerTransport := NewStdioTransport(strings.NewReader(""), buf)

	request := &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`42`),
		Method:  "tools/list",
	}

	if err := writerTransport.WriteMessage(context.Background(), request); err != nil {
		t.Fatalf("WriteMessage error: %v", err)
	}

	readerTransport := NewStdioTransport(buf, &bytes.Buffer{})

	msg, err := readerTransport.ReadMessage(context.Background())
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}

	if msg.Method != "tools/list" {
		t.Errorf("expected method=tools/list, got %s", msg.Method)
	}
	if string(msg.ID) != "42" {
		t.Errorf("expected id=42, got %s", msg.ID)
	}
}

func TestStdioTransport_ReadMessage_ContextCancelled(t *testing.T) {
	// Use a reader that blocks forever (no data)
	pr, _ := blockingPipe()

	tr := NewStdioTransport(pr, &bytes.Buffer{})
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := tr.ReadMessage(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestStdioTransport_ReadMessage_InvalidJSON(t *testing.T) {
	input := "not valid json\n"
	reader := strings.NewReader(input)

	tr := NewStdioTransport(reader, &bytes.Buffer{})
	defer tr.Close()

	_, err := tr.ReadMessage(context.Background())
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestStdioTransport_ReadMessage_MultipleMessages(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	reader := strings.NewReader(input)

	tr := NewStdioTransport(reader, &bytes.Buffer{})
	defer tr.Close()

	msg1, err := tr.ReadMessage(context.Background())
	if err != nil {
		t.Fatalf("ReadMessage 1 error: %v", err)
	}
	if msg1.Method != "initialize" {
		t.Errorf("msg1: expected method=initialize, got %s", msg1.Method)
	}

	msg2, err := tr.ReadMessage(context.Background())
	if err != nil {
		t.Fatalf("ReadMessage 2 error: %v", err)
	}
	if msg2.Method != "tools/list" {
		t.Errorf("msg2: expected method=tools/list, got %s", msg2.Method)
	}
}

// blockingPipe returns an io.Reader that blocks forever on reads.
func blockingPipe() (*blockingReader, struct{}) {
	return &blockingReader{ch: make(chan struct{})}, struct{}{}
}

type blockingReader struct {
	ch chan struct{}
}

func (r *blockingReader) Read([]byte) (int, error) {
	<-r.ch // blocks forever
	return 0, nil
}
