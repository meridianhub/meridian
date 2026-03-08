package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

func TestHandlerEvolution_VersionAndDeprecatedFields(t *testing.T) {
	yamlData := `
service: test_service
version: "2.0"
handlers:
  test.record_entry:
    description: "Record an entry (v2)"
    version: 2
    compensation_strategy: none
    params:
      quantity:
        type: Decimal
        required: true
      instrument_code:
        type: string
        required: true
      side:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
    returns:
      entry_id:
        type: string
    conversions:
      - from_version: 1
        from_name: test.initiate_log
        param_mapping:
          quantity: amount
          instrument_code: currency
          side: direction
        defaults:
          instrument_code: "'GBP'"
        sunset: "3.0"
  test.old_handler:
    description: "An old handler still present but deprecated"
    deprecated: true
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    returns:
      status:
        type: string
`

	schema, err := Parse([]byte(yamlData))
	require.NoError(t, err)

	// Verify version field parsed
	recordEntry := schema.Handlers["test.record_entry"]
	require.NotNil(t, recordEntry)
	assert.Equal(t, 2, recordEntry.Version)
	assert.False(t, recordEntry.Deprecated)

	// Verify conversions parsed
	require.Len(t, recordEntry.Conversions, 1)
	conv := recordEntry.Conversions[0]
	assert.Equal(t, 1, conv.FromVersion)
	assert.Equal(t, "test.initiate_log", conv.FromName)
	assert.Equal(t, map[string]string{
		"quantity":        "amount",
		"instrument_code": "currency",
		"side":            "direction",
	}, conv.ParamMapping)
	assert.Equal(t, map[string]string{
		"instrument_code": "'GBP'",
	}, conv.Defaults)
	assert.Equal(t, "3.0", conv.Sunset)

	// Verify deprecated field
	oldHandler := schema.Handlers["test.old_handler"]
	require.NotNil(t, oldHandler)
	assert.True(t, oldHandler.Deprecated)
}

func TestHandlerEvolution_ConversionRuleValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "from_version must be positive",
			yaml: `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test"
    version: 2
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    conversions:
      - from_version: 0
        from_name: test.old
`,
			wantErr: "from_version must be positive",
		},
		{
			name: "from_version must be less than current version",
			yaml: `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test"
    version: 2
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    conversions:
      - from_version: 2
        from_name: test.old
`,
			wantErr: "from_version (2) must be less than current version (2)",
		},
		{
			name: "param_mapping references unknown parameter",
			yaml: `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test"
    version: 2
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    conversions:
      - from_version: 1
        param_mapping:
          nonexistent: old_param
`,
			wantErr: "param_mapping references unknown parameter \"nonexistent\"",
		},
		{
			name: "defaults references unknown parameter",
			yaml: `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test"
    version: 2
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    conversions:
      - from_version: 1
        defaults:
          missing_param: "'default'"
`,
			wantErr: "defaults references unknown parameter \"missing_param\"",
		},
		{
			name: "valid conversion rule passes",
			yaml: `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test"
    version: 2
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    conversions:
      - from_version: 1
        from_name: test.old_handler
        param_mapping:
          id: old_id
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHandlerEvolution_RegistryDeprecatedLookup(t *testing.T) {
	yamlData := `
service: test_service
version: "2.0"
handlers:
  test.record_entry:
    description: "Record an entry (v2)"
    version: 2
    compensation_strategy: none
    params:
      quantity:
        type: Decimal
        required: true
      instrument_code:
        type: string
        required: true
    returns:
      entry_id:
        type: string
    conversions:
      - from_version: 1
        from_name: test.initiate_log
        param_mapping:
          quantity: amount
          instrument_code: currency
        sunset: "3.0"
`

	registry := NewRegistry()
	err := registry.LoadFromYAML([]byte(yamlData))
	require.NoError(t, err)

	// Current handler exists
	assert.True(t, registry.HasHandler("test.record_entry"))

	// Deprecated name is indexed
	mapping := registry.LookupDeprecated("test.initiate_log")
	require.NotNil(t, mapping)
	assert.Equal(t, "test.record_entry", mapping.CurrentName)
	assert.Equal(t, 1, mapping.ConversionRule.FromVersion)
	assert.Equal(t, "3.0", mapping.ConversionRule.Sunset)

	// Non-deprecated name returns nil
	assert.Nil(t, registry.LookupDeprecated("test.record_entry"))
	assert.Nil(t, registry.LookupDeprecated("nonexistent"))
}

func TestHandlerEvolution_IsDeprecated(t *testing.T) {
	yamlData := `
service: test_service
version: "1.0"
handlers:
  test.current:
    description: "Current handler"
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
  test.old:
    description: "Old handler"
    deprecated: true
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
`

	registry := NewRegistry()
	err := registry.LoadFromYAML([]byte(yamlData))
	require.NoError(t, err)

	assert.False(t, registry.IsDeprecated("test.current"))
	assert.True(t, registry.IsDeprecated("test.old"))
	assert.False(t, registry.IsDeprecated("nonexistent"))
}

func TestHandlerEvolution_LinterMetadataDeprecation(t *testing.T) {
	yamlData := `
service: test_service
version: "2.0"
handlers:
  test.record_entry:
    description: "Record an entry (v2)"
    version: 2
    compensation_strategy: none
    params:
      quantity:
        type: Decimal
        required: true
    conversions:
      - from_version: 1
        from_name: test.initiate_log
        param_mapping:
          quantity: amount
  test.deprecated_handler:
    description: "Old handler"
    deprecated: true
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
`

	registry := NewRegistry()
	err := registry.LoadFromYAML([]byte(yamlData))
	require.NoError(t, err)

	metadata := registry.BuildLinterMetadata()

	// Current handler not deprecated
	assert.False(t, metadata["test.record_entry"].IsDeprecated)
	assert.Equal(t, "", metadata["test.record_entry"].ReplacedBy)

	// Deprecated handler marked
	assert.True(t, metadata["test.deprecated_handler"].IsDeprecated)

	// Deprecated name alias also in metadata
	deprecatedAlias, ok := metadata["test.initiate_log"]
	require.True(t, ok)
	assert.True(t, deprecatedAlias.IsDeprecated)
	assert.Equal(t, "test.record_entry", deprecatedAlias.ReplacedBy)
}

func TestHandlerEvolution_ValidationModulesDeprecatedWarning(t *testing.T) {
	yamlData := `
service: test_service
version: "2.0"
handlers:
  test.record_entry:
    description: "Record an entry (v2)"
    version: 2
    compensation_strategy: none
    params:
      quantity:
        type: Decimal
        required: true
    returns:
      entry_id:
        type: string
    conversions:
      - from_version: 1
        from_name: test.initiate_log
        param_mapping:
          quantity: amount
        sunset: "3.0"
`

	registry := NewRegistry()
	err := registry.LoadFromYAML([]byte(yamlData))
	require.NoError(t, err)

	var warnings []ValidationWarning
	modules, err := BuildValidationModulesWithWarnings(registry, nil, &warnings)
	require.NoError(t, err)
	require.NotNil(t, modules)

	// Current handler should be accessible and produce no warnings
	thread := &starlark.Thread{Name: "test"}
	testModule, ok := modules["test"]
	require.True(t, ok)

	recordEntry, err := testModule.(*starlarkstruct.Struct).Attr("record_entry")
	require.NoError(t, err)

	_, err = starlark.Call(thread, recordEntry, nil, []starlark.Tuple{
		{starlark.String("quantity"), starlark.String("100.00")},
	})
	require.NoError(t, err)
	assert.Empty(t, warnings)

	// Deprecated handler name should also be accessible and produce a warning
	initiateLog, err := testModule.(*starlarkstruct.Struct).Attr("initiate_log")
	require.NoError(t, err)

	_, err = starlark.Call(thread, initiateLog, nil, nil)
	require.NoError(t, err)

	require.Len(t, warnings, 1)
	assert.Equal(t, ValidationCodeDeprecatedHandler, warnings[0].Code)
	assert.Contains(t, warnings[0].Message, "test.initiate_log")
	assert.Contains(t, warnings[0].Message, "deprecated")
	assert.Contains(t, warnings[0].Suggestion, "test.record_entry")
	assert.Contains(t, warnings[0].Suggestion, "amount->quantity")
	assert.Contains(t, warnings[0].Suggestion, "version 3.0")
}

func TestHandlerEvolution_DeprecatedCurrentHandlerWarning(t *testing.T) {
	yamlData := `
service: test_service
version: "1.0"
handlers:
  test.old_handler:
    description: "Old handler"
    deprecated: true
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    returns:
      status:
        type: string
`

	registry := NewRegistry()
	err := registry.LoadFromYAML([]byte(yamlData))
	require.NoError(t, err)

	var warnings []ValidationWarning
	modules, err := BuildValidationModulesWithWarnings(registry, nil, &warnings)
	require.NoError(t, err)

	thread := &starlark.Thread{Name: "test"}
	testModule := modules["test"].(*starlarkstruct.Struct)

	oldHandler, err := testModule.Attr("old_handler")
	require.NoError(t, err)

	// Calling the deprecated handler still validates params
	_, err = starlark.Call(thread, oldHandler, nil, []starlark.Tuple{
		{starlark.String("id"), starlark.String("123")},
	})
	require.NoError(t, err)

	require.Len(t, warnings, 1)
	assert.Equal(t, ValidationCodeDeprecatedHandler, warnings[0].Code)
	assert.Contains(t, warnings[0].Message, "test.old_handler")

	// Validation still works — unknown param rejected
	_, err = starlark.Call(thread, oldHandler, nil, []starlark.Tuple{
		{starlark.String("nonexistent"), starlark.String("123")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNKNOWN_PARAM")
}

func TestHandlerEvolution_CurrentVersionHandlerNoWarning(t *testing.T) {
	yamlData := `
service: test_service
version: "2.0"
handlers:
  test.handler:
    description: "Current handler"
    version: 2
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    returns:
      result:
        type: string
`

	registry := NewRegistry()
	err := registry.LoadFromYAML([]byte(yamlData))
	require.NoError(t, err)

	var warnings []ValidationWarning
	modules, err := BuildValidationModulesWithWarnings(registry, nil, &warnings)
	require.NoError(t, err)

	thread := &starlark.Thread{Name: "test"}
	testModule := modules["test"].(*starlarkstruct.Struct)

	handler, err := testModule.Attr("handler")
	require.NoError(t, err)

	_, err = starlark.Call(thread, handler, nil, []starlark.Tuple{
		{starlark.String("id"), starlark.String("123")},
	})
	require.NoError(t, err)
	assert.Empty(t, warnings)
}

func TestHandlerEvolution_HandlersYAMLVersionConversion(t *testing.T) {
	// Verify the embedded handlers.yaml can add versioned handlers with conversions
	// by testing with a custom YAML that mimics the real handlers.yaml structure
	yamlData := `
service: platform
version: "2.0"
handlers:
  position_keeping.record_entry:
    description: "Record a position entry (v2)"
    version: 2
    compensation_strategy: none
    params:
      quantity:
        type: Decimal
        required: true
      instrument_code:
        type: string
        required: true
      side:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
      correlation_id:
        type: string
        required: false
    returns:
      entry_id:
        type: string
    conversions:
      - from_version: 1
        from_name: position_keeping.initiate_log
        param_mapping:
          quantity: amount
          instrument_code: currency
          side: direction
        defaults:
          correlation_id: "'auto_' + old_params.account_id"
        sunset: "3.0"
`

	registry := NewRegistry()
	err := registry.LoadFromYAML([]byte(yamlData))
	require.NoError(t, err)

	// Verify the deprecated name is indexed
	mapping := registry.LookupDeprecated("position_keeping.initiate_log")
	require.NotNil(t, mapping)
	assert.Equal(t, "position_keeping.record_entry", mapping.CurrentName)

	// Verify validation modules expose both old and new names
	var warnings []ValidationWarning
	modules, err := BuildValidationModulesWithWarnings(registry, nil, &warnings)
	require.NoError(t, err)

	pk := modules["position_keeping"].(*starlarkstruct.Struct)

	// New name works
	_, err = pk.Attr("record_entry")
	require.NoError(t, err)

	// Old name works (deprecated alias)
	_, err = pk.Attr("initiate_log")
	require.NoError(t, err)
}
