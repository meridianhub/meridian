package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestTracer(t *testing.T) {
	tracer := Tracer()
	assert.NotNil(t, tracer)
}

func TestStartSpan(t *testing.T) {
	ctx := context.Background()
	spanCtx, span := StartSpan(ctx, "test-operation",
		AttrRunID.String("run-001"),
		AttrAccountID.String("ACC-001"),
	)
	defer span.End()

	assert.NotNil(t, spanCtx)
	assert.NotNil(t, span)
}

func TestStartSpan_NoAttributes(t *testing.T) {
	ctx := context.Background()
	spanCtx, span := StartSpan(ctx, "test-operation")
	defer span.End()

	assert.NotNil(t, spanCtx)
	assert.NotNil(t, span)
}

func TestAttributeKeys(t *testing.T) {
	// Verify attribute keys are usable
	attrs := []attribute.KeyValue{
		AttrRunID.String("run-001"),
		AttrAccountID.String("ACC-001"),
		AttrInstrumentCode.String("GBP"),
		AttrRunType.String("manual"),
		AttrVarianceCount.Int(5),
		AttrSnapshotCount.Int(10),
		AttrDisputeID.String("disp-001"),
		AttrVarianceID.String("var-001"),
	}
	assert.Len(t, attrs, 8)
}
