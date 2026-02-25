// Package transport defines the Transport interface and JSON-RPC 2.0 message types
// for the MCP server's communication layer.
package transport

import "encoding/json"

// JSONRPCVersion is the JSON-RPC protocol version string.
const JSONRPCVersion = "2.0"

// JSONRPCMessage represents a JSON-RPC 2.0 message (request, response, or notification).
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// IsRequest returns true if the message is a JSON-RPC request (has method and id).
func (m *JSONRPCMessage) IsRequest() bool {
	return m.Method != "" && m.ID != nil
}

// IsNotification returns true if the message is a JSON-RPC notification (has method, no id).
func (m *JSONRPCMessage) IsNotification() bool {
	return m.Method != "" && m.ID == nil
}

// IsResponse returns true if the message is a JSON-RPC response (has result or error, no method).
func (m *JSONRPCMessage) IsResponse() bool {
	return m.Method == "" && (m.Result != nil || m.Error != nil)
}

// NewResponse creates a JSON-RPC 2.0 response message with the given id and result.
func NewResponse(id json.RawMessage, result any) (*JSONRPCMessage, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Result:  data,
	}, nil
}

// NewErrorResponse creates a JSON-RPC 2.0 error response message.
func NewErrorResponse(id json.RawMessage, code int, message string) *JSONRPCMessage {
	return &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
}
