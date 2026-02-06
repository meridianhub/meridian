package clients

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// Test helpers for metadata extraction
func ExtractOutgoingMetadata(ctx context.Context) metadata.MD {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return metadata.MD{}
	}
	return md
}

func AppendOutgoingMetadata(ctx context.Context, key, value string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, key, value)
}

func TestPropagateIdempotencyKey(t *testing.T) {
	t.Run("handles nil context gracefully", func(t *testing.T) {
		// Should not panic - using nil to test nil-safety explicitly
		//nolint:staticcheck // Testing nil context handling explicitly
		ctx := PropagateIdempotencyKey(nil, "test-key")
		assert.NotNil(t, ctx, "should return a valid context from nil")
		retrieved := ExtractIdempotencyKey(ctx)
		assert.Equal(t, "test-key", retrieved, "should store key even when starting from nil context")
	})

	t.Run("stores key successfully", func(t *testing.T) {
		ctx := context.Background()
		key := "test-key-123"

		ctx = PropagateIdempotencyKey(ctx, key)

		retrieved := ExtractIdempotencyKey(ctx)
		assert.Equal(t, key, retrieved, "should retrieve the stored key")
	})

	t.Run("preserves context immutability", func(t *testing.T) {
		ctx1 := context.Background()
		key := "original-key"

		ctx2 := PropagateIdempotencyKey(ctx1, key)

		// Original context should not have the key
		assert.Empty(t, ExtractIdempotencyKey(ctx1), "original context should not be modified")
		// New context should have the key
		assert.Equal(t, key, ExtractIdempotencyKey(ctx2), "new context should have the key")
	})

	t.Run("handles empty key", func(t *testing.T) {
		ctx := context.Background()
		ctx = PropagateIdempotencyKey(ctx, "")

		retrieved := ExtractIdempotencyKey(ctx)
		assert.Empty(t, retrieved, "should store and retrieve empty key")
	})

	t.Run("overwrites previous key", func(t *testing.T) {
		ctx := context.Background()
		ctx = PropagateIdempotencyKey(ctx, "first-key")
		ctx = PropagateIdempotencyKey(ctx, "second-key")

		retrieved := ExtractIdempotencyKey(ctx)
		assert.Equal(t, "second-key", retrieved, "should retrieve the most recent key")
	})
}

func TestExtractIdempotencyKey(t *testing.T) {
	t.Run("returns empty string when key not present", func(t *testing.T) {
		ctx := context.Background()

		retrieved := ExtractIdempotencyKey(ctx)
		assert.Empty(t, retrieved, "should return empty string for context without key")
	})

	t.Run("handles nil context gracefully", func(t *testing.T) {
		// Should not panic - using nil to test nil-safety explicitly
		//nolint:staticcheck // Testing nil context handling explicitly
		retrieved := ExtractIdempotencyKey(nil)
		assert.Empty(t, retrieved, "should return empty string for nil context")
	})

	t.Run("retrieves key with special characters", func(t *testing.T) {
		ctx := context.Background()
		key := "saga_abc123_step_5"

		ctx = PropagateIdempotencyKey(ctx, key)

		retrieved := ExtractIdempotencyKey(ctx)
		assert.Equal(t, key, retrieved, "should handle keys with underscores")
	})

	t.Run("handles multiple context values", func(t *testing.T) {
		ctx := context.Background()

		// Add multiple values to context
		type otherKeyType string
		ctx = context.WithValue(ctx, otherKeyType("other"), "other-value")
		ctx = PropagateIdempotencyKey(ctx, "idempotency-key")
		ctx = context.WithValue(ctx, otherKeyType("another"), "another-value")

		// Should still retrieve the correct key
		retrieved := ExtractIdempotencyKey(ctx)
		assert.Equal(t, "idempotency-key", retrieved, "should retrieve key among other context values")

		// Other values should be preserved
		other, ok := ctx.Value(otherKeyType("other")).(string)
		require.True(t, ok)
		assert.Equal(t, "other-value", other)

		another, ok := ctx.Value(otherKeyType("another")).(string)
		require.True(t, ok)
		assert.Equal(t, "another-value", another)
	})
}

func TestApplyIdempotencyKey(t *testing.T) {
	t.Run("adds key to gRPC metadata", func(t *testing.T) {
		ctx := context.Background()
		key := "test-idempotency-key"

		ctx = PropagateIdempotencyKey(ctx, key)
		ctx = ApplyIdempotencyKey(ctx, nil)

		// Verify metadata was added
		md := ExtractOutgoingMetadata(ctx)
		values := md.Get("x-idempotency-key")
		require.Len(t, values, 1, "should have one idempotency key in metadata")
		assert.Equal(t, key, values[0], "metadata should contain the idempotency key")
	})

	t.Run("no-op when context has no key", func(t *testing.T) {
		ctx := context.Background()

		// Should not panic or add metadata
		ctx = ApplyIdempotencyKey(ctx, nil)

		md := ExtractOutgoingMetadata(ctx)
		values := md.Get("x-idempotency-key")
		assert.Empty(t, values, "should not add metadata when no key present")
	})

	t.Run("handles nil context", func(t *testing.T) {
		// Should not panic - using nil to test nil-safety explicitly
		//nolint:staticcheck // Testing nil context handling explicitly
		ctx := ApplyIdempotencyKey(nil, nil)
		assert.NotNil(t, ctx, "should return a valid context")

		md := ExtractOutgoingMetadata(ctx)
		values := md.Get("x-idempotency-key")
		assert.Empty(t, values, "should not add metadata for nil context")
	})

	t.Run("handles empty key", func(t *testing.T) {
		ctx := context.Background()
		ctx = PropagateIdempotencyKey(ctx, "")

		ctx = ApplyIdempotencyKey(ctx, nil)

		// Empty key should not add metadata
		md := ExtractOutgoingMetadata(ctx)
		values := md.Get("x-idempotency-key")
		assert.Empty(t, values, "should not add metadata for empty key")
	})

	t.Run("preserves existing metadata", func(t *testing.T) {
		ctx := context.Background()
		key := "new-idempotency-key"

		// Add existing metadata
		ctx = AppendOutgoingMetadata(ctx, "existing-header", "existing-value")
		ctx = PropagateIdempotencyKey(ctx, key)
		ctx = ApplyIdempotencyKey(ctx, nil)

		md := ExtractOutgoingMetadata(ctx)

		// Check both headers are present
		existingValues := md.Get("existing-header")
		require.Len(t, existingValues, 1)
		assert.Equal(t, "existing-value", existingValues[0], "should preserve existing metadata")

		idempValues := md.Get("x-idempotency-key")
		require.Len(t, idempValues, 1)
		assert.Equal(t, key, idempValues[0], "should add idempotency key metadata")
	})
}
