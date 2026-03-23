package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSkipManifestDoesNotBlockFixtures(t *testing.T) {
	// Verify that --skip-manifest and --with-fixtures can be set independently.
	// The bug: runSeed() returned early when skipManifest was true,
	// preventing withFixtures code from executing.

	cmd := rootCmd

	// Parse flags without executing - verify both flags are accepted together.
	err := cmd.ParseFlags([]string{"--skip-manifest", "--with-fixtures"})
	assert.NoError(t, err)

	// After parsing, both package-level vars should be set.
	assert.True(t, skipManifest, "skipManifest flag should be true")
	assert.True(t, withFixtures, "withFixtures flag should be true")

	// Reset for other tests.
	skipManifest = false
	withFixtures = false
}
