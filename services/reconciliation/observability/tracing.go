package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "meridian/reconciliation"

// Tracer returns the OpenTelemetry tracer for the reconciliation service.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// StartSpan creates a new span as a child of the span in the context.
// The returned context contains the new span; callers must call span.End()
// when the operation completes.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name,
		trace.WithAttributes(attrs...),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
}

// Common attribute keys for reconciliation spans.
var (
	AttrRunID          = attribute.Key("reconciliation.run_id")
	AttrAccountID      = attribute.Key("reconciliation.account_id")
	AttrInstrumentCode = attribute.Key("reconciliation.instrument_code")
	AttrRunType        = attribute.Key("reconciliation.run_type")
	AttrVarianceCount  = attribute.Key("reconciliation.variance_count")
	AttrSnapshotCount  = attribute.Key("reconciliation.snapshot_count")
	AttrDisputeID      = attribute.Key("reconciliation.dispute_id")
	AttrVarianceID     = attribute.Key("reconciliation.variance_id")
)
