package clients

import (
	"context"

	"google.golang.org/grpc/metadata"
)

// idempotencyKeyType is a private type for context key to prevent collisions.
type idempotencyKeyType string

// idempotencyKeyContextKey is the context key for storing idempotency keys.
const idempotencyKeyContextKey idempotencyKeyType = "idempotency_key"

// PropagateIdempotencyKey stores an idempotency key in the context for
// later extraction by service clients. The key will be propagated via
// both protobuf fields (if available) and gRPC metadata headers when
// ApplyIdempotencyKey is called.
//
// This function creates a new context with the idempotency key attached,
// preserving immutability of the original context.
//
// Example usage:
//
//	ctx := clients.PropagateIdempotencyKey(ctx, "saga_abc123_step_5")
//	// Later in service client code:
//	ctx = clients.ApplyIdempotencyKey(ctx, req)
//	resp, err := c.client.SomeMethod(ctx, req)
func PropagateIdempotencyKey(ctx context.Context, key string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, idempotencyKeyContextKey, key)
}

// ExtractIdempotencyKey retrieves the idempotency key from the context.
// Returns an empty string if the key is not present in the context.
//
// This function is safe to call on nil contexts or contexts without
// an idempotency key.
//
// Example usage:
//
//	key := clients.ExtractIdempotencyKey(ctx)
//	if key != "" {
//	    // Use the key for idempotent operations
//	}
func ExtractIdempotencyKey(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if key, ok := ctx.Value(idempotencyKeyContextKey).(string); ok {
		return key
	}
	return ""
}

// ApplyIdempotencyKey applies the idempotency key from context to gRPC metadata
// using the x-idempotency-key header. This function should be called before
// making gRPC service calls to ensure the idempotency key is propagated.
//
// The key is only added to metadata if:
//   - The context contains an idempotency key (via PropagateIdempotencyKey)
//   - The key is non-empty
//
// This function preserves any existing metadata in the context.
//
// The req parameter is reserved for future enhancement where the idempotency
// key could be set on protobuf messages that have an IdempotencyKey field.
// Currently, only metadata propagation is implemented as it works universally
// across all gRPC calls.
//
// Example usage in service clients:
//
//	func (c *Client) CreateAccount(ctx context.Context, req *pb.CreateAccountRequest) (*pb.CreateAccountResponse, error) {
//	    ctx = clients.ApplyIdempotencyKey(ctx, req)
//	    return c.client.CreateAccount(ctx, req)
//	}
func ApplyIdempotencyKey(ctx context.Context, _ interface{}) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	key := ExtractIdempotencyKey(ctx)
	if key == "" {
		return ctx
	}

	// Apply to gRPC metadata header
	ctx = metadata.AppendToOutgoingContext(ctx, "x-idempotency-key", key)

	return ctx
}
