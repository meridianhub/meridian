package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// testdataCookbookLoader returns a CookbookLoader backed by the testdata/cookbook fixtures.
func testdataCookbookLoader(t *testing.T) tools.CookbookLoader {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Join(filepath.Dir(file), "testdata", "cookbook_discover")
	return tools.NewFSCookbookLoader(os.DirFS(dir))
}

// callDiscover is a helper that registers and calls the discover tool with the given params.
func callDiscover(t *testing.T, loader tools.CookbookLoader, params json.RawMessage) map[string]interface{} {
	t.Helper()
	r := tools.NewRegistry()
	tools.RegisterCookbookDiscoverTool(r, loader)

	result, err := r.Call(context.Background(), "meridian_cookbook_discover", params)
	if err != nil {
		t.Fatalf("unexpected error calling discover: %v", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("result is not a map: %v", err)
	}
	return m
}

// getSlice extracts a JSON array field from a map result.
func getSlice(t *testing.T, m map[string]interface{}, key string) []map[string]interface{} {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		t.Fatalf("missing key %q in result", key)
	}
	items, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("key %q is not a slice, got %T", key, raw)
	}
	result := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		b, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("failed to marshal item: %v", err)
		}
		var entry map[string]interface{}
		if err := json.Unmarshal(b, &entry); err != nil {
			t.Fatalf("failed to unmarshal item: %v", err)
		}
		result = append(result, entry)
	}
	return result
}

// findByName returns the first entry with the given name field, or nil.
func findByName(entries []map[string]interface{}, name string) map[string]interface{} {
	for _, e := range entries {
		if n, _ := e["name"].(string); n == name {
			return e
		}
	}
	return nil
}

// TestCookbookDiscover_EmptyManifest_FoundationPatternsCompatible verifies that
// patterns with no requirements appear in compatible when no manifest is provided.
func TestCookbookDiscover_EmptyManifest_FoundationPatternsCompatible(t *testing.T) {
	loader := testdataCookbookLoader(t)
	m := callDiscover(t, loader, json.RawMessage(`{}`))

	compatible := getSlice(t, m, "compatible")
	entry := findByName(compatible, "fiat-foundation")
	if entry == nil {
		t.Error("expected fiat-foundation in compatible for empty manifest")
	}
}

// TestCookbookDiscover_ManifestWithGBP_EnergySettlementCompatible verifies that
// energy-settlement becomes compatible when GBP is present in the manifest.
func TestCookbookDiscover_ManifestWithGBP_EnergySettlementCompatible(t *testing.T) {
	loader := testdataCookbookLoader(t)
	manifest := `{"instruments": [{"code": "GBP", "name": "British Pound"}]}`
	params := json.RawMessage(`{"manifest": ` + manifest + `}`)
	m := callDiscover(t, loader, params)

	compatible := getSlice(t, m, "compatible")
	entry := findByName(compatible, "energy-settlement")
	if entry == nil {
		t.Error("expected energy-settlement in compatible when GBP is present")
	}
}

// TestCookbookDiscover_EmptyManifest_UnmetRequirementsIncompatible verifies that
// patterns with unmet instrument requirements appear in incompatible.
func TestCookbookDiscover_EmptyManifest_UnmetRequirementsIncompatible(t *testing.T) {
	loader := testdataCookbookLoader(t)
	m := callDiscover(t, loader, json.RawMessage(`{}`))

	incompatible := getSlice(t, m, "incompatible")

	// energy-settlement requires GBP; it should appear in incompatible when GBP is absent.
	entry := findByName(incompatible, "energy-settlement")
	if entry == nil {
		t.Error("expected energy-settlement in incompatible for empty manifest")
	}

	// Verify missing instruments are reported.
	missing, ok := entry["missing"].([]interface{})
	if !ok || len(missing) == 0 {
		t.Error("expected non-empty missing list for energy-settlement")
	}
	found := false
	for _, item := range missing {
		if s, _ := item.(string); s == "GBP" {
			found = true
		}
	}
	if !found {
		t.Error("expected GBP in missing list for energy-settlement")
	}
}

// TestCookbookDiscover_InstalledPatternConflict_AppearsInConflicts verifies that a pattern
// conflicting with an already-installed pattern (inferred from manifest instruments) is reported.
//
// The conflicting-pattern fixture declares conflicts_with: ["fiat-foundation"].
// When GBP and USD are both present in the manifest, fiat-foundation is inferred as installed
// (because it provides both GBP and USD). So conflicting-pattern should appear in conflicts.
func TestCookbookDiscover_InstalledPatternConflict_AppearsInConflicts(t *testing.T) {
	loader := testdataCookbookLoader(t)
	// Provide both GBP and USD so fiat-foundation is inferred as installed.
	manifest := `{"instruments": [{"code": "GBP"}, {"code": "USD"}]}`
	params := json.RawMessage(`{"manifest": ` + manifest + `}`)
	m := callDiscover(t, loader, params)

	conflicts := getSlice(t, m, "conflicts")
	entry := findByName(conflicts, "conflicting-pattern")
	if entry == nil {
		t.Error("expected conflicting-pattern in conflicts when fiat-foundation is inferred as installed")
	}

	if conflictingWith, _ := entry["conflicting_with"].(string); conflictingWith != "fiat-foundation" {
		t.Errorf("conflicting_with = %q, want fiat-foundation", conflictingWith)
	}
}

// TestCookbookDiscover_HATEOASLinks_CorrectlyFormatted verifies that compatible patterns
// include properly structured HATEOAS navigation links.
func TestCookbookDiscover_HATEOASLinks_CorrectlyFormatted(t *testing.T) {
	loader := testdataCookbookLoader(t)
	m := callDiscover(t, loader, json.RawMessage(`{}`))

	compatible := getSlice(t, m, "compatible")
	entry := findByName(compatible, "fiat-foundation")
	if entry == nil {
		t.Fatal("fiat-foundation not found in compatible")
	}

	links, ok := entry["_links"].(map[string]interface{})
	if !ok {
		t.Fatalf("_links is not a map, got %T", entry["_links"])
	}

	// Verify detail link.
	detail, ok := links["detail"].(map[string]interface{})
	if !ok {
		t.Fatal("_links.detail is missing or not a map")
	}
	if tool, _ := detail["tool"].(string); tool != "meridian_cookbook_describe" {
		t.Errorf("_links.detail.tool = %q, want meridian_cookbook_describe", tool)
	}
	params, ok := detail["params"].(map[string]interface{})
	if !ok {
		t.Fatal("_links.detail.params is missing or not a map")
	}
	if name, _ := params["name"].(string); name != "fiat-foundation" {
		t.Errorf("_links.detail.params.name = %q, want fiat-foundation", name)
	}

	// Verify compose link.
	compose, ok := links["compose"].(map[string]interface{})
	if !ok {
		t.Fatal("_links.compose is missing or not a map")
	}
	if tool, _ := compose["tool"].(string); tool != "meridian_manifest_plan" {
		t.Errorf("_links.compose.tool = %q, want meridian_manifest_plan", tool)
	}

	// Verify validate link.
	validate, ok := links["validate"].(map[string]interface{})
	if !ok {
		t.Fatal("_links.validate is missing or not a map")
	}
	if tool, _ := validate["tool"].(string); tool != "meridian_manifest_validate" {
		t.Errorf("_links.validate.tool = %q, want meridian_manifest_validate", tool)
	}
}

// TestCookbookDiscover_TypeFilter_PatternOnly verifies the type filter excludes UI items.
func TestCookbookDiscover_TypeFilter_PatternOnly(t *testing.T) {
	loader := testdataCookbookLoader(t)
	m := callDiscover(t, loader, json.RawMessage(`{"type": "registry:pattern"}`))

	compatible := getSlice(t, m, "compatible")
	incompatible := getSlice(t, m, "incompatible")
	conflicts := getSlice(t, m, "conflicts")

	allEntries := make([]map[string]interface{}, 0, len(compatible)+len(incompatible)+len(conflicts))
	allEntries = append(allEntries, compatible...)
	allEntries = append(allEntries, incompatible...)
	allEntries = append(allEntries, conflicts...)

	for _, entry := range allEntries {
		if typ, _ := entry["type"].(string); typ == "registry:ui" {
			t.Errorf("type filter registry:pattern should exclude UI items, but found %q", entry["name"])
		}
	}

	// Verify that at least one pattern item appears.
	if len(allEntries) == 0 {
		t.Error("expected at least one pattern item in results when filtering registry:pattern")
	}
}

// TestCookbookDiscover_TypeFilter_UIOnly verifies the type filter shows only UI items.
func TestCookbookDiscover_TypeFilter_UIOnly(t *testing.T) {
	loader := testdataCookbookLoader(t)
	m := callDiscover(t, loader, json.RawMessage(`{"type": "registry:ui"}`))

	compatible := getSlice(t, m, "compatible")
	incompatible := getSlice(t, m, "incompatible")
	conflicts := getSlice(t, m, "conflicts")

	allEntries := make([]map[string]interface{}, 0, len(compatible)+len(incompatible)+len(conflicts))
	allEntries = append(allEntries, compatible...)
	allEntries = append(allEntries, incompatible...)
	allEntries = append(allEntries, conflicts...)

	for _, entry := range allEntries {
		if typ, _ := entry["type"].(string); typ == "registry:pattern" {
			t.Errorf("type filter registry:ui should exclude pattern items, but found %q", entry["name"])
		}
	}

	// transaction-table is a UI item in the test registry; it should appear in compatible.
	found := findByName(compatible, "transaction-table")
	if found == nil {
		t.Error("expected transaction-table in compatible when filtering for registry:ui")
	}
}

// TestCookbookDiscover_InvalidParams_ReturnsError verifies that malformed JSON params
// return a structured error rather than panicking.
func TestCookbookDiscover_InvalidParams_ReturnsError(t *testing.T) {
	loader := testdataCookbookLoader(t)
	r := tools.NewRegistry()
	tools.RegisterCookbookDiscoverTool(r, loader)

	// The registry validates against the JSON schema before calling the handler.
	// An invalid type value should be caught by schema validation.
	_, err := r.Call(context.Background(), "meridian_cookbook_discover", json.RawMessage(`{"type": "invalid-type"}`))
	if err == nil {
		t.Error("expected schema validation error for invalid type value")
	}
}

// TestCookbookDiscover_EmptyRegistry_ReturnsEmptyBuckets verifies that an empty
// cookbook registry produces empty result buckets without errors.
func TestCookbookDiscover_EmptyRegistry_ReturnsEmptyBuckets(t *testing.T) {
	// Use a temporary directory with an empty registry.
	dir := t.TempDir()
	registryJSON := `{"name": "empty-test", "items": []}`
	if err := os.WriteFile(filepath.Join(dir, "registry.json"), []byte(registryJSON), 0o644); err != nil {
		t.Fatalf("failed to write registry.json: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "patterns"), 0o755); err != nil {
		t.Fatalf("failed to create patterns dir: %v", err)
	}

	loader := tools.NewFSCookbookLoader(os.DirFS(dir))
	m := callDiscover(t, loader, json.RawMessage(`{}`))

	compatible := getSlice(t, m, "compatible")
	incompatible := getSlice(t, m, "incompatible")
	conflicts := getSlice(t, m, "conflicts")

	if len(compatible) != 0 {
		t.Errorf("expected 0 compatible items, got %d", len(compatible))
	}
	if len(incompatible) != 0 {
		t.Errorf("expected 0 incompatible items, got %d", len(incompatible))
	}
	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflict items, got %d", len(conflicts))
	}
}
