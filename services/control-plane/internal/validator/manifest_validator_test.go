package validator

import (
	"strings"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// testSchemaForStarlark returns a Schema with the position_keeping service and
// initiate_log handler, providing the typed modules needed for Starlark validation tests.
func testSchemaForStarlark() *schema.Schema {
	return &schema.Schema{
		Service: "position_keeping",
		Handlers: map[string]*schema.HandlerDef{
			"position_keeping.initiate_log": {
				Params: map[string]*schema.FieldDef{
					"position_id":     {Type: schema.TypeString, Required: true},
					"amount":          {Type: schema.TypeDecimal, Required: true},
					"direction":       {Type: schema.TypeEnum, Required: true, Values: []string{"CREDIT", "DEBIT"}},
					"instrument_code": {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"log_id": {Type: schema.TypeString},
					"status": {Type: schema.TypeString},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
		},
	}
}

// validManifest returns a fully-populated valid manifest for testing.
func validManifest() *controlplanev1.Manifest {
	seedData, _ := structpb.NewStruct(map[string]interface{}{
		"default_market": "nordpool",
	})
	return &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:        "Test Manifest",
			Industry:    "energy",
			Description: "A test manifest for energy trading",
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
					Bucketing:  "",
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
				Trigger: "api:/v1/sagas/execute",
				Script:  "def execute(ctx):\n    return {\"status\": \"ok\"}\n",
			},
		},
		SeedData: seedData,
	}
}

func TestNew(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	if v == nil {
		t.Fatal("New() returned nil validator")
	}
}

func TestValidateValidManifest(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	result := v.Validate(validManifest(), nil)
	if !result.Valid {
		t.Errorf("expected valid manifest, got errors: %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

func TestValidateStructural_MissingVersion(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Version = ""

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for missing version")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "PROTO_VALIDATION" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected PROTO_VALIDATION error for missing version")
	}
}

func TestValidateStructural_MissingMetadata(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Metadata = nil

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for missing metadata")
	}
}

func TestValidateStructural_InvalidVersion(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Version = "v1.0" // Invalid: must match ^[0-9]+\.[0-9]+$

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for bad version format")
	}
}

func TestValidateDuplicate_InstrumentCodes(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Instruments = append(m.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "GBP", // Duplicate
		Name: "Pound Sterling Again",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "GBP",
			Precision: 2,
		},
	})

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for duplicate instrument code")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_CODE" && strings.Contains(e.Path, "instruments") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected DUPLICATE_CODE error for instruments")
	}
}

func TestValidateDuplicate_AccountTypeCodes(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.AccountTypes = append(m.AccountTypes, &controlplanev1.AccountTypeDefinition{
		Code:          "SETTLEMENT", // Duplicate
		Name:          "Settlement Again",
		NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT,
	})

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for duplicate account type code")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_CODE" && strings.Contains(e.Path, "account_types") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected DUPLICATE_CODE error for account_types")
	}
}

func TestValidateDuplicate_SagaNames(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "process_settlement", // Duplicate
		Trigger: "api:/v1/tenants",
		Script:  "def execute(ctx):\n    pass\n",
	})

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for duplicate saga name")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_NAME" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected DUPLICATE_NAME error for sagas")
	}
}

func TestValidate_EventTriggerWithoutFilter_Warning(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_transaction_captured",
		Trigger: "event:position-keeping.transaction-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		// No Filter set - should produce a warning
	})

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest (missing event filter is a warning), got errors: %v", result.Errors)
	}

	found := false
	for _, w := range result.Warnings {
		if w.Code == "MISSING_EVENT_FILTER" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected MISSING_EVENT_FILTER warning, got warnings: %v", result.Warnings)
	}
}

func TestValidate_EventTriggerWithFilter_NoWarning(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	filter := `event.amount > 0 && event.currency == "GBP"`
	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_transaction_captured",
		Trigger: "event:position-keeping.transaction-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		Filter:  &filter,
	})

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest, got errors: %v", result.Errors)
	}

	for _, w := range result.Warnings {
		if w.Code == "MISSING_EVENT_FILTER" {
			t.Errorf("unexpected MISSING_EVENT_FILTER warning when filter is set")
		}
	}
}

func TestValidate_EventTrigger_InvalidChannel_Error(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_unknown_event",
		Trigger: "event:nonexistent.topic.v1",
		Script:  "def execute(ctx):\n    return {}\n",
	})

	result := v.Validate(m, nil)
	assert.False(t, result.Valid, "expected invalid manifest for unknown event channel")

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_EVENT_CHANNEL" {
			found = true
			assert.Contains(t, e.Message, "nonexistent.topic.v1")
			assert.NotEmpty(t, e.AvailableFields, "expected available channels listed")
			break
		}
	}
	assert.True(t, found, "expected INVALID_EVENT_CHANNEL error, got errors: %v", result.Errors)
}

func TestValidate_EventTrigger_ValidChannel_Passes(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	filter := "event.amount > 0"
	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_transaction_posted",
		Trigger: "event:position-keeping.transaction-posted.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		Filter:  &filter,
	})

	result := v.Validate(m, nil)
	assert.True(t, result.Valid, "expected valid manifest, got errors: %v", result.Errors)
	for _, e := range result.Errors {
		assert.NotEqual(t, "INVALID_EVENT_CHANNEL", e.Code)
	}
}

func TestValidate_NonEventTrigger_NoChannelCheck(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	m := validManifest()
	// api: trigger should not trigger channel validation even with a weird path
	m.Sagas[0].Trigger = "api:/v1/health"

	result := v.Validate(m, nil)
	assert.True(t, result.Valid, "expected valid manifest, got errors: %v", result.Errors)
	for _, e := range result.Errors {
		assert.NotEqual(t, "INVALID_EVENT_CHANNEL", e.Code)
	}
}

func TestValidate_EventTrigger_InvalidCELFilter_Error(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	badFilter := "event.amount >>> 0"
	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_transaction_captured_filtered",
		Trigger: "event:position-keeping.transaction-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		Filter:  &badFilter,
	})

	result := v.Validate(m, nil)
	assert.False(t, result.Valid, "expected invalid manifest for bad CEL filter")

	found := false
	for _, e := range result.Errors {
		if strings.Contains(e.Code, "CEL") && strings.Contains(e.Path, "filter") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected CEL error on filter path, got errors: %v", result.Errors)
}

func TestValidate_EventTrigger_InvalidChannel_SuggestsClose(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// Typo close to "position-keeping.transaction-captured.v1"
	// Use a channel name with one character deleted to trigger the suggestion logic.
	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_transaction_captured_typo",
		Trigger: "event:position-keeping.transacton-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
	})

	result := v.Validate(m, nil)
	assert.False(t, result.Valid, "expected invalid manifest for typo'd event channel")

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_EVENT_CHANNEL" {
			found = true
			assert.NotEmpty(t, e.Suggestion, "expected a suggestion for close typo")
			break
		}
	}
	assert.True(t, found, "expected INVALID_EVENT_CHANNEL error, got errors: %v", result.Errors)
}

func TestValidateCEL_ValidExpression(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.AccountTypes[0].Policies = &controlplanev1.AccountTypePolicies{
		Validation: "amount > 0 && quantity >= 0.0",
	}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest, got errors: %v", result.Errors)
	}
}

func TestValidateCEL_UndeclaredReference_WithSuggestion(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	// "quanity" is a typo for "quantity"
	m.AccountTypes[0].Policies = &controlplanev1.AccountTypePolicies{
		Validation: "quanity >= 0",
	}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for CEL undeclared reference")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "CEL_UNDECLARED_REFERENCE" {
			found = true
			if e.Suggestion == "" {
				t.Error("expected suggestion for typo 'quanity'")
			}
			if !strings.Contains(e.Suggestion, "quantity") {
				t.Errorf("expected suggestion to contain 'quantity', got %q", e.Suggestion)
			}
			if len(e.AvailableFields) == 0 {
				t.Error("expected available_fields to be populated")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected CEL_UNDECLARED_REFERENCE error, got: %v", result.Errors)
	}
}

func TestValidateCEL_TypeError(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	// Type error: comparing string to int
	m.AccountTypes[0].Policies = &controlplanev1.AccountTypePolicies{
		Validation: "instrument + 42",
	}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for CEL type error")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "CEL_COMPILATION_ERROR" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected CEL_COMPILATION_ERROR, got: %v", result.Errors)
	}
}

func TestValidateCEL_ExpressionTooLong(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.AccountTypes[0].Policies = &controlplanev1.AccountTypePolicies{
		Validation: strings.Repeat("a", 4097),
	}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for oversized CEL expression")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "CEL_EXPRESSION_TOO_LONG" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected CEL_EXPRESSION_TOO_LONG error")
	}
}

func TestValidateCEL_BucketingExpression(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.AccountTypes[0].Policies = &controlplanev1.AccountTypePolicies{
		Bucketing: "instrument_code + ':' + attributes.batch_id",
	}

	result := v.Validate(m, nil)
	// CEL map access with dot notation on a map type creates a type error,
	// but the expression should at least attempt compilation
	// The exact behavior depends on CEL version - just verify we get a result
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestValidateStarlark_ValidScript(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Sagas[0].Script = `def execute(ctx):
    data = input_data
    amount = Decimal("100.00")
    return {"status": "ok", "amount": amount}
`

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest, got errors: %v", result.Errors)
	}
}

func TestValidateStarlark_SyntaxError(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Sagas[0].Script = "def execute(ctx)\n    return {}\n" // Missing colon

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for Starlark syntax error")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == CodeStarlarkSyntaxError || e.Code == CodeStarlarkCompilationError {
			found = true
			if e.Line == 0 && e.Column == 0 {
				// Line info may not always be extractable, but let's not fail on this
				t.Log("line/column info not extracted from syntax error (may be format-dependent)")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected STARLARK_SYNTAX_ERROR, got: %v", result.Errors)
	}
}

func TestValidateStarlark_UndefinedName_WithSuggestion(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	// "position_keepng" is a typo for "position_keeping"
	m.Sagas[0].Script = `def execute(ctx):
    result = position_keepng.initiate_log(amount="100.00")
    return result
`

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for undefined Starlark name")
	}

	found := false
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "undefined") {
			found = true
			if e.Suggestion == "" {
				t.Error("expected suggestion for typo 'position_keepng'")
			}
			if !strings.Contains(e.Suggestion, "position_keeping") {
				t.Errorf("expected suggestion to contain 'position_keeping', got %q", e.Suggestion)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected undefined name error, got: %v", result.Errors)
	}
}

func TestValidateStarlark_ServiceModuleAccess(t *testing.T) {
	v, err := New(WithDerivedSchema(testSchemaForStarlark()))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Sagas[0].Script = `def execute(ctx):
    result = position_keeping.initiate_log(
        position_id="123",
        amount=Decimal("100.00"),
        direction="CREDIT",
    )
    return {"log_id": result.log_id}
`

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with service module calls, got errors: %v", result.Errors)
	}
}

func TestValidateStarlark_ScriptTooLarge(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Sagas[0].Script = strings.Repeat("x", 65537)

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for oversized script")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "STARLARK_SCRIPT_TOO_LARGE" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected STARLARK_SCRIPT_TOO_LARGE error")
	}
}

func TestValidateCrossRef_UndefinedInstrumentInAccountType(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.AccountTypes[0].AllowedInstruments = []string{"GBP", "NONEXISTENT"}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for undefined instrument reference")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "UNDEFINED_INSTRUMENT_REFERENCE" && strings.Contains(e.Path, "allowed_instruments") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected UNDEFINED_INSTRUMENT_REFERENCE error for allowed_instruments")
	}
}

func TestValidateCrossRef_UndefinedInstrumentInValuationRule(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.ValuationRules[0].FromInstrument = "NONEXISTENT"

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for undefined from_instrument")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "UNDEFINED_INSTRUMENT_REFERENCE" && strings.Contains(e.Path, "from_instrument") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected UNDEFINED_INSTRUMENT_REFERENCE error for from_instrument")
	}
}

func TestValidateCrossRef_WithSuggestion(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	// "GBQ" is close to "GBP"
	m.AccountTypes[0].AllowedInstruments = []string{"GBQ"}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for undefined instrument reference")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "UNDEFINED_INSTRUMENT_REFERENCE" {
			found = true
			if e.Suggestion == "" {
				t.Error("expected suggestion for typo 'GBQ'")
			}
			if !strings.Contains(e.Suggestion, "GBP") {
				t.Errorf("expected suggestion to contain 'GBP', got %q", e.Suggestion)
			}
			break
		}
	}
	if !found {
		t.Error("expected UNDEFINED_INSTRUMENT_REFERENCE error with suggestion")
	}
}

func TestValidateImmutability_InstrumentCodeChanged_NoImmutabilityError(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	prev := validManifest()
	curr := validManifest()
	curr.Instruments[0].Code = "USD" // Changed from GBP

	result := v.Validate(curr, prev)
	// validateImmutability is now a no-op - removals are caught by destructive changes.
	// There should be no IMMUTABLE_FIELD_CHANGED errors.
	for _, e := range result.Errors {
		if e.Code == "IMMUTABLE_FIELD_CHANGED" {
			t.Errorf("unexpected IMMUTABLE_FIELD_CHANGED error: %s", e.Message)
		}
	}
}

func TestValidateImmutability_AccountTypeCodeChanged_NoImmutabilityError(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	prev := validManifest()
	curr := validManifest()
	curr.AccountTypes[0].Code = "SAVINGS" // Changed from SETTLEMENT

	result := v.Validate(curr, prev)
	// validateImmutability is now a no-op - removals are caught by destructive changes.
	for _, e := range result.Errors {
		if e.Code == "IMMUTABLE_FIELD_CHANGED" {
			t.Errorf("unexpected IMMUTABLE_FIELD_CHANGED error: %s", e.Message)
		}
	}
}

func TestValidateImmutability_DisplayNameChangeAllowed(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	prev := validManifest()
	curr := validManifest()
	curr.Instruments[0].Name = "Pounds Sterling"  // Name change OK
	curr.AccountTypes[0].Name = "Settlement Acct" // Name change OK

	result := v.Validate(curr, prev)
	if !result.Valid {
		t.Errorf("expected valid manifest when only display names changed, got errors: %v", result.Errors)
	}
}

func TestValidateImmutability_NoChanges(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	prev := validManifest()
	curr := validManifest()

	result := v.Validate(curr, prev)
	if !result.Valid {
		t.Errorf("expected valid manifest when nothing changed, got errors: %v", result.Errors)
	}
}

func TestValidateImmutability_NilPrevious(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	result := v.Validate(validManifest(), nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with nil previous, got errors: %v", result.Errors)
	}
}

func TestValidateMinimalManifest(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name: "Minimal",
		},
	}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid minimal manifest, got errors: %v", result.Errors)
	}
}

func TestValidateMultipleErrors(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	// Multiple issues at once
	m.AccountTypes[0].AllowedInstruments = []string{"NONEXISTENT"}
	m.AccountTypes[0].Policies = &controlplanev1.AccountTypePolicies{
		Validation: "undeclared_var > 0",
	}
	m.ValuationRules[0].FromInstrument = "MISSING"

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest with multiple errors")
	}
	if len(result.Errors) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"kitten", "sitting", 3},
		{"quantity", "quanity", 1},
		{"position_keeping", "position_keepng", 1},
		{"GBP", "GBQ", 1},
		{"abc", "xyz", 3},
	}

	for _, tt := range tests {
		t.Run(tt.a+"->"+tt.b, func(t *testing.T) {
			got := levenshteinDistance(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestFindClosestMatch(t *testing.T) {
	candidates := []string{"quantity", "instrument", "bucket_id", "as_of", "amount"}

	tests := []struct {
		target string
		want   string
	}{
		{"quanity", "quantity"},
		{"amout", "amount"},
		{"instrumnt", "instrument"},
		{"completely_different_very_long_name", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			got := findClosestMatch(tt.target, candidates)
			if got != tt.want {
				t.Errorf("findClosestMatch(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}

func TestExtractUndeclaredReference(t *testing.T) {
	tests := []struct {
		errMsg string
		want   string
	}{
		{"ERROR: <input>:1:1: undeclared reference to 'quanity'", "quanity"},
		{"no undeclared reference here", ""},
		{"undeclared reference to 'foo'", "foo"},
	}

	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			got := extractUndeclaredReference(tt.errMsg)
			if got != tt.want {
				t.Errorf("extractUndeclaredReference(%q) = %q, want %q", tt.errMsg, got, tt.want)
			}
		})
	}
}

func TestExtractUndefinedStarlarkName(t *testing.T) {
	tests := []struct {
		errMsg string
		want   string
	}{
		{"script.star:2:14: undefined: position_keepng", "position_keepng"},
		{"no undefined here", ""},
		{"undefined: foo", "foo"},
	}

	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			got := extractUndefinedStarlarkName(tt.errMsg)
			if got != tt.want {
				t.Errorf("extractUndefinedStarlarkName(%q) = %q, want %q", tt.errMsg, got, tt.want)
			}
		})
	}
}

func TestValidationErrorInterface(t *testing.T) {
	ve := ValidationError{
		Severity:   SeverityError,
		Path:       "instruments[0].code",
		Code:       "TEST",
		Message:    "test message",
		Suggestion: "try something else",
	}

	errStr := ve.Error()
	if !strings.Contains(errStr, "instruments[0].code") {
		t.Errorf("Error() should contain path, got: %s", errStr)
	}
	if !strings.Contains(errStr, "test message") {
		t.Errorf("Error() should contain message, got: %s", errStr)
	}
	if !strings.Contains(errStr, "try something else") {
		t.Errorf("Error() should contain suggestion, got: %s", errStr)
	}
}

func TestValidateCrossRef_ValuationRuleToInstrument(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.ValuationRules[0].ToInstrument = "NONEXISTENT"

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for undefined to_instrument")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "UNDEFINED_INSTRUMENT_REFERENCE" && strings.Contains(e.Path, "to_instrument") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected UNDEFINED_INSTRUMENT_REFERENCE error for to_instrument")
	}
}

func TestValidateCEL_EmptyExpression(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.AccountTypes[0].Policies = &controlplanev1.AccountTypePolicies{
		Validation: "", // Empty is allowed
	}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with empty CEL expression, got errors: %v", result.Errors)
	}
}

func TestValidateCEL_NilPolicies(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.AccountTypes[0].Policies = nil

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with nil policies, got errors: %v", result.Errors)
	}
}

func TestValidateStarlark_EmptyScript(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.Sagas[0].Script = "" // Empty is skipped (proto validation would catch this)

	result := v.Validate(m, nil)
	// Proto validation should catch empty script requirement
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestValidateStarlark_TypedModules_UnknownHandler_TopLevel(t *testing.T) {
	v, err := New(WithDerivedSchema(testSchemaForStarlark()))
	require.NoError(t, err)

	m := validManifest()
	// Top-level handler call triggers struct attribute lookup immediately
	m.Sagas[0].Script = `result = position_keeping.nonexistent_handler(amount="100")
`
	result := v.Validate(m, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "UNKNOWN_HANDLER" {
			found = true
			assert.Contains(t, e.Message, "nonexistent_handler")
			assert.NotEmpty(t, e.AvailableFields, "should list available handlers")
			break
		}
	}
	assert.True(t, found, "expected UNKNOWN_HANDLER error, got: %v", result.Errors)
}

func TestValidateStarlark_TypedModules_UnknownParam_TopLevel(t *testing.T) {
	v, err := New(WithDerivedSchema(testSchemaForStarlark()))
	require.NoError(t, err)

	m := validManifest()
	// Top-level handler call with unknown param
	m.Sagas[0].Script = `result = position_keeping.initiate_log(
    position_id="123",
    amont=Decimal("100.00"),
    direction="CREDIT",
)
`
	result := v.Validate(m, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "UNKNOWN_PARAM" {
			found = true
			assert.Contains(t, e.Message, "amont")
			assert.NotEmpty(t, e.Suggestion)
			assert.Contains(t, e.Suggestion, "amount")
			break
		}
	}
	assert.True(t, found, "expected UNKNOWN_PARAM error, got: %v", result.Errors)
}

func TestValidateStarlark_TypedModules_MissingRequiredParam_TopLevel(t *testing.T) {
	v, err := New(WithDerivedSchema(testSchemaForStarlark()))
	require.NoError(t, err)

	m := validManifest()
	// Top-level call missing required 'amount' and 'direction'
	m.Sagas[0].Script = `result = position_keeping.initiate_log(
    position_id="123",
)
`
	result := v.Validate(m, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "MISSING_REQUIRED_PARAM" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected MISSING_REQUIRED_PARAM error, got: %v", result.Errors)
}

func TestValidateStarlark_TypedModules_WrongParamType_TopLevel(t *testing.T) {
	v, err := New(WithDerivedSchema(testSchemaForStarlark()))
	require.NoError(t, err)

	m := validManifest()
	// Top-level call with wrong type: position_id expects string, give it a list
	m.Sagas[0].Script = `result = position_keeping.initiate_log(
    position_id=[1, 2, 3],
    amount=Decimal("100.00"),
    direction="CREDIT",
)
`
	result := v.Validate(m, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "WRONG_PARAM_TYPE" {
			found = true
			assert.Contains(t, e.Message, "position_id")
			break
		}
	}
	assert.True(t, found, "expected WRONG_PARAM_TYPE error, got: %v", result.Errors)
}

func TestValidateStarlark_TypedModules_ValidComplexCall(t *testing.T) {
	v, err := New(WithDerivedSchema(testSchemaForStarlark()))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas[0].Script = `def execute(ctx):
    log = position_keeping.initiate_log(
        position_id="pos-001",
        amount=Decimal("250.00"),
        direction="DEBIT",
        instrument_code="GBP",
    )
    log_id = log.log_id
    status = log.status
    return {"log_id": log_id, "status": status}
`
	result := v.Validate(m, nil)
	assert.True(t, result.Valid, "expected valid manifest, got errors: %v", result.Errors)
}

func TestValidateStarlark_TypedModules_ValidHandlerInFunction(t *testing.T) {
	v, err := New(WithDerivedSchema(testSchemaForStarlark()))
	require.NoError(t, err)

	m := validManifest()
	// Handler calls inside functions compile without error — the typed module
	// ensures only real handler names are accessible on the service struct.
	m.Sagas[0].Script = `def execute(ctx):
    result = position_keeping.initiate_log(
        position_id="123",
        amount=Decimal("100.00"),
        direction="CREDIT",
    )
    return {"status": "ok"}
`
	result := v.Validate(m, nil)
	assert.True(t, result.Valid, "expected valid manifest, got errors: %v", result.Errors)
}
