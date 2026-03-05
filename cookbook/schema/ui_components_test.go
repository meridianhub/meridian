package schema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uiComponentNames lists every UI component entry added by tasks 4 and 5.
var uiComponentNames = []string{
	// Task 4: shared components
	"quality-ladder-badge",
	"cel-editor",
	"starlark-editor",
	"saga-timeline",
	"create-valuation-feature-dialog",
	// Task 5: feature components
	"direction-badge",
	"balance-indicator",
	"stat-card",
	"quick-actions",
	"activity-feed",
}

// repoRoot returns the absolute path to the repository root, two levels above the schema/ dir.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir := schemaDir(t)
	// schema/ → cookbook/ → repo root
	return filepath.Join(dir, "..", "..")
}

// cookbookDir returns the absolute path to the cookbook/ directory.
func cookbookDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "cookbook")
}

// TestUIComponentEntry_SchemaValid validates each component.json against registry-item.json.
func TestUIComponentEntry_SchemaValid(t *testing.T) {
	s := compileSchema(t, "registry-item.json")

	for _, name := range uiComponentNames {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(cookbookDir(t), "ui", name, "component.json")
			data, err := os.ReadFile(path)
			require.NoError(t, err, "component.json missing for %s", name)

			var v any
			require.NoError(t, json.Unmarshal(data, &v))

			err = s.Validate(v)
			assert.NoError(t, err, "component.json for %s is invalid", name)
		})
	}
}

// TestUIComponentEntry_FilesExist verifies that every path listed in files[] actually exists.
func TestUIComponentEntry_FilesExist(t *testing.T) {
	root := repoRoot(t)

	for _, name := range uiComponentNames {
		t.Run(name, func(t *testing.T) {
			componentPath := filepath.Join(cookbookDir(t), "ui", name, "component.json")
			data, err := os.ReadFile(componentPath)
			require.NoError(t, err)

			var item struct {
				Files []struct {
					Path string `json:"path"`
				} `json:"files"`
			}
			require.NoError(t, json.Unmarshal(data, &item))

			require.NotEmpty(t, item.Files, "component.json for %s has no files", name)

			for _, f := range item.Files {
				// Reject absolute or escaping paths before joining with root.
				assert.False(t, filepath.IsAbs(f.Path),
					"file path %q in %s/component.json must be relative", f.Path, name)
				assert.False(t, strings.HasPrefix(filepath.Clean(f.Path), ".."),
					"file path %q in %s/component.json must not escape the repo root", f.Path, name)

				fullPath := filepath.Join(root, f.Path)
				_, statErr := os.Stat(fullPath)
				assert.NoError(t, statErr, "file %s listed in %s/component.json does not exist", f.Path, name)
			}
		})
	}
}

// TestUIComponentEntry_NameMatchesDirectory checks that the name field equals the directory name.
func TestUIComponentEntry_NameMatchesDirectory(t *testing.T) {
	for _, name := range uiComponentNames {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(cookbookDir(t), "ui", name, "component.json")
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			var item struct {
				Name string `json:"name"`
			}
			require.NoError(t, json.Unmarshal(data, &item))

			assert.Equal(t, name, item.Name, "name field should match directory name for %s", name)
		})
	}
}

// TestUIComponentEntry_TypeIsRegistryUI verifies all entries declare type registry:ui.
func TestUIComponentEntry_TypeIsRegistryUI(t *testing.T) {
	for _, name := range uiComponentNames {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(cookbookDir(t), "ui", name, "component.json")
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			var item struct {
				Type string `json:"type"`
			}
			require.NoError(t, json.Unmarshal(data, &item))

			assert.Equal(t, "registry:ui", item.Type, "%s should have type registry:ui", name)
		})
	}
}

// TestRegistryJSON_ContainsAllUIComponents checks that registry.json lists every component.
func TestRegistryJSON_ContainsAllUIComponents(t *testing.T) {
	registryPath := filepath.Join(cookbookDir(t), "registry.json")
	data, err := os.ReadFile(registryPath)
	require.NoError(t, err)

	var registry struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(data, &registry))

	indexed := make(map[string]bool, len(registry.Items))
	for _, item := range registry.Items {
		indexed[item.Name] = true
	}

	for _, name := range uiComponentNames {
		assert.True(t, indexed[name], "registry.json is missing entry for %s", name)
	}
}

// TestRegistryDependencies_ReferenceValidEntries checks that registryDependencies name
// entries that exist either in the registry or as a known UI component.
func TestRegistryDependencies_ReferenceValidEntries(t *testing.T) {
	// Build a set of all known entry names from registry.json.
	registryPath := filepath.Join(cookbookDir(t), "registry.json")
	data, err := os.ReadFile(registryPath)
	require.NoError(t, err)

	var registry struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(data, &registry))

	known := make(map[string]bool, len(registry.Items))
	for _, item := range registry.Items {
		known[item.Name] = true
	}

	for _, name := range uiComponentNames {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(cookbookDir(t), "ui", name, "component.json")
			componentData, readErr := os.ReadFile(path)
			require.NoError(t, readErr)

			var item struct {
				RegistryDependencies []string `json:"registryDependencies"`
			}
			require.NoError(t, json.Unmarshal(componentData, &item))

			for _, dep := range item.RegistryDependencies {
				assert.True(t, known[dep],
					"registryDependency %q in %s not found in registry.json", dep, name)
			}
		})
	}
}
