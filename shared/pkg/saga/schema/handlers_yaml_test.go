package schema

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoregistry"

	// Import all service protos to register them in the global registry
	_ "github.com/meridianhub/meridian/api/proto/meridian/correspondence/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
)

// testdataDir returns the directory containing this test file (same as handlers.yaml).
func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Dir(filename)
}

func TestLoadHandlersYAML(t *testing.T) {
	yamlPath := filepath.Join(testdataDir(), "handlers.yaml")
	data, err := os.ReadFile(yamlPath)
	require.NoError(t, err, "handlers.yaml should exist")

	schema, err := Parse(data)
	require.NoError(t, err, "handlers.yaml should parse without errors")

	assert.Equal(t, "meridian", schema.Service)
	assert.Equal(t, "2.0", schema.Version)
	assert.GreaterOrEqual(t, len(schema.Handlers), 35, "should have at least 35 handlers")
}

func TestHandlersYAML_ProtoResolution(t *testing.T) {
	yamlPath := filepath.Join(testdataDir(), "handlers.yaml")
	data, err := os.ReadFile(yamlPath)
	require.NoError(t, err)

	schema, err := Parse(data)
	require.NoError(t, err)

	err = schema.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.NoError(t, err, "proto resolution should succeed for all handlers")

	// Verify proto-referenced handlers have populated params
	protoRefHandlers := 0
	legacyHandlers := 0

	for name, handler := range schema.Handlers {
		if handler.HasProtoRef() {
			protoRefHandlers++
			// Proto-referenced handlers should have resolved params
			assert.NotNil(t, handler.Params,
				"handler %s: params should be resolved from proto", name)
			assert.NotNil(t, handler.Returns,
				"handler %s: returns should be resolved from proto", name)
		} else {
			legacyHandlers++
		}
	}

	t.Logf("Proto-referenced: %d, Legacy: %d, Total: %d",
		protoRefHandlers, legacyHandlers, protoRefHandlers+legacyHandlers)

	assert.GreaterOrEqual(t, protoRefHandlers, 36,
		"at least 36 handlers should use proto-referenced format")
}

func TestHandlersYAML_AllExpectedHandlersPresent(t *testing.T) {
	yamlPath := filepath.Join(testdataDir(), "handlers.yaml")
	data, err := os.ReadFile(yamlPath)
	require.NoError(t, err)

	schema, err := Parse(data)
	require.NoError(t, err)

	err = schema.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.NoError(t, err)

	expectedHandlers := []string{
		"position_keeping.initiate_log",
		"position_keeping.update_log",
		"position_keeping.cancel_log",
		"current_account.create_lien",
		"current_account.execute_lien",
		"current_account.terminate_lien",
		"current_account.save",
		"current_account.control",
		"financial_accounting.initiate_booking_log",
		"financial_accounting.update_booking_log",
		"financial_accounting.capture_posting",
		"financial_accounting.compensate_posting",
		"financial_accounting.create_booking",
		"financial_accounting.post_entries",
		"financial_accounting.reverse_entries",
		"financial_gateway.dispatch_payment",
		"financial_gateway.cancel_payment",
		"financial_gateway.dispatch_refund",
		"operational_gateway.dispatch_instruction",
		"operational_gateway.cancel_instruction",
		"operational_gateway.get_instruction",
		"reconciliation.initiate_run",
		"reconciliation.execute_run",
		"reconciliation.retrieve_run",
		"reconciliation.cancel_run",
		"reconciliation.assert_balance",
		"reconciliation.initiate_dispute",
		"party.get_default_payment_method",
		"party.list_participants",
		"party.get_structuring_data",
		"internal_account.initiate",
		"internal_account.retrieve",
		"internal_account.get_balance",
		"market_information.get_rate",
		"reference_data.retrieve_instrument",
		"correspondence.initiate_outbound",
		"notification.send",
	}

	for _, name := range expectedHandlers {
		handler, exists := schema.Handlers[name]
		assert.True(t, exists, "handler %q should be in handlers.yaml", name)
		if exists {
			assert.NotEmpty(t, handler.Description,
				"handler %q should have a description", name)
		}
	}
}

func TestHandlersYAML_CompensationCoverage(t *testing.T) {
	yamlPath := filepath.Join(testdataDir(), "handlers.yaml")
	data, err := os.ReadFile(yamlPath)
	require.NoError(t, err)

	schema, err := Parse(data)
	require.NoError(t, err)

	for name, handler := range schema.Handlers {
		t.Run(name, func(t *testing.T) {
			hasCompensate := handler.Compensate != ""
			hasStrategy := handler.CompensationStrategy != ""

			assert.True(t, hasCompensate || hasStrategy,
				"handler %q must declare either 'compensate' or 'compensation_strategy'", name)

			// If compensate is set, verify the target handler exists
			if hasCompensate {
				_, targetExists := schema.Handlers[handler.Compensate]
				assert.True(t, targetExists,
					"handler %q references compensate handler %q which is not in the schema",
					name, handler.Compensate)
			}
		})
	}
}

func TestHandlersYAML_PositionKeepingAlias(t *testing.T) {
	yamlPath := filepath.Join(testdataDir(), "handlers.yaml")
	data, err := os.ReadFile(yamlPath)
	require.NoError(t, err)

	schema, err := Parse(data)
	require.NoError(t, err)

	err = schema.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.NoError(t, err)

	handler := schema.Handlers["position_keeping.initiate_log"]
	require.NotNil(t, handler)

	// account_id should be aliased to position_id
	_, hasAccountID := handler.Params["account_id"]
	assert.False(t, hasAccountID, "account_id should be aliased away")

	_, hasPositionID := handler.Params["position_id"]
	assert.True(t, hasPositionID, "position_id alias should be present")
}

func TestHandlersYAML_ExternalHandlers(t *testing.T) {
	yamlPath := filepath.Join(testdataDir(), "handlers.yaml")
	data, err := os.ReadFile(yamlPath)
	require.NoError(t, err)

	schema, err := Parse(data)
	require.NoError(t, err)

	expectedExternal := []string{
		"financial_gateway.dispatch_payment",
		"financial_gateway.cancel_payment",
		"financial_gateway.dispatch_refund",
		"operational_gateway.dispatch_instruction",
		"operational_gateway.cancel_instruction",
	}

	for _, name := range expectedExternal {
		handler := schema.Handlers[name]
		require.NotNil(t, handler, "handler %s should exist", name)
		assert.True(t, handler.External,
			"handler %s should be marked as external", name)
	}

	// Verify internal handlers are NOT external
	internalHandlers := []string{
		"position_keeping.initiate_log",
		"financial_accounting.capture_posting",
		"reconciliation.initiate_run",
	}

	for _, name := range internalHandlers {
		handler := schema.Handlers[name]
		require.NotNil(t, handler, "handler %s should exist", name)
		assert.False(t, handler.External,
			"handler %s should NOT be marked as external", name)
	}
}

func TestHandlersYAML_CompositePostEntries(t *testing.T) {
	yamlPath := filepath.Join(testdataDir(), "handlers.yaml")
	data, err := os.ReadFile(yamlPath)
	require.NoError(t, err)

	schema, err := Parse(data)
	require.NoError(t, err)

	handler := schema.Handlers["financial_accounting.post_entries"]
	require.NotNil(t, handler)

	// post_entries is a composite handler: no proto_ref, marked composite
	assert.True(t, handler.Composite, "post_entries should be marked as composite")
	assert.True(t, handler.IsComposite(), "IsComposite() should return true")
	assert.Nil(t, handler.ProtoRef, "composite handler should not have proto_ref")
	assert.Equal(t, "financial_accounting.reverse_entries", handler.Compensate)

	// Proto resolution should succeed - composite handlers are skipped
	err = schema.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.NoError(t, err, "proto resolution should skip composite handlers without error")

	// Params should remain empty (intentionally) after resolution
	assert.Empty(t, handler.Params, "composite handler params should remain empty after proto resolution")
}

func TestHandlersYAML_CompositeHandlerValidation(t *testing.T) {
	t.Run("valid composite handler", func(t *testing.T) {
		yamlData := []byte(`
service: test
version: "1.0"
handlers:
  test.composite_op:
    description: "A composite handler"
    compensation_strategy: none
    composite: true
    params: {}
`)
		schema, err := Parse(yamlData)
		require.NoError(t, err, "composite handler with empty params should be valid")

		handler := schema.Handlers["test.composite_op"]
		require.NotNil(t, handler)
		assert.True(t, handler.IsComposite())
		assert.False(t, handler.HasProtoRef())
	})

	t.Run("composite with proto_ref is rejected", func(t *testing.T) {
		yamlData := []byte(`
service: test
version: "1.0"
handlers:
  test.bad_composite:
    description: "Invalid: composite with proto_ref"
    compensation_strategy: none
    composite: true
    proto_ref:
      proto_rpc: "some.Service/Method"
`)
		_, err := Parse(yamlData)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCompositeWithProtoRef)
	})
}

func TestHandlersYAML_SizeReduction(t *testing.T) {
	yamlPath := filepath.Join(testdataDir(), "handlers.yaml")
	data, err := os.ReadFile(yamlPath)
	require.NoError(t, err)

	// The slim format should be significantly smaller than a verbose format
	// with full inline param/return definitions. Target: under 500 lines.
	lineCount := 1
	for _, b := range data {
		if b == '\n' {
			lineCount++
		}
	}
	t.Logf("handlers.yaml: %d lines, %d bytes", lineCount, len(data))

	// Verify under 500 lines (was targeting ~400 in task description)
	assert.LessOrEqual(t, lineCount, 500,
		"slim handlers.yaml should be under 500 lines")
}
