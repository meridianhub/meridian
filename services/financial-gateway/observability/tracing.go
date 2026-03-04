package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "meridian/financial-gateway"

// Tracer returns the OpenTelemetry tracer for the financial gateway service.
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

// Common attribute keys for financial gateway spans.
var (
	AttrTenantID       = attribute.Key("financial_gateway.tenant_id")
	AttrDispatchID     = attribute.Key("financial_gateway.dispatch_id")
	AttrPaymentOrderID = attribute.Key("financial_gateway.payment_order_id")
	AttrPaymentRail    = attribute.Key("financial_gateway.payment_rail")
	AttrDispatchStatus = attribute.Key("financial_gateway.dispatch_status")
	AttrCorrelationID  = attribute.Key("financial_gateway.correlation_id")
	AttrProviderRef    = attribute.Key("financial_gateway.provider_reference")
	AttrAmountUnits    = attribute.Key("financial_gateway.amount_units")
	AttrInstrumentCode = attribute.Key("financial_gateway.instrument_code")
)
