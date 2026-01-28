package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TestKnowledgeAtInterceptor_ExtractsValidHeader verifies that the interceptor
// extracts a valid x-knowledge-at header and stores it in context
func TestKnowledgeAtInterceptor_ExtractsValidHeader(t *testing.T) {
	t.Parallel()

	interceptor := db.KnowledgeAtInterceptor()

	expectedTime := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	md := metadata.New(map[string]string{
		"x-knowledge-at": expectedTime.Format(time.RFC3339Nano),
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	var capturedCtx context.Context
	handler := func(ctx context.Context, _ interface{}) (interface{}, error) {
		capturedCtx = ctx
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	_, err := interceptor(ctx, nil, info, handler)

	require.NoError(t, err)
	require.NotNil(t, capturedCtx)

	// Verify the timestamp was extracted and stored in context
	knowledgeAt := db.GetKnowledgeAt(capturedCtx)
	assert.Equal(t, expectedTime, knowledgeAt)
}

// TestKnowledgeAtInterceptor_IgnoresMalformedTimestamp verifies that the interceptor
// gracefully ignores malformed timestamps without failing the request
func TestKnowledgeAtInterceptor_IgnoresMalformedTimestamp(t *testing.T) {
	t.Parallel()

	interceptor := db.KnowledgeAtInterceptor()

	md := metadata.New(map[string]string{
		"x-knowledge-at": "not-a-valid-timestamp",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	resp, err := interceptor(ctx, nil, info, handler)

	// Request should succeed despite malformed timestamp
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

// TestKnowledgeAtInterceptor_PassThroughWithoutHeader verifies that the interceptor
// passes through requests without x-knowledge-at header
func TestKnowledgeAtInterceptor_PassThroughWithoutHeader(t *testing.T) {
	t.Parallel()

	interceptor := db.KnowledgeAtInterceptor()

	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	resp, err := interceptor(context.Background(), nil, info, handler)

	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

// TestKnowledgeAtInterceptor_ContextValueRetrievableDownstream verifies that
// the extracted knowledge_at value can be retrieved downstream in the handler chain
func TestKnowledgeAtInterceptor_ContextValueRetrievableDownstream(t *testing.T) {
	t.Parallel()

	interceptor := db.KnowledgeAtInterceptor()

	expectedTime := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	md := metadata.New(map[string]string{
		"x-knowledge-at": expectedTime.Format(time.RFC3339Nano),
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	var retrievedTime time.Time
	handler := func(ctx context.Context, _ interface{}) (interface{}, error) {
		retrievedTime = db.GetKnowledgeAt(ctx)
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	_, err := interceptor(ctx, nil, info, handler)

	require.NoError(t, err)
	assert.Equal(t, expectedTime, retrievedTime)
}

// TestKnowledgeAtInterceptor_CanBeChained verifies that multiple interceptors
// can be chained without conflict
func TestKnowledgeAtInterceptor_CanBeChained(t *testing.T) {
	t.Parallel()

	knowledgeAtInterceptor := db.KnowledgeAtInterceptor()

	// contextKeyTest is a typed context key for testing
	type contextKeyTest string
	const testKey contextKeyTest = "test-key"

	// Simulated second interceptor that adds something to context
	secondInterceptor := func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Add something else to context
		ctx = context.WithValue(ctx, testKey, "test-value")
		return handler(ctx, req)
	}

	expectedTime := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	md := metadata.New(map[string]string{
		"x-knowledge-at": expectedTime.Format(time.RFC3339Nano),
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	var capturedCtx context.Context
	handler := func(ctx context.Context, _ interface{}) (interface{}, error) {
		capturedCtx = ctx
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	// Chain interceptors
	_, err := knowledgeAtInterceptor(ctx, nil, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return secondInterceptor(ctx, req, info, handler)
	})

	require.NoError(t, err)

	// Both values should be present in context
	knowledgeAt := db.GetKnowledgeAt(capturedCtx)
	assert.Equal(t, expectedTime, knowledgeAt)

	testValue := capturedCtx.Value(testKey)
	assert.Equal(t, "test-value", testValue)
}

// TestGetKnowledgeAt_ReturnsContextTimestamp verifies that GetKnowledgeAt
// returns the timestamp when present in context (set via interceptor)
func TestGetKnowledgeAt_ReturnsContextTimestamp(t *testing.T) {
	t.Parallel()

	interceptor := db.KnowledgeAtInterceptor()

	expectedTime := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	md := metadata.New(map[string]string{
		"x-knowledge-at": expectedTime.Format(time.RFC3339Nano),
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	var result time.Time
	handler := func(ctx context.Context, _ interface{}) (interface{}, error) {
		result = db.GetKnowledgeAt(ctx)
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	_, err := interceptor(ctx, nil, info, handler)
	require.NoError(t, err)

	assert.Equal(t, expectedTime, result)
}

// TestGetKnowledgeAt_FallbackToTimeNow verifies that GetKnowledgeAt
// falls back to time.Now() when context lacks timestamp
func TestGetKnowledgeAt_FallbackToTimeNow(t *testing.T) {
	t.Parallel()

	before := time.Now()
	result := db.GetKnowledgeAt(context.Background())
	after := time.Now()

	// Result should be between before and after (allowing for execution time)
	assert.True(t, !result.Before(before) && !result.After(after),
		"Expected result to be current time (between %v and %v), got %v",
		before, after, result)
}

// TestGetKnowledgeAt_FallbackWhenZeroTime verifies that GetKnowledgeAt
// falls back to time.Now() when no header is provided (resulting in zero time)
func TestGetKnowledgeAt_FallbackWhenZeroTime(t *testing.T) {
	t.Parallel()

	// Context without knowledge_at header - should fallback to time.Now()
	before := time.Now()
	result := db.GetKnowledgeAt(context.Background())
	after := time.Now()

	assert.True(t, !result.Before(before) && !result.After(after),
		"Expected fallback to current time when timestamp is zero")
}

// TestGetKnowledgeAt_ConcurrentSafety verifies that concurrent calls to
// GetKnowledgeAt are safe
func TestGetKnowledgeAt_ConcurrentSafety(t *testing.T) {
	t.Parallel()

	interceptor := db.KnowledgeAtInterceptor()

	expectedTime := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	md := metadata.New(map[string]string{
		"x-knowledge-at": expectedTime.Format(time.RFC3339Nano),
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	// Set up context via interceptor first
	var enrichedCtx context.Context
	handler := func(ctx context.Context, _ interface{}) (interface{}, error) {
		enrichedCtx = ctx
		return "ok", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
	_, err := interceptor(ctx, nil, info, handler)
	require.NoError(t, err)

	// Launch multiple goroutines calling GetKnowledgeAt concurrently
	const numGoroutines = 100
	results := make(chan time.Time, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			results <- db.GetKnowledgeAt(enrichedCtx)
		}()
	}

	// Collect all results
	for i := 0; i < numGoroutines; i++ {
		result := <-results
		assert.Equal(t, expectedTime, result)
	}
}
