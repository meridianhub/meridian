package applier

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSagaInput(t *testing.T) {
	executor := &ManifestExecutor{}

	input := &ApplyManifestInput{
		ManifestVersion: "42",
		TenantID:        "org_test",
		Instruments: []InstrumentInput{
			{
				Code:          "USD",
				DisplayName:   "US Dollar",
				Dimension:     "CURRENCY",
				DecimalPlaces: 2,
			},
			{
				Code:          "KWH",
				DisplayName:   "Kilowatt Hour",
				Dimension:     "ENERGY",
				DecimalPlaces: 4,
			},
		},
		AccountTypes: []AccountTypeInput{
			{
				Code:           "CLEARING_USD",
				DisplayName:    "USD Clearing Account",
				InstrumentCode: "USD",
				AccountType:    "CLEARING",
				BehaviorClass:  "CLEARING",
				NormalBalance:  "DEBIT",
			},
		},
		ValuationRules: []ValuationRuleInput{
			{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				RuleType:       "FIXED_RATE",
			},
		},
		SagaDefinitions: []SagaDefinitionInput{
			{
				Name:    "deposit",
				Version: "1.0.0",
			},
		},
	}

	sagaInput := executor.buildSagaInput(input)

	assert.Equal(t, "42", sagaInput["manifest_version"])

	instruments, ok := sagaInput["instruments"].([]interface{})
	require.True(t, ok)
	assert.Len(t, instruments, 2)

	firstInst, ok := instruments[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "USD", firstInst["code"])
	assert.Equal(t, "CURRENCY", firstInst["dimension"])

	accountTypes, ok := sagaInput["account_types"].([]interface{})
	require.True(t, ok)
	assert.Len(t, accountTypes, 1)

	firstAT, ok := accountTypes[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "CLEARING_USD", firstAT["code"])
	assert.Equal(t, "USD", firstAT["instrument_code"])

	rules, ok := sagaInput["valuation_rules"].([]interface{})
	require.True(t, ok)
	assert.Len(t, rules, 1)

	sagaDefs, ok := sagaInput["saga_definitions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, sagaDefs, 1)
}

func TestBuildSagaInput_Empty(t *testing.T) {
	executor := &ManifestExecutor{}

	input := &ApplyManifestInput{
		ManifestVersion: "1",
	}

	sagaInput := executor.buildSagaInput(input)

	assert.Equal(t, "1", sagaInput["manifest_version"])

	instruments, ok := sagaInput["instruments"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, instruments)

	accountTypes, ok := sagaInput["account_types"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, accountTypes)
}

func TestParseManifestVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"1", 1},
		{"42", 42},
		{"100", 100},
		{"1.2.3", 1},
		{"", 1},
		{"abc", 1},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseManifestVersion(tt.input))
		})
	}
}

func TestNewManifestExecutor(t *testing.T) {
	executor := NewManifestExecutor(ManifestExecutorConfig{})
	require.NotNil(t, executor)
	assert.NotNil(t, executor.logger)
}

// TestApplyManifestSaga_ZeroLocalSagaDefinitions is the critical test for subtask 7.7.
// It validates the complete end-to-end path for a tenant with 0 local saga definitions:
//  1. Load the embedded apply_manifest.star from platform defaults
//  2. Register all manifest handlers (reference_data + internal_account)
//  3. Build typed Starlark service modules from handlers.yaml schema
//  4. Execute the saga with a full manifest input
//  5. Verify all 4 phases execute in order and produce correct results
//
// This test proves that the platform default fallback (ADR-0028) path works:
// a brand-new tenant can apply a manifest using only the embedded saga script.
func TestApplyManifestSaga_ZeroLocalSagaDefinitions(t *testing.T) {
	// Step 1: Load embedded saga script (simulates platform default fallback)
	script, version, err := loadEmbeddedApplyManifest()
	require.NoError(t, err, "embedded apply_manifest saga must be loadable")
	assert.Equal(t, "1.2.0", version)
	assert.Contains(t, script, "execute_apply_manifest")

	// Step 2: Set up handler registry with mock handlers
	registry := saga.NewHandlerRegistry()

	// Track which handlers are called and in what order
	var handlerCalls []string
	mockRefData := &mockReferenceData{
		registerInstrumentFn: func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
			handlerCalls = append(handlerCalls, "register_instrument:"+params["instrument_code"].(string))
			return map[string]any{
				"instrument_code": params["instrument_code"],
				"status":          "REGISTERED",
			}, nil
		},
		registerAccountTypeFn: func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
			handlerCalls = append(handlerCalls, "register_account_type:"+params["code"].(string))
			return map[string]any{
				"code":    params["code"],
				"version": int32(1),
				"status":  "REGISTERED",
			}, nil
		},
		registerValuationRuleFn: func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
			handlerCalls = append(handlerCalls, "register_valuation_rule:"+params["from_instrument"].(string)+"_"+params["to_instrument"].(string))
			return map[string]any{
				"from_instrument": params["from_instrument"],
				"to_instrument":   params["to_instrument"],
				"status":          "REGISTERED",
			}, nil
		},
		registerSagaDefinitionFn: func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
			handlerCalls = append(handlerCalls, "register_saga_definition:"+params["saga_name"].(string))
			return map[string]any{
				"saga_name": params["saga_name"],
				"status":    "REGISTERED",
			}, nil
		},
	}
	mockIBA := &mockInternalAccount{
		initiateAccountFn: func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
			handlerCalls = append(handlerCalls, "initiate_account:"+params["account_code"].(string))
			return map[string]any{
				"account_id":      uuid.New().String(),
				"account_code":    params["account_code"],
				"name":            params["name"],
				"account_type":    params["account_type"],
				"status":          "ACTIVE",
				"instrument_code": params["instrument_code"],
			}, nil
		},
	}

	deps := &HandlerDependencies{
		ReferenceData:   mockRefData,
		InternalAccount: mockIBA,
	}
	err = RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	// Step 3: Load handlers.yaml schema and build typed service modules
	handlersYAML, err := embeddedHandlersYAML()
	require.NoError(t, err, "handlers.yaml must be loadable")

	schemaRegistry := schema.NewRegistry()
	err = schemaRegistry.LoadFromYAML(handlersYAML)
	require.NoError(t, err, "handlers.yaml must parse correctly")

	serviceModules, err := schema.BuildServiceModules(registry, schemaRegistry)
	require.NoError(t, err, "service modules must build from schema + registry")

	// Step 4: Create the saga runner
	runtime, err := saga.NewRuntime(nil)
	require.NoError(t, err)

	runner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       registry,
		ServiceModules: serviceModules,
	})
	require.NoError(t, err)

	// Step 5: Build manifest input (simulates a real manifest apply)
	executor := &ManifestExecutor{}
	manifestInput := &ApplyManifestInput{
		ManifestVersion: "1",
		TenantID:        "org_new_tenant",
		Instruments: []InstrumentInput{
			{
				Code:          "USD",
				DisplayName:   "US Dollar",
				Dimension:     "CURRENCY",
				DecimalPlaces: 2,
			},
			{
				Code:          "KWH",
				DisplayName:   "Kilowatt Hour",
				Dimension:     "ENERGY",
				DecimalPlaces: 4,
			},
		},
		AccountTypes: []AccountTypeInput{
			{
				Code:           "CLEARING_USD",
				DisplayName:    "USD Clearing Account",
				InstrumentCode: "USD",
				AccountType:    "CLEARING",
				BehaviorClass:  "CLEARING",
				NormalBalance:  "DEBIT",
			},
		},
		ValuationRules: []ValuationRuleInput{
			{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				RuleType:       "FIXED_RATE",
			},
		},
		SagaDefinitions: []SagaDefinitionInput{
			{
				Name:    "deposit",
				Version: "1.0.0",
			},
		},
	}

	sagaInput := executor.buildSagaInput(manifestInput)

	// Step 6: Execute the saga
	runnerInput := saga.RunnerInput{
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		KnowledgeAt:     time.Now(),
		Input:           sagaInput,
	}

	output, err := runner.ExecuteSaga(context.Background(), "apply_manifest", script, runnerInput)
	require.NoError(t, err, "saga execution must not return error")
	require.NotNil(t, output)
	assert.True(t, output.Success, "saga must succeed; error: %s", output.Error)

	// Step 7: Verify phased execution order
	// Phase 1: Instruments (2 instruments)
	// Phase 2: Account types (1 register + 1 initiate)
	// Phase 3: Valuation rules (1 rule)
	// Phase 4: Saga definitions (1 definition)
	expectedCalls := []string{
		// Phase 1
		"register_instrument:USD",
		"register_instrument:KWH",
		// Phase 2
		"register_account_type:CLEARING_USD",
		"initiate_account:CLEARING_USD",
		// Phase 3
		"register_valuation_rule:KWH_GBP",
		// Phase 4
		"register_saga_definition:deposit",
	}
	assert.Equal(t, expectedCalls, handlerCalls, "handlers must be called in phased order")

	// Step 8: Verify step results
	assert.Len(t, output.StepResults, 6, "must have 6 step results (2+2+1+1)")
	for _, step := range output.StepResults {
		assert.True(t, step.Success, "step %s must succeed", step.StepName)
	}

	// Step 9: Verify compensation metadata is set for compensable steps.
	// StepResult.StepName is the handler qualified name (e.g., "reference_data.register_instrument"),
	// not the Starlark step() name (e.g., "register_instrument_USD"). This is because typed
	// service modules record the handler name in the step result.
	var compensationCount int
	for _, step := range output.StepResults {
		switch step.StepName {
		case "reference_data.register_instrument":
			assert.Equal(t, "reference_data.delete_instrument", step.CompensateHandler,
				"register_instrument must have delete_instrument compensation")
			assert.NotEmpty(t, step.CompensateParams, "compensation params must be derived")
			compensationCount++
		case "reference_data.register_account_type":
			assert.Equal(t, "reference_data.delete_account_type", step.CompensateHandler,
				"register_account_type must have delete_account_type compensation")
			compensationCount++
		}
	}
	assert.Equal(t, 3, compensationCount, "must find compensation metadata for 2 instruments + 1 account type")
}

// embeddedHandlersYAML reads the handlers.yaml from the applier package.
func embeddedHandlersYAML() ([]byte, error) {
	return handlersYAMLFS.ReadFile("handlers.yaml")
}
