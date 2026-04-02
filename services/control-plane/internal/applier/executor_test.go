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

	marketSources, ok := sagaInput["market_data_sources"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, marketSources)

	marketSets, ok := sagaInput["market_data_sets"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, marketSets)

	orgs, ok := sagaInput["organizations"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, orgs)

	internalAccts, ok := sagaInput["internal_accounts"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, internalAccts)
}

func TestBuildSagaInput_NewResourceTypes(t *testing.T) {
	executor := &ManifestExecutor{}

	input := &ApplyManifestInput{
		ManifestVersion: "2",
		TenantID:        "org_test",
		MarketDataSources: []MarketDataSourceInput{
			{
				Code:        "BLOOMBERG",
				Name:        "Bloomberg Financial Data",
				Description: "FX and commodity data",
				TrustLevel:  90,
			},
		},
		MarketDataSets: []MarketDataSetInput{
			{
				Code:                    "USD_EUR_FX",
				Category:                "DATA_CATEGORY_FX_RATE",
				Unit:                    "USD/EUR",
				SourceCode:              "BLOOMBERG",
				DisplayName:             "USD/EUR Spot Rate",
				Description:             "Spot FX rate",
				ValidationExpression:    "value > 0",
				ResolutionKeyExpression: "observed_at",
			},
		},
		Organizations: []OrganizationInput{
			{
				Code:                  "ACME_ENERGY",
				Name:                  "Acme Energy Corp",
				LegalName:             "Acme Energy Corporation",
				DisplayName:           "Acme Energy",
				ExternalReference:     "LEI-ACME-001",
				ExternalReferenceType: "LEI",
				PartyType:             "ORGANIZATION",
				Attributes:            map[string]string{"industry": "energy"},
			},
		},
		InternalAccounts: []InternalAccountInput{
			{
				Code:              "REVENUE_GBP",
				AccountType:       "REVENUE",
				InstrumentCode:    "GBP",
				OwnerOrganization: "ACME_ENERGY",
				Description:       "Revenue account",
			},
		},
	}

	sagaInput := executor.buildSagaInput(input)

	sources, ok := sagaInput["market_data_sources"].([]interface{})
	require.True(t, ok)
	require.Len(t, sources, 1)
	firstSrc, ok := sources[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "BLOOMBERG", firstSrc["code"])
	assert.Equal(t, 90, firstSrc["trust_level"])

	datasets, ok := sagaInput["market_data_sets"].([]interface{})
	require.True(t, ok)
	require.Len(t, datasets, 1)
	firstDS, ok := datasets[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "USD_EUR_FX", firstDS["code"])
	assert.Equal(t, "BLOOMBERG", firstDS["source_code"])
	assert.Equal(t, "value > 0", firstDS["validation_expression"])
	assert.Equal(t, "observed_at", firstDS["resolution_key_expression"])

	orgs, ok := sagaInput["organizations"].([]interface{})
	require.True(t, ok)
	require.Len(t, orgs, 1)
	firstOrg, ok := orgs[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "ACME_ENERGY", firstOrg["code"])
	assert.Equal(t, "Acme Energy Corporation", firstOrg["legal_name"])
	assert.Equal(t, "Acme Energy", firstOrg["display_name"])
	assert.Equal(t, "LEI-ACME-001", firstOrg["external_reference"])
	assert.Equal(t, "LEI", firstOrg["external_reference_type"])
	attrs, ok := firstOrg["attributes"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "energy", attrs["industry"])

	ias, ok := sagaInput["internal_accounts"].([]interface{})
	require.True(t, ok)
	require.Len(t, ias, 1)
	firstIA, ok := ias[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "REVENUE_GBP", firstIA["code"])
	assert.Equal(t, "ACME_ENERGY", firstIA["owner_organization"])
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

func TestBuildSagaInput_ProviderConnectionsAndRoutes(t *testing.T) {
	executor := &ManifestExecutor{}

	input := &ApplyManifestInput{
		ManifestVersion: "3",
		TenantID:        "org_test",
		ProviderConnections: []ProviderConnectionInput{
			{
				ConnectionID:    "stripe-conn",
				ProviderName:    "Stripe",
				ProviderType:    "payment_gateway",
				Protocol:        "HTTPS",
				BaseURL:         "https://api.stripe.com",
				AuthType:        "api_key",
				AuthConfig:      map[string]any{"header_name": "Authorization"},
				RetryPolicy:     map[string]any{"max_attempts": 3},
				RateLimitConfig: map[string]any{"requests_per_second": 100.0},
			},
		},
		InstructionRoutes: []InstructionRouteInput{
			{
				InstructionType:      "payment.initiate",
				ConnectionID:         "stripe-conn",
				FallbackConnectionID: "backup-conn",
				OutboundMapping:      "stripe-outbound",
				InboundMapping:       "stripe-inbound",
				HTTPMethod:           "POST",
				PathTemplate:         "/v1/payment_intents",
			},
		},
	}

	sagaInput := executor.buildSagaInput(input)

	conns, ok := sagaInput["provider_connections"].([]interface{})
	require.True(t, ok)
	require.Len(t, conns, 1)
	firstConn, ok := conns[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "stripe-conn", firstConn["connection_id"])
	assert.Equal(t, "Stripe", firstConn["provider_name"])
	assert.Equal(t, "payment_gateway", firstConn["provider_type"])
	assert.Equal(t, "HTTPS", firstConn["protocol"])
	assert.Equal(t, "https://api.stripe.com", firstConn["base_url"])
	assert.Equal(t, "api_key", firstConn["auth_type"])
	authCfg, ok := firstConn["auth_config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Authorization", authCfg["header_name"])

	routes, ok := sagaInput["instruction_routes"].([]interface{})
	require.True(t, ok)
	require.Len(t, routes, 1)
	firstRoute, ok := routes[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "payment.initiate", firstRoute["instruction_type"])
	assert.Equal(t, "stripe-conn", firstRoute["connection_id"])
	assert.Equal(t, "backup-conn", firstRoute["fallback_connection_id"])
	assert.Equal(t, "stripe-outbound", firstRoute["outbound_mapping"])
	assert.Equal(t, "stripe-inbound", firstRoute["inbound_mapping"])
	assert.Equal(t, "POST", firstRoute["http_method"])
	assert.Equal(t, "/v1/payment_intents", firstRoute["path_template"])
}

func TestBuildSagaInput_AccountTypeWithValuationMethods(t *testing.T) {
	executor := &ManifestExecutor{}

	input := &ApplyManifestInput{
		ManifestVersion: "1",
		AccountTypes: []AccountTypeInput{
			{
				Code:                    "SAVINGS_GBP",
				DisplayName:             "GBP Savings",
				Description:             "A savings account",
				BehaviorClass:           "SAVINGS",
				NormalBalance:           "CREDIT",
				InstrumentCode:          "GBP",
				AccountType:             "SAVINGS",
				DefaultSagaPrefix:       "savings",
				DefaultConversionMethod: "SPOT",
				ValidationCEL:           "amount > 0",
				EligibilityCEL:          "true",
				AttributeSchema:         `{"type":"object"}`,
				ValuationMethods: []ValuationMethodInput{
					{InputInstrument: "USD", MethodName: "SPOT_RATE"},
					{InputInstrument: "EUR", MethodName: "WEIGHTED_AVG"},
				},
			},
		},
	}

	sagaInput := executor.buildSagaInput(input)
	ats, ok := sagaInput["account_types"].([]interface{})
	require.True(t, ok)
	require.Len(t, ats, 1)
	at, ok := ats[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "savings", at["default_saga_prefix"])
	assert.Equal(t, "SPOT", at["default_conversion_method"])
	assert.Equal(t, "amount > 0", at["validation_cel"])
	assert.Equal(t, "true", at["eligibility_cel"])
	assert.Equal(t, `{"type":"object"}`, at["attribute_schema"])

	vms, ok := at["valuation_methods"].([]interface{})
	require.True(t, ok)
	require.Len(t, vms, 2)
	vm0, ok := vms[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "USD", vm0["input_instrument"])
	assert.Equal(t, "SPOT_RATE", vm0["method_name"])
}

func TestNewManifestExecutorFromDeps_NilPool(t *testing.T) {
	_, err := NewManifestExecutorFromDeps(ManifestExecutorDepsConfig{
		Pool: nil,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPoolRequired)
}

func TestApplyManifest_NilInput(t *testing.T) {
	executor := &ManifestExecutor{}
	_, err := executor.Apply(context.Background(), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilInput)
}

func TestApplyManifest_MissingTenantID(t *testing.T) {
	executor := &ManifestExecutor{}
	_, err := executor.Apply(context.Background(), &ApplyManifestInput{
		ManifestVersion: "1",
		TenantID:        "",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingTenantID)
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
//  3. Build typed Starlark service modules from proto-derived schema
//  4. Execute the saga with a full manifest input
//  5. Verify all 4 phases execute in order and produce correct results
//
// This test proves that the platform default fallback (ADR-0028) path works:
// a brand-new tenant can apply a manifest using only the embedded saga script.
func TestApplyManifestSaga_ZeroLocalSagaDefinitions(t *testing.T) {
	// Step 1: Load embedded saga script (simulates platform default fallback)
	script, version, err := loadEmbeddedApplyManifest()
	require.NoError(t, err, "embedded apply_manifest saga must be loadable")
	assert.Equal(t, "1.3.0", version)
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
		activateInstrumentFn: func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
			handlerCalls = append(handlerCalls, "activate_instrument:"+params["instrument_code"].(string))
			return map[string]any{
				"instrument_code": params["instrument_code"],
				"status":          "ACTIVE",
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

	// Step 3: Build typed service modules (schema derived from proto metadata on handlers)
	serviceModules, err := schema.BuildServiceModules(registry)
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
	// Phase 10: Instruments (2 register + 2 activate = 4 steps)
	// Phase 20: Account types (1 register)
	// Phase 40: Valuation rules (1 rule)
	// Phase 60: Internal accounts (1 auto-derived from account types)
	// Phase 70: Saga definitions (1 definition)
	expectedCalls := []string{
		// Phase 10
		"register_instrument:USD",
		"activate_instrument:USD",
		"register_instrument:KWH",
		"activate_instrument:KWH",
		// Phase 20
		"register_account_type:CLEARING_USD",
		// Phase 40
		"register_valuation_rule:KWH_GBP",
		// Phase 60 (auto-derived from account types since no explicit internal_accounts)
		"initiate_account:CLEARING_USD",
		// Phase 70
		"register_saga_definition:deposit",
	}
	assert.Equal(t, expectedCalls, handlerCalls, "handlers must be called in phased order")

	// Step 8: Verify step results
	assert.Len(t, output.StepResults, 8, "must have 8 step results (4+1+1+1+1)")
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
