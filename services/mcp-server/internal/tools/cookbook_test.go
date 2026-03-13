package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// newCookbookRegistry creates a test Registry with cookbook tools registered.
func newCookbookRegistry(t *testing.T) *testServer {
	t.Helper()
	r := newTestServer(t)
	fsys := os.DirFS("testdata/cookbook")
	tools.RegisterCookbookTools(r.Server(), fsys)
	return r
}

// callCookbookTool calls the given tool with JSON params and returns the result.
// The result is round-tripped through JSON to normalise types (e.g. int → float64,
// []map[...]... → []interface{}) matching what an MCP client would see.
func callCookbookTool(t *testing.T, r *testServer, name string, params string) map[string]interface{} {
	t.Helper()
	raw, err := r.Call(context.Background(), name, json.RawMessage(params))
	if err != nil {
		t.Fatalf("tool %q call failed: %v", name, err)
	}
	// Round-trip through JSON to normalise types.
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return m
}

// --- meridian_cookbook_list tests ---

func TestCookbookList_NoFilter_ReturnsAllEntries(t *testing.T) {
	r := newCookbookRegistry(t)
	m := callCookbookTool(t, r, "meridian_cookbook_list", `{}`)

	count, ok := m["count"].(float64)
	if !ok {
		t.Fatalf("expected count field, got %v", m)
	}
	if count != 3 {
		t.Errorf("expected count=3, got %v", count)
	}
	items, ok := m["items"].([]interface{})
	if !ok || len(items) == 0 {
		t.Fatalf("expected non-empty items slice, got %T: %v", m["items"], m["items"])
	}
}

func TestCookbookList_TypeFilterUI_ReturnsOnlyUI(t *testing.T) {
	r := newCookbookRegistry(t)
	m := callCookbookTool(t, r, "meridian_cookbook_list", `{"type": "registry:ui"}`)

	items := m["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 UI item, got %d: %v", len(items), items)
	}
	item := items[0].(map[string]interface{})
	if item["type"] != "registry:ui" {
		t.Errorf("expected type=registry:ui, got %v", item["type"])
	}
	if item["name"] != "account-balance" {
		t.Errorf("expected name=account-balance, got %v", item["name"])
	}
}

func TestCookbookList_TypeFilterPattern_ReturnsOnlyPatterns(t *testing.T) {
	r := newCookbookRegistry(t)
	m := callCookbookTool(t, r, "meridian_cookbook_list", `{"type": "registry:pattern"}`)

	items := m["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 pattern items, got %d", len(items))
	}
	for _, v := range items {
		item := v.(map[string]interface{})
		if item["type"] != "registry:pattern" {
			t.Errorf("expected type=registry:pattern, got %v", item["type"])
		}
	}
}

func TestCookbookList_CategoryFilter_ReturnsMatchingItems(t *testing.T) {
	r := newCookbookRegistry(t)
	m := callCookbookTool(t, r, "meridian_cookbook_list", `{"category": "banking"}`)

	items := m["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 banking item, got %d", len(items))
	}
	item := items[0].(map[string]interface{})
	if item["name"] != "current-account" {
		t.Errorf("expected name=current-account, got %v", item["name"])
	}
}

func TestCookbookList_IndustryFilter_ReturnsMatchingPatterns(t *testing.T) {
	r := newCookbookRegistry(t)
	m := callCookbookTool(t, r, "meridian_cookbook_list", `{"industry": "energy"}`)

	items := m["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 energy pattern, got %d", len(items))
	}
	item := items[0].(map[string]interface{})
	if item["name"] != "energy-settlement" {
		t.Errorf("expected name=energy-settlement, got %v", item["name"])
	}
}

func TestCookbookList_IndustryFilter_ExcludesUIItems(t *testing.T) {
	r := newCookbookRegistry(t)
	// UI items don't have industries — even without type filter, they should be excluded.
	m := callCookbookTool(t, r, "meridian_cookbook_list", `{"industry": "banking"}`)

	items := m["items"].([]interface{})
	for _, v := range items {
		item := v.(map[string]interface{})
		if item["type"] == "registry:ui" {
			t.Errorf("unexpected UI item %v when filtering by industry", item["name"])
		}
	}
}

func TestCookbookList_TypeAndCategoryFilter_ANDLogic(t *testing.T) {
	r := newCookbookRegistry(t)
	// registry:pattern AND category=banking → only current-account
	m := callCookbookTool(t, r, "meridian_cookbook_list", `{"type": "registry:pattern", "category": "banking"}`)

	items := m["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 result, got %d", len(items))
	}
	item := items[0].(map[string]interface{})
	if item["name"] != "current-account" {
		t.Errorf("expected current-account, got %v", item["name"])
	}
}

func TestCookbookList_NonexistentCategory_ReturnsEmpty(t *testing.T) {
	r := newCookbookRegistry(t)
	m := callCookbookTool(t, r, "meridian_cookbook_list", `{"category": "nonexistent-xyz"}`)

	items, ok := m["items"].([]interface{})
	if !ok {
		t.Fatalf("expected items field")
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for nonexistent category, got %d", len(items))
	}
	if count := m["count"].(float64); count != 0 {
		t.Errorf("expected count=0, got %v", count)
	}
}

// --- meridian_cookbook_describe tests ---

func TestCookbookDescribe_UIComponent_ReturnsFullMetadata(t *testing.T) {
	r := newCookbookRegistry(t)
	m := callCookbookTool(t, r, "meridian_cookbook_describe", `{"name": "account-balance"}`)

	if m["name"] != "account-balance" {
		t.Errorf("expected name=account-balance, got %v", m["name"])
	}
	if m["type"] != "registry:ui" {
		t.Errorf("expected type=registry:ui, got %v", m["type"])
	}
	if m["description"] == nil || m["description"] == "" {
		t.Error("expected non-empty description")
	}
	meta, ok := m["meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected meta field, got %T", m["meta"])
	}
	if meta["feature_module"] == nil {
		t.Error("expected feature_module in meta")
	}
}

func TestCookbookDescribe_Pattern_ReturnsMetadataWithFiles(t *testing.T) {
	r := newCookbookRegistry(t)
	m := callCookbookTool(t, r, "meridian_cookbook_describe", `{"name": "current-account"}`)

	if m["name"] != "current-account" {
		t.Errorf("expected name=current-account, got %v", m["name"])
	}

	files, ok := m["files"].([]interface{})
	if !ok || len(files) == 0 {
		t.Fatalf("expected files slice, got %T: %v", m["files"], m["files"])
	}

	// With include_files default (true), file content should be embedded.
	file := files[0].(map[string]interface{})
	if file["content"] == nil {
		t.Error("expected file content to be embedded by default")
	}
	content, ok := file["content"].(string)
	if !ok || content == "" {
		t.Error("expected non-empty file content string")
	}
}

func TestCookbookDescribe_IncludeFilesFalse_OmitsContent(t *testing.T) {
	r := newCookbookRegistry(t)
	m := callCookbookTool(t, r, "meridian_cookbook_describe", `{"name": "current-account", "include_files": false}`)

	files, ok := m["files"].([]interface{})
	if !ok || len(files) == 0 {
		t.Fatalf("expected files slice")
	}

	file := files[0].(map[string]interface{})
	if _, hasContent := file["content"]; hasContent {
		t.Error("expected file content to be absent when include_files=false")
	}
	// Path metadata should still be present.
	if file["path"] == nil {
		t.Error("expected path field even when content is omitted")
	}
}

func TestCookbookDescribe_NonexistentEntry_ReturnsError(t *testing.T) {
	r := newCookbookRegistry(t)
	m := callCookbookTool(t, r, "meridian_cookbook_describe", `{"name": "does-not-exist"}`)

	if m["error"] == nil {
		t.Error("expected error field for nonexistent entry")
	}
}

// --- Registration tests ---

func TestCookbookTools_Registered(t *testing.T) {
	r := newCookbookRegistry(t)

	listed := r.List(context.Background())
	names := make(map[string]bool)
	for _, tool := range listed {
		names[tool.Name] = true
	}

	required := []string{
		"meridian_cookbook_list",
		"meridian_cookbook_describe",
	}
	for _, name := range required {
		if !names[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestCookbookTools_NilFS_SkipsRegistration(t *testing.T) {
	r := newTestServer(t)
	tools.RegisterCookbookTools(r.Server(), nil)

	if len(r.List(context.Background())) != 0 {
		t.Error("expected no tools registered when cookbookFS is nil")
	}
}

func TestCookbookTools_CorrectCategory(t *testing.T) {
	r := newCookbookRegistry(t)

	for _, tool := range r.List(context.Background()) {
		if tool.Category != tools.CategoryRead {
			t.Errorf("tool %q: expected CategoryRead, got %v", tool.Name, tool.Category)
		}
	}
}
