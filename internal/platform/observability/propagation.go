package observability

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/propagation"
)

// HTTPInjectTraceContext injects trace context into HTTP request headers
//
// This enables trace propagation across HTTP service boundaries.
// Call this before making an outgoing HTTP request.
//
// Example:
//
//	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
//	if err != nil {
//	    return err
//	}
//	observability.HTTPInjectTraceContext(ctx, req.Header)
//	resp, err := client.Do(req)
func HTTPInjectTraceContext(ctx context.Context, header http.Header) {
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)

	carrier := propagation.HeaderCarrier(header)
	propagator.Inject(ctx, carrier)
}

// HTTPExtractTraceContext extracts trace context from HTTP request headers
//
// This enables receiving trace context from incoming HTTP requests.
// Call this early in your HTTP handler to continue the trace.
//
// Example:
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//	    ctx := observability.HTTPExtractTraceContext(r.Context(), r.Header)
//	    ctx, span := tracer.Start(ctx, "handle-request")
//	    defer span.End()
//	    // ... handle request with traced context
//	}
func HTTPExtractTraceContext(ctx context.Context, header http.Header) context.Context {
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)

	carrier := propagation.HeaderCarrier(header)
	return propagator.Extract(ctx, carrier)
}

// MapInjectTraceContext injects trace context into a string map
//
// This is useful for propagating trace context through message queues
// or other systems that don't have built-in header support.
//
// Example (Kafka):
//
//	headers := make(map[string]string)
//	observability.MapInjectTraceContext(ctx, headers)
//
//	msg := &kafka.Message{
//	    Topic: "events",
//	    Value: payload,
//	    Headers: kafkaHeadersFromMap(headers),
//	}
func MapInjectTraceContext(ctx context.Context, carrier map[string]string) {
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)

	propagator.Inject(ctx, propagation.MapCarrier(carrier))
}

// MapExtractTraceContext extracts trace context from a string map
//
// This is useful for receiving trace context from message queues
// or other systems that don't have built-in header support.
//
// Example (Kafka):
//
//	headers := kafkaHeadersToMap(msg.Headers)
//	ctx := observability.MapExtractTraceContext(context.Background(), headers)
//	ctx, span := tracer.Start(ctx, "process-message")
//	defer span.End()
func MapExtractTraceContext(ctx context.Context, carrier map[string]string) context.Context {
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)

	return propagator.Extract(ctx, propagation.MapCarrier(carrier))
}

// DetachedContext returns a new context without cancellation
//
// This is useful for background operations that should continue
// after the parent context is cancelled, while preserving trace context.
//
// Example:
//
//	func handler(ctx context.Context) error {
//	    // Start async operation that should complete even if request is cancelled
//	    go func() {
//	        bgCtx := observability.DetachedContext(ctx)
//	        _, span := tracer.Start(bgCtx, "background-cleanup")
//	        defer span.End()
//	        // ... perform cleanup
//	    }()
//	    return nil
//	}
func DetachedContext(ctx context.Context) context.Context {
	span := SpanFromContext(ctx)
	return ContextWithSpan(context.Background(), span)
}
