package service_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdatePosition_ReturnsUnimplemented verifies that UpdatePosition always returns UNIMPLEMENTED.
func TestUpdatePosition_ReturnsUnimplemented(t *testing.T) {
	// Create a minimal service - these blocked methods don't need any dependencies
	svc, err := service.NewPositionKeepingService(
		new(MockRepository),
		new(MockMeasurementRepository),
		domain.NewInMemoryEventPublisher(),
		new(MockIdempotencyService),
		newTestOutboxPublisher(t),
	)
	require.NoError(t, err)

	ctx := context.Background()

	// Call UpdatePosition with any request
	resp, err := svc.UpdatePosition(ctx, &positionkeepingv1.UpdatePositionRequest{
		PositionId: "550e8400-e29b-41d4-a716-446655440000",
		NewAmount:  "100.00",
	})

	// Should always return nil response and UNIMPLEMENTED error
	assert.Nil(t, resp)
	require.Error(t, err)

	// Verify it's the correct gRPC status code
	st, ok := status.FromError(err)
	require.True(t, ok, "error should be a gRPC status error")
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "append-only")
	assert.Contains(t, st.Message(), "create a new position record")
}

// TestMergePositions_ReturnsUnimplemented verifies that MergePositions always returns UNIMPLEMENTED.
func TestMergePositions_ReturnsUnimplemented(t *testing.T) {
	// Create a minimal service - these blocked methods don't need any dependencies
	svc, err := service.NewPositionKeepingService(
		new(MockRepository),
		new(MockMeasurementRepository),
		domain.NewInMemoryEventPublisher(),
		new(MockIdempotencyService),
		newTestOutboxPublisher(t),
	)
	require.NoError(t, err)

	ctx := context.Background()

	// Call MergePositions with any request
	resp, err := svc.MergePositions(ctx, &positionkeepingv1.MergePositionsRequest{
		AccountId:      "ACC-001",
		InstrumentCode: "GBP",
		BucketKey:      "default",
	})

	// Should always return nil response and UNIMPLEMENTED error
	assert.Nil(t, resp)
	require.Error(t, err)

	// Verify it's the correct gRPC status code
	st, ok := status.FromError(err)
	require.True(t, ok, "error should be a gRPC status error")
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "append-only")
	assert.Contains(t, st.Message(), "read-time aggregation")
}

// TestUpdatePosition_NilRequest_StillReturnsUnimplemented verifies behavior with nil request.
func TestUpdatePosition_NilRequest_StillReturnsUnimplemented(t *testing.T) {
	svc, err := service.NewPositionKeepingService(
		new(MockRepository),
		new(MockMeasurementRepository),
		domain.NewInMemoryEventPublisher(),
		new(MockIdempotencyService),
		newTestOutboxPublisher(t),
	)
	require.NoError(t, err)

	ctx := context.Background()

	// Call with nil request - should still return UNIMPLEMENTED (not panic)
	resp, err := svc.UpdatePosition(ctx, nil)

	assert.Nil(t, resp)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

// TestMergePositions_NilRequest_StillReturnsUnimplemented verifies behavior with nil request.
func TestMergePositions_NilRequest_StillReturnsUnimplemented(t *testing.T) {
	svc, err := service.NewPositionKeepingService(
		new(MockRepository),
		new(MockMeasurementRepository),
		domain.NewInMemoryEventPublisher(),
		new(MockIdempotencyService),
		newTestOutboxPublisher(t),
	)
	require.NoError(t, err)

	ctx := context.Background()

	// Call with nil request - should still return UNIMPLEMENTED (not panic)
	resp, err := svc.MergePositions(ctx, nil)

	assert.Nil(t, resp)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}
