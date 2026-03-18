// Package valuation provides the Engine interface and implementations for executing
// valuation methods with Starlark and CEL policy runtimes.
//
// The engine coordinates Starlark method execution (procedural logic), CEL policy
// evaluation (mathematical calculations), and read-only builtin functions
// (market_data, run_policy, etc.) within a security-hardened sandbox.
//
// Security guarantees:
//   - No filesystem or network access
//   - 5-second execution timeout
//   - 10 MB memory limit
//   - CEL cost limit: 10,000 units per policy
//
// An L1 in-process cache stores compiled methods and policies to avoid recompilation
// on repeated calls for the same instrument pair.
package valuation
