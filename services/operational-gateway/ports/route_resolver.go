// Package ports defines the interfaces (ports) for the operational-gateway service.
package ports

import (
	"context"
	"errors"
)

// ErrRouteNotFound is returned when no route is configured for the given instruction type.
var ErrRouteNotFound = errors.New("no route configured for instruction type")

// RouteResolver resolves the dispatch route for an instruction type within a tenant.
// Implementations may look up routes from a configuration store, manifest registry,
// or an in-memory cache seeded at startup.
type RouteResolver interface {
	// Resolve returns the InstructionRoute for the given tenant and instruction type.
	// Returns ErrRouteNotFound if no route is configured.
	Resolve(ctx context.Context, tenantID string, instructionType string) (*InstructionRoute, error)
}
