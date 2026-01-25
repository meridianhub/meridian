package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHandlerSchema(t *testing.T) {
	yaml := `
service: current_account
version: "1.0"
handlers:
  position_keeping.initiate_log:
    description: "Create DEBIT/CREDIT entry in PositionKeeping service"
    params:
      position_id:
        type: string
        required: true
        description: "Unique identifier for the position"
      amount:
        type: Decimal
        required: true
        description: "Transaction amount"
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
        description: "Direction of the transaction"
    returns:
      log_id:
        type: string
        description: "Generated log identifier"
      status:
        type: string
        description: "Status of the log entry"
    compensate: position_keeping.cancel_log
`

	schema, err := Parse([]byte(yaml))
	require.NoError(t, err)
	require.NotNil(t, schema)

	assert.Equal(t, "current_account", schema.Service)
	assert.Equal(t, "1.0", schema.Version)
	assert.Len(t, schema.Handlers, 1)

	handler, exists := schema.Handlers["position_keeping.initiate_log"]
	require.True(t, exists)
	assert.Equal(t, "Create DEBIT/CREDIT entry in PositionKeeping service", handler.Description)
	assert.Equal(t, "position_keeping.cancel_log", handler.Compensate)

	// Check params
	assert.Len(t, handler.Params, 3)

	positionID := handler.Params["position_id"]
	assert.Equal(t, TypeString, positionID.Type)
	assert.True(t, positionID.Required)

	amount := handler.Params["amount"]
	assert.Equal(t, TypeDecimal, amount.Type)
	assert.True(t, amount.Required)

	direction := handler.Params["direction"]
	assert.Equal(t, TypeEnum, direction.Type)
	assert.True(t, direction.Required)
	assert.Equal(t, []string{"DEBIT", "CREDIT"}, direction.Values)

	// Check returns
	assert.Len(t, handler.Returns, 2)
	logID := handler.Returns["log_id"]
	assert.Equal(t, TypeString, logID.Type)
}

func TestParseHandlerSchemaValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing service",
			yaml: `
version: "1.0"
handlers:
  test.handler:
    description: "Test"
`,
			wantErr: "service is required",
		},
		{
			name: "invalid type",
			yaml: `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test"
    params:
      foo:
        type: invalid_type
        required: true
`,
			wantErr: "unknown type",
		},
		{
			name: "enum without values",
			yaml: `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test"
    params:
      direction:
        type: enum
        required: true
`,
			wantErr: "enum type requires values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestFieldTypeValidation(t *testing.T) {
	tests := []struct {
		typeStr string
		want    FieldType
		valid   bool
	}{
		{"string", TypeString, true},
		{"int32", TypeInt32, true},
		{"int64", TypeInt64, true},
		{"uint32", TypeUint32, true},
		{"bool", TypeBool, true},
		{"Decimal", TypeDecimal, true},
		{"enum", TypeEnum, true},
		{"array", TypeArray, true},
		{"map", TypeMap, true},
		{"uuid", TypeUUID, true},
		{"invalid", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.typeStr, func(t *testing.T) {
			ft, err := ParseFieldType(tt.typeStr)
			if tt.valid {
				require.NoError(t, err)
				assert.Equal(t, tt.want, ft)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestSchemaRegistry(t *testing.T) {
	registry := NewRegistry()

	yaml := `
service: test_service
version: "1.0"
handlers:
  test.handler:
    description: "A test handler"
    params:
      id:
        type: string
        required: true
    returns:
      result:
        type: string
`

	err := registry.LoadFromYAML([]byte(yaml))
	require.NoError(t, err)

	// Get handler schema
	handler, err := registry.GetHandler("test.handler")
	require.NoError(t, err)
	assert.Equal(t, "A test handler", handler.Description)

	// List handlers
	handlers := registry.ListHandlers()
	assert.Contains(t, handlers, "test.handler")

	// Non-existent handler
	_, err = registry.GetHandler("nonexistent")
	assert.Error(t, err)
}

func TestLoadMultipleSchemas(t *testing.T) {
	registry := NewRegistry()

	yaml1 := `
service: service_a
version: "1.0"
handlers:
  service_a.method1:
    description: "Method 1"
`

	yaml2 := `
service: service_b
version: "1.0"
handlers:
  service_b.method1:
    description: "Method 1 in B"
`

	require.NoError(t, registry.LoadFromYAML([]byte(yaml1)))
	require.NoError(t, registry.LoadFromYAML([]byte(yaml2)))

	handlers := registry.ListHandlers()
	assert.Len(t, handlers, 2)
	assert.Contains(t, handlers, "service_a.method1")
	assert.Contains(t, handlers, "service_b.method1")
}

func TestValidateHandlerParams(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test handler"
    params:
      required_field:
        type: string
        required: true
      optional_field:
        type: string
        required: false
      amount:
        type: Decimal
        required: true
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
`

	schema, err := Parse([]byte(yaml))
	require.NoError(t, err)

	handler := schema.Handlers["test.handler"]

	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid params",
			params: map[string]any{
				"required_field": "value",
				"amount":         "100.50",
				"direction":      "DEBIT",
			},
			wantErr: false,
		},
		{
			name: "missing required field",
			params: map[string]any{
				"amount":    "100.50",
				"direction": "DEBIT",
			},
			wantErr: true,
			errMsg:  "required_field",
		},
		{
			name: "invalid enum value",
			params: map[string]any{
				"required_field": "value",
				"amount":         "100.50",
				"direction":      "INVALID",
			},
			wantErr: true,
			errMsg:  "direction",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.ValidateParams(tt.params)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, strings.Contains(err.Error(), tt.errMsg),
					"expected error to contain %q, got %q", tt.errMsg, err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}
