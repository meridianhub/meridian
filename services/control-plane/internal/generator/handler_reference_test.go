package generator

import (
	"strings"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildHandlerReferenceCard_EmptyRegistry(t *testing.T) {
	registry := schema.NewRegistry()
	result := BuildHandlerReferenceCard(registry)

	assert.Contains(t, result, "## Handler Reference Card")
	assert.Contains(t, result, "ctx.<service>.<handler>")
}

func TestBuildHandlerReferenceCard_SingleHandler(t *testing.T) {
	registry := schema.NewRegistry()
	err := registry.LoadFromYAML([]byte(`
service: position_keeping
version: "1.0"
handlers:
  position_keeping.initiate_log:
    description: Initiates a financial position log entry.
    params:
      account_id:
        type: uuid
        required: true
        description: The account identifier.
      amount:
        type: Decimal
        required: true
    returns:
      log_id:
        type: uuid
        description: The created log identifier.
    compensate: position_keeping.void_log
`))
	require.NoError(t, err)

	result := BuildHandlerReferenceCard(registry)

	assert.Contains(t, result, "### position_keeping")
	assert.Contains(t, result, "`position_keeping.initiate_log`")
	assert.Contains(t, result, "Initiates a financial position log entry.")
	assert.Contains(t, result, "`account_id`: uuid *(required)*")
	assert.Contains(t, result, "`amount`: Decimal *(required)*")
	assert.Contains(t, result, "`log_id`: uuid")
	assert.Contains(t, result, "**Compensation:** `position_keeping.void_log`")
}

func TestBuildHandlerReferenceCard_DeprecatedHandler(t *testing.T) {
	registry := schema.NewRegistry()
	err := registry.LoadFromYAML([]byte(`
service: financial_accounting
version: "1.0"
handlers:
  financial_accounting.old_booking:
    description: Old booking handler.
    deprecated: true
    params:
      amount:
        type: Decimal
        required: true
    compensation_strategy: none
`))
	require.NoError(t, err)

	result := BuildHandlerReferenceCard(registry)

	assert.Contains(t, result, "`financial_accounting.old_booking` *(deprecated)*")
}

func TestBuildHandlerReferenceCard_EnumField(t *testing.T) {
	registry := schema.NewRegistry()
	err := registry.LoadFromYAML([]byte(`
service: current_account
version: "1.0"
handlers:
  current_account.update_status:
    params:
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
    compensation_strategy: none
`))
	require.NoError(t, err)

	result := BuildHandlerReferenceCard(registry)

	assert.Contains(t, result, "`direction`: enum(DEBIT|CREDIT) *(required)*")
}

func TestBuildHandlerReferenceCard_MultipleServices(t *testing.T) {
	registry := schema.NewRegistry()
	err := registry.LoadFromYAML([]byte(`
service: position_keeping
version: "1.0"
handlers:
  position_keeping.capture:
    params:
      amount:
        type: Decimal
        required: true
    compensate: position_keeping.reverse_capture
`))
	require.NoError(t, err)

	err = registry.LoadFromYAML([]byte(`
service: current_account
version: "1.0"
handlers:
  current_account.freeze:
    params:
      account_id:
        type: uuid
        required: true
    compensation_strategy: none
`))
	require.NoError(t, err)

	result := BuildHandlerReferenceCard(registry)

	// Both services should appear
	assert.Contains(t, result, "### position_keeping")
	assert.Contains(t, result, "### current_account")

	// current_account should appear before position_keeping (alphabetical order)
	caIdx := strings.Index(result, "### current_account")
	pkIdx := strings.Index(result, "### position_keeping")
	assert.Less(t, caIdx, pkIdx, "services should be sorted alphabetically")
}

func TestBuildHandlerReferenceCard_CompensationStrategySagaManaged(t *testing.T) {
	registry := schema.NewRegistry()
	err := registry.LoadFromYAML([]byte(`
service: payment_order
version: "1.0"
handlers:
  payment_order.execute:
    params:
      order_id:
        type: uuid
        required: true
    compensation_strategy: saga_managed
`))
	require.NoError(t, err)

	result := BuildHandlerReferenceCard(registry)

	assert.Contains(t, result, "**Compensation:** saga-managed")
}

func TestBuildHandlerReferenceCard_ArrayAndMapFields(t *testing.T) {
	registry := schema.NewRegistry()
	err := registry.LoadFromYAML([]byte(`
service: reconciliation
version: "1.0"
handlers:
  reconciliation.batch_check:
    params:
      entry_ids:
        type: array
        item_type: uuid
        required: true
      metadata:
        type: map
        key_type: string
        value_type: string
    compensation_strategy: none
`))
	require.NoError(t, err)

	result := BuildHandlerReferenceCard(registry)

	assert.Contains(t, result, "`entry_ids`: array<uuid>")
	assert.Contains(t, result, "`metadata`: map<string,string>")
}

func TestServicePrefix(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "WithDot", input: "position_keeping.initiate_log", expected: "position_keeping"},
		{name: "NoDot", input: "standalone_handler", expected: "standalone_handler"},
		{name: "MultipleDots", input: "a.b.c", expected: "a"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := servicePrefix(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}
