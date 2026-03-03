package examples

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

// examplesDir returns the directory containing the example .star files.
func examplesDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get caller info")
	return filepath.Dir(filename)
}

// starlarkFileOptions matches the saga runtime's parser configuration:
// Set=true, While=false (no while loops), GlobalReassign=true, Recursion=false.
var starlarkFileOptions = &syntax.FileOptions{
	Set:            true,
	While:          false,
	GlobalReassign: true,
	Recursion:      false,
}

func TestExampleSagas_SyntaxValid(t *testing.T) {
	dir := examplesDir(t)

	examples := []struct {
		name     string
		filename string
	}{
		{"usage_to_value", "usage_to_value.star"},
		{"compute_billing", "compute_billing.star"},
		{"race_result_distribution", "race_result_distribution.star"},
		{"corporate_action_cost_adjustment", "corporate_action_cost_adjustment.star"},
	}

	for _, ex := range examples {
		t.Run(ex.name, func(t *testing.T) {
			path := filepath.Join(dir, ex.filename)
			script, err := os.ReadFile(path)
			require.NoError(t, err, "failed to read %s", ex.filename)
			require.NotEmpty(t, script, "%s should not be empty", ex.filename)

			_, err = starlarkFileOptions.Parse(ex.filename, script, 0)
			assert.NoError(t, err, "Starlark syntax validation failed for %s", ex.name)
		})
	}
}

func TestExampleSagas_AllStarFilesHaveTests(t *testing.T) {
	dir := examplesDir(t)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	testedFiles := map[string]bool{
		"usage_to_value.star":                   true,
		"compute_billing.star":                  true,
		"race_result_distribution.star":         true,
		"corporate_action_cost_adjustment.star": true,
	}

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".star" {
			assert.True(t, testedFiles[entry.Name()],
				"star file %s exists but is not covered by TestExampleSagas_SyntaxValid — add it to the test list",
				entry.Name())
		}
	}
}
