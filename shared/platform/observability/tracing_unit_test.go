package observability_test

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestTracer_RecordError_NoSpan(t *testing.T) {
	tracer := newTestTracer(t)

	// Context with no span - should not panic.
	tracer.RecordError(context.Background(), assert.AnError)
}

func TestTracer_AddEvent_NoSpan(t *testing.T) {
	tracer := newTestTracer(t)

	// Context with no span - should not panic.
	tracer.AddEvent(context.Background(), "test-event")
}

func TestTracer_SetAttributes_NoSpan(t *testing.T) {
	tracer := newTestTracer(t)

	// Context with no span - should not panic.
	tracer.SetAttributes(context.Background(), attribute.String("key", "value"))
}

func TestTracer_Shutdown_WithoutDeadline(t *testing.T) {
	tracer := newTestTracer(t)

	// Context without deadline - shutdown should add one internally.
	err := tracer.Shutdown(context.Background())
	assert.NoError(t, err)
}

func TestNewTracer_EnabledWithInvalidEndpoint(t *testing.T) {
	// Enabled tracer with a non-existent endpoint - should still create
	// (connection errors happen at export time, not creation time).
	cfg := observability.TracerConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "localhost:99999",
		SamplingRate: 1.0,
		Enabled:      true,
	}

	tracer, err := observability.NewTracer(context.Background(), cfg)
	require.NoError(t, err)
	assert.NotNil(t, tracer)

	// Shutdown should work even with bad endpoint.
	err = tracer.Shutdown(context.Background())
	assert.NoError(t, err)
}
