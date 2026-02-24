package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// StdioTransport implements the Transport interface over stdin/stdout.
// Each JSON-RPC message is a single line of JSON terminated by a newline.
type StdioTransport struct {
	reader  *bufio.Reader
	writer  io.Writer
	writeMu sync.Mutex
	closer  io.Closer
}

// NewStdioTransport creates a new stdio transport reading from r and writing to w.
// If r implements io.Closer, it will be closed when Close is called.
func NewStdioTransport(r io.Reader, w io.Writer) *StdioTransport {
	var closer io.Closer
	if c, ok := r.(io.Closer); ok {
		closer = c
	}
	return &StdioTransport{
		reader: bufio.NewReader(r),
		writer: w,
		closer: closer,
	}
}

// ReadMessage reads a single JSON-RPC message from stdin.
// Each message must be a single line of valid JSON.
func (t *StdioTransport) ReadMessage(ctx context.Context) (*JSONRPCMessage, error) {
	type result struct {
		msg *JSONRPCMessage
		err error
	}

	ch := make(chan result, 1)
	go func() {
		line, err := t.reader.ReadBytes('\n')
		if err != nil {
			ch <- result{err: fmt.Errorf("read stdin: %w", err)}
			return
		}

		var msg JSONRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			ch <- result{err: fmt.Errorf("unmarshal message: %w", err)}
			return
		}

		ch <- result{msg: &msg}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
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
func (t *StdioTransport) Close() error {
	if t.closer != nil {
		return t.closer.Close()
	}
	return nil
}
