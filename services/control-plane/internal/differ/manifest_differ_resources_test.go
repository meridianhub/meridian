package differ

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── diffInstruments ─────────────────────────────────────────────────────────

func TestDiffInstruments_Create(t *testing.T) {
	d := New(nil, nil)
	manifest := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
		},
	}

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceInstrument)
	require.Len(t, creates, 1)
	assert.Equal(t, "GBP", creates[0].ResourceCode)
}

func TestDiffInstruments_Delete(t *testing.T) {
	d := New(nil, nil)
	last := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
			{Code: "USD", Name: "US Dollar"},
		},
	}
	next := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
		},
	}

	plan, err := d.Diff(context.Background(), last, next)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceInstrument)
	require.Len(t, deletes, 1)
	assert.Equal(t, "USD", deletes[0].ResourceCode)
}

func TestDiffInstruments_Update(t *testing.T) {
	d := New(nil, nil)
	last := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "Pound"},
		},
	}
	next := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
		},
	}

	plan, err := d.Diff(context.Background(), last, next)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceInstrument)
	require.Len(t, updates, 1)
	assert.Contains(t, updates[0].Description, "GBP")
}

func TestDiffInstruments_NoChange(t *testing.T) {
	d := New(nil, nil)
	manifest := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
		},
	}

	plan, err := d.Diff(context.Background(), manifest, manifest)
	require.NoError(t, err)

	noChanges := filterActionsByResource(plan.Actions, ActionNoChange, ResourceInstrument)
	assert.Len(t, noChanges, 1)
}

// ─── diffAccountTypes ────────────────────────────────────────────────────────

func TestDiffAccountTypes_Create(t *testing.T) {
	d := New(nil, nil)
	manifest := &controlplanev1.Manifest{
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "SETTLEMENT", Name: "Settlement"},
		},
	}

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceAccountType)
	require.Len(t, creates, 1)
	assert.Equal(t, "SETTLEMENT", creates[0].ResourceCode)
}

func TestDiffAccountTypes_Delete(t *testing.T) {
	d := New(nil, nil)
	last := &controlplanev1.Manifest{
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "SETTLEMENT", Name: "Settlement"},
			{Code: "CLEARING", Name: "Clearing"},
		},
	}
	next := &controlplanev1.Manifest{
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "SETTLEMENT", Name: "Settlement"},
		},
	}

	plan, err := d.Diff(context.Background(), last, next)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceAccountType)
	require.Len(t, deletes, 1)
	assert.Equal(t, "CLEARING", deletes[0].ResourceCode)
}

// ─── diffValuationRules ──────────────────────────────────────────────────────

func TestDiffValuationRules_Create(t *testing.T) {
	d := New(nil, nil)
	manifest := &controlplanev1.Manifest{
		ValuationRules: []*controlplanev1.ValuationRule{
			{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
			},
		},
	}

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceValuationRule)
	require.Len(t, creates, 1)
	assert.Equal(t, "KWH->GBP", creates[0].ResourceCode)
}

func TestDiffValuationRules_Delete(t *testing.T) {
	d := New(nil, nil)
	last := &controlplanev1.Manifest{
		ValuationRules: []*controlplanev1.ValuationRule{
			{FromInstrument: "KWH", ToInstrument: "GBP"},
			{FromInstrument: "EUR", ToInstrument: "GBP"},
		},
	}
	next := &controlplanev1.Manifest{
		ValuationRules: []*controlplanev1.ValuationRule{
			{FromInstrument: "KWH", ToInstrument: "GBP"},
		},
	}

	plan, err := d.Diff(context.Background(), last, next)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceValuationRule)
	require.Len(t, deletes, 1)
	assert.Equal(t, "EUR->GBP", deletes[0].ResourceCode)
}

// ─── diffSagas ───────────────────────────────────────────────────────────────

func TestDiffSagas_Create(t *testing.T) {
	d := New(nil, nil)
	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "process_order", Trigger: "api:/v1/orders", Script: "def execute(ctx): pass"},
		},
	}

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceSaga)
	require.Len(t, creates, 1)
	assert.Equal(t, "process_order", creates[0].ResourceCode)
}

func TestDiffSagas_Update_ScriptChange(t *testing.T) {
	d := New(nil, nil)
	last := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "process_order", Trigger: "api:/v1/orders", Script: "old script"},
		},
	}
	next := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "process_order", Trigger: "api:/v1/orders", Script: "new script"},
		},
	}

	plan, err := d.Diff(context.Background(), last, next)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceSaga)
	require.Len(t, updates, 1)
	assert.Contains(t, updates[0].Description, "script changed")
}

// ─── diffPartyTypes ──────────────────────────────────────────────────────────

func TestDiffPartyTypes_Create(t *testing.T) {
	d := New(nil, nil)
	manifest := &controlplanev1.Manifest{
		PartyTypes: []*partyv1.PartyTypeDefinition{
			{TenantId: "t1", PartyType: "CUSTOMER"},
		},
	}

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourcePartyType)
	require.Len(t, creates, 1)
	assert.Equal(t, "t1:CUSTOMER", creates[0].ResourceCode)
}

// ─── diffOrganizations ───────────────────────────────────────────────────────

func TestDiffOrganizations_Create(t *testing.T) {
	d := New(nil, nil)
	manifest := &controlplanev1.Manifest{
		Organizations: []*controlplanev1.OrganizationDefinition{
			{Code: "PLATFORM", Name: "Platform", PartyType: "OPERATOR"},
		},
	}

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceOrganization)
	require.Len(t, creates, 1)
	assert.Equal(t, "PLATFORM", creates[0].ResourceCode)
}

func TestDiffOrganizations_Update_AttributeChange(t *testing.T) {
	d := New(nil, nil)
	last := &controlplanev1.Manifest{
		Organizations: []*controlplanev1.OrganizationDefinition{
			{Code: "PLATFORM", Name: "Platform", Attributes: map[string]string{"region": "EU"}},
		},
	}
	next := &controlplanev1.Manifest{
		Organizations: []*controlplanev1.OrganizationDefinition{
			{Code: "PLATFORM", Name: "Platform", Attributes: map[string]string{"region": "US"}},
		},
	}

	plan, err := d.Diff(context.Background(), last, next)
	require.NoError(t, err)

	updates := filterActionsByResource(plan.Actions, ActionUpdate, ResourceOrganization)
	require.Len(t, updates, 1)
}

// ─── diffMarketDataSources ───────────────────────────────────────────────────

func TestDiffMarketDataSources_Create(t *testing.T) {
	d := New(nil, nil)
	manifest := &controlplanev1.Manifest{
		MarketData: &controlplanev1.MarketDataConfig{
			Sources: []*controlplanev1.MarketDataSourceDefinition{
				{
					Code:       "nordpool_spot",
					Name:       "Nord Pool Spot",
					TrustLevel: 90,
				},
			},
		},
	}

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceMarketDataSource)
	require.Len(t, creates, 1)
	assert.Equal(t, "nordpool_spot", creates[0].ResourceCode)
}

// ─── diffInternalAccounts ────────────────────────────────────────────────────

func TestDiffInternalAccounts_Create(t *testing.T) {
	d := New(nil, nil)
	manifest := &controlplanev1.Manifest{
		InternalAccounts: []*controlplanev1.InternalAccountDefinition{
			{Code: "PLATFORM_REVENUE", AccountType: "REVENUE", Instrument: "GBP"},
		},
	}

	plan, err := d.Diff(context.Background(), nil, manifest)
	require.NoError(t, err)

	creates := filterActionsByResource(plan.Actions, ActionCreate, ResourceInternalAccount)
	require.Len(t, creates, 1)
	assert.Equal(t, "PLATFORM_REVENUE", creates[0].ResourceCode)
}

// ─── WithSkipSafetyChecks ────────────────────────────────────────────────────

func TestDiffWithSkipSafetyChecks_AllowsDeletion(t *testing.T) {
	d := New(nil, nil)
	last := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "OLD"},
		},
	}
	next := &controlplanev1.Manifest{}

	plan, err := d.Diff(context.Background(), last, next, WithSkipSafetyChecks())
	require.NoError(t, err)
	assert.Empty(t, plan.BlockedDeletions)
	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceInstrument)
	require.Len(t, deletes, 1)
	assert.Equal(t, "OLD", deletes[0].ResourceCode)
}

// ─── DiffPlan.HasBreakingChanges ─────────────────────────────────────────────

func TestDiffPlan_BreakingChangesFlaggedOnDelete(t *testing.T) {
	d := New(nil, nil)
	last := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
		},
	}
	next := &controlplanev1.Manifest{}

	plan, err := d.Diff(context.Background(), last, next)
	require.NoError(t, err)

	deletes := filterActionsByResource(plan.Actions, ActionDelete, ResourceInstrument)
	require.Len(t, deletes, 1)
	assert.True(t, deletes[0].Breaking)
	assert.True(t, plan.HasBreakingChanges)
}
