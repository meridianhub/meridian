package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

func TestHardenThread_SetsMaxSteps(t *testing.T) {
	thread := &starlark.Thread{Name: "test"}
	cfg := DefaultConfig()

	HardenThread(thread, cfg)

	// Verify the step limit is enforced by running a script that exceeds it.
	// A simple loop that does cfg.MaxStepsPerExecution+1 iterations should fail.
	// We use a small config to make the test fast.
	smallCfg := Config{MaxStepsPerExecution: 100}
	smallThread := &starlark.Thread{Name: "small-test"}
	HardenThread(smallThread, smallCfg)

	// Execute a tight loop inside a function — should exceed 100 steps and error.
	script := `
def work():
    x = 0
    for i in range(1000):
        x = x + 1
    return x
work()
`
	//nolint:staticcheck // ExecFileOptions requires FileOptions which we don't need to customize
	_, err := starlark.ExecFile(smallThread, "test.star", script, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many steps")
}

func TestHardenThread_AllowsSmallScripts(t *testing.T) {
	thread := &starlark.Thread{Name: "test"}
	cfg := DefaultConfig() // 1M steps — plenty for a small script

	HardenThread(thread, cfg)

	script := `x = 1 + 2`
	//nolint:staticcheck // ExecFileOptions requires FileOptions which we don't need to customize
	_, err := starlark.ExecFile(thread, "test.star", script, nil)
	assert.NoError(t, err)
}

func TestHardenThread_ValuationStepsHigherThanDefault(t *testing.T) {
	// Valuation config allows more steps than the default.
	defCfg := DefaultConfig()
	valCfg := ValuationConfig()
	assert.Greater(t, valCfg.MaxStepsPerExecution, defCfg.MaxStepsPerExecution)
}
