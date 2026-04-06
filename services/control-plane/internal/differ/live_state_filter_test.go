package differ

import (
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterTenantOwned_NilLiveState(t *testing.T) {
	result := filterTenantOwned(nil)
	assert.Nil(t, result)
}

func TestFilterTenantOwned_NilSystemCodes_NoFiltering(t *testing.T) {
	live := &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP"},
		},
	}
	result := filterTenantOwned(live)
	require.NotNil(t, result)
	assert.Len(t, result.Instruments, 1)
}

func TestFilterTenantOwned_EmptySystemCodes_NoFiltering(t *testing.T) {
	live := &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP"},
		},
		SystemCodes: map[ResourceType]map[string]bool{},
	}
	result := filterTenantOwned(live)
	require.NotNil(t, result)
	assert.Len(t, result.Instruments, 1)
}

func TestFilterTenantOwned_Instruments_SystemFiltered(t *testing.T) {
	live := &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
			{Code: "SYS_USD", Name: "System USD"},
			{Code: "KWH", Name: "Kilowatt Hour"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceInstrument: {"SYS_USD": true},
		},
	}
	result := filterTenantOwned(live)
	require.NotNil(t, result)
	require.Len(t, result.Instruments, 2)
	codes := instrumentCodes(result.Instruments)
	assert.Contains(t, codes, "GBP")
	assert.Contains(t, codes, "KWH")
	assert.NotContains(t, codes, "SYS_USD")
}

func TestFilterTenantOwned_AccountTypes_SystemFiltered(t *testing.T) {
	live := &LiveState{
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "CURRENT"},
			{Code: "SYS_SUSPENSE"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceAccountType: {"SYS_SUSPENSE": true},
		},
	}
	result := filterTenantOwned(live)
	require.NotNil(t, result)
	require.Len(t, result.AccountTypes, 1)
	assert.Equal(t, "CURRENT", result.AccountTypes[0].GetCode())
}

func TestFilterTenantOwned_Sagas_SystemFiltered(t *testing.T) {
	live := &LiveState{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "platform_payment_saga"},
			{Name: "tenant_custom_saga"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceSaga: {"platform_payment_saga": true},
		},
	}
	result := filterTenantOwned(live)
	require.NotNil(t, result)
	require.Len(t, result.Sagas, 1)
	assert.Equal(t, "tenant_custom_saga", result.Sagas[0].GetName())
}

func TestFilterTenantOwned_MultipleTypes(t *testing.T) {
	live := &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP"},
			{Code: "SYS_EUR"},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "CURRENT"},
			{Code: "SYS_FEE"},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "tenant_saga"},
			{Name: "sys_saga"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceInstrument:  {"SYS_EUR": true},
			ResourceAccountType: {"SYS_FEE": true},
			ResourceSaga:        {"sys_saga": true},
		},
	}
	result := filterTenantOwned(live)
	require.NotNil(t, result)

	assert.Len(t, result.Instruments, 1)
	assert.Equal(t, "GBP", result.Instruments[0].GetCode())

	assert.Len(t, result.AccountTypes, 1)
	assert.Equal(t, "CURRENT", result.AccountTypes[0].GetCode())

	assert.Len(t, result.Sagas, 1)
	assert.Equal(t, "tenant_saga", result.Sagas[0].GetName())
}

func TestFilterTenantOwned_AllResourceTypesFiltered(t *testing.T) {
	// SystemCodes can filter any resource type. Verify all 9 types are filtered.
	live := &LiveState{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "TENANT_GBP"},
			{Code: "SYS_GBP"},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "TENANT_AT"},
			{Code: "SYS_AT"},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "tenant_saga"},
			{Name: "sys_saga"},
		},
		MarketDataSources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "TENANT_SRC"},
			{Code: "SYS_SRC"},
		},
		MarketDataSets: []*controlplanev1.MarketDataSetDefinition{
			{Code: "TENANT_SET"},
			{Code: "SYS_SET"},
		},
		Organizations: []*controlplanev1.OrganizationDefinition{
			{Code: "TENANT_ORG"},
			{Code: "SYS_ORG"},
		},
		InternalAccounts: []*controlplanev1.InternalAccountDefinition{
			{Code: "TENANT_ACCT"},
			{Code: "SYS_ACCT"},
		},
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "tenant-conn"},
			{ConnectionId: "sys-conn"},
		},
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{InstructionType: "tenant-route"},
			{InstructionType: "sys-route"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceInstrument:         {"SYS_GBP": true},
			ResourceAccountType:        {"SYS_AT": true},
			ResourceSaga:               {"sys_saga": true},
			ResourceMarketDataSource:   {"SYS_SRC": true},
			ResourceMarketDataSet:      {"SYS_SET": true},
			ResourceOrganization:       {"SYS_ORG": true},
			ResourceInternalAccount:    {"SYS_ACCT": true},
			ResourceProviderConnection: {"sys-conn": true},
			ResourceInstructionRoute:   {"sys-route": true},
		},
	}
	result := filterTenantOwned(live)
	require.NotNil(t, result)

	assert.Len(t, result.Instruments, 1)
	assert.Equal(t, "TENANT_GBP", result.Instruments[0].GetCode())
	assert.Len(t, result.AccountTypes, 1)
	assert.Equal(t, "TENANT_AT", result.AccountTypes[0].GetCode())
	assert.Len(t, result.Sagas, 1)
	assert.Equal(t, "tenant_saga", result.Sagas[0].GetName())
	assert.Len(t, result.MarketDataSources, 1)
	assert.Equal(t, "TENANT_SRC", result.MarketDataSources[0].GetCode())
	assert.Len(t, result.MarketDataSets, 1)
	assert.Equal(t, "TENANT_SET", result.MarketDataSets[0].GetCode())
	assert.Len(t, result.Organizations, 1)
	assert.Equal(t, "TENANT_ORG", result.Organizations[0].GetCode())
	assert.Len(t, result.InternalAccounts, 1)
	assert.Equal(t, "TENANT_ACCT", result.InternalAccounts[0].GetCode())
	assert.Len(t, result.ProviderConnections, 1)
	assert.Equal(t, "tenant-conn", result.ProviderConnections[0].GetConnectionId())
	assert.Len(t, result.InstructionRoutes, 1)
	assert.Equal(t, "tenant-route", result.InstructionRoutes[0].GetInstructionType())
}

func TestFilterTenantOwned_PreservesSystemCodesMap(t *testing.T) {
	systemCodes := map[ResourceType]map[string]bool{
		ResourceInstrument: {"SYS_GBP": true},
	}
	live := &LiveState{
		SystemCodes: systemCodes,
	}
	result := filterTenantOwned(live)
	assert.Equal(t, systemCodes, result.SystemCodes)
}

// --- helpers ---

func instrumentCodes(instruments []*controlplanev1.InstrumentDefinition) []string {
	codes := make([]string, len(instruments))
	for i, inst := range instruments {
		codes[i] = inst.GetCode()
	}
	return codes
}
