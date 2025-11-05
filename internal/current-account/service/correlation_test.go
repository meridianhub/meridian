package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func TestExtractCorrelationID_ExistingID(t *testing.T) {
	t.Parallel()

	// Create context with correlation ID in metadata
	md := metadata.New(map[string]string{
		CorrelationIDKey: "test-correlation-123",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	correlationID := ExtractCorrelationID(ctx)

	assert.Equal(t, "test-correlation-123", correlationID)
}

func TestExtractCorrelationID_NoMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	correlationID := ExtractCorrelationID(ctx)

	// Should generate a new UUID
	assert.NotEmpty(t, correlationID)
	assert.Equal(t, 36, len(correlationID), "UUID should be 36 characters")
}

func TestExtractCorrelationID_EmptyID(t *testing.T) {
	t.Parallel()

	// Create context with empty correlation ID
	md := metadata.New(map[string]string{
		CorrelationIDKey: "",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	correlationID := ExtractCorrelationID(ctx)

	// Should generate a new UUID
	assert.NotEmpty(t, correlationID)
	assert.Equal(t, 36, len(correlationID))
}

func TestExtractCorrelationID_MissingKey(t *testing.T) {
	t.Parallel()

	// Create context with metadata but no correlation ID key
	md := metadata.New(map[string]string{
		"other-key": "other-value",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	correlationID := ExtractCorrelationID(ctx)

	// Should generate a new UUID
	assert.NotEmpty(t, correlationID)
	assert.Equal(t, 36, len(correlationID))
}

func TestPropagateCorrelationID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	testCorrelationID := "test-correlation-456"

	// Propagate correlation ID
	newCtx := PropagateCorrelationID(ctx, testCorrelationID)

	// Extract outgoing metadata
	md, ok := metadata.FromOutgoingContext(newCtx)
	assert.True(t, ok, "outgoing metadata should exist")

	values := md.Get(CorrelationIDKey)
	assert.Len(t, values, 1, "should have one correlation ID")
	assert.Equal(t, testCorrelationID, values[0])
}

func TestPropagateCorrelationID_PreservesExistingMetadata(t *testing.T) {
	t.Parallel()

	// Create context with existing outgoing metadata
	ctx := metadata.AppendToOutgoingContext(context.Background(), "existing-key", "existing-value")

	testCorrelationID := "test-correlation-789"

	// Propagate correlation ID
	newCtx := PropagateCorrelationID(ctx, testCorrelationID)

	// Extract outgoing metadata
	md, ok := metadata.FromOutgoingContext(newCtx)
	assert.True(t, ok)

	// Verify both keys exist
	correlationValues := md.Get(CorrelationIDKey)
	assert.Len(t, correlationValues, 1)
	assert.Equal(t, testCorrelationID, correlationValues[0])

	existingValues := md.Get("existing-key")
	assert.Len(t, existingValues, 1)
	assert.Equal(t, "existing-value", existingValues[0])
}

func TestGenerateCorrelationID_Uniqueness(t *testing.T) {
	t.Parallel()

	// Generate multiple correlation IDs and verify they're unique
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateCorrelationID()
		assert.NotEmpty(t, id)
		assert.False(t, ids[id], "correlation ID should be unique")
		ids[id] = true
	}
}

func TestCorrelationID_RoundTrip(t *testing.T) {
	t.Parallel()

	// Simulate a full round-trip:
	// 1. Extract correlation ID from incoming request
	// 2. Propagate it to outgoing call
	// 3. Verify it's preserved

	originalID := "original-correlation-id"

	// Step 1: Incoming context (simulating incoming gRPC request)
	incomingMD := metadata.New(map[string]string{
		CorrelationIDKey: originalID,
	})
	incomingCtx := metadata.NewIncomingContext(context.Background(), incomingMD)

	// Extract correlation ID
	extractedID := ExtractCorrelationID(incomingCtx)
	assert.Equal(t, originalID, extractedID)

	// Step 2: Propagate to outgoing context (simulating outgoing gRPC call)
	outgoingCtx := PropagateCorrelationID(context.Background(), extractedID)

	// Step 3: Verify in outgoing metadata
	outgoingMD, ok := metadata.FromOutgoingContext(outgoingCtx)
	assert.True(t, ok)

	outgoingValues := outgoingMD.Get(CorrelationIDKey)
	assert.Len(t, outgoingValues, 1)
	assert.Equal(t, originalID, outgoingValues[0])
}
