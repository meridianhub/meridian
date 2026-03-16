package applier

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	// expected_sequence_number=0 should skip the check, even without a historyRepo
	resp, err := handler.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{
		Manifest:               newTestManifest(),
		DryRun:                 true,
		ExpectedSequenceNumber: 0,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
}

func TestApplyManifest_ExpectedSequenceNumber_NilRepo_SkipsCheck(t *testing.T) {
	// When historyRepo is nil, the check is skipped even with a non-zero expected_sequence_number
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
