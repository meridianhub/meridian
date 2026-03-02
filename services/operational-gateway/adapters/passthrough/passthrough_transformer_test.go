package passthrough_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/operational-gateway/adapters/passthrough"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// newTestInstruction creates an Instruction with the given payload for testing.
func newTestInstruction(payload map[string]any) *domain.Instruction {
	inst, _ := domain.NewInstruction(
		uuid.New(),
		"payment.create",
		"conn-001",
		payload,
	)
	return inst
}

// TestTransformer_ImplementsPayloadTransformer is a compile-time assertion that Transformer
// satisfies the PayloadTransformer interface.
func TestTransformer_ImplementsPayloadTransformer(_ *testing.T) {
	var _ ports.PayloadTransformer = passthrough.NewTransformer()
}

// TestTransformer_TransformOutbound_ReturnsRawJSON verifies that TransformOutbound
// serializes the instruction payload to JSON without modification.
func TestTransformer_TransformOutbound_ReturnsRawJSON(t *testing.T) {
	tr := passthrough.NewTransformer()

	payload := map[string]any{"amount": "100.00", "currency": "GBP"}
	inst := newTestInstruction(payload)
	route := &ports.InstructionRoute{
		HTTPMethod:   "POST",
		PathTemplate: "/payments",
	}

	body, headers, err := tr.TransformOutbound(context.Background(), inst, route)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "100.00", got["amount"])
	assert.Equal(t, "GBP", got["currency"])
	assert.Nil(t, headers)
}

// TestTransformer_TransformOutbound_IncludesStaticHeaders verifies that static headers
// defined on the route are returned unchanged.
func TestTransformer_TransformOutbound_IncludesStaticHeaders(t *testing.T) {
	tr := passthrough.NewTransformer()

	inst := newTestInstruction(map[string]any{"x": 1})
	route := &ports.InstructionRoute{
		Headers: map[string]string{
			"X-Version": "1",
			"Accept":    "application/json",
		},
	}

	_, headers, err := tr.TransformOutbound(context.Background(), inst, route)
	require.NoError(t, err)
	assert.Equal(t, "1", headers["X-Version"])
	assert.Equal(t, "application/json", headers["Accept"])
}

// TestTransformer_TransformOutbound_EmptyHeaders verifies nil is returned when the route
// has no static headers.
func TestTransformer_TransformOutbound_EmptyHeaders(t *testing.T) {
	tr := passthrough.NewTransformer()

	inst := newTestInstruction(map[string]any{"k": "v"})
	route := &ports.InstructionRoute{}

	_, headers, err := tr.TransformOutbound(context.Background(), inst, route)
	require.NoError(t, err)
	assert.Nil(t, headers)
}

// TestTransformer_TransformInbound_Success_2xx verifies that HTTP 2xx responses
// produce an ACCEPTED outcome with no failure reason.
func TestTransformer_TransformInbound_Success_2xx(t *testing.T) {
	tr := passthrough.NewTransformer()
	route := &ports.InstructionRoute{}

	for _, code := range []int{200, 201, 202, 204} {
		outcome, err := tr.TransformInbound(context.Background(), code, []byte(`{}`), route)
		require.NoError(t, err, "status %d should not error", code)
		require.NotNil(t, outcome)
		assert.Equal(t, "ACCEPTED", outcome.ProviderStatus, "status %d", code)
		assert.Empty(t, outcome.FailureReason, "status %d should have no failure reason", code)
		assert.False(t, outcome.ShouldRetry, "status %d should not retry", code)
	}
}

// TestTransformer_TransformInbound_Failure_non2xx verifies that non-2xx responses
// produce a REJECTED outcome with the status code in the failure reason.
func TestTransformer_TransformInbound_Failure_non2xx(t *testing.T) {
	tr := passthrough.NewTransformer()
	route := &ports.InstructionRoute{}

	for _, code := range []int{400, 401, 403, 404, 422, 429, 500, 502, 503} {
		outcome, err := tr.TransformInbound(context.Background(), code, []byte(`{"error":"msg"}`), route)
		require.NoError(t, err, "status %d should not error", code)
		require.NotNil(t, outcome)
		assert.Equal(t, "REJECTED", outcome.ProviderStatus, "status %d", code)
		assert.Contains(t, outcome.FailureReason, "HTTP", "status %d failure reason should mention HTTP", code)
		assert.False(t, outcome.ShouldRetry, "status %d ShouldRetry should be false (passthrough has no retry logic)", code)
	}
}

// TestTransformer_TransformInbound_IgnoresBody verifies that the passthrough transformer
// does not attempt to parse the response body.
func TestTransformer_TransformInbound_IgnoresBody(t *testing.T) {
	tr := passthrough.NewTransformer()
	route := &ports.InstructionRoute{}

	// Even invalid JSON should not cause an error in passthrough mode.
	outcome, err := tr.TransformInbound(context.Background(), 200, []byte(`not valid json`), route)
	require.NoError(t, err)
	assert.Equal(t, "ACCEPTED", outcome.ProviderStatus)
}

// TestTransformer_TransformOutbound_ReturnsError_WhenInstructionNil verifies that a nil
// instruction results in a wrapped ErrTransformFailed.
func TestTransformer_TransformOutbound_ReturnsError_WhenInstructionNil(t *testing.T) {
	tr := passthrough.NewTransformer()
	route := &ports.InstructionRoute{}

	_, _, err := tr.TransformOutbound(context.Background(), nil, route)
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrTransformFailed)
}

// TestTransformer_TransformOutbound_ReturnsError_WhenRouteNil verifies that a nil route
// results in a wrapped ErrTransformFailed.
func TestTransformer_TransformOutbound_ReturnsError_WhenRouteNil(t *testing.T) {
	tr := passthrough.NewTransformer()
	inst := newTestInstruction(map[string]any{"k": "v"})

	_, _, err := tr.TransformOutbound(context.Background(), inst, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrTransformFailed)
}
