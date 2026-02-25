package applier

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockValuationMethod implements ValuationMethodService for testing.
type mockValuationMethod struct {
	resolveMethodFn func(ctx *saga.StarlarkContext, name string) (string, int, []string, error)
}

func (m *mockValuationMethod) ResolveMethod(ctx *saga.StarlarkContext, name string) (string, int, []string, error) {
	if m.resolveMethodFn != nil {
		return m.resolveMethodFn(ctx, name)
	}
	// Default: return a deterministic UUID for any method name
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("test.method."+name))
	return id.String(), 1, nil, nil
}

// mockReferenceData implements ReferenceDataService for testing.
type mockReferenceData struct {
	registerInstrumentFn     func(*saga.StarlarkContext, map[string]any) (any, error)
	deleteInstrumentFn       func(*saga.StarlarkContext, map[string]any) (any, error)
	registerAccountTypeFn    func(*saga.StarlarkContext, map[string]any) (any, error)
	deleteAccountTypeFn      func(*saga.StarlarkContext, map[string]any) (any, error)
	registerValuationRuleFn  func(*saga.StarlarkContext, map[string]any) (any, error)
	registerSagaDefinitionFn func(*saga.StarlarkContext, map[string]any) (any, error)
}

func (m *mockReferenceData) RegisterInstrument(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	if m.registerInstrumentFn != nil {
		return m.registerInstrumentFn(ctx, params)
	}
	return map[string]any{"instrument_code": params["instrument_code"], "status": "REGISTERED"}, nil
}

func (m *mockReferenceData) DeleteInstrument(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	if m.deleteInstrumentFn != nil {
		return m.deleteInstrumentFn(ctx, params)
	}
	return map[string]any{"instrument_code": params["instrument_code"], "status": "DELETED"}, nil
}

func (m *mockReferenceData) RegisterAccountType(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	if m.registerAccountTypeFn != nil {
		return m.registerAccountTypeFn(ctx, params)
	}
	return map[string]any{"code": params["code"], "version": 1, "status": "ACTIVE"}, nil
}

func (m *mockReferenceData) DeleteAccountType(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	if m.deleteAccountTypeFn != nil {
		return m.deleteAccountTypeFn(ctx, params)
	}
	return map[string]any{"code": params["code"], "status": "DELETED"}, nil
}

func (m *mockReferenceData) RegisterValuationRule(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	if m.registerValuationRuleFn != nil {
		return m.registerValuationRuleFn(ctx, params)
	}
	return map[string]any{
		"from_instrument": params["from_instrument"],
		"to_instrument":   params["to_instrument"],
		"status":          "REGISTERED",
	}, nil
}

func (m *mockReferenceData) RegisterSagaDefinition(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	if m.registerSagaDefinitionFn != nil {
		return m.registerSagaDefinitionFn(ctx, params)
	}
	return map[string]any{"saga_name": params["saga_name"], "status": "REGISTERED"}, nil
}

// mockInternalAccount implements InternalAccountService for testing.
type mockInternalAccount struct {
	initiateAccountFn func(*saga.StarlarkContext, map[string]any) (any, error)
}

func (m *mockInternalAccount) InitiateAccount(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	if m.initiateAccountFn != nil {
		return m.initiateAccountFn(ctx, params)
	}
	return map[string]any{
		"account_id":      uuid.New().String(),
		"account_code":    params["account_code"],
		"name":            params["name"],
		"account_type":    params["account_type"],
		"status":          "ACTIVE",
		"instrument_code": params["instrument_code"],
	}, nil
}

func newTestStarlarkContext() *saga.StarlarkContext {
	return &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		KnowledgeAt:     time.Now(),
	}
}

func TestRegisterManifestHandlers(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData:   &mockReferenceData{},
		InternalAccount: &mockInternalAccount{},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	expectedHandlers := []string{
		"reference_data.register_instrument",
		"reference_data.delete_instrument",
		"reference_data.register_account_type",
		"reference_data.delete_account_type",
		"reference_data.register_valuation_rule",
		"reference_data.register_saga_definition",
		"internal_account.initiate",
	}

	for _, name := range expectedHandlers {
		handler, err := registry.Get(name)
		assert.NoError(t, err, "handler %s should be registered", name)
		assert.NotNil(t, handler, "handler %s should not be nil", name)
	}
}

func TestRegisterInstrumentHandler(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData:   &mockReferenceData{},
		InternalAccount: &mockInternalAccount{},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	handler, err := registry.Get("reference_data.register_instrument")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"instrument_code": "USD",
		"display_name":    "US Dollar",
		"dimension":       "CURRENCY",
		"decimal_places":  int64(2),
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "USD", resultMap["instrument_code"])
	assert.Equal(t, "REGISTERED", resultMap["status"])
}

func TestRegisterInstrumentHandler_Error(t *testing.T) {
	expectedErr := errors.New("registration failed")
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData: &mockReferenceData{
			registerInstrumentFn: func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
				return nil, expectedErr
			},
		},
		InternalAccount: &mockInternalAccount{},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	handler, err := registry.Get("reference_data.register_instrument")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{"instrument_code": "FAIL"})
	assert.ErrorIs(t, err, expectedErr)
}

func TestInitiateAccountHandler(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData:   &mockReferenceData{},
		InternalAccount: &mockInternalAccount{},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	handler, err := registry.Get("internal_account.initiate")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"account_code":    "CLEARING_USD",
		"name":            "USD Clearing Account",
		"account_type":    "CLEARING",
		"instrument_code": "USD",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "CLEARING_USD", resultMap["account_code"])
	assert.Equal(t, "ACTIVE", resultMap["status"])
	assert.NotEmpty(t, resultMap["account_id"])
}

func TestRegisterValuationRuleHandler(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData:   &mockReferenceData{},
		InternalAccount: &mockInternalAccount{},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	handler, err := registry.Get("reference_data.register_valuation_rule")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"from_instrument": "KWH",
		"to_instrument":   "GBP",
		"rule_type":       "FIXED_RATE",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "KWH", resultMap["from_instrument"])
	assert.Equal(t, "GBP", resultMap["to_instrument"])
	assert.Equal(t, "REGISTERED", resultMap["status"])
}

func TestRegisterSagaDefinitionHandler(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData:   &mockReferenceData{},
		InternalAccount: &mockInternalAccount{},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	handler, err := registry.Get("reference_data.register_saga_definition")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"saga_name":    "current_account_deposit",
		"display_name": "Current Account Deposit",
		"version":      "1.0.0",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "current_account_deposit", resultMap["saga_name"])
	assert.Equal(t, "REGISTERED", resultMap["status"])
}

func TestCompensationHandlers(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData:   &mockReferenceData{},
		InternalAccount: &mockInternalAccount{},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	ctx := newTestStarlarkContext()

	t.Run("delete_instrument", func(t *testing.T) {
		handler, err := registry.Get("reference_data.delete_instrument")
		require.NoError(t, err)

		result, err := handler(ctx, map[string]any{"instrument_code": "USD"})
		require.NoError(t, err)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "DELETED", resultMap["status"])
	})

	t.Run("delete_account_type", func(t *testing.T) {
		handler, err := registry.Get("reference_data.delete_account_type")
		require.NoError(t, err)

		result, err := handler(ctx, map[string]any{"code": "CLEARING"})
		require.NoError(t, err)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "DELETED", resultMap["status"])
	})
}

// TestAccountTypeHandler_FullParams verifies handler accepts full AccountTypeDefinition params.
func TestAccountTypeHandler_FullParams(t *testing.T) {
	var capturedParams map[string]any
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData: &mockReferenceData{
			registerAccountTypeFn: func(_ *saga.StarlarkContext, p map[string]any) (any, error) {
				capturedParams = p
				return map[string]any{"code": p["code"], "version": 1, "status": "ACTIVE"}, nil
			},
		},
		InternalAccount: &mockInternalAccount{},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	handler, err := registry.Get("reference_data.register_account_type")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"code":            "CUSTOMER_CURRENT",
		"display_name":    "Current Account",
		"behavior_class":  "CUSTOMER",
		"normal_balance":  "CREDIT",
		"instrument_code": "GBP",
		"description":     "Standard customer current account",
		"validation_cel":  "amount > 0",
		"eligibility_cel": "balance >= 0",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "CUSTOMER_CURRENT", resultMap["code"])
	assert.Equal(t, "ACTIVE", resultMap["status"])

	// Verify all params were forwarded to reference data service
	assert.Equal(t, "CUSTOMER_CURRENT", capturedParams["code"])
	assert.Equal(t, "CUSTOMER", capturedParams["behavior_class"])
	assert.Equal(t, "CREDIT", capturedParams["normal_balance"])
	assert.Equal(t, "amount > 0", capturedParams["validation_cel"])
	assert.Equal(t, "balance >= 0", capturedParams["eligibility_cel"])
}

// TestAccountTypeHandler_IdempotentOnError verifies that reference data errors are propagated.
func TestAccountTypeHandler_IdempotentOnError(t *testing.T) {
	expectedErr := errors.New("reference data unavailable")
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData: &mockReferenceData{
			registerAccountTypeFn: func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
				return nil, expectedErr
			},
		},
		InternalAccount: &mockInternalAccount{},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	handler, err := registry.Get("reference_data.register_account_type")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"code":            "CLEARING",
		"behavior_class":  "CLEARING",
		"normal_balance":  "DEBIT",
		"instrument_code": "GBP",
	})
	assert.ErrorIs(t, err, expectedErr)
}

// TestAccountTypeHandler_ResolvesDefaultConversionMethod verifies that a named method is resolved
// to a UUID+version before calling the reference data service.
func TestAccountTypeHandler_ResolvesDefaultConversionMethod(t *testing.T) {
	resolvedID := uuid.New().String()
	var capturedParams map[string]any

	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData: &mockReferenceData{
			registerAccountTypeFn: func(_ *saga.StarlarkContext, p map[string]any) (any, error) {
				capturedParams = p
				return map[string]any{"code": p["code"], "version": 1, "status": "ACTIVE"}, nil
			},
		},
		InternalAccount: &mockInternalAccount{},
		ValuationMethod: &mockValuationMethod{
			resolveMethodFn: func(_ *saga.StarlarkContext, name string) (string, int, []string, error) {
				if name == "forex-spot-v1" {
					return resolvedID, 3, nil, nil
				}
				return "", 0, nil, errors.New("not found")
			},
		},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	handler, err := registry.Get("reference_data.register_account_type")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"code":                      "FX_ACCOUNT",
		"display_name":              "FX Trading Account",
		"behavior_class":            "CUSTOMER",
		"normal_balance":            "DEBIT",
		"instrument_code":           "USD",
		"default_conversion_method": "forex-spot-v1",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "FX_ACCOUNT", resultMap["code"])

	// Verify the method name was replaced with UUID + version
	assert.Equal(t, resolvedID, capturedParams["default_conversion_method_id"])
	assert.Equal(t, 3, capturedParams["default_conversion_method_version"])
	_, hasOldKey := capturedParams["default_conversion_method"]
	assert.False(t, hasOldKey, "original string key should be removed from params")
}

// TestAccountTypeHandler_UnresolvableConversionMethod verifies structured error with suggestions.
func TestAccountTypeHandler_UnresolvableConversionMethod(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData:   &mockReferenceData{},
		InternalAccount: &mockInternalAccount{},
		ValuationMethod: &mockValuationMethod{
			resolveMethodFn: func(_ *saga.StarlarkContext, _ string) (string, int, []string, error) {
				// Return suggestions for typo
				return "", 0, []string{"forex-spot-v1", "forex-spot-v2"}, errors.New("method not found")
			},
		},
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	handler, err := registry.Get("reference_data.register_account_type")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"code":                      "FX_ACCOUNT",
		"behavior_class":            "CUSTOMER",
		"normal_balance":            "DEBIT",
		"instrument_code":           "USD",
		"default_conversion_method": "forex-spt-v1", // typo
	}

	_, err = handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forex-spt-v1")
	assert.Contains(t, err.Error(), "forex-spot-v1")
}

// TestAccountTypeHandler_ConversionMethodWithoutService verifies error when service is nil.
func TestAccountTypeHandler_ConversionMethodWithoutService(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	deps := &HandlerDependencies{
		ReferenceData:   &mockReferenceData{},
		InternalAccount: &mockInternalAccount{},
		ValuationMethod: nil, // no valuation method service
	}

	err := RegisterManifestHandlers(registry, deps)
	require.NoError(t, err)

	handler, err := registry.Get("reference_data.register_account_type")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"code":                      "FX_ACCOUNT",
		"behavior_class":            "CUSTOMER",
		"normal_balance":            "DEBIT",
		"instrument_code":           "USD",
		"default_conversion_method": "forex-spot-v1",
	}

	_, err = handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no ValuationMethodService configured")
}
