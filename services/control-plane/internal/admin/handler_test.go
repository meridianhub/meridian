package admin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
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

func TestHandler_MapError_SagaNotFound(t *testing.T) {
	handler := NewHandler(nil, nil)
	id := uuid.New()

	grpcErr := handler.mapError(saga.ErrSagaNotFound, "test", id)
	st, ok := status.FromError(grpcErr)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "saga not found")
}

func TestHandler_MapError_DefaultInternal(t *testing.T) {
	handler := NewHandler(nil, nil)
	id := uuid.New()

	grpcErr := handler.mapError(errors.New("unexpected database error"), "transaction", id)
	st, ok := status.FromError(grpcErr)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to trace causation tree for transaction")
}

func TestTreeNodeToProto_FullTree(t *testing.T) {
	now := time.Now()
	effectiveAt := now.Add(-1 * time.Hour)
	knowledgeAt := now.Add(-30 * time.Minute)
	executedAt := now.Add(-45 * time.Minute)
	stepError := "connection timeout"

	childID := uuid.New()
	childNode := &saga.CausationTreeNode{
		SagaID:   childID,
		SagaName: "child_saga",
		Status:   "COMPLETED",
		Steps:    []saga.StepNode{},
	}

	rootID := uuid.New()
	node := &saga.CausationTreeNode{
		SagaID:      rootID,
		SagaName:    "process_settlement",
		Status:      "FAILED",
		EffectiveAt: &effectiveAt,
		KnowledgeAt: &knowledgeAt,
		FailedStep: &saga.FailedStep{
			Index:         2,
			Error:         "step failed",
			ErrorCategory: "TRANSIENT",
		},
		Steps: []saga.StepNode{
			{
				Index:      0,
				Name:       "create_lien",
				Status:     "COMPLETED",
				ExecutedAt: &executedAt,
			},
			{
				Index:      1,
				Name:       "charge_card",
				Status:     "FAILED",
				ExecutedAt: &executedAt,
				Error:      &stepError,
				ChildSagas: []*saga.CausationTreeNode{childNode},
			},
		},
	}

	proto := treeNodeToProto(node)
	require.NotNil(t, proto)

	assert.Equal(t, rootID.String(), proto.SagaId)
	assert.Equal(t, "process_settlement", proto.SagaName)
	assert.Equal(t, "FAILED", proto.Status)

	// EffectiveAt and KnowledgeAt should be populated
	require.NotNil(t, proto.EffectiveAt)
	assert.Equal(t, effectiveAt.Unix(), proto.EffectiveAt.AsTime().Unix())
	require.NotNil(t, proto.KnowledgeAt)
	assert.Equal(t, knowledgeAt.Unix(), proto.KnowledgeAt.AsTime().Unix())

	// FailedStep should be populated
	require.NotNil(t, proto.FailedStep)
	assert.Equal(t, int32(2), proto.FailedStep.Index)
	assert.Equal(t, "step failed", proto.FailedStep.Error)
	assert.Equal(t, "TRANSIENT", proto.FailedStep.ErrorCategory)

	// Steps should be converted
	require.Len(t, proto.Steps, 2)

	// First step: completed, no error, no children
	assert.Equal(t, int32(0), proto.Steps[0].Index)
	assert.Equal(t, "create_lien", proto.Steps[0].Name)
	assert.Equal(t, "COMPLETED", proto.Steps[0].Status)
	require.NotNil(t, proto.Steps[0].ExecutedAt)
	assert.Empty(t, proto.Steps[0].Error)
	assert.Empty(t, proto.Steps[0].ChildSagas)

	// Second step: failed with error and child saga
	assert.Equal(t, int32(1), proto.Steps[1].Index)
	assert.Equal(t, "charge_card", proto.Steps[1].Name)
	assert.Equal(t, "FAILED", proto.Steps[1].Status)
	assert.Equal(t, "connection timeout", proto.Steps[1].Error)
	require.Len(t, proto.Steps[1].ChildSagas, 1)
	assert.Equal(t, childID.String(), proto.Steps[1].ChildSagas[0].SagaId)
	assert.Equal(t, "child_saga", proto.Steps[1].ChildSagas[0].SagaName)
}

func TestStepNodeToProto_WithAllFields(t *testing.T) {
	executedAt := time.Now()
	errMsg := "validation failed"

	childSagaID := uuid.New()
	step := &saga.StepNode{
		Index:      3,
		Name:       "validate_payment",
		Status:     "FAILED",
		ExecutedAt: &executedAt,
		Error:      &errMsg,
		ChildSagas: []*saga.CausationTreeNode{
			{
				SagaID:   childSagaID,
				SagaName: "compensation_saga",
				Status:   "COMPLETED",
				Steps:    []saga.StepNode{},
			},
		},
	}

	proto := stepNodeToProto(step)
	require.NotNil(t, proto)
	assert.Equal(t, int32(3), proto.Index)
	assert.Equal(t, "validate_payment", proto.Name)
	assert.Equal(t, "FAILED", proto.Status)
	require.NotNil(t, proto.ExecutedAt)
	assert.Equal(t, executedAt.Unix(), proto.ExecutedAt.AsTime().Unix())
	assert.Equal(t, "validation failed", proto.Error)
	require.Len(t, proto.ChildSagas, 1)
	assert.Equal(t, childSagaID.String(), proto.ChildSagas[0].SagaId)
}

func TestTreeNodeToProto_NoOptionalFields(t *testing.T) {
	node := &saga.CausationTreeNode{
		SagaID:   uuid.New(),
		SagaName: "simple_saga",
		Status:   "COMPLETED",
		Steps: []saga.StepNode{
			{
				Index:  0,
				Name:   "step_one",
				Status: "COMPLETED",
			},
		},
	}

	proto := treeNodeToProto(node)
	require.NotNil(t, proto)
	assert.Nil(t, proto.EffectiveAt)
	assert.Nil(t, proto.KnowledgeAt)
	assert.Nil(t, proto.FailedStep)
	require.Len(t, proto.Steps, 1)
	assert.Nil(t, proto.Steps[0].ExecutedAt)
	assert.Empty(t, proto.Steps[0].Error)
	assert.Empty(t, proto.Steps[0].ChildSagas)
}
