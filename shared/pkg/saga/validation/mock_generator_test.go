package validation

import (
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSchemaYAML provides a self-contained schema for mock generator tests.
// This replaces the deleted handlers.yaml with the specific handlers used by these tests.
var testSchemaYAML = []byte(`
service: test
version: "1.0"
handlers:
  position_keeping.initiate_log:
    description: "Initiate a position log entry"
    compensation_strategy: auto
    compensate: position_keeping.cancel_log
    params:
      position_id:
        type: string
        required: true
      amount:
        type: Decimal
        required: true
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
    returns:
      log_id:
        type: string
      position_id:
        type: string
      amount:
        type: Decimal
      direction:
        type: enum
        values: [DEBIT, CREDIT]
      status:
        type: string
  position_keeping.cancel_log:
    description: "Cancel a position log entry"
    compensation_strategy: none
    params:
      log_id:
        type: string
        required: true
    returns:
      status:
        type: string
  financial_accounting.post_entries:
    description: "Post accounting entries"
    compensation_strategy: auto
    compensate: financial_accounting.reverse_entries
    params:
      entries:
        type: array
        required: true
    returns:
      posting_ids:
        type: array
      status:
        type: string
  financial_accounting.reverse_entries:
    description: "Reverse accounting entries"
    compensation_strategy: none
    params:
      posting_ids:
        type: array
        required: true
    returns:
      status:
        type: string
  repository.save:
    description: "Save an entity to the repository"
    compensation_strategy: none
    params:
      entity_type:
        type: string
        required: true
      entity:
        type: map
        required: true
    returns:
      entity:
        type: map
      status:
        type: string
  payment_order.create_lien:
    description: "Create a payment order lien"
    compensation_strategy: auto
    compensate: payment_order.terminate_lien
    params:
      account_id:
        type: string
        required: true
      amount_cents:
        type: int64
        required: true
      currency:
        type: string
        required: true
      payment_order_id:
        type: string
        required: true
    returns:
      lien_id:
        type: string
      bucket_id:
        type: string
      status:
        type: string
  payment_order.terminate_lien:
    description: "Terminate a payment order lien"
    compensation_strategy: none
    params:
      lien_id:
        type: string
        required: true
    returns:
      status:
        type: string
  current_account.create_lien:
    description: "Create a current account lien"
    compensation_strategy: auto
    compensate: current_account.terminate_lien
    params:
      account_id:
        type: string
        required: true
      amount:
        type: Decimal
        required: true
    returns:
      lien_id:
        type: string
      status:
        type: string
  current_account.terminate_lien:
    description: "Terminate a current account lien"
    compensation_strategy: none
    params:
      lien_id:
        type: string
        required: true
    returns:
      status:
        type: string
`)

func testRegistry(t *testing.T) *schema.Registry {
	t.Helper()
	reg := schema.NewRegistry()
	err := reg.LoadFromYAML(testSchemaYAML)
	require.NoError(t, err, "Failed to load test schema")
	return reg
}

// TestParseHandlerSchemas verifies parsing of handler schemas using schema.Registry
func TestParseHandlerSchemas(t *testing.T) {
	registry := testRegistry(t)

	// Verify known handlers exist
	handler, err := registry.GetHandler("position_keeping.initiate_log")
	require.NoError(t, err, "Expected position_keeping.initiate_log to be registered")
	require.NotNil(t, handler)

	// Verify param types
	assert.Equal(t, schema.TypeString, handler.Params["position_id"].Type)
	assert.Equal(t, schema.TypeDecimal, handler.Params["amount"].Type)
	assert.Equal(t, schema.TypeEnum, handler.Params["direction"].Type)
	assert.Equal(t, []string{"DEBIT", "CREDIT"}, handler.Params["direction"].Values)

	// Verify return types
	assert.Equal(t, schema.TypeString, handler.Returns["log_id"].Type)
	assert.Equal(t, schema.TypeDecimal, handler.Returns["amount"].Type)
}

// TestGenerateMockForSimpleHandler verifies basic mock generation
func TestGenerateMockForSimpleHandler(t *testing.T) {
	registry := testRegistry(t)

	handler, err := registry.GetHandler("position_keeping.initiate_log")
	require.NoError(t, err)

	// Generate mock handler
	mockHandler := GenerateMockHandler(handler)
	require.NotNil(t, mockHandler)

	// Execute mock with test params
	ctx := &saga.StarlarkContext{}
	params := map[string]any{
		"position_id": "pos_123",
		"amount":      decimal.NewFromFloat(100.50),
		"direction":   "DEBIT",
	}

	result, err := mockHandler(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok, "Expected result to be map[string]any, got %T", result)

	// Verify all return fields present
	assert.Contains(t, resultMap, "log_id")
	assert.Contains(t, resultMap, "position_id")
	assert.Contains(t, resultMap, "amount")
	assert.Contains(t, resultMap, "direction")
	assert.Contains(t, resultMap, "status")

	// Verify echoed fields
	assert.Equal(t, "pos_123", resultMap["position_id"])
	assert.Equal(t, decimal.NewFromFloat(100.50), resultMap["amount"])
	assert.Equal(t, "DEBIT", resultMap["direction"])

	// Verify generated fields
	assert.NotEmpty(t, resultMap["log_id"])
	assert.NotEmpty(t, resultMap["status"])
}

// TestDeterministicOutput verifies mocks return consistent results
func TestDeterministicOutput(t *testing.T) {
	registry := testRegistry(t)

	handler, err := registry.GetHandler("position_keeping.initiate_log")
	require.NoError(t, err)

	mockHandler := GenerateMockHandler(handler)

	ctx := &saga.StarlarkContext{}
	params := map[string]any{
		"position_id": "pos_123",
		"amount":      decimal.NewFromFloat(100.50),
		"direction":   "CREDIT",
	}

	// Call twice with same params
	result1, err1 := mockHandler(ctx, params)
	require.NoError(t, err1)

	result2, err2 := mockHandler(ctx, params)
	require.NoError(t, err2)

	// Results should be identical (deterministic)
	assert.Equal(t, result1, result2, "Mock should be deterministic")
}

// TestGenerateMockForEnumField verifies enum fields use first valid value
func TestGenerateMockForEnumField(t *testing.T) {
	registry := testRegistry(t)

	handler, err := registry.GetHandler("position_keeping.initiate_log")
	require.NoError(t, err)

	mockHandler := GenerateMockHandler(handler)

	ctx := &saga.StarlarkContext{}
	params := map[string]any{
		"position_id": "pos_123",
		"amount":      decimal.NewFromFloat(50.00),
		"direction":   "DEBIT", // Enum input
	}

	result, err := mockHandler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok, "Expected result to be map[string]any, got %T", result)
	// Direction should be echoed
	assert.Equal(t, "DEBIT", resultMap["direction"])
}

// TestGenerateMockForArrayField verifies array fields return empty arrays
func TestGenerateMockForArrayField(t *testing.T) {
	registry := testRegistry(t)

	handler, err := registry.GetHandler("financial_accounting.post_entries")
	require.NoError(t, err)

	mockHandler := GenerateMockHandler(handler)

	ctx := &saga.StarlarkContext{}
	params := map[string]any{
		"entries": []any{},
	}

	result, err := mockHandler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok, "Expected result to be map[string]any, got %T", result)

	// Verify posting_ids is an array (empty for mocks)
	postingIDs, ok := resultMap["posting_ids"].([]any)
	assert.True(t, ok, "posting_ids should be array")
	assert.NotNil(t, postingIDs, "posting_ids should not be nil")
}

// TestGenerateMockForMapField verifies map fields return empty maps
func TestGenerateMockForMapField(t *testing.T) {
	registry := testRegistry(t)

	handler, err := registry.GetHandler("repository.save")
	require.NoError(t, err)

	mockHandler := GenerateMockHandler(handler)

	ctx := &saga.StarlarkContext{}
	params := map[string]any{
		"entity_type": "Account",
		"entity":      map[string]any{"id": "123"},
	}

	result, err := mockHandler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok, "Expected result to be map[string]any, got %T", result)

	// Verify entity is echoed as map
	entity, ok := resultMap["entity"].(map[string]any)
	assert.True(t, ok, "entity should be map")
	assert.Equal(t, "123", entity["id"])
}

// TestGenerateMockForHandlerWithInt64Params verifies mock handles int64 params
func TestGenerateMockForHandlerWithInt64Params(t *testing.T) {
	registry := testRegistry(t)

	handler, err := registry.GetHandler("payment_order.create_lien")
	require.NoError(t, err)

	mockHandler := GenerateMockHandler(handler)

	ctx := &saga.StarlarkContext{}
	params := map[string]any{
		"account_id":       "acc_123",
		"amount_cents":     int64(5000),
		"currency":         "GBP",
		"payment_order_id": "po_123",
	}

	result, err := mockHandler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok, "Expected result to be map[string]any, got %T", result)

	// Verify string fields are present
	assert.NotEmpty(t, resultMap["lien_id"])
	assert.NotEmpty(t, resultMap["bucket_id"])
	assert.NotEmpty(t, resultMap["status"])
}

// TestRegisterMockHandlers verifies mock registration in HandlerRegistry
func TestRegisterMockHandlers(t *testing.T) {
	schemaRegistry := testRegistry(t)

	handlerRegistry := saga.NewHandlerRegistry()

	// Register all mocks
	err := RegisterMockHandlers(handlerRegistry, schemaRegistry)
	require.NoError(t, err, "Should register mocks without error")

	// Verify known handlers are registered
	assert.True(t, handlerRegistry.Has("position_keeping.initiate_log"))
	assert.True(t, handlerRegistry.Has("financial_accounting.post_entries"))
	assert.True(t, handlerRegistry.Has("current_account.create_lien"))

	// Verify handler can be retrieved and executed
	handler, err := handlerRegistry.Get("position_keeping.initiate_log")
	require.NoError(t, err)
	require.NotNil(t, handler)

	ctx := &saga.StarlarkContext{}
	params := map[string]any{
		"position_id": "pos_test",
		"amount":      decimal.NewFromFloat(75.25),
		"direction":   "CREDIT",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

// TestNewMockHandlerRegistry verifies helper function creates isolated registry
func TestNewMockHandlerRegistry(t *testing.T) {
	schemaRegistry := testRegistry(t)

	mockRegistry, err := NewMockHandlerRegistry(schemaRegistry)
	require.NoError(t, err)
	require.NotNil(t, mockRegistry)

	// Verify it's a new isolated instance
	assert.True(t, mockRegistry.Has("position_keeping.initiate_log"))

	// Create another registry to verify isolation
	anotherRegistry := saga.NewHandlerRegistry()
	assert.False(t, anotherRegistry.Has("position_keeping.initiate_log"),
		"New registry should be isolated from mock registry")
}
