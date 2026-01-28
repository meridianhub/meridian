package clients

import (
	"context"
	"time"

	"google.golang.org/grpc/metadata"
)

// knowledgeAtKeyType is a custom type for the context key to avoid collisions.
type knowledgeAtKeyType string

// knowledgeAtContextKey is the context key used to store the knowledge_at timestamp.
const knowledgeAtContextKey knowledgeAtKeyType = "knowledge_at"

// PropagateKnowledgeAt stores a knowledge_at timestamp in context for
// bi-temporal query routing. Services should use this timestamp instead
// of time.Now() when querying data, enabling deterministic replay.
//
// The timestamp is stored in the context and can be retrieved using ExtractKnowledgeAt.
// To propagate it across gRPC calls, use ApplyKnowledgeAt to add it to metadata headers.
//
// Example:
//
//	ctx := clients.PropagateKnowledgeAt(ctx, saga.KnowledgeAt)
//	ctx = clients.ApplyKnowledgeAt(ctx)  // For gRPC calls
//	result, err := service.Query(ctx, req)
func PropagateKnowledgeAt(ctx context.Context, knowledgeAt time.Time) context.Context {
	return context.WithValue(ctx, knowledgeAtContextKey, knowledgeAt)
}

// ExtractKnowledgeAt retrieves the knowledge_at timestamp from context.
// Returns zero time if not present.
//
// This function is typically used to retrieve the timestamp stored by PropagateKnowledgeAt
// or extracted from gRPC metadata by an interceptor.
//
// Example:
//
//	knowledgeAt := clients.ExtractKnowledgeAt(ctx)
//	if !knowledgeAt.IsZero() {
//	    // Use historical timestamp for queries
//	}
func ExtractKnowledgeAt(ctx context.Context) time.Time {
	if ts, ok := ctx.Value(knowledgeAtContextKey).(time.Time); ok {
		return ts
	}
	return time.Time{}
}

// ApplyKnowledgeAt adds the knowledge_at timestamp to gRPC metadata headers.
// Services can intercept this header (using KnowledgeAtInterceptor) to override
// time.Now() for bi-temporal queries.
//
// If no knowledge_at timestamp is present in the context (or if it's zero),
// this function returns the context unchanged (no-op).
//
// The timestamp is formatted using RFC3339Nano for precision and transmitted
// as the "x-knowledge-at" gRPC metadata header.
//
// Example:
//
//	ctx = clients.PropagateKnowledgeAt(ctx, saga.KnowledgeAt)
//	ctx = clients.ApplyKnowledgeAt(ctx)
//	result, err := service.Query(ctx, req)  // Header propagated automatically
func ApplyKnowledgeAt(ctx context.Context) context.Context {
	knowledgeAt := ExtractKnowledgeAt(ctx)
	if knowledgeAt.IsZero() {
		return ctx
	}

	// RFC3339Nano format for precision
	return metadata.AppendToOutgoingContext(ctx,
		"x-knowledge-at",
		knowledgeAt.Format(time.RFC3339Nano))
}
