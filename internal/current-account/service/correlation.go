// Package service implements gRPC services for the current account domain
package service

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"
)

const (
	// CorrelationIDKey is the metadata key for correlation IDs
	CorrelationIDKey = "x-correlation-id"
)

// ExtractCorrelationID extracts correlation ID from incoming gRPC metadata
// If no correlation ID exists, generates a new one
func ExtractCorrelationID(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return generateCorrelationID()
	}

	values := md.Get(CorrelationIDKey)
	if len(values) == 0 || values[0] == "" {
		return generateCorrelationID()
	}

	return values[0]
}

// PropagateCorrelationID adds correlation ID to outgoing gRPC metadata
func PropagateCorrelationID(ctx context.Context, correlationID string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, CorrelationIDKey, correlationID)
}

// generateCorrelationID creates a new correlation ID
func generateCorrelationID() string {
	return uuid.New().String()
}
