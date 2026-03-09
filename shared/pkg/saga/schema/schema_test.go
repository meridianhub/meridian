package schema

import (
	"os"
	"path/filepath"
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
    compensation_strategy: none
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
    compensation_strategy: none
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
    compensation_strategy: none
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
    compensation_strategy: none
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
    compensation_strategy: none
`

	yaml2 := `
service: service_b
version: "1.0"
handlers:
  service_b.method1:
    description: "Method 1 in B"
    compensation_strategy: none
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
    compensation_strategy: none
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

func TestLoadFromFile(t *testing.T) {
	// Create a temporary YAML file
	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, "test_handlers.yaml")

	yamlContent := `
service: file_test
version: "1.0"
handlers:
  file_test.handler:
    description: "Handler loaded from file"
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    returns:
      status:
        type: string
`
	err := os.WriteFile(schemaPath, []byte(yamlContent), 0o644)
	require.NoError(t, err)

	registry := NewRegistry()
	err = registry.LoadFromFile(schemaPath)
	require.NoError(t, err)

	handler, err := registry.GetHandler("file_test.handler")
	require.NoError(t, err)
	assert.Equal(t, "Handler loaded from file", handler.Description)
}

func TestLoadFromFile_NotFound(t *testing.T) {
	registry := NewRegistry()
	err := registry.LoadFromFile("/nonexistent/path/schema.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read schema file")
}

func TestLoadFromDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple YAML files
	files := map[string]string{
		"service_a.yaml": `
service: service_a
version: "1.0"
handlers:
  service_a.method1:
    description: "Method 1 from service A"
    compensation_strategy: none
`,
		"service_b.yml": `
service: service_b
version: "1.0"
handlers:
  service_b.method1:
    description: "Method 1 from service B"
    compensation_strategy: none
`,
		"not_a_schema.txt": `This should be ignored`,
	}

	for name, content := range files {
		path := filepath.Join(tmpDir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	// Create a subdirectory (should be skipped)
	subDir := filepath.Join(tmpDir, "subdir")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(subDir, "ignored.yaml"),
		[]byte(`service: ignored`),
		0o644,
	))

	registry := NewRegistry()
	err := registry.LoadFromDirectory(tmpDir)
	require.NoError(t, err)

	handlers := registry.ListHandlers()
	assert.Len(t, handlers, 2)
	assert.Contains(t, handlers, "service_a.method1")
	assert.Contains(t, handlers, "service_b.method1")
}

func TestLoadFromDirectory_InvalidSchema(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an invalid YAML file
	invalidPath := filepath.Join(tmpDir, "invalid.yaml")
	err := os.WriteFile(invalidPath, []byte(`
service: ""
handlers: {}
`), 0o644)
	require.NoError(t, err)

	registry := NewRegistry()
	err = registry.LoadFromDirectory(tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service is required")
}

func TestLoadFromDirectory_NotFound(t *testing.T) {
	registry := NewRegistry()
	err := registry.LoadFromDirectory("/nonexistent/directory")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read schema directory")
}

func TestRegistryValidateHandlerParams(t *testing.T) {
	registry := NewRegistry()

	yamlContent := `
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
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
`
	require.NoError(t, registry.LoadFromYAML([]byte(yamlContent)))

	// Valid params
	err := registry.ValidateHandlerParams("test.handler", map[string]any{
		"required_field": "value",
		"direction":      "DEBIT",
	})
	require.NoError(t, err)

	// Missing required field
	err = registry.ValidateHandlerParams("test.handler", map[string]any{
		"direction": "DEBIT",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingRequiredParam)

	// Handler not found
	err = registry.ValidateHandlerParams("nonexistent.handler", map[string]any{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHandlerNotFound)
}

func TestHandlerDef_ExternalField(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected bool
	}{
		{
			name: "handler with external: true",
			yaml: `
service: test
version: "1.0"
handlers:
  test.external_handler:
    description: "External payment gateway handler"
    compensation_strategy: none
    external: true
    params:
      payment_id:
        type: string
        required: true
`,
			expected: true,
		},
		{
			name: "handler without external field (defaults to false)",
			yaml: `
service: test
version: "1.0"
handlers:
  test.internal_handler:
    description: "Internal handler"
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
`,
			expected: false,
		},
		{
			name: "handler with explicit external: false",
			yaml: `
service: test
version: "1.0"
handlers:
  test.internal_handler:
    description: "Internal handler"
    compensation_strategy: none
    external: false
    params:
      id:
        type: string
        required: true
`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema, err := Parse([]byte(tt.yaml))
			require.NoError(t, err)
			require.Len(t, schema.Handlers, 1)

			// Get the handler (extract from map)
			var handler *HandlerDef
			for _, h := range schema.Handlers {
				handler = h
				break
			}

			assert.Equal(t, tt.expected, handler.External,
				"Expected External=%v, got %v", tt.expected, handler.External)
		})
	}
}

func TestPlatformHandlersSchema_ExternalHandlers(t *testing.T) {
	handlerRegistry := buildFullHandlerRegistry(t)
	derivedSchema, err := DeriveSchema(handlerRegistry)
	require.NoError(t, err)

	// Wrap in registry for GetHandler
	reg := NewRegistry()
	for name, def := range derivedSchema.Handlers {
		reg.handlers[name] = def
	}

	// Verify a few internal handlers are NOT marked as external
	internalHandlers := []string{
		"financial_accounting.post_entries",
		"position_keeping.initiate_log",
	}

	for _, handlerName := range internalHandlers {
		h, err := reg.GetHandler(handlerName)
		require.NoError(t, err, "expected %s in derived registry", handlerName)
		assert.False(t, h.External, "%s should NOT be marked as external", handlerName)
	}
}

func TestBuildLinterMetadata(t *testing.T) {
	tests := []struct {
		name           string
		yaml           string
		expectedMeta   map[string]LinterMetadata
		expectedCounts struct {
			total    int
			external int
		}
	}{
		{
			name: "registry with external and internal handlers",
			yaml: `
service: test
version: "1.0"
handlers:
  internal.save:
    description: "Internal save operation"
    compensation_strategy: none
    external: false
    params:
      id:
        type: string
        required: true
  gateway.send:
    description: "External gateway call"
    compensation_strategy: none
    external: true
    params:
      amount:
        type: Decimal
        required: true
  accounting.post:
    description: "No external field (defaults to false)"
    compensation_strategy: none
    params:
      entry_id:
        type: string
        required: true
`,
			expectedMeta: map[string]LinterMetadata{
				"gateway.send": {
					IsExternal:           true,
					RequiresPreCheck:     true,
					CompensationStrategy: "none",
				},
				"internal.save": {
					CompensationStrategy: "none",
				},
				"accounting.post": {
					CompensationStrategy: "none",
				},
			},
			expectedCounts: struct {
				total    int
				external int
			}{
				total:    3,
				external: 1,
			},
		},
		{
			name: "registry with no external handlers",
			yaml: `
service: test
version: "1.0"
handlers:
  internal.handler1:
    description: "Internal handler 1"
    compensation_strategy: none
    params: {}
  internal.handler2:
    description: "Internal handler 2"
    compensation_strategy: none
    external: false
    params: {}
`,
			expectedMeta: map[string]LinterMetadata{
				"internal.handler1": {
					CompensationStrategy: "none",
				},
				"internal.handler2": {
					CompensationStrategy: "none",
				},
			},
			expectedCounts: struct {
				total    int
				external int
			}{
				total:    2,
				external: 0,
			},
		},
		{
			name: "empty registry",
			yaml: `
service: empty
version: "1.0"
handlers: {}
`,
			expectedMeta: map[string]LinterMetadata{},
			expectedCounts: struct {
				total    int
				external int
			}{
				total:    0,
				external: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry()
			err := registry.LoadFromYAML([]byte(tt.yaml))
			require.NoError(t, err)

			metadata := registry.BuildLinterMetadata()

			// Verify expected metadata - all handlers should be in metadata now
			assert.Equal(t, len(tt.expectedMeta), len(metadata),
				"Expected %d handlers in metadata, got %d", len(tt.expectedMeta), len(metadata))

			for handlerName, expectedMeta := range tt.expectedMeta {
				actualMeta, exists := metadata[handlerName]
				assert.True(t, exists, "Expected handler %s to be in metadata", handlerName)
				assert.Equal(t, expectedMeta.IsExternal, actualMeta.IsExternal)
				assert.Equal(t, expectedMeta.RequiresPreCheck, actualMeta.RequiresPreCheck)
				assert.Equal(t, expectedMeta.CompensationStrategy, actualMeta.CompensationStrategy)
				assert.Equal(t, expectedMeta.HasAutoCompensation, actualMeta.HasAutoCompensation)
			}

			// Verify all handlers are in metadata
			allHandlers := registry.ListHandlers()
			assert.Len(t, allHandlers, tt.expectedCounts.total,
				"Expected %d total handlers", tt.expectedCounts.total)

			for _, handlerName := range allHandlers {
				_, exists := metadata[handlerName]
				assert.True(t, exists, "Handler %s should be in metadata", handlerName)
			}
		})
	}
}
