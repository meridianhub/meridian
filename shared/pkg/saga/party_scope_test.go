// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPartyScope_Contains(t *testing.T) {
	partyID := uuid.New()
	otherParty := uuid.New()
	unrelatedParty := uuid.New()

	scope := &PartyScope{
		PartyID:        partyID,
		PartyType:      PartyTypeOrganization,
		VisibleParties: []uuid.UUID{partyID, otherParty},
		TenantID:       "tenant-1",
	}

	t.Run("returns true for visible party", func(t *testing.T) {
		assert.True(t, scope.Contains(partyID))
		assert.True(t, scope.Contains(otherParty))
	})

	t.Run("returns false for non-visible party", func(t *testing.T) {
		assert.False(t, scope.Contains(unrelatedParty))
	})
}

func TestPartyScope_String(t *testing.T) {
	partyID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	otherParty := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	scope := &PartyScope{
		PartyID:        partyID,
		PartyType:      PartyTypeOrganization,
		VisibleParties: []uuid.UUID{partyID, otherParty},
		TenantID:       "tenant-1",
	}

	str := scope.String()
	assert.Contains(t, str, "11111111-1111-1111-1111-111111111111")
	assert.Contains(t, str, "ORGANIZATION")
	assert.Contains(t, str, "tenant-1")
}

func TestPartyScope_Immutability(t *testing.T) {
	partyID := uuid.New()
	originalParties := []uuid.UUID{partyID}

	scope := NewPartyScope(partyID, PartyTypeIndividual, originalParties, "tenant-1")

	// Verify that modifying the original slice doesn't affect the scope
	originalParties[0] = uuid.New()
	assert.Equal(t, partyID, scope.VisibleParties[0])

	// Verify that getting visible parties returns a copy
	parties := scope.GetVisibleParties()
	parties[0] = uuid.New()
	assert.Equal(t, partyID, scope.VisibleParties[0])
}

// Mock implementation of PartyHierarchyClient for testing
type mockPartyHierarchyClient struct {
	// partyTypes maps party IDs to their types
	partyTypes map[uuid.UUID]string
	// hierarchy maps parent party IDs to their child party IDs
	hierarchy map[uuid.UUID][]uuid.UUID
	// tenantParties maps tenant IDs to all parties in that tenant
	tenantParties map[string][]uuid.UUID
}

func newMockPartyHierarchyClient() *mockPartyHierarchyClient {
	return &mockPartyHierarchyClient{
		partyTypes:    make(map[uuid.UUID]string),
		hierarchy:     make(map[uuid.UUID][]uuid.UUID),
		tenantParties: make(map[string][]uuid.UUID),
	}
}

func (m *mockPartyHierarchyClient) GetPartyType(_ context.Context, partyID uuid.UUID) (string, error) {
	if pt, ok := m.partyTypes[partyID]; ok {
		return pt, nil
	}
	return "", ErrPartyNotFound
}

func (m *mockPartyHierarchyClient) GetDescendants(_ context.Context, partyID uuid.UUID) ([]uuid.UUID, error) {
	var result []uuid.UUID
	// Recursive helper to get all descendants
	var collectDescendants func(id uuid.UUID)
	collectDescendants = func(id uuid.UUID) {
		children, ok := m.hierarchy[id]
		if !ok {
			return
		}
		for _, child := range children {
			result = append(result, child)
			collectDescendants(child)
		}
	}
	collectDescendants(partyID)
	return result, nil
}

func (m *mockPartyHierarchyClient) GetTenantParties(_ context.Context, tenantID string) ([]uuid.UUID, error) {
	if parties, ok := m.tenantParties[tenantID]; ok {
		return parties, nil
	}
	return []uuid.UUID{}, nil
}

func (m *mockPartyHierarchyClient) GetTenantID(_ context.Context, _ uuid.UUID) (string, error) {
	// For simplicity, return "tenant-1" for all parties in the mock
	return "tenant-1", nil
}

func TestPartyScopeResolver_Individual(t *testing.T) {
	ctx := context.Background()
	individualID := uuid.New()

	mock := newMockPartyHierarchyClient()
	mock.partyTypes[individualID] = "INDIVIDUAL"

	resolver := NewPartyScopeResolver(mock)
	scope, err := resolver.Resolve(ctx, individualID)

	require.NoError(t, err)
	assert.Equal(t, individualID, scope.PartyID)
	assert.Equal(t, PartyTypeIndividual, scope.PartyType)
	assert.Equal(t, []uuid.UUID{individualID}, scope.VisibleParties)
	assert.Equal(t, "tenant-1", scope.TenantID)
}

func TestPartyScopeResolver_Organization_NoDescendants(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()

	mock := newMockPartyHierarchyClient()
	mock.partyTypes[orgID] = "ORGANIZATION"
	// No descendants

	resolver := NewPartyScopeResolver(mock)
	scope, err := resolver.Resolve(ctx, orgID)

	require.NoError(t, err)
	assert.Equal(t, orgID, scope.PartyID)
	assert.Equal(t, PartyTypeOrganization, scope.PartyType)
	assert.Equal(t, []uuid.UUID{orgID}, scope.VisibleParties)
}

func TestPartyScopeResolver_Organization_WithDescendants(t *testing.T) {
	ctx := context.Background()
	parentOrg := uuid.New()
	childOrg1 := uuid.New()
	childOrg2 := uuid.New()
	grandChild := uuid.New()

	mock := newMockPartyHierarchyClient()
	mock.partyTypes[parentOrg] = "ORGANIZATION"
	mock.hierarchy[parentOrg] = []uuid.UUID{childOrg1, childOrg2}
	mock.hierarchy[childOrg1] = []uuid.UUID{grandChild}

	resolver := NewPartyScopeResolver(mock)
	scope, err := resolver.Resolve(ctx, parentOrg)

	require.NoError(t, err)
	assert.Equal(t, parentOrg, scope.PartyID)
	assert.Equal(t, PartyTypeOrganization, scope.PartyType)

	// Should contain self + all descendants (recursive)
	assert.Contains(t, scope.VisibleParties, parentOrg)
	assert.Contains(t, scope.VisibleParties, childOrg1)
	assert.Contains(t, scope.VisibleParties, childOrg2)
	assert.Contains(t, scope.VisibleParties, grandChild)
	assert.Len(t, scope.VisibleParties, 4)
}

func TestPartyScopeResolver_System(t *testing.T) {
	ctx := context.Background()
	systemPartyID := uuid.New()
	party1 := uuid.New()
	party2 := uuid.New()
	party3 := uuid.New()

	mock := newMockPartyHierarchyClient()
	mock.partyTypes[systemPartyID] = "SYSTEM"
	mock.tenantParties["tenant-1"] = []uuid.UUID{systemPartyID, party1, party2, party3}

	resolver := NewPartyScopeResolver(mock)
	scope, err := resolver.Resolve(ctx, systemPartyID)

	require.NoError(t, err)
	assert.Equal(t, systemPartyID, scope.PartyID)
	assert.Equal(t, PartyTypeSystem, scope.PartyType)

	// SYSTEM should see all parties in the tenant
	assert.Contains(t, scope.VisibleParties, systemPartyID)
	assert.Contains(t, scope.VisibleParties, party1)
	assert.Contains(t, scope.VisibleParties, party2)
	assert.Contains(t, scope.VisibleParties, party3)
	assert.Len(t, scope.VisibleParties, 4)
}

func TestPartyScopeResolver_PartyNotFound(t *testing.T) {
	ctx := context.Background()
	unknownParty := uuid.New()

	mock := newMockPartyHierarchyClient()
	// Don't add the party to the mock

	resolver := NewPartyScopeResolver(mock)
	_, err := resolver.Resolve(ctx, unknownParty)

	assert.ErrorIs(t, err, ErrPartyNotFound)
}

func TestPartyScopeResolver_UnknownPartyType(t *testing.T) {
	ctx := context.Background()
	partyID := uuid.New()

	mock := newMockPartyHierarchyClient()
	mock.partyTypes[partyID] = "UNKNOWN_TYPE"

	resolver := NewPartyScopeResolver(mock)
	_, err := resolver.Resolve(ctx, partyID)

	assert.ErrorIs(t, err, ErrInvalidPartyScopeType)
}

// Integration tests for Runtime with PartyScope

func TestRuntime_WithPartyScopeResolver(t *testing.T) {
	ctx := context.Background()
	partyID := uuid.New()

	mock := newMockPartyHierarchyClient()
	mock.partyTypes[partyID] = PartyTypeIndividual
	resolver := NewPartyScopeResolver(mock)

	runtime, err := NewRuntime(nil, WithPartyScopeResolver(resolver))
	require.NoError(t, err)

	script := `
# Access party_scope in Starlark
scope_party_id = party_scope["party_id"]
scope_party_type = party_scope["party_type"]
scope_tenant_id = party_scope["tenant_id"]
visible_count = len(party_scope["visible_parties"])
`

	result, err := runtime.ExecuteSagaWithInput(ctx, "party_scope_test", script, ExecutionInput{
		ExecutingPartyID: &partyID,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.PartyScope)
	assert.Equal(t, partyID, result.PartyScope.PartyID)
	assert.Equal(t, PartyTypeIndividual, result.PartyScope.PartyType)
	assert.Equal(t, partyID.String(), result.Globals["scope_party_id"])
	assert.Equal(t, PartyTypeIndividual, result.Globals["scope_party_type"])
	assert.Equal(t, int64(1), result.Globals["visible_count"])
}

func TestRuntime_PartyScopeImmutable(t *testing.T) {
	ctx := context.Background()
	partyID := uuid.New()

	mock := newMockPartyHierarchyClient()
	mock.partyTypes[partyID] = PartyTypeIndividual
	resolver := NewPartyScopeResolver(mock)

	runtime, err := NewRuntime(nil, WithPartyScopeResolver(resolver))
	require.NoError(t, err)

	// Try to modify the frozen party_scope dict - should fail with error
	script := `party_scope["new_key"] = "value"`

	_, err = runtime.ExecuteSagaWithInput(ctx, "immutable_test", script, ExecutionInput{
		ExecutingPartyID: &partyID,
	})

	// Starlark should error when trying to mutate a frozen dict
	require.Error(t, err)
	assert.Contains(t, err.Error(), "frozen")
}

func TestRuntime_PartyScopeVisiblePartiesImmutable(t *testing.T) {
	ctx := context.Background()
	partyID := uuid.New()
	childParty := uuid.New()

	mock := newMockPartyHierarchyClient()
	mock.partyTypes[partyID] = PartyTypeOrganization
	mock.hierarchy[partyID] = []uuid.UUID{childParty}
	resolver := NewPartyScopeResolver(mock)

	runtime, err := NewRuntime(nil, WithPartyScopeResolver(resolver))
	require.NoError(t, err)

	// Try to append to visible_parties list - should fail with error
	script := `party_scope["visible_parties"].append("new_party")`

	_, err = runtime.ExecuteSagaWithInput(ctx, "list_immutable_test", script, ExecutionInput{
		ExecutingPartyID: &partyID,
	})

	// Starlark should error when trying to mutate a frozen list
	require.Error(t, err)
	assert.Contains(t, err.Error(), "frozen")
}

func TestRuntime_PartyScopeResolutionFailure(t *testing.T) {
	ctx := context.Background()
	unknownParty := uuid.New()

	mock := newMockPartyHierarchyClient()
	// Don't add the party - resolution should fail
	resolver := NewPartyScopeResolver(mock)

	runtime, err := NewRuntime(nil, WithPartyScopeResolver(resolver))
	require.NoError(t, err)

	script := `result = "should not reach here"`

	_, err = runtime.ExecuteSagaWithInput(ctx, "resolution_failure", script, ExecutionInput{
		ExecutingPartyID: &unknownParty,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "step -1")
	assert.ErrorIs(t, err, ErrPartyNotFound)
}

func TestRuntime_NoPartyScopeWithoutExecutingPartyID(t *testing.T) {
	ctx := context.Background()

	mock := newMockPartyHierarchyClient()
	resolver := NewPartyScopeResolver(mock)

	runtime, err := NewRuntime(nil, WithPartyScopeResolver(resolver))
	require.NoError(t, err)

	// Without ExecutingPartyID, party_scope should not be available in result
	script := `result = "executed"`

	result, err := runtime.ExecuteSagaWithInput(ctx, "no_party_scope", script, ExecutionInput{
		Data: map[string]interface{}{"key": "value"},
	})

	require.NoError(t, err)
	assert.Nil(t, result.PartyScope, "PartyScope should be nil when ExecutingPartyID is not provided")
}

func TestRuntime_NoPartyScopeWithoutResolver(t *testing.T) {
	ctx := context.Background()
	partyID := uuid.New()

	// No resolver configured
	runtime, err := NewRuntime(nil)
	require.NoError(t, err)

	script := `result = "executed"`

	result, err := runtime.ExecuteSagaWithInput(ctx, "no_resolver", script, ExecutionInput{
		ExecutingPartyID: &partyID,
	})

	require.NoError(t, err)
	assert.Nil(t, result.PartyScope, "PartyScope should be nil when resolver is not configured")
}

func TestRuntime_VisibilityPreflightPasses(t *testing.T) {
	ctx := context.Background()
	partyID := uuid.New()
	visibleCounterparty := uuid.New()

	mock := newMockPartyHierarchyClient()
	mock.partyTypes[partyID] = PartyTypeOrganization
	mock.hierarchy[partyID] = []uuid.UUID{visibleCounterparty}
	resolver := NewPartyScopeResolver(mock)

	runtime, err := NewRuntime(nil, WithPartyScopeResolver(resolver))
	require.NoError(t, err)

	script := `result = "executed"`

	// Input references only visible parties
	result, err := runtime.ExecuteSagaWithInput(ctx, "preflight_pass", script, ExecutionInput{
		ExecutingPartyID: &partyID,
		Data: map[string]interface{}{
			"party_id":        partyID.String(),
			"counterparty_id": visibleCounterparty.String(),
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestRuntime_VisibilityPreflightFailsForInvisibleParty(t *testing.T) {
	ctx := context.Background()
	partyID := uuid.New()
	invisibleParty := uuid.New()

	mock := newMockPartyHierarchyClient()
	mock.partyTypes[partyID] = PartyTypeIndividual // Individual can only see self
	resolver := NewPartyScopeResolver(mock)

	runtime, err := NewRuntime(nil, WithPartyScopeResolver(resolver))
	require.NoError(t, err)

	script := `result = "should not execute"`

	// Input references an invisible party
	_, err = runtime.ExecuteSagaWithInput(ctx, "preflight_fail", script, ExecutionInput{
		ExecutingPartyID: &partyID,
		Data: map[string]interface{}{
			"party_id":        partyID.String(),
			"counterparty_id": invisibleParty.String(), // Not visible to individual
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "step -0.5")
	assert.ErrorIs(t, err, ErrVisibilityViolation)
}
