package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestTracer(t *testing.T) {
	tr := Tracer()
	assert.NotNil(t, tr)
}

func TestStartSpan(t *testing.T) {
	ctx, span := StartSpan(context.Background(), "test-span",
		AttrTenantID.String("tenant-a"),
		AttrPaymentRail.String("stripe"),
	)
	defer span.End()

	assert.NotNil(t, ctx)
	assert.NotNil(t, span)
}

func TestRecordError(t *testing.T) {
	_, span := StartSpan(context.Background(), "test-error")
	defer span.End()

	// With error
	RecordError(span, errors.New("test error"))

	// Nil error - should be a no-op
	RecordError(span, nil)

	// Nil span - should be a no-op
	RecordError(nil, errors.New("test error"))
}

func TestAttributeKeys(t *testing.T) {
	// Verify attribute keys are defined and usable
	attrs := []attribute.KeyValue{
		AttrTenantID.String("tenant-a"),
		AttrDispatchID.String("dispatch-1"),
		AttrPaymentOrderID.String("po-1"),
		AttrPaymentRail.String("stripe"),
		AttrDispatchStatus.String("DELIVERED"),
		AttrCorrelationID.String("corr-1"),
		AttrProviderRef.String("pi_test"),
		AttrAmountUnits.Int64(1000),
		AttrInstrumentCode.String("GBP"),
	}
	assert.Len(t, attrs, 9)
}
