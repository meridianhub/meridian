package differ

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoOpSafetyChecker_CheckAccountTypeDeletion(t *testing.T) {
	checker := &NoOpSafetyChecker{}
	blocked, err := checker.CheckAccountTypeDeletion(context.Background(), "CURRENT")
	assert.NoError(t, err)
	assert.Nil(t, blocked)
}

func TestNoOpSafetyChecker_CheckInstrumentDeletion(t *testing.T) {
	checker := &NoOpSafetyChecker{}
	blocked, err := checker.CheckInstrumentDeletion(context.Background(), "GBP")
	assert.NoError(t, err)
	assert.Nil(t, blocked)
}

func TestNoOpSafetyChecker_CheckSagaDeletion(t *testing.T) {
	checker := &NoOpSafetyChecker{}
	blocked, err := checker.CheckSagaDeletion(context.Background(), "process_settlement")
	assert.NoError(t, err)
	assert.Nil(t, blocked)
}

func TestNoOpDriftDetector_DetectDrift(t *testing.T) {
	detector := &NoOpDriftDetector{}
	warnings, err := detector.DetectDrift(context.Background(), nil)
	assert.NoError(t, err)
	assert.Nil(t, warnings)
}

func TestDiffPlan_HasBlockedDeletions_Empty(t *testing.T) {
	plan := &DiffPlan{}
	assert.False(t, plan.HasBlockedDeletions())
}

func TestDiffPlan_HasBlockedDeletions_WithEntries(t *testing.T) {
	plan := &DiffPlan{
		BlockedDeletions: []BlockedDeletion{
			{ResourceType: ResourceInstrument, ResourceCode: "GBP", Reason: "in use"},
		},
	}
	assert.True(t, plan.HasBlockedDeletions())
}

func TestDiffPlan_Summary_Empty(t *testing.T) {
	plan := &DiffPlan{}
	assert.Equal(t, "0 to create, 0 to update, 0 to delete, 0 no-change", plan.Summary())
}

func TestDiffPlan_Summary_Mixed(t *testing.T) {
	plan := &DiffPlan{
		Actions: []PlannedAction{
			{Action: ActionCreate},
			{Action: ActionCreate},
			{Action: ActionUpdate},
			{Action: ActionDelete},
			{Action: ActionNoChange},
			{Action: ActionNoChange},
			{Action: ActionNoChange},
		},
	}
	assert.Equal(t, "2 to create, 1 to update, 1 to delete, 3 no-change", plan.Summary())
}

func TestDiffPlan_BlockedDeletionErrors_Empty(t *testing.T) {
	plan := &DiffPlan{}
	assert.Empty(t, plan.BlockedDeletionErrors())
}

func TestDiffPlan_BlockedDeletionErrors_Formatted(t *testing.T) {
	plan := &DiffPlan{
		BlockedDeletions: []BlockedDeletion{
			{ResourceType: ResourceInstrument, ResourceCode: "GBP", Reason: "has active positions"},
			{ResourceType: ResourceAccountType, ResourceCode: "CURRENT", Reason: "in use by customers"},
		},
	}
	errors := plan.BlockedDeletionErrors()
	assert.Len(t, errors, 2)
	assert.Equal(t, "Cannot delete instrument GBP: has active positions", errors[0])
	assert.Equal(t, "Cannot delete account_type CURRENT: in use by customers", errors[1])
}

func TestErrNilManifest(t *testing.T) {
	assert.EqualError(t, ErrNilManifest, "new manifest cannot be nil")
}
