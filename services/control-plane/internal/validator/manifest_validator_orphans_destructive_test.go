package validator

import (
	"strings"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Task 11: Orphan Detection Tests ────────────────────────────────────────

func TestValidateOrphans_UnusedProviderConnection(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "stripe"},
			{ConnectionId: "unused_provider"},
		},
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{InstructionType: "payment.initiate", ConnectionId: "stripe"},
		},
	}
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "payment_saga",
			Trigger: "api:/v1/payments",
			Script:  "def execute(ctx):\n    operational_gateway.dispatch_instruction(instruction_type=\"payment.initiate\", payload={})\n",
		},
	}

	result := v.Validate(m, nil)

	found := false
	for _, w := range result.Warnings {
		if w.Code == "ORPHAN_PROVIDER_CONNECTION" {
			found = true
			assert.Contains(t, w.Message, "unused_provider")
			break
		}
	}
	assert.True(t, found, "expected ORPHAN_PROVIDER_CONNECTION warning, got warnings: %v", result.Warnings)
}

func TestValidateOrphans_FallbackConnectionNotOrphan(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "primary"},
			{ConnectionId: "fallback"},
		},
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{InstructionType: "payment.initiate", ConnectionId: "primary", FallbackConnectionId: "fallback"},
		},
	}
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "payment_saga",
			Trigger: "api:/v1/payments",
			Script:  "def execute(ctx):\n    operational_gateway.dispatch_instruction(\"payment.initiate\", payload={})\n",
		},
	}

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "ORPHAN_PROVIDER_CONNECTION", w.Code,
			"fallback connection should not be flagged as orphan: %v", w)
	}
}

func TestValidateOrphans_WebhookSourceNotOrphan(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "stripe"},
		},
	}
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "webhook_handler",
			Trigger: "webhook:stripe.payment_intent.succeeded",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "ORPHAN_PROVIDER_CONNECTION", w.Code,
			"webhook source should count as usage: %v", w)
	}
}

func TestValidateOrphans_UnusedInstructionRoute(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "stripe"},
		},
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{InstructionType: "payment.initiate", ConnectionId: "stripe"},
			{InstructionType: "payment.refund", ConnectionId: "stripe"},
		},
	}
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "payment_saga",
			Trigger: "api:/v1/payments",
			Script:  "def execute(ctx):\n    operational_gateway.dispatch_instruction(instruction_type=\"payment.initiate\", payload={})\n",
		},
	}

	result := v.Validate(m, nil)

	found := false
	for _, w := range result.Warnings {
		if w.Code == "ORPHAN_INSTRUCTION_ROUTE" {
			found = true
			assert.Contains(t, w.Message, "payment.refund")
			break
		}
	}
	assert.True(t, found, "expected ORPHAN_INSTRUCTION_ROUTE warning, got warnings: %v", result.Warnings)
}

func TestValidateOrphans_AllConnected_NoWarnings(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "stripe"},
		},
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{InstructionType: "payment.initiate", ConnectionId: "stripe"},
		},
	}
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "payment_saga",
			Trigger: "api:/v1/payments",
			Script:  "def execute(ctx):\n    operational_gateway.dispatch_instruction(\"payment.initiate\", payload={})\n",
		},
	}

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "ORPHAN_PROVIDER_CONNECTION", w.Code, "unexpected orphan warning: %v", w)
		assert.NotEqual(t, "ORPHAN_INSTRUCTION_ROUTE", w.Code, "unexpected orphan warning: %v", w)
	}
}

func TestValidateOrphans_DispatchRegex_KeywordArg(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "bank"},
		},
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{InstructionType: "bank.transfer", ConnectionId: "bank"},
		},
	}
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "transfer_saga",
			Trigger: "api:/v1/payments",
			Script:  "def execute(ctx):\n    operational_gateway.dispatch_instruction(instruction_type='bank.transfer', payload={})\n",
		},
	}

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "ORPHAN_INSTRUCTION_ROUTE", w.Code,
			"keyword arg dispatch should be detected: %v", w)
	}
}

func TestValidateOrphans_DispatchRegex_PositionalArg(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "bank"},
		},
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
			{InstructionType: "bank.transfer", ConnectionId: "bank"},
		},
	}
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "transfer_saga",
			Trigger: "api:/v1/payments",
			Script:  "def execute(ctx):\n    operational_gateway.dispatch_instruction(\"bank.transfer\", payload={})\n",
		},
	}

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "ORPHAN_INSTRUCTION_ROUTE", w.Code,
			"positional arg dispatch should be detected: %v", w)
	}
}

func TestValidateOrphans_NoGateway_NoWarnings(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = nil

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "ORPHAN_PROVIDER_CONNECTION", w.Code)
		assert.NotEqual(t, "ORPHAN_INSTRUCTION_ROUTE", w.Code)
	}
}

// --- Destructive change detection tests ---

func TestValidateDestructiveChanges_RemoveInstrumentUsedByAccountType(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	prev := validManifest() // Has GBP instrument used by SETTLEMENT account type
	curr := validManifest()
	// Remove GBP instrument (index 0), keep KWH
	curr.Instruments = curr.Instruments[1:] // Only KWH remains
	// Remove the account type that references GBP so cross-ref passes
	// but the destructive check should still fire based on previous manifest
	curr.AccountTypes = nil

	result := v.Validate(curr, prev)
	assert.False(t, result.Valid, "should be invalid when removing instrument used by account_type")

	found := false
	for _, e := range result.Errors {
		if e.Code == "DESTRUCTIVE_INSTRUMENT_REMOVAL" {
			found = true
			assert.Contains(t, e.Message, "GBP")
			assert.Contains(t, e.Message, "account_type:SETTLEMENT")
			break
		}
	}
	assert.True(t, found, "expected DESTRUCTIVE_INSTRUMENT_REMOVAL error, got: %v", result.Errors)
}

func TestValidateDestructiveChanges_RemoveAccountTypeWithNoDependents(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// The default saga has a simple script with no handler calls referencing accounts.
	// Removing the account type should not produce a destructive error.
	prev := validManifest()
	curr := validManifest()
	curr.AccountTypes = nil

	result := v.Validate(curr, prev)
	for _, e := range result.Errors {
		assert.NotEqual(t, "DESTRUCTIVE_ACCOUNT_TYPE_REMOVAL", e.Code,
			"account type with no dependents should not produce destructive error")
	}
}

func TestValidateDestructiveChanges_RemoveAccountTypeWithDependentsViaGraph(t *testing.T) {
	// Test via the relationship graph Dependents method directly.
	g := &RelationshipGraph{
		Nodes: []GraphNode{
			{ID: "account_type:SETTLEMENT", Type: NodeTypeAccountType, Name: "Settlement"},
			{ID: "saga:process_payment", Type: NodeTypeSaga, Name: "process_payment"},
		},
		Edges: []GraphEdge{
			{
				Source:       "saga:process_payment",
				Target:       "account_type:SETTLEMENT",
				Relationship: RelWritesTo,
				IsDynamic:    true,
			},
		},
	}

	dependents := g.Dependents("account_type:SETTLEMENT")
	assert.Equal(t, []string{"saga:process_payment"}, dependents,
		"SETTLEMENT should have saga:process_payment as dependent")
}

func TestValidateDestructiveChanges_RemoveSagaWithNoSubscriptions(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	prev := validManifest()
	curr := validManifest()
	curr.Sagas = nil // Remove the saga

	result := v.Validate(curr, prev)
	// Removing a saga with no active subscriptions should succeed
	// (other validation errors may occur, but no DESTRUCTIVE_SAGA_REMOVAL)
	for _, e := range result.Errors {
		assert.NotEqual(t, "DESTRUCTIVE_SAGA_REMOVAL", e.Code,
			"should not produce DESTRUCTIVE_SAGA_REMOVAL for saga with no dependents")
	}
}

func TestValidateDestructiveChanges_RemoveUnusedInstrument(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	prev := validManifest()
	// Add an unused instrument to the previous manifest
	prev.Instruments = append(prev.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "EUR",
			Precision: 2,
		},
	})

	curr := validManifest() // Does not have EUR

	result := v.Validate(curr, prev)
	// Should not produce destructive error for unused instrument
	for _, e := range result.Errors {
		if e.Code == "DESTRUCTIVE_INSTRUMENT_REMOVAL" {
			assert.NotContains(t, e.Message, "EUR",
				"should not flag removal of unused instrument EUR")
		}
	}
}

func TestValidateDestructiveChanges_AddNewResourcesNeverTriggersWarning(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	prev := validManifest()
	curr := validManifest()
	// Add new resources
	curr.Instruments = append(curr.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "EUR",
			Precision: 2,
		},
	})
	curr.AccountTypes = append(curr.AccountTypes, &controlplanev1.AccountTypeDefinition{
		Code:               "REVENUE",
		Name:               "Revenue Account",
		NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT,
		AllowedInstruments: []string{"GBP"},
		Policies: &controlplanev1.AccountTypePolicies{
			Validation: "amount > 0",
		},
	})

	result := v.Validate(curr, prev)
	for _, e := range result.Errors {
		assert.NotContains(t, e.Code, "DESTRUCTIVE_",
			"adding new resources should never trigger destructive warnings")
	}
}

func TestValidateDestructiveChanges_ForceOverrideBypassesChecks(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	prev := validManifest()
	curr := validManifest()
	curr.Instruments = curr.Instruments[1:] // Remove GBP
	curr.AccountTypes = nil                 // Remove account types

	result := v.Validate(curr, prev, WithForceDestructiveChanges())
	// With force, destructive errors become warnings
	for _, e := range result.Errors {
		assert.NotContains(t, e.Code, "DESTRUCTIVE_",
			"force override should convert destructive errors to warnings")
	}

	// Verify they appear as warnings instead
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w.Code, "DESTRUCTIVE_") {
			found = true
			break
		}
	}
	assert.True(t, found, "force override should produce destructive warnings, not errors")
}

func TestValidateDestructiveChanges_NilPreviousNoCheck(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	result := v.Validate(validManifest(), nil)
	for _, e := range result.Errors {
		assert.NotContains(t, e.Code, "DESTRUCTIVE_",
			"nil previous manifest should never trigger destructive checks")
	}
}

func TestValidateDestructiveChanges_RemoveInstrumentUsedByValuationRule(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	prev := validManifest() // Has KWH->GBP valuation rule
	curr := validManifest()
	// Remove KWH instrument (index 1)
	curr.Instruments = curr.Instruments[:1] // Only GBP remains
	// Remove valuation rules that reference KWH
	curr.ValuationRules = nil

	result := v.Validate(curr, prev)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "DESTRUCTIVE_INSTRUMENT_REMOVAL" && strings.Contains(e.Message, "KWH") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected DESTRUCTIVE_INSTRUMENT_REMOVAL for KWH used in valuation rule, got: %v", result.Errors)
}

func TestWithSkipImmutabilityChecks_SkipsImmutableFieldChanged(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	prev := validManifest()
	curr := validManifest()
	curr.Instruments[0].Code = "USD" // Changed from GBP - normally triggers IMMUTABLE_FIELD_CHANGED

	result := v.Validate(curr, prev, WithSkipImmutabilityChecks())

	// Should NOT contain IMMUTABLE_FIELD_CHANGED errors
	for _, e := range result.Errors {
		assert.NotEqual(t, "IMMUTABLE_FIELD_CHANGED", e.Code,
			"expected no IMMUTABLE_FIELD_CHANGED errors when skip_immutability_checks is set")
	}
}

func TestWithSkipImmutabilityChecks_SkipsDestructiveRemoval(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	prev := validManifest()
	curr := validManifest()
	curr.Instruments = curr.Instruments[:1] // Remove KWH - normally triggers DESTRUCTIVE_INSTRUMENT_REMOVAL

	result := v.Validate(curr, prev, WithSkipImmutabilityChecks())

	for _, e := range result.Errors {
		assert.NotEqual(t, "DESTRUCTIVE_INSTRUMENT_REMOVAL", e.Code,
			"expected no DESTRUCTIVE_INSTRUMENT_REMOVAL errors when skip_immutability_checks is set")
	}
}

func TestWithoutSkipImmutabilityChecks_NoImmutabilityErrors(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	prev := validManifest()
	curr := validManifest()
	curr.Instruments[0].Code = "USD" // Changed from GBP

	result := v.Validate(curr, prev)
	// validateImmutability is now a no-op - no IMMUTABLE_FIELD_CHANGED errors expected.
	for _, e := range result.Errors {
		assert.NotEqual(t, "IMMUTABLE_FIELD_CHANGED", e.Code, "unexpected IMMUTABLE_FIELD_CHANGED error: %s", e.Message)
	}
}

// --- Market Data validation tests ---

func TestValidate_DuplicateMarketDataSourceCodes(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "BLOOMBERG", Name: "Bloomberg 1", TrustLevel: 90},
			{Code: "BLOOMBERG", Name: "Bloomberg 2", TrustLevel: 80},
		},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_CODE" && strings.Contains(e.Path, "market_data.sources") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected DUPLICATE_CODE error for market data sources")
}

func TestValidate_DuplicateMarketDataSetCodes(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "ECB", Name: "ECB", TrustLevel: 95},
		},
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{Code: "USD_EUR_FX", Category: marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE, Unit: "USD/EUR", SourceCode: "ECB"},
			{Code: "USD_EUR_FX", Category: marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE, Unit: "EUR/USD", SourceCode: "ECB"},
		},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_CODE" && strings.Contains(e.Path, "market_data.datasets") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected DUPLICATE_CODE error for market data sets")
}

func TestValidate_MarketDataSetReferencesInvalidSource(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "ECB", Name: "ECB", TrustLevel: 95},
		},
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{Code: "USD_EUR_FX", Category: marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE, Unit: "USD/EUR", SourceCode: "BLOOMBERG"},
		},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_REFERENCE" && strings.Contains(e.Path, "market_data.datasets[0].source_code") {
			found = true
			assert.Contains(t, e.Message, "BLOOMBERG")
			break
		}
	}
	assert.True(t, found, "expected INVALID_REFERENCE error for invalid source_code")
}

func TestValidate_MarketDataSetValidSourceReference(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "ECB", Name: "European Central Bank", TrustLevel: 95},
		},
		Datasets: []*controlplanev1.MarketDataSetDefinition{
			{Code: "USD_EUR_FX", Category: marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE, Unit: "USD/EUR", SourceCode: "ECB"},
		},
	}

	result := v.Validate(manifest, nil)
	// No INVALID_REFERENCE errors expected for the market data section
	for _, e := range result.Errors {
		if e.Code == "INVALID_REFERENCE" && strings.Contains(e.Path, "market_data") {
			t.Errorf("unexpected INVALID_REFERENCE error: %s", e.Message)
		}
	}
}

// --- Organization validation tests ---

func TestValidate_DuplicateOrganizationCodes(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "ACME", Name: "Acme Energy 1", PartyType: "ORGANIZATION"},
		{Code: "ACME", Name: "Acme Energy 2", PartyType: "ORGANIZATION"},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_CODE" && strings.Contains(e.Path, "organizations") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected DUPLICATE_CODE error for organizations")
}

func TestValidate_OrganizationReferencesInvalidPartyType(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "ACME", Name: "Acme Corp", PartyType: "UNKNOWN_TYPE"},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_REFERENCE" && strings.Contains(e.Path, "organizations[0].party_type") {
			found = true
			assert.Contains(t, e.Message, "UNKNOWN_TYPE")
			break
		}
	}
	assert.True(t, found, "expected INVALID_REFERENCE error for invalid party_type")
}

func TestValidate_OrganizationReferencesBuiltInPartyType(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "ACME", Name: "Acme Corp", PartyType: "ORGANIZATION"},
		{Code: "BOB", Name: "Bob Smith", PartyType: "PERSON"},
	}

	result := v.Validate(manifest, nil)
	for _, e := range result.Errors {
		if e.Code == "INVALID_REFERENCE" && strings.Contains(e.Path, "organizations") {
			t.Errorf("unexpected INVALID_REFERENCE error: %s", e.Message)
		}
	}
}

func TestValidate_OrganizationReferencesManifestDefinedPartyType(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{TenantId: "test", PartyType: "COUNTERPARTY", AttributeSchema: `{"type":"object"}`},
	}
	manifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "PARTNER", Name: "Trading Partner", PartyType: "COUNTERPARTY"},
	}

	result := v.Validate(manifest, nil)
	for _, e := range result.Errors {
		if e.Code == "INVALID_REFERENCE" && strings.Contains(e.Path, "organizations") {
			t.Errorf("unexpected INVALID_REFERENCE error: %s", e.Message)
		}
	}
}

func TestValidationError_ResourceTypeAndID_DuplicateCode(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.Instruments = append(manifest.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "GBP",
		Name: "Duplicate GBP",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "GBP",
			Precision: 2,
		},
	})

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_CODE" && strings.Contains(e.Path, "instruments") {
			found = true
			assert.Equal(t, "instrument", e.ResourceType, "expected resource_type=instrument")
			assert.Equal(t, "GBP", e.ResourceID, "expected resource_id=GBP")
			break
		}
	}
	assert.True(t, found, "expected DUPLICATE_CODE error for instruments")
}

func TestValidationError_ResourceTypeAndID_UndefinedInstrumentRef(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	// "GBX" is a typo for "GBP" — should produce a suggestion
	manifest.AccountTypes[0].AllowedInstruments = []string{"GBX"}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "UNDEFINED_INSTRUMENT_REFERENCE" {
			found = true
			assert.Equal(t, "account_type", e.ResourceType, "expected resource_type=account_type")
			assert.Equal(t, "SETTLEMENT", e.ResourceID, "expected resource_id=SETTLEMENT")
			assert.NotEmpty(t, e.Suggestion, "expected suggestion for unknown instrument reference")
			assert.Contains(t, e.Suggestion, "GBP", "expected suggestion to contain GBP")
			break
		}
	}
	assert.True(t, found, "expected UNDEFINED_INSTRUMENT_REFERENCE error")
}

func TestValidationError_ResourceTypeAndID_StarlarkSyntaxError(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.Sagas[0].Script = "def execute(ctx)\n    return {}\n" // Missing colon

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == CodeStarlarkSyntaxError || e.Code == CodeStarlarkCompilationError {
			found = true
			assert.Equal(t, "saga", e.ResourceType, "expected resource_type=saga")
			assert.Equal(t, "process_settlement", e.ResourceID, "expected resource_id=process_settlement")
			break
		}
	}
	assert.True(t, found, "expected STARLARK_SYNTAX_ERROR with resource context")
}

func TestValidationError_AggregatesAllErrors(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	// Introduce multiple errors: duplicate instrument + bad cross-reference
	manifest.Instruments = append(manifest.Instruments, &controlplanev1.InstrumentDefinition{
		Code:       "GBP",
		Name:       "Duplicate GBP",
		Type:       controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{Unit: "GBP", Precision: 2},
	})
	manifest.AccountTypes = append(manifest.AccountTypes, &controlplanev1.AccountTypeDefinition{
		Code:               "CASH",
		Name:               "Cash Account",
		NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
		AllowedInstruments: []string{"UNKNOWN_INSTRUMENT"},
	})

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)
	// Must have at least 2 errors - aggregation not fail-fast
	assert.GreaterOrEqual(t, len(result.Errors), 2,
		"expected multiple errors aggregated, got: %v", result.Errors)
}

func TestValidationError_CELError_ResourceTypeAndID(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.AccountTypes[0].Policies = &controlplanev1.AccountTypePolicies{
		Validation: "nonexistent_field > 0",
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "CEL_UNDECLARED_REFERENCE" || e.Code == "CEL_COMPILATION_ERROR" {
			found = true
			assert.Equal(t, "account_type", e.ResourceType, "expected resource_type=account_type")
			assert.Equal(t, "SETTLEMENT", e.ResourceID, "expected resource_id=SETTLEMENT")
			break
		}
	}
	assert.True(t, found, "expected CEL error with resource context")
}

// --- Internal Account validation tests ---

func TestValidate_DuplicateInternalAccountCodes(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "SETTLEMENT", Instrument: "GBP"},
		{Code: "REVENUE_GBP", AccountType: "SETTLEMENT", Instrument: "KWH"},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_CODE" && strings.Contains(e.Path, "internal_accounts") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected DUPLICATE_CODE error for internal_accounts")
}

func TestValidate_InternalAccountReferencesInvalidAccountType(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "NONEXISTENT", Instrument: "GBP"},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_REFERENCE" && strings.Contains(e.Path, "internal_accounts[0].account_type") {
			found = true
			assert.Contains(t, e.Message, "NONEXISTENT")
			break
		}
	}
	assert.True(t, found, "expected INVALID_REFERENCE error for invalid account_type")
}

func TestValidate_InternalAccountReferencesInvalidInstrument(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_XYZ", AccountType: "SETTLEMENT", Instrument: "XYZ"},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_REFERENCE" && strings.Contains(e.Path, "internal_accounts[0].instrument") {
			found = true
			assert.Contains(t, e.Message, "XYZ")
			break
		}
	}
	assert.True(t, found, "expected INVALID_REFERENCE error for invalid instrument")
}

func TestValidate_InternalAccountReferencesInvalidOrganization(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "SETTLEMENT", Instrument: "GBP", OwnerOrganization: "UNKNOWN_ORG"},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_REFERENCE" && strings.Contains(e.Path, "internal_accounts[0].owner_organization") {
			found = true
			assert.Contains(t, e.Message, "UNKNOWN_ORG")
			break
		}
	}
	assert.True(t, found, "expected INVALID_REFERENCE error for invalid owner_organization")
}

func TestValidate_InternalAccountValidReferences(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "ACME", Name: "Acme Corp", PartyType: "ORGANIZATION"},
	}
	manifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "SETTLEMENT", Instrument: "GBP", OwnerOrganization: "ACME"},
		{Code: "REVENUE_KWH", AccountType: "SETTLEMENT", Instrument: "KWH"},
	}

	result := v.Validate(manifest, nil)
	for _, e := range result.Errors {
		if e.Code == "INVALID_REFERENCE" && strings.Contains(e.Path, "internal_accounts") {
			t.Errorf("unexpected INVALID_REFERENCE error: %s", e.Message)
		}
	}
}

func TestValidate_InternalAccountNoOwnerOrganization(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	manifest := validManifest()
	manifest.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "SETTLEMENT", Instrument: "GBP"},
	}

	result := v.Validate(manifest, nil)
	for _, e := range result.Errors {
		if e.Code == "INVALID_REFERENCE" && strings.Contains(e.Path, "internal_accounts[0].owner_organization") {
			t.Errorf("unexpected INVALID_REFERENCE error for empty owner_organization: %s", e.Message)
		}
	}
}
