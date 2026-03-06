package valuation

import (
	"context"
	"fmt"
	"sync"
)

// ErrMethodNotRegistered is returned when a method ID is not found in the static resolver.
var ErrMethodNotRegistered = fmt.Errorf("method not registered: %w", ErrMethodNotFound)

// StaticMethodResolver implements MethodResolver using an in-memory method registry.
// Methods are registered at startup and looked up by ID. This is used when a gRPC
// endpoint to the Reference Data service is unavailable, allowing the valuation engine
// to operate with pre-configured methods (e.g., identity conversion, system defaults).
type StaticMethodResolver struct {
	mu      sync.RWMutex
	methods map[string]*Method // keyed by method ID
}

// NewStaticMethodResolver creates a new empty resolver.
func NewStaticMethodResolver() *StaticMethodResolver {
	return &StaticMethodResolver{
		methods: make(map[string]*Method),
	}
}

// Register adds a method to the resolver. If a method with the same ID already exists,
// it is overwritten.
func (r *StaticMethodResolver) Register(method *Method) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods[method.ID] = method
}

// ResolveMethod retrieves a method by ID. Version is ignored for static methods.
func (r *StaticMethodResolver) ResolveMethod(_ context.Context, methodID string, _ *int) (*Method, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	method, ok := r.methods[methodID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMethodNotRegistered, methodID)
	}
	return method, nil
}

// IdentityMethodID is a well-known valuation method ID for identity (pass-through) conversion.
// The Starlark script returns the input amount unchanged.
const IdentityMethodID = "00000000-0000-0000-0000-000000000001"

// IdentityConversionScript is a Starlark script that performs identity valuation:
// returns the input quantity unchanged.
const IdentityConversionScript = `
# Identity valuation: returns input amount unchanged
input = ctx["input_quantity"]
result = {
    "valued_amount": input["amount"],
    "instrument": input["instrument"],
}
`

// NewIdentityMethodResolver creates a StaticMethodResolver pre-loaded with the
// identity conversion method. This is the minimal viable replacement for
// StubValuationEngine - it runs real Starlark execution but produces the same
// identity output.
func NewIdentityMethodResolver() *StaticMethodResolver {
	resolver := NewStaticMethodResolver()
	resolver.Register(&Method{
		ID:               IdentityMethodID,
		Version:          1,
		Name:             "identity-conversion",
		Script:           IdentityConversionScript,
		OutputInstrument: "", // No output validation - passes through input instrument
		Description:      "Identity valuation: returns input amount in the same instrument",
	})
	return resolver
}
