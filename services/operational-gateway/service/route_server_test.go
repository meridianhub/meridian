package service

import (
	"testing"

	"github.com/google/uuid"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ========== NewInstructionRouteService logger default ==========

func TestNewInstructionRouteService_NilLogger_UsesDefault(t *testing.T) {
	svc, err := NewInstructionRouteService(newMockRouteRepo(), newMockConnectionRepo(), nil)
	require.NoError(t, err)
	assert.NotNil(t, svc)
	assert.NotNil(t, svc.logger)
}

// ========== UpsertRoute additional edge cases ==========

func TestUpsertRoute_WithAllOptionalFields(t *testing.T) {
	svc, _, connRepo := newTestRouteService(t)
	ctx := tenantContext("test-tenant")
	tid := testTenantID()
	connID := uuid.New().String()
	fallbackID := uuid.New().String()
	seedConnection(connRepo, tid, connID)
	seedConnection(connRepo, tid, fallbackID)

	resp, err := svc.UpsertRoute(ctx, &opgatewayv1.UpsertRouteRequest{
		InstructionType:      "kyc.verify",
		ConnectionId:         connID,
		FallbackConnectionId: fallbackID,
		OutboundMapping:      "kyc-outbound-v2",
		InboundMapping:       "kyc-inbound-v2",
		HttpMethod:           "PATCH",
		PathTemplate:         "/v2/verifications",
	})

	require.NoError(t, err)
	assert.Equal(t, "kyc.verify", resp.Route.InstructionType)
	assert.Equal(t, connID, resp.Route.ConnectionId)
	assert.Equal(t, fallbackID, resp.Route.FallbackConnectionId)
	assert.Equal(t, "kyc-outbound-v2", resp.Route.OutboundMapping)
	assert.Equal(t, "kyc-inbound-v2", resp.Route.InboundMapping)
	assert.Equal(t, "PATCH", resp.Route.HttpMethod)
	assert.Equal(t, "/v2/verifications", resp.Route.PathTemplate)
}

func TestUpsertRoute_IdempotentUpsert(t *testing.T) {
	svc, routeRepo, connRepo := newTestRouteService(t)
	ctx := tenantContext("test-tenant")
	tid := testTenantID()
	connID1 := uuid.New().String()
	connID2 := uuid.New().String()
	seedConnection(connRepo, tid, connID1)
	seedConnection(connRepo, tid, connID2)

	// First upsert.
	_, err := svc.UpsertRoute(ctx, &opgatewayv1.UpsertRouteRequest{
		InstructionType: "device.command",
		ConnectionId:    connID1,
		PathTemplate:    "/v1/commands",
	})
	require.NoError(t, err)

	// Second upsert with different connection (update semantics).
	resp, err := svc.UpsertRoute(ctx, &opgatewayv1.UpsertRouteRequest{
		InstructionType: "device.command",
		ConnectionId:    connID2,
		PathTemplate:    "/v2/commands",
	})
	require.NoError(t, err)
	assert.Equal(t, connID2, resp.Route.ConnectionId)
	assert.Equal(t, "/v2/commands", resp.Route.PathTemplate)

	// Only one route should exist for this tenant+type.
	routes, err := routeRepo.ListByTenant(ctx, tid)
	require.NoError(t, err)
	assert.Len(t, routes, 1)
}

// ========== GetRoute edge cases ==========

func TestGetRoute_DifferentTenants_Isolated(t *testing.T) {
	svc, routeRepo, _ := newTestRouteService(t)

	tid1 := testTenantID()

	route := &domain.Route{
		TenantID:        tid1,
		InstructionType: "kyc.verify",
		ConnectionID:    uuid.New().String(),
	}
	routeRepo.routes[tid1+":kyc.verify"] = route

	// tenant1 can find it.
	ctx1 := tenantContext("test-tenant")
	resp, err := svc.GetRoute(ctx1, &opgatewayv1.GetRouteRequest{InstructionType: "kyc.verify"})
	require.NoError(t, err)
	assert.Equal(t, "kyc.verify", resp.Route.InstructionType)

	// tenant2 gets not found.
	ctx2 := tenantContext("other-tenant")
	_, err = svc.GetRoute(ctx2, &opgatewayv1.GetRouteRequest{InstructionType: "kyc.verify"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ========== ListRoutes with multiple tenants ==========

func TestListRoutes_MultiTenantIsolation(t *testing.T) {
	svc, routeRepo, _ := newTestRouteService(t)

	tid1 := testTenantID()
	tid2 := tenantIDToUUID(tenant.TenantID("other-tenant"))

	for _, itype := range []string{"kyc.verify", "device.command"} {
		routeRepo.routes[tid1+":"+itype] = &domain.Route{TenantID: tid1, InstructionType: itype, ConnectionID: uuid.New().String()}
	}
	routeRepo.routes[tid2+":notification.send"] = &domain.Route{TenantID: tid2, InstructionType: "notification.send", ConnectionID: uuid.New().String()}

	// tenant1 sees only their 2 routes.
	ctx1 := tenantContext("test-tenant")
	resp1, err := svc.ListRoutes(ctx1, &opgatewayv1.ListRoutesRequest{})
	require.NoError(t, err)
	assert.Len(t, resp1.Routes, 2)
	for _, r := range resp1.Routes {
		assert.NotEqual(t, "notification.send", r.InstructionType)
	}

	// tenant2 sees only their 1 route.
	ctx2 := tenantContext("other-tenant")
	resp2, err := svc.ListRoutes(ctx2, &opgatewayv1.ListRoutesRequest{})
	require.NoError(t, err)
	assert.Len(t, resp2.Routes, 1)
	assert.Equal(t, "notification.send", resp2.Routes[0].InstructionType)
}

// ========== routeToProto field mapping ==========

func TestRouteToProto_EmptyOptionalFields(t *testing.T) {
	route := &domain.Route{
		TenantID:        testTenantID(),
		InstructionType: "kyc.verify",
		ConnectionID:    "conn-1",
		// All optional fields left empty.
	}

	p := routeToProto(route)
	assert.Equal(t, "kyc.verify", p.InstructionType)
	assert.Equal(t, "conn-1", p.ConnectionId)
	assert.Empty(t, p.FallbackConnectionId)
	assert.Empty(t, p.OutboundMapping)
	assert.Empty(t, p.InboundMapping)
	assert.Empty(t, p.HttpMethod)
	assert.Empty(t, p.PathTemplate)
}
