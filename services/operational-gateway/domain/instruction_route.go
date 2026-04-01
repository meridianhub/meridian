package domain

import (
	"errors"
	"time"
)

// RouteStatus represents the lifecycle state of an instruction route.
type RouteStatus string

const (
	// RouteStatusActive means the route is available for dispatching instructions.
	RouteStatusActive RouteStatus = "ACTIVE"
	// RouteStatusDeprecated means the route is no longer recommended for new use.
	RouteStatusDeprecated RouteStatus = "DEPRECATED"
)

// InstructionRoute domain errors.
var (
	ErrInstructionTypeRequired  = errors.New("instruction_type is required")
	ErrConnectionIDRequired     = errors.New("connection_id is required")
	ErrInstructionRouteNotFound = errors.New("instruction route not found")
	ErrRouteNotActive           = errors.New("route is not in ACTIVE status")
)

// Route represents a configured mapping from an instruction_type to a provider connection.
// Routes are configured via manifest apply and resolved at dispatch time by the dispatch worker.
type Route struct {
	// TenantID is the tenant this route belongs to (UUID string).
	TenantID string

	// InstructionType is the unique key for route lookup (e.g. "payment.initiate").
	InstructionType string

	// ConnectionID is the primary ProviderConnection UUID for dispatching this instruction type.
	ConnectionID string

	// FallbackConnectionID is an optional secondary connection UUID used when the primary is unhealthy.
	// Empty string means no fallback.
	FallbackConnectionID string

	// OutboundMapping is the name of the MappingDefinition for outbound payload transformation.
	// Empty string means no mapping (passthrough).
	OutboundMapping string

	// InboundMapping is the name of the MappingDefinition for inbound response transformation.
	// Empty string means no mapping (passthrough).
	InboundMapping string

	// HTTPMethod is the HTTP verb for HTTPS/WEBHOOK protocols (e.g. "POST", "PUT").
	HTTPMethod string

	// PathTemplate is the URL path template appended to the connection base_url.
	PathTemplate string

	// Status is the lifecycle state of this route (ACTIVE or DEPRECATED).
	Status RouteStatus

	// DeprecatedAt is when this route was deprecated, or nil if not deprecated.
	DeprecatedAt *time.Time

	// CreatedAt is when this route was first created.
	CreatedAt time.Time

	// UpdatedAt is when this route was last updated.
	UpdatedAt time.Time
}

// NewRoute constructs a new Route with required fields validated.
func NewRoute(tenantID, instructionType, connectionID string) (*Route, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	if instructionType == "" {
		return nil, ErrInstructionTypeRequired
	}
	if connectionID == "" {
		return nil, ErrConnectionIDRequired
	}
	now := time.Now().UTC()
	return &Route{
		TenantID:        tenantID,
		InstructionType: instructionType,
		ConnectionID:    connectionID,
		Status:          RouteStatusActive,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

// Deprecate transitions the route from ACTIVE to DEPRECATED.
// Returns ErrRouteNotActive if the route is not in ACTIVE status.
// Idempotent: returns nil if already DEPRECATED.
func (r *Route) Deprecate() error {
	if r.Status == RouteStatusDeprecated {
		return nil // idempotent
	}
	if r.Status != RouteStatusActive {
		return ErrRouteNotActive
	}
	now := time.Now().UTC()
	r.Status = RouteStatusDeprecated
	r.DeprecatedAt = &now
	r.UpdatedAt = now
	return nil
}
