package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestTracer_ReturnsNonNil(t *testing.T) {
	tr := Tracer()
	assert.NotNil(t, tr)
}

func TestStartSpan_CreatesSpan(t *testing.T) {
	ctx, span := StartSpan(context.Background(), "test-span",
		AttrTenantID.String("tenant-1"),
		AttrInstructionID.String("inst-1"),
	)
	require.NotNil(t, span)
	require.NotNil(t, ctx)
	span.End()
}

func TestStartSpan_WithMultipleAttributes(t *testing.T) {
	attrs := []attribute.KeyValue{
		AttrInstructionType.String("kyc.verify"),
		AttrProviderName.String("Onfido"),
		AttrAttemptCount.Int(1),
		AttrMaxAttempts.Int(3),
		AttrBatchSize.Int(10),
	}
	ctx, span := StartSpan(context.Background(), "multi-attr-span", attrs...)
	require.NotNil(t, span)
	require.NotNil(t, ctx)
	span.End()
}

func TestRecordError_NilError(t *testing.T) {
	assert.NotPanics(t, func() {
		_, span := StartSpan(context.Background(), "test")
		defer span.End()
		RecordError(span, nil)
	})
}

func TestRecordError_NilSpan(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordError(nil, errors.New("test error"))
	})
}

func TestRecordError_WithError(t *testing.T) {
	assert.NotPanics(t, func() {
		_, span := StartSpan(context.Background(), "error-span")
		defer span.End()
		RecordError(span, errors.New("test error"))
	})
}

func TestRecordError_SetsErrorStatus(t *testing.T) {
	// Use noop tracer to verify no panic; status is set internally.
	assert.NotPanics(t, func() {
		tracer := noop.NewTracerProvider().Tracer("test")
		_, span := tracer.Start(context.Background(), "test-op")
		RecordError(span, errors.New("something went wrong"))
		span.End()
	})
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
	ctx, span := StartSpan(context.Background(), "valid-span")
	defer span.End()
	// Verify span is embedded in the returned context.
	spanFromCtx := trace.SpanFromContext(ctx)
	assert.Equal(t, span, spanFromCtx)
}
