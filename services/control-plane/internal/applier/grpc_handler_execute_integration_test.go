package applier

import (
	"context"
	"errors"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errBoom is a sentinel error used to force a saga handler failure in tests.
var errBoom = errors.New("instrument registration boom")

// newDBBackedHandler builds an ApplyManifestHandler with a real DB-backed
// executor (real saga runner + noop service handlers) but no history service
// or version store. Suitable for exercising the live execute/recordAndFinalize
// path. Returns the handler and the underlying pool for assertions.
func newDBBackedHandler(t *testing.T) *ApplyManifestHandler {
	t.Helper()

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))

	script, version, err := loadEmbeddedApplyManifest()
	require.NoError(t, err)
	insertPlatformSaga(t, pool, version, script)

	deps := &HandlerDependencies{
		ReferenceData:      &noopReferenceData{},
		InternalAccount:    &noopInternalAccount{},
		MarketInformation:  &noopMarketInformation{},
		Party:              &noopParty{},
		OperationalGateway: &noopOperationalGateway{},
	}
	executor := newExecutorWithRunner(t, pool, deps)

	v, err := validator.New()
	require.NoError(t, err)

	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator: v,
		Differ:    differ.New(nil, nil, nil),
		Planner:   planner.NewManifestPlanner(),
		Executor:  executor,
	})
	require.NoError(t, err)
	return handler
}

// TestExecute_RealExecutor_Success drives the handler.execute success branch
// against a real DB-backed executor: a valid plan applies cleanly and the
// step result reports SUCCESS with a saga_execution_id detail.
func TestExecute_RealExecutor_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	handler := newDBBackedHandler(t)

	execPlan := &planner.ExecutionPlan{
		TenantID:        "org_exec",
		ManifestVersion: "1.0",
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments, ResourceID: "GBP"},
		},
	}

	req := &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		AppliedBy: "tester",
	}

	result := handler.execute(context.Background(), req, execPlan, nil)

	require.NoError(t, result.err)
	assert.NotEmpty(t, result.jobID)
	require.NotNil(t, result.stepResult)
	assert.Equal(t, "execute", result.stepResult.StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, result.stepResult.Status)
	assert.Contains(t, result.stepResult.Details, "saga_execution_id")
	assert.NotEmpty(t, result.phaseStatus)
}

// newFailingDBBackedHandler builds a handler whose executor runs a saga that
// fails during Phase 10 (register_instrument returns an error), so the apply
// reports a failure status. Exercises the execute error branch and
// buildExecutionFailureResponse.
func newFailingDBBackedHandler(t *testing.T) *ApplyManifestHandler {
	t.Helper()

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))

	script, version, err := loadEmbeddedApplyManifest()
	require.NoError(t, err)
	insertPlatformSaga(t, pool, version, script)

	failingRefData := &mockReferenceData{
		registerInstrumentFn: func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
			return nil, errBoom
		},
	}
	deps := &HandlerDependencies{
		ReferenceData:   failingRefData,
		InternalAccount: &noopInternalAccount{},
	}
	executor := newExecutorWithRunner(t, pool, deps)

	v, err := validator.New()
	require.NoError(t, err)

	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator: v,
		Differ:    differ.New(nil, nil, nil),
		Planner:   planner.NewManifestPlanner(),
		Executor:  executor,
	})
	require.NoError(t, err)
	return handler
}

// TestApplyManifest_RealExecutor_ExecutionFails drives the full non-dry-run
// pipeline where the saga fails: execute returns an error and the handler maps
// it through buildExecutionFailureResponse to a failure status with phase
// status populated.
func TestApplyManifest_RealExecutor_ExecutionFails(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	handler := newFailingDBBackedHandler(t)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("org_fail_e2e"))
	resp, err := handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		AppliedBy: "tester",
	})

	// Execution failure is conveyed via response status, not a gRPC error.
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Contains(t, []controlplanev1.ApplyManifestStatus{
		controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED,
		controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_PARTIAL,
	}, resp.Status)
	// The execute step result must report FAILED.
	var sawFailedExecute bool
	for _, sr := range resp.StepResults {
		if sr.StepName == "execute" && sr.Status == controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED {
			sawFailedExecute = true
		}
	}
	assert.True(t, sawFailedExecute, "expected a FAILED execute step result")
	assert.NotEmpty(t, resp.PhaseStatus)
}

// TestApplyManifest_RealExecutor_EndToEnd drives the full non-dry-run
// ApplyManifest pipeline through a real executor with no history service.
// This exercises applyPlanAndExecute, execute (success), recordAndFinalize,
// the nil-historyService recordHistory early-return, and runPostApplyHooks.
func TestApplyManifest_RealExecutor_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	handler := newDBBackedHandler(t)

	var hookTenant string
	handler.postApplyHooks = []PostApplyHook{
		func(_ context.Context, tenantID string) { hookTenant = tenantID },
	}

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("org_e2e"))
	resp, err := handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		AppliedBy: "tester",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED, resp.Status)
	assert.NotEmpty(t, resp.JobId)
	// Post-apply hook must have been invoked with the request tenant.
	assert.Equal(t, "org_e2e", hookTenant)
	// No history service configured, so no snapshot is attached.
	assert.Nil(t, resp.Snapshot)
}
