package planner

import (
	"strings"
	"testing"

	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlan_NilDiffPlan_ReturnsError(t *testing.T) {
	p := NewManifestPlanner()
	_, err := p.Plan(nil, "tenant-1", "1.0", false)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNilDiffPlan)
}

func TestPlan_EmptyDiffPlan_EmptyExecutionPlan(t *testing.T) {
	p := NewManifestPlanner()
	plan, err := p.Plan(&differ.DiffPlan{}, "tenant-1", "1.0", false)
	require.NoError(t, err)
	assert.Empty(t, plan.Calls)
	assert.Equal(t, "tenant-1", plan.TenantID)
	assert.Equal(t, "1.0", plan.ManifestVersion)
}

func TestPlan_NoChangeActionsFiltered(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceSaga, ResourceCode: "test_saga", Action: differ.ActionNoChange},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	assert.Empty(t, plan.Calls, "NO_CHANGE actions should be filtered out")
}

func TestPlan_InstrumentsInPhase1(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate, Description: "Create instrument GBP"},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "KWH", Action: differ.ActionUpdate, Description: "Update instrument KWH"},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "EUR", Action: differ.ActionDelete, Description: "Delete instrument EUR"},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	assert.Len(t, plan.Calls, 3)

	for _, call := range plan.Calls {
		assert.Equal(t, PhaseInstruments, call.Phase, "all instrument actions should be in Phase 1")
	}
}

func TestPlan_AccountTypesInPhase2(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, PhaseAccountTypes, plan.Calls[0].Phase)
}

func TestPlan_ValuationRulesInPhase3(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceValuationRule, ResourceCode: "KWH->GBP", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, PhaseValuationRules, plan.Calls[0].Phase)
}

func TestPlan_SagasInPhase4(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceSaga, ResourceCode: "process_settlement", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, PhaseSagas, plan.Calls[0].Phase)
}

func TestPlan_PhaseOrdering_InstrumentsBeforeAccountTypes(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			// Intentionally reverse order to verify sorting
			{ResourceType: differ.ResourceSaga, ResourceCode: "saga_a", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceValuationRule, ResourceCode: "KWH->GBP", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 4)

	// Verify phase ordering: instruments (1) < account types (2) < valuation rules (3) < sagas (4)
	for i := 1; i < len(plan.Calls); i++ {
		assert.LessOrEqual(t, int(plan.Calls[i-1].Phase), int(plan.Calls[i].Phase),
			"calls should be sorted by phase: %v before %v", plan.Calls[i-1], plan.Calls[i])
	}
}

func TestPlan_GRPCMethodMapping_Instruments(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "KWH", Action: differ.ActionUpdate},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "EUR", Action: differ.ActionDelete},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)

	callsByCode := indexCallsByResourceID(plan.Calls)
	assert.Equal(t, MethodRegisterInstrument, callsByCode["GBP"].GRPCMethod)
	assert.Equal(t, MethodUpdateInstrument, callsByCode["KWH"].GRPCMethod)
	assert.Equal(t, MethodDeprecateInstrument, callsByCode["EUR"].GRPCMethod)
}

func TestPlan_GRPCMethodMapping_AccountTypes(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)

	callsByCode := indexCallsByResourceID(plan.Calls)
	assert.Equal(t, MethodInitiateAccount, callsByCode["CURRENT"].GRPCMethod)
}

func TestPlan_GRPCMethodMapping_Sagas(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceSaga, ResourceCode: "deposit", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceSaga, ResourceCode: "withdrawal", Action: differ.ActionUpdate},
			{ResourceType: differ.ResourceSaga, ResourceCode: "legacy", Action: differ.ActionDelete},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)

	callsByCode := indexCallsByResourceID(plan.Calls)
	assert.Equal(t, MethodCreateSagaDraft, callsByCode["deposit"].GRPCMethod)
	assert.Equal(t, MethodUpdateSagaDefinition, callsByCode["withdrawal"].GRPCMethod)
	assert.Equal(t, MethodDeprecateSaga, callsByCode["legacy"].GRPCMethod)
}

func TestPlan_IdempotencyKeys_Deterministic(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate},
		},
	}

	plan1, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)

	plan2, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)

	assert.Equal(t, plan1.Calls[0].IdempotencyKey, plan2.Calls[0].IdempotencyKey,
		"idempotency keys should be deterministic for same inputs")
}

func TestPlan_IdempotencyKeys_UniquePerResource(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "EUR", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 2)

	assert.NotEqual(t, plan.Calls[0].IdempotencyKey, plan.Calls[1].IdempotencyKey,
		"different resources should have different idempotency keys")
}

func TestPlan_IdempotencyKeys_DifferentTenants(t *testing.T) {
	key1 := GenerateIdempotencyKey("tenant-1", "1.0", differ.ResourceInstrument, "GBP", differ.ActionCreate)
	key2 := GenerateIdempotencyKey("tenant-2", "1.0", differ.ResourceInstrument, "GBP", differ.ActionCreate)

	assert.NotEqual(t, key1, key2, "different tenants should produce different keys")
}

func TestPlan_IdempotencyKeys_DifferentVersions(t *testing.T) {
	key1 := GenerateIdempotencyKey("tenant-1", "1.0", differ.ResourceInstrument, "GBP", differ.ActionCreate)
	key2 := GenerateIdempotencyKey("tenant-1", "2.0", differ.ResourceInstrument, "GBP", differ.ActionCreate)

	assert.NotEqual(t, key1, key2, "different manifest versions should produce different keys")
}

func TestPlan_IdempotencyKeys_DifferentActions(t *testing.T) {
	key1 := GenerateIdempotencyKey("tenant-1", "1.0", differ.ResourceInstrument, "GBP", differ.ActionCreate)
	key2 := GenerateIdempotencyKey("tenant-1", "1.0", differ.ResourceInstrument, "GBP", differ.ActionUpdate)

	assert.NotEqual(t, key1, key2, "different actions should produce different keys")
}

func TestPlan_IdempotencyKey_Format(t *testing.T) {
	key := GenerateIdempotencyKey("tenant-1", "1.0", differ.ResourceInstrument, "GBP", differ.ActionCreate)
	assert.Len(t, key, 64, "SHA-256 hex string should be 64 characters")
	assert.Regexp(t, "^[a-f0-9]{64}$", key, "should be lowercase hex")
}

func TestPlan_DryRun_PropagatedToAllCalls(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", true)
	require.NoError(t, err)
	assert.True(t, plan.DryRun)

	for _, call := range plan.Calls {
		assert.True(t, call.DryRun, "all calls should have DryRun=true when plan is dry run")
	}
}

func TestPlan_DryRun_False(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	assert.False(t, plan.DryRun)

	for _, call := range plan.Calls {
		assert.False(t, call.DryRun)
	}
}

func TestPlan_DeterministicOrdering(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceSaga, ResourceCode: "z_saga", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "ZZZ", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "AAA", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceSaga, ResourceCode: "a_saga", Action: differ.ActionCreate},
		},
	}

	// Plan multiple times to verify determinism
	var prevPlan *ExecutionPlan
	for i := 0; i < 5; i++ {
		plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
		require.NoError(t, err)

		if prevPlan != nil {
			for j := range plan.Calls {
				assert.Equal(t, prevPlan.Calls[j].ResourceID, plan.Calls[j].ResourceID,
					"call ordering should be deterministic across runs")
			}
		}
		prevPlan = plan
	}
}

func TestPlan_DescriptionPreserved(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate, Description: "Create instrument GBP (British Pound Sterling)"},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	assert.Equal(t, "Create instrument GBP (British Pound Sterling)", plan.Calls[0].Description)
}

func TestPlan_MixedActions_CorrectPhaseAssignment(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "EUR", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceValuationRule, ResourceCode: "KWH->GBP", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceSaga, ResourceCode: "deposit", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceSaga, ResourceCode: "withdrawal", Action: differ.ActionNoChange},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)

	// Should have 4 calls (2 NO_CHANGE filtered)
	assert.Len(t, plan.Calls, 4)

	// Verify phase progression
	for i := 1; i < len(plan.Calls); i++ {
		assert.LessOrEqual(t, int(plan.Calls[i-1].Phase), int(plan.Calls[i].Phase))
	}
}

// --- ExecutionPlan method tests ---

func TestExecutionPlan_ByPhase(t *testing.T) {
	plan := &ExecutionPlan{
		Calls: []PlannedCall{
			{Phase: PhaseInstruments, ResourceID: "GBP"},
			{Phase: PhaseInstruments, ResourceID: "EUR"},
			{Phase: PhaseAccountTypes, ResourceID: "CURRENT"},
			{Phase: PhaseSagas, ResourceID: "deposit"},
		},
	}

	byPhase := plan.ByPhase()
	assert.Len(t, byPhase[PhaseInstruments], 2)
	assert.Len(t, byPhase[PhaseAccountTypes], 1)
	assert.Len(t, byPhase[PhaseSagas], 1)
	assert.Empty(t, byPhase[PhaseValuationRules])
}

func TestExecutionPlan_Phases(t *testing.T) {
	plan := &ExecutionPlan{
		Calls: []PlannedCall{
			{Phase: PhaseInstruments},
			{Phase: PhaseInstruments},
			{Phase: PhaseSagas},
		},
	}

	phases := plan.Phases()
	assert.Equal(t, []Phase{PhaseInstruments, PhaseSagas}, phases)
}

func TestExecutionPlan_Summary(t *testing.T) {
	plan := &ExecutionPlan{
		Calls: []PlannedCall{
			{Action: differ.ActionCreate},
			{Action: differ.ActionCreate},
			{Action: differ.ActionUpdate},
			{Action: differ.ActionDelete},
		},
	}

	summary := plan.Summary()
	assert.Contains(t, summary, "4 calls")
	assert.Contains(t, summary, "Creates: 2")
	assert.Contains(t, summary, "Updates: 1")
	assert.Contains(t, summary, "Deletes: 1")
}

func TestExecutionPlan_Summary_DryRun(t *testing.T) {
	plan := &ExecutionPlan{
		DryRun: true,
		Calls: []PlannedCall{
			{Action: differ.ActionCreate},
		},
	}

	summary := plan.Summary()
	assert.Contains(t, summary, "[DRY RUN]")
}

func TestExecutionPlan_Visualize(t *testing.T) {
	plan := &ExecutionPlan{
		TenantID:        "acme-energy",
		ManifestVersion: "1.0",
		Calls: []PlannedCall{
			{Phase: PhaseInstruments, ResourceType: differ.ResourceInstrument, ResourceID: "GBP", Action: differ.ActionCreate, GRPCMethod: MethodRegisterInstrument, Description: "Create instrument GBP"},
			{Phase: PhaseAccountTypes, ResourceType: differ.ResourceAccountType, ResourceID: "CURRENT", Action: differ.ActionCreate, GRPCMethod: MethodInitiateAccount, Description: "Create account type CURRENT"},
			{Phase: PhaseSagas, ResourceType: differ.ResourceSaga, ResourceID: "deposit", Action: differ.ActionUpdate, GRPCMethod: MethodUpdateSagaDefinition, Description: "Update saga deposit"},
		},
	}

	viz := plan.Visualize()

	assert.Contains(t, viz, "EXECUTION PLAN")
	assert.Contains(t, viz, "acme-energy")
	assert.Contains(t, viz, "1.0")
	assert.Contains(t, viz, "Total calls: 3")
	assert.Contains(t, viz, "Phase 1: Instruments")
	assert.Contains(t, viz, "Phase 2: Account Types")
	assert.Contains(t, viz, "Phase 4: Saga Definitions")
	assert.Contains(t, viz, "+ instrument GBP")
	assert.Contains(t, viz, "+ account_type CURRENT")
	assert.Contains(t, viz, "~ saga deposit")
}

func TestExecutionPlan_Visualize_DryRun(t *testing.T) {
	plan := &ExecutionPlan{
		TenantID:        "test",
		ManifestVersion: "1.0",
		DryRun:          true,
		Calls: []PlannedCall{
			{Phase: PhaseInstruments, ResourceType: differ.ResourceInstrument, ResourceID: "GBP", Action: differ.ActionCreate, GRPCMethod: MethodRegisterInstrument},
		},
	}

	viz := plan.Visualize()
	assert.Contains(t, viz, "DRY RUN")
}

func TestExecutionPlan_Visualize_DeleteActions(t *testing.T) {
	plan := &ExecutionPlan{
		TenantID:        "test",
		ManifestVersion: "1.0",
		Calls: []PlannedCall{
			{Phase: PhaseInstruments, ResourceType: differ.ResourceInstrument, ResourceID: "EUR", Action: differ.ActionDelete, GRPCMethod: MethodDeprecateInstrument},
		},
	}

	viz := plan.Visualize()
	assert.Contains(t, viz, "- instrument EUR")
}

// --- PhaseLabel tests ---

func TestPhaseLabel(t *testing.T) {
	assert.Equal(t, "Instruments", PhaseLabel(PhaseInstruments))
	assert.Equal(t, "Account Types", PhaseLabel(PhaseAccountTypes))
	assert.Equal(t, "Valuation Rules", PhaseLabel(PhaseValuationRules))
	assert.Equal(t, "Saga Definitions", PhaseLabel(PhaseSagas))
	assert.Equal(t, "Seed Data", PhaseLabel(PhaseSeedData))
	assert.Equal(t, "Party Types", PhaseLabel(PhasePartyTypes))
	assert.True(t, strings.HasPrefix(PhaseLabel(Phase(99)), "Phase("))
}

// --- Full integration scenario ---

func TestPlan_FullEnergyManifest(t *testing.T) {
	p := NewManifestPlanner()

	// Simulate a diff plan from applying the energy manifest for the first time
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate, Description: "Create instrument GBP (British Pound Sterling)"},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "KWH", Action: differ.ActionCreate, Description: "Create instrument KWH (Kilowatt Hour)"},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "CARBON_CREDIT", Action: differ.ActionCreate, Description: "Create instrument CARBON_CREDIT"},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "ENERGY_TRADING", Action: differ.ActionCreate, Description: "Create account type ENERGY_TRADING"},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CARBON_INVENTORY", Action: differ.ActionCreate, Description: "Create account type CARBON_INVENTORY"},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "SETTLEMENT", Action: differ.ActionCreate, Description: "Create account type SETTLEMENT"},
			{ResourceType: differ.ResourceValuationRule, ResourceCode: "KWH->GBP", Action: differ.ActionCreate, Description: "Create valuation rule KWH->GBP"},
			{ResourceType: differ.ResourceValuationRule, ResourceCode: "CARBON_CREDIT->GBP", Action: differ.ActionCreate, Description: "Create valuation rule CARBON_CREDIT->GBP"},
			{ResourceType: differ.ResourceSaga, ResourceCode: "process_energy_settlement", Action: differ.ActionCreate, Description: "Create saga process_energy_settlement"},
		},
	}

	plan, err := p.Plan(diffPlan, "acme-energy", "1.0", false)
	require.NoError(t, err)

	assert.Len(t, plan.Calls, 9)

	// All calls should have idempotency keys
	for _, call := range plan.Calls {
		assert.NotEmpty(t, call.IdempotencyKey)
		assert.Len(t, call.IdempotencyKey, 64)
	}

	// Verify phase ordering is correct
	byPhase := plan.ByPhase()
	assert.Len(t, byPhase[PhaseInstruments], 3, "3 instruments in phase 1")
	assert.Len(t, byPhase[PhaseAccountTypes], 3, "3 account types in phase 2")
	assert.Len(t, byPhase[PhaseValuationRules], 2, "2 valuation rules in phase 3")
	assert.Len(t, byPhase[PhaseSagas], 1, "1 saga in phase 4")

	// Verify instruments come before account types
	lastInstrumentPhase := plan.Calls[2].Phase // 3rd call should be last instrument
	firstAccountPhase := plan.Calls[3].Phase   // 4th call should be first account type
	assert.Less(t, int(lastInstrumentPhase), int(firstAccountPhase),
		"instruments must complete before account types")
}

func TestPlan_FullEnergyManifest_DryRun(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "acme-energy", "1.0", true)
	require.NoError(t, err)

	assert.True(t, plan.DryRun)
	assert.True(t, plan.Calls[0].DryRun)

	// Idempotency key should still be the same regardless of dry run
	planNotDry, err := p.Plan(diffPlan, "acme-energy", "1.0", false)
	require.NoError(t, err)
	assert.Equal(t, plan.Calls[0].IdempotencyKey, planNotDry.Calls[0].IdempotencyKey,
		"idempotency key should not change based on dry run flag")
}

// --- Party type planner tests ---

func TestPlan_PartyTypesInPhase6(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourcePartyType, ResourceCode: "tenant-1:PERSON", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, PhasePartyTypes, plan.Calls[0].Phase)
}

func TestPlan_GRPCMethodMapping_PartyTypes(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourcePartyType, ResourceCode: "tenant-1:PERSON", Action: differ.ActionCreate},
			{ResourceType: differ.ResourcePartyType, ResourceCode: "tenant-1:ORGANIZATION", Action: differ.ActionUpdate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)

	callsByCode := indexCallsByResourceID(plan.Calls)
	assert.Equal(t, MethodRegisterPartyType, callsByCode["tenant-1:PERSON"].GRPCMethod)
	assert.Equal(t, MethodUpdatePartyType, callsByCode["tenant-1:ORGANIZATION"].GRPCMethod)
}

func TestPlan_PartyType_Delete_NotSupported(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourcePartyType, ResourceCode: "tenant-1:PERSON", Action: differ.ActionDelete},
		},
	}

	_, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	assert.Error(t, err, "DELETE for party types should fail")
	assert.ErrorIs(t, err, ErrDeleteNotSupportedForPartyType)
}

// --- Market Data Source planner tests ---

func TestPlan_MarketDataSourcesInPhase9(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceMarketDataSource, ResourceCode: "BLOOMBERG", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, PhaseMarketDataSources, plan.Calls[0].Phase)
	assert.Equal(t, MethodRegisterDataSource, plan.Calls[0].GRPCMethod)
}

func TestPlan_MarketDataSourceUpdate(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceMarketDataSource, ResourceCode: "ECB", Action: differ.ActionUpdate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, MethodUpdateDataSource, plan.Calls[0].GRPCMethod)
}

func TestPlan_MarketDataSourceDelete(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceMarketDataSource, ResourceCode: "REUTERS", Action: differ.ActionDelete},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, MethodDeactivateDataSource, plan.Calls[0].GRPCMethod)
}

// --- Market Data Set planner tests ---

func TestPlan_MarketDataSetsInPhase10(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceMarketDataSet, ResourceCode: "USD_EUR_FX", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, PhaseMarketDataSets, plan.Calls[0].Phase)
	assert.Equal(t, MethodRegisterDataSet, plan.Calls[0].GRPCMethod)
}

func TestPlan_MarketDataSetUpdate(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceMarketDataSet, ResourceCode: "BRENT_CRUDE", Action: differ.ActionUpdate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, MethodUpdateDataSet, plan.Calls[0].GRPCMethod)
}

func TestPlan_MarketDataSetDelete(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceMarketDataSet, ResourceCode: "OLD_FX", Action: differ.ActionDelete},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, MethodDeprecateDataSet, plan.Calls[0].GRPCMethod)
}

// --- Organization planner tests ---

func TestPlan_OrganizationsInPhase11(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceOrganization, ResourceCode: "ACME_ENERGY", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, PhaseOrganizations, plan.Calls[0].Phase)
	assert.Equal(t, MethodRegisterOrganization, plan.Calls[0].GRPCMethod)
}

func TestPlan_OrganizationUpdate(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceOrganization, ResourceCode: "ACME_ENERGY", Action: differ.ActionUpdate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, MethodRegisterOrganization, plan.Calls[0].GRPCMethod)
}

func TestPlan_PhaseOrdering_MarketDataBeforeOrganizations(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceOrganization, ResourceCode: "ORG", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceMarketDataSet, ResourceCode: "FX", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceMarketDataSource, ResourceCode: "SRC", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 3)

	// Verify phase ordering: sources (9) < sets (10) < organizations (11)
	assert.Equal(t, PhaseMarketDataSources, plan.Calls[0].Phase)
	assert.Equal(t, PhaseMarketDataSets, plan.Calls[1].Phase)
	assert.Equal(t, PhaseOrganizations, plan.Calls[2].Phase)
}

// --- Test helpers ---

func indexCallsByResourceID(calls []PlannedCall) map[string]PlannedCall {
	m := make(map[string]PlannedCall, len(calls))
	for _, call := range calls {
		m[call.ResourceID] = call
	}
	return m
}
