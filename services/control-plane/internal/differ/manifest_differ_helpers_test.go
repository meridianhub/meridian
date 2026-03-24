package differ

import (
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── nil manifest helpers ────────────────────────────────────────────────────

func TestGetInstruments_NilManifest(t *testing.T) {
	assert.Nil(t, getInstruments(nil))
}

func TestGetAccountTypes_NilManifest(t *testing.T) {
	assert.Nil(t, getAccountTypes(nil))
}

func TestGetValuationRules_NilManifest(t *testing.T) {
	assert.Nil(t, getValuationRules(nil))
}

func TestGetSagas_NilManifest(t *testing.T) {
	assert.Nil(t, getSagas(nil))
}

func TestGetPartyTypes_NilManifest(t *testing.T) {
	assert.Nil(t, getPartyTypes(nil))
}

func TestGetMappings_NilManifest(t *testing.T) {
	assert.Nil(t, getMappings(nil))
}

func TestGetProviderConnections_NilManifest(t *testing.T) {
	assert.Nil(t, getProviderConnections(nil))
}

func TestGetInstructionRoutes_NilManifest(t *testing.T) {
	assert.Nil(t, getInstructionRoutes(nil))
}

func TestGetMarketDataSources_NilManifest(t *testing.T) {
	assert.Nil(t, getMarketDataSources(nil))
}

func TestGetMarketDataSets_NilManifest(t *testing.T) {
	assert.Nil(t, getMarketDataSets(nil))
}

func TestGetOrganizations_NilManifest(t *testing.T) {
	assert.Nil(t, getOrganizations(nil))
}

func TestGetInternalAccounts_NilManifest(t *testing.T) {
	assert.Nil(t, getInternalAccounts(nil))
}

// ─── Map building helpers ────────────────────────────────────────────────────

func TestInstrumentMap(t *testing.T) {
	instruments := []*controlplanev1.InstrumentDefinition{
		{Code: "GBP", Name: "British Pound"},
		{Code: "USD", Name: "US Dollar"},
	}
	m := instrumentMap(instruments)
	require.Len(t, m, 2)
	assert.Equal(t, "British Pound", m["GBP"].GetName())
	assert.Equal(t, "US Dollar", m["USD"].GetName())
}

func TestInstrumentMap_Empty(t *testing.T) {
	assert.Empty(t, instrumentMap(nil))
}

func TestAccountTypeMap(t *testing.T) {
	types := []*controlplanev1.AccountTypeDefinition{
		{Code: "SETTLEMENT", Name: "Settlement"},
		{Code: "CLEARING", Name: "Clearing"},
	}
	m := accountTypeMap(types)
	require.Len(t, m, 2)
	assert.Equal(t, "Settlement", m["SETTLEMENT"].GetName())
}

func TestValuationRuleMap(t *testing.T) {
	rules := []*controlplanev1.ValuationRule{
		{FromInstrument: "KWH", ToInstrument: "GBP"},
	}
	m := valuationRuleMap(rules)
	require.Len(t, m, 1)
	_, ok := m["KWH->GBP"]
	assert.True(t, ok)
}

func TestSagaMap(t *testing.T) {
	sagas := []*controlplanev1.SagaDefinition{
		{Name: "process_order"},
		{Name: "cancel_order"},
	}
	m := sagaMap(sagas)
	require.Len(t, m, 2)
	assert.NotNil(t, m["process_order"])
	assert.NotNil(t, m["cancel_order"])
}

func TestPartyTypeKey(t *testing.T) {
	k := partyTypeKey("tenant-1", "CUSTOMER")
	assert.Equal(t, "tenant-1", k.TenantID)
	assert.Equal(t, "CUSTOMER", k.PartyType)
	assert.Equal(t, "tenant-1:CUSTOMER", k.String())
}

func TestPartyTypeMap(t *testing.T) {
	defs := []*partyv1.PartyTypeDefinition{
		{TenantId: "t1", PartyType: "CUSTOMER"},
		{TenantId: "t2", PartyType: "CUSTOMER"},
	}
	m := partyTypeMap(defs)
	require.Len(t, m, 2)
	assert.NotNil(t, m[partyTypeKey("t1", "CUSTOMER")])
	assert.NotNil(t, m[partyTypeKey("t2", "CUSTOMER")])
}

func TestMappingKey_Helper(t *testing.T) {
	assert.Equal(t, "order_mapping:1", mappingKey("order_mapping", 1))
	assert.Equal(t, "my_map:42", mappingKey("my_map", 42))
}

func TestMappingMap(t *testing.T) {
	mappings := []*mappingv1.MappingDefinition{
		{Name: "order_mapping", Version: 1},
		{Name: "order_mapping", Version: 2},
	}
	m := mappingMap(mappings)
	require.Len(t, m, 2)
	assert.NotNil(t, m["order_mapping:1"])
	assert.NotNil(t, m["order_mapping:2"])
}

func TestProviderConnectionMap(t *testing.T) {
	conns := []*controlplanev1.ProviderConnectionConfig{
		{ConnectionId: "stripe_connect"},
		{ConnectionId: "adyen"},
	}
	m := providerConnectionMap(conns)
	require.Len(t, m, 2)
	assert.NotNil(t, m["stripe_connect"])
}

func TestInstructionRouteMap(t *testing.T) {
	routes := []*controlplanev1.InstructionRouteConfig{
		{InstructionType: "CHARGE"},
		{InstructionType: "REFUND"},
	}
	m := instructionRouteMap(routes)
	require.Len(t, m, 2)
	assert.NotNil(t, m["CHARGE"])
}

func TestOrganizationMap(t *testing.T) {
	orgs := []*controlplanev1.OrganizationDefinition{
		{Code: "ORG_A", Name: "Organization A"},
	}
	m := organizationMap(orgs)
	assert.NotNil(t, m["ORG_A"])
}

func TestInternalAccountMap(t *testing.T) {
	accounts := []*controlplanev1.InternalAccountDefinition{
		{Code: "PLATFORM_REVENUE"},
	}
	m := internalAccountMap(accounts)
	assert.NotNil(t, m["PLATFORM_REVENUE"])
}

// ─── Change description helpers ──────────────────────────────────────────────

func TestDescribeInstrumentChanges_NameChange(t *testing.T) {
	prev := &controlplanev1.InstrumentDefinition{Code: "GBP", Name: "Pound"}
	updated := &controlplanev1.InstrumentDefinition{Code: "GBP", Name: "British Pound"}
	desc := describeInstrumentChanges("GBP", prev, updated)
	assert.Contains(t, desc, "GBP")
	assert.Contains(t, desc, "Pound")
	assert.Contains(t, desc, "British Pound")
}

func TestDescribeInstrumentChanges_NoChange(t *testing.T) {
	inst := &controlplanev1.InstrumentDefinition{Code: "GBP", Name: "British Pound"}
	desc := describeInstrumentChanges("GBP", inst, inst)
	assert.Equal(t, "Update instrument GBP", desc)
}

func TestDescribeAccountTypeChanges_NormalBalanceChange(t *testing.T) {
	prev := &controlplanev1.AccountTypeDefinition{
		Code:          "SETTLEMENT",
		Name:          "Settlement",
		NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
	}
	updated := &controlplanev1.AccountTypeDefinition{
		Code:          "SETTLEMENT",
		Name:          "Settlement",
		NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT,
	}
	desc := describeAccountTypeChanges("SETTLEMENT", prev, updated)
	assert.Contains(t, desc, "SETTLEMENT")
	assert.Contains(t, desc, "normal_balance")
}

func TestDescribeSagaChanges_ScriptChange(t *testing.T) {
	prev := &controlplanev1.SagaDefinition{Name: "s1", Script: "old script"}
	updated := &controlplanev1.SagaDefinition{Name: "s1", Script: "new script"}
	desc := describeSagaChanges("s1", prev, updated)
	assert.Contains(t, desc, "s1")
	assert.Contains(t, desc, "script changed")
}

func TestDescribeSagaChanges_NoChange(t *testing.T) {
	saga := &controlplanev1.SagaDefinition{Name: "s1", Script: "same", Trigger: "api:/v1"}
	desc := describeSagaChanges("s1", saga, saga)
	assert.Equal(t, "Update saga s1", desc)
}

func TestDescribePartyTypeChanges_AttributeSchemaChange(t *testing.T) {
	prev := &partyv1.PartyTypeDefinition{AttributeSchema: `{"old": "schema"}`}
	updated := &partyv1.PartyTypeDefinition{AttributeSchema: `{"new": "schema"}`}
	desc := describePartyTypeChanges("t1:CUSTOMER", prev, updated)
	assert.Contains(t, desc, "attribute_schema")
}

func TestDescribeMappingChanges_TargetServiceChange(t *testing.T) {
	prev := &mappingv1.MappingDefinition{TargetService: "order_service"}
	updated := &mappingv1.MappingDefinition{TargetService: "payment_service"}
	desc := describeMappingChanges("my_map:1", prev, updated)
	assert.Contains(t, desc, "target_service")
}

func TestAttributesEqual_Equal(t *testing.T) {
	a := map[string]string{"key": "value", "foo": "bar"}
	b := map[string]string{"foo": "bar", "key": "value"}
	assert.True(t, attributesEqual(a, b))
}

func TestAttributesEqual_DifferentValues(t *testing.T) {
	a := map[string]string{"key": "value1"}
	b := map[string]string{"key": "value2"}
	assert.False(t, attributesEqual(a, b))
}

func TestAttributesEqual_DifferentLengths(t *testing.T) {
	a := map[string]string{"key": "value", "extra": "field"}
	b := map[string]string{"key": "value"}
	assert.False(t, attributesEqual(a, b))
}

func TestAttributesEqual_NilMaps(t *testing.T) {
	assert.True(t, attributesEqual(nil, nil))
	assert.True(t, attributesEqual(nil, map[string]string{}))
	assert.True(t, attributesEqual(map[string]string{}, nil))
}
