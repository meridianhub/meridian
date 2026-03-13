package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// testServer wraps an *mcp.Server and provides a test-friendly Call/List API.
// The in-memory transport is lazily established on first Call or List.
type testServer struct {
	srv     *mcp.Server
	t       *testing.T
	session *mcp.ClientSession
}

// newTestServer creates an *mcp.Server ready for tool registration.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)
	return &testServer{srv: srv, t: t}
}

// Server returns the underlying *mcp.Server for direct registration calls.
func (ts *testServer) Server() *mcp.Server {
	return ts.srv
}

// ensureConnected lazily creates the in-memory transport pair and connects.
func (ts *testServer) ensureConnected(ctx context.Context) *mcp.ClientSession {
	if ts.session != nil {
		return ts.session
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := ts.srv.Connect(ctx, st, nil)
	if err != nil {
		ts.t.Fatalf("server connect: %v", err)
	}
	ts.t.Cleanup(func() { ss.Close() })

	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		ts.t.Fatalf("client connect: %v", err)
	}
	ts.t.Cleanup(func() { cs.Close() })

	ts.session = cs
	return cs
}

// Call invokes the named tool with the given JSON params and returns the
// deserialized result. This mirrors the old Registry.Call() API for test compat.
func (ts *testServer) Call(ctx context.Context, name string, params json.RawMessage) (interface{}, error) {
	cs := ts.ensureConnected(ctx)

	var args any
	if params == nil || string(params) == "null" {
		args = map[string]any{}
	} else {
		var m map[string]any
		if err := json.Unmarshal(params, &m); err != nil {
			return nil, fmt.Errorf("unmarshal params: %w", err)
		}
		args = m
	}

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, err
	}

	// The SDK returns tool errors as IsError=true on the result, not as Go errors.
	if result.IsError {
		if len(result.Content) > 0 {
			if tc, ok := result.Content[0].(*mcp.TextContent); ok {
				return nil, errors.New(tc.Text)
			}
		}
		return nil, fmt.Errorf("tool %q returned an error", name)
	}

	if len(result.Content) == 0 {
		return nil, fmt.Errorf("tool %q returned no content", name)
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return nil, fmt.Errorf("tool %q returned non-text content: %T", name, result.Content[0])
	}

	var v interface{}
	if unmarshalErr := json.Unmarshal([]byte(tc.Text), &v); unmarshalErr != nil {
		return tc.Text, nil //nolint:nilerr // intentional: non-JSON text is a valid result, not an error
	}
	return v, nil
}

// List returns tool metadata for all registered tools.
func (ts *testServer) List(ctx context.Context) []tools.Tool {
	cs := ts.ensureConnected(ctx)

	result, err := cs.ListTools(ctx, nil)
	if err != nil {
		ts.t.Fatalf("ListTools: %v", err)
	}

	out := make([]tools.Tool, 0, len(result.Tools))
	for _, t := range result.Tools {
		out = append(out, tools.Tool{
			Name:        t.Name,
			Description: t.Description,
		})
	}
	return out
}
