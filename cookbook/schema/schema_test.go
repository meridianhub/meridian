package schema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaDir returns the absolute path to the directory containing the schema files.
func schemaDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Dir(file)
}

// compileSchema loads a JSON Schema file from the schema directory and compiles it.
func compileSchema(t *testing.T, filename string) *jsonschema.Schema {
	t.Helper()
	dir := schemaDir(t)
	path := filepath.Join(dir, filename)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read schema file %s", filename)

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft7

	uri := "file:///" + strings.TrimPrefix(filepath.ToSlash(path), "/")
	err = compiler.AddResource(uri, strings.NewReader(string(data)))
	require.NoError(t, err, "failed to add schema resource %s", filename)

	s, err := compiler.Compile(uri)
	require.NoError(t, err, "failed to compile schema %s", filename)
	return s
}

// validateJSON validates a JSON object (as map[string]any) against the schema.
func validateJSON(t *testing.T, s *jsonschema.Schema, obj any) error {
	t.Helper()
	b, err := json.Marshal(obj)
	require.NoError(t, err)
	var v any
	require.NoError(t, json.Unmarshal(b, &v))
	return s.Validate(v)
}

// --- registry-item.json tests ---

func TestRegistryItemSchema_Compiles(t *testing.T) {
	compileSchema(t, "registry-item.json")
}

func TestRegistryItemSchema_ValidUIComponent(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "transaction-table",
		"type":        "registry:ui",
		"title":       "Transaction Table",
		"description": "Displays a paginated table of ledger transactions.",
		"categories":  []string{"ledger", "display"},
		"meta": map[string]any{
			"feature_module":      "current-account",
			"tenant_configurable": true,
			"configurable_props":  []string{"pageSize", "columns"},
			"used_by":             []string{"current-account", "savings"},
		},
	}

	err := validateJSON(t, s, item)
	assert.NoError(t, err)
}

func TestRegistryItemSchema_ValidUIComponent_MinimalFields(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "balance-card",
		"type":        "registry:ui",
		"title":       "Balance Card",
		"description": "Shows account balance.",
	}

	err := validateJSON(t, s, item)
	assert.NoError(t, err)
}

func TestRegistryItemSchema_ValidPattern(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "fiat-current-account",
		"type":        "registry:pattern",
		"title":       "Fiat Current Account",
		"description": "A full-featured current account supporting fiat currency.",
		"categories":  []string{"banking", "fiat"},
		"meta": map[string]any{
			"complexity":     5,
			"design_pattern": "saga-orchestration",
			"industries":     []string{"banking", "fintech"},
			"provides": map[string]any{
				"instruments":     []string{"GBP", "USD"},
				"account_types":   []string{"CURRENT"},
				"sagas":           []string{"deposit", "withdrawal", "transfer"},
				"valuation_rules": []string{"gbp-usd-fx"},
				"triggers":        []string{"api.deposit", "api.withdrawal"},
			},
			"requires": map[string]any{
				"instruments": []string{"GBP"},
				"market_data": []string{"GBP_USD_FX"},
			},
			"composes_with":  []string{"overdraft-protection"},
			"conflicts_with": []string{"savings-only-account"},
			"extends":        []string{"base-account"},
		},
	}

	err := validateJSON(t, s, item)
	assert.NoError(t, err)
}

func TestRegistryItemSchema_ValidPattern_MinimalMeta(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "energy-trading",
		"type":        "registry:pattern",
		"title":       "Energy Trading",
		"description": "Handles energy commodity trading.",
		"meta": map[string]any{
			"complexity": 3,
		},
	}

	err := validateJSON(t, s, item)
	assert.NoError(t, err)
}

func TestRegistryItemSchema_ValidPattern_NullDesignPattern(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "carbon-credits",
		"type":        "registry:pattern",
		"title":       "Carbon Credits",
		"description": "Tracks carbon offset credits.",
		"meta": map[string]any{
			"complexity":     2,
			"design_pattern": nil,
		},
	}

	err := validateJSON(t, s, item)
	assert.NoError(t, err)
}

func TestRegistryItemSchema_InvalidName_UpperCase(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "MyComponent",
		"type":        "registry:ui",
		"title":       "My Component",
		"description": "A component.",
	}

	err := validateJSON(t, s, item)
	assert.Error(t, err, "uppercase name should fail validation")
}

func TestRegistryItemSchema_InvalidName_StartsWithDigit(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "1-invalid",
		"type":        "registry:ui",
		"title":       "Invalid",
		"description": "A component.",
	}

	err := validateJSON(t, s, item)
	assert.Error(t, err, "name starting with digit should fail validation")
}

func TestRegistryItemSchema_InvalidType(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "my-thing",
		"type":        "registry:unknown",
		"title":       "My Thing",
		"description": "A thing.",
	}

	err := validateJSON(t, s, item)
	assert.Error(t, err, "unknown type should fail validation")
}

func TestRegistryItemSchema_MissingRequiredName(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"type":        "registry:ui",
		"title":       "No Name",
		"description": "Missing name field.",
	}

	err := validateJSON(t, s, item)
	assert.Error(t, err, "missing name should fail validation")
}

func TestRegistryItemSchema_MissingRequiredTitle(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "no-title",
		"type":        "registry:ui",
		"description": "Missing title field.",
	}

	err := validateJSON(t, s, item)
	assert.Error(t, err, "missing title should fail validation")
}

func TestRegistryItemSchema_MissingRequiredDescription(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":  "no-desc",
		"type":  "registry:ui",
		"title": "No Description",
	}

	err := validateJSON(t, s, item)
	assert.Error(t, err, "missing description should fail validation")
}

func TestRegistryItemSchema_PatternMissingComplexity(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "missing-complexity",
		"type":        "registry:pattern",
		"title":       "Missing Complexity",
		"description": "Pattern without complexity field.",
		"meta": map[string]any{
			"industries": []string{"banking"},
		},
	}

	err := validateJSON(t, s, item)
	assert.Error(t, err, "pattern without complexity in meta should fail")
}

func TestRegistryItemSchema_UIWithFilesArray(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "account-form",
		"type":        "registry:ui",
		"title":       "Account Form",
		"description": "Form for creating accounts.",
		"files": []map[string]any{
			{"path": "components/AccountForm.tsx", "type": "registry:file"},
			{"path": "components/AccountForm.test.tsx", "type": "registry:file", "target": "src/components/AccountForm.test.tsx"},
		},
	}

	err := validateJSON(t, s, item)
	assert.NoError(t, err)
}

func TestRegistryItemSchema_FileMissingPath(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	item := map[string]any{
		"name":        "bad-files",
		"type":        "registry:ui",
		"title":       "Bad Files",
		"description": "Component with invalid files entry.",
		"files": []map[string]any{
			{"type": "registry:file"},
		},
	}

	err := validateJSON(t, s, item)
	assert.Error(t, err, "file entry missing path should fail validation")
}

// --- registry.json tests ---

func TestRegistryIndexSchema_Compiles(t *testing.T) {
	compileSchema(t, "registry.json")
}

func TestRegistryIndexSchema_ValidEmptyItems(t *testing.T) {
	s := compileSchema(t, "registry.json")

	index := map[string]any{
		"name":  "meridian-cookbook",
		"items": []any{},
	}

	err := validateJSON(t, s, index)
	assert.NoError(t, err)
}

func TestRegistryIndexSchema_ValidWithMixedItems(t *testing.T) {
	s := compileSchema(t, "registry.json")

	index := map[string]any{
		"$schema":  "https://cookbook.meridianhub.org/schema/registry.json",
		"name":     "meridian-cookbook",
		"homepage": "https://github.com/meridianhub/meridian",
		"items": []map[string]any{
			{
				"name":       "transaction-table",
				"type":       "registry:ui",
				"title":      "Transaction Table",
				"categories": []string{"ledger"},
			},
			{
				"name":  "fiat-current-account",
				"type":  "registry:pattern",
				"title": "Fiat Current Account",
			},
		},
	}

	err := validateJSON(t, s, index)
	assert.NoError(t, err)
}

func TestRegistryIndexSchema_MissingRequiredName(t *testing.T) {
	s := compileSchema(t, "registry.json")

	index := map[string]any{
		"items": []any{},
	}

	err := validateJSON(t, s, index)
	assert.Error(t, err, "missing name should fail validation")
}

func TestRegistryIndexSchema_MissingRequiredItems(t *testing.T) {
	s := compileSchema(t, "registry.json")

	index := map[string]any{
		"name": "meridian-cookbook",
	}

	err := validateJSON(t, s, index)
	assert.Error(t, err, "missing items should fail validation")
}

func TestRegistryIndexSchema_ItemMissingName(t *testing.T) {
	s := compileSchema(t, "registry.json")

	index := map[string]any{
		"name": "meridian-cookbook",
		"items": []map[string]any{
			{
				"type":  "registry:ui",
				"title": "No Name",
			},
		},
	}

	err := validateJSON(t, s, index)
	assert.Error(t, err, "item missing name should fail validation")
}

func TestRegistryIndexSchema_ItemMissingTitle(t *testing.T) {
	s := compileSchema(t, "registry.json")

	index := map[string]any{
		"name": "meridian-cookbook",
		"items": []map[string]any{
			{
				"name": "no-title",
				"type": "registry:ui",
			},
		},
	}

	err := validateJSON(t, s, index)
	assert.Error(t, err, "item missing title should fail validation")
}

func TestRegistryIndexSchema_ItemInvalidNamePattern(t *testing.T) {
	s := compileSchema(t, "registry.json")

	index := map[string]any{
		"name": "meridian-cookbook",
		"items": []map[string]any{
			{
				"name":  "BadName",
				"type":  "registry:ui",
				"title": "Bad Name",
			},
		},
	}

	err := validateJSON(t, s, index)
	assert.Error(t, err, "item with invalid name pattern should fail validation")
}

func TestRegistryIndexSchema_ItemMissingType(t *testing.T) {
	s := compileSchema(t, "registry.json")

	index := map[string]any{
		"name": "meridian-cookbook",
		"items": []map[string]any{
			{
				"name":  "my-item",
				"title": "My Item",
			},
		},
	}

	err := validateJSON(t, s, index)
	assert.Error(t, err, "item missing type should fail validation")
}

// TestRegistryIndexSchema_ActualRegistryFile validates the real registry.json file.
func TestRegistryIndexSchema_ActualRegistryFile(t *testing.T) {
	s := compileSchema(t, "registry.json")

	dir := schemaDir(t)
	// registry.json is one level up from schema/
	registryPath := filepath.Join(dir, "..", "registry.json")

	data, err := os.ReadFile(registryPath)
	require.NoError(t, err, "failed to read cookbook/registry.json")

	var v any
	require.NoError(t, json.Unmarshal(data, &v))

	err = s.Validate(v)
	assert.NoError(t, err, "cookbook/registry.json should be valid against registry.json schema")
}
