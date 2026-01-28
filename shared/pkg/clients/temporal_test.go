package clients_test

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// TestPropagateKnowledgeAt_StoresAndRetrievesTimestamp verifies that PropagateKnowledgeAt stores
// a timestamp that can be retrieved with ExtractKnowledgeAt
func TestPropagateKnowledgeAt_StoresAndRetrievesTimestamp(t *testing.T) {
	t.Parallel()

	knowledgeAt := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	ctx := clients.PropagateKnowledgeAt(context.Background(), knowledgeAt)

	result := clients.ExtractKnowledgeAt(ctx)

	assert.Equal(t, knowledgeAt, result)
}

// TestExtractKnowledgeAt_ReturnsZeroTimeWhenNotPresent verifies that ExtractKnowledgeAt
// returns zero time when no knowledge_at timestamp is in context
func TestExtractKnowledgeAt_ReturnsZeroTimeWhenNotPresent(t *testing.T) {
	t.Parallel()

	result := clients.ExtractKnowledgeAt(context.Background())

	assert.True(t, result.IsZero(), "Expected zero time when knowledge_at not present")
}

// TestApplyKnowledgeAt_AddsHeaderWithRFC3339NanoFormat verifies that ApplyKnowledgeAt
// adds x-knowledge-at header to outgoing gRPC metadata with RFC3339Nano format
func TestApplyKnowledgeAt_AddsHeaderWithRFC3339NanoFormat(t *testing.T) {
	t.Parallel()

	knowledgeAt := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	ctx := clients.PropagateKnowledgeAt(context.Background(), knowledgeAt)
	ctx = clients.ApplyKnowledgeAt(ctx)

	// Extract metadata from context
	md, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok, "Expected outgoing metadata to be present")

	values := md.Get("x-knowledge-at")
	require.Len(t, values, 1, "Expected exactly one x-knowledge-at header")

	// Verify format is RFC3339Nano
	expectedFormat := knowledgeAt.Format(time.RFC3339Nano)
	assert.Equal(t, expectedFormat, values[0])

	// Verify it can be parsed back
	parsed, err := time.Parse(time.RFC3339Nano, values[0])
	require.NoError(t, err)
	assert.Equal(t, knowledgeAt, parsed)
}

// TestApplyKnowledgeAt_NoOpWhenZeroTime verifies that ApplyKnowledgeAt does not
// add header when knowledge_at is zero time
func TestApplyKnowledgeAt_NoOpWhenZeroTime(t *testing.T) {
	t.Parallel()

	ctx := clients.ApplyKnowledgeAt(context.Background())

	// Should not have added metadata
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		values := md.Get("x-knowledge-at")
		assert.Empty(t, values, "Expected no x-knowledge-at header when timestamp is zero")
	}
}

// TestApplyKnowledgeAt_NoOpWhenNotPropagated verifies that ApplyKnowledgeAt is a no-op
// when knowledge_at was never propagated to context
func TestApplyKnowledgeAt_NoOpWhenNotPropagated(t *testing.T) {
	t.Parallel()

	ctx := clients.ApplyKnowledgeAt(context.Background())

	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		values := md.Get("x-knowledge-at")
		assert.Empty(t, values, "Expected no x-knowledge-at header when not propagated")
	}
}

// TestContextImmutability_PropagateDoesNotMutateOriginal verifies that PropagateKnowledgeAt
// does not mutate the original context
func TestContextImmutability_PropagateDoesNotMutateOriginal(t *testing.T) {
	t.Parallel()

	originalCtx := context.Background()
	knowledgeAt := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)

	_ = clients.PropagateKnowledgeAt(originalCtx, knowledgeAt)

	// Original context should not have knowledge_at
	result := clients.ExtractKnowledgeAt(originalCtx)
	assert.True(t, result.IsZero(), "Original context should not be mutated")
}

// TestContextImmutability_ApplyDoesNotMutateOriginal verifies that ApplyKnowledgeAt
// does not mutate the original context
func TestContextImmutability_ApplyDoesNotMutateOriginal(t *testing.T) {
	t.Parallel()

	knowledgeAt := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	originalCtx := clients.PropagateKnowledgeAt(context.Background(), knowledgeAt)

	_ = clients.ApplyKnowledgeAt(originalCtx)

	// Original context should not have outgoing metadata added
	md, ok := metadata.FromOutgoingContext(originalCtx)
	if ok {
		values := md.Get("x-knowledge-at")
		assert.Empty(t, values, "Original context should not be mutated")
	}
}

// TestApplyKnowledgeAt_PreservesExistingMetadata verifies that ApplyKnowledgeAt
// preserves existing outgoing metadata
func TestApplyKnowledgeAt_PreservesExistingMetadata(t *testing.T) {
	t.Parallel()

	// Start with context that already has metadata
	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-tenant-id", "tenant-123")

	knowledgeAt := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	ctx = clients.PropagateKnowledgeAt(ctx, knowledgeAt)
	ctx = clients.ApplyKnowledgeAt(ctx)

	md, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok)

	// Both headers should be present
	tenantValues := md.Get("x-tenant-id")
	require.Len(t, tenantValues, 1)
	assert.Equal(t, "tenant-123", tenantValues[0])

	knowledgeAtValues := md.Get("x-knowledge-at")
	require.Len(t, knowledgeAtValues, 1)
	assert.NotEmpty(t, knowledgeAtValues[0])
}
