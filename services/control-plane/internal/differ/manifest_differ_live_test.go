package differ

import (
	"context"
	"fmt"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLiveStateProvider implements LiveStateProvider for tests.
type mockLiveStateProvider struct {
	state *LiveState
	err   error
}

func (m *mockLiveStateProvider) QueryLiveState(_ context.Context, _ string) (*LiveState, error) {
	return m.state, m.err
}

func TestDiffAgainstLiveState_NilManifest(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{}}
	d := New(nil, nil, provider)

	_, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", nil)
	assert.ErrorIs(t, err, ErrNilManifest)
}

func TestDiffAgainstLiveState_NoProvider(t *testing.T) {
	d := New(nil, nil, nil)

	_, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", &controlplanev1.Manifest{})
	assert.ErrorIs(t, err, ErrNoLiveStateProvider)
}

func TestDiffAgainstLiveState_QueryFailure(t *testing.T) {
	provider := &mockLiveStateProvider{err: fmt.Errorf("connection refused")}
	d := New(nil, nil, provider)

	_, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", &controlplanev1.Manifest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query live state")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestDiffAgainstLiveState_EmptyLiveState_AllCreate(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "CURRENT", Name: "Current Account"},
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	creates := filterActions(plan.Actions, ActionCreate)
	assert.Len(t, creates, 2)
	assertActionExists(t, creates, ResourceInstrument, "GBP", ActionCreate)
	assertActionExists(t, creates, ResourceAccountType, "CURRENT", ActionCreate)
}

func TestDiffAgainstLiveState_Instruments_Create(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	require.Len(t, plan.Actions, 1)
	assert.Equal(t, ActionCreate, plan.Actions[0].Action)
	assert.Equal(t, ResourceInstrument, plan.Actions[0].ResourceType)
	assert.Equal(t, "GBP", plan.Actions[0].ResourceCode)
}

func TestDiffAgainstLiveState_Instruments_Update(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "Pound Sterling", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	require.Len(t, plan.Actions, 1)
	assert.Equal(t, ActionUpdate, plan.Actions[0].Action)
	assert.Equal(t, "GBP", plan.Actions[0].ResourceCode)
}

func TestDiffAgainstLiveState_Instruments_NoChange(t *testing.T) {
	inst := &controlplanev1.InstrumentDefinition{
		Code: "GBP", Name: "British Pound", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
	}
	provider := &mockLiveStateProvider{state: &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{inst},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	require.Len(t, plan.Actions, 1)
	assert.Equal(t, ActionNoChange, plan.Actions[0].Action)
}

func TestDiffAgainstLiveState_Instruments_Deprecate(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "OLD_INST", Name: "Old Instrument"},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	require.Len(t, plan.Actions, 1)
	assert.Equal(t, ActionDeprecate, plan.Actions[0].Action)
	assert.Equal(t, ResourceInstrument, plan.Actions[0].ResourceType)
	assert.Equal(t, "OLD_INST", plan.Actions[0].ResourceCode)
	assert.Contains(t, plan.Actions[0].Description, "Deprecate")
}

func TestDiffAgainstLiveState_SystemInstruments_Filtered(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "SYS_INST", Name: "System Instrument"},
			{Code: "TENANT_INST", Name: "Tenant Instrument"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceInstrument: {"SYS_INST": true},
		},
	}}
	d := New(nil, nil, provider)

	// Manifest doesn't include either - SYS_INST should be filtered, TENANT_INST should be DEPRECATE
	manifest := &controlplanev1.Manifest{}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	require.Len(t, plan.Actions, 1)
	assert.Equal(t, ActionDeprecate, plan.Actions[0].Action)
	assert.Equal(t, "TENANT_INST", plan.Actions[0].ResourceCode)
}

func TestDiffAgainstLiveState_SystemInstruments_NotMatchedForUpdate(t *testing.T) {
	// System instrument in live state should not match manifest instrument for NO_CHANGE
	provider := &mockLiveStateProvider{state: &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceInstrument: {"GBP": true},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	// GBP in live is system, so it's filtered - manifest GBP should be CREATE
	require.Len(t, plan.Actions, 1)
	assert.Equal(t, ActionCreate, plan.Actions[0].Action)
	assert.Equal(t, "GBP", plan.Actions[0].ResourceCode)
}

func TestDiffAgainstLiveState_AccountTypes_AllActions(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "CURRENT", Name: "Current Account", NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT},
			{Code: "SAVINGS", Name: "Savings Account", NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT},
			{Code: "OLD_TYPE", Name: "Old Type"},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "CURRENT", Name: "Current Account", NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT}, // NO_CHANGE
			{Code: "SAVINGS", Name: "Updated Savings", NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT}, // UPDATE
			{Code: "NEW_TYPE", Name: "New Type"}, // CREATE
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	assert.Len(t, plan.Actions, 4)
	assertActionExists(t, plan.Actions, ResourceAccountType, "CURRENT", ActionNoChange)
	assertActionExists(t, plan.Actions, ResourceAccountType, "SAVINGS", ActionUpdate)
	assertActionExists(t, plan.Actions, ResourceAccountType, "NEW_TYPE", ActionCreate)
	assertActionExists(t, plan.Actions, ResourceAccountType, "OLD_TYPE", ActionDeprecate)
}

func TestDiffAgainstLiveState_SystemAccountTypes_Filtered(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "SYSTEM_AT", Name: "System Account Type"},
			{Code: "TENANT_AT", Name: "Tenant Account Type"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceAccountType: {"SYSTEM_AT": true},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	require.Len(t, plan.Actions, 1)
	assert.Equal(t, "TENANT_AT", plan.Actions[0].ResourceCode)
	assert.Equal(t, ActionDeprecate, plan.Actions[0].Action)
}

func TestDiffAgainstLiveState_Sagas_AllActions(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "existing_saga", Trigger: "api:/v1/settle", Script: "old_script"},
			{Name: "unchanged_saga", Trigger: "event:foo", Script: "script"},
			{Name: "removed_saga", Trigger: "scheduled:daily", Script: "old"},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "existing_saga", Trigger: "api:/v1/settle", Script: "new_script"}, // UPDATE
			{Name: "unchanged_saga", Trigger: "event:foo", Script: "script"},          // NO_CHANGE
			{Name: "new_saga", Trigger: "webhook:stripe", Script: "new"},              // CREATE
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	assert.Len(t, plan.Actions, 4)
	assertActionExists(t, plan.Actions, ResourceSaga, "existing_saga", ActionUpdate)
	assertActionExists(t, plan.Actions, ResourceSaga, "unchanged_saga", ActionNoChange)
	assertActionExists(t, plan.Actions, ResourceSaga, "new_saga", ActionCreate)
	assertActionExists(t, plan.Actions, ResourceSaga, "removed_saga", ActionDeprecate)
}

func TestDiffAgainstLiveState_SystemSagas_Filtered(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "system_saga", Trigger: "event:sys", Script: "sys_script"},
			{Name: "tenant_saga", Trigger: "api:/v1/pay", Script: "pay_script"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceSaga: {"system_saga": true},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	require.Len(t, plan.Actions, 1)
	assert.Equal(t, "tenant_saga", plan.Actions[0].ResourceCode)
	assert.Equal(t, ActionDeprecate, plan.Actions[0].Action)
}

func TestDiffAgainstLiveState_MarketDataSources(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		MarketDataSources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "ECB", Name: "European Central Bank", TrustLevel: 10},
			{Code: "OLD_SRC", Name: "Old Source"},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		MarketData: &controlplanev1.MarketDataConfig{
			Sources: []*controlplanev1.MarketDataSourceDefinition{
				{Code: "ECB", Name: "ECB Updated", TrustLevel: 10},
				{Code: "NEW_SRC", Name: "New Source"},
			},
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	assert.Len(t, plan.Actions, 3)
	assertActionExists(t, plan.Actions, ResourceMarketDataSource, "ECB", ActionUpdate)
	assertActionExists(t, plan.Actions, ResourceMarketDataSource, "NEW_SRC", ActionCreate)
	assertActionExists(t, plan.Actions, ResourceMarketDataSource, "OLD_SRC", ActionDeprecate)
}

func TestDiffAgainstLiveState_MarketDataSets(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		MarketDataSets: []*controlplanev1.MarketDataSetDefinition{
			{Code: "GBP_USD", Unit: "rate"},
			{Code: "OLD_SET", Unit: "price"},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		MarketData: &controlplanev1.MarketDataConfig{
			Datasets: []*controlplanev1.MarketDataSetDefinition{
				{Code: "GBP_USD", Unit: "rate"},
				{Code: "NEW_SET", Unit: "price"},
			},
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	assert.Len(t, plan.Actions, 3)
	assertActionExists(t, plan.Actions, ResourceMarketDataSet, "GBP_USD", ActionNoChange)
	assertActionExists(t, plan.Actions, ResourceMarketDataSet, "NEW_SET", ActionCreate)
	assertActionExists(t, plan.Actions, ResourceMarketDataSet, "OLD_SET", ActionDeprecate)
}

func TestDiffAgainstLiveState_Organizations(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		Organizations: []*controlplanev1.OrganizationDefinition{
			{Code: "ORG_A", Name: "Organization A"},
			{Code: "ORG_OLD", Name: "Old Org"},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		Organizations: []*controlplanev1.OrganizationDefinition{
			{Code: "ORG_A", Name: "Organization A"},     // NO_CHANGE
			{Code: "ORG_NEW", Name: "New Organization"}, // CREATE
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	assert.Len(t, plan.Actions, 3)
	assertActionExists(t, plan.Actions, ResourceOrganization, "ORG_A", ActionNoChange)
	assertActionExists(t, plan.Actions, ResourceOrganization, "ORG_NEW", ActionCreate)
	assertActionExists(t, plan.Actions, ResourceOrganization, "ORG_OLD", ActionDeprecate)
}

func TestDiffAgainstLiveState_InternalAccounts(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		InternalAccounts: []*controlplanev1.InternalAccountDefinition{
			{Code: "SUSPENSE", AccountType: "CURRENT", Instrument: "GBP"},
			{Code: "OLD_ACCT", AccountType: "SAVINGS", Instrument: "USD"},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		InternalAccounts: []*controlplanev1.InternalAccountDefinition{
			{Code: "SUSPENSE", AccountType: "CURRENT", Instrument: "GBP"},      // NO_CHANGE
			{Code: "NEW_ACCT", AccountType: "CURRENT", Instrument: "EUR"}, // CREATE
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	assert.Len(t, plan.Actions, 3)
	assertActionExists(t, plan.Actions, ResourceInternalAccount, "SUSPENSE", ActionNoChange)
	assertActionExists(t, plan.Actions, ResourceInternalAccount, "NEW_ACCT", ActionCreate)
	assertActionExists(t, plan.Actions, ResourceInternalAccount, "OLD_ACCT", ActionDeprecate)
}

func TestDiffAgainstLiveState_ProviderConnections(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "stripe-1", ProviderName: "stripe"},
			{ConnectionId: "old-conn", ProviderName: "legacy"},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
				{ConnectionId: "stripe-1", ProviderName: "stripe"},         // NO_CHANGE
				{ConnectionId: "new-conn", ProviderName: "new-provider"}, // CREATE
			},
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	assert.Len(t, plan.Actions, 3)
	assertActionExists(t, plan.Actions, ResourceProviderConnection, "stripe-1", ActionNoChange)
	assertActionExists(t, plan.Actions, ResourceProviderConnection, "new-conn", ActionCreate)
	assertActionExists(t, plan.Actions, ResourceProviderConnection, "old-conn", ActionDeprecate)
}

func TestDiffAgainstLiveState_InstructionRoutes(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{InstructionType: "payment", ConnectionId: "stripe-1"},
			{InstructionType: "old-route", ConnectionId: "legacy"},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
				{InstructionType: "payment", ConnectionId: "stripe-1"},       // NO_CHANGE
				{InstructionType: "new-route", ConnectionId: "new-conn"}, // CREATE
			},
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	assert.Len(t, plan.Actions, 3)
	assertActionExists(t, plan.Actions, ResourceInstructionRoute, "payment", ActionNoChange)
	assertActionExists(t, plan.Actions, ResourceInstructionRoute, "new-route", ActionCreate)
	assertActionExists(t, plan.Actions, ResourceInstructionRoute, "old-route", ActionDeprecate)
}

func TestDiffAgainstLiveState_MultipleSystemCodeTypes(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "SYS_GBP", Name: "System GBP"},
			{Code: "TENANT_KWH", Name: "Tenant kWh"},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "SYS_CURRENT", Name: "System Current"},
			{Code: "TENANT_SAVINGS", Name: "Tenant Savings"},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "sys_saga", Trigger: "event:sys", Script: "s"},
			{Name: "tenant_saga", Trigger: "api:/pay", Script: "p"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceInstrument:  {"SYS_GBP": true},
			ResourceAccountType: {"SYS_CURRENT": true},
			ResourceSaga:        {"sys_saga": true},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	// Only tenant resources should appear as DEPRECATE - 3 total
	assert.Len(t, plan.Actions, 3)
	for _, a := range plan.Actions {
		assert.Equal(t, ActionDeprecate, a.Action)
		assert.NotContains(t, a.ResourceCode, "SYS")
	}
}

func TestDiffAgainstLiveState_NilSystemCodes(t *testing.T) {
	// When SystemCodes is nil, no filtering should happen
	provider := &mockLiveStateProvider{state: &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	require.Len(t, plan.Actions, 1)
	assert.Equal(t, ActionDeprecate, plan.Actions[0].Action)
	assert.Equal(t, "GBP", plan.Actions[0].ResourceCode)
}

func TestDiffAgainstLiveState_ResultsSorted(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "ZZZ", Name: "Z Instrument"},
			{Code: "AAA", Name: "A Instrument"},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "BBB", Name: "B Type"},
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	require.Len(t, plan.Actions, 3)
	// Should be sorted by resource type then code
	assert.Equal(t, ResourceAccountType, plan.Actions[0].ResourceType)
	assert.Equal(t, "BBB", plan.Actions[0].ResourceCode)
	assert.Equal(t, ResourceInstrument, plan.Actions[1].ResourceType)
	assert.Equal(t, "AAA", plan.Actions[1].ResourceCode)
	assert.Equal(t, ResourceInstrument, plan.Actions[2].ResourceType)
	assert.Equal(t, "ZZZ", plan.Actions[2].ResourceCode)
}

func TestDiffAgainstLiveState_FullMixedScenario(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
			{Code: "KWH", Name: "Kilowatt Hour", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY},
			{Code: "SYS_FIAT", Name: "System Fiat"},
			{Code: "LEGACY", Name: "Legacy Instrument"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceInstrument: {"SYS_FIAT": true},
		},
	}}
	d := New(nil, nil, provider)

	manifest := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},          // NO_CHANGE
			{Code: "KWH", Name: "Kilowatt-Hour Updated", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY}, // UPDATE
			{Code: "CO2", Name: "Carbon Credit", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_VOUCHER}, // CREATE
		},
	}

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", manifest)
	require.NoError(t, err)

	assert.Len(t, plan.Actions, 4)
	assertActionExists(t, plan.Actions, ResourceInstrument, "GBP", ActionNoChange)
	assertActionExists(t, plan.Actions, ResourceInstrument, "KWH", ActionUpdate)
	assertActionExists(t, plan.Actions, ResourceInstrument, "CO2", ActionCreate)
	assertActionExists(t, plan.Actions, ResourceInstrument, "LEGACY", ActionDeprecate)
	// SYS_FIAT should NOT appear at all
	for _, a := range plan.Actions {
		assert.NotEqual(t, "SYS_FIAT", a.ResourceCode)
	}
}

func TestDiffAgainstLiveState_EmptyManifestAndLive(t *testing.T) {
	provider := &mockLiveStateProvider{state: &LiveState{}}
	d := New(nil, nil, provider)

	plan, err := d.DiffAgainstLiveState(context.Background(), "tenant-1", &controlplanev1.Manifest{})
	require.NoError(t, err)
	assert.Empty(t, plan.Actions)
}

// --- helpers ---

func assertActionExists(t *testing.T, actions []PlannedAction, rt ResourceType, code string, action ActionType) {
	t.Helper()
	for _, a := range actions {
		if a.ResourceType == rt && a.ResourceCode == code && a.Action == action {
			return
		}
	}
	t.Errorf("expected action %s for %s %s, not found in %d actions", action, rt, code, len(actions))
}
