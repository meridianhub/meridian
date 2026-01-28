package db

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

// knowledgeAtKey is the context key for storing knowledge_at timestamp.
const knowledgeAtKey contextKey = "knowledge_at"

// KnowledgeAtInterceptor extracts the x-knowledge-at header from incoming gRPC metadata
// and stores it in the request context. This enables bi-temporal query routing where
// services can query historical data as it existed at a specific point in time.
//
// The interceptor:
//   - Extracts the "x-knowledge-at" header from incoming gRPC metadata
//   - Parses it as RFC3339Nano timestamp
//   - Stores the parsed timestamp in context with key "knowledge_at"
//   - Gracefully handles malformed or missing timestamps (passes request through)
//
// Services should use GetKnowledgeAt(ctx) to retrieve the timestamp and use it
// instead of time.Now() for data access queries to enable deterministic saga replay.
//
// Example usage in service initialization:
//
//	server := grpc.NewServer(
//	    grpc.ChainUnaryInterceptor(
//	        db.KnowledgeAtInterceptor(),
//	        // ... other interceptors
//	    ),
//	)
func KnowledgeAtInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if values := md.Get("x-knowledge-at"); len(values) > 0 {
				if knowledgeAt, err := time.Parse(time.RFC3339Nano, values[0]); err == nil {
					// Store parsed timestamp in context for downstream use
					ctx = context.WithValue(ctx, knowledgeAtKey, knowledgeAt)
				}
				// Silently ignore parse errors - the request should proceed with current time
			}
		}
		return handler(ctx, req)
	}
}

// GetKnowledgeAt returns the knowledge_at timestamp from context,
// falling back to time.Now() if not present or zero.
//
// This function provides the abstraction layer for bi-temporal queries:
//   - During saga replay with historical timestamp: returns the knowledge_at from context
//   - During normal execution: returns time.Now()
//
// Services should use this function instead of time.Now() for all timestamp-sensitive
// data access operations to enable deterministic saga replay.
//
// Example usage in a repository:
//
//	func (r *Repository) GetActivePositions(ctx context.Context) ([]Position, error) {
//	    queryTime := db.GetKnowledgeAt(ctx)  // Historical or current time
//	    return r.db.Query(`
//	        SELECT * FROM positions
//	        WHERE valid_from <= $1 AND (valid_to IS NULL OR valid_to > $1)
//	    `, queryTime)
//	}
func GetKnowledgeAt(ctx context.Context) time.Time {
	if ts, ok := ctx.Value(knowledgeAtKey).(time.Time); ok && !ts.IsZero() {
		return ts
	}
	return time.Now()
}
