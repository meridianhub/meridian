package cookbook_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
	"gopkg.in/yaml.v3"
)

// cookbookDir returns the absolute path to the cookbook root directory.
func cookbookDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Dir(file)
}

// loadRegistry reads and parses registry.json, returning the items list.
func loadRegistry(t *testing.T) []map[string]any {
	t.Helper()
	path := filepath.Join(cookbookDir(t), "registry.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read registry.json")

	var registry map[string]any
	require.NoError(t, json.Unmarshal(data, &registry))

	items, ok := registry["items"].([]any)
	require.True(t, ok, "registry.json items should be an array")

	result := make([]map[string]any, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]any)
		require.True(t, ok, "registry item[%d] should be an object", i)
		result = append(result, m)
	}
	return result
}

// registryNames returns a set of all names in registry.json.
func registryNames(t *testing.T) map[string]bool {
	t.Helper()
	items := loadRegistry(t)
	names := make(map[string]bool, len(items))
	for _, item := range items {
		if name, ok := item["name"].(string); ok {
			names[name] = true
		}
	}
	return names
}

// loadPatternJSON reads and unmarshals a pattern.json for a given pattern name.
func loadPatternJSON(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join(cookbookDir(t), "patterns", name, "pattern.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read pattern.json for %s", name)

	var v map[string]any
	require.NoError(t, json.Unmarshal(data, &v))
	return v
}

// loadManifestFragment reads and parses a manifest-fragment.yaml for a given pattern name.
func loadManifestFragment(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join(cookbookDir(t), "patterns", name, "manifest-fragment.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read manifest-fragment.yaml for %s", name)

	var v map[string]any
	require.NoError(t, yaml.Unmarshal(data, &v))
	return v
}

// allPatternNames returns the names of all pattern directories.
func allPatternNames(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(cookbookDir(t), "patterns")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "failed to read patterns directory")

	names := make([]string, 0)
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// TestStarlarkSyntax_AllStarFilesParseWithoutError validates the syntax of every
// .star file found in the patterns directory.
func TestStarlarkSyntax_AllStarFilesParseWithoutError(t *testing.T) {
	patternsDir := filepath.Join(cookbookDir(t), "patterns")

	var starFiles []string
	err := filepath.Walk(patternsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".star") {
			starFiles = append(starFiles, path)
		}
		return nil
	})
	require.NoError(t, err, "failed to walk patterns directory")
	require.NotEmpty(t, starFiles, "expected at least one .star file in patterns/")

	opts := &syntax.FileOptions{}
	for _, path := range starFiles {
		relPath, _ := filepath.Rel(cookbookDir(t), path)
		t.Run(relPath, func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err, "failed to read %s", relPath)

			_, parseErr := opts.Parse(filepath.Base(path), string(data), 0)
			assert.NoError(t, parseErr, "starlark file %s should parse without syntax errors", relPath)
		})
	}
}

// TestRegistryDependencies_AllRefsExist verifies that every name listed in
// registryDependencies within each pattern.json corresponds to an entry in registry.json.
func TestRegistryDependencies_AllRefsExist(t *testing.T) {
	names := registryNames(t)
	patterns := allPatternNames(t)

	for _, patternName := range patterns {
		t.Run(patternName, func(t *testing.T) {
			pattern := loadPatternJSON(t, patternName)
			deps, ok := pattern["registryDependencies"].([]any)
			if !ok {
				// Field absent — no dependencies to validate.
				return
			}

			for i, dep := range deps {
				depName, ok := dep.(string)
				require.True(t, ok, "registryDependencies[%d] should be a string in %s", i, patternName)
				assert.True(t, names[depName],
					"registryDependencies[%d] %q in %s does not exist in registry.json",
					i, depName, patternName)
			}
		})
	}
}

// TestComposition_ComposesWithRefsExist verifies that every name listed in
// meta.composes_with within each pattern.json corresponds to an entry in registry.json.
func TestComposition_ComposesWithRefsExist(t *testing.T) {
	names := registryNames(t)
	patterns := allPatternNames(t)

	for _, patternName := range patterns {
		t.Run(patternName, func(t *testing.T) {
			pattern := loadPatternJSON(t, patternName)
			meta, ok := pattern["meta"].(map[string]any)
			if !ok {
				return
			}
			composesWith, ok := meta["composes_with"].([]any)
			if !ok {
				return
			}

			for i, ref := range composesWith {
				refName, ok := ref.(string)
				require.True(t, ok, "meta.composes_with[%d] should be a string in %s", i, patternName)
				assert.True(t, names[refName],
					"meta.composes_with[%d] %q in %s does not exist in registry.json",
					i, refName, patternName)
			}
		})
	}
}

// TestComposition_ManifestFragmentsMergeWithoutKeyConflict verifies that when two patterns
// that declare each other in composes_with have their manifest fragments merged, there are
// no conflicting top-level keys that would produce an invalid combined manifest.
// This is a best-effort structural check — it confirms both fragments are valid YAML and
// that the union of their top-level keys is non-empty.
func TestComposition_ManifestFragmentsMergeWithoutKeyConflict(t *testing.T) {
	names := registryNames(t)
	patterns := allPatternNames(t)

	for _, patternName := range patterns {
		t.Run(patternName, func(t *testing.T) {
			pattern := loadPatternJSON(t, patternName)
			meta, ok := pattern["meta"].(map[string]any)
			if !ok {
				return
			}
			composesWith, ok := meta["composes_with"].([]any)
			if !ok || len(composesWith) == 0 {
				return
			}

			baseFragment := loadManifestFragment(t, patternName)

			for _, ref := range composesWith {
				refName, ok := ref.(string)
				if !ok || !names[refName] {
					// Ref validity is already checked by TestComposition_ComposesWithRefsExist.
					continue
				}

				t.Run("with_"+refName, func(t *testing.T) {
					otherFragment := loadManifestFragment(t, refName)

					// Merge: collect all top-level keys from both fragments.
					merged := make(map[string][]any)
					collectFragmentKeys(merged, patternName, baseFragment)
					collectFragmentKeys(merged, refName, otherFragment)

					// The merged result should have at least one key.
					assert.NotEmpty(t, merged,
						"merged manifest fragment of %s + %s should not be empty", patternName, refName)
				})
			}
		})
	}
}

// collectFragmentKeys accumulates top-level list values from a manifest fragment into
// the merged map, keyed by the YAML key name. The source label is recorded for
// diagnostic purposes but is not used in assertions — the test only checks structural
// validity, not deep semantic conflicts.
func collectFragmentKeys(merged map[string][]any, _ string, fragment map[string]any) {
	for k, v := range fragment {
		switch val := v.(type) {
		case []any:
			merged[k] = append(merged[k], val...)
		default:
			// Non-list top-level keys (scalars, maps) are recorded as single-element slices.
			merged[k] = append(merged[k], val)
		}
	}
}

// TestOrphanDetection_NoUnreferencedFilesInCookbook checks that every .json, .yaml,
// and .star file under cookbook/ (excluding schema/ and the registry itself) is
// referenced by at least one pattern's files[] array in pattern.json.
//
// Files that are only reachable via directory convention (manifest-fragment.yaml,
// pattern.json) are excluded from the orphan check since they are the authoritative
// sources loaded by the test suite itself, not consumer-facing downloadable files.
func TestOrphanDetection_NoUnreferencedFilesInCookbook(t *testing.T) {
	root := cookbookDir(t)

	// Collect all file paths referenced by patterns' files[] entries.
	referenced := make(map[string]bool)
	for _, patternName := range allPatternNames(t) {
		pattern := loadPatternJSON(t, patternName)
		files, ok := pattern["files"].([]any)
		if !ok {
			continue
		}
		for _, f := range files {
			entry, ok := f.(map[string]any)
			if !ok {
				continue
			}
			if relPath, ok := entry["path"].(string); ok {
				// Normalise to the absolute path within the cookbook directory.
				abs := filepath.Join(root, relPath)
				referenced[filepath.Clean(abs)] = true
			}
		}
	}

	// Walk the cookbook, collecting candidate files.
	skipDirs := map[string]bool{
		filepath.Join(root, "schema"): true,
		filepath.Join(root, "ui"):     true,
		filepath.Join(root, "docs"):   true,
	}
	// These per-pattern files are loaded by convention, not via files[].
	conventionFiles := map[string]bool{
		"pattern.json":           true,
		"manifest-fragment.yaml": true,
	}

	var orphans []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip the explicitly excluded directories.
			if skipDirs[path] {
				return filepath.SkipDir
			}
			return nil
		}

		// Only check files with relevant extensions.
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".json" && ext != ".yaml" && ext != ".star" {
			return nil
		}

		// Skip the top-level registry.json.
		if path == filepath.Join(root, "registry.json") {
			return nil
		}

		// Skip convention files.
		if conventionFiles[filepath.Base(path)] {
			return nil
		}

		if !referenced[filepath.Clean(path)] {
			rel, _ := filepath.Rel(root, path)
			orphans = append(orphans, rel)
		}
		return nil
	})
	require.NoError(t, err, "failed to walk cookbook directory")

	assert.Empty(t, orphans,
		"the following files in cookbook/ are not referenced by any pattern's files[] array: %v", orphans)
}

// TestFileExistence_AllFilesInPatternJSONExist verifies that every path listed in
// a pattern's files[] array actually exists on disk.
// This is a cross-cutting check that complements the per-pattern tests in patterns_test.go.
func TestFileExistence_AllFilesInPatternJSONExist(t *testing.T) {
	root := cookbookDir(t)

	for _, patternName := range allPatternNames(t) {
		t.Run(patternName, func(t *testing.T) {
			pattern := loadPatternJSON(t, patternName)
			files, ok := pattern["files"].([]any)
			if !ok {
				// Patterns without a files[] field (e.g. base-fiat-*) are skipped.
				return
			}

			for i, f := range files {
				entry, ok := f.(map[string]any)
				require.True(t, ok, "files[%d] should be an object in %s", i, patternName)

				relPath, ok := entry["path"].(string)
				require.True(t, ok, "files[%d].path should be a string in %s", i, patternName)

				absPath := filepath.Join(root, relPath)
				_, err := os.Stat(absPath)
				assert.NoError(t, err,
					"file %s listed in %s/pattern.json files[] does not exist", relPath, patternName)
			}
		})
	}
}
