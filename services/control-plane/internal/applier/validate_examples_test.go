package applier_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

// tenantExamplesDir returns the directory containing the tenant saga example .star files.
func tenantExamplesDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get caller info")
	return filepath.Join(filepath.Dir(filename), "testdata", "tenant-saga-examples")
}

// starlarkFileOptions matches the saga runtime's parser configuration:
// Set=true, While=false (no while loops), GlobalReassign=true, Recursion=false.
var starlarkFileOptions = &syntax.FileOptions{
	Set:            true,
	While:          false,
	GlobalReassign: true,
	Recursion:      false,
}

func TestTenantExampleSagas_SyntaxValid(t *testing.T) {
	dir := tenantExamplesDir(t)

	examples := []struct {
		name     string
		filename string
	}{
		{"usage_to_value", "usage_to_value.star"},
		{"compute_billing", "compute_billing.star"},
		{"race_result_distribution", "race_result_distribution.star"},
		{"corporate_action_cost_adjustment", "corporate_action_cost_adjustment.star"},
		{"tou_energy_valuation", "tou_energy_valuation.star"},
		{"dynamic_capacity_billing", "dynamic_capacity_billing.star"},
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

func TestTenantExampleSagas_AllStarFilesHaveTests(t *testing.T) {
	dir := tenantExamplesDir(t)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	testedFiles := map[string]bool{
		"usage_to_value.star":                   true,
		"compute_billing.star":                  true,
		"race_result_distribution.star":         true,
		"corporate_action_cost_adjustment.star": true,
		"tou_energy_valuation.star":             true,
		"dynamic_capacity_billing.star":         true,
	}

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".star" {
			assert.True(t, testedFiles[entry.Name()],
				"star file %s exists but is not covered by TestTenantExampleSagas_SyntaxValid — add it to the test list",
				entry.Name())
		}
	}
}
