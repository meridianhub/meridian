package saga_test

import (
	"flag"
	"os"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var scriptPath = flag.String("script", "", "Path to saga script to validate")

// TestValidateSagaScript_ProductionScript validates a single production script
// passed via -script flag. Used by CI to validate all discovered scripts.
func TestValidateSagaScript_ProductionScript(t *testing.T) {
	if *scriptPath == "" {
		t.Skip("No -script flag provided, skipping production script validation")
	}

	// Read script file
	content, err := os.ReadFile(*scriptPath)
	require.NoError(t, err, "Failed to read script file: %s", *scriptPath)

	script := string(content)

	// Validate syntax and static analysis
	err = saga.ValidateSagaScript(script)
	assert.NoError(t, err, "Script validation failed for %s", *scriptPath)
}
