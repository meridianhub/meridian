package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// testRegistryWithEvolution creates a schema.Registry with a handler that has
// a deprecated alias via conversion rules. This mirrors the handler_evolution_test
// pattern: test.initiate_log (deprecated) -> test.record_entry (current).
func testRegistryWithEvolution() *schema.Registry {
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
        sunset: "3.0"
  test.other_handler:
    description: "Another handler that is not deprecated"
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    returns:
      status:
        type: string
`
	reg := schema.NewRegistry()
	if err := reg.LoadFromYAML([]byte(yamlData)); err != nil {
		panic(err)
	}
	return reg
}

func TestManifestFix_DeprecatedHandlerConverted(t *testing.T) {
	reg := testRegistryWithEvolution()
	r := tools.NewRegistry()
	tools.RegisterManifestFixTool(r, reg)

	manifest := map[string]interface{}{
		"version": "1.0",
		"metadata": map[string]interface{}{
			"name":        "Test",
			"industry":    "energy",
			"description": "Test manifest",
		},
		"sagas": []interface{}{
			map[string]interface{}{
				"name":    "test_saga",
				"trigger": "api:/v1/test",
				"script": `def execute(ctx):
    result = test.initiate_log(
        amount="100.00",
        currency="GBP",
        direction="CREDIT",
    )
    return {"status": "done"}
`,
			},
		},
	}

	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	params := json.RawMessage(`{"manifest": ` + string(manifestJSON) + `}`)
	result, err := r.Call(context.Background(), "meridian_manifest_fix", params)
	require.NoError(t, err)

	m, ok := result.(map[string]interface{})
	require.True(t, ok)

	// Should have conversions
	conversions, ok := m["conversions"].([]interface{})
	require.True(t, ok)
	require.Len(t, conversions, 1)

	conv := conversions[0].(map[string]interface{})
	assert.Equal(t, "test_saga", conv["saga"])
	assert.Contains(t, conv["message"], "test.initiate_log")
	assert.Contains(t, conv["message"], "test.record_entry")

	// The fixed manifest should contain the converted script
	fixedManifest, ok := m["manifest"].(map[string]interface{})
	require.True(t, ok)

	sagas, ok := fixedManifest["sagas"].([]interface{})
	require.True(t, ok)
	require.Len(t, sagas, 1)

	saga := sagas[0].(map[string]interface{})
	script := saga["script"].(string)
	assert.Contains(t, script, "test.record_entry(")
	assert.NotContains(t, script, "test.initiate_log(")
	assert.Contains(t, script, "quantity=")
	assert.Contains(t, script, "instrument_code=")
	assert.Contains(t, script, "side=")
}

func TestManifestFix_NoDeprecatedHandlers_PassesThrough(t *testing.T) {
	reg := testRegistryWithEvolution()
	r := tools.NewRegistry()
	tools.RegisterManifestFixTool(r, reg)

	manifest := map[string]interface{}{
		"version": "1.0",
		"metadata": map[string]interface{}{
			"name":        "Test",
			"industry":    "energy",
			"description": "Test manifest",
		},
		"sagas": []interface{}{
			map[string]interface{}{
				"name":    "test_saga",
				"trigger": "api:/v1/test",
				"script": `def execute(ctx):
    result = test.record_entry(
        quantity="100.00",
        instrument_code="GBP",
        side="CREDIT",
    )
    return {"status": "done"}
`,
			},
		},
	}

	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	params := json.RawMessage(`{"manifest": ` + string(manifestJSON) + `}`)
	result, err := r.Call(context.Background(), "meridian_manifest_fix", params)
	require.NoError(t, err)

	m, ok := result.(map[string]interface{})
	require.True(t, ok)

	conversions, ok := m["conversions"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, conversions)

	// Manifest should be unchanged
	fixedManifest := m["manifest"].(map[string]interface{})
	sagas := fixedManifest["sagas"].([]interface{})
	saga := sagas[0].(map[string]interface{})
	assert.Contains(t, saga["script"].(string), "test.record_entry(")
}

func TestManifestFix_MultipleSagas(t *testing.T) {
	reg := testRegistryWithEvolution()
	r := tools.NewRegistry()
	tools.RegisterManifestFixTool(r, reg)

	manifest := map[string]interface{}{
		"version": "1.0",
		"metadata": map[string]interface{}{
			"name":        "Test",
			"industry":    "energy",
			"description": "Test manifest",
		},
		"sagas": []interface{}{
			map[string]interface{}{
				"name":    "saga_one",
				"trigger": "api:/v1/one",
				"script": `def execute(ctx):
    test.initiate_log(amount="50.00", currency="USD", direction="DEBIT")
`,
			},
			map[string]interface{}{
				"name":    "saga_two",
				"trigger": "api:/v1/two",
				"script": `def execute(ctx):
    test.other_handler(id="abc")
`,
			},
		},
	}

	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	params := json.RawMessage(`{"manifest": ` + string(manifestJSON) + `}`)
	result, err := r.Call(context.Background(), "meridian_manifest_fix", params)
	require.NoError(t, err)

	m := result.(map[string]interface{})
	conversions := m["conversions"].([]interface{})
	assert.Len(t, conversions, 1)

	conv := conversions[0].(map[string]interface{})
	assert.Equal(t, "saga_one", conv["saga"])
}

func TestManifestFix_NoSagas_ReturnsEmpty(t *testing.T) {
	reg := testRegistryWithEvolution()
	r := tools.NewRegistry()
	tools.RegisterManifestFixTool(r, reg)

	manifest := map[string]interface{}{
		"version": "1.0",
		"metadata": map[string]interface{}{
			"name":        "Test",
			"industry":    "energy",
			"description": "Test manifest",
		},
	}

	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	params := json.RawMessage(`{"manifest": ` + string(manifestJSON) + `}`)
	result, err := r.Call(context.Background(), "meridian_manifest_fix", params)
	require.NoError(t, err)

	m := result.(map[string]interface{})
	conversions := m["conversions"].([]interface{})
	assert.Empty(t, conversions)
}

func TestManifestFix_InvalidManifestJSON(t *testing.T) {
	reg := testRegistryWithEvolution()
	r := tools.NewRegistry()
	tools.RegisterManifestFixTool(r, reg)

	params := json.RawMessage(`{"manifest": "not an object"}`)
	_, err := r.Call(context.Background(), "meridian_manifest_fix", params)
	// JSON schema validation rejects non-object manifest
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestManifestFix_ParamRenamingScopedToCallSite(t *testing.T) {
	reg := testRegistryWithEvolution()
	r := tools.NewRegistry()
	tools.RegisterManifestFixTool(r, reg)

	// Script has both a deprecated call AND a non-deprecated call using
	// the same old param name "amount". Param renaming must only apply
	// inside the deprecated call's parentheses.
	manifest := map[string]interface{}{
		"version": "1.0",
		"metadata": map[string]interface{}{
			"name":        "Test",
			"industry":    "energy",
			"description": "Test manifest",
		},
		"sagas": []interface{}{
			map[string]interface{}{
				"name":    "scoped_test",
				"trigger": "api:/v1/test",
				"script": `def execute(ctx):
    test.initiate_log(amount="100.00", currency="GBP", direction="CREDIT")
    test.other_handler(id="abc")
    x = some_func(amount="200.00")
`,
			},
		},
	}

	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	params := json.RawMessage(`{"manifest": ` + string(manifestJSON) + `}`)
	result, err := r.Call(context.Background(), "meridian_manifest_fix", params)
	require.NoError(t, err)

	m := result.(map[string]interface{})
	fixedManifest := m["manifest"].(map[string]interface{})
	sagas := fixedManifest["sagas"].([]interface{})
	saga := sagas[0].(map[string]interface{})
	script := saga["script"].(string)

	// The deprecated call should be converted
	assert.Contains(t, script, "test.record_entry(quantity=")
	assert.NotContains(t, script, "test.initiate_log(")

	// The non-deprecated call's "amount" param must NOT be renamed
	assert.Contains(t, script, `some_func(amount="200.00")`)

	// other_handler should be untouched
	assert.Contains(t, script, `test.other_handler(id="abc")`)
}

func TestManifestFix_ParensInStringLiteral(t *testing.T) {
	reg := testRegistryWithEvolution()
	r := tools.NewRegistry()
	tools.RegisterManifestFixTool(r, reg)

	// Script has a string literal containing a closing paren.
	// extractCallBody must not be fooled by it.
	manifest := map[string]interface{}{
		"version": "1.0",
		"metadata": map[string]interface{}{
			"name":        "Test",
			"industry":    "energy",
			"description": "Test manifest",
		},
		"sagas": []interface{}{
			map[string]interface{}{
				"name":    "string_paren_test",
				"trigger": "api:/v1/test",
				"script": `def execute(ctx):
    test.initiate_log(amount="100.00)", currency="GBP", direction="CREDIT")
    return {"status": "done"}
`,
			},
		},
	}

	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	params := json.RawMessage(`{"manifest": ` + string(manifestJSON) + `}`)
	result, err := r.Call(context.Background(), "meridian_manifest_fix", params)
	require.NoError(t, err)

	m := result.(map[string]interface{})
	fixedManifest := m["manifest"].(map[string]interface{})
	sagas := fixedManifest["sagas"].([]interface{})
	saga := sagas[0].(map[string]interface{})
	script := saga["script"].(string)

	// Handler should be converted despite the paren in string
	assert.Contains(t, script, "test.record_entry(")
	assert.NotContains(t, script, "test.initiate_log(")
	// All three params should be renamed
	assert.Contains(t, script, "quantity=")
	assert.Contains(t, script, "instrument_code=")
	assert.Contains(t, script, "side=")
}

func TestManifestFix_SuffixParamNotRenamed(t *testing.T) {
	reg := testRegistryWithEvolution()
	r := tools.NewRegistry()
	tools.RegisterManifestFixTool(r, reg)

	// "total_amount" should NOT be renamed even though "amount" is in the mapping.
	manifest := map[string]interface{}{
		"version": "1.0",
		"metadata": map[string]interface{}{
			"name":        "Test",
			"industry":    "energy",
			"description": "Test manifest",
		},
		"sagas": []interface{}{
			map[string]interface{}{
				"name":    "suffix_test",
				"trigger": "api:/v1/test",
				"script": `def execute(ctx):
    test.initiate_log(amount="100.00", currency="GBP", direction="CREDIT", total_amount="200.00")
`,
			},
		},
	}

	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	params := json.RawMessage(`{"manifest": ` + string(manifestJSON) + `}`)
	result, err := r.Call(context.Background(), "meridian_manifest_fix", params)
	require.NoError(t, err)

	m := result.(map[string]interface{})
	fixedManifest := m["manifest"].(map[string]interface{})
	sagas := fixedManifest["sagas"].([]interface{})
	saga := sagas[0].(map[string]interface{})
	script := saga["script"].(string)

	// "amount" kwarg renamed to "quantity", but "total_amount" kept as-is
	assert.Contains(t, script, "quantity=")
	assert.Contains(t, script, "total_amount=")
}

func TestManifestFix_NestedCallParamsNotRenamed(t *testing.T) {
	reg := testRegistryWithEvolution()
	r := tools.NewRegistry()
	tools.RegisterManifestFixTool(r, reg)

	// The deprecated call has a nested function call with a param name
	// that matches the mapping. Only the outer kwarg should be renamed.
	manifest := map[string]interface{}{
		"version": "1.0",
		"metadata": map[string]interface{}{
			"name":        "Test",
			"industry":    "energy",
			"description": "Test manifest",
		},
		"sagas": []interface{}{
			map[string]interface{}{
				"name":    "nested_test",
				"trigger": "api:/v1/test",
				"script": `def execute(ctx):
    test.initiate_log(amount=compute(currency="USD"), currency="GBP", direction="CREDIT")
`,
			},
		},
	}

	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	params := json.RawMessage(`{"manifest": ` + string(manifestJSON) + `}`)
	result, err := r.Call(context.Background(), "meridian_manifest_fix", params)
	require.NoError(t, err)

	m := result.(map[string]interface{})
	fixedManifest := m["manifest"].(map[string]interface{})
	sagas := fixedManifest["sagas"].([]interface{})
	saga := sagas[0].(map[string]interface{})
	script := saga["script"].(string)

	// Outer "amount" renamed to "quantity", outer "currency" renamed to "instrument_code"
	assert.Contains(t, script, "quantity=compute(")
	assert.Contains(t, script, "instrument_code=\"GBP\"")
	// Inner "currency" inside compute() must NOT be renamed
	assert.Contains(t, script, `compute(currency="USD")`)
}

func TestManifestFix_ParamMappingApplied(t *testing.T) {
	reg := testRegistryWithEvolution()
	r := tools.NewRegistry()
	tools.RegisterManifestFixTool(r, reg)

	manifest := map[string]interface{}{
		"version": "1.0",
		"metadata": map[string]interface{}{
			"name":        "Test",
			"industry":    "energy",
			"description": "Test manifest",
		},
		"sagas": []interface{}{
			map[string]interface{}{
				"name":    "param_test",
				"trigger": "api:/v1/test",
				"script": `def execute(ctx):
    test.initiate_log(amount="100.00", currency="GBP", direction="CREDIT")
`,
			},
		},
	}

	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	params := json.RawMessage(`{"manifest": ` + string(manifestJSON) + `}`)
	result, err := r.Call(context.Background(), "meridian_manifest_fix", params)
	require.NoError(t, err)

	m := result.(map[string]interface{})
	fixedManifest := m["manifest"].(map[string]interface{})
	sagas := fixedManifest["sagas"].([]interface{})
	saga := sagas[0].(map[string]interface{})
	script := saga["script"].(string)

	// Old param names should be replaced with new ones
	assert.Contains(t, script, "quantity=")
	assert.Contains(t, script, "instrument_code=")
	assert.Contains(t, script, "side=")
	assert.NotContains(t, script, "amount=")
	assert.NotContains(t, script, "currency=")
	assert.NotContains(t, script, "direction=")
}
