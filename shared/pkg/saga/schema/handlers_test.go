package schema

import (
	_ "embed"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed handlers.yaml
var platformHandlersYAML []byte

func TestPlatformHandlersSchema(t *testing.T) {
	schema, err := Parse(platformHandlersYAML)
	require.NoError(t, err, "platform handlers.yaml should parse without errors")

	assert.Equal(t, "platform", schema.Service)
	assert.Equal(t, "1.0", schema.Version)

	// Expected handlers from handlers.yaml
	expectedHandlers := []string{
		"position_keeping.initiate_log",
		"position_keeping.update_log",
		"position_keeping.cancel_log",
		"financial_accounting.post_entries",
		"financial_accounting.reverse_entries",
		"financial_accounting.create_booking",
		"financial_accounting.initiate_booking_log",
		"financial_accounting.capture_posting",
		"financial_accounting.compensate_posting",
		"financial_accounting.update_booking_log",
		"current_account.create_lien",
		"current_account.execute_lien",
		"current_account.terminate_lien",
		"current_account.save",
		"current_account.control",
		"valuation_engine.valuate",
		"repository.save",
		"notification.send",
		"payment_order.create_lien",
		"payment_order.terminate_lien",
		"payment_order.send_to_gateway",
		"payment_order.post_ledger_entries",
		"payment_order.execute_lien",
		"reconciliation.initiate_run",
		"reconciliation.execute_run",
		"reconciliation.retrieve_run",
		"reconciliation.cancel_run",
		"reconciliation.assert_balance",
		"reconciliation.initiate_dispute",
		"party.get_default_payment_method",
		"operational_gateway.dispatch_instruction",
		"operational_gateway.cancel_instruction",
		"operational_gateway.get_instruction",
		"financial_gateway.dispatch_payment",
		"financial_gateway.cancel_payment",
		"financial_gateway.dispatch_refund",
	}

	assert.Len(t, schema.Handlers, len(expectedHandlers),
		"schema should define all %d platform handlers", len(expectedHandlers))

	for _, name := range expectedHandlers {
		handler, exists := schema.Handlers[name]
		assert.True(t, exists, "handler %q should be defined in schema", name)
		if exists {
			assert.NotEmpty(t, handler.Description, "handler %q should have a description", name)
		}
	}
}

func TestCompensationHandlersExist(t *testing.T) {
	schema, err := Parse(platformHandlersYAML)
	require.NoError(t, err)

	// Check that all compensate references point to existing handlers
	for name, handler := range schema.Handlers {
		if handler.Compensate != "" {
			_, exists := schema.Handlers[handler.Compensate]
			assert.True(t, exists,
				"handler %q references compensate handler %q which does not exist",
				name, handler.Compensate)
		}
	}
}

func TestHandlerParamTypes(t *testing.T) {
	schema, err := Parse(platformHandlersYAML)
	require.NoError(t, err)

	// Validate specific handler param types
	initLog := schema.Handlers["position_keeping.initiate_log"]
	require.NotNil(t, initLog)

	assert.Equal(t, TypeString, initLog.Params["position_id"].Type)
	assert.Equal(t, TypeDecimal, initLog.Params["amount"].Type)
	assert.Equal(t, TypeEnum, initLog.Params["direction"].Type)
	assert.Equal(t, []string{"DEBIT", "CREDIT"}, initLog.Params["direction"].Values)

	// Validate lien handler
	createLien := schema.Handlers["current_account.create_lien"]
	require.NotNil(t, createLien)
	assert.Equal(t, TypeString, createLien.Params["account_id"].Type)
	assert.Equal(t, TypeDecimal, createLien.Params["amount"].Type)

	// Validate array param
	postEntries := schema.Handlers["financial_accounting.post_entries"]
	require.NotNil(t, postEntries)
	assert.Equal(t, TypeArray, postEntries.Params["entries"].Type)
}

func TestRegistryLoadPlatformHandlers(t *testing.T) {
	registry := NewRegistry()
	err := registry.LoadFromYAML(platformHandlersYAML)
	require.NoError(t, err)

	handlers := registry.ListHandlers()
	assert.Len(t, handlers, 36, "registry should contain all 36 platform handlers")

	// Verify we can get each handler
	for _, name := range handlers {
		handler, err := registry.GetHandler(name)
		require.NoError(t, err, "should be able to get handler %q", name)
		assert.NotNil(t, handler)
	}
}
