package transport

import "context"

// Transport defines the interface for MCP server communication.
// Implementations handle reading and writing JSON-RPC 2.0 messages
// over different transport mechanisms (stdio, SSE).
type Transport interface {
	// ReadMessage reads the next JSON-RPC message from the transport.
	// It blocks until a message is available or the context is cancelled.
	ReadMessage(ctx context.Context) (*JSONRPCMessage, error)

	// WriteMessage writes a JSON-RPC message to the transport.
	WriteMessage(ctx context.Context, msg *JSONRPCMessage) error

	// Close releases any resources held by the transport.
	Close() error
}
