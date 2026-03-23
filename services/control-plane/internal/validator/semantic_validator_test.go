package validator

import (
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Settlement Completeness ──────────────────────────────────────────────────

func TestValidateSettlementCompleteness_AllowedInstrument(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// SETTLEMENT account type restricts to GBP; add an internal account using GBP
	m.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{
			Code:        "REVENUE_GBP",
			AccountType: "SETTLEMENT",
			Instrument:  "GBP",
		},
	}

	result := v.Validate(m, nil)
	for _, e := range result.Errors {
		assert.NotEqual(t, "INSTRUMENT_NOT_ALLOWED_FOR_ACCOUNT_TYPE", e.Code,
			"should not flag GBP in SETTLEMENT account type")
	}
}

func TestValidateSettlementCompleteness_DisallowedInstrument(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// SETTLEMENT account type restricts to GBP; internal account uses KWH - mismatch
	m.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{
			Code:        "ENERGY_RESERVE",
			AccountType: "SETTLEMENT",
			Instrument:  "KWH",
		},
	}

	result := v.Validate(m, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "INSTRUMENT_NOT_ALLOWED_FOR_ACCOUNT_TYPE" {
			found = true
			assert.Equal(t, "internal_accounts[0].instrument", e.Path)
			assert.Equal(t, "internal_account", e.ResourceType)
			assert.Equal(t, "ENERGY_RESERVE", e.ResourceID)
			break
		}
	}
	assert.True(t, found, "expected INSTRUMENT_NOT_ALLOWED_FOR_ACCOUNT_TYPE error")
}

func TestValidateSettlementCompleteness_UnrestrictedAccountType(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// Add account type with no allowed_instruments restriction
	m.AccountTypes = append(m.AccountTypes, &controlplanev1.AccountTypeDefinition{
		Code:          "OMNIBUS",
		Name:          "Omnibus Account",
		NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
		// AllowedInstruments is empty = all instruments allowed
	})
	m.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{
			Code:        "OMNIBUS_KWH",
			AccountType: "OMNIBUS",
			Instrument:  "KWH",
		},
	}

	result := v.Validate(m, nil)
	for _, e := range result.Errors {
		assert.NotEqual(t, "INSTRUMENT_NOT_ALLOWED_FOR_ACCOUNT_TYPE", e.Code,
			"unrestricted account type should allow any instrument")
	}
}

// ─── Saga Handler Completeness ────────────────────────────────────────────────

func TestValidateSagaHandlerCompleteness_KnownHandler(t *testing.T) {
	reg := &schema.Schema{
		Service: "meridian",
		Handlers: map[string]*schema.HandlerDef{
			"position_keeping.initiate_log": {
				Params: map[string]*schema.FieldDef{},
			},
		},
	}
	v, err := New(WithDerivedSchema(reg), WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "test_saga",
			Trigger: "api:/v1/sagas/execute",
			Script:  "def execute(ctx):\n    result = position_keeping.initiate_log(position_id='x', amount=Decimal('1'), direction='CREDIT')\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "UNKNOWN_HANDLER_REFERENCE", w.Code,
			"known handler should not produce UNKNOWN_HANDLER_REFERENCE warning")
	}
}

func TestValidateSagaHandlerCompleteness_UnknownHandler(t *testing.T) {
	reg := &schema.Schema{
		Service: "meridian",
		Handlers: map[string]*schema.HandlerDef{
			"position_keeping.initiate_log": {
				Params: map[string]*schema.FieldDef{},
			},
		},
	}
	v, err := New(WithDerivedSchema(reg), WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "test_saga",
			Trigger: "api:/v1/sagas/execute",
			// Calls position_keeping.nonexistent_handler which is not in registry
			Script: "def execute(ctx):\n    position_keeping.nonexistent_handler(x=1)\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	found := false
	for _, w := range result.Warnings {
		if w.Code == "UNKNOWN_HANDLER_REFERENCE" {
			found = true
			assert.Equal(t, "sagas[0].script", w.Path)
			assert.Equal(t, "saga", w.ResourceType)
			assert.Equal(t, "test_saga", w.ResourceID)
			break
		}
	}
	assert.True(t, found, "expected UNKNOWN_HANDLER_REFERENCE warning for unknown handler")
}

func TestValidateSagaHandlerCompleteness_NoSchema_NoWarning(t *testing.T) {
	// Without a schema registry, no UNKNOWN_HANDLER_REFERENCE warnings should fire
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "test_saga",
			Trigger: "api:/v1/sagas/execute",
			Script:  "def execute(ctx):\n    position_keeping.completely_made_up()\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "UNKNOWN_HANDLER_REFERENCE", w.Code,
			"without schema registry, handler completeness check should be skipped")
	}
}

// ─── Valuation Rule Cycle Detection ──────────────────────────────────────────

func TestValidateValuationRuleCycles_NoCycle(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// validManifest already has KWH -> GBP, no cycle

	result := v.Validate(m, nil)
	for _, e := range result.Errors {
		assert.NotEqual(t, "VALUATION_RULE_CYCLE", e.Code, "no cycle should be detected")
	}
}

func TestValidateValuationRuleCycles_DirectCycle(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// Add GBP -> KWH to create a cycle: KWH -> GBP -> KWH
	m.ValuationRules = append(m.ValuationRules, &controlplanev1.ValuationRule{
		FromInstrument: "GBP",
		ToInstrument:   "KWH",
		Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
		Source:         "manual",
	})

	result := v.Validate(m, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "VALUATION_RULE_CYCLE" {
			found = true
			assert.Equal(t, "valuation_rules", e.Path)
			assert.Equal(t, "valuation_rule", e.ResourceType)
			assert.Contains(t, e.Message, "circular dependency")
			break
		}
	}
	assert.True(t, found, "expected VALUATION_RULE_CYCLE error for GBP<->KWH cycle")
}

func TestValidateValuationRuleCycles_ThreeNodeCycle(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// Add EUR instrument and EUR->GBP, GBP->EUR rules to create a cycle
	m.Instruments = append(m.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "EUR",
			Precision: 2,
		},
	})
	m.ValuationRules = []*controlplanev1.ValuationRule{
		{
			FromInstrument: "GBP",
			ToInstrument:   "EUR",
			Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
			Source:         "ecb",
		},
		{
			FromInstrument: "EUR",
			ToInstrument:   "KWH",
			Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
			Source:         "spot",
		},
		{
			FromInstrument: "KWH",
			ToInstrument:   "GBP",
			Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
			Source:         "spot",
		},
	}

	result := v.Validate(m, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "VALUATION_RULE_CYCLE" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected VALUATION_RULE_CYCLE error for three-node cycle")
}

func TestValidateValuationRuleCycles_DAGNoCycle(t *testing.T) {
	// EUR -> GBP -> KWH is a DAG (no cycle)
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	m.Instruments = append(m.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "EUR",
			Precision: 2,
		},
	})
	m.ValuationRules = []*controlplanev1.ValuationRule{
		{
			FromInstrument: "KWH",
			ToInstrument:   "GBP",
			Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
			Source:         "nordpool",
		},
		{
			FromInstrument: "EUR",
			ToInstrument:   "GBP",
			Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
			Source:         "ecb",
		},
	}
	// Both EUR and KWH convert to GBP but there is no cycle

	result := v.Validate(m, nil)
	for _, e := range result.Errors {
		assert.NotEqual(t, "VALUATION_RULE_CYCLE", e.Code, "DAG should not report cycle")
	}
}

// ─── Instrument-Account Type Consistency ─────────────────────────────────────

func TestValidateInstrumentAccountTypeConsistency_UsedCombination(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// SETTLEMENT allows GBP; add an internal account that uses GBP
	m.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{
			Code:        "REVENUE_GBP",
			AccountType: "SETTLEMENT",
			Instrument:  "GBP",
		},
	}

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "INSTRUMENT_UNUSED_IN_ACCOUNT_TYPE", w.Code,
			"used instrument+account_type combination should not warn")
	}
}

func TestValidateInstrumentAccountTypeConsistency_UnusedCombination(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// SETTLEMENT allows GBP but also add KWH; only GBP internal account exists
	m.AccountTypes = []*controlplanev1.AccountTypeDefinition{
		{
			Code:               "SETTLEMENT",
			Name:               "Settlement Account",
			NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
			AllowedInstruments: []string{"GBP", "KWH"},
		},
	}
	m.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{
			Code:        "REVENUE_GBP",
			AccountType: "SETTLEMENT",
			Instrument:  "GBP",
		},
	}

	result := v.Validate(m, nil)
	found := false
	for _, w := range result.Warnings {
		if w.Code == "INSTRUMENT_UNUSED_IN_ACCOUNT_TYPE" && w.ResourceID == "SETTLEMENT" {
			found = true
			assert.Contains(t, w.Message, "KWH")
			break
		}
	}
	assert.True(t, found, "expected INSTRUMENT_UNUSED_IN_ACCOUNT_TYPE warning for KWH in SETTLEMENT")
}

func TestValidateInstrumentAccountTypeConsistency_NoInternalAccounts(t *testing.T) {
	// When no internal accounts are defined, the check is skipped
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// No internal accounts

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "INSTRUMENT_UNUSED_IN_ACCOUNT_TYPE", w.Code,
			"check should be skipped when no internal accounts are defined")
	}
}

// ─── Orphaned Instruments ─────────────────────────────────────────────────────

func TestValidateOrphanedInstruments_Referenced(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// GBP is in SETTLEMENT.allowed_instruments; KWH is in a valuation rule
	// Both are referenced, no orphan warnings expected

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "ORPHANED_INSTRUMENT", w.Code,
			"referenced instruments should not produce orphan warnings")
	}
}

func TestValidateOrphanedInstruments_Orphaned(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// Add USD instrument with no references anywhere
	m.Instruments = append(m.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "USD",
		Name: "US Dollar",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "USD",
			Precision: 2,
		},
	})

	result := v.Validate(m, nil)
	found := false
	for _, w := range result.Warnings {
		if w.Code == "ORPHANED_INSTRUMENT" && w.ResourceID == "USD" {
			found = true
			assert.Contains(t, w.Message, "USD")
			assert.Equal(t, "instrument", w.ResourceType)
			break
		}
	}
	assert.True(t, found, "expected ORPHANED_INSTRUMENT warning for USD")
}

func TestValidateOrphanedInstruments_ReferencedByInternalAccount(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	// Add account type with no allowed_instruments and internal account using KWH
	// KWH is also in valuation rule so it's not orphaned
	m.AccountTypes = append(m.AccountTypes, &controlplanev1.AccountTypeDefinition{
		Code:          "ENERGY",
		Name:          "Energy Account",
		NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
	})
	m.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{
			Code:        "ENERGY_KWH",
			AccountType: "ENERGY",
			Instrument:  "KWH",
		},
	}

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		if w.Code == "ORPHANED_INSTRUMENT" && w.ResourceID == "KWH" {
			t.Error("KWH referenced by internal account should not be orphaned")
		}
	}
}

// ─── Valid Manifest Produces No New Errors ────────────────────────────────────

func TestSemanticValidations_ValidManifest_NoNewErrors(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil), WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	m := validManifest()
	result := v.Validate(m, nil)
	assert.True(t, result.Valid, "valid manifest should pass semantic validations: %v", result.Errors)

	semanticCodes := map[string]bool{
		"INSTRUMENT_NOT_ALLOWED_FOR_ACCOUNT_TYPE": true,
		"UNKNOWN_HANDLER_REFERENCE":               true,
		"VALUATION_RULE_CYCLE":                    true,
	}
	for _, e := range result.Errors {
		assert.False(t, semanticCodes[e.Code], "unexpected semantic error %q: %s", e.Code, e.Message)
	}
}
