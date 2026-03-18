package service

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ========== Mock route repository ==========

type mockRouteRepo struct {
	mu        sync.RWMutex
	routes    map[string]*domain.Route // key: tenantID:instructionType
	upsertErr error
	findErr   error
	listErr   error
}

func newMockRouteRepo() *mockRouteRepo {
	return &mockRouteRepo{
		routes: make(map[string]*domain.Route),
	}
}

func (m *mockRouteRepo) Upsert(_ context.Context, route *domain.Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upsertErr != nil {
		return m.upsertErr
	}
	stored := *route
	m.routes[route.TenantID+":"+route.InstructionType] = &stored
	return nil
}

func (m *mockRouteRepo) FindByInstructionType(_ context.Context, tenantID string, instructionType string) (*domain.Route, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.findErr != nil {
		return nil, m.findErr
	}
	route, ok := m.routes[tenantID+":"+instructionType]
	if !ok {
		return nil, ports.ErrRouteNotFound
	}
	stored := *route
	return &stored, nil
}

func (m *mockRouteRepo) ListByTenant(_ context.Context, tenantID string) ([]*domain.Route, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	var result []*domain.Route
	for _, r := range m.routes {
		if r.TenantID == tenantID {
			stored := *r
			result = append(result, &stored)
		}
	}
	return result, nil
}

// ========== Test helpers ==========

func newTestRouteService(t *testing.T) (*InstructionRouteService, *mockRouteRepo, *mockConnectionRepo) {
	t.Helper()
	routeRepo := newMockRouteRepo()
	connRepo := newMockConnectionRepo()
	svc, err := NewInstructionRouteService(routeRepo, connRepo, nil)
	require.NoError(t, err)
	return svc, routeRepo, connRepo
}

func seedConnection(connRepo *mockConnectionRepo, tenantID, connectionID string) {
	conn := &domain.ProviderConnection{
		TenantID:     tenantID,
		ConnectionID: connectionID,
		ProviderName: "TestProvider",
		Protocol:     domain.ProtocolHTTPS,
		BaseURL:      "https://api.example.com",
	}
	connRepo.mu.Lock()
	connRepo.connections[tenantID+":"+connectionID] = conn
	connRepo.mu.Unlock()
}

// ========== Constructor tests ==========

func TestNewInstructionRouteService_NilRouteRepo(t *testing.T) {
	_, err := NewInstructionRouteService(nil, newMockConnectionRepo(), nil)
	assert.ErrorIs(t, err, ErrRouteRepoNil)
}

func TestNewInstructionRouteService_NilConnRepo(t *testing.T) {
	_, err := NewInstructionRouteService(newMockRouteRepo(), nil, nil)
	assert.ErrorIs(t, err, ErrConnectionRepoNil)
}

func TestNewInstructionRouteService_Success(t *testing.T) {
	svc, err := NewInstructionRouteService(newMockRouteRepo(), newMockConnectionRepo(), nil)
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

// ========== UpsertRoute tests ==========

func TestUpsertRoute_Success(t *testing.T) {
	svc, _, connRepo := newTestRouteService(t)
	ctx := tenantContext("test-tenant")
	tid := testTenantID()
	connID := uuid.New().String()
	seedConnection(connRepo, tid, connID)

	resp, err := svc.UpsertRoute(ctx, &opgatewayv1.UpsertRouteRequest{
		InstructionType: "kyc.verify",
		ConnectionId:    connID,
		OutboundMapping: "kyc-outbound",
		InboundMapping:  "kyc-inbound",
		HttpMethod:      "POST",
		PathTemplate:    "/v1/checks",
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Route)
	assert.Equal(t, "kyc.verify", resp.Route.InstructionType)
	assert.Equal(t, connID, resp.Route.ConnectionId)
	assert.Equal(t, "kyc-outbound", resp.Route.OutboundMapping)
	assert.Equal(t, "kyc-inbound", resp.Route.InboundMapping)
	assert.Equal(t, "POST", resp.Route.HttpMethod)
	assert.Equal(t, "/v1/checks", resp.Route.PathTemplate)
}

func TestUpsertRoute_MissingTenant(t *testing.T) {
	svc, _, _ := newTestRouteService(t)

	_, err := svc.UpsertRoute(context.Background(), &opgatewayv1.UpsertRouteRequest{
		InstructionType: "kyc.verify",
		ConnectionId:    uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestUpsertRoute_MissingInstructionType(t *testing.T) {
	svc, _, _ := newTestRouteService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.UpsertRoute(ctx, &opgatewayv1.UpsertRouteRequest{
		ConnectionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpsertRoute_MissingConnectionID(t *testing.T) {
	svc, _, _ := newTestRouteService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.UpsertRoute(ctx, &opgatewayv1.UpsertRouteRequest{
		InstructionType: "kyc.verify",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpsertRoute_ConnectionNotFound(t *testing.T) {
	svc, _, _ := newTestRouteService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.UpsertRoute(ctx, &opgatewayv1.UpsertRouteRequest{
		InstructionType: "kyc.verify",
		ConnectionId:    uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUpsertRoute_ConnectionRepoError(t *testing.T) {
	svc, _, connRepo := newTestRouteService(t)
	ctx := tenantContext("test-tenant")
	connRepo.findErr = assert.AnError

	_, err := svc.UpsertRoute(ctx, &opgatewayv1.UpsertRouteRequest{
		InstructionType: "kyc.verify",
		ConnectionId:    uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestUpsertRoute_FallbackConnectionNotFound(t *testing.T) {
	svc, _, connRepo := newTestRouteService(t)
	ctx := tenantContext("test-tenant")
	tid := testTenantID()
	connID := uuid.New().String()
	seedConnection(connRepo, tid, connID)

	_, err := svc.UpsertRoute(ctx, &opgatewayv1.UpsertRouteRequest{
		InstructionType:      "kyc.verify",
		ConnectionId:         connID,
		FallbackConnectionId: uuid.New().String(), // does not exist
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "fallback")
}

func TestUpsertRoute_RouteRepoError(t *testing.T) {
	svc, routeRepo, connRepo := newTestRouteService(t)
	ctx := tenantContext("test-tenant")
	tid := testTenantID()
	connID := uuid.New().String()
	seedConnection(connRepo, tid, connID)
	routeRepo.upsertErr = assert.AnError

	_, err := svc.UpsertRoute(ctx, &opgatewayv1.UpsertRouteRequest{
		InstructionType: "kyc.verify",
		ConnectionId:    connID,
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestUpsertRoute_WithFallback(t *testing.T) {
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
	})

	require.NoError(t, err)
	assert.Equal(t, fallbackID, resp.Route.FallbackConnectionId)
}

// ========== GetRoute tests ==========

func TestGetRoute_Success(t *testing.T) {
	svc, routeRepo, _ := newTestRouteService(t)
	ctx := tenantContext("test-tenant")
	tid := testTenantID()

	route, err := domain.NewRoute(tid, "kyc.verify", uuid.New().String())
	require.NoError(t, err)
	routeRepo.routes[tid+":kyc.verify"] = route

	resp, err := svc.GetRoute(ctx, &opgatewayv1.GetRouteRequest{
		InstructionType: "kyc.verify",
	})

	require.NoError(t, err)
	assert.Equal(t, "kyc.verify", resp.Route.InstructionType)
}

func TestGetRoute_MissingInstructionType(t *testing.T) {
	svc, _, _ := newTestRouteService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.GetRoute(ctx, &opgatewayv1.GetRouteRequest{})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetRoute_MissingTenant(t *testing.T) {
	svc, _, _ := newTestRouteService(t)

	_, err := svc.GetRoute(context.Background(), &opgatewayv1.GetRouteRequest{
		InstructionType: "kyc.verify",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestGetRoute_NotFound(t *testing.T) {
	svc, _, _ := newTestRouteService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.GetRoute(ctx, &opgatewayv1.GetRouteRequest{
		InstructionType: "nonexistent.type",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetRoute_RepoError(t *testing.T) {
	svc, routeRepo, _ := newTestRouteService(t)
	ctx := tenantContext("test-tenant")
	routeRepo.findErr = assert.AnError

	_, err := svc.GetRoute(ctx, &opgatewayv1.GetRouteRequest{
		InstructionType: "kyc.verify",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ========== ListRoutes tests ==========

func TestListRoutes_Success(t *testing.T) {
	svc, routeRepo, _ := newTestRouteService(t)
	ctx := tenantContext("test-tenant")
	tid := testTenantID()

	for _, itype := range []string{"kyc.verify", "device.command", "notification.send"} {
		route, err := domain.NewRoute(tid, itype, uuid.New().String())
		require.NoError(t, err)
		routeRepo.routes[tid+":"+itype] = route
	}

	resp, err := svc.ListRoutes(ctx, &opgatewayv1.ListRoutesRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Routes, 3)
}

func TestListRoutes_MissingTenant(t *testing.T) {
	svc, _, _ := newTestRouteService(t)

	_, err := svc.ListRoutes(context.Background(), &opgatewayv1.ListRoutesRequest{})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestListRoutes_Empty(t *testing.T) {
	svc, _, _ := newTestRouteService(t)
	ctx := tenantContext("test-tenant")

	resp, err := svc.ListRoutes(ctx, &opgatewayv1.ListRoutesRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Routes)
}

func TestListRoutes_RepoError(t *testing.T) {
	svc, routeRepo, _ := newTestRouteService(t)
	ctx := tenantContext("test-tenant")
	routeRepo.listErr = assert.AnError

	_, err := svc.ListRoutes(ctx, &opgatewayv1.ListRoutesRequest{})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ========== routeToProto ==========

func TestRouteToProto(t *testing.T) {
	route := &domain.Route{
		TenantID:             testTenantID(),
		InstructionType:      "kyc.verify",
		ConnectionID:         "conn-1",
		FallbackConnectionID: "conn-2",
		OutboundMapping:      "outbound",
		InboundMapping:       "inbound",
		HTTPMethod:           "POST",
		PathTemplate:         "/v1/checks",
	}

	p := routeToProto(route)
	assert.Equal(t, "kyc.verify", p.InstructionType)
	assert.Equal(t, "conn-1", p.ConnectionId)
	assert.Equal(t, "conn-2", p.FallbackConnectionId)
	assert.Equal(t, "outbound", p.OutboundMapping)
	assert.Equal(t, "inbound", p.InboundMapping)
	assert.Equal(t, "POST", p.HttpMethod)
	assert.Equal(t, "/v1/checks", p.PathTemplate)
}
