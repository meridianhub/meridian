package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// readResult carries a parsed message or an error from the background reader.
type readResult struct {
	msg *JSONRPCMessage
	err error
}

// StdioTransport implements the Transport interface over stdin/stdout.
// Each JSON-RPC message is a single line of JSON terminated by a newline.
// A single background goroutine reads from the reader, preventing goroutine
// leaks on context cancellation.
type StdioTransport struct {
	reader  *bufio.Reader
	writer  io.Writer
	writeMu sync.Mutex
	closer  io.Closer
	msgs    chan readResult
	once    sync.Once
}

// NewStdioTransport creates a new stdio transport reading from r and writing to w.
// If r implements io.Closer, it will be closed when Close is called.
func NewStdioTransport(r io.Reader, w io.Writer) *StdioTransport {
	var closer io.Closer
	if c, ok := r.(io.Closer); ok {
		closer = c
	}
	t := &StdioTransport{
		reader: bufio.NewReader(r),
		writer: w,
		closer: closer,
		msgs:   make(chan readResult, 1),
	}
	// Start the single reader goroutine that lives for the transport's lifetime.
	go t.readLoop()
	return t
}

// readLoop continuously reads newline-delimited JSON from the reader and
// sends parsed messages (or errors) to the msgs channel. It exits when
// the reader returns an error (e.g. EOF or the underlying reader is closed).
func (t *StdioTransport) readLoop() {
	defer close(t.msgs)
	for {
		line, err := t.reader.ReadBytes('\n')
		if err != nil {
			t.msgs <- readResult{err: fmt.Errorf("read stdin: %w", err)}
			return
		}

		var msg JSONRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			t.msgs <- readResult{err: fmt.Errorf("unmarshal message: %w", err)}
			return
		}

		t.msgs <- readResult{msg: &msg}
	}
}

// ReadMessage reads a single JSON-RPC message from stdin.
// Each message must be a single line of valid JSON.
func (t *StdioTransport) ReadMessage(ctx context.Context) (*JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r, ok := <-t.msgs:
		if !ok {
			return nil, io.EOF
		}
		return r.msg, r.err
	}
}

// WriteMessage writes a single JSON-RPC message to stdout as a newline-delimited JSON line.
func (t *StdioTransport) WriteMessage(_ context.Context, msg *JSONRPCMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	data = append(data, '\n')
	if _, err := t.writer.Write(data); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}

	return nil
}

// Close closes the underlying reader if it implements io.Closer.
// This also terminates the background reader goroutine.
func (t *StdioTransport) Close() error {
	var closeErr error
	t.once.Do(func() {
		if t.closer != nil {
			closeErr = t.closer.Close()
		}
	})
	return closeErr
}
