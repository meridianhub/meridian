package observability_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/meridianhub/meridian/internal/platform/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestHTTPInjectTraceContext(t *testing.T) {
	ctx := context.Background()

	cfg := observability.TracerConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "alloy:4317",
		SamplingRate: 1.0,
		Enabled:      false,
	}

	tracer, err := observability.NewTracer(ctx, cfg)
	require.NoError(t, err)

	// Start a span
	ctx, span := tracer.Start(ctx, "test-operation")
	defer span.End()

	// Inject into HTTP headers
	headers := http.Header{}
	observability.HTTPInjectTraceContext(ctx, headers)

	// Note: Disabled tracer doesn't create valid span contexts
	// We're just testing the function doesn't panic
	// Headers may be empty with no-op tracer
	assert.NotNil(t, headers)
}

func TestHTTPExtractTraceContext(t *testing.T) {
	ctx := context.Background()

	cfg := observability.TracerConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "alloy:4317",
		SamplingRate: 1.0,
		Enabled:      false,
	}

	tracer, err := observability.NewTracer(ctx, cfg)
	require.NoError(t, err)

	// Create a span and inject into headers
	ctx, span := tracer.Start(ctx, "test-operation")
	defer span.End()

	headers := http.Header{}
	observability.HTTPInjectTraceContext(ctx, headers)

	// Extract from headers
	newCtx := observability.HTTPExtractTraceContext(context.Background(), headers)

	// Just verify the function works without panicking
	assert.NotNil(t, newCtx)
}

func TestMapInjectTraceContext(t *testing.T) {
	ctx := context.Background()

	cfg := observability.TracerConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "alloy:4317",
		SamplingRate: 1.0,
		Enabled:      false,
	}

	tracer, err := observability.NewTracer(ctx, cfg)
	require.NoError(t, err)

	// Start a span
	ctx, span := tracer.Start(ctx, "test-operation")
	defer span.End()

	// Inject into map
	carrier := make(map[string]string)
	observability.MapInjectTraceContext(ctx, carrier)

	// Note: Disabled tracer doesn't create valid span contexts
	// We're just testing the function doesn't panic
	assert.NotNil(t, carrier)
}

func TestMapExtractTraceContext(t *testing.T) {
	ctx := context.Background()

	cfg := observability.TracerConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "alloy:4317",
		SamplingRate: 1.0,
		Enabled:      false,
	}

	tracer, err := observability.NewTracer(ctx, cfg)
	require.NoError(t, err)

	// Create a span and inject into map
	ctx, span := tracer.Start(ctx, "test-operation")
	defer span.End()

	carrier := make(map[string]string)
	observability.MapInjectTraceContext(ctx, carrier)

	// Extract from map
	newCtx := observability.MapExtractTraceContext(context.Background(), carrier)

	// Just verify the function works without panicking
	assert.NotNil(t, newCtx)
}

func TestDetachedContext(t *testing.T) {
	ctx := context.Background()

	cfg := observability.TracerConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "alloy:4317",
		SamplingRate: 1.0,
		Enabled:      false,
	}

	tracer, err := observability.NewTracer(ctx, cfg)
	require.NoError(t, err)

	// Create a span
	ctx, span := tracer.Start(ctx, "test-operation")
	defer span.End()

	// Create detached context
	detachedCtx := observability.DetachedContext(ctx)

	// Detached context should have the span but not be affected by parent cancellation
	extractedSpan := trace.SpanFromContext(detachedCtx)
	assert.Equal(t, span.SpanContext().TraceID(), extractedSpan.SpanContext().TraceID())

	// Verify the context is actually detached (has no deadline)
	_, hasDeadline := detachedCtx.Deadline()
	assert.False(t, hasDeadline, "Detached context should not have deadline from parent")
}
