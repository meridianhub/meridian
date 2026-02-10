package admin

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

func TestHandler_GetCausationTreeForPosition_InvalidUUID(t *testing.T) {
	handler := NewHandler(nil, nil)

	req := &controlplanev1.GetCausationTreeForPositionRequest{
		PositionId: "not-a-uuid",
	}

	_, err := handler.GetCausationTreeForPosition(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid position_id")
}

func TestHandler_GetCausationTreeForTransaction_InvalidUUID(t *testing.T) {
	handler := NewHandler(nil, nil)

	req := &controlplanev1.GetCausationTreeForTransactionRequest{
		TransactionId: "bad-uuid",
	}

	_, err := handler.GetCausationTreeForTransaction(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid transaction_id")
}

func TestHandler_GetCausationTreeForEvent_InvalidUUID(t *testing.T) {
	handler := NewHandler(nil, nil)

	req := &controlplanev1.GetCausationTreeForEventRequest{
		EventId: "invalid",
	}

	_, err := handler.GetCausationTreeForEvent(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid event_id")
}

func TestHandler_MapError_NotFound(t *testing.T) {
	handler := NewHandler(nil, nil)
	id := uuid.New()

	tests := []struct {
		name     string
		err      error
		contains string
	}{
		{
			name:     "no saga found",
			err:      ErrNoSagaFound,
			contains: "no saga found",
		},
		{
			name:     "causation chain too deep",
			err:      ErrCausationChainTooDeep,
			contains: "causation chain too deep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grpcErr := handler.mapError(tt.err, "test", id)

			st, ok := status.FromError(grpcErr)
			require.True(t, ok)
			assert.Equal(t, codes.NotFound, st.Code())
			assert.Contains(t, st.Message(), tt.contains)
		})
	}
}

func TestTreeNodeToProto_Nil(t *testing.T) {
	result := treeNodeToProto(nil)
	assert.Nil(t, result)
}

func TestStepNodeToProto_Nil(t *testing.T) {
	result := stepNodeToProto(nil)
	assert.Nil(t, result)
}
