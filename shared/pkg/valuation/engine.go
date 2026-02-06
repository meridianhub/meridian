package valuation

import (
	"context"
)

// Engine executes valuation methods with Starlark + CEL policy runtime.
//
// The engine coordinates:
//   - Starlark method execution (procedural logic)
//   - CEL policy evaluation (mathematical calculations)
//   - Read-only builtin functions (market_data, run_policy, etc.)
//   - L1 cache for methods and policies
//
// Security guarantees:
//   - No filesystem access
//   - No network access (except via whitelisted builtins)
//   - 5-second execution timeout
//   - 64MB memory limit
//   - CEL cost limit: 10,000 units per policy
type Engine interface {
	// Valuate executes a valuation method and returns the valued amount.
	//
	// The execution flow:
	//   1. Validate request
	//   2. Resolve ValuationMethod from Reference Data (via cache)
	//   3. Load Starlark script
	//   4. Execute with sandboxed runtime
	//   5. Validate output instrument matches declared output_instrument
	//   6. Return Response with audit trail
	//
	// Errors:
	//   - ErrInvalidRequest: request validation failed
	//   - ErrMethodNotFound: method not found in Reference Data
	//   - ErrStarlarkTimeout: execution exceeded 5 seconds
	//   - ErrStarlarkMemoryLimit: execution exceeded 64MB
	//   - ErrStarlarkSandboxViolation: attempted filesystem/network access
	//   - ErrOutputMismatch: output instrument != declared output_instrument
	Valuate(ctx context.Context, req *Request) (*Response, error)
}

// Config holds configuration for creating an Engine instance.
type Config struct {
	// PolicyRuntime executes CEL policies with cost validation.
	PolicyRuntime PolicyRuntime

	// StarlarkRuntime executes Starlark scripts in a sandboxed environment.
	StarlarkRuntime StarlarkRuntime

	// Cache provides L1 in-memory caching for methods and policies.
	Cache Cache

	// MaxPathEntries is the maximum number of calculation path entries (default: 20).
	MaxPathEntries int
}

// PolicyRuntime provides CEL policy compilation and execution with cost limits.
type PolicyRuntime interface {
	// CompilePolicy compiles a CEL expression and validates cost < 10,000 units.
	// Returns an error if compilation fails or cost exceeds limit.
	CompilePolicy(expression string) (CompiledPolicy, error)

	// EvaluatePolicy executes a compiled policy with the given inputs.
	// Returns the result value and actual cost units consumed.
	EvaluatePolicy(ctx context.Context, policy CompiledPolicy, inputs map[string]interface{}) (interface{}, uint64, error)
}

// CompiledPolicy represents a compiled CEL policy ready for execution.
type CompiledPolicy interface {
	// EstimatedCost returns the estimated cost in CEL units.
	EstimatedCost() uint64

	// Expression returns the original CEL expression.
	Expression() string
}

// StarlarkRuntime provides sandboxed Starlark script execution.
type StarlarkRuntime interface {
	// Execute runs a Starlark script with the given context and returns the result.
	// The runtime enforces:
	//   - 5-second timeout
	//   - 64MB memory limit
	//   - No filesystem access
	//   - No network access
	Execute(ctx context.Context, script string, input *Request) (*Response, error)
}

// Cache provides L1 in-memory caching with TTL for methods and policies.
type Cache interface {
	// GetMethod retrieves a cached valuation method.
	// Returns nil if not found or expired.
	GetMethod(methodID string, version *int) (*Method, error)

	// SetMethod stores a valuation method in cache with TTL.
	SetMethod(method *Method) error

	// GetPolicy retrieves a cached CEL policy.
	// Returns nil if not found or expired.
	GetPolicy(policyName string, version *int) (CompiledPolicy, error)

	// SetPolicy stores a compiled policy in cache with TTL.
	SetPolicy(policyName string, version int, policy CompiledPolicy) error

	// Clear removes all cached entries.
	Clear()
}

// Method represents a Starlark-based valuation method from Reference Data.
type Method struct {
	// ID is the unique identifier for this method.
	ID string

	// Version is the version number of this method.
	Version int

	// Name is the human-readable name of this method.
	Name string

	// Script is the Starlark code that implements the valuation logic.
	Script string

	// OutputInstrument is the expected instrument code for the output (e.g., "GBP", "USD").
	// If specified, the engine validates that the output matches this instrument.
	OutputInstrument string

	// Description provides documentation for this method.
	Description string
}
