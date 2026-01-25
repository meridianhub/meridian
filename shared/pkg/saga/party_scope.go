// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// PartyType constants for party scope resolution.
// These represent the type of party for data isolation purposes.
const (
	// PartyTypeIndividual represents a natural person.
	// Individuals can only see their own data.
	PartyTypeIndividual = "INDIVIDUAL"

	// PartyTypeOrganization represents a legal entity.
	// Organizations can see their own data plus all descendant parties.
	PartyTypeOrganization = "ORGANIZATION"

	// PartyTypeSystem represents a system-level party with tenant-wide visibility.
	// System parties can see all parties within their tenant.
	PartyTypeSystem = "SYSTEM"
)

// Errors for party scope resolution.
var (
	// ErrPartyNotFound is returned when the party cannot be found.
	ErrPartyNotFound = errors.New("party not found")

	// ErrInvalidPartyScopeType is returned when the party type is not recognized.
	ErrInvalidPartyScopeType = errors.New("invalid party scope type")
)

// NewPartyScope creates a new immutable PartyScope.
// It creates a defensive copy of the visibleParties slice.
func NewPartyScope(partyID uuid.UUID, partyType string, visibleParties []uuid.UUID, tenantID string) *PartyScope {
	// Create defensive copy to ensure immutability
	parties := make([]uuid.UUID, len(visibleParties))
	copy(parties, visibleParties)

	return &PartyScope{
		PartyID:        partyID,
		PartyType:      partyType,
		VisibleParties: parties,
		TenantID:       tenantID,
	}
}

// Contains checks if the given party ID is within the visible scope.
func (ps *PartyScope) Contains(partyID uuid.UUID) bool {
	for _, visible := range ps.VisibleParties {
		if visible == partyID {
			return true
		}
	}
	return false
}

// GetVisibleParties returns a copy of the visible parties slice.
// This ensures the internal state cannot be modified by callers.
func (ps *PartyScope) GetVisibleParties() []uuid.UUID {
	result := make([]uuid.UUID, len(ps.VisibleParties))
	copy(result, ps.VisibleParties)
	return result
}

// String returns a human-readable representation of the party scope.
func (ps *PartyScope) String() string {
	return fmt.Sprintf("PartyScope{PartyID: %s, PartyType: %s, TenantID: %s, VisibleParties: %d}",
		ps.PartyID, ps.PartyType, ps.TenantID, len(ps.VisibleParties))
}

// PartyHierarchyClient defines the interface for querying party hierarchy information.
// This interface abstracts the Party Service gRPC client for testability.
type PartyHierarchyClient interface {
	// GetPartyType retrieves the type of a party (INDIVIDUAL, ORGANIZATION, SYSTEM).
	GetPartyType(ctx context.Context, partyID uuid.UUID) (string, error)

	// GetDescendants retrieves all descendant party IDs for an organization.
	// Returns an empty slice if the party has no descendants.
	GetDescendants(ctx context.Context, partyID uuid.UUID) ([]uuid.UUID, error)

	// GetTenantParties retrieves all party IDs within a tenant.
	GetTenantParties(ctx context.Context, tenantID string) ([]uuid.UUID, error)

	// GetTenantID retrieves the tenant ID for a given party.
	GetTenantID(ctx context.Context, partyID uuid.UUID) (string, error)
}

// PartyScopeResolver resolves the data visibility scope for a party.
type PartyScopeResolver interface {
	// Resolve determines the visible parties for a given party ID based on party type:
	// - INDIVIDUAL: Returns scope containing only self
	// - ORGANIZATION: Returns scope containing self + all descendants (recursive)
	// - SYSTEM: Returns scope containing all parties in the tenant
	Resolve(ctx context.Context, partyID uuid.UUID) (*PartyScope, error)
}

// partyScopeResolverImpl implements PartyScopeResolver using a PartyHierarchyClient.
type partyScopeResolverImpl struct {
	client PartyHierarchyClient
}

// NewPartyScopeResolver creates a new PartyScopeResolver with the given hierarchy client.
func NewPartyScopeResolver(client PartyHierarchyClient) PartyScopeResolver {
	return &partyScopeResolverImpl{client: client}
}

// Resolve implements PartyScopeResolver.
func (r *partyScopeResolverImpl) Resolve(ctx context.Context, partyID uuid.UUID) (*PartyScope, error) {
	// Get the party type
	partyType, err := r.client.GetPartyType(ctx, partyID)
	if err != nil {
		return nil, err
	}

	// Get the tenant ID
	tenantID, err := r.client.GetTenantID(ctx, partyID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant ID: %w", err)
	}

	var visibleParties []uuid.UUID

	switch partyType {
	case PartyTypeIndividual:
		// Individuals can only see their own data
		visibleParties = []uuid.UUID{partyID}

	case PartyTypeOrganization:
		// Organizations can see self + all descendants
		descendants, err := r.client.GetDescendants(ctx, partyID)
		if err != nil {
			return nil, fmt.Errorf("failed to get descendants: %w", err)
		}
		visibleParties = make([]uuid.UUID, 0, 1+len(descendants))
		visibleParties = append(visibleParties, partyID)
		visibleParties = append(visibleParties, descendants...)

	case PartyTypeSystem:
		// System parties can see all parties in the tenant
		tenantParties, err := r.client.GetTenantParties(ctx, tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to get tenant parties: %w", err)
		}
		visibleParties = tenantParties

	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidPartyScopeType, partyType)
	}

	return NewPartyScope(partyID, partyType, visibleParties, tenantID), nil
}
