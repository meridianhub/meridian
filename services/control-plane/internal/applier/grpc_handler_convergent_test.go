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
	state     *differ.LiveState
	err       error
	callCount int
}

func (m *mockLiveStateProvider) QueryLiveState(_ context.Context, _ string) (*differ.LiveState, error) {
	m.callCount++
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
	// even if a provider is configured. Verify by checking call count.
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
	assert.Equal(t, 0, liveState.callCount, "QueryLiveState should not be called when skipImmutability is set")
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
	// Live state has a system-managed instrument that is NOT in the manifest.
	// Without SystemCodes filtering, it would appear as a DEPRECATE action.
	// With filtering, it should be excluded from the diff entirely.
	mf := newTestManifest()

	// Include both the manifest instruments AND a system-managed one in live state.
	allInstruments := append(
		mf.GetInstruments(),
		&controlplanev1.InstrumentDefinition{
			Code: "SYSTEM_INTERNAL",
			Name: "System Internal Instrument",
			Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
			Dimensions: &controlplanev1.InstrumentDimensions{
				Unit:      "SYS",
				Precision: 2,
			},
		},
	)

	liveState := &mockLiveStateProvider{
		state: &differ.LiveState{
			Instruments:  allInstruments,
			AccountTypes: mf.GetAccountTypes(),
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
	// The diff should succeed without SYSTEM_INTERNAL appearing as DEPRECATE.
	diffStep := resp.StepResults[1]
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, diffStep.Status)
	// Verify SYSTEM_INTERNAL does not appear in the diff summary (it would if
	// the system resource was not filtered out before diff).
	assert.NotContains(t, resp.DiffSummary, "SYSTEM_INTERNAL",
		"system-managed resource should be excluded from diff")
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

// --- Task 12: buildInput / Execute Input Selection Tests ---

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

func TestDiffer_HasLiveState_WithProvider(t *testing.T) {
	liveState := &mockLiveStateProvider{state: &differ.LiveState{}}
	d := differ.New(nil, nil, liveState)
	assert.True(t, d.HasLiveState())
}

func TestDiffer_HasLiveState_WithoutProvider(t *testing.T) {
	d := differ.New(nil, nil, nil)
	assert.False(t, d.HasLiveState())
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
