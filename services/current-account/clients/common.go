package clients

import (
	"context"
	"time"

	"google.golang.org/grpc/metadata"
)

// PropagateCorrelationID extracts correlation ID from context and adds it to gRPC metadata
func PropagateCorrelationID(ctx context.Context) context.Context {
	correlationID := ExtractCorrelationID(ctx)
	if correlationID == "" {
		return ctx
	}

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		md = md.Copy()
	}

	md.Set("x-correlation-id", correlationID)
	return metadata.NewOutgoingContext(ctx, md)
}

// ExtractCorrelationID attempts to extract correlation ID from context
// It checks multiple common keys used for correlation/request tracking
func ExtractCorrelationID(ctx context.Context) string {
	keys := []string{"correlation-id", "x-correlation-id", "x-request-id", "request-id"}

	// Check context values first
	for _, key := range keys {
		if val := ctx.Value(key); val != nil {
			if id, ok := val.(string); ok && id != "" {
				return id
			}
		}
	}

	// Check incoming metadata as fallback
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, key := range keys {
			if vals := md.Get(key); len(vals) > 0 && vals[0] != "" {
				return vals[0]
			}
		}
	}

	return ""
}

// WithTimeout applies a timeout to the context if one isn't already set
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return ctx, func() {}
}
