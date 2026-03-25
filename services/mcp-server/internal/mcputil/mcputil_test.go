package mcputil_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/meridianhub/meridian/services/mcp-server/internal/mcputil"
)

// --- TextResult ---

func TestTextResult_ReturnsTextContent(t *testing.T) {
	result := mcputil.TextResult("hello world")

	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	assert.False(t, result.IsError)

	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected *mcp.TextContent, got %T", result.Content[0])
	assert.Equal(t, "hello world", tc.Text)
}

func TestTextResult_EmptyString_ReturnsEmptyTextContent(t *testing.T) {
	result := mcputil.TextResult("")

	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	tc := result.Content[0].(*mcp.TextContent)
	assert.Equal(t, "", tc.Text)
}

func TestTextResult_IsNotError(t *testing.T) {
	result := mcputil.TextResult("ok")
	assert.False(t, result.IsError)
}

// --- JSONResult ---

func TestJSONResult_SimpleStruct_ReturnsMarshaledText(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	result, err := mcputil.JSONResult(payload{Name: "test", Value: 42})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	tc := result.Content[0].(*mcp.TextContent)
	assert.JSONEq(t, `{"name":"test","value":42}`, tc.Text)
}

func TestJSONResult_Map_ReturnsMarshaledJSON(t *testing.T) {
	data := map[string]interface{}{"key": "value", "count": 3}
	result, err := mcputil.JSONResult(data)

	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	tc := result.Content[0].(*mcp.TextContent)
	assert.Contains(t, tc.Text, `"key"`)
	assert.Contains(t, tc.Text, `"count"`)
}

func TestJSONResult_Nil_ReturnsNullJSON(t *testing.T) {
	result, err := mcputil.JSONResult(nil)

	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	tc := result.Content[0].(*mcp.TextContent)
	assert.Equal(t, "null", tc.Text)
}

func TestJSONResult_UnmarshalableValue_ReturnsError(t *testing.T) {
	// Channels cannot be marshaled to JSON.
	result, err := mcputil.JSONResult(make(chan int))

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestJSONResult_IsNotError(t *testing.T) {
	result, err := mcputil.JSONResult(map[string]string{"ok": "true"})

	require.NoError(t, err)
	assert.False(t, result.IsError)
}

// --- ErrorResult ---

func TestErrorResult_SetsIsError(t *testing.T) {
	result := mcputil.ErrorResult("something went wrong")

	require.NotNil(t, result)
	assert.True(t, result.IsError)
}

func TestErrorResult_ContainsMessage(t *testing.T) {
	msg := "operation failed: not found"
	result := mcputil.ErrorResult(msg)

	require.Len(t, result.Content, 1)
	tc := result.Content[0].(*mcp.TextContent)
	assert.Equal(t, msg, tc.Text)
}

func TestErrorResult_EmptyMessage_StillSetsIsError(t *testing.T) {
	result := mcputil.ErrorResult("")

	assert.True(t, result.IsError)
	tc := result.Content[0].(*mcp.TextContent)
	assert.Equal(t, "", tc.Text)
}

// --- FormatError ---

func TestFormatError_FormatsMessage(t *testing.T) {
	result := mcputil.FormatError("error: %s (code %d)", "not found", 404)

	require.NotNil(t, result)
	assert.True(t, result.IsError)
	tc := result.Content[0].(*mcp.TextContent)
	assert.Equal(t, "error: not found (code 404)", tc.Text)
}

func TestFormatError_NoArgs_PassesThroughFormat(t *testing.T) {
	result := mcputil.FormatError("simple error message")

	tc := result.Content[0].(*mcp.TextContent)
	assert.Equal(t, "simple error message", tc.Text)
}

func TestFormatError_SetsIsError(t *testing.T) {
	result := mcputil.FormatError("bad input: %v", "missing field")
	assert.True(t, result.IsError)
}

// --- SanitizeError ---

func TestSanitizeError_GRPCStatusError_ReturnsMessage(t *testing.T) {
	grpcErr := status.Errorf(codes.NotFound, "account not found")
	msg := mcputil.SanitizeError(grpcErr)
	assert.Equal(t, "account not found", msg)
}

func TestSanitizeError_GRPCStatusError_StripsCodeFromMessage(t *testing.T) {
	// gRPC status error message should not include the code prefix.
	grpcErr := status.Errorf(codes.InvalidArgument, "invalid parameter: amount must be positive")
	msg := mcputil.SanitizeError(grpcErr)
	assert.Equal(t, "invalid parameter: amount must be positive", msg)
	assert.NotContains(t, msg, "InvalidArgument")
	assert.NotContains(t, msg, "rpc error")
}

func TestSanitizeError_PlainError_ReturnsFullMessage(t *testing.T) {
	err := errors.New("plain error message")
	msg := mcputil.SanitizeError(err)
	assert.Equal(t, "plain error message", msg)
}

func TestSanitizeError_WrappedError_ReturnsFullChain(t *testing.T) {
	inner := errors.New("inner failure")
	outer := fmt.Errorf("operation failed: %w", inner)
	msg := mcputil.SanitizeError(outer)
	assert.Contains(t, msg, "inner failure")
}

func TestSanitizeError_GRPCInternalError_ReturnsMessageNotCode(t *testing.T) {
	grpcErr := status.Errorf(codes.Internal, "unexpected database error")
	msg := mcputil.SanitizeError(grpcErr)
	// Should return the message, not the numeric code or status string.
	assert.Equal(t, "unexpected database error", msg)
}
