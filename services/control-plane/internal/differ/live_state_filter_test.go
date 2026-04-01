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

// TestFilterTenantOwned_SagaOverride_Retained verifies that a tenant override saga
// (is_system=false with platform_ref) is retained after filtering.
// Tenant overrides are not in SystemCodes (they are not platform defaults), so they
// pass through filterTenantOwned and must be included in diff planning.
func TestFilterTenantOwned_SagaOverride_Retained(t *testing.T) {
	live := &LiveState{
		Sagas: []*controlplanev1.SagaDefinition{
			// Platform default: is_system=true, filtered out.
			{Name: "platform_saga"},
			// Tenant override: is_system=false, has platform_ref (tracked in PlatformRefs).
			{Name: "override_saga"},
			// Regular tenant saga: no platform_ref.
			{Name: "custom_saga"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceSaga: {"platform_saga": true},
		},
		// PlatformRefs documents which sagas are tenant overrides of platform defaults.
		PlatformRefs: map[ResourceType]map[string]bool{
			ResourceSaga: {"override_saga": true},
		},
	}
	result := filterTenantOwned(live)
	require.NotNil(t, result)
	require.Len(t, result.Sagas, 2, "both override_saga and custom_saga should be retained")
	names := sagaNames(result.Sagas)
	assert.Contains(t, names, "override_saga")
	assert.Contains(t, names, "custom_saga")
	assert.NotContains(t, names, "platform_saga")
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

func TestFilterTenantOwned_NonFilteredResourcesPreserved(t *testing.T) {
	// MarketDataSources, MarketDataSets, Organizations, etc. are passed through unchanged.
	live := &LiveState{
		MarketDataSources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "ECB"},
		},
		MarketDataSets: []*controlplanev1.MarketDataSetDefinition{
			{Code: "FX_RATES"},
		},
		Organizations: []*controlplanev1.OrganizationDefinition{
			{Code: "ACME"},
		},
		InternalAccounts: []*controlplanev1.InternalAccountDefinition{
			{Code: "SUSPENSE"},
		},
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "stripe-1"},
		},
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{InstructionType: "payment"},
		},
		SystemCodes: map[ResourceType]map[string]bool{
			ResourceInstrument: {"SYS_GBP": true}, // only instruments filtered
		},
	}
	result := filterTenantOwned(live)
	require.NotNil(t, result)

	assert.Len(t, result.MarketDataSources, 1)
	assert.Len(t, result.MarketDataSets, 1)
	assert.Len(t, result.Organizations, 1)
	assert.Len(t, result.InternalAccounts, 1)
	assert.Len(t, result.ProviderConnections, 1)
	assert.Len(t, result.InstructionRoutes, 1)
}

func TestFilterTenantOwned_PreservesSystemAndPlatformRefMaps(t *testing.T) {
	systemCodes := map[ResourceType]map[string]bool{
		ResourceInstrument: {"SYS_GBP": true},
	}
	platformRefs := map[ResourceType]map[string]bool{
		ResourceSaga: {"override_saga": true},
	}
	live := &LiveState{
		SystemCodes:  systemCodes,
		PlatformRefs: platformRefs,
	}
	result := filterTenantOwned(live)
	assert.Equal(t, systemCodes, result.SystemCodes)
	assert.Equal(t, platformRefs, result.PlatformRefs)
}

// --- helpers ---

func instrumentCodes(instruments []*controlplanev1.InstrumentDefinition) []string {
	codes := make([]string, len(instruments))
	for i, inst := range instruments {
		codes[i] = inst.GetCode()
	}
	return codes
}

func sagaNames(sagas []*controlplanev1.SagaDefinition) []string {
	names := make([]string, len(sagas))
	for i, s := range sagas {
		names[i] = s.GetName()
	}
	return names
}
