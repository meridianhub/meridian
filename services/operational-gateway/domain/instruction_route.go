package domain

import (
	"errors"
	"time"
)

// InstructionRoute domain errors.
var (
	ErrInstructionTypeRequired  = errors.New("instruction_type is required")
	ErrConnectionIDRequired     = errors.New("connection_id is required")
	ErrInstructionRouteNotFound = errors.New("instruction route not found")
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

	// CreatedAt is when this route was first created.
	CreatedAt time.Time

	// UpdatedAt is when this route was last updated.
	UpdatedAt time.Time
}

// NewRoute constructs a new Route with required fields validated.
func NewRoute(tenantID, instructionType, connectionID string) (*Route, error) {
	if tenantID == "" {
		return nil, ErrConnectionIDRequired
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
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}
