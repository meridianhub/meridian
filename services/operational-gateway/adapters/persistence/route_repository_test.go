package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeRoute constructs a minimal domain.Route for testing.
func makeRoute(t *testing.T, tenantID, instructionType, connectionID string) *domain.Route {
	t.Helper()
	route, err := domain.NewRoute(tenantID, instructionType, connectionID)
	require.NoError(t, err)
	return route
}

// insertRouteConnection creates a provider connection row to satisfy instruction_routes FK constraints.
func insertRouteConnection(t *testing.T, connRepo *ConnectionRepository, tenantID, connectionID string) {
	t.Helper()
	conn, err := domain.NewProviderConnection(
		tenantID,
		"test-provider",
		"test-type",
		domain.ProtocolHTTPS,
		"https://api.example.com",
		&domain.APIKeyAuth{HeaderName: "X-Key", SecretRef: "ref"},
		domain.RetryPolicy{MaxAttempts: 3, InitialBackoff: 100 * time.Millisecond, BackoffMultiplier: 2.0},
		domain.RateLimitConfig{},
	)
	require.NoError(t, err)
	conn.ConnectionID = connectionID
	require.NoError(t, connRepo.Upsert(context.Background(), conn))
}

// TestRouteRepository_Upsert_Create verifies a new route can be inserted.
func TestRouteRepository_Upsert_Create(t *testing.T) {
	db, ctx := setupTestDB(t)
	connRepo := NewConnectionRepository(db)
	routeRepo := NewRouteRepository(db)

	tenantID := uuid.New().String()
	connID := uuid.New().String()
	insertRouteConnection(t, connRepo, tenantID, connID)

	route := makeRoute(t, tenantID, "kyc.verify", connID)
	require.NoError(t, routeRepo.Upsert(ctx, route))

	found, err := routeRepo.FindByInstructionType(ctx, tenantID, "kyc.verify")
	require.NoError(t, err)
	assert.Equal(t, tenantID, found.TenantID)
	assert.Equal(t, "kyc.verify", found.InstructionType)
	assert.Equal(t, connID, found.ConnectionID)
}

// TestRouteRepository_Upsert_Update verifies upsert replaces an existing route.
func TestRouteRepository_Upsert_Update(t *testing.T) {
	db, ctx := setupTestDB(t)
	connRepo := NewConnectionRepository(db)
	routeRepo := NewRouteRepository(db)

	tenantID := uuid.New().String()
	connID1 := uuid.New().String()
	connID2 := uuid.New().String()
	insertRouteConnection(t, connRepo, tenantID, connID1)
	insertRouteConnection(t, connRepo, tenantID, connID2)

	// Initial upsert.
	route := makeRoute(t, tenantID, "device.command", connID1)
	route.HTTPMethod = "POST"
	route.PathTemplate = "/v1/commands"
	require.NoError(t, routeRepo.Upsert(ctx, route))

	// Update with new connection and fields.
	updated := makeRoute(t, tenantID, "device.command", connID2)
	updated.HTTPMethod = "PUT"
	updated.PathTemplate = "/v2/commands"
	updated.OutboundMapping = "device-outbound"
	updated.InboundMapping = "device-inbound"
	require.NoError(t, routeRepo.Upsert(ctx, updated))

	found, err := routeRepo.FindByInstructionType(ctx, tenantID, "device.command")
	require.NoError(t, err)
	assert.Equal(t, connID2, found.ConnectionID)
	assert.Equal(t, "PUT", found.HTTPMethod)
	assert.Equal(t, "/v2/commands", found.PathTemplate)
	assert.Equal(t, "device-outbound", found.OutboundMapping)
	assert.Equal(t, "device-inbound", found.InboundMapping)
}

// TestRouteRepository_Upsert_WithFallback verifies fallback connection is persisted.
func TestRouteRepository_Upsert_WithFallback(t *testing.T) {
	db, ctx := setupTestDB(t)
	connRepo := NewConnectionRepository(db)
	routeRepo := NewRouteRepository(db)

	tenantID := uuid.New().String()
	connID := uuid.New().String()
	fallbackID := uuid.New().String()
	insertRouteConnection(t, connRepo, tenantID, connID)
	insertRouteConnection(t, connRepo, tenantID, fallbackID)

	route := makeRoute(t, tenantID, "notification.send", connID)
	route.FallbackConnectionID = fallbackID
	require.NoError(t, routeRepo.Upsert(ctx, route))

	found, err := routeRepo.FindByInstructionType(ctx, tenantID, "notification.send")
	require.NoError(t, err)
	assert.Equal(t, fallbackID, found.FallbackConnectionID)
}

// TestRouteRepository_FindByInstructionType_NotFound verifies ErrRouteNotFound is returned.
func TestRouteRepository_FindByInstructionType_NotFound(t *testing.T) {
	db, ctx := setupTestDB(t)
	routeRepo := NewRouteRepository(db)

	_, err := routeRepo.FindByInstructionType(ctx, uuid.New().String(), "nonexistent.type")
	require.ErrorIs(t, err, ports.ErrRouteNotFound)
}

// TestRouteRepository_FindByInstructionType_InvalidTenantUUID verifies non-UUID tenant returns ErrRouteNotFound.
func TestRouteRepository_FindByInstructionType_InvalidTenantUUID(t *testing.T) {
	db, ctx := setupTestDB(t)
	routeRepo := NewRouteRepository(db)

	_, err := routeRepo.FindByInstructionType(ctx, "not-a-uuid", "kyc.verify")
	require.ErrorIs(t, err, ports.ErrRouteNotFound)
}

// TestRouteRepository_ListByTenant_Empty verifies empty slice when no routes exist.
func TestRouteRepository_ListByTenant_Empty(t *testing.T) {
	db, ctx := setupTestDB(t)
	routeRepo := NewRouteRepository(db)

	routes, err := routeRepo.ListByTenant(ctx, uuid.New().String())
	require.NoError(t, err)
	assert.Empty(t, routes)
}

// TestRouteRepository_ListByTenant_InvalidUUID verifies non-UUID tenant returns empty slice (not an error).
func TestRouteRepository_ListByTenant_InvalidUUID(t *testing.T) {
	db, ctx := setupTestDB(t)
	routeRepo := NewRouteRepository(db)

	routes, err := routeRepo.ListByTenant(ctx, "not-a-uuid")
	require.NoError(t, err)
	assert.Empty(t, routes)
}

// TestRouteRepository_ListByTenant_OrderedByInstructionType verifies routes are ordered alphabetically.
func TestRouteRepository_ListByTenant_OrderedByInstructionType(t *testing.T) {
	db, ctx := setupTestDB(t)
	connRepo := NewConnectionRepository(db)
	routeRepo := NewRouteRepository(db)

	tenantID := uuid.New().String()
	connID := uuid.New().String()
	insertRouteConnection(t, connRepo, tenantID, connID)

	// Insert in reverse alphabetical order.
	for _, itype := range []string{"notification.send", "kyc.verify", "device.command"} {
		route := makeRoute(t, tenantID, itype, connID)
		require.NoError(t, routeRepo.Upsert(ctx, route))
	}

	routes, err := routeRepo.ListByTenant(ctx, tenantID)
	require.NoError(t, err)
	require.Len(t, routes, 3)

	// Results must be ordered by instruction_type ASC.
	assert.Equal(t, "device.command", routes[0].InstructionType)
	assert.Equal(t, "kyc.verify", routes[1].InstructionType)
	assert.Equal(t, "notification.send", routes[2].InstructionType)
}

// TestRouteRepository_ListByTenant_TenantIsolation verifies routes from other tenants are not returned.
func TestRouteRepository_ListByTenant_TenantIsolation(t *testing.T) {
	db, ctx := setupTestDB(t)
	connRepo := NewConnectionRepository(db)
	routeRepo := NewRouteRepository(db)

	tenant1 := uuid.New().String()
	tenant2 := uuid.New().String()
	connID1 := uuid.New().String()
	connID2 := uuid.New().String()
	insertRouteConnection(t, connRepo, tenant1, connID1)
	insertRouteConnection(t, connRepo, tenant2, connID2)

	route1 := makeRoute(t, tenant1, "kyc.verify", connID1)
	require.NoError(t, routeRepo.Upsert(ctx, route1))
	route2 := makeRoute(t, tenant2, "kyc.verify", connID2)
	require.NoError(t, routeRepo.Upsert(ctx, route2))

	routes1, err := routeRepo.ListByTenant(ctx, tenant1)
	require.NoError(t, err)
	require.Len(t, routes1, 1)
	assert.Equal(t, tenant1, routes1[0].TenantID)

	routes2, err := routeRepo.ListByTenant(ctx, tenant2)
	require.NoError(t, err)
	require.Len(t, routes2, 1)
	assert.Equal(t, tenant2, routes2[0].TenantID)
}

// TestRouteRepository_Upsert_AllFields verifies all optional fields round-trip correctly.
func TestRouteRepository_Upsert_AllFields(t *testing.T) {
	db, ctx := setupTestDB(t)
	connRepo := NewConnectionRepository(db)
	routeRepo := NewRouteRepository(db)

	tenantID := uuid.New().String()
	connID := uuid.New().String()
	fallbackID := uuid.New().String()
	insertRouteConnection(t, connRepo, tenantID, connID)
	insertRouteConnection(t, connRepo, tenantID, fallbackID)

	route := makeRoute(t, tenantID, "payment.initiate", connID)
	route.FallbackConnectionID = fallbackID
	route.OutboundMapping = "payment-outbound"
	route.InboundMapping = "payment-inbound"
	route.HTTPMethod = "POST"
	route.PathTemplate = "/v1/payments"
	require.NoError(t, routeRepo.Upsert(ctx, route))

	found, err := routeRepo.FindByInstructionType(ctx, tenantID, "payment.initiate")
	require.NoError(t, err)
	assert.Equal(t, tenantID, found.TenantID)
	assert.Equal(t, "payment.initiate", found.InstructionType)
	assert.Equal(t, connID, found.ConnectionID)
	assert.Equal(t, fallbackID, found.FallbackConnectionID)
	assert.Equal(t, "payment-outbound", found.OutboundMapping)
	assert.Equal(t, "payment-inbound", found.InboundMapping)
	assert.Equal(t, "POST", found.HTTPMethod)
	assert.Equal(t, "/v1/payments", found.PathTemplate)
	assert.False(t, found.CreatedAt.IsZero())
	assert.False(t, found.UpdatedAt.IsZero())
}

// TestRouteRepository_Upsert_ClearsOptionalFields verifies removing fallback on update works.
func TestRouteRepository_Upsert_ClearsOptionalFields(t *testing.T) {
	db, ctx := setupTestDB(t)
	connRepo := NewConnectionRepository(db)
	routeRepo := NewRouteRepository(db)

	tenantID := uuid.New().String()
	connID := uuid.New().String()
	fallbackID := uuid.New().String()
	insertRouteConnection(t, connRepo, tenantID, connID)
	insertRouteConnection(t, connRepo, tenantID, fallbackID)

	// Insert with fallback.
	route := makeRoute(t, tenantID, "kyc.check", connID)
	route.FallbackConnectionID = fallbackID
	route.OutboundMapping = "outbound-v1"
	require.NoError(t, routeRepo.Upsert(ctx, route))

	// Update without fallback (should clear it).
	updated := makeRoute(t, tenantID, "kyc.check", connID)
	updated.OutboundMapping = "outbound-v2"
	require.NoError(t, routeRepo.Upsert(ctx, updated))

	found, err := routeRepo.FindByInstructionType(ctx, tenantID, "kyc.check")
	require.NoError(t, err)
	assert.Empty(t, found.FallbackConnectionID)
	assert.Equal(t, "outbound-v2", found.OutboundMapping)
}
