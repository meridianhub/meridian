package differ

import (
	"context"
	"fmt"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testManifest returns a fully populated manifest for testing.
func testManifest() *controlplanev1.Manifest {
	return &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:     "Test Manifest",
			Industry: "energy",
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
			{
				Code: "KWH",
				Name: "Kilowatt Hour",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "kWh",
					Precision: 3,
				},
			},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{
				Code:               "SETTLEMENT",
				Name:               "Settlement Account",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
				AllowedInstruments: []string{"GBP"},
				Policies: &controlplanev1.AccountTypePolicies{
					Validation: "amount > 0",
				},
			},
		},
		ValuationRules: []*controlplanev1.ValuationRule{
			{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
				Source:         "nordpool_spot",
			},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "process_settlement",
				Trigger: "api:/v1/settlements",
				Script:  "def execute(ctx):\n    return {\"status\": \"ok\"}\n",
			},
		},
	}
}

func TestDiff_NilLastApplied_AllCreates(t *testing.T) {
	d := New(nil, nil)
	manifest := testManifest()

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActions(plan.Actions, ActionCreate)
	assert.Len(t, creates, 5, "expected 5 creates: 2 instruments + 1 account type + 1 valuation rule + 1 saga")

	noChanges := filterActions(plan.Actions, ActionNoChange)
	assert.Empty(t, noChanges)

	deletes := filterActions(plan.Actions, ActionDelete)
	assert.Empty(t, deletes)
}

func TestDiff_NilNewManifest_ReturnsError(t *testing.T) {
	d := New(nil, nil)
	_, err := d.Diff(context.Background(), testManifest(), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "new manifest cannot be nil")
}

func TestDiff_IdenticalManifests_AllNoChange(t *testing.T) {
	d := New(nil, nil)
	manifest := testManifest()

	plan, err := d.Diff(context.Background(), manifest, manifest)
	require.NoError(t, err)

	noChanges := filterActions(plan.Actions, ActionNoChange)
	assert.Len(t, noChanges, 5, "expected all 5 resources as NO_CHANGE")

	creates := filterActions(plan.Actions, ActionCreate)
	assert.Empty(t, creates)

	assert.False(t, plan.HasBreakingChanges)
}

func TestDiff_AddedInstrument_Create(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Instruments = append(newManifest.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "EUR",
			Precision: 2,
		},
	})

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActions(plan.Actions, ActionCreate)
	assert.Len(t, creates, 1)
	assert.Equal(t, "EUR", creates[0].ResourceCode)
	assert.Equal(t, ResourceInstrument, creates[0].ResourceType)
}

func TestDiff_RemovedInstrument_Delete(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Instruments = newManifest.Instruments[:1] // keep only GBP, remove KWH

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	deletes := filterActions(plan.Actions, ActionDelete)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "KWH", deletes[0].ResourceCode)
	assert.True(t, deletes[0].Breaking)
	assert.True(t, plan.HasBreakingChanges)
}

func TestDiff_ModifiedInstrument_Update(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Instruments[0].Name = "GBP (Updated)"

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActions(plan.Actions, ActionUpdate)
	assert.Len(t, updates, 1)
	assert.Equal(t, "GBP", updates[0].ResourceCode)
	assert.Contains(t, updates[0].Description, "name:")
}

func TestDiff_ModifiedInstrumentType_DescribesChange(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Instruments[1].Type = controlplanev1.InstrumentType_INSTRUMENT_TYPE_VOUCHER

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActions(plan.Actions, ActionUpdate)
	assert.Len(t, updates, 1)
	assert.Equal(t, "KWH", updates[0].ResourceCode)
	assert.Contains(t, updates[0].Description, "type:")
}

func TestDiff_ModifiedAccountType_Update(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.AccountTypes[0].Policies.Validation = "amount > 0 && amount < 1000000"

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceAccountType)
	assert.Len(t, updates, 1)
	assert.Equal(t, "SETTLEMENT", updates[0].ResourceCode)
	assert.Contains(t, updates[0].Description, "policies changed")
}

func TestDiff_ModifiedAccountTypeName_DescribesChange(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.AccountTypes[0].Name = "Updated Settlement Account"

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceAccountType)
	assert.Len(t, updates, 1)
	assert.Contains(t, updates[0].Description, "name:")
}

func TestDiff_ModifiedSaga_Update(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Sagas[0].Script = "def execute(ctx):\n    return {\"status\": \"updated\"}\n"

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceSaga)
	assert.Len(t, updates, 1)
	assert.Equal(t, "process_settlement", updates[0].ResourceCode)
	assert.Contains(t, updates[0].Description, "script changed")
}

func TestDiff_ModifiedSagaTrigger_DescribesChange(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Sagas[0].Trigger = "api:/v2/settlements"

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceSaga)
	assert.Len(t, updates, 1)
	assert.Contains(t, updates[0].Description, "trigger:")
}

func TestDiff_AddedSaga_Create(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Sagas = append(newManifest.Sagas, &controlplanev1.SagaDefinition{
		Name:    "daily_reconciliation",
		Trigger: "scheduled:daily_at_0200",
		Script:  "def execute(ctx):\n    return {}\n",
	})

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceSaga)
	assert.Len(t, creates, 1)
	assert.Equal(t, "daily_reconciliation", creates[0].ResourceCode)
	assert.Contains(t, creates[0].Description, "trigger: scheduled:")
}

func TestDiff_RemovedSaga_Delete(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Sagas = nil

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceSaga)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "process_settlement", deletes[0].ResourceCode)
	assert.True(t, deletes[0].Breaking)
}

func TestDiff_RemovedAccountType_Delete(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.AccountTypes = nil

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceAccountType)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "SETTLEMENT", deletes[0].ResourceCode)
}

func TestDiff_ValuationRuleAdded(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.ValuationRules = append(newManifest.ValuationRules, &controlplanev1.ValuationRule{
		FromInstrument: "EUR",
		ToInstrument:   "GBP",
		Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
		Source:         "ecb_fx_daily",
	})

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceValuationRule)
	assert.Len(t, creates, 1)
	assert.Equal(t, "EUR->GBP", creates[0].ResourceCode)
}

func TestDiff_ValuationRuleModified(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.ValuationRules[0].Method = controlplanev1.ValuationMethod_VALUATION_METHOD_FIXED

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceValuationRule)
	assert.Len(t, updates, 1)
	assert.Equal(t, "KWH->GBP", updates[0].ResourceCode)
}

func TestDiff_ValuationRuleRemoved(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.ValuationRules = nil

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceValuationRule)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "KWH->GBP", deletes[0].ResourceCode)
}

func TestDiff_MultipleChanges_MixedActions(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	// Remove KWH instrument
	newManifest.Instruments = newManifest.Instruments[:1]
	// Add EUR instrument
	newManifest.Instruments = append(newManifest.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "EUR",
			Precision: 2,
		},
	})
	// Modify settlement account type
	newManifest.AccountTypes[0].Name = "Updated Settlement"
	// Add new saga
	newManifest.Sagas = append(newManifest.Sagas, &controlplanev1.SagaDefinition{
		Name:    "new_workflow",
		Trigger: "api:/v1/workflows",
		Script:  "def execute(ctx):\n    pass\n",
	})

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActions(plan.Actions, ActionCreate)
	updates := filterActions(plan.Actions, ActionUpdate)
	deletes := filterActions(plan.Actions, ActionDelete)
	noChanges := filterActions(plan.Actions, ActionNoChange)

	assert.Len(t, creates, 2, "EUR instrument + new_workflow saga")
	assert.Len(t, updates, 1, "modified SETTLEMENT account type")
	assert.Len(t, deletes, 1, "removed KWH instrument")
	assert.Len(t, noChanges, 3, "GBP unchanged, valuation rule unchanged, process_settlement saga unchanged")

	assert.True(t, plan.HasBreakingChanges, "should be breaking due to DELETE")
}

func TestDiff_EmptyManifests_NoActions(t *testing.T) {
	d := New(nil, nil)
	empty := &controlplanev1.Manifest{
		Version:  "1.0",
		Metadata: &controlplanev1.ManifestMetadata{Name: "Empty"},
	}

	plan, err := d.Diff(context.Background(), empty, empty)
	require.NoError(t, err)
	assert.Empty(t, plan.Actions)
	assert.False(t, plan.HasBreakingChanges)
}

func TestDiff_SafetyChecker_BlocksDeletion(t *testing.T) {
	checker := &mockSafetyChecker{
		accountBlocked: map[string]*BlockedDeletion{
			"SETTLEMENT": {
				ResourceType: ResourceAccountType,
				ResourceCode: "SETTLEMENT",
				Reason:       "1,234 accounts with non-zero balances exist",
			},
		},
	}
	d := New(checker, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.AccountTypes = nil

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	assert.True(t, plan.HasBlockedDeletions())
	assert.Len(t, plan.BlockedDeletions, 1)
	assert.Equal(t, "SETTLEMENT", plan.BlockedDeletions[0].ResourceCode)
	assert.Contains(t, plan.BlockedDeletions[0].Reason, "1,234 accounts")
}

func TestDiff_SafetyChecker_BlocksInstrumentDeletion(t *testing.T) {
	checker := &mockSafetyChecker{
		instrumentBlocked: map[string]*BlockedDeletion{
			"KWH": {
				ResourceType: ResourceInstrument,
				ResourceCode: "KWH",
				Reason:       "referenced by 3 active valuation rules",
			},
		},
	}
	d := New(checker, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Instruments = newManifest.Instruments[:1] // remove KWH

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	assert.True(t, plan.HasBlockedDeletions())
	assert.Len(t, plan.BlockedDeletions, 1)
	assert.Contains(t, plan.BlockedDeletions[0].Reason, "3 active valuation rules")
}

func TestDiff_SafetyChecker_BlocksSagaDeletion(t *testing.T) {
	checker := &mockSafetyChecker{
		sagaBlocked: map[string]*BlockedDeletion{
			"process_settlement": {
				ResourceType: ResourceSaga,
				ResourceCode: "process_settlement",
				Reason:       "5 pending/running saga instances",
			},
		},
	}
	d := New(checker, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Sagas = nil

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	assert.True(t, plan.HasBlockedDeletions())
	assert.Len(t, plan.BlockedDeletions, 1)
	assert.Contains(t, plan.BlockedDeletions[0].Reason, "5 pending/running")
}

func TestDiff_SafetyChecker_AllowsDeletionWhenNothingBlocked(t *testing.T) {
	checker := &mockSafetyChecker{} // no blocked resources
	d := New(checker, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.AccountTypes = nil

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	assert.False(t, plan.HasBlockedDeletions())
	assert.Empty(t, plan.BlockedDeletions)
}

func TestDiff_SafetyChecker_ErrorPropagates(t *testing.T) {
	checker := &mockSafetyChecker{
		err: fmt.Errorf("connection refused"),
	}
	d := New(checker, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.AccountTypes = nil

	_, err := d.Diff(context.Background(), oldManifest, newManifest)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestDiff_DriftDetector_WarningsIncluded(t *testing.T) {
	detector := &mockDriftDetector{
		warnings: []DriftWarning{
			{
				ResourceType: ResourceInstrument,
				ResourceCode: "GBP",
				Description:  "precision manually changed from 2 to 4 in database",
			},
		},
	}
	d := New(nil, detector)
	manifest := testManifest()

	plan, err := d.Diff(context.Background(), manifest, manifest)
	require.NoError(t, err)

	assert.Len(t, plan.DriftWarnings, 1)
	assert.Equal(t, "GBP", plan.DriftWarnings[0].ResourceCode)
	assert.Contains(t, plan.DriftWarnings[0].Description, "precision manually changed")
}

func TestDiff_DriftDetector_NotCalledWhenNoLastApplied(t *testing.T) {
	detector := &mockDriftDetector{
		called: false,
	}
	d := New(nil, detector)

	plan, err := d.Diff(context.Background(), nil, testManifest())
	require.NoError(t, err)

	assert.False(t, detector.called, "drift detector should not be called when no last-applied manifest")
	assert.Empty(t, plan.DriftWarnings)
}

func TestDiff_DriftDetector_ErrorPropagates(t *testing.T) {
	detector := &mockDriftDetector{
		err: fmt.Errorf("database unreachable"),
	}
	d := New(nil, detector)
	manifest := testManifest()

	_, err := d.Diff(context.Background(), manifest, manifest)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database unreachable")
}

func TestDiff_DeterministicOrdering(t *testing.T) {
	d := New(nil, nil)

	plan, err := d.Diff(context.Background(), nil, testManifest())
	require.NoError(t, err)

	// Verify actions are sorted by resource type then code
	for i := 1; i < len(plan.Actions); i++ {
		prev := plan.Actions[i-1]
		curr := plan.Actions[i]
		if prev.ResourceType == curr.ResourceType {
			assert.LessOrEqual(t, prev.ResourceCode, curr.ResourceCode,
				"actions within same resource type should be sorted by code")
		}
	}
}

func TestDiffPlan_Summary(t *testing.T) {
	plan := &DiffPlan{
		Actions: []PlannedAction{
			{Action: ActionCreate},
			{Action: ActionCreate},
			{Action: ActionUpdate},
			{Action: ActionNoChange},
			{Action: ActionNoChange},
			{Action: ActionNoChange},
		},
	}

	summary := plan.Summary()
	assert.Equal(t, "2 to create, 1 to update, 0 to delete, 3 no-change", summary)
}

func TestDiffPlan_BlockedDeletionErrors(t *testing.T) {
	plan := &DiffPlan{
		BlockedDeletions: []BlockedDeletion{
			{
				ResourceType: ResourceAccountType,
				ResourceCode: "CUSTOMER_PREPAID",
				Reason:       "1,234 accounts with balances exist",
			},
			{
				ResourceType: ResourceInstrument,
				ResourceCode: "KWH",
				Reason:       "referenced by active valuation rules",
			},
		},
	}

	errors := plan.BlockedDeletionErrors()
	assert.Len(t, errors, 2)
	assert.Equal(t, "Cannot delete account_type CUSTOMER_PREPAID: 1,234 accounts with balances exist", errors[0])
	assert.Equal(t, "Cannot delete instrument KWH: referenced by active valuation rules", errors[1])
}

func TestDiff_BreakingChangeFlagging(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Instruments = newManifest.Instruments[:1] // remove KWH

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	assert.True(t, plan.HasBreakingChanges)

	for _, action := range plan.Actions {
		if action.Action == ActionDelete {
			assert.True(t, action.Breaking, "DELETE actions should be flagged as breaking")
		}
		if action.Action == ActionNoChange || action.Action == ActionCreate {
			assert.False(t, action.Breaking, "non-DELETE actions should not be breaking")
		}
	}
}

func TestValRuleKey(t *testing.T) {
	assert.Equal(t, "KWH->GBP", valRuleKey("KWH", "GBP"))
	assert.Equal(t, "EUR->GBP", valRuleKey("eur", "gbp"))
	assert.Equal(t, "A->B", valRuleKey("a", "B"))
}

// --- Mock implementations ---

type mockSafetyChecker struct {
	accountBlocked    map[string]*BlockedDeletion
	instrumentBlocked map[string]*BlockedDeletion
	sagaBlocked       map[string]*BlockedDeletion
	err               error
}

func (m *mockSafetyChecker) CheckAccountTypeDeletion(_ context.Context, code string) (*BlockedDeletion, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.accountBlocked != nil {
		return m.accountBlocked[code], nil
	}
	return nil, nil
}

func (m *mockSafetyChecker) CheckInstrumentDeletion(_ context.Context, code string) (*BlockedDeletion, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.instrumentBlocked != nil {
		return m.instrumentBlocked[code], nil
	}
	return nil, nil
}

func (m *mockSafetyChecker) CheckSagaDeletion(_ context.Context, name string) (*BlockedDeletion, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.sagaBlocked != nil {
		return m.sagaBlocked[name], nil
	}
	return nil, nil
}

type mockDriftDetector struct {
	warnings []DriftWarning
	err      error
	called   bool
}

func (m *mockDriftDetector) DetectDrift(_ context.Context, _ *controlplanev1.Manifest) ([]DriftWarning, error) {
	m.called = true
	if m.err != nil {
		return nil, m.err
	}
	return m.warnings, nil
}

// --- Test helpers ---

func filterActions(actions []PlannedAction, actionType ActionType) []PlannedAction {
	var result []PlannedAction
	for _, a := range actions {
		if a.Action == actionType {
			result = append(result, a)
		}
	}
	return result
}

func filterActionsByResource(actions []PlannedAction, actionType ActionType, resourceType ResourceType) []PlannedAction {
	var result []PlannedAction
	for _, a := range actions {
		if a.Action == actionType && a.ResourceType == resourceType {
			result = append(result, a)
		}
	}
	return result
}

// --- Party type differ tests ---

func testPartyTypeDefinition(tenantID, partyType, schema string) *partyv1.PartyTypeDefinition {
	return &partyv1.PartyTypeDefinition{
		TenantId:        tenantID,
		PartyType:       partyType,
		AttributeSchema: schema,
	}
}

func TestDiff_PartyTypeAdded_Create(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		testPartyTypeDefinition("tenant-1", "PERSON", `{"type":"object"}`),
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourcePartyType)
	assert.Len(t, creates, 1)
	assert.Equal(t, "tenant-1:PERSON", creates[0].ResourceCode)
	assert.Contains(t, creates[0].Description, "Create party type")
}

func TestDiff_PartyTypeRemoved_Delete(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		testPartyTypeDefinition("tenant-1", "PERSON", `{"type":"object"}`),
	}

	newManifest := testManifest()
	// No party types in new manifest

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourcePartyType)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "tenant-1:PERSON", deletes[0].ResourceCode)
	assert.True(t, deletes[0].Breaking)
	assert.True(t, plan.HasBreakingChanges)
}

func TestDiff_PartyTypeUnchanged_NoChange(t *testing.T) {
	d := New(nil, nil)
	partyType := testPartyTypeDefinition("tenant-1", "ORGANIZATION", `{"type":"object","properties":{}}`)
	manifest := testManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{partyType}

	plan, err := d.Diff(context.Background(), manifest, manifest)
	require.NoError(t, err)

	noChanges := filterActionsByResource(plan.Actions, ActionNoChange, ResourcePartyType)
	assert.Len(t, noChanges, 1)
	assert.Equal(t, "tenant-1:ORGANIZATION", noChanges[0].ResourceCode)
}

func TestDiff_PartyTypeModifiedSchema_Update(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		testPartyTypeDefinition("tenant-1", "PERSON", `{"type":"object"}`),
	}

	newManifest := testManifest()
	newManifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		testPartyTypeDefinition("tenant-1", "PERSON", `{"type":"object","properties":{"name":{"type":"string"}}}`),
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourcePartyType)
	assert.Len(t, updates, 1)
	assert.Equal(t, "tenant-1:PERSON", updates[0].ResourceCode)
	assert.Contains(t, updates[0].Description, "attribute_schema changed")
}

func TestDiff_PartyTypeModifiedCEL_DescribesChange(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{"type":"object"}`,
			ValidationCel:   "attributes.age > 18",
		},
	}

	newManifest := testManifest()
	newManifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{"type":"object"}`,
			ValidationCel:   "attributes.age > 21",
		},
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourcePartyType)
	assert.Len(t, updates, 1)
	assert.Contains(t, updates[0].Description, "validation_cel changed")
}

func TestDiff_MultiplePartyTypes_DifferentTenants(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		testPartyTypeDefinition("tenant-1", "PERSON", `{"type":"object"}`),
		testPartyTypeDefinition("tenant-2", "PERSON", `{"type":"object"}`),
		testPartyTypeDefinition("tenant-1", "ORGANIZATION", `{"type":"object"}`),
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourcePartyType)
	assert.Len(t, creates, 3, "all three party types should be created")
}

func TestDiff_PartyTypeKey_IsCompositeOfTenantAndType(t *testing.T) {
	// Same party_type, different tenants = different keys
	assert.NotEqual(t, partyTypeKey("tenant-1", "PERSON"), partyTypeKey("tenant-2", "PERSON"))
	// Same tenant, different party_types = different keys
	assert.NotEqual(t, partyTypeKey("tenant-1", "PERSON"), partyTypeKey("tenant-1", "ORGANIZATION"))
	// Same values = same key
	assert.Equal(t, partyTypeKey("tenant-1", "PERSON"), partyTypeKey("tenant-1", "PERSON"))
}

func TestDiff_NilLastApplied_WithPartyTypes_AllCreates(t *testing.T) {
	d := New(nil, nil)
	manifest := testManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		testPartyTypeDefinition("tenant-1", "PERSON", `{"type":"object"}`),
	}

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourcePartyType)
	assert.Len(t, creates, 1)
	assert.Equal(t, "tenant-1:PERSON", creates[0].ResourceCode)
}

func TestDiff_AddedEventSaga_Create(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	filter := `event.amount > 0 && event.currency == "GBP"`
	newManifest := testManifest()
	newManifest.Sagas = append(newManifest.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_transaction_captured",
		Trigger: "event:position-keeping.transaction-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		Filter:  &filter,
	})

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceSaga)
	assert.Len(t, creates, 1)
	assert.Equal(t, "on_transaction_captured", creates[0].ResourceCode)
	assert.Contains(t, creates[0].Description, "trigger: event:")
}

func TestDiff_ModifiedSagaFilter_DescribesChange(t *testing.T) {
	d := New(nil, nil)

	filter := `event.amount > 0`
	oldManifest := testManifest()
	oldManifest.Sagas[0].Trigger = "event:position-keeping.transaction-captured.v1"
	oldManifest.Sagas[0].Filter = &filter

	newFilter := `event.amount > 100`
	newManifest := testManifest()
	newManifest.Sagas[0].Trigger = "event:position-keeping.transaction-captured.v1"
	newManifest.Sagas[0].Filter = &newFilter

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceSaga)
	assert.Len(t, updates, 1)
	assert.Contains(t, updates[0].Description, "filter changed")
}

func TestDiff_WithSkipSafetyChecks_SkipsSafetyChecksAndBreakingFlags(t *testing.T) {
	checker := &mockSafetyChecker{
		instrumentBlocked: map[string]*BlockedDeletion{
			"KWH": {
				ResourceType: ResourceInstrument,
				ResourceCode: "KWH",
				Reason:       "referenced by 3 active valuation rules",
			},
		},
	}
	d := New(checker, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Instruments = newManifest.Instruments[:1] // remove KWH

	plan, err := d.Diff(context.Background(), oldManifest, newManifest, WithSkipSafetyChecks())
	require.NoError(t, err)

	// DELETE actions should still be present
	deletes := filterActions(plan.Actions, ActionDelete)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "KWH", deletes[0].ResourceCode)

	// But they should NOT be flagged as breaking
	assert.False(t, deletes[0].Breaking)
	assert.False(t, plan.HasBreakingChanges)

	// And no blocked deletions should be recorded (safety checks skipped)
	assert.False(t, plan.HasBlockedDeletions())
	assert.Empty(t, plan.BlockedDeletions)
}

func TestDiff_WithSkipSafetyChecks_SafetyCheckerErrorNotReturned(t *testing.T) {
	checker := &mockSafetyChecker{
		err: fmt.Errorf("connection refused"),
	}
	d := New(checker, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.AccountTypes = nil

	// With skip, the safety checker error should not propagate
	plan, err := d.Diff(context.Background(), oldManifest, newManifest, WithSkipSafetyChecks())
	require.NoError(t, err)
	assert.NotNil(t, plan)
}

func TestDiff_WithoutSkipSafetyChecks_SafetyChecksStillRun(t *testing.T) {
	checker := &mockSafetyChecker{
		instrumentBlocked: map[string]*BlockedDeletion{
			"KWH": {
				ResourceType: ResourceInstrument,
				ResourceCode: "KWH",
				Reason:       "referenced by active rules",
			},
		},
	}
	d := New(checker, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Instruments = newManifest.Instruments[:1]

	// Without skip option, safety checks run as normal
	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	assert.True(t, plan.HasBlockedDeletions())
	assert.True(t, plan.HasBreakingChanges)
}

// --- Market Data Source differ tests ---

func TestDiff_MarketDataSourceAdded_Create(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "BLOOMBERG", Name: "Bloomberg Terminal", TrustLevel: 90},
		},
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceMarketDataSource)
	assert.Len(t, creates, 1)
	assert.Equal(t, "BLOOMBERG", creates[0].ResourceCode)
	assert.Contains(t, creates[0].Description, "Bloomberg Terminal")
}

func TestDiff_MarketDataSourceRemoved_Delete(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "ECB", Name: "European Central Bank", TrustLevel: 95},
		},
	}

	newManifest := testManifest()

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceMarketDataSource)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "ECB", deletes[0].ResourceCode)
}

func TestDiff_MarketDataSourceModified_Update(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "BLOOMBERG", Name: "Bloomberg Terminal", TrustLevel: 90},
		},
	}

	newManifest := testManifest()
	newManifest.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "BLOOMBERG", Name: "Bloomberg Terminal (Updated)", TrustLevel: 95},
		},
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceMarketDataSource)
	assert.Len(t, updates, 1)
	assert.Equal(t, "BLOOMBERG", updates[0].ResourceCode)
	assert.Contains(t, updates[0].Description, "name:")
	assert.Contains(t, updates[0].Description, "trust_level:")
}

func TestDiff_MarketDataSourceUnchanged_NoChange(t *testing.T) {
	d := New(nil, nil)
	manifest := testManifest()
	manifest.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "ECB", Name: "ECB", TrustLevel: 100},
		},
	}

	plan, err := d.Diff(context.Background(), manifest, manifest)
	require.NoError(t, err)

	noChanges := filterActionsByResource(plan.Actions, ActionNoChange, ResourceMarketDataSource)
	assert.Len(t, noChanges, 1)
	assert.Equal(t, "ECB", noChanges[0].ResourceCode)
}

// --- Market Data Set differ tests ---

func TestDiff_MarketDataSetAdded_Create(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.MarketData = &controlplanev1.MarketDataConfig{
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{
				Code:       "USD_EUR_FX",
				Category:   marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:       "USD/EUR",
				SourceCode: "ECB",
			},
		},
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceMarketDataSet)
	assert.Len(t, creates, 1)
	assert.Equal(t, "USD_EUR_FX", creates[0].ResourceCode)
	assert.Contains(t, creates[0].Description, "USD/EUR")
}

func TestDiff_MarketDataSetRemoved_Delete(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.MarketData = &controlplanev1.MarketDataConfig{
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{
				Code:       "BRENT_CRUDE",
				Category:   marketinformationv1.DataCategory_DATA_CATEGORY_COMMODITY_PRICE,
				Unit:       "USD/BBL",
				SourceCode: "REUTERS",
			},
		},
	}

	newManifest := testManifest()

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceMarketDataSet)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "BRENT_CRUDE", deletes[0].ResourceCode)
}

func TestDiff_MarketDataSetModified_Update(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.MarketData = &controlplanev1.MarketDataConfig{
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{
				Code:       "USD_EUR_FX",
				Category:   marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:       "USD/EUR",
				SourceCode: "ECB",
			},
		},
	}

	newManifest := testManifest()
	newManifest.MarketData = &controlplanev1.MarketDataConfig{
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{
				Code:       "USD_EUR_FX",
				Category:   marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:       "EUR/USD",
				SourceCode: "BLOOMBERG",
			},
		},
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceMarketDataSet)
	assert.Len(t, updates, 1)
	assert.Equal(t, "USD_EUR_FX", updates[0].ResourceCode)
	assert.Contains(t, updates[0].Description, "unit:")
	assert.Contains(t, updates[0].Description, "source_code:")
}

// --- Organization differ tests ---

func TestDiff_OrganizationAdded_Create(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "ACME_ENERGY", Name: "Acme Energy Ltd", PartyType: "ORGANIZATION"},
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceOrganization)
	assert.Len(t, creates, 1)
	assert.Equal(t, "ACME_ENERGY", creates[0].ResourceCode)
	assert.Contains(t, creates[0].Description, "Acme Energy Ltd")
}

func TestDiff_OrganizationRemoved_Delete(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "GRID_OPS", Name: "Grid Operations", PartyType: "ORGANIZATION"},
	}

	newManifest := testManifest()

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceOrganization)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "GRID_OPS", deletes[0].ResourceCode)
}

func TestDiff_OrganizationModified_Update(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "ACME_ENERGY", Name: "Acme Energy Ltd", PartyType: "ORGANIZATION"},
	}

	newManifest := testManifest()
	newManifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "ACME_ENERGY", Name: "Acme Energy PLC", PartyType: "COUNTERPARTY"},
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceOrganization)
	assert.Len(t, updates, 1)
	assert.Equal(t, "ACME_ENERGY", updates[0].ResourceCode)
	assert.Contains(t, updates[0].Description, "name:")
	assert.Contains(t, updates[0].Description, "party_type:")
}

func TestDiff_OrganizationUnchanged_NoChange(t *testing.T) {
	d := New(nil, nil)
	manifest := testManifest()
	manifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "ACME_ENERGY", Name: "Acme Energy Ltd", PartyType: "ORGANIZATION"},
	}

	plan, err := d.Diff(context.Background(), manifest, manifest)
	require.NoError(t, err)

	noChanges := filterActionsByResource(plan.Actions, ActionNoChange, ResourceOrganization)
	assert.Len(t, noChanges, 1)
	assert.Equal(t, "ACME_ENERGY", noChanges[0].ResourceCode)
}

// --- Internal Account differ tests ---

func TestDiff_InternalAccountAdded_Create(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()

	newManifest := testManifest()
	newManifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "REVENUE", Instrument: "GBP"},
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceInternalAccount)
	assert.Len(t, creates, 1)
	assert.Equal(t, "REVENUE_GBP", creates[0].ResourceCode)
	assert.Contains(t, creates[0].Description, "REVENUE")
	assert.Contains(t, creates[0].Description, "GBP")
}

func TestDiff_InternalAccountRemoved_Delete(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "SETTLEMENT_KWH", AccountType: "SETTLEMENT", Instrument: "KWH"},
	}

	newManifest := testManifest()

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceInternalAccount)
	assert.Len(t, deletes, 1)
	assert.Equal(t, "SETTLEMENT_KWH", deletes[0].ResourceCode)
}

func TestDiff_InternalAccountModified_Update(t *testing.T) {
	d := New(nil, nil)
	oldManifest := testManifest()
	oldManifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "REVENUE", Instrument: "GBP"},
	}

	newManifest := testManifest()
	newManifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "CURRENT", Instrument: "GBP", OwnerOrganization: "ACME"},
	}

	plan, err := d.Diff(context.Background(), oldManifest, newManifest)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceInternalAccount)
	assert.Len(t, updates, 1)
	assert.Equal(t, "REVENUE_GBP", updates[0].ResourceCode)
	assert.Contains(t, updates[0].Description, "account_type:")
	assert.Contains(t, updates[0].Description, "owner_organization:")
}

func TestDiff_InternalAccountUnchanged_NoChange(t *testing.T) {
	d := New(nil, nil)
	manifest := testManifest()
	manifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "REVENUE", Instrument: "GBP"},
	}

	plan, err := d.Diff(context.Background(), manifest, manifest)
	require.NoError(t, err)

	noChanges := filterActionsByResource(plan.Actions, ActionNoChange, ResourceInternalAccount)
	assert.Len(t, noChanges, 1)
	assert.Equal(t, "REVENUE_GBP", noChanges[0].ResourceCode)
}
