package sandbox

import "go.starlark.net/starlark"

// HardenThread applies security constraints from the given Config to a
// Starlark thread. This sets the maximum execution steps to prevent
// runaway scripts from exhausting compute resources.
func HardenThread(thread *starlark.Thread, cfg Config) {
	thread.SetMaxExecutionSteps(cfg.MaxStepsPerExecution)
}
