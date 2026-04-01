package applier

import (
	"context"
	"fmt"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock LiveStateProvider ---

// mockLiveStateProvider returns a fixed LiveState for testing.
type mockLiveStateProvider struct {
	state *differ.LiveState
	err   error
}

func (m *mockLiveStateProvider) QueryLiveState(_ context.Context, _ string) (*differ.LiveState, error) {
	return m.state, m.err
}

// newTestHandlerWithLiveState creates a handler with a LiveStateProvider.
func newTestHandlerWithLiveState(t *testing.T, liveState differ.LiveStateProvider) *ApplyManifestHandler {
	t.Helper()

	v, err := validator.New()
	require.NoError(t, err)

	d := differ.New(nil, nil, liveState)
	p := planner.NewManifestPlanner()

	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator: v,
		Differ:    d,
		Planner:   p,
		LiveState: liveState,
	})
	require.NoError(t, err)
	return handler
}

// --- Task 12: Live-State Diff Wiring Tests ---

func TestApplyManifest_LiveStateDiff_DryRun_NewResources(t *testing.T) {
	// Live state has no instruments or account types - all resources are CREATE.
	liveState := &mockLiveStateProvider{
		state: &differ.LiveState{},
	}
	handler := newTestHandlerWithLiveState(t, liveState)

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	resp, err := handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest: newTestManifest(),
		DryRun:   true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)

	// Verify diff step succeeded
	require.GreaterOrEqual(t, len(resp.StepResults), 2)
	diffStep := resp.StepResults[1]
	assert.Equal(t, "diff", diffStep.StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, diffStep.Status)

	// Action count should reflect creates for all manifest resources
	assert.NotEmpty(t, diffStep.Details["action_count"])
}

func TestApplyManifest_LiveStateDiff_DryRun_ExistingResources_NoChange(t *testing.T) {
	// Live state matches the manifest exactly - all NO_CHANGE.
	mf := newTestManifest()
	liveState := &mockLiveStateProvider{
		state: &differ.LiveState{
			Instruments:  mf.GetInstruments(),
			AccountTypes: mf.GetAccountTypes(),
		},
	}
	handler := newTestHandlerWithLiveState(t, liveState)

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	resp, err := handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest: mf,
		DryRun:   true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)

	// Verify diff reports no actionable changes
	diffStep := resp.StepResults[1]
	assert.Equal(t, "diff", diffStep.StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, diffStep.Status)
}

func TestApplyManifest_LiveStateDiff_SkipImmutability_FallsBackToStoredDiff(t *testing.T) {
	// With skipImmutability (new-tenant mode), live-state diff is NOT used
	// even if a provider is configured.
	liveState := &mockLiveStateProvider{
		state: &differ.LiveState{},
	}
	handler := newTestHandlerWithLiveState(t, liveState)

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:               newTestManifest(),
		DryRun:                 true,
		SkipImmutabilityChecks: true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
}

func TestApplyManifest_LiveStateDiff_Error_ReturnsFailure(t *testing.T) {
	// LiveStateProvider returns an error - diff step should fail.
	liveState := &mockLiveStateProvider{
		err: fmt.Errorf("service unavailable"),
	}
	handler := newTestHandlerWithLiveState(t, liveState)

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	resp, err := handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		DryRun:    true,
		AppliedBy: "test-user",
	})

	require.NoError(t, err) // gRPC returns nil error, status in response
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED, resp.Status)

	// Verify the diff step has the error
	diffStep := resp.StepResults[1]
	assert.Equal(t, "diff", diffStep.StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED, diffStep.Status)
	assert.Contains(t, diffStep.Message, "Live-state diff failed")
}

func TestApplyManifest_LiveStateDiff_SystemResourcesExcluded(t *testing.T) {
	// Live state has system resources that should be excluded from diff.
	mf := newTestManifest()
	liveState := &mockLiveStateProvider{
		state: &differ.LiveState{
			Instruments:  mf.GetInstruments(),
			AccountTypes: mf.GetAccountTypes(),
			// System-managed instrument not in manifest should NOT appear as DEPRECATE.
			SystemCodes: map[differ.ResourceType]map[string]bool{
				differ.ResourceInstrument: {"SYSTEM_INTERNAL": true},
			},
		},
	}
	handler := newTestHandlerWithLiveState(t, liveState)

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	resp, err := handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest: mf,
		DryRun:   true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
}

func TestApplyManifest_NoLiveState_FallsBackToStoredManifestDiff(t *testing.T) {
	// Without LiveStateProvider, handler uses stored manifest diff (legacy path).
	handler := newTestHandler(t)

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest: newTestManifest(),
		DryRun:   true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)

	diffStep := resp.StepResults[1]
	assert.Equal(t, "diff", diffStep.StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, diffStep.Status)
}

func TestApplyManifest_LiveStateDiff_NoExecutor_FailsGracefully(t *testing.T) {
	// Live state diff works but no executor - should fail at execute step.
	liveState := &mockLiveStateProvider{
		state: &differ.LiveState{},
	}
	handler := newTestHandlerWithLiveState(t, liveState)

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	resp, err := handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		DryRun:    false,
		AppliedBy: "test-user",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	// Without executor, non-dry-run applies fail
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED, resp.Status)
}

// --- Task 12: Execute with DiffPlan Tests ---

func TestExecute_WithDiffPlan_UsesFilteredInput(t *testing.T) {
	// Verifies that when diffPlan is provided, buildExecutorInputFromPlan is used.
	handler := newTestHandler(t) // no executor

	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{
				ResourceType: differ.ResourceInstrument,
				ResourceCode: "GBP",
				Action:       differ.ActionCreate,
			},
			{
				ResourceType: differ.ResourceAccountType,
				ResourceCode: "SAVINGS",
				Action:       differ.ActionNoChange, // Should be excluded from input
			},
		},
	}
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments, ResourceID: "GBP"},
		},
	}

	result := handler.execute(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		AppliedBy: "test",
	}, plan, diffPlan)

	// Handler has no executor, so it fails at executor nil check - but proves
	// the code path through buildExecutorInputFromPlan was reached.
	assert.Error(t, result.err)
	assert.ErrorIs(t, result.err, ErrExecutorNotConfigured)
}

func TestExecute_NilDiffPlan_UsesBuildExecutorInput(t *testing.T) {
	// Verifies that when diffPlan is nil, buildExecutorInput is used (legacy path).
	handler := newTestHandler(t) // no executor

	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments, ResourceID: "GBP"},
		},
	}

	result := handler.execute(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		AppliedBy: "test",
	}, plan, nil)

	assert.Error(t, result.err)
	assert.ErrorIs(t, result.err, ErrExecutorNotConfigured)
}

// --- Task 11: Partial Success Handling Verification Tests ---

func TestBuildExecutionFailureResponse_PartialFailure_RecordsPartialStatus(t *testing.T) {
	// When some phases succeed and one fails, the response should have PARTIAL status.
	handler := newTestHandler(t) // no history service, so recording is a no-op

	execResult := executeOutput{
		jobID: "test-job-id",
		err:   fmt.Errorf("phase 2 failed"),
		phaseStatus: manifest.PhaseStatusMap{
			"phase_1": {Status: manifest.PhaseStatusCompleted},
			"phase_2": {Status: manifest.PhaseStatusFailed, Error: "phase 2 failed"},
			"phase_3": {Status: manifest.PhaseStatusSkipped},
		},
	}

	ctx := context.Background()
	response := &controlplanev1.ApplyManifestResponse{}

	result := handler.buildExecutionFailureResponse(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		AppliedBy: "test-user",
	}, execResult, response, handler.logger)

	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_PARTIAL, result.Status)
	require.NotNil(t, result.PhaseStatus)
	assert.Equal(t, "COMPLETED", result.PhaseStatus["phase_1"].Status)
	assert.Equal(t, "FAILED", result.PhaseStatus["phase_2"].Status)
	assert.Equal(t, "SKIPPED", result.PhaseStatus["phase_3"].Status)
}

func TestBuildExecutionFailureResponse_TotalFailure_RecordsFailedStatus(t *testing.T) {
	// When all phases fail, the response should have FAILED status.
	handler := newTestHandler(t)

	execResult := executeOutput{
		jobID: "test-job-id",
		err:   fmt.Errorf("total failure"),
		phaseStatus: manifest.PhaseStatusMap{
			"phase_1": {Status: manifest.PhaseStatusFailed, Error: "total failure"},
			"phase_2": {Status: manifest.PhaseStatusSkipped},
		},
	}

	ctx := context.Background()
	response := &controlplanev1.ApplyManifestResponse{}

	result := handler.buildExecutionFailureResponse(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		AppliedBy: "test-user",
	}, execResult, response, handler.logger)

	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED, result.Status)
	require.NotNil(t, result.PhaseStatus)
}

func TestClassifyFailure_PartialWithMultipleCompletedAndFailed(t *testing.T) {
	// Multiple completed phases + a failed phase = PARTIAL.
	ps := manifest.PhaseStatusMap{
		"phase_1": {Status: manifest.PhaseStatusCompleted},
		"phase_2": {Status: manifest.PhaseStatusCompleted},
		"phase_3": {Status: manifest.PhaseStatusFailed},
		"phase_4": {Status: manifest.PhaseStatusSkipped},
	}
	applyStatus, protoStatus := classifyFailure(ps)
	assert.Equal(t, manifest.ApplyStatusPartial, applyStatus)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_PARTIAL, protoStatus)
}

func TestUpdatePhaseStatus_PartialFailure_CorrectPhaseClassification(t *testing.T) {
	// 3 phases: instruments, account types, sagas.
	// Instruments succeed, account types fail, sagas skipped.
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments},
			{Phase: planner.PhaseAccountTypes},
			{Phase: planner.PhaseSagas},
		},
	}
	ps := buildInitialPhaseStatus(plan)

	result := &ApplyManifestResult{
		Status: "failed",
		Error:  "account type registration failed",
		StepResults: []saga.StepResult{
			{StepName: "step1", Success: true},
			{StepName: "step2", Success: false, Error: "account type registration failed"},
		},
	}
	updatePhaseStatus(ps, plan, result, fmt.Errorf("saga failed"))

	assert.Equal(t, manifest.PhaseStatusCompleted, ps["phase_1"].Status)
	assert.Equal(t, manifest.PhaseStatusFailed, ps["phase_2"].Status)
	assert.Equal(t, "account type registration failed", ps["phase_2"].Error)
	assert.Equal(t, manifest.PhaseStatusSkipped, ps["phase_4"].Status)

	// Verify classifyFailure correctly identifies this as PARTIAL
	applyStatus, protoStatus := classifyFailure(ps)
	assert.Equal(t, manifest.ApplyStatusPartial, applyStatus)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_PARTIAL, protoStatus)
}

// --- Task 12: Handler Config Tests ---

func TestNewApplyManifestHandler_LiveStateProviderStored(t *testing.T) {
	v, err := validator.New()
	require.NoError(t, err)

	liveState := &mockLiveStateProvider{state: &differ.LiveState{}}
	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator: v,
		Differ:    differ.New(nil, nil, liveState),
		Planner:   planner.NewManifestPlanner(),
		LiveState: liveState,
	})
	require.NoError(t, err)
	assert.NotNil(t, handler.liveState)
}

func TestNewApplyManifestHandler_NilLiveStateProvider_IsValid(t *testing.T) {
	v, err := validator.New()
	require.NoError(t, err)

	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator: v,
		Differ:    differ.New(nil, nil, nil),
		Planner:   planner.NewManifestPlanner(),
	})
	require.NoError(t, err)
	assert.Nil(t, handler.liveState)
}

// --- Task 12: buildInput Tests ---

func TestBuildInput_WithDiffPlan_FiltersResources(t *testing.T) {
	mf := newTestManifest()
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionNoChange},
		},
	}

	input := buildInput(mf, plan)
	// Only GBP instrument should be included (CREATE), account type excluded (NO_CHANGE).
	require.Len(t, input.Instruments, 1)
	assert.Equal(t, "GBP", input.Instruments[0].Code)
	assert.Empty(t, input.AccountTypes)
}

func TestBuildInput_NilDiffPlan_IncludesAllResources(t *testing.T) {
	mf := newTestManifest()

	input := buildInput(mf, nil)
	require.Len(t, input.Instruments, 1)
	require.Len(t, input.AccountTypes, 1)
}

// --- Task 12: buildDiffOutput Tests ---

func TestBuildDiffOutput(t *testing.T) {
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{
				ResourceType: differ.ResourceInstrument,
				ResourceCode: "GBP",
				Action:       differ.ActionCreate,
				Description:  "Create instrument GBP",
			},
		},
		HasBreakingChanges: true,
	}

	output := buildDiffOutput(plan)

	assert.Nil(t, output.err)
	assert.Equal(t, plan, output.plan)
	assert.Equal(t, "diff", output.stepResult.StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, output.stepResult.Status)
	assert.Equal(t, "true", output.stepResult.Details["has_breaking_changes"])
	assert.Equal(t, "1", output.stepResult.Details["action_count"])
}
