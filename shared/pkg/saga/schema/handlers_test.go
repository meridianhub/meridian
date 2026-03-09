package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlatformHandlersSchema(t *testing.T) {
	handlerRegistry := buildFullHandlerRegistry(t)
	derivedSchema, err := DeriveSchema(handlerRegistry)
	require.NoError(t, err, "DeriveSchema should succeed")

	assert.NotEmpty(t, derivedSchema.Handlers, "derived schema should contain handlers")

	// Verify key handlers with proto annotations exist in the derived schema.
	// Only handlers with ProtoRequestType annotations produce derived schema entries.
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
		"internal_account.initiate",
	}

	for _, name := range expectedHandlers {
		handler, exists := derivedSchema.Handlers[name]
		assert.True(t, exists, "handler %q should be in derived schema", name)
		if exists {
			assert.NotEmpty(t, handler.Description, "handler %q should have a description", name)
		}
	}

	t.Logf("Derived schema contains %d handlers", len(derivedSchema.Handlers))
}

func TestCompensationHandlersExist(t *testing.T) {
	handlerRegistry := buildFullHandlerRegistry(t)
	derivedSchema, err := DeriveSchema(handlerRegistry)
	require.NoError(t, err)

	for name, handler := range derivedSchema.Handlers {
		if handler.Compensate != "" {
			_, exists := derivedSchema.Handlers[handler.Compensate]
			assert.True(t, exists,
				"handler %q references compensate handler %q which does not exist",
				name, handler.Compensate)
		}
	}
}

func TestCompensationCoverage(t *testing.T) {
	handlerRegistry := buildFullHandlerRegistry(t)
	derivedSchema, err := DeriveSchema(handlerRegistry)
	require.NoError(t, err)

	for name, handler := range derivedSchema.Handlers {
		t.Run(name, func(t *testing.T) {
			hasCompensate := handler.Compensate != ""
			hasStrategy := handler.CompensationStrategy != ""

			assert.True(t, hasCompensate || hasStrategy,
				"handler %q must declare either 'compensate' or 'compensation_strategy'", name)

			if hasCompensate && hasStrategy {
				assert.Equal(t, CompensationStrategyAuto, handler.CompensationStrategy,
					"handler %q with 'compensate' should only use strategy 'auto'", name)
			}
		})
	}
}

func TestCompensationSchemaValidation_RejectsInvalidStrategy(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test handler"
    compensation_strategy: invalid_value
    params: {}
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid compensation_strategy value")
}

func TestCompensationSchemaValidation_RejectsMissing(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test handler"
    params: {}
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must declare either")
}

func TestCompensationSchemaValidation_RejectsConflict(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test handler"
    compensate: test.undo
    compensation_strategy: none
    params: {}
  test.undo:
    description: "Undo handler"
    compensation_strategy: none
    params: {}
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "should not set")
}

func TestCompensationSchemaValidation_AcceptsCompensateWithAutoStrategy(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test handler"
    compensate: test.undo
    compensation_strategy: auto
    params: {}
  test.undo:
    description: "Undo handler"
    compensation_strategy: none
    params: {}
`
	_, err := Parse([]byte(yaml))
	require.NoError(t, err, "compensate + auto strategy should be valid")
}

func TestHandlerParamTypes(t *testing.T) {
	handlerRegistry := buildFullHandlerRegistry(t)
	derivedSchema, err := DeriveSchema(handlerRegistry)
	require.NoError(t, err)

	// Validate position_keeping.initiate_log param types
	initLog := derivedSchema.Handlers["position_keeping.initiate_log"]
	require.NotNil(t, initLog, "position_keeping.initiate_log should exist")

	assert.Equal(t, TypeString, initLog.Params["position_id"].Type)
	assert.Equal(t, TypeDecimal, initLog.Params["amount"].Type)
	assert.Equal(t, TypeEnum, initLog.Params["direction"].Type)

	// Validate current_account.create_lien param types
	createLien := derivedSchema.Handlers["current_account.create_lien"]
	require.NotNil(t, createLien, "current_account.create_lien should exist")
	assert.Equal(t, TypeString, createLien.Params["account_id"].Type)
	assert.Equal(t, TypeDecimal, createLien.Params["amount"].Type)
}

func TestRegistryFromDerivedSchema(t *testing.T) {
	handlerRegistry := buildFullHandlerRegistry(t)
	derivedSchema, err := DeriveSchema(handlerRegistry)
	require.NoError(t, err)

	// Wrap derived schema in a Registry to verify compatibility
	reg := NewRegistry()
	for name, def := range derivedSchema.Handlers {
		reg.handlers[name] = def
	}

	handlers := reg.ListHandlers()
	assert.NotEmpty(t, handlers, "registry should contain derived handlers")

	// Verify each handler is retrievable
	for _, name := range handlers {
		handler, err := reg.GetHandler(name)
		require.NoError(t, err, "should be able to get handler %q", name)
		assert.NotNil(t, handler)
	}
}
