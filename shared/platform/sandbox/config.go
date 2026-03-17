// Package sandbox provides unified Starlark sandbox security configuration
// and thread hardening for all Meridian runtimes (saga, valuation, forecasting).
package sandbox

import "time"

// Config holds security constraints for a Starlark sandbox.
type Config struct {
	// Timeout is the maximum execution time for a script.
	// Note: HardenThread does not apply this value; each runtime manages
	// timeouts independently via context.WithTimeout. This field is provided
	// so callers have a single config struct for all sandbox parameters.
	Timeout time.Duration

	// MaxScriptSize is the maximum allowed script size in bytes.
	MaxScriptSize int

	// MaxStepsPerExecution limits the number of Starlark interpreter steps
	// to prevent infinite loops and resource exhaustion.
	MaxStepsPerExecution uint64
}

// DefaultConfig returns the standard sandbox configuration used by the saga runtime.
// Timeout=5s, MaxScriptSize=64KB, MaxSteps=1M.
func DefaultConfig() Config {
	return Config{
		Timeout:              5 * time.Second,
		MaxScriptSize:        64 * 1024,
		MaxStepsPerExecution: 1_000_000,
	}
}

// ValuationConfig returns the sandbox configuration for valuation scripts.
// Timeout=5s, MaxScriptSize=64KB, MaxSteps=5M.
func ValuationConfig() Config {
	return Config{
		Timeout:              5 * time.Second,
		MaxScriptSize:        64 * 1024,
		MaxStepsPerExecution: 5_000_000,
	}
}

// ForecasterConfig returns the sandbox configuration for forecasting scripts.
// Timeout=10s, MaxScriptSize=64KB, MaxSteps=1M.
func ForecasterConfig() Config {
	return Config{
		Timeout:              10 * time.Second,
		MaxScriptSize:        64 * 1024,
		MaxStepsPerExecution: 1_000_000,
	}
}
