package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "meridian/operational-gateway"

// Tracer returns the OpenTelemetry tracer for the operational gateway service.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// StartSpan creates a new internal span as a child of the span in the context.
// The returned context contains the new span; callers must call span.End() when done.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name,
		trace.WithAttributes(attrs...),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
}

// RecordError marks a span as having errored and records the error message.
func RecordError(span trace.Span, err error) {
	if err == nil || span == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "error")
}

// Common attribute keys for operational gateway spans.
var (
	AttrTenantID             = attribute.Key("gateway.tenant_id")
	AttrInstructionID        = attribute.Key("gateway.instruction_id")
	AttrInstructionType      = attribute.Key("gateway.instruction_type")
	AttrInstructionStatus    = attribute.Key("gateway.instruction_status")
	AttrProviderConnectionID = attribute.Key("gateway.provider_connection_id")
	AttrProviderName         = attribute.Key("gateway.provider_name")
	AttrAttemptCount         = attribute.Key("gateway.attempt_count")
	AttrMaxAttempts          = attribute.Key("gateway.max_attempts")
	AttrErrorCode            = attribute.Key("gateway.error_code")
	AttrBatchSize            = attribute.Key("gateway.batch_size")
)
