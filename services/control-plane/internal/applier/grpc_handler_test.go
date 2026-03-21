package applier

import (
	"context"
	"fmt"
	"testing"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// newTestManifest creates a minimal valid manifest for testing.
func newTestManifest() *controlplanev1.Manifest {
	return &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:     "Test Manifest",
			Industry: "testing",
		},
		Instruments: []*controlplanev1.InstrumentDefinition{
			{
				Code: "GBP",
				Name: "British Pound Sterling",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "GBP",
					Precision: 2,
				},
			},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{
				Code:               "CURRENT",
				Name:               "Current Account",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
				AllowedInstruments: []string{"GBP"},
			},
		},
	}
}

// newTestHandler creates an ApplyManifestHandler with real validator/differ/planner
// but no executor or history service (suitable for unit tests).
func newTestHandler(t *testing.T) *ApplyManifestHandler {
	t.Helper()

	v, err := validator.New()
	require.NoError(t, err)

	d := differ.New(nil, nil)
	p := planner.NewManifestPlanner()

	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator: v,
		Differ:    d,
		Planner:   p,
	})
	require.NoError(t, err)
	return handler
}

func TestNewApplyManifestHandler_RequiredDependencies(t *testing.T) {
	v, err := validator.New()
	require.NoError(t, err)

	d := differ.New(nil, nil)
	p := planner.NewManifestPlanner()

	tests := []struct {
		name    string
		cfg     ApplyManifestHandlerConfig
		wantErr error
	}{
		{
			name:    "missing validator",
			cfg:     ApplyManifestHandlerConfig{Differ: d, Planner: p},
			wantErr: ErrValidatorRequired,
		},
		{
			name:    "missing differ",
			cfg:     ApplyManifestHandlerConfig{Validator: v, Planner: p},
			wantErr: ErrDifferRequired,
		},
		{
			name:    "missing planner",
			cfg:     ApplyManifestHandlerConfig{Validator: v, Differ: d},
			wantErr: ErrPlannerRequired,
		},
		{
			name: "all required present",
			cfg:  ApplyManifestHandlerConfig{Validator: v, Differ: d, Planner: p},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewApplyManifestHandler(tt.cfg)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNewApplyManifestHandler_PostApplyHooksStored(t *testing.T) {
	v, err := validator.New()
	require.NoError(t, err)

	d := differ.New(nil, nil)
	p := planner.NewManifestPlanner()

	called := false
	hook := PostApplyHook(func(_ context.Context, _ string) {
		called = true
	})

	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator:      v,
		Differ:         d,
		Planner:        p,
		PostApplyHooks: []PostApplyHook{hook},
	})
	require.NoError(t, err)
	assert.Len(t, handler.postApplyHooks, 1)

	// Verify the hook is callable
	handler.postApplyHooks[0](context.Background(), "test-tenant")
	assert.True(t, called)
}

func TestApplyManifest_NilManifest(t *testing.T) {
	handler := newTestHandler(t)

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		AppliedBy: "test-user",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "manifest is required")
}

func TestApplyManifest_EmptyAppliedBy(t *testing.T) {
	handler := newTestHandler(t)

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest: newTestManifest(),
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "applied_by is required")
}

func TestApplyManifest_DryRun_AllowsEmptyAppliedBy(t *testing.T) {
	handler := newTestHandler(t)

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest: newTestManifest(),
		DryRun:   true,
		// AppliedBy intentionally omitted — dry-run should not require it
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
}

func TestApplyManifest_ValidManifest_DryRun(t *testing.T) {
	handler := newTestHandler(t)

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		DryRun:    true,
		AppliedBy: "test-user",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)

	// Should have step results for: validate, diff, plan, execute (skipped)
	require.Len(t, resp.StepResults, 4)

	assert.Equal(t, "validate", resp.StepResults[0].StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, resp.StepResults[0].Status)

	assert.Equal(t, "diff", resp.StepResults[1].StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, resp.StepResults[1].Status)

	assert.Equal(t, "plan", resp.StepResults[2].StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, resp.StepResults[2].Status)

	assert.Equal(t, "execute", resp.StepResults[3].StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SKIPPED, resp.StepResults[3].Status)

	// Diff summary should indicate creates
	assert.NotEmpty(t, resp.DiffSummary)
}

func TestApplyManifest_InvalidManifest_ValidationFails(t *testing.T) {
	handler := newTestHandler(t)

	// Create a manifest missing required fields
	invalidManifest := &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name: "Test",
		},
		Instruments: []*controlplanev1.InstrumentDefinition{
			{
				Code: "GBP",
				Name: "British Pound",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "GBP",
					Precision: 2,
				},
			},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{
				Code:               "CURRENT",
				Name:               "Current Account",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
				AllowedInstruments: []string{"NONEXISTENT"}, // Invalid reference
			},
		},
	}

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:  invalidManifest,
		AppliedBy: "test-user",
	})

	require.NoError(t, err) // RPC succeeds, but status is VALIDATION_FAILED
	require.NotNil(t, resp)

	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED, resp.Status)
	assert.NotEmpty(t, resp.ValidationErrors)

	// Verify structured validation errors
	foundRefError := false
	for _, ve := range resp.ValidationErrors {
		if ve.Code == "UNDEFINED_INSTRUMENT_REFERENCE" {
			foundRefError = true
			assert.Contains(t, ve.Path, "account_types")
			assert.Contains(t, ve.Message, "NONEXISTENT")
		}
	}
	assert.True(t, foundRefError, "expected UNDEFINED_INSTRUMENT_REFERENCE validation error")
}

func TestApplyManifest_ValidManifest_NoExecutor(t *testing.T) {
	handler := newTestHandler(t)

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		DryRun:    false,
		AppliedBy: "test-user",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)

	// Without executor configured, non-dry-run applies should fail
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED, resp.Status)
}

func TestApplyManifest_CELValidationError(t *testing.T) {
	handler := newTestHandler(t)

	manifest := newTestManifest()
	manifest.AccountTypes[0].Policies = &controlplanev1.AccountTypePolicies{
		Validation: "amuont > 0", // Typo of "amount" - close enough for suggestion
	}

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:  manifest,
		AppliedBy: "test-user",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED, resp.Status)

	foundCELError := false
	for _, ve := range resp.ValidationErrors {
		if ve.Code == "CEL_UNDECLARED_REFERENCE" {
			foundCELError = true
			assert.NotEmpty(t, ve.Suggestion, "expected 'Did you mean...?' suggestion for typo")
		}
	}
	assert.True(t, foundCELError, "expected CEL undeclared reference error")
}

func TestApplyManifest_DuplicateInstrumentCodes(t *testing.T) {
	handler := newTestHandler(t)

	manifest := newTestManifest()
	manifest.Instruments = append(manifest.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "GBP", // Duplicate
		Name: "Another GBP",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "GBP",
			Precision: 2,
		},
	})

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:  manifest,
		AppliedBy: "test-user",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED, resp.Status)

	foundDupe := false
	for _, ve := range resp.ValidationErrors {
		if ve.Code == "DUPLICATE_CODE" {
			foundDupe = true
		}
	}
	assert.True(t, foundDupe, "expected DUPLICATE_CODE validation error")
}

func TestBuildExecutorInput(t *testing.T) {
	manifest := newTestManifest()
	manifest.ValuationRules = []*controlplanev1.ValuationRule{
		{
			FromInstrument: "GBP",
			ToInstrument:   "KWH",
			Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_FIXED,
		},
	}
	manifest.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "test_saga",
			Trigger: "api:/test",
			Script:  "def execute(ctx):\n  pass\n",
		},
	}

	input := buildExecutorInput(manifest)

	assert.Equal(t, "1.0", input.ManifestVersion)
	require.Len(t, input.Instruments, 1)
	assert.Equal(t, "GBP", input.Instruments[0].Code)
	assert.Equal(t, "British Pound Sterling", input.Instruments[0].DisplayName)
	assert.Equal(t, 2, input.Instruments[0].DecimalPlaces)

	require.Len(t, input.AccountTypes, 1)
	assert.Equal(t, "CURRENT", input.AccountTypes[0].Code)

	require.Len(t, input.ValuationRules, 1)
	assert.Equal(t, "GBP", input.ValuationRules[0].FromInstrument)
	assert.Equal(t, "KWH", input.ValuationRules[0].ToInstrument)

	require.Len(t, input.SagaDefinitions, 1)
	assert.Equal(t, "test_saga", input.SagaDefinitions[0].Name)
}

func TestBuildExecutorInput_NewResourceTypes(t *testing.T) {
	manifest := newTestManifest()
	manifest.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{
				Code:        "BLOOMBERG",
				Name:        "Bloomberg Financial Data",
				Description: "FX rates and indices",
				TrustLevel:  90,
			},
		},
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{
				Code:        "USD_EUR_FX",
				Unit:        "USD/EUR",
				SourceCode:  "BLOOMBERG",
				DisplayName: "USD/EUR Spot Rate",
			},
		},
	}
	manifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{
			Code:      "ACME_ENERGY",
			Name:      proto.String("Acme Energy Corp"),
			PartyType: "ORGANIZATION",
			Attributes: map[string]string{
				"industry": "energy",
			},
		},
	}
	manifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{
			Code:              "REVENUE_GBP",
			AccountType:       "REVENUE",
			Instrument:        "GBP",
			OwnerOrganization: "ACME_ENERGY",
			Description:       "Revenue clearing account",
		},
	}

	input := buildExecutorInput(manifest)

	require.Len(t, input.MarketDataSources, 1)
	assert.Equal(t, "BLOOMBERG", input.MarketDataSources[0].Code)
	assert.Equal(t, "Bloomberg Financial Data", input.MarketDataSources[0].Name)
	assert.Equal(t, 90, input.MarketDataSources[0].TrustLevel)

	require.Len(t, input.MarketDataSets, 1)
	assert.Equal(t, "USD_EUR_FX", input.MarketDataSets[0].Code)
	assert.Equal(t, "BLOOMBERG", input.MarketDataSets[0].SourceCode)

	require.Len(t, input.Organizations, 1)
	assert.Equal(t, "ACME_ENERGY", input.Organizations[0].Code)
	assert.Equal(t, "ORGANIZATION", input.Organizations[0].PartyType)
	assert.Equal(t, "energy", input.Organizations[0].Attributes["industry"])

	require.Len(t, input.InternalAccounts, 1)
	assert.Equal(t, "REVENUE_GBP", input.InternalAccounts[0].Code)
	assert.Equal(t, "REVENUE", input.InternalAccounts[0].AccountType)
	assert.Equal(t, "GBP", input.InternalAccounts[0].InstrumentCode)
	assert.Equal(t, "ACME_ENERGY", input.InternalAccounts[0].OwnerOrganization)
}

// --- skip_immutability_checks tests ---

// mockVersionStore returns a fixed manifest as the latest applied version.
type mockVersionStore struct {
	manifest *controlplanev1.Manifest
}

func (m *mockVersionStore) GetLatestApplied(_ context.Context) (*differ.ManifestVersion, error) {
	if m.manifest == nil {
		return nil, nil
	}
	return &differ.ManifestVersion{Manifest: m.manifest}, nil
}

func (m *mockVersionStore) Save(_ context.Context, _ *controlplanev1.Manifest, _ string) error {
	return nil
}

// newTestHandlerWithVersionStore creates a handler backed by a version store
// that returns prev as the last-applied manifest.
func newTestHandlerWithVersionStore(t *testing.T, prev *controlplanev1.Manifest) *ApplyManifestHandler {
	t.Helper()

	v, err := validator.New()
	require.NoError(t, err)

	d := differ.New(nil, nil)
	p := planner.NewManifestPlanner()

	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator:    v,
		Differ:       d,
		Planner:      p,
		VersionStore: &mockVersionStore{manifest: prev},
	})
	require.NoError(t, err)
	return handler
}

func TestApplyManifest_SkipImmutabilityChecks_DryRun_SkipsImmutabilityErrors(t *testing.T) {
	prev := newTestManifest()
	handler := newTestHandlerWithVersionStore(t, prev)

	// Change instrument code — normally triggers IMMUTABLE_FIELD_CHANGED
	curr := newTestManifest()
	curr.Instruments[0].Code = "USD"
	curr.AccountTypes[0].AllowedInstruments = []string{"USD"}

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:               curr,
		DryRun:                 true,
		SkipImmutabilityChecks: true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)

	// Should succeed as dry-run, not fail validation
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status,
		"expected DRY_RUN status when skip_immutability_checks is set with dry_run=true")

	// No IMMUTABLE_FIELD_CHANGED in validation errors
	for _, ve := range resp.ValidationErrors {
		assert.NotEqual(t, "IMMUTABLE_FIELD_CHANGED", ve.Code)
	}
}

func TestApplyManifest_ExpectedSequenceNumberZero_SkipsCheck(t *testing.T) {
	handler := newTestHandler(t)

	// expected_sequence_number=0 should skip the check (overwrite mode)
	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:               newTestManifest(),
		DryRun:                 true,
		ExpectedSequenceNumber: 0,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
}

func TestApplyManifest_ExpectedSequenceNumber_NoHistoryService_SkipsCheck(t *testing.T) {
	// Without a historyService, the optimistic lock check is skipped
	// (recordHistory returns nil, nil when historyService is nil)
	handler := newTestHandler(t)

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:               newTestManifest(),
		DryRun:                 true,
		ExpectedSequenceNumber: 42,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
}

func TestApplyManifest_SkipImmutabilityChecks_NotDryRun_StillEnforces(t *testing.T) {
	prev := newTestManifest()
	handler := newTestHandlerWithVersionStore(t, prev)

	// Change instrument code — triggers IMMUTABLE_FIELD_CHANGED
	curr := newTestManifest()
	curr.Instruments[0].Code = "USD"
	curr.AccountTypes[0].AllowedInstruments = []string{"USD"}

	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:               curr,
		DryRun:                 false,
		AppliedBy:              "test-user",
		SkipImmutabilityChecks: true, // should be ignored because dry_run=false
	})

	require.NoError(t, err)
	require.NotNil(t, resp)

	// Should fail validation because skip_immutability_checks is ignored when dry_run=false
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED, resp.Status,
		"expected VALIDATION_FAILED when skip_immutability_checks is set but dry_run=false")

	found := false
	for _, ve := range resp.ValidationErrors {
		if ve.Code == "IMMUTABLE_FIELD_CHANGED" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected IMMUTABLE_FIELD_CHANGED error when dry_run=false, regardless of skip flag")
}

// --- Phase Status Tests ---

func TestClassifyFailure_NilPhaseStatus(t *testing.T) {
	applyStatus, protoStatus := classifyFailure(nil)
	assert.Equal(t, manifest.ApplyStatusFailed, applyStatus)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED, protoStatus)
}

func TestClassifyFailure_AllFailed(t *testing.T) {
	ps := manifest.PhaseStatusMap{
		"phase_1": {Status: manifest.PhaseStatusFailed},
		"phase_2": {Status: manifest.PhaseStatusSkipped},
	}
	applyStatus, protoStatus := classifyFailure(ps)
	assert.Equal(t, manifest.ApplyStatusFailed, applyStatus)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED, protoStatus)
}

func TestClassifyFailure_Partial(t *testing.T) {
	ps := manifest.PhaseStatusMap{
		"phase_1": {Status: manifest.PhaseStatusCompleted},
		"phase_2": {Status: manifest.PhaseStatusFailed},
		"phase_3": {Status: manifest.PhaseStatusSkipped},
	}
	applyStatus, protoStatus := classifyFailure(ps)
	assert.Equal(t, manifest.ApplyStatusPartial, applyStatus)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_PARTIAL, protoStatus)
}

func TestBuildInitialPhaseStatus(t *testing.T) {
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments, ResourceID: "GBP"},
			{Phase: planner.PhaseAccountTypes, ResourceID: "CURRENT"},
			{Phase: planner.PhaseSagas, ResourceID: "test_saga"},
		},
	}

	ps := buildInitialPhaseStatus(plan)

	assert.Len(t, ps, 3)
	assert.Equal(t, manifest.PhaseStatusPending, ps["phase_1"].Status)
	assert.Equal(t, manifest.PhaseStatusPending, ps["phase_2"].Status)
	assert.Equal(t, manifest.PhaseStatusPending, ps["phase_4"].Status)
}

func TestUpdatePhaseStatus_AllSuccess(t *testing.T) {
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments},
			{Phase: planner.PhaseAccountTypes},
		},
	}
	ps := buildInitialPhaseStatus(plan)

	result := &ApplyManifestResult{Status: "applied"}
	updatePhaseStatus(ps, plan, result, nil)

	assert.Equal(t, manifest.PhaseStatusCompleted, ps["phase_1"].Status)
	assert.Equal(t, manifest.PhaseStatusCompleted, ps["phase_2"].Status)
}

func TestUpdatePhaseStatus_PartialFailure(t *testing.T) {
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments},
			{Phase: planner.PhaseAccountTypes},
			{Phase: planner.PhaseSagas},
		},
	}
	ps := buildInitialPhaseStatus(plan)

	// Positional correlation: step 0 -> call 0 (phase 1), step 1 -> call 1 (phase 2)
	result := &ApplyManifestResult{
		Status: "failed",
		Error:  "account type creation failed",
		StepResults: []saga.StepResult{
			{StepName: "reference_data.register_instrument", Success: true},
			{StepName: "reference_data.register_account_type", Success: false, Error: "account type creation failed"},
		},
	}
	updatePhaseStatus(ps, plan, result, fmt.Errorf("saga failed"))

	assert.Equal(t, manifest.PhaseStatusCompleted, ps["phase_1"].Status)
	assert.Equal(t, manifest.PhaseStatusFailed, ps["phase_2"].Status)
	assert.Equal(t, "account type creation failed", ps["phase_2"].Error)
	assert.Equal(t, manifest.PhaseStatusSkipped, ps["phase_4"].Status)
}

func TestFindFailedPhase_NoResult(t *testing.T) {
	plan := &planner.ExecutionPlan{}
	assert.Equal(t, planner.Phase(0), findFailedPhase(plan, nil))
}

func TestFindFailedPhase_WithResult(t *testing.T) {
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments},
			{Phase: planner.PhaseAccountTypes},
		},
	}
	// Positional correlation: step 0 -> call 0 (Instruments), step 1 -> call 1 (AccountTypes)
	result := &ApplyManifestResult{
		StepResults: []saga.StepResult{
			{StepName: "reference_data.register_instrument", Success: true},
			{StepName: "reference_data.register_account_type", Success: false},
		},
	}
	assert.Equal(t, planner.PhaseAccountTypes, findFailedPhase(plan, result))
}

func TestPhaseStatusMapToResponseProto_Nil(t *testing.T) {
	assert.Nil(t, phaseStatusMapToResponseProto(nil))
}

func TestPhaseStatusMapToResponseProto_Populated(t *testing.T) {
	now := time.Now().UTC()
	ps := manifest.PhaseStatusMap{
		"phase_1": {
			Status:      manifest.PhaseStatusCompleted,
			StartedAt:   &now,
			CompletedAt: &now,
		},
		"phase_2": {
			Status: manifest.PhaseStatusFailed,
			Error:  "something failed",
		},
	}

	proto := phaseStatusMapToResponseProto(ps)
	require.Len(t, proto, 2)

	assert.Equal(t, "COMPLETED", proto["phase_1"].Status)
	assert.NotNil(t, proto["phase_1"].StartedAt)
	assert.NotNil(t, proto["phase_1"].CompletedAt)

	assert.Equal(t, "FAILED", proto["phase_2"].Status)
	assert.Equal(t, "something failed", proto["phase_2"].Error)
}

// --- runPostApplyHooks tests ---

func TestRunPostApplyHooks_NoHooks(t *testing.T) {
	handler := newTestHandler(t)
	// Should not panic with no hooks
	handler.runPostApplyHooks(context.Background(), "tenant-1", handler.logger)
}

func TestRunPostApplyHooks_MultipleHooks(t *testing.T) {
	v, err := validator.New()
	require.NoError(t, err)

	var calls []string
	hook1 := PostApplyHook(func(_ context.Context, tid string) {
		calls = append(calls, "hook1:"+tid)
	})
	hook2 := PostApplyHook(func(_ context.Context, tid string) {
		calls = append(calls, "hook2:"+tid)
	})

	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator:      v,
		Differ:         differ.New(nil, nil),
		Planner:        planner.NewManifestPlanner(),
		PostApplyHooks: []PostApplyHook{hook1, hook2},
	})
	require.NoError(t, err)

	handler.runPostApplyHooks(context.Background(), "test-tenant", handler.logger)
	assert.Equal(t, []string{"hook1:test-tenant", "hook2:test-tenant"}, calls)
}

func TestRunPostApplyHooks_PanicRecovery(t *testing.T) {
	v, err := validator.New()
	require.NoError(t, err)

	var calls []string
	panicHook := PostApplyHook(func(_ context.Context, _ string) {
		panic("hook exploded")
	})
	normalHook := PostApplyHook(func(_ context.Context, tid string) {
		calls = append(calls, "survived:"+tid)
	})

	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator:      v,
		Differ:         differ.New(nil, nil),
		Planner:        planner.NewManifestPlanner(),
		PostApplyHooks: []PostApplyHook{panicHook, normalHook},
	})
	require.NoError(t, err)

	// Should not panic; second hook should still run
	handler.runPostApplyHooks(context.Background(), "test-tenant", handler.logger)
	assert.Equal(t, []string{"survived:test-tenant"}, calls)
}

// --- checkBlockedDeletions tests ---

func TestCheckBlockedDeletions_NoBlockedDeletions(t *testing.T) {
	handler := newTestHandler(t)
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{Action: differ.ActionCreate, ResourceCode: "GBP"},
		},
	}
	result := handler.checkBlockedDeletions(plan, false, handler.logger)
	assert.Nil(t, result)
}

func TestCheckBlockedDeletions_BlockedDeletions_NoForce(t *testing.T) {
	handler := newTestHandler(t)
	plan := &differ.DiffPlan{
		BlockedDeletions: []differ.BlockedDeletion{
			{ResourceType: "instrument", ResourceCode: "GBP", Reason: "has active positions"},
			{ResourceType: "account_type", ResourceCode: "CURRENT", Reason: "in use"},
		},
	}
	result := handler.checkBlockedDeletions(plan, false, handler.logger)
	require.NotNil(t, result)
	assert.Equal(t, "safety_check", result.StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED, result.Status)
	assert.Contains(t, result.Message, "force=true")
	assert.Len(t, result.Details, 2)
}

func TestCheckBlockedDeletions_BlockedDeletions_WithForce(t *testing.T) {
	handler := newTestHandler(t)
	plan := &differ.DiffPlan{
		BlockedDeletions: []differ.BlockedDeletion{
			{ResourceType: "instrument", ResourceCode: "GBP", Reason: "has active positions"},
		},
	}
	result := handler.checkBlockedDeletions(plan, true, handler.logger)
	assert.Nil(t, result, "force=true should override blocked deletions")
}

// --- instrumentTypeToDimension tests ---

func TestInstrumentTypeToDimension(t *testing.T) {
	tests := []struct {
		name     string
		instType controlplanev1.InstrumentType
		unit     string
		want     string
	}{
		{"fiat", controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT, "GBP", "CURRENCY"},
		{"voucher", controlplanev1.InstrumentType_INSTRUMENT_TYPE_VOUCHER, "POINTS", "COUNT"},
		{"commodity with known unit", controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY, "energy", "ENERGY"},
		{"commodity with unknown unit", controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY, "unknown_unit", ""},
		{"unspecified with known unit", controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED, "energy", "ENERGY"},
		{"unspecified with unknown unit", controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED, "xyz", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := instrumentTypeToDimension(tt.instType, tt.unit)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- stripEnumPrefix tests ---

func TestStripEnumPrefix(t *testing.T) {
	assert.Equal(t, "DEBIT", stripEnumPrefix("NORMAL_BALANCE_DEBIT", "NORMAL_BALANCE_"))
	assert.Equal(t, "CREDIT", stripEnumPrefix("NORMAL_BALANCE_CREDIT", "NORMAL_BALANCE_"))
	assert.Equal(t, "UNSPECIFIED", stripEnumPrefix("UNSPECIFIED", "NORMAL_BALANCE_"))
}

// --- extractAuthConfig tests ---

func TestExtractAuthConfig_Nil(t *testing.T) {
	authType, config := extractAuthConfig(nil)
	assert.Empty(t, authType)
	assert.Nil(t, config)
}

func TestExtractAuthConfig_ApiKey(t *testing.T) {
	auth := &controlplanev1.AuthConfigManifest{
		AuthConfig: &controlplanev1.AuthConfigManifest_ApiKey{
			ApiKey: &controlplanev1.ApiKeyAuthConfig{
				HeaderName:      "X-API-Key",
				ApiKeySecretRef: "secret/api-key",
			},
		},
	}
	authType, config := extractAuthConfig(auth)
	assert.Equal(t, "api_key", authType)
	assert.Equal(t, "X-API-Key", config["header_name"])
	assert.Equal(t, "secret/api-key", config["secret_ref"])
}

func TestExtractAuthConfig_Basic(t *testing.T) {
	auth := &controlplanev1.AuthConfigManifest{
		AuthConfig: &controlplanev1.AuthConfigManifest_Basic{
			Basic: &controlplanev1.BasicAuthConfig{
				Username:          "user",
				PasswordSecretRef: "secret/password",
			},
		},
	}
	authType, config := extractAuthConfig(auth)
	assert.Equal(t, "basic", authType)
	assert.Equal(t, "user", config["username"])
	assert.Equal(t, "secret/password", config["password_ref"])
}

func TestExtractAuthConfig_Oauth2(t *testing.T) {
	auth := &controlplanev1.AuthConfigManifest{
		AuthConfig: &controlplanev1.AuthConfigManifest_Oauth2{
			Oauth2: &controlplanev1.OAuth2AuthConfig{
				TokenUrl:        "https://auth.example.com/token",
				ClientId:        "client-123",
				ClientSecretRef: "secret/oauth",
				Scopes:          []string{"read", "write"},
			},
		},
	}
	authType, config := extractAuthConfig(auth)
	assert.Equal(t, "oauth2", authType)
	assert.Equal(t, "https://auth.example.com/token", config["token_url"])
	assert.Equal(t, "client-123", config["client_id"])
	assert.Equal(t, "secret/oauth", config["client_secret_ref"])
	assert.Equal(t, []string{"read", "write"}, config["scopes"])
}

func TestExtractAuthConfig_Hmac(t *testing.T) {
	auth := &controlplanev1.AuthConfigManifest{
		AuthConfig: &controlplanev1.AuthConfigManifest_Hmac{
			Hmac: &controlplanev1.HMACAuthConfig{
				Algorithm:       "SHA256",
				SecretRef:       "secret/hmac",
				SignatureHeader: "X-Signature",
			},
		},
	}
	authType, config := extractAuthConfig(auth)
	assert.Equal(t, "hmac", authType)
	assert.Equal(t, "SHA256", config["algorithm"])
	assert.Equal(t, "secret/hmac", config["secret_ref"])
	assert.Equal(t, "X-Signature", config["signature_header"])
}

func TestExtractAuthConfig_Mtls(t *testing.T) {
	auth := &controlplanev1.AuthConfigManifest{
		AuthConfig: &controlplanev1.AuthConfigManifest_Mtls{
			Mtls: &controlplanev1.MTLSAuthConfig{
				ClientCertSecretRef: "secret/cert",
				ClientKeySecretRef:  "secret/key",
				CaCertSecretRef:     "secret/ca",
			},
		},
	}
	authType, config := extractAuthConfig(auth)
	assert.Equal(t, "mtls", authType)
	assert.Equal(t, "secret/cert", config["client_cert_ref"])
	assert.Equal(t, "secret/key", config["client_key_ref"])
	assert.Equal(t, "secret/ca", config["ca_cert_ref"])
}

// --- buildExecutorInput operational gateway tests ---

func TestBuildExecutorInput_OperationalGateway(t *testing.T) {
	mf := newTestManifest()
	mf.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{
				ConnectionId: "stripe-conn",
				ProviderName: "Stripe",
				ProviderType: "payment_processor",
				Protocol:     controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_HTTPS,
				BaseUrl:      "https://api.stripe.com",
				Auth: &controlplanev1.AuthConfigManifest{
					AuthConfig: &controlplanev1.AuthConfigManifest_ApiKey{
						ApiKey: &controlplanev1.ApiKeyAuthConfig{
							HeaderName:      "Authorization",
							ApiKeySecretRef: "secret/stripe-key",
						},
					},
				},
				RetryPolicy: &controlplanev1.RetryPolicyConfig{
					MaxAttempts:           3,
					InitialBackoffSeconds: 1,
					MaxBackoffSeconds:     30,
					BackoffMultiplier:     2.0,
				},
				RateLimit: &controlplanev1.RateLimitConfig{
					RequestsPerSecond: 100,
					BurstSize:         200,
				},
			},
		},
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{
				InstructionType:      "PAYMENT",
				ConnectionId:         "stripe-conn",
				FallbackConnectionId: "backup-conn",
				OutboundMappingId:    "map-out-1",
				InboundMappingId:     "map-in-1",
				HttpMethod:           "POST",
				PathTemplate:         "/v1/charges",
			},
		},
	}

	input := buildExecutorInput(mf)

	require.Len(t, input.ProviderConnections, 1)
	pc := input.ProviderConnections[0]
	assert.Equal(t, "stripe-conn", pc.ConnectionID)
	assert.Equal(t, "Stripe", pc.ProviderName)
	assert.Equal(t, "payment_processor", pc.ProviderType)
	assert.Equal(t, "PROVIDER_PROTOCOL_HTTPS", pc.Protocol)
	assert.Equal(t, "https://api.stripe.com", pc.BaseURL)
	assert.Equal(t, "api_key", pc.AuthType)
	assert.Equal(t, "Authorization", pc.AuthConfig["header_name"])
	assert.NotNil(t, pc.RetryPolicy)
	assert.EqualValues(t, 3, pc.RetryPolicy["max_attempts"])
	assert.NotNil(t, pc.RateLimitConfig)
	assert.EqualValues(t, 100, pc.RateLimitConfig["requests_per_second"])

	require.Len(t, input.InstructionRoutes, 1)
	route := input.InstructionRoutes[0]
	assert.Equal(t, "PAYMENT", route.InstructionType)
	assert.Equal(t, "stripe-conn", route.ConnectionID)
	assert.Equal(t, "backup-conn", route.FallbackConnectionID)
	assert.Equal(t, "map-out-1", route.OutboundMapping)
	assert.Equal(t, "map-in-1", route.InboundMapping)
	assert.Equal(t, "POST", route.HTTPMethod)
	assert.Equal(t, "/v1/charges", route.PathTemplate)
}

func TestBuildExecutorInput_NilOperationalGateway(t *testing.T) {
	mf := newTestManifest()
	mf.OperationalGateway = nil

	input := buildExecutorInput(mf)
	assert.Empty(t, input.ProviderConnections)
	assert.Empty(t, input.InstructionRoutes)
}

func TestBuildExecutorInput_ConnectionWithoutRetryOrRateLimit(t *testing.T) {
	mf := newTestManifest()
	mf.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{
				ConnectionId: "simple-conn",
				ProviderName: "Simple",
				ProviderType: "webhook",
				BaseUrl:      "https://example.com",
			},
		},
	}

	input := buildExecutorInput(mf)
	require.Len(t, input.ProviderConnections, 1)
	assert.Nil(t, input.ProviderConnections[0].RetryPolicy)
	assert.Nil(t, input.ProviderConnections[0].RateLimitConfig)
}

func TestBuildExecutorInput_AccountTypeUnspecifiedNormalBalance(t *testing.T) {
	mf := newTestManifest()
	mf.AccountTypes[0].NormalBalance = controlplanev1.NormalBalance_NORMAL_BALANCE_UNSPECIFIED

	input := buildExecutorInput(mf)
	require.Len(t, input.AccountTypes, 1)
	assert.Equal(t, "DEBIT", input.AccountTypes[0].NormalBalance, "UNSPECIFIED should default to DEBIT")
}

func TestBuildExecutorInput_AccountTypeNoInstruments(t *testing.T) {
	mf := newTestManifest()
	mf.AccountTypes[0].AllowedInstruments = nil

	input := buildExecutorInput(mf)
	require.Len(t, input.AccountTypes, 1)
	assert.Empty(t, input.AccountTypes[0].InstrumentCode, "no instruments should yield empty instrument code")
}

func TestBuildExecutorInput_NilMarketData(t *testing.T) {
	mf := newTestManifest()
	mf.MarketData = nil

	input := buildExecutorInput(mf)
	assert.Empty(t, input.MarketDataSources)
	assert.Empty(t, input.MarketDataSets)
}

func TestBuildExecutorInput_OrganizationAlignedFields(t *testing.T) {
	t.Run("new fields pass through directly", func(t *testing.T) {
		mf := newTestManifest()
		mf.Organizations = []*controlplanev1.OrganizationDefinition{
			{
				Code:                  "ACME",
				Name:                  proto.String("Legacy Name"),
				PartyType:             "ORGANIZATION",
				LegalName:             proto.String("Acme Corp Legal"),
				DisplayName:           proto.String("Acme Corp"),
				ExternalReference:     proto.String("LEI-123456"),
				ExternalReferenceType: proto.String("LEI"),
			},
		}

		input := buildExecutorInput(mf)

		require.Len(t, input.Organizations, 1)
		org := input.Organizations[0]
		assert.Equal(t, "Acme Corp Legal", org.LegalName)
		assert.Equal(t, "Acme Corp", org.DisplayName)
		assert.Equal(t, "LEI-123456", org.ExternalReference)
		assert.Equal(t, "LEI", org.ExternalReferenceType)
	})

	t.Run("backward compat falls back to name", func(t *testing.T) {
		mf := newTestManifest()
		mf.Organizations = []*controlplanev1.OrganizationDefinition{
			{
				Code:      "ACME",
				Name:      proto.String("Acme Energy Corp"),
				PartyType: "ORGANIZATION",
				// No legal_name, display_name, external_reference set
			},
		}

		input := buildExecutorInput(mf)

		require.Len(t, input.Organizations, 1)
		org := input.Organizations[0]
		assert.Equal(t, "Acme Energy Corp", org.LegalName, "legal_name should fall back to name")
		assert.Equal(t, "Acme Energy Corp", org.DisplayName, "display_name should fall back to legal_name")
		assert.Equal(t, "ACME", org.ExternalReference, "external_reference should fall back to code")
		assert.Equal(t, "", org.ExternalReferenceType, "external_reference_type should be empty when not set")
	})

	t.Run("code-only fallback when both legal_name and name are empty", func(t *testing.T) {
		mf := newTestManifest()
		mf.Organizations = []*controlplanev1.OrganizationDefinition{
			{
				Code:      "GRID_OPS",
				PartyType: "ORGANIZATION",
				// No name, legal_name, or display_name set
			},
		}

		input := buildExecutorInput(mf)

		require.Len(t, input.Organizations, 1)
		org := input.Organizations[0]
		assert.Equal(t, "GRID_OPS", org.LegalName, "legal_name should fall back to code when name is also empty")
		assert.Equal(t, "GRID_OPS", org.DisplayName, "display_name should fall back through to code")
		assert.Equal(t, "GRID_OPS", org.ExternalReference, "external_reference should fall back to code")
	})
}

func TestBuildExecutorInput_MarketDataSetAlignedFields(t *testing.T) {
	mf := newTestManifest()
	mf.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "ECB", Name: "ECB", TrustLevel: 95},
		},
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{
				Code:                    "USD_EUR_FX",
				Category:                1, // FX_RATE
				Unit:                    "USD/EUR",
				SourceCode:              "ECB",
				ValidationExpression:    proto.String("value > 0"),
				ResolutionKeyExpression: proto.String("observed_at + ':' + source_code"),
			},
		},
	}

	input := buildExecutorInput(mf)

	require.Len(t, input.MarketDataSets, 1)
	ds := input.MarketDataSets[0]
	assert.Equal(t, "value > 0", ds.ValidationExpression)
	assert.Equal(t, "observed_at + ':' + source_code", ds.ResolutionKeyExpression)
}

func TestBuildExecutorInput_InstrumentFallbackDimension(t *testing.T) {
	mf := newTestManifest()
	// Unspecified type with unknown unit - dimension will be empty, fallback to CURRENCY
	mf.Instruments[0].Type = controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED
	mf.Instruments[0].Dimensions.Unit = "unknown_xyz"

	input := buildExecutorInput(mf)
	require.Len(t, input.Instruments, 1)
	assert.Equal(t, "CURRENCY", input.Instruments[0].Dimension, "empty dimension should fallback to CURRENCY")
}

// --- execute tests ---

func TestExecute_NilExecutor(t *testing.T) {
	handler := newTestHandler(t)
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments, ResourceID: "GBP"},
		},
	}

	result := handler.execute(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:  newTestManifest(),
		AppliedBy: "test",
	}, plan)

	assert.Error(t, result.err)
	assert.ErrorIs(t, result.err, ErrExecutorNotConfigured)
	assert.Equal(t, "execute", result.stepResult.StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED, result.stepResult.Status)
	assert.Contains(t, result.stepResult.Message, "Executor not configured")
}

// --- recordHistory tests ---

func TestRecordHistory_NilHistoryService(t *testing.T) {
	handler := newTestHandler(t)

	snapshot, err := handler.recordHistory(
		context.Background(),
		newTestManifest(),
		"admin",
		"",
		manifest.ApplyStatusApplied,
		nil,
		0,
	)

	assert.Nil(t, snapshot)
	assert.NoError(t, err)
}

func TestRecordHistoryWithPhaseStatus_NilHistoryService(t *testing.T) {
	handler := newTestHandler(t)

	snapshot, err := handler.recordHistoryWithPhaseStatus(
		context.Background(),
		newTestManifest(),
		"admin",
		"",
		manifest.ApplyStatusFailed,
		nil,
		0,
		manifest.PhaseStatusMap{"phase_1": {Status: manifest.PhaseStatusFailed}},
	)

	assert.Nil(t, snapshot)
	assert.NoError(t, err)
}

// --- updatePhaseStatus edge case tests ---

func TestUpdatePhaseStatus_NilResult_ErrorOnly(t *testing.T) {
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments},
			{Phase: planner.PhaseAccountTypes},
		},
	}
	ps := buildInitialPhaseStatus(plan)

	updatePhaseStatus(ps, plan, nil, fmt.Errorf("connection refused"))

	// With nil result, findFailedPhase returns 0 - all phases should be marked FAILED
	for _, entry := range ps {
		assert.Equal(t, manifest.PhaseStatusFailed, entry.Status)
		assert.Equal(t, "connection refused", entry.Error)
	}
}

func TestFindFailedPhase_AllSuccess(t *testing.T) {
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments},
		},
	}
	result := &ApplyManifestResult{
		StepResults: []saga.StepResult{
			{StepName: "step1", Success: true},
		},
	}
	assert.Equal(t, planner.Phase(0), findFailedPhase(plan, result))
}

func TestFindFailedPhase_FailedStepBeyondCalls(t *testing.T) {
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments},
		},
	}
	// More step results than planned calls
	result := &ApplyManifestResult{
		StepResults: []saga.StepResult{
			{StepName: "step1", Success: true},
			{StepName: "extra", Success: false},
		},
	}
	// Index 1 is beyond plan.Calls, so returns 0
	assert.Equal(t, planner.Phase(0), findFailedPhase(plan, result))
}

func TestFindFailedPhase_EmptyStepResults(t *testing.T) {
	plan := &planner.ExecutionPlan{
		Calls: []planner.PlannedCall{
			{Phase: planner.PhaseInstruments},
		},
	}
	result := &ApplyManifestResult{
		StepResults: []saga.StepResult{},
	}
	assert.Equal(t, planner.Phase(0), findFailedPhase(plan, result))
}

// --- classifyFailure edge case ---

func TestClassifyFailure_EmptyMap(t *testing.T) {
	ps := manifest.PhaseStatusMap{}
	applyStatus, protoStatus := classifyFailure(ps)
	assert.Equal(t, manifest.ApplyStatusFailed, applyStatus)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED, protoStatus)
}

func TestClassifyFailure_OnlyCompleted(t *testing.T) {
	ps := manifest.PhaseStatusMap{
		"phase_1": {Status: manifest.PhaseStatusCompleted},
		"phase_2": {Status: manifest.PhaseStatusCompleted},
	}
	applyStatus, protoStatus := classifyFailure(ps)
	// All completed without any failed - should return Failed since this function
	// is only called on failure paths
	assert.Equal(t, manifest.ApplyStatusFailed, applyStatus)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED, protoStatus)
}

// --- extractMarketData tests ---

func TestExtractMarketData_WithDatasets(t *testing.T) {
	mf := newTestManifest()
	mf.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{
				Code:        "EXCHANGE_A",
				Name:        "Exchange A",
				Description: "Primary exchange",
				TrustLevel:  85,
			},
		},
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{
				Code:        "FX_RATE_1",
				Unit:        "USD/EUR",
				SourceCode:  "EXCHANGE_A",
				DisplayName: "USD/EUR Rate",
				Description: "Spot exchange rate",
			},
		},
	}

	input := buildExecutorInput(mf)

	require.Len(t, input.MarketDataSources, 1)
	assert.Equal(t, "EXCHANGE_A", input.MarketDataSources[0].Code)
	assert.Equal(t, 85, input.MarketDataSources[0].TrustLevel)

	require.Len(t, input.MarketDataSets, 1)
	assert.Equal(t, "FX_RATE_1", input.MarketDataSets[0].Code)
	assert.NotEmpty(t, input.MarketDataSets[0].Code)
	assert.Equal(t, "USD/EUR", input.MarketDataSets[0].Unit)
	assert.Equal(t, "EXCHANGE_A", input.MarketDataSets[0].SourceCode)
}

// --- buildExecutorInput with sagas ---

func TestBuildExecutorInput_MultipleSagas(t *testing.T) {
	mf := newTestManifest()
	mf.Sagas = []*controlplanev1.SagaDefinition{
		{Name: "saga_1", Script: "def execute(ctx): pass"},
		{Name: "saga_2", Script: "def execute(ctx): return"},
	}

	input := buildExecutorInput(mf)
	require.Len(t, input.SagaDefinitions, 2)
	assert.Equal(t, "saga_1", input.SagaDefinitions[0].Name)
	assert.Equal(t, "saga_2", input.SagaDefinitions[1].Name)
}

// --- buildExecutorInput with valuation rules ---

func TestBuildExecutorInput_ValuationRules(t *testing.T) {
	mf := newTestManifest()
	mf.ValuationRules = []*controlplanev1.ValuationRule{
		{
			FromInstrument: "GBP",
			ToInstrument:   "USD",
			Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
		},
		{
			FromInstrument: "EUR",
			ToInstrument:   "GBP",
			Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_FIXED,
		},
	}

	input := buildExecutorInput(mf)
	require.Len(t, input.ValuationRules, 2)
	assert.Equal(t, "VALUATION_METHOD_SPOT_RATE", input.ValuationRules[0].RuleType)
	assert.Equal(t, "VALUATION_METHOD_FIXED", input.ValuationRules[1].RuleType)
}
