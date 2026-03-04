// Package tools provides the tool registry for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
)

// itemTypePattern is the registry:pattern item type constant.
const itemTypePattern = "registry:pattern"

// cookbookRegistryItem is a summary entry from cookbook/registry.json.
type cookbookRegistryItem struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Title      string   `json:"title"`
	Categories []string `json:"categories,omitempty"`
}

// cookbookRegistry is the top-level structure of cookbook/registry.json.
type cookbookRegistry struct {
	Name  string                 `json:"name"`
	Items []cookbookRegistryItem `json:"items"`
}

// RegisterCookbookTools registers the meridian_cookbook_list and meridian_cookbook_describe
// tools into the registry. cookbookFS must expose the cookbook directory tree, rooted such that
// "registry.json", "ui/<name>/component.json" and "patterns/<name>/pattern.json" are resolvable.
//
// If cookbookFS is nil the tools are silently skipped.
func RegisterCookbookTools(registry *Registry, cookbookFS fs.FS) {
	if cookbookFS == nil {
		return
	}

	tools := []Tool{
		buildCookbookListTool(cookbookFS),
		buildCookbookDescribeTool(cookbookFS),
	}
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			panic(fmt.Sprintf("failed to register cookbook tool %q: %v", t.Name, err))
		}
	}
}

// buildCookbookListTool returns the meridian_cookbook_list tool.
func buildCookbookListTool(cookbookFS fs.FS) Tool {
	return Tool{
		Name:     "meridian_cookbook_list",
		Category: CategoryRead,
		Description: "List available Meridian Cookbook entries. " +
			"Returns registry summary items filtered by type, category, and industry. " +
			"Use this to discover UI components (registry:ui) and business patterns (registry:pattern).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"type": map[string]interface{}{
					"type":        "string",
					"enum":        []interface{}{"registry:ui", "registry:pattern"},
					"description": "Filter by item type: UI component or business pattern.",
				},
				"category": map[string]interface{}{
					"type":        "string",
					"description": "Filter by category tag (e.g. \"payments\", \"energy\"). Case-insensitive substring match.",
				},
				"industry": map[string]interface{}{
					"type":        "string",
					"description": "Filter patterns by industry (meta.industries). Only applies to registry:pattern items.",
				},
			},
		},
		Handler: func(_ context.Context, params json.RawMessage) (interface{}, error) {
			return handleCookbookList(cookbookFS, params)
		},
	}
}

// cookbookListParams holds parsed parameters for meridian_cookbook_list.
type cookbookListParams struct {
	Type     string `json:"type"`
	Category string `json:"category"`
	Industry string `json:"industry"`
}

// handleCookbookList implements the meridian_cookbook_list handler logic.
func handleCookbookList(cookbookFS fs.FS, params json.RawMessage) (interface{}, error) {
	var p cookbookListParams
	if err := json.Unmarshal(params, &p); err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("invalid parameters: %s", err.Error()),
		}, nil
	}

	reg, err := loadCookbookRegistry(cookbookFS)
	if err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("failed to load cookbook registry: %s", err.Error()),
		}, nil
	}

	result := make([]map[string]interface{}, 0, len(reg.Items))
	for _, item := range reg.Items {
		if !itemPassesFilters(cookbookFS, item, p) {
			continue
		}
		entry := map[string]interface{}{
			"name":  item.Name,
			"type":  item.Type,
			"title": item.Title,
		}
		if len(item.Categories) > 0 {
			entry["categories"] = item.Categories
		}
		result = append(result, entry)
	}

	return map[string]interface{}{
		"count": len(result),
		"items": result,
	}, nil
}

// itemPassesFilters returns true when item satisfies all active filters in p.
// All active filters must match (AND logic).
func itemPassesFilters(cookbookFS fs.FS, item cookbookRegistryItem, p cookbookListParams) bool {
	if p.Type != "" && item.Type != p.Type {
		return false
	}
	if p.Category != "" && !containsCategory(item.Categories, p.Category) {
		return false
	}
	if p.Industry != "" {
		// Industry filter only applies to patterns; UI components have no industries.
		if item.Type != itemTypePattern {
			return false
		}
		match, err := itemMatchesIndustry(cookbookFS, item.Name, p.Industry)
		if err != nil || !match {
			return false
		}
	}
	return true
}

// buildCookbookDescribeTool returns the meridian_cookbook_describe tool.
func buildCookbookDescribeTool(cookbookFS fs.FS) Tool {
	return Tool{
		Name:     "meridian_cookbook_describe",
		Category: CategoryRead,
		Description: "Return full details for a specific Meridian Cookbook entry. " +
			"Loads the complete item definition including metadata and optionally file contents. " +
			"Use this to retrieve a pattern's manifest fragment or a UI component's configuration.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "The kebab-case name of the cookbook entry (e.g. \"current-account\").",
				},
				"include_files": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether to include file contents in the response (default true).",
				},
			},
			"required": []interface{}{"name"},
		},
		Handler: func(_ context.Context, params json.RawMessage) (interface{}, error) {
			return handleCookbookDescribe(cookbookFS, params)
		},
	}
}

// cookbookDescribeParams holds parsed parameters for meridian_cookbook_describe.
type cookbookDescribeParams struct {
	Name         string `json:"name"`
	IncludeFiles *bool  `json:"include_files"`
}

// handleCookbookDescribe implements the meridian_cookbook_describe handler logic.
func handleCookbookDescribe(cookbookFS fs.FS, params json.RawMessage) (interface{}, error) {
	var p cookbookDescribeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("invalid parameters: %s", err.Error()),
		}, nil
	}

	// Resolve the item entry from the registry to determine its type.
	reg, err := loadCookbookRegistry(cookbookFS)
	if err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("failed to load cookbook registry: %s", err.Error()),
		}, nil
	}

	var registryEntry *cookbookRegistryItem
	for i := range reg.Items {
		if reg.Items[i].Name == p.Name {
			registryEntry = &reg.Items[i]
			break
		}
	}
	if registryEntry == nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("cookbook entry %q not found", p.Name),
		}, nil
	}

	// Determine detail file path based on type.
	detailPath := cookbookDetailPath(registryEntry.Type, p.Name)
	data, err := fs.ReadFile(cookbookFS, detailPath)
	if err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("failed to read cookbook entry %q: %s", p.Name, err.Error()),
		}, nil
	}

	var detail map[string]interface{}
	if err := json.Unmarshal(data, &detail); err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("failed to parse cookbook entry %q: %s", p.Name, err.Error()),
		}, nil
	}

	// Include file contents by default.
	includeFiles := true
	if p.IncludeFiles != nil {
		includeFiles = *p.IncludeFiles
	}

	if !includeFiles {
		// Strip file content fields but keep file metadata.
		stripFileContents(detail)
	} else {
		// Embed actual file contents into the "files" array.
		if err := embedFileContents(cookbookFS, registryEntry.Type, p.Name, detail); err != nil {
			// Non-fatal: return metadata without contents and note the error.
			detail["files_error"] = err.Error()
		}
	}

	return detail, nil
}

// loadCookbookRegistry reads and parses the cookbook registry.json from the given fs.
func loadCookbookRegistry(cookbookFS fs.FS) (*cookbookRegistry, error) {
	data, err := fs.ReadFile(cookbookFS, "registry.json")
	if err != nil {
		return nil, fmt.Errorf("read registry.json: %w", err)
	}
	var reg cookbookRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse registry.json: %w", err)
	}
	return &reg, nil
}

// cookbookDetailPath returns the path to the full detail file for a named item.
func cookbookDetailPath(itemType, name string) string {
	if itemType == itemTypePattern {
		return fmt.Sprintf("patterns/%s/pattern.json", name)
	}
	return fmt.Sprintf("ui/%s/component.json", name)
}

// containsCategory reports whether categories contains a case-insensitive match for target.
func containsCategory(categories []string, target string) bool {
	targetLower := toLower(target)
	for _, c := range categories {
		if toLower(c) == targetLower {
			return true
		}
	}
	return false
}

// itemMatchesIndustry loads the full pattern detail and checks meta.industries.
func itemMatchesIndustry(cookbookFS fs.FS, name, industry string) (bool, error) {
	data, err := fs.ReadFile(cookbookFS, fmt.Sprintf("patterns/%s/pattern.json", name))
	if err != nil {
		return false, err
	}
	var detail struct {
		Meta struct {
			Industries []string `json:"industries"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(data, &detail); err != nil {
		return false, err
	}
	industryLower := toLower(industry)
	for _, ind := range detail.Meta.Industries {
		if toLower(ind) == industryLower {
			return true, nil
		}
	}
	return false, nil
}

// stripFileContents removes the "content" field from each entry in the "files" array,
// preserving path, type, and target metadata.
func stripFileContents(detail map[string]interface{}) {
	files, ok := detail["files"].([]interface{})
	if !ok {
		return
	}
	for _, f := range files {
		if fm, ok := f.(map[string]interface{}); ok {
			delete(fm, "content")
		}
	}
}

// embedFileContents reads the content of each file listed in detail["files"] from the filesystem
// and injects it as a "content" string field.
func embedFileContents(cookbookFS fs.FS, itemType, name string, detail map[string]interface{}) error {
	files, ok := detail["files"].([]interface{})
	if !ok {
		return nil
	}

	// Determine the base directory for relative file paths.
	var baseDir string
	if itemType == itemTypePattern {
		baseDir = fmt.Sprintf("patterns/%s", name)
	} else {
		baseDir = fmt.Sprintf("ui/%s", name)
	}

	for _, f := range files {
		fm, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		pathVal, ok := fm["path"].(string)
		if !ok || pathVal == "" {
			continue
		}
		filePath := fmt.Sprintf("%s/%s", baseDir, pathVal)
		data, err := fs.ReadFile(cookbookFS, filePath)
		if err != nil {
			// Best effort: note missing files but continue.
			fm["content_error"] = fmt.Sprintf("could not read %q: %s", filePath, err.Error())
			continue
		}
		fm["content"] = string(data)
	}
	return nil
}

// toLower returns a lowercase copy of s without importing strings in the function body.
func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}
