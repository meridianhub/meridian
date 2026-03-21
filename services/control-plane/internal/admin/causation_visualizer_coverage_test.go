package admin

import (
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCausationVisualizer_WithLogger(t *testing.T) {
	logger := slog.Default()
	v := NewCausationVisualizer(nil, nil, logger)
	require.NotNil(t, v)
	assert.Equal(t, logger, v.logger)
}

func TestNewCausationVisualizer_NilLogger_UsesDefault(t *testing.T) {
	v := NewCausationVisualizer(nil, nil, nil)
	require.NotNil(t, v)
	assert.NotNil(t, v.logger)
}

func TestCausationTreeResult_AllFields(t *testing.T) {
	sagaID := uuid.New()
	now := time.Now()
	tree := &saga.CausationTreeNode{
		SagaID:      sagaID,
		SagaName:    "test_saga",
		Status:      "COMPLETED",
		KnowledgeAt: &now,
		Steps: []saga.StepNode{
			{
				Index:  0,
				Name:   "step_0",
				Status: "COMPLETED",
			},
			{
				Index:  1,
				Name:   "step_1",
				Status: "COMPLETED",
			},
		},
	}

	result := &CausationTreeResult{
		Tree:   tree,
		Depth:  5,
		SagaID: sagaID,
	}

	assert.Equal(t, sagaID, result.SagaID)
	assert.Equal(t, 5, result.Depth)
	assert.Equal(t, "test_saga", result.Tree.SagaName)
	assert.Len(t, result.Tree.Steps, 2)
}

func TestPositionInfo_AllFields(t *testing.T) {
	posID := uuid.New()
	info := &PositionInfo{
		PositionID: posID,
		AccountID:  "acct-999",
	}

	assert.Equal(t, posID, info.PositionID)
	assert.Equal(t, "acct-999", info.AccountID)
}

func TestErrorSentinels_Messages(t *testing.T) {
	assert.EqualError(t, ErrNoSagaFound, "no saga found for the given entity")
	assert.EqualError(t, ErrCausationChainTooDeep, "causation chain exceeds maximum depth")
	assert.ErrorIs(t, ErrNoSagaFound, ErrNoSagaFound)
	assert.ErrorIs(t, ErrCausationChainTooDeep, ErrCausationChainTooDeep)
}

func TestCausationVisualizer_FieldAssignment(t *testing.T) {
	v := NewCausationVisualizer(nil, nil, nil)
	assert.Nil(t, v.db)
	assert.Nil(t, v.treeRepo)
	assert.NotNil(t, v.logger)
}
