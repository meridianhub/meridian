package saga

import (
	"context"
	"testing"

	"github.com/google/uuid"
	pkgsaga "github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockInstrumentChecker implements InstrumentChecker for testing.
type mockInstrumentChecker struct {
	instruments      map[string]bool // code -> isActive
	attributeSchemas map[string]map[string]interface{}
	existsError      error
	activeError      error
	schemaError      error
	listCodesError   error
}

func newMockInstrumentChecker() *mockInstrumentChecker {
	return &mockInstrumentChecker{
		instruments:      make(map[string]bool),
		attributeSchemas: make(map[string]map[string]interface{}),
	}
}

func (m *mockInstrumentChecker) InstrumentExists(_ context.Context, code string) (bool, error) {
	if m.existsError != nil {
		return false, m.existsError
	}
	_, exists := m.instruments[code]
	return exists, nil
}

func (m *mockInstrumentChecker) InstrumentIsActive(_ context.Context, code string) (bool, error) {
	if m.activeError != nil {
		return false, m.activeError
	}
	active, exists := m.instruments[code]
	return exists && active, nil
}

func (m *mockInstrumentChecker) GetAttributeSchema(_ context.Context, code string) (map[string]interface{}, error) {
	if m.schemaError != nil {
		return nil, m.schemaError
	}
	return m.attributeSchemas[code], nil
}

func (m *mockInstrumentChecker) ListActiveInstrumentCodes(_ context.Context) ([]string, error) {
	if m.listCodesError != nil {
		return nil, m.listCodesError
	}
	var codes []string
	for code, active := range m.instruments {
		if active {
			codes = append(codes, code)
		}
	}
	return codes, nil
}

// mockDefinitionChecker implements SagaChecker for testing.
type mockDefinitionChecker struct {
	sagas          map[string]bool // name -> isActive
	existsError    error
	activeError    error
	listNamesError error
}

func newMockDefinitionChecker() *mockDefinitionChecker {
	return &mockDefinitionChecker{
		sagas: make(map[string]bool),
	}
}

func (m *mockDefinitionChecker) SagaExists(_ context.Context, name string) (bool, error) {
	if m.existsError != nil {
		return false, m.existsError
	}
	_, exists := m.sagas[name]
	return exists, nil
}

func (m *mockDefinitionChecker) SagaIsActive(_ context.Context, name string) (bool, error) {
	if m.activeError != nil {
		return false, m.activeError
	}
	active, exists := m.sagas[name]
	return exists && active, nil
}

func (m *mockDefinitionChecker) ListActiveSagaNames(_ context.Context) ([]string, error) {
	if m.listNamesError != nil {
		return nil, m.listNamesError
	}
	var names []string
	for name, active := range m.sagas {
		if active {
			names = append(names, name)
		}
	}
	return names, nil
}

func TestExtractReferences_Empty(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	refs, err := v.ExtractReferences("")
	require.NoError(t, err)
	assert.Empty(t, refs)
}

func TestExtractReferences_InstrumentReferences(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga():
    instrument = resolve_instrument("KWH")
    other = resolve_instrument("USD")
    return instrument
`
	refs, err := v.ExtractReferences(script)
	require.NoError(t, err)
	require.Len(t, refs, 2)

	assert.Equal(t, ReferenceTypeInstrument, refs[0].Type)
	assert.Equal(t, "KWH", refs[0].Key)
	assert.Equal(t, 3, refs[0].LineNumber)

	assert.Equal(t, ReferenceTypeInstrument, refs[1].Type)
	assert.Equal(t, "USD", refs[1].Key)
	assert.Equal(t, 4, refs[1].LineNumber)
}

func TestExtractReferences_AccountReferences(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga():
    account = resolve_account("clearing_GBP")
    other = resolve_account("settlement_EUR")
    return account
`
	refs, err := v.ExtractReferences(script)
	require.NoError(t, err)
	require.Len(t, refs, 2)

	assert.Equal(t, ReferenceTypeAccount, refs[0].Type)
	assert.Equal(t, "clearing_GBP", refs[0].Key)
	assert.Equal(t, 3, refs[0].LineNumber)

	assert.Equal(t, ReferenceTypeAccount, refs[1].Type)
	assert.Equal(t, "settlement_EUR", refs[1].Key)
	assert.Equal(t, 4, refs[1].LineNumber)
}

func TestExtractReferences_SagaReferences(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga():
    invoke_saga("withdrawal")
    invoke_saga("deposit")
`
	refs, err := v.ExtractReferences(script)
	require.NoError(t, err)
	require.Len(t, refs, 2)

	assert.Equal(t, ReferenceTypeSaga, refs[0].Type)
	assert.Equal(t, "withdrawal", refs[0].Key)
	assert.Equal(t, 3, refs[0].LineNumber)

	assert.Equal(t, ReferenceTypeSaga, refs[1].Type)
	assert.Equal(t, "deposit", refs[1].Key)
	assert.Equal(t, 4, refs[1].LineNumber)
}

func TestExtractReferences_StepHandlerReferences(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga():
    step(name="init", action="position_keeping.initiate_log")
    step(name="post", action="financial_accounting.post_entries")
`
	refs, err := v.ExtractReferences(script)
	require.NoError(t, err)
	require.Len(t, refs, 2)

	assert.Equal(t, ReferenceTypeStepHandler, refs[0].Type)
	assert.Equal(t, "position_keeping.initiate_log", refs[0].Key)
	assert.Equal(t, 3, refs[0].LineNumber)

	assert.Equal(t, ReferenceTypeStepHandler, refs[1].Type)
	assert.Equal(t, "financial_accounting.post_entries", refs[1].Key)
	assert.Equal(t, 4, refs[1].LineNumber)
}

func TestExtractReferences_AttributeReferences(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga(ctx):
    value = ctx.position.attributes["meter_reading"]
    other = ctx.instrument.attributes["serial_number"]
`
	refs, err := v.ExtractReferences(script)
	require.NoError(t, err)
	require.Len(t, refs, 2)

	assert.Equal(t, ReferenceTypeAttribute, refs[0].Type)
	assert.Equal(t, "meter_reading", refs[0].AttributeKey)
	assert.Equal(t, 3, refs[0].LineNumber)

	assert.Equal(t, ReferenceTypeAttribute, refs[1].Type)
	assert.Equal(t, "serial_number", refs[1].AttributeKey)
	assert.Equal(t, 4, refs[1].LineNumber)
}

func TestExtractReferences_AllTypes(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga(ctx):
    # Step handler
    step(name="init", action="position_keeping.initiate_log")

    # Instrument
    instrument = resolve_instrument("KWH")

    # Account
    account = resolve_account("clearing_GBP")

    # Saga
    invoke_saga("withdrawal")

    # Attribute
    value = ctx.position.attributes["meter_reading"]
`
	refs, err := v.ExtractReferences(script)
	require.NoError(t, err)
	require.Len(t, refs, 5)

	// Verify all types are present
	types := make(map[ReferenceType]bool)
	for _, ref := range refs {
		types[ref.Type] = true
	}

	assert.True(t, types[ReferenceTypeStepHandler])
	assert.True(t, types[ReferenceTypeInstrument])
	assert.True(t, types[ReferenceTypeAccount])
	assert.True(t, types[ReferenceTypeSaga])
	assert.True(t, types[ReferenceTypeAttribute])
}

func TestExtractReferences_SyntaxError(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga(
    # Missing closing parenthesis
`
	_, err := v.ExtractReferences(script)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse script")
}

func TestValidateDraft_AllowsWarnings(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	instrumentChecker := newMockInstrumentChecker()
	// KWH exists but is not active
	instrumentChecker.instruments["KWH"] = false

	v := NewReferenceValidator(registry, instrumentChecker, nil, nil)

	script := `
def my_saga():
    instrument = resolve_instrument("KWH")
    missing = resolve_instrument("NONEXISTENT")
`
	result, err := v.ValidateDraft(context.Background(), uuid.New(), script)
	require.NoError(t, err)

	// Should have warnings but still be allowed (not BLOCKED)
	assert.Equal(t, "WARNINGS", result.Status)
	assert.Len(t, result.Errors, 1) // Only NONEXISTENT doesn't exist

	// The error should be a warning, not critical
	assert.False(t, result.Errors[0].IsCritical)
	assert.Equal(t, "NONEXISTENT", result.Errors[0].Reference.Key)
}

func TestValidateActivation_BlocksOnMissingHandler(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	// Don't register any handlers

	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga():
    step(name="init", action="nonexistent_handler")
`
	result, err := v.ValidateActivation(context.Background(), uuid.New(), script)
	require.NoError(t, err)

	assert.Equal(t, "BLOCKED", result.Status)
	require.Len(t, result.Errors, 1)
	assert.True(t, result.Errors[0].IsCritical)
	assert.Contains(t, result.Errors[0].Message, "nonexistent_handler")
}

func TestValidateActivation_BlocksOnDeprecatedInstrument(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	instrumentChecker := newMockInstrumentChecker()
	// KWH exists but is not active (deprecated)
	instrumentChecker.instruments["KWH"] = false

	v := NewReferenceValidator(registry, instrumentChecker, nil, nil)

	script := `
def my_saga():
    instrument = resolve_instrument("KWH")
`
	result, err := v.ValidateActivation(context.Background(), uuid.New(), script)
	require.NoError(t, err)

	assert.Equal(t, "BLOCKED", result.Status)
	require.Len(t, result.Errors, 1)
	assert.True(t, result.Errors[0].IsCritical)
	assert.Contains(t, result.Errors[0].Message, "not in ACTIVE status")
}

func TestValidateActivation_BlocksOnMissingSaga(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	sagaChecker := newMockDefinitionChecker()
	// No sagas registered

	v := NewReferenceValidator(registry, nil, sagaChecker, nil)

	script := `
def my_saga():
    invoke_saga("nonexistent_saga")
`
	result, err := v.ValidateActivation(context.Background(), uuid.New(), script)
	require.NoError(t, err)

	assert.Equal(t, "BLOCKED", result.Status)
	require.Len(t, result.Errors, 1)
	assert.True(t, result.Errors[0].IsCritical)
	assert.Contains(t, result.Errors[0].Message, "does not exist")
}

func TestValidateActivation_BlocksOnInactiveSaga(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	sagaChecker := newMockDefinitionChecker()
	sagaChecker.sagas["withdrawal"] = false // exists but not active

	v := NewReferenceValidator(registry, nil, sagaChecker, nil)

	script := `
def my_saga():
    invoke_saga("withdrawal")
`
	result, err := v.ValidateActivation(context.Background(), uuid.New(), script)
	require.NoError(t, err)

	assert.Equal(t, "BLOCKED", result.Status)
	require.Len(t, result.Errors, 1)
	assert.True(t, result.Errors[0].IsCritical)
	assert.Contains(t, result.Errors[0].Message, "does not have an ACTIVE version")
}

func TestValidateActivation_BlocksOnMissingAttribute(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	instrumentChecker := newMockInstrumentChecker()
	instrumentChecker.instruments["KWH"] = true
	instrumentChecker.attributeSchemas["KWH"] = map[string]interface{}{
		"meter_id":  "string",
		"reading":   "decimal",
		"timestamp": "datetime",
	}

	v := NewReferenceValidator(registry, instrumentChecker, nil, nil)

	script := `
def my_saga():
    # Typo in attribute name
    value = resolve_instrument("KWH").attributes["meteer_reading"]
`
	result, err := v.ValidateActivation(context.Background(), uuid.New(), script)
	require.NoError(t, err)

	// Should find the attribute reference with a typo
	assert.Equal(t, "BLOCKED", result.Status)
	require.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Message, "not found in instrument")
}

func TestValidateActivation_SuggestsSimilarNames(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	_ = registry.Register("position_keeping.initiate_log", nil)
	_ = registry.Register("position_keeping.update_log", nil)
	_ = registry.Register("financial_accounting.post_entries", nil)

	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga():
    # Typo - missing underscore
    step(name="init", action="positionkeeping.initiate_log")
`
	result, err := v.ValidateActivation(context.Background(), uuid.New(), script)
	require.NoError(t, err)

	assert.Equal(t, "BLOCKED", result.Status)
	require.Len(t, result.Errors, 1)
	// Should suggest similar handler
	assert.Contains(t, result.Errors[0].Suggestion, "position_keeping")
}

func TestValidateActivation_PassesWithValidReferences(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	_ = registry.Register("position_keeping.initiate_log", nil)

	instrumentChecker := newMockInstrumentChecker()
	instrumentChecker.instruments["KWH"] = true

	sagaChecker := newMockDefinitionChecker()
	sagaChecker.sagas["withdrawal"] = true

	v := NewReferenceValidator(registry, instrumentChecker, sagaChecker, nil)

	script := `
def my_saga():
    step(name="init", action="position_keeping.initiate_log")
    instrument = resolve_instrument("KWH")
    invoke_saga("withdrawal")
`
	result, err := v.ValidateActivation(context.Background(), uuid.New(), script)
	require.NoError(t, err)

	assert.Equal(t, "READY", result.Status)
	assert.Empty(t, result.Errors)
	assert.Len(t, result.References, 3)
}

func TestValidateRuntime_ChecksHandlersOnly(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	_ = registry.Register("position_keeping.initiate_log", nil)

	v := NewReferenceValidator(registry, nil, nil, nil)

	// Script with valid handler
	def := &Definition{
		ID:     uuid.New(),
		Script: `step(name="init", action="position_keeping.initiate_log")`,
	}

	err := v.ValidateRuntime(context.Background(), def)
	require.NoError(t, err)

	// Script with invalid handler
	def.Script = `step(name="init", action="nonexistent_handler")`
	err = v.ValidateRuntime(context.Background(), def)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step handler not registered")
}

func TestValidationResult_FormatReport(t *testing.T) {
	result := &ValidationResult{
		Status: "BLOCKED",
		References: []Reference{
			{Type: ReferenceTypeStepHandler, Key: "handler1", LineNumber: 5},
			{Type: ReferenceTypeInstrument, Key: "KWH", LineNumber: 10},
		},
		Errors: []ValidationError{
			{
				Reference:  Reference{Type: ReferenceTypeStepHandler, Key: "bad_handler", LineNumber: 15},
				Message:    "step handler 'bad_handler' is not registered",
				Suggestion: "Did you mean 'position_keeping.initiate_log'?",
				IsCritical: true,
			},
			{
				Reference:  Reference{Type: ReferenceTypeInstrument, Key: "XYZ", LineNumber: 20},
				Message:    "instrument 'XYZ' does not exist",
				IsCritical: false,
			},
		},
	}

	report := result.FormatReport()

	// Check report contains expected sections
	assert.Contains(t, report, "=== Saga Validation Report ===")
	assert.Contains(t, report, "Step Handlers:")
	assert.Contains(t, report, "Instrument References:")
	assert.Contains(t, report, "[X]") // Critical error icon
	assert.Contains(t, report, "[!]") // Warning icon
	assert.Contains(t, report, "bad_handler")
	assert.Contains(t, report, "Did you mean")
	assert.Contains(t, report, "Status: BLOCKED")
	assert.Contains(t, report, "Critical Errors: 1")
	assert.Contains(t, report, "Warnings: 1")
}

func TestValidator_ImplementsValidatorInterface(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	_ = registry.Register("position_keeping.initiate_log", nil)

	v := NewReferenceValidator(registry, nil, nil, nil)

	def := &Definition{
		ID:     uuid.New(),
		Script: `step(name="init", action="position_keeping.initiate_log")`,
	}

	// Should satisfy the Validator interface
	err := v.Validate(context.Background(), def)
	require.NoError(t, err)

	// Invalid script should return error
	def.Script = `step(name="init", action="nonexistent")`
	err = v.Validate(context.Background(), def)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestExtractReferences_NestedExpressions(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga(ctx):
    if ctx.amount > 100:
        instrument = resolve_instrument("USD")
    else:
        instrument = resolve_instrument("EUR")

    for i in range(10):
        step(name="process_" + str(i), action="financial_accounting.post_entries")
`
	refs, err := v.ExtractReferences(script)
	require.NoError(t, err)

	// Should find all references in nested structures
	instrumentRefs := 0
	handlerRefs := 0
	for _, ref := range refs {
		if ref.Type == ReferenceTypeInstrument {
			instrumentRefs++
		}
		if ref.Type == ReferenceTypeStepHandler {
			handlerRefs++
		}
	}
	assert.Equal(t, 2, instrumentRefs)
	assert.Equal(t, 1, handlerRefs)
}

func TestExtractReferences_ListComprehension(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def my_saga():
    codes = ["USD", "EUR", "GBP"]
    instruments = [resolve_instrument(code) for code in codes]
`
	refs, err := v.ExtractReferences(script)
	require.NoError(t, err)

	// resolve_instrument is called with a variable, not a literal
	// So we won't extract a reference (can't determine the value statically)
	assert.Empty(t, refs)
}

func TestFindSimilar(t *testing.T) {
	tests := []struct {
		target     string
		candidates []string
		want       string
	}{
		{
			target:     "position_keeping.initiate",
			candidates: []string{"position_keeping.initiate_log", "position_keeping.update_log"},
			want:       "position_keeping.initiate_log",
		},
		{
			target:     "positionkeeping.initiate_log",
			candidates: []string{"position_keeping.initiate_log", "financial_accounting.post_entries"},
			want:       "position_keeping.initiate_log",
		},
		{
			target:     "completely_different",
			candidates: []string{"position_keeping.initiate_log"},
			want:       "", // No good match
		},
		{
			target:     "usd",
			candidates: []string{"USD", "EUR", "GBP"},
			want:       "USD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			got := findSimilar(tt.target, tt.candidates)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestListHandlers(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	_ = registry.Register("z_handler", nil)
	_ = registry.Register("a_handler", nil)
	_ = registry.Register("m_handler", nil)

	v := NewReferenceValidator(registry, nil, nil, nil)

	handlers := v.ListHandlers()

	// Should be sorted alphabetically
	assert.Equal(t, []string{"a_handler", "m_handler", "z_handler"}, handlers)
}

func TestExtractReferences_StepHandlerWithParams(t *testing.T) {
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def execute(ctx):
    step(
        action = "position_keeping.initiate_log",
        params = {
            "position_id": ctx.position_id,
            "amount": ctx.amount,
            "direction": "DEBIT",
        }
    )
`

	refs, err := v.ExtractReferences(script)
	require.NoError(t, err)
	require.Len(t, refs, 1)

	ref := refs[0]
	assert.Equal(t, ReferenceTypeStepHandler, ref.Type)
	assert.Equal(t, "position_keeping.initiate_log", ref.Key)
	assert.True(t, ref.ParamsKnown, "ParamsKnown should be true for literal dict")
	assert.NotNil(t, ref.Params)
	assert.True(t, ref.Params["position_id"])
	assert.True(t, ref.Params["amount"])
	assert.True(t, ref.Params["direction"])
}

func TestValidateDraft_WithSchemaRegistry(t *testing.T) {
	// Register the handler in DomainHandlerRegistry
	handlerRegistry := pkgsaga.NewHandlerRegistry()
	_ = handlerRegistry.Register("test.handler", nil)

	// Create schema registry with handler schema
	schemaRegistry := schema.NewRegistry()
	schemaYAML := `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test handler"
    compensation_strategy: none
    params:
      required_field:
        type: string
        required: true
      optional_field:
        type: string
        required: false
`
	require.NoError(t, schemaRegistry.LoadFromYAML([]byte(schemaYAML)))

	v := NewReferenceValidator(handlerRegistry, nil, nil, nil)
	v.SetSchemaRegistry(schemaRegistry)

	tests := []struct {
		name       string
		script     string
		wantErrors int
		checkMsg   string
	}{
		{
			name: "valid params - all required present",
			script: `
def execute(ctx):
    step(
        action = "test.handler",
        params = {
            "required_field": "value",
        }
    )
`,
			wantErrors: 0,
		},
		{
			name: "missing required param",
			script: `
def execute(ctx):
    step(
        action = "test.handler",
        params = {
            "optional_field": "value",
        }
    )
`,
			wantErrors: 1,
			checkMsg:   "missing required parameter 'required_field'",
		},
		{
			name: "unknown param (warning)",
			script: `
def execute(ctx):
    step(
        action = "test.handler",
        params = {
            "required_field": "value",
            "unknown_field": "value",
        }
    )
`,
			wantErrors: 1,
			checkMsg:   "unknown parameter 'unknown_field'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sagaID := uuid.New()
			result, err := v.ValidateDraft(context.Background(), sagaID, tt.script)
			require.NoError(t, err)

			assert.Len(t, result.Errors, tt.wantErrors)
			if tt.checkMsg != "" && len(result.Errors) > 0 {
				assert.Contains(t, result.Errors[0].Message, tt.checkMsg)
			}
		})
	}
}

func TestValidateActivation_BlocksOnMissingRequiredParams(t *testing.T) {
	// Register the handler
	handlerRegistry := pkgsaga.NewHandlerRegistry()
	_ = handlerRegistry.Register("test.handler", nil)

	// Create schema registry
	schemaRegistry := schema.NewRegistry()
	schemaYAML := `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test handler"
    compensation_strategy: none
    params:
      required_field:
        type: string
        required: true
`
	require.NoError(t, schemaRegistry.LoadFromYAML([]byte(schemaYAML)))

	v := NewReferenceValidator(handlerRegistry, nil, nil, nil)
	v.SetSchemaRegistry(schemaRegistry)

	script := `
def execute(ctx):
    step(
        action = "test.handler",
        params = {}
    )
`

	sagaID := uuid.New()
	result, err := v.ValidateActivation(context.Background(), sagaID, script)
	require.NoError(t, err)

	// Activation should be blocked
	assert.Equal(t, statusBlocked, result.Status)
	assert.Len(t, result.Errors, 1)
	assert.True(t, result.Errors[0].IsCritical)
	assert.Contains(t, result.Errors[0].Message, "missing required parameter")
}

func TestValidateDraft_NoSchemaRegistry(t *testing.T) {
	// When no schema registry is set, validation should still work
	// but skip parameter validation
	handlerRegistry := pkgsaga.NewHandlerRegistry()
	_ = handlerRegistry.Register("test.handler", nil)

	v := NewReferenceValidator(handlerRegistry, nil, nil, nil)
	// Note: NOT setting schema registry

	script := `
def execute(ctx):
    step(
        action = "test.handler",
        params = {
            "any_field": "value",
        }
    )
`

	sagaID := uuid.New()
	result, err := v.ValidateDraft(context.Background(), sagaID, script)
	require.NoError(t, err)

	// Should pass with no errors (no schema to validate against)
	assert.Empty(t, result.Errors)
	assert.Equal(t, statusReady, result.Status)
}

func TestExtractReferences_StepHandlerWithVariableParams(t *testing.T) {
	// When params is a variable, we can't statically extract it
	registry := pkgsaga.NewHandlerRegistry()
	v := NewReferenceValidator(registry, nil, nil, nil)

	script := `
def execute(ctx):
    my_params = {"position_id": ctx.position_id}
    step(
        action = "position_keeping.initiate_log",
        params = my_params  # Variable, not a literal dict
    )
`

	refs, err := v.ExtractReferences(script)
	require.NoError(t, err)
	require.Len(t, refs, 1)

	ref := refs[0]
	assert.Equal(t, ReferenceTypeStepHandler, ref.Type)
	assert.Equal(t, "position_keeping.initiate_log", ref.Key)
	assert.False(t, ref.ParamsKnown, "ParamsKnown should be false for variable params")
	assert.Empty(t, ref.Params)
}

func TestValidateDraft_SkipsValidationForVariableParams(t *testing.T) {
	// When params is a variable, we should skip validation (no false positives)
	handlerRegistry := pkgsaga.NewHandlerRegistry()
	_ = handlerRegistry.Register("test.handler", nil)

	schemaRegistry := schema.NewRegistry()
	schemaYAML := `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test handler"
    compensation_strategy: none
    params:
      required_field:
        type: string
        required: true
`
	require.NoError(t, schemaRegistry.LoadFromYAML([]byte(schemaYAML)))

	v := NewReferenceValidator(handlerRegistry, nil, nil, nil)
	v.SetSchemaRegistry(schemaRegistry)

	script := `
def execute(ctx):
    params_dict = {"required_field": "value"}
    step(
        action = "test.handler",
        params = params_dict  # Variable - can't validate statically
    )
`

	sagaID := uuid.New()
	result, err := v.ValidateDraft(context.Background(), sagaID, script)
	require.NoError(t, err)

	// Should pass - we skip validation for non-static params
	assert.Empty(t, result.Errors)
	assert.Equal(t, statusReady, result.Status)
}
