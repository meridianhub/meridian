package applier

import (
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Helper key functions ---

func TestValRuleKey(t *testing.T) {
	assert.Equal(t, "GBP->KWH", valRuleKey("gbp", "kwh"))
	assert.Equal(t, "USD->EUR", valRuleKey("USD", "EUR"))
	assert.Equal(t, "->", valRuleKey("", ""))
}

func TestPartyTypeKey(t *testing.T) {
	assert.Equal(t, "tenant-1:INDIVIDUAL", partyTypeKey("tenant-1", "INDIVIDUAL"))
	assert.Equal(t, ":", partyTypeKey("", ""))
}

func TestMappingKey(t *testing.T) {
	assert.Equal(t, "my-mapping:1", mappingKey("my-mapping", 1))
	assert.Equal(t, "transform:5", mappingKey("transform", 5))
}

// --- patchResource error paths ---

func TestPatchResource_UnsupportedResourceType(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_UNSPECIFIED,
	}

	_, err := patchResource(base, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedResourceType)
}

func TestPatchResource_InstrumentTypeMismatch(t *testing.T) {
	base := newTestManifest()
	// Declare resource type as instrument but provide an account type payload
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_AccountType{
			AccountType: &controlplanev1.AccountTypeDefinition{Code: "CURRENT"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_AccountTypeTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_ACCOUNT_TYPE,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_ValuationRuleTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_VALUATION_RULE,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_SagaTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_SAGA,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_ProviderConnectionTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_PROVIDER_CONNECTION,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_InstructionRouteTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUCTION_ROUTE,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_MarketDataSourceTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MARKET_DATA_SOURCE,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_MarketDataSetTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MARKET_DATA_SET,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_OrganizationTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_ORGANIZATION,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_InternalAccountTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INTERNAL_ACCOUNT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

// --- Patch resource types not yet covered ---

func TestPatchResource_AddPartyType(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_PARTY_TYPE,
		Resource: &controlplanev1.ApplyResourceRequest_PartyType{
			PartyType: &partyv1.PartyTypeDefinition{
				TenantId:  "tenant-1",
				PartyType: "INDIVIDUAL",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.PartyTypes, 1)
	assert.Equal(t, "INDIVIDUAL", patched.PartyTypes[0].GetPartyType())
	assert.Equal(t, "tenant-1", patched.PartyTypes[0].GetTenantId())
}

func TestPatchResource_UpdatePartyType(t *testing.T) {
	base := newTestManifest()
	base.PartyTypes = []*partyv1.PartyTypeDefinition{
		{TenantId: "tenant-1", PartyType: "INDIVIDUAL"},
	}

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_PARTY_TYPE,
		Resource: &controlplanev1.ApplyResourceRequest_PartyType{
			PartyType: &partyv1.PartyTypeDefinition{
				TenantId:  "tenant-1",
				PartyType: "INDIVIDUAL",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.PartyTypes, 1)
}

func TestPatchResource_PartyTypeTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_PARTY_TYPE,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_AddMapping(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MAPPING,
		Resource: &controlplanev1.ApplyResourceRequest_Mapping{
			Mapping: &mappingv1.MappingDefinition{
				Name:    "transform-payments",
				Version: 1,
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.Mappings, 1)
	assert.Equal(t, "transform-payments", patched.Mappings[0].GetName())
	assert.Equal(t, int32(1), patched.Mappings[0].GetVersion())
}

func TestPatchResource_UpdateMapping(t *testing.T) {
	base := newTestManifest()
	base.Mappings = []*mappingv1.MappingDefinition{
		{Name: "transform-payments", Version: 1},
	}

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MAPPING,
		Resource: &controlplanev1.ApplyResourceRequest_Mapping{
			Mapping: &mappingv1.MappingDefinition{
				Name:    "transform-payments",
				Version: 1,
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.Mappings, 1)
}

func TestPatchResource_MappingTypeMismatch(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MAPPING,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
		},
	}

	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_AddInstructionRoute(t *testing.T) {
	base := newTestManifest()
	require.Nil(t, base.OperationalGateway)

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUCTION_ROUTE,
		Resource: &controlplanev1.ApplyResourceRequest_InstructionRoute{
			InstructionRoute: &controlplanev1.InstructionRouteConfig{
				InstructionType: "payment.initiate",
				ConnectionId:    "stripe-payments",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.NotNil(t, patched.OperationalGateway)
	require.Len(t, patched.OperationalGateway.InstructionRoutes, 1)
	assert.Equal(t, "payment.initiate", patched.OperationalGateway.InstructionRoutes[0].GetInstructionType())
}

func TestPatchResource_AddMarketDataSet(t *testing.T) {
	base := newTestManifest()
	require.Nil(t, base.MarketData)

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MARKET_DATA_SET,
		Resource: &controlplanev1.ApplyResourceRequest_MarketDataSet{
			MarketDataSet: &controlplanev1.MarketDataSetDefinition{
				Code:       "USD_EUR_FX",
				Unit:       "USD/EUR",
				SourceCode: "BLOOMBERG",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.NotNil(t, patched.MarketData)
	require.Len(t, patched.MarketData.Datasets, 1)
	assert.Equal(t, "USD_EUR_FX", patched.MarketData.Datasets[0].GetCode())
}

func TestPatchResource_AddValuationRule(t *testing.T) {
	base := newTestManifest()

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_VALUATION_RULE,
		Resource: &controlplanev1.ApplyResourceRequest_ValuationRule{
			ValuationRule: &controlplanev1.ValuationRule{
				FromInstrument: "GBP",
				ToInstrument:   "KWH",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_FIXED,
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.ValuationRules, 1)
	assert.Equal(t, "GBP", patched.ValuationRules[0].GetFromInstrument())
	assert.Equal(t, "KWH", patched.ValuationRules[0].GetToInstrument())
}

func TestPatchResource_UpdateValuationRule(t *testing.T) {
	base := newTestManifest()
	base.ValuationRules = []*controlplanev1.ValuationRule{
		{FromInstrument: "GBP", ToInstrument: "KWH", Method: controlplanev1.ValuationMethod_VALUATION_METHOD_FIXED},
	}

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_VALUATION_RULE,
		Resource: &controlplanev1.ApplyResourceRequest_ValuationRule{
			ValuationRule: &controlplanev1.ValuationRule{
				FromInstrument: "GBP",
				ToInstrument:   "KWH",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.ValuationRules, 1)
	assert.Equal(t, controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE, patched.ValuationRules[0].GetMethod())
}

// --- resourceID additional coverage ---

func TestResourceID_PartyType(t *testing.T) {
	req := &controlplanev1.ApplyResourceRequest{
		Resource: &controlplanev1.ApplyResourceRequest_PartyType{
			PartyType: &partyv1.PartyTypeDefinition{
				TenantId:  "tenant-1",
				PartyType: "INDIVIDUAL",
			},
		},
	}
	assert.Equal(t, "tenant-1:INDIVIDUAL", resourceID(req))
}

func TestResourceID_Mapping(t *testing.T) {
	req := &controlplanev1.ApplyResourceRequest{
		Resource: &controlplanev1.ApplyResourceRequest_Mapping{
			Mapping: &mappingv1.MappingDefinition{
				Name:    "transform",
				Version: 3,
			},
		},
	}
	assert.Equal(t, "transform:3", resourceID(req))
}

func TestResourceID_ProviderConnection(t *testing.T) {
	req := &controlplanev1.ApplyResourceRequest{
		Resource: &controlplanev1.ApplyResourceRequest_ProviderConnection{
			ProviderConnection: &controlplanev1.ProviderConnectionConfig{
				ConnectionId: "stripe-payments",
			},
		},
	}
	assert.Equal(t, "stripe-payments", resourceID(req))
}

func TestResourceID_InstructionRoute(t *testing.T) {
	req := &controlplanev1.ApplyResourceRequest{
		Resource: &controlplanev1.ApplyResourceRequest_InstructionRoute{
			InstructionRoute: &controlplanev1.InstructionRouteConfig{
				InstructionType: "payment.initiate",
			},
		},
	}
	assert.Equal(t, "payment.initiate", resourceID(req))
}

func TestResourceID_MarketDataSource(t *testing.T) {
	req := &controlplanev1.ApplyResourceRequest{
		Resource: &controlplanev1.ApplyResourceRequest_MarketDataSource{
			MarketDataSource: &controlplanev1.MarketDataSourceDefinition{
				Code: "BLOOMBERG",
			},
		},
	}
	assert.Equal(t, "BLOOMBERG", resourceID(req))
}

func TestResourceID_MarketDataSet(t *testing.T) {
	req := &controlplanev1.ApplyResourceRequest{
		Resource: &controlplanev1.ApplyResourceRequest_MarketDataSet{
			MarketDataSet: &controlplanev1.MarketDataSetDefinition{
				Code: "USD_EUR_FX",
			},
		},
	}
	assert.Equal(t, "USD_EUR_FX", resourceID(req))
}

func TestResourceID_InternalAccount(t *testing.T) {
	req := &controlplanev1.ApplyResourceRequest{
		Resource: &controlplanev1.ApplyResourceRequest_InternalAccount{
			InternalAccount: &controlplanev1.InternalAccountDefinition{
				Code: "REVENUE_GBP",
			},
		},
	}
	assert.Equal(t, "REVENUE_GBP", resourceID(req))
}

func TestResourceID_NilResource(t *testing.T) {
	req := &controlplanev1.ApplyResourceRequest{}
	assert.Equal(t, "", resourceID(req))
}

// --- upsertInSlice ---

func TestUpsertInSlice_AppendNew(t *testing.T) {
	type item struct{ Code string }
	slice := []item{{Code: "A"}, {Code: "B"}}
	result := upsertInSlice(slice, item{Code: "C"}, func(i item) string { return i.Code }, "C")
	require.Len(t, result, 3)
	assert.Equal(t, "C", result[2].Code)
}

func TestUpsertInSlice_ReplaceExisting(t *testing.T) {
	type item struct {
		Code string
		Name string
	}
	slice := []item{{Code: "A", Name: "Alpha"}, {Code: "B", Name: "Beta"}}
	result := upsertInSlice(slice, item{Code: "A", Name: "Updated"}, func(i item) string { return i.Code }, "A")
	require.Len(t, result, 2)
	assert.Equal(t, "Updated", result[0].Name)
}

func TestUpsertInSlice_EmptySlice(t *testing.T) {
	type item struct{ Code string }
	var slice []item
	result := upsertInSlice(slice, item{Code: "A"}, func(i item) string { return i.Code }, "A")
	require.Len(t, result, 1)
}

// --- Nil payload variants for patcher functions ---

func TestPatchResource_InstrumentNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_AccountTypeNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_ACCOUNT_TYPE,
		Resource: &controlplanev1.ApplyResourceRequest_AccountType{
			AccountType: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_SagaNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_SAGA,
		Resource: &controlplanev1.ApplyResourceRequest_Saga{
			Saga: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_ValuationRuleNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_VALUATION_RULE,
		Resource: &controlplanev1.ApplyResourceRequest_ValuationRule{
			ValuationRule: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_PartyTypeNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_PARTY_TYPE,
		Resource: &controlplanev1.ApplyResourceRequest_PartyType{
			PartyType: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_MappingNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MAPPING,
		Resource: &controlplanev1.ApplyResourceRequest_Mapping{
			Mapping: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_ProviderConnectionNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_PROVIDER_CONNECTION,
		Resource: &controlplanev1.ApplyResourceRequest_ProviderConnection{
			ProviderConnection: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_InstructionRouteNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUCTION_ROUTE,
		Resource: &controlplanev1.ApplyResourceRequest_InstructionRoute{
			InstructionRoute: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_MarketDataSourceNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MARKET_DATA_SOURCE,
		Resource: &controlplanev1.ApplyResourceRequest_MarketDataSource{
			MarketDataSource: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_MarketDataSetNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MARKET_DATA_SET,
		Resource: &controlplanev1.ApplyResourceRequest_MarketDataSet{
			MarketDataSet: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_OrganizationNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_ORGANIZATION,
		Resource: &controlplanev1.ApplyResourceRequest_Organization{
			Organization: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

func TestPatchResource_InternalAccountNilPayload(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INTERNAL_ACCOUNT,
		Resource: &controlplanev1.ApplyResourceRequest_InternalAccount{
			InternalAccount: nil,
		},
	}
	_, err := patchResource(base, req)
	require.ErrorIs(t, err, ErrResourceTypeMismatch)
}

// --- Existing OperationalGateway / MarketData upsert (not nil init) ---

func TestPatchResource_ProviderConnection_ExistingGateway(t *testing.T) {
	base := newTestManifest()
	base.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "existing-conn", ProviderName: "OldProvider"},
		},
	}

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_PROVIDER_CONNECTION,
		Resource: &controlplanev1.ApplyResourceRequest_ProviderConnection{
			ProviderConnection: &controlplanev1.ProviderConnectionConfig{
				ConnectionId: "new-conn",
				ProviderName: "NewProvider",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.OperationalGateway.ProviderConnections, 2)
}

func TestPatchResource_InstructionRoute_ExistingGateway(t *testing.T) {
	base := newTestManifest()
	base.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{InstructionType: "existing.route"},
		},
	}

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUCTION_ROUTE,
		Resource: &controlplanev1.ApplyResourceRequest_InstructionRoute{
			InstructionRoute: &controlplanev1.InstructionRouteConfig{
				InstructionType: "new.route",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.OperationalGateway.InstructionRoutes, 2)
}

func TestPatchResource_MarketDataSource_ExistingMarketData(t *testing.T) {
	base := newTestManifest()
	base.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "EXISTING_SOURCE"},
		},
	}

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MARKET_DATA_SOURCE,
		Resource: &controlplanev1.ApplyResourceRequest_MarketDataSource{
			MarketDataSource: &controlplanev1.MarketDataSourceDefinition{
				Code: "NEW_SOURCE",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.MarketData.Sources, 2)
}

func TestPatchResource_MarketDataSet_ExistingMarketData(t *testing.T) {
	base := newTestManifest()
	base.MarketData = &controlplanev1.MarketDataConfig{
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{Code: "EXISTING_DATASET"},
		},
	}

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MARKET_DATA_SET,
		Resource: &controlplanev1.ApplyResourceRequest_MarketDataSet{
			MarketDataSet: &controlplanev1.MarketDataSetDefinition{
				Code: "NEW_DATASET",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.MarketData.Datasets, 2)
}

// --- patchResource does not mutate base ---

func TestPatchResource_DoesNotMutateBase(t *testing.T) {
	base := newTestManifest()
	originalLen := len(base.Instruments)

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{
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

	patched, err := patchResource(base, req)
	require.NoError(t, err)

	assert.Len(t, base.Instruments, originalLen, "base should not be mutated")
	assert.Len(t, patched.Instruments, originalLen+1, "patched should have one more instrument")
}
