package admin

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

func TestNewCausationVisualizer(t *testing.T) {
	v := NewCausationVisualizer(nil, nil, nil)
	require.NotNil(t, v)
	assert.NotNil(t, v.logger)
}

func TestCausationTreeResult_Fields(t *testing.T) {
	sagaID := uuid.New()
	now := time.Now()
	tree := &saga.CausationTreeNode{
		SagaID:   sagaID,
		SagaName: "test_saga",
		Status:   "COMPLETED",
		Steps: []saga.StepNode{
			{
				Index:  0,
				Name:   "step_0",
				Status: "COMPLETED",
			},
		},
		KnowledgeAt: &now,
	}

	result := &CausationTreeResult{
		Tree:   tree,
		Depth:  3,
		SagaID: sagaID,
	}

	assert.Equal(t, sagaID, result.SagaID)
	assert.Equal(t, 3, result.Depth)
	assert.Equal(t, "test_saga", result.Tree.SagaName)
	assert.Len(t, result.Tree.Steps, 1)
}

func TestPositionInfo_Fields(t *testing.T) {
	posID := uuid.New()
	info := &PositionInfo{
		PositionID: posID,
		AccountID:  "acct-001",
	}

	assert.Equal(t, posID, info.PositionID)
	assert.Equal(t, "acct-001", info.AccountID)
}

func TestErrorSentinels(t *testing.T) {
	assert.EqualError(t, ErrNoSagaFound, "no saga found for the given entity")
	assert.EqualError(t, ErrCausationChainTooDeep, "causation chain exceeds maximum depth")
}
