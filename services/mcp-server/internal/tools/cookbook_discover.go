// Package tools provides the tool registry for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
)

// CookbookLoader abstracts loading of cookbook registry and pattern files.
// Implementations can use the embedded filesystem or a test fixture directory.
type CookbookLoader interface {
	// LoadRegistry returns the parsed registry index (cookbook/registry.json).
	LoadRegistry() (*cookbookRegistry, error)
	// LoadPattern returns the parsed full pattern detail for the given pattern name.
	// Returns nil, nil when the pattern file does not exist.
	LoadPattern(name string) (*cookbookPatternDetail, error)
}

// fsCookbookLoader loads cookbook data from an fs.FS rooted at the cookbook directory.
type fsCookbookLoader struct {
	cookbookFS fs.FS
}

// NewFSCookbookLoader returns a CookbookLoader that reads from the given fs.FS.
// The fs.FS must have registry.json at its root and pattern files under patterns/<name>/pattern.json.
func NewFSCookbookLoader(cookbookFS fs.FS) CookbookLoader {
	return &fsCookbookLoader{cookbookFS: cookbookFS}
}

func (l *fsCookbookLoader) LoadRegistry() (*cookbookRegistry, error) {
	return loadCookbookRegistry(l.cookbookFS)
}

func (l *fsCookbookLoader) LoadPattern(name string) (*cookbookPatternDetail, error) {
	path := fmt.Sprintf("patterns/%s/pattern.json", name)
	data, err := fs.ReadFile(l.cookbookFS, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Pattern file does not exist — not an error, item may be registry:ui only.
			return nil, nil //nolint:nilnil // absence of file is intentional; caller checks nil
		}
		return nil, fmt.Errorf("read pattern %q: %w", name, err)
	}
	var detail cookbookPatternDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		return nil, fmt.Errorf("parse pattern %q: %w", name, err)
	}
	return &detail, nil
}

// cookbookPatternDetail is the full content of a patterns/<name>/pattern.json file.
// cookbookRegistryItem (the index summary) is defined in cookbook.go.
type cookbookPatternDetail struct {
	Name        string               `json:"name"`
	Type        string               `json:"type"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Categories  []string             `json:"categories,omitempty"`
	Meta        *cookbookPatternMeta `json:"meta,omitempty"`
}

// cookbookPatternMeta holds the meta block for a registry:pattern item.
type cookbookPatternMeta struct {
	Complexity    int                      `json:"complexity"`
	DesignPattern *string                  `json:"design_pattern,omitempty"`
	Industries    []string                 `json:"industries,omitempty"`
	Provides      *cookbookPatternProvides `json:"provides,omitempty"`
	Requires      *cookbookPatternRequires `json:"requires,omitempty"`
	ComposesWith  []string                 `json:"composes_with,omitempty"`
	ConflictsWith []string                 `json:"conflicts_with,omitempty"`
	Extends       []string                 `json:"extends,omitempty"`
}

// cookbookPatternProvides lists manifest components a pattern contributes.
type cookbookPatternProvides struct {
	Instruments    []string `json:"instruments,omitempty"`
	AccountTypes   []string `json:"account_types,omitempty"`
	Sagas          []string `json:"sagas,omitempty"`
	ValuationRules []string `json:"valuation_rules,omitempty"`
	Triggers       []string `json:"triggers,omitempty"`
}

// cookbookPatternRequires lists external prerequisites a pattern depends on.
type cookbookPatternRequires struct {
	Instruments []string `json:"instruments,omitempty"`
	MarketData  []string `json:"market_data,omitempty"`
}

// manifestState holds the extracted economy state parsed from a manifest JSON object.
// It is used for compatibility checks without requiring a live gRPC connection.
type manifestState struct {
	// instrumentCodes is the set of instrument codes present in the manifest.
	instrumentCodes map[string]bool
}

// parseManifestState extracts compatibility-relevant state from a raw manifest JSON object.
// Accepts nil to represent an empty/unconfigured manifest.
func parseManifestState(manifestJSON json.RawMessage) *manifestState {
	state := &manifestState{
		instrumentCodes: make(map[string]bool),
	}
	if len(manifestJSON) == 0 {
		return state
	}

	// We only need the instruments list for compatibility checks.
	// Use a minimal struct to avoid pulling in proto dependencies.
	var raw struct {
		Instruments []struct {
			Code string `json:"code"`
		} `json:"instruments"`
	}
	if err := json.Unmarshal(manifestJSON, &raw); err != nil {
		// If we can't parse, treat as empty — caller gets conservative results.
		return state
	}
	for _, inst := range raw.Instruments {
		if inst.Code != "" {
			state.instrumentCodes[inst.Code] = true
		}
	}
	return state
}

// discoverResult is the full response returned by meridian_cookbook_discover.
type discoverResult struct {
	Compatible   []compatibleEntry   `json:"compatible"`
	Incompatible []incompatibleEntry `json:"incompatible"`
	Conflicts    []conflictEntry     `json:"conflicts"`
}

// compatibleEntry describes a pattern that is compatible with the current manifest.
type compatibleEntry struct {
	Name       string                 `json:"name"`
	Title      string                 `json:"title"`
	Type       string                 `json:"type"`
	Reason     string                 `json:"reason"`
	Complexity int                    `json:"complexity,omitempty"`
	Categories []string               `json:"categories,omitempty"`
	Links      map[string]interface{} `json:"_links"`
}

// incompatibleEntry describes a pattern that cannot be applied yet due to missing prerequisites.
type incompatibleEntry struct {
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	Type    string   `json:"type"`
	Reason  string   `json:"reason"`
	Missing []string `json:"missing,omitempty"`
}

// conflictEntry describes a pattern that conflicts with an already-installed pattern.
type conflictEntry struct {
	Name            string `json:"name"`
	Title           string `json:"title"`
	Type            string `json:"type"`
	Reason          string `json:"reason"`
	ConflictingWith string `json:"conflicting_with"`
}

// buildHATEOASLinks constructs the navigation links for a compatible pattern.
func buildHATEOASLinks(name string) map[string]interface{} {
	return map[string]interface{}{
		"detail": map[string]interface{}{
			"tool":   "meridian_cookbook_describe",
			"params": map[string]interface{}{"name": name},
		},
		"compose": map[string]interface{}{
			"tool":        "meridian_manifest_plan",
			"description": "Plan manifest with this pattern",
		},
		"validate": map[string]interface{}{
			"tool":        "meridian_manifest_validate",
			"description": "Validate composed manifest",
		},
	}
}

// checkCompatibility evaluates a registry item against the current manifest state.
// Returns (compatible, incompatible, conflict). Exactly one bucket will receive the item.
func checkCompatibility(
	entry cookbookRegistryItem,
	detail *cookbookPatternDetail,
	state *manifestState,
	installedNames map[string]bool,
) (comp *compatibleEntry, incompat *incompatibleEntry, conflict *conflictEntry) {
	// For UI components, meta is feature-module-based; no instrument requirements.
	// They are always considered compatible unless explicitly conflicting.
	if detail == nil || entry.Type == "registry:ui" {
		return &compatibleEntry{
			Name:       entry.Name,
			Title:      entry.Title,
			Type:       entry.Type,
			Reason:     "UI component; no manifest prerequisites required",
			Categories: entry.Categories,
			Links:      buildHATEOASLinks(entry.Name),
		}, nil, nil
	}

	meta := detail.Meta

	// Check conflicts first: if this pattern conflicts with an installed one, skip.
	if meta != nil {
		for _, conflictName := range meta.ConflictsWith {
			if installedNames[conflictName] {
				return nil, nil, &conflictEntry{
					Name:            detail.Name,
					Title:           detail.Title,
					Type:            detail.Type,
					Reason:          fmt.Sprintf("conflicts with installed pattern %q", conflictName),
					ConflictingWith: conflictName,
				}
			}
		}
	}

	// Check required instruments.
	if meta != nil && meta.Requires != nil {
		var missing []string
		for _, code := range meta.Requires.Instruments {
			if !state.instrumentCodes[code] {
				missing = append(missing, code)
			}
		}
		if len(missing) > 0 {
			reason := fmt.Sprintf("missing required instruments: %v", missing)
			return nil, &incompatibleEntry{
				Name:    detail.Name,
				Title:   detail.Title,
				Type:    detail.Type,
				Reason:  reason,
				Missing: missing,
			}, nil
		}
	}

	// Pattern is compatible.
	reason := "no prerequisites required"
	if meta != nil && meta.Requires != nil && len(meta.Requires.Instruments) > 0 {
		reason = fmt.Sprintf("all required instruments (%v) present", meta.Requires.Instruments)
	}

	complexity := 0
	if meta != nil {
		complexity = meta.Complexity
	}

	return &compatibleEntry{
		Name:       detail.Name,
		Title:      detail.Title,
		Type:       detail.Type,
		Reason:     reason,
		Complexity: complexity,
		Categories: detail.Categories,
		Links:      buildHATEOASLinks(detail.Name),
	}, nil, nil
}

// cookbookDiscoverParams holds the parsed input parameters for the discover tool.
type cookbookDiscoverParams struct {
	Manifest json.RawMessage `json:"manifest"`
	Type     string          `json:"type"`
}

// handleCookbookDiscover implements the meridian_cookbook_discover handler.
func handleCookbookDiscover(_ context.Context, loader CookbookLoader, params json.RawMessage) (interface{}, error) {
	var p cookbookDiscoverParams
	if err := json.Unmarshal(params, &p); err != nil {
		return formatError("invalid parameters: " + err.Error()), nil //nolint:nilerr // tool errors are returned in the result
	}

	reg, err := loader.LoadRegistry()
	if err != nil {
		return formatError("failed to load cookbook registry: " + err.Error()), nil //nolint:nilerr // tool errors are returned in the result
	}

	state := parseManifestState(p.Manifest)

	// Load all pattern details upfront so we can do cross-pattern conflict detection.
	// Patterns that fail to load are skipped gracefully.
	allDetails := make(map[string]*cookbookPatternDetail, len(reg.Items))
	for _, entry := range reg.Items {
		detail, loadErr := loader.LoadPattern(entry.Name)
		if loadErr != nil {
			continue
		}
		allDetails[entry.Name] = detail // may be nil for UI items with no pattern.json
	}

	// Determine installed patterns from the manifest state.
	// A pattern is considered "installed" if all instruments it provides are present in the manifest.
	// This is a heuristic for v1 — future versions may track installation state explicitly.
	installedNames := buildInstalledPatternSet(reg.Items, allDetails, state)

	result := discoverResult{
		Compatible:   []compatibleEntry{},
		Incompatible: []incompatibleEntry{},
		Conflicts:    []conflictEntry{},
	}

	for _, entry := range reg.Items {
		// Apply type filter.
		if p.Type != "" && entry.Type != p.Type {
			continue
		}

		detail := allDetails[entry.Name] // nil for UI items or failed loads

		comp, incompat, conflict := checkCompatibility(entry, detail, state, installedNames)
		switch {
		case comp != nil:
			result.Compatible = append(result.Compatible, *comp)
		case incompat != nil:
			result.Incompatible = append(result.Incompatible, *incompat)
		case conflict != nil:
			result.Conflicts = append(result.Conflicts, *conflict)
		}
	}

	return result, nil
}

// buildInstalledPatternSet determines which patterns are likely already installed in the manifest.
// A pattern is considered installed if all instruments it provides are present in the manifest.
// This is a heuristic: it assumes that if a pattern's instruments are present, the pattern was applied.
func buildInstalledPatternSet(items []cookbookRegistryItem, allDetails map[string]*cookbookPatternDetail, state *manifestState) map[string]bool {
	installed := make(map[string]bool)
	for _, item := range items {
		if item.Type != itemTypePattern {
			continue
		}
		detail := allDetails[item.Name]
		if detail == nil || detail.Meta == nil || detail.Meta.Provides == nil {
			continue
		}
		provided := detail.Meta.Provides.Instruments
		if len(provided) == 0 {
			continue
		}
		allPresent := true
		for _, code := range provided {
			if !state.instrumentCodes[code] {
				allPresent = false
				break
			}
		}
		if allPresent {
			installed[item.Name] = true
		}
	}
	return installed
}

// buildCookbookDiscoverTool returns the meridian_cookbook_discover Tool.
func buildCookbookDiscoverTool(loader CookbookLoader) Tool {
	return Tool{
		Name:     "meridian_cookbook_discover",
		Category: CategoryRead,
		Description: "Inspect a tenant manifest and return compatible cookbook patterns with HATEOAS navigation links. " +
			"Checks pattern prerequisites (required instruments) and conflicts against the provided manifest. " +
			"Use this to discover which patterns can be applied to the current economy configuration.",
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"manifest": map[string]interface{}{
					"type":        "object",
					"description": "Current manifest to analyze. If omitted, all patterns with no requirements are shown as compatible.",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"description": "Optional filter: \"registry:ui\" or \"registry:pattern\".",
					"enum":        []interface{}{"registry:ui", "registry:pattern"},
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleCookbookDiscover(ctx, loader, params)
		},
	}
}

// RegisterCookbookDiscoverTool registers the meridian_cookbook_discover tool into the registry.
// loader provides access to the cookbook registry and pattern files.
func RegisterCookbookDiscoverTool(registry *Registry, loader CookbookLoader) {
	t := buildCookbookDiscoverTool(loader)
	if err := registry.Register(t); err != nil {
		panic(fmt.Sprintf("failed to register cookbook_discover tool: %v", err))
	}
}
