package applier

import (
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildExecutorInputFromPlan_FiltersNoChangeActions(t *testing.T) {
	mf := &controlplanev1.Manifest{
		Version: "2.0",
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
			{
				Code: "USD",
				Name: "US Dollar",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "USD",
					Precision: 2,
				},
			},
			{
				Code: "KWH",
				Name: "Kilowatt Hour",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "kWh",
					Precision: 4,
				},
			},
		},
	}

	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "USD", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "KWH", Action: differ.ActionUpdate},
		},
	}

	input := buildExecutorInputFromPlan(mf, plan)

	require.NotNil(t, input)
	assert.Equal(t, "2.0", input.ManifestVersion)

	// GBP is NO_CHANGE and should be excluded
	assert.Len(t, input.Instruments, 2)
	assert.Equal(t, "USD", input.Instruments[0].Code)
	assert.Equal(t, "CREATE", input.Instruments[0].Action)
	assert.Equal(t, "KWH", input.Instruments[1].Code)
	assert.Equal(t, "UPDATE", input.Instruments[1].Action)
}

func TestBuildExecutorInputFromPlan_IncludesDeprecateActions(t *testing.T) {
	mf := &controlplanev1.Manifest{
		Version: "3.0",
		Instruments: []*controlplanev1.InstrumentDefinition{
			{
				Code: "EUR",
				Name: "Euro",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "EUR",
					Precision: 2,
				},
			},
		},
	}

	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "EUR", Action: differ.ActionDeprecate},
		},
	}

	input := buildExecutorInputFromPlan(mf, plan)

	require.Len(t, input.Instruments, 1)
	assert.Equal(t, "EUR", input.Instruments[0].Code)
	assert.Equal(t, "DEPRECATE", input.Instruments[0].Action)
}

func TestBuildExecutorInputFromPlan_AccountTypes(t *testing.T) {
	mf := &controlplanev1.Manifest{
		Version: "1.0",
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{
				Code:               "CURRENT",
				Name:               "Current Account",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
				AllowedInstruments: []string{"GBP"},
			},
			{
				Code:               "SAVINGS",
				Name:               "Savings Account",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT,
				AllowedInstruments: []string{"GBP"},
			},
		},
	}

	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "SAVINGS", Action: differ.ActionCreate},
		},
	}

	input := buildExecutorInputFromPlan(mf, plan)

	require.Len(t, input.AccountTypes, 1)
	assert.Equal(t, "SAVINGS", input.AccountTypes[0].Code)
	assert.Equal(t, "CREATE", input.AccountTypes[0].Action)
}

func TestBuildExecutorInputFromPlan_MultipleResourceTypes(t *testing.T) {
	mf := &controlplanev1.Manifest{
		Version: "4.0",
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
				AllowedInstruments: []string{"GBP"},
			},
		},
		ValuationRules: []*controlplanev1.ValuationRule{
			{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_FIXED,
			},
		},
		Organizations: []*controlplanev1.OrganizationDefinition{
			{
				Code: "ACME",
				Name: "Acme Corp",
			},
		},
		InternalAccounts: []*controlplanev1.InternalAccountDefinition{
			{
				Code:        "REVENUE_GBP",
				AccountType: "CURRENT",
				Instrument:  "GBP",
			},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:   "deposit",
				Script: "def execute(): pass",
			},
		},
	}

	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionUpdate},
			{ResourceType: differ.ResourceValuationRule, ResourceCode: "KWH->GBP", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceOrganization, ResourceCode: "ACME", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceInternalAccount, ResourceCode: "REVENUE_GBP", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceSaga, ResourceCode: "deposit", Action: differ.ActionNoChange},
		},
	}

	input := buildExecutorInputFromPlan(mf, plan)

	// GBP instrument: NO_CHANGE -> excluded
	assert.Empty(t, input.Instruments)

	// CURRENT account type: UPDATE -> included
	require.Len(t, input.AccountTypes, 1)
	assert.Equal(t, "CURRENT", input.AccountTypes[0].Code)
	assert.Equal(t, "UPDATE", input.AccountTypes[0].Action)

	// Valuation rule: CREATE -> included
	require.Len(t, input.ValuationRules, 1)
	assert.Equal(t, "KWH", input.ValuationRules[0].FromInstrument)
	assert.Equal(t, "CREATE", input.ValuationRules[0].Action)

	// ACME org: NO_CHANGE -> excluded
	assert.Empty(t, input.Organizations)

	// REVENUE_GBP internal account: CREATE -> included
	require.Len(t, input.InternalAccounts, 1)
	assert.Equal(t, "REVENUE_GBP", input.InternalAccounts[0].Code)
	assert.Equal(t, "CREATE", input.InternalAccounts[0].Action)

	// deposit saga: NO_CHANGE -> excluded
	assert.Empty(t, input.SagaDefinitions)
}

func TestBuildExecutorInputFromPlan_EmptyPlan(t *testing.T) {
	mf := &controlplanev1.Manifest{
		Version: "1.0",
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
	}

	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionNoChange},
		},
	}

	input := buildExecutorInputFromPlan(mf, plan)

	assert.Equal(t, "1.0", input.ManifestVersion)
	assert.Empty(t, input.Instruments)
	assert.Empty(t, input.AccountTypes)
}

func TestBuildExecutorInputFromPlan_MarketData(t *testing.T) {
	mf := &controlplanev1.Manifest{
		Version: "1.0",
		MarketData: &controlplanev1.MarketDataConfig{
			Sources: []*controlplanev1.MarketDataSourceDefinition{
				{Code: "BLOOMBERG", Name: "Bloomberg", TrustLevel: 90},
				{Code: "REUTERS", Name: "Reuters", TrustLevel: 85},
			},
			Datasets: []*controlplanev1.MarketDataSetDefinition{
				{
					Code:       "USD_EUR_FX",
					SourceCode: "BLOOMBERG",
				},
			},
		},
	}

	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceMarketDataSource, ResourceCode: "BLOOMBERG", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceMarketDataSource, ResourceCode: "REUTERS", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceMarketDataSet, ResourceCode: "USD_EUR_FX", Action: differ.ActionUpdate},
		},
	}

	input := buildExecutorInputFromPlan(mf, plan)

	require.Len(t, input.MarketDataSources, 1)
	assert.Equal(t, "REUTERS", input.MarketDataSources[0].Code)
	assert.Equal(t, "CREATE", input.MarketDataSources[0].Action)

	require.Len(t, input.MarketDataSets, 1)
	assert.Equal(t, "USD_EUR_FX", input.MarketDataSets[0].Code)
	assert.Equal(t, "UPDATE", input.MarketDataSets[0].Action)
}

func TestBuildExecutorInputFromPlan_OperationalGateway(t *testing.T) {
	mf := &controlplanev1.Manifest{
		Version: "1.0",
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
				{
					ConnectionId: "stripe-conn",
					ProviderName: "Stripe",
					Protocol:     controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_HTTPS,
					BaseUrl:      "https://api.stripe.com",
				},
				{
					ConnectionId: "backup-conn",
					ProviderName: "Backup",
					Protocol:     controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_HTTPS,
					BaseUrl:      "https://backup.example.com",
				},
			},
			InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
				{
					InstructionType: "payment.initiate",
					ConnectionId:    "stripe-conn",
				},
			},
		},
	}

	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceProviderConnection, ResourceCode: "stripe-conn", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceProviderConnection, ResourceCode: "backup-conn", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceInstructionRoute, ResourceCode: "payment.initiate", Action: differ.ActionUpdate},
		},
	}

	input := buildExecutorInputFromPlan(mf, plan)

	require.Len(t, input.ProviderConnections, 1)
	assert.Equal(t, "backup-conn", input.ProviderConnections[0].ConnectionID)
	assert.Equal(t, "CREATE", input.ProviderConnections[0].Action)

	require.Len(t, input.InstructionRoutes, 1)
	assert.Equal(t, "payment.initiate", input.InstructionRoutes[0].InstructionType)
	assert.Equal(t, "UPDATE", input.InstructionRoutes[0].Action)
}

func TestBuildExecutorInputFromPlan_ActionPassedToSagaInput(t *testing.T) {
	mf := &controlplanev1.Manifest{
		Version: "1.0",
		Instruments: []*controlplanev1.InstrumentDefinition{
			{
				Code: "USD",
				Name: "US Dollar",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "USD",
					Precision: 2,
				},
			},
		},
	}

	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "USD", Action: differ.ActionUpdate},
		},
	}

	input := buildExecutorInputFromPlan(mf, plan)
	require.Len(t, input.Instruments, 1)

	// Verify action propagates through to saga input
	executor := &ManifestExecutor{}
	sagaInput := executor.buildSagaInput(input)

	instruments, ok := sagaInput["instruments"].([]interface{})
	require.True(t, ok)
	require.Len(t, instruments, 1)

	inst, ok := instruments[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "UPDATE", inst["action"])
}

func TestBuildSagaInput_ActionFieldOmittedWhenEmpty(t *testing.T) {
	executor := &ManifestExecutor{}

	// Legacy path: no action set (from buildExecutorInput)
	input := &ApplyManifestInput{
		ManifestVersion: "1",
		Instruments: []InstrumentInput{
			{Code: "GBP", DisplayName: "Pound", Dimension: "CURRENCY", DecimalPlaces: 2},
		},
	}

	sagaInput := executor.buildSagaInput(input)
	instruments, ok := sagaInput["instruments"].([]interface{})
	require.True(t, ok)
	require.Len(t, instruments, 1)

	inst, ok := instruments[0].(map[string]interface{})
	require.True(t, ok)

	// When Action is empty, the "action" key should be empty string (legacy behavior)
	assert.Equal(t, "", inst["action"])
}

func TestPlannedResourceAction_Fields(t *testing.T) {
	pra := PlannedResourceAction{
		ResourceType: differ.ResourceInstrument,
		ResourceCode: "GBP",
		Action:       differ.ActionCreate,
	}

	assert.Equal(t, differ.ResourceInstrument, pra.ResourceType)
	assert.Equal(t, "GBP", pra.ResourceCode)
	assert.Equal(t, differ.ActionCreate, pra.Action)
}

func TestBuildExecutorInputFromPlan_DeleteActionsExcluded(t *testing.T) {
	// DELETE actions should NOT appear in the executor input.
	// Deletions are handled separately (e.g., blocked deletion safety checks).
	mf := &controlplanev1.Manifest{
		Version: "1.0",
		Instruments: []*controlplanev1.InstrumentDefinition{
			{
				Code: "OBSOLETE",
				Name: "Obsolete Currency",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "OBSOLETE",
					Precision: 2,
				},
			},
		},
	}

	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "OBSOLETE", Action: differ.ActionDelete},
		},
	}

	input := buildExecutorInputFromPlan(mf, plan)

	// DELETE actions are not included - the existing manifest won't contain
	// deleted resources (they were removed from the new manifest).
	assert.Empty(t, input.Instruments)
}
