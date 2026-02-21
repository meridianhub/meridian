package planner

import (
	"testing"

	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlan_MappingsInPhase5(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceMapping, ResourceCode: "stripe_webhook:1", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 1)
	assert.Equal(t, PhaseMappings, plan.Calls[0].Phase)
}

func TestPlan_MappingsAfterSagas(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceMapping, ResourceCode: "stripe_webhook:1", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceSaga, ResourceCode: "process_payment", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionCreate},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)
	require.Len(t, plan.Calls, 3)

	// Verify each resource is assigned to its exact expected phase.
	callsByCode := indexCallsByResourceID(plan.Calls)
	assert.Equal(t, PhaseInstruments, callsByCode["GBP"].Phase, "instrument should be in phase 1")
	assert.Equal(t, PhaseSagas, callsByCode["process_payment"].Phase, "saga should be in phase 4")
	assert.Equal(t, PhaseMappings, callsByCode["stripe_webhook:1"].Phase, "mapping should be in phase 5")
}

func TestPlan_GRPCMethodMapping_Mappings(t *testing.T) {
	p := NewManifestPlanner()
	diffPlan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceMapping, ResourceCode: "stripe_webhook:1", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceMapping, ResourceCode: "shopify_order:2", Action: differ.ActionUpdate},
			{ResourceType: differ.ResourceMapping, ResourceCode: "old_mapping:1", Action: differ.ActionDelete},
		},
	}

	plan, err := p.Plan(diffPlan, "tenant-1", "1.0", false)
	require.NoError(t, err)

	callsByCode := indexCallsByResourceID(plan.Calls)
	assert.Equal(t, MethodCreateMapping, callsByCode["stripe_webhook:1"].GRPCMethod)
	assert.Equal(t, MethodUpdateMapping, callsByCode["shopify_order:2"].GRPCMethod)
	assert.Equal(t, MethodDeprecateMapping, callsByCode["old_mapping:1"].GRPCMethod)
}

func TestPlan_MappingsPhaseIs5(t *testing.T) {
	// Verify PhaseMappings constant has value 5 (between Sagas=4 and SeedData=6)
	assert.Equal(t, Phase(5), PhaseMappings)
	assert.Equal(t, Phase(6), PhaseSeedData)
	assert.Less(t, int(PhaseSagas), int(PhaseMappings))
	assert.Less(t, int(PhaseMappings), int(PhaseSeedData))
}

func TestPlan_MappingPhaseLabel(t *testing.T) {
	assert.Equal(t, "Instruments", PhaseLabel(PhaseInstruments))
	assert.Equal(t, "Account Types", PhaseLabel(PhaseAccountTypes))
	assert.Equal(t, "Valuation Rules", PhaseLabel(PhaseValuationRules))
	assert.Equal(t, "Saga Definitions", PhaseLabel(PhaseSagas))
	assert.Equal(t, "Mapping Definitions", PhaseLabel(PhaseMappings))
	assert.Equal(t, "Seed Data", PhaseLabel(PhaseSeedData))
}
