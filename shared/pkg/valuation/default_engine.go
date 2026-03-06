package valuation

import (
	"context"
	"fmt"
	"time"
)

// defaultEngine implements Engine by orchestrating StarlarkRuntime, PolicyRuntime, and Cache.
type defaultEngine struct {
	starlark       StarlarkRuntime
	cache          Cache
	methodResolver MethodResolver
	maxPathEntries int
}

// MethodResolver fetches valuation methods from an external source (e.g., Reference Data).
type MethodResolver interface {
	// ResolveMethod retrieves a valuation method by ID and optional version.
	// If version is nil, returns the latest active version.
	ResolveMethod(ctx context.Context, methodID string, version *int) (*Method, error)
}

// NewEngine creates a new Engine that orchestrates Starlark execution with caching.
func NewEngine(cfg Config, methodResolver MethodResolver) Engine {
	maxPath := cfg.MaxPathEntries
	if maxPath == 0 {
		maxPath = MaxPathEntries
	}

	return &defaultEngine{
		starlark:       cfg.StarlarkRuntime,
		cache:          cfg.Cache,
		methodResolver: methodResolver,
		maxPathEntries: maxPath,
	}
}

// Valuate executes a valuation method and returns the valued amount.
func (e *defaultEngine) Valuate(ctx context.Context, req *Request) (*Response, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	method, cacheHit, err := e.resolveMethod(ctx, req.MethodID.String(), req.MethodVersion)
	if err != nil {
		return nil, err
	}

	resp, err := e.starlark.Execute(ctx, method.Script, req)
	if err != nil {
		return nil, err
	}

	// Validate output instrument if method declares one
	if method.OutputInstrument != "" && resp.ValuedAmount.InstrumentCode != method.OutputInstrument {
		return nil, fmt.Errorf("%w: expected %s, got %s",
			ErrOutputMismatch, method.OutputInstrument, resp.ValuedAmount.InstrumentCode)
	}

	resp.CacheHit = cacheHit
	resp.ComputedAt = time.Now()

	return resp, nil
}

// resolveMethod fetches a method from cache or the external resolver.
func (e *defaultEngine) resolveMethod(ctx context.Context, methodID string, version *int) (*Method, bool, error) {
	if e.cache != nil {
		cached, err := e.cache.GetMethod(methodID, version)
		if err != nil {
			return nil, false, fmt.Errorf("cache lookup failed: %w", err)
		}
		if cached != nil {
			return cached, true, nil
		}
	}

	method, err := e.methodResolver.ResolveMethod(ctx, methodID, version)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrMethodNotFound, err)
	}

	if e.cache != nil {
		_ = e.cache.SetMethod(method)
	}

	return method, false, nil
}
