package patterns_test

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
	"gopkg.in/yaml.v3"
)

// patternsDir returns the absolute path to the patterns directory.
func patternsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Dir(file)
}

// schemaDir returns the absolute path to the schema directory.
func schemaDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(patternsDir(t), "..", "schema")
}

// compileSchema loads and compiles a JSON Schema file.
func compileSchema(t *testing.T, filename string) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join(schemaDir(t), filename)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read schema file %s", filename)

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft7

	uri := "file:///" + strings.TrimPrefix(filepath.ToSlash(path), "/")
	err = compiler.AddResource(uri, strings.NewReader(string(data)))
	require.NoError(t, err)

	s, err := compiler.Compile(uri)
	require.NoError(t, err)
	return s
}

// loadPatternJSON reads and unmarshals a pattern.json file.
func loadPatternJSON(t *testing.T, patternName string) map[string]any {
	t.Helper()
	path := filepath.Join(patternsDir(t), patternName, "pattern.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read pattern.json for %s", patternName)

	var v map[string]any
	require.NoError(t, json.Unmarshal(data, &v))
	return v
}

// validatePatternJSON validates a pattern.json against the registry-item.json schema.
func validatePatternJSON(t *testing.T, s *jsonschema.Schema, patternName string) {
	t.Helper()
	path := filepath.Join(patternsDir(t), patternName, "pattern.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var v any
	require.NoError(t, json.Unmarshal(data, &v))
	err = s.Validate(v)
	assert.NoError(t, err, "pattern.json for %s should be valid against registry-item.json schema", patternName)
}

// loadManifestFragment reads and parses the manifest-fragment.yaml for a pattern.
func loadManifestFragment(t *testing.T, patternName string) map[string]any {
	t.Helper()
	path := filepath.Join(patternsDir(t), patternName, "manifest-fragment.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read manifest-fragment.yaml for %s", patternName)

	var v map[string]any
	require.NoError(t, yaml.Unmarshal(data, &v))
	return v
}

// --- base-fiat-gbp tests ---

func TestBaseFiatGBP_PatternJSONValidatesAgainstSchema(t *testing.T) {
	s := compileSchema(t, "registry-item.json")
	validatePatternJSON(t, s, "base-fiat-gbp")
}

func TestBaseFiatGBP_ManifestFragmentIsValidYAML(t *testing.T) {
	fragment := loadManifestFragment(t, "base-fiat-gbp")
	require.NotNil(t, fragment, "manifest-fragment.yaml should not be empty")
}

func TestBaseFiatGBP_ManifestFragmentContainsGBPInstrument(t *testing.T) {
	fragment := loadManifestFragment(t, "base-fiat-gbp")

	instruments, ok := fragment["instruments"].([]any)
	require.True(t, ok, "manifest-fragment.yaml should have an instruments list")
	require.Len(t, instruments, 1, "base-fiat-gbp should define exactly one instrument")

	instrument, ok := instruments[0].(map[string]any)
	require.True(t, ok, "instrument entry should be a map")
	assert.Equal(t, "GBP", instrument["code"], "instrument code should be GBP")
	dimensions, ok := instrument["dimensions"].(map[string]any)
	require.True(t, ok, "instrument should have a dimensions map")
	assert.EqualValues(t, 2, dimensions["precision"], "instrument precision should be 2")
}

func TestBaseFiatGBP_ProvidesMatchesManifestInstruments(t *testing.T) {
	pattern := loadPatternJSON(t, "base-fiat-gbp")
	fragment := loadManifestFragment(t, "base-fiat-gbp")

	meta, ok := pattern["meta"].(map[string]any)
	require.True(t, ok)
	provides, ok := meta["provides"].(map[string]any)
	require.True(t, ok)
	providedInstruments, ok := provides["instruments"].([]any)
	require.True(t, ok)

	instruments, ok := fragment["instruments"].([]any)
	require.True(t, ok)

	require.Equal(t, len(instruments), len(providedInstruments),
		"provides.instruments count should match instruments in manifest-fragment.yaml")

	fragmentCodes := make(map[string]bool)
	for i, inst := range instruments {
		m, ok := inst.(map[string]any)
		require.True(t, ok, "instrument[%d] should be an object", i)
		c, ok := m["code"].(string)
		require.True(t, ok, "instrument[%d].code should be a string", i)
		fragmentCodes[c] = true
	}
	for i, code := range providedInstruments {
		c, ok := code.(string)
		require.True(t, ok, "provided instrument[%d] should be a string", i)
		assert.True(t, fragmentCodes[c],
			"instrument %q in provides should be present in manifest-fragment.yaml", c)
	}
}

func TestBaseFiatGBP_HasEmptyDependencies(t *testing.T) {
	pattern := loadPatternJSON(t, "base-fiat-gbp")

	deps, ok := pattern["registryDependencies"].([]any)
	require.True(t, ok, "registryDependencies should be present and an array")
	assert.Empty(t, deps, "foundation pattern should have no registry dependencies")

	meta, ok := pattern["meta"].(map[string]any)
	require.True(t, ok)
	requires, ok := meta["requires"].(map[string]any)
	require.True(t, ok, "meta.requires should be present")

	instrRequires, ok := requires["instruments"].([]any)
	require.True(t, ok)
	assert.Empty(t, instrRequires, "foundation pattern should require no instruments")

	marketRequires, ok := requires["market_data"].([]any)
	require.True(t, ok)
	assert.Empty(t, marketRequires, "foundation pattern should require no market_data")
}

// --- base-fiat-usd tests ---

func TestBaseFiatUSD_PatternJSONValidatesAgainstSchema(t *testing.T) {
	s := compileSchema(t, "registry-item.json")
	validatePatternJSON(t, s, "base-fiat-usd")
}

func TestBaseFiatUSD_ManifestFragmentIsValidYAML(t *testing.T) {
	fragment := loadManifestFragment(t, "base-fiat-usd")
	require.NotNil(t, fragment, "manifest-fragment.yaml should not be empty")
}

func TestBaseFiatUSD_ManifestFragmentContainsUSDInstrument(t *testing.T) {
	fragment := loadManifestFragment(t, "base-fiat-usd")

	instruments, ok := fragment["instruments"].([]any)
	require.True(t, ok, "manifest-fragment.yaml should have an instruments list")
	require.Len(t, instruments, 1, "base-fiat-usd should define exactly one instrument")

	instrument, ok := instruments[0].(map[string]any)
	require.True(t, ok, "instrument entry should be a map")
	assert.Equal(t, "USD", instrument["code"], "instrument code should be USD")
	dimensions, ok := instrument["dimensions"].(map[string]any)
	require.True(t, ok, "instrument should have a dimensions map")
	assert.EqualValues(t, 2, dimensions["precision"], "instrument precision should be 2")
}

func TestBaseFiatUSD_ProvidesMatchesManifestInstruments(t *testing.T) {
	pattern := loadPatternJSON(t, "base-fiat-usd")
	fragment := loadManifestFragment(t, "base-fiat-usd")

	meta, ok := pattern["meta"].(map[string]any)
	require.True(t, ok)
	provides, ok := meta["provides"].(map[string]any)
	require.True(t, ok)
	providedInstruments, ok := provides["instruments"].([]any)
	require.True(t, ok)

	instruments, ok := fragment["instruments"].([]any)
	require.True(t, ok)

	require.Equal(t, len(instruments), len(providedInstruments),
		"provides.instruments count should match instruments in manifest-fragment.yaml")

	fragmentCodes := make(map[string]bool)
	for i, inst := range instruments {
		m, ok := inst.(map[string]any)
		require.True(t, ok, "instrument[%d] should be an object", i)
		c, ok := m["code"].(string)
		require.True(t, ok, "instrument[%d].code should be a string", i)
		fragmentCodes[c] = true
	}
	for i, code := range providedInstruments {
		c, ok := code.(string)
		require.True(t, ok, "provided instrument[%d] should be a string", i)
		assert.True(t, fragmentCodes[c],
			"instrument %q in provides should be present in manifest-fragment.yaml", c)
	}
}

func TestBaseFiatUSD_HasEmptyDependencies(t *testing.T) {
	pattern := loadPatternJSON(t, "base-fiat-usd")

	deps, ok := pattern["registryDependencies"].([]any)
	require.True(t, ok, "registryDependencies should be present and an array")
	assert.Empty(t, deps, "foundation pattern should have no registry dependencies")

	meta, ok := pattern["meta"].(map[string]any)
	require.True(t, ok)
	requires, ok := meta["requires"].(map[string]any)
	require.True(t, ok, "meta.requires should be present")

	instrRequires, ok := requires["instruments"].([]any)
	require.True(t, ok)
	assert.Empty(t, instrRequires, "foundation pattern should require no instruments")

	marketRequires, ok := requires["market_data"].([]any)
	require.True(t, ok)
	assert.Empty(t, marketRequires, "foundation pattern should require no market_data")
}

// --- registry.json integration tests ---

func TestRegistry_ContainsBaseFiatGBP(t *testing.T) {
	registryPath := filepath.Join(patternsDir(t), "..", "registry.json")
	data, err := os.ReadFile(registryPath)
	require.NoError(t, err)

	var registry map[string]any
	require.NoError(t, json.Unmarshal(data, &registry))

	items, ok := registry["items"].([]any)
	require.True(t, ok)

	found := false
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if m["name"] == "base-fiat-gbp" {
			found = true
			assert.Equal(t, "registry:pattern", m["type"])
		}
	}
	assert.True(t, found, "registry.json should contain base-fiat-gbp entry")
}

func TestRegistry_ContainsBaseFiatUSD(t *testing.T) {
	registryPath := filepath.Join(patternsDir(t), "..", "registry.json")
	data, err := os.ReadFile(registryPath)
	require.NoError(t, err)

	var registry map[string]any
	require.NoError(t, json.Unmarshal(data, &registry))

	items, ok := registry["items"].([]any)
	require.True(t, ok)

	found := false
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if m["name"] == "base-fiat-usd" {
			found = true
			assert.Equal(t, "registry:pattern", m["type"])
		}
	}
	assert.True(t, found, "registry.json should contain base-fiat-usd entry")
}
