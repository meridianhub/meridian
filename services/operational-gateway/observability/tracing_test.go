package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// setupTestTracer installs a recording tracer provider and returns the span recorder.
// It restores the original provider when the test finishes.
func setupTestTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
	})
	return sr
}

func TestTracer_ReturnsNonNil(t *testing.T) {
	tr := Tracer()
	assert.NotNil(t, tr)
}

func TestStartSpan_CreatesSpan(t *testing.T) {
	sr := setupTestTracer(t)

	ctx, span := StartSpan(context.Background(), "test-span",
		AttrTenantID.String("tenant-1"),
		AttrInstructionID.String("inst-1"),
	)
	require.NotNil(t, span)
	require.NotNil(t, ctx)
	span.End()

	spans := sr.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "test-span", spans[0].Name())
	assert.Equal(t, trace.SpanKindInternal, spans[0].SpanKind())

	attrs := spans[0].Attributes()
	assert.Contains(t, attrs, AttrTenantID.String("tenant-1"))
	assert.Contains(t, attrs, AttrInstructionID.String("inst-1"))
}

func TestStartSpan_WithMultipleAttributes(t *testing.T) {
	sr := setupTestTracer(t)

	attrs := []attribute.KeyValue{
		AttrInstructionType.String("kyc.verify"),
		AttrProviderName.String("Onfido"),
		AttrAttemptCount.Int(1),
		AttrMaxAttempts.Int(3),
		AttrBatchSize.Int(10),
	}
	_, span := StartSpan(context.Background(), "multi-attr-span", attrs...)
	span.End()

	spans := sr.Ended()
	require.Len(t, spans, 1)
	recorded := spans[0].Attributes()
	for _, a := range attrs {
		assert.Contains(t, recorded, a)
	}
}

func TestRecordError_NilError(t *testing.T) {
	sr := setupTestTracer(t)

	_, span := StartSpan(context.Background(), "test")
	RecordError(span, nil)
	span.End()

	spans := sr.Ended()
	require.Len(t, spans, 1)
	// Nil error should not set error status.
	assert.Equal(t, codes.Unset, spans[0].Status().Code)
	assert.Empty(t, spans[0].Events())
}

func TestRecordError_NilSpan(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordError(nil, errors.New("test error"))
	})
}

func TestRecordError_WithError(t *testing.T) {
	sr := setupTestTracer(t)

	_, span := StartSpan(context.Background(), "error-span")
	RecordError(span, errors.New("something broke"))
	span.End()

	spans := sr.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status().Code)
	assert.Equal(t, "error", spans[0].Status().Description)
	require.NotEmpty(t, spans[0].Events())
	assert.Equal(t, "exception", spans[0].Events()[0].Name)
}

func TestAttributeKeys_AreDistinct(t *testing.T) {
	keys := []attribute.Key{
		AttrTenantID,
		AttrInstructionID,
		AttrInstructionType,
		AttrInstructionStatus,
		AttrProviderConnectionID,
		AttrProviderName,
		AttrAttemptCount,
		AttrMaxAttempts,
		AttrErrorCode,
		AttrBatchSize,
	}
	seen := make(map[attribute.Key]bool)
	for _, k := range keys {
		assert.False(t, seen[k], "duplicate attribute key: %s", k)
		seen[k] = true
	}
}

func TestStartSpan_ProducesValidSpanContext(t *testing.T) {
	sr := setupTestTracer(t)

	ctx, span := StartSpan(context.Background(), "valid-span")
	defer span.End()
	// Verify span is embedded in the returned context.
	spanFromCtx := trace.SpanFromContext(ctx)
	assert.Equal(t, span, spanFromCtx)

	span.End()
	spans := sr.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "valid-span", spans[0].Name())
}
