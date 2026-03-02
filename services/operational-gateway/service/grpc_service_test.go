package service

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
)

// ========== Mock repositories ==========

// mockInstructionRepo is an in-memory implementation of ports.InstructionRepository for testing.
type mockInstructionRepo struct {
	mu           sync.RWMutex
	instructions map[uuid.UUID]*domain.Instruction
	saveErr      error
	findErr      error
}

func newMockInstructionRepo() *mockInstructionRepo {
	return &mockInstructionRepo{
		instructions: make(map[uuid.UUID]*domain.Instruction),
	}
}

func (m *mockInstructionRepo) Save(_ context.Context, inst *domain.Instruction, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	stored := *inst
	m.instructions[inst.ID] = &stored
	inst.Version++
	return nil
}

func (m *mockInstructionRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.Instruction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.findErr != nil {
		return nil, m.findErr
	}
	inst, ok := m.instructions[id]
	if !ok {
		return nil, ports.ErrInstructionNotFound
	}
	stored := *inst
	return &stored, nil
}

func (m *mockInstructionRepo) ListByTenant(_ context.Context, params ports.ListInstructionsParams) ([]*domain.Instruction, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*domain.Instruction
	for _, inst := range m.instructions {
		if inst.TenantID.String() != params.TenantID {
			continue
		}
		if params.InstructionType != "" && inst.InstructionType != params.InstructionType {
			continue
		}
		if params.ProviderConnectionID != "" && inst.ProviderConnectionID != params.ProviderConnectionID {
			continue
		}
		if len(params.Statuses) > 0 {
			found := false
			for _, s := range params.Statuses {
				if inst.Status == s {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		stored := *inst
		result = append(result, &stored)
	}

	total := int64(len(result))
	// Apply offset and limit.
	if params.Offset >= len(result) {
		return []*domain.Instruction{}, total, nil
	}
	result = result[params.Offset:]
	if params.Limit > 0 && len(result) > params.Limit {
		result = result[:params.Limit]
	}
	return result, total, nil
}

func (m *mockInstructionRepo) FetchDispatchable(_ context.Context, _ ports.FetchDispatchableParams) ([]*domain.Instruction, error) {
	return nil, nil
}

func (m *mockInstructionRepo) FindExpired(_ context.Context, _ int) ([]*domain.Instruction, error) {
	return nil, nil
}

// mockConnectionRepo is an in-memory implementation of ports.ConnectionRepository for testing.
type mockConnectionRepo struct {
	mu          sync.RWMutex
	connections map[string]*domain.ProviderConnection
	upsertErr   error
	findErr     error
}

func newMockConnectionRepo() *mockConnectionRepo {
	return &mockConnectionRepo{
		connections: make(map[string]*domain.ProviderConnection),
	}
}

func (m *mockConnectionRepo) Upsert(_ context.Context, conn *domain.ProviderConnection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upsertErr != nil {
		return m.upsertErr
	}
	stored := *conn
	m.connections[conn.TenantID+":"+conn.ConnectionID] = &stored
	return nil
}

func (m *mockConnectionRepo) FindByID(_ context.Context, tenantID string, connectionID string) (*domain.ProviderConnection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.findErr != nil {
		return nil, m.findErr
	}
	conn, ok := m.connections[tenantID+":"+connectionID]
	if !ok {
		return nil, ports.ErrConnectionNotFound
	}
	stored := *conn
	return &stored, nil
}

func (m *mockConnectionRepo) ListByTenant(_ context.Context, tenantID string) ([]*domain.ProviderConnection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*domain.ProviderConnection
	for _, conn := range m.connections {
		if conn.TenantID == tenantID {
			stored := *conn
			result = append(result, &stored)
		}
	}
	return result, nil
}

func (m *mockConnectionRepo) UpdateHealth(_ context.Context, conn *domain.ProviderConnection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := conn.TenantID + ":" + conn.ConnectionID
	existing, ok := m.connections[key]
	if !ok {
		return ports.ErrConnectionNotFound
	}
	existing.HealthStatus = conn.HealthStatus
	existing.LastHealthCheckAt = conn.LastHealthCheckAt
	existing.CircuitState = conn.CircuitState
	return nil
}

// ========== Test helpers ==========

// tenantContext returns a context with the given tenant ID attached.
func tenantContext(tid string) context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(tid))
}

// testTenantUUID is a fixed UUID for use in tests as both the tenant context value
// and the UUID that tenantIDToUUID produces for "test-tenant".
func testTenantID() string {
	// tenantIDToUUID("test-tenant") → UUID v5 derived from "test-tenant"
	return tenantIDToUUID(tenant.TenantID("test-tenant"))
}

func newTestOGService(t *testing.T) (*OperationalGatewayService, *mockInstructionRepo, *mockConnectionRepo) {
	t.Helper()
	instRepo := newMockInstructionRepo()
	connRepo := newMockConnectionRepo()
	svc, err := NewOperationalGatewayService(instRepo, connRepo, nil)
	require.NoError(t, err)
	return svc, instRepo, connRepo
}

func newTestConnService(t *testing.T) (*ProviderConnectionService, *mockConnectionRepo) {
	t.Helper()
	connRepo := newMockConnectionRepo()
	instRepo := newMockInstructionRepo()
	svc, err := NewProviderConnectionService(connRepo, instRepo, nil)
	require.NoError(t, err)
	return svc, connRepo
}

// ========== DispatchInstruction tests ==========

func TestDispatchInstruction_Success(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	payload, err := structpb.NewStruct(map[string]any{"amount": 100.0})
	require.NoError(t, err)

	resp, err := svc.DispatchInstruction(ctx, &opgatewayv1.DispatchInstructionRequest{
		InstructionType: "payment.initiate",
		Payload:         payload,
		IdempotencyKey:  &commonpb.IdempotencyKey{Key: "idem-1"},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Instruction.Id)
	assert.Equal(t, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING, resp.Instruction.Status)
	assert.Equal(t, "payment.initiate", resp.Instruction.InstructionType)
}

func TestDispatchInstruction_MissingTenant(t *testing.T) {
	svc, _, _ := newTestOGService(t)

	payload, err := structpb.NewStruct(map[string]any{"amount": 100.0})
	require.NoError(t, err)

	_, err = svc.DispatchInstruction(context.Background(), &opgatewayv1.DispatchInstructionRequest{
		InstructionType: "payment.initiate",
		Payload:         payload,
		IdempotencyKey:  &commonpb.IdempotencyKey{Key: "idem-1"},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestDispatchInstruction_MissingInstructionType(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	payload, err := structpb.NewStruct(map[string]any{"amount": 100.0})
	require.NoError(t, err)

	_, err = svc.DispatchInstruction(ctx, &opgatewayv1.DispatchInstructionRequest{
		InstructionType: "",
		Payload:         payload,
		IdempotencyKey:  &commonpb.IdempotencyKey{Key: "idem-1"},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDispatchInstruction_MissingPayload(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.DispatchInstruction(ctx, &opgatewayv1.DispatchInstructionRequest{
		InstructionType: "payment.initiate",
		Payload:         nil,
		IdempotencyKey:  &commonpb.IdempotencyKey{Key: "idem-1"},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDispatchInstruction_MissingIdempotencyKey(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	payload, _ := structpb.NewStruct(map[string]any{"x": 1.0})
	_, err := svc.DispatchInstruction(ctx, &opgatewayv1.DispatchInstructionRequest{
		InstructionType: "payment.initiate",
		Payload:         payload,
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDispatchInstruction_RepoError(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	instRepo.saveErr = assert.AnError
	ctx := tenantContext("test-tenant")

	payload, _ := structpb.NewStruct(map[string]any{"x": 1.0})
	_, err := svc.DispatchInstruction(ctx, &opgatewayv1.DispatchInstructionRequest{
		InstructionType: "payment.initiate",
		Payload:         payload,
		IdempotencyKey:  &commonpb.IdempotencyKey{Key: "idem-2"},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ========== CancelInstruction tests ==========

func TestCancelInstruction_Success(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	// Pre-populate a PENDING instruction.
	tid := uuid.MustParse(testTenantID())
	inst, err := domain.NewInstruction(tid, "payment.initiate", "pending", map[string]any{"x": 1})
	require.NoError(t, err)
	inst.ID = uuid.New()
	instRepo.instructions[inst.ID] = inst

	resp, err := svc.CancelInstruction(ctx, &opgatewayv1.CancelInstructionRequest{
		InstructionId: inst.ID.String(),
	})

	require.NoError(t, err)
	assert.Equal(t, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED, resp.Instruction.Status)
}

func TestCancelInstruction_NotFound(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.CancelInstruction(ctx, &opgatewayv1.CancelInstructionRequest{
		InstructionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCancelInstruction_WrongTenant(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)

	// Store instruction under a different tenant.
	otherTenantID := uuid.New()
	inst, err := domain.NewInstruction(otherTenantID, "payment.initiate", "pending", map[string]any{"x": 1})
	require.NoError(t, err)
	inst.ID = uuid.New()
	instRepo.instructions[inst.ID] = inst

	// Request from a different tenant.
	ctx := tenantContext("test-tenant")
	_, err = svc.CancelInstruction(ctx, &opgatewayv1.CancelInstructionRequest{
		InstructionId: inst.ID.String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCancelInstruction_NotCancellable(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	tid := uuid.MustParse(testTenantID())
	inst, err := domain.NewInstruction(tid, "payment.initiate", "pending", map[string]any{"x": 1})
	require.NoError(t, err)
	inst.ID = uuid.New()
	// Transition to DISPATCHING.
	require.NoError(t, inst.MarkDispatching())
	instRepo.instructions[inst.ID] = inst

	_, err = svc.CancelInstruction(ctx, &opgatewayv1.CancelInstructionRequest{
		InstructionId: inst.ID.String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// ========== GetInstruction tests ==========

func TestGetInstruction_Success(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	tid := uuid.MustParse(testTenantID())
	inst, err := domain.NewInstruction(tid, "payment.initiate", "pending", map[string]any{"x": 1})
	require.NoError(t, err)
	inst.ID = uuid.New()
	instRepo.instructions[inst.ID] = inst

	resp, err := svc.GetInstruction(ctx, &opgatewayv1.GetInstructionRequest{
		InstructionId: inst.ID.String(),
	})

	require.NoError(t, err)
	assert.Equal(t, inst.ID.String(), resp.Instruction.Id)
	assert.Equal(t, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING, resp.Instruction.Status)
}

func TestGetInstruction_NotFound(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.GetInstruction(ctx, &opgatewayv1.GetInstructionRequest{
		InstructionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetInstruction_MissingTenant(t *testing.T) {
	svc, _, _ := newTestOGService(t)

	_, err := svc.GetInstruction(context.Background(), &opgatewayv1.GetInstructionRequest{
		InstructionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestGetInstruction_InvalidUUID(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.GetInstruction(ctx, &opgatewayv1.GetInstructionRequest{
		InstructionId: "not-a-uuid",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ========== ListInstructions tests ==========

func TestListInstructions_Success(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	tid := uuid.MustParse(testTenantID())
	for i := 0; i < 3; i++ {
		inst, err := domain.NewInstruction(tid, "payment.initiate", "pending", map[string]any{"i": float64(i)})
		require.NoError(t, err)
		inst.ID = uuid.New()
		instRepo.instructions[inst.ID] = inst
	}

	resp, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Instructions, 3)
	assert.Equal(t, int64(3), resp.Pagination.TotalCount)
}

func TestListInstructions_Pagination(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	tid := uuid.MustParse(testTenantID())
	for i := 0; i < 5; i++ {
		inst, err := domain.NewInstruction(tid, "payment.initiate", "pending", map[string]any{"i": float64(i)})
		require.NoError(t, err)
		inst.ID = uuid.New()
		instRepo.instructions[inst.ID] = inst
	}

	// First page: 2 items.
	resp1, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		Pagination: &commonpb.Pagination{PageSize: 2},
	})
	require.NoError(t, err)
	assert.Len(t, resp1.Instructions, 2)
	assert.NotEmpty(t, resp1.Pagination.NextPageToken)

	// Second page using token.
	resp2, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		Pagination: &commonpb.Pagination{
			PageSize:  2,
			PageToken: resp1.Pagination.NextPageToken,
		},
	})
	require.NoError(t, err)
	assert.Len(t, resp2.Instructions, 2)

	// Third page.
	resp3, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		Pagination: &commonpb.Pagination{
			PageSize:  2,
			PageToken: resp2.Pagination.NextPageToken,
		},
	})
	require.NoError(t, err)
	assert.Len(t, resp3.Instructions, 1)
	assert.Empty(t, resp3.Pagination.NextPageToken)
}

func TestListInstructions_PageSizeBounds(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	// Page size > maxPageSize should be clamped (no error).
	resp, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		Pagination: &commonpb.Pagination{PageSize: 999999},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestListInstructions_InvalidPageToken(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		Pagination: &commonpb.Pagination{PageToken: "garbage"},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListInstructions_MissingTenant(t *testing.T) {
	svc, _, _ := newTestOGService(t)

	_, err := svc.ListInstructions(context.Background(), &opgatewayv1.ListInstructionsRequest{})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// ========== UpsertConnection tests ==========

func TestUpsertConnection_Success(t *testing.T) {
	svc, _ := newTestConnService(t)
	ctx := tenantContext("test-tenant")

	resp, err := svc.UpsertConnection(ctx, &opgatewayv1.UpsertConnectionRequest{
		ProviderName: "Stripe",
		ProviderType: "payment_gateway",
		Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
		BaseUrl:      "https://api.stripe.com",
		AuthConfig: &opgatewayv1.UpsertConnectionRequest_ApiKey{
			ApiKey: &opgatewayv1.ApiKeyAuth{
				HeaderName: "X-API-Key",
				SecretRef:  "stripe-api-key",
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Connection)
	assert.Equal(t, "Stripe", resp.Connection.ProviderName)
	assert.Equal(t, opgatewayv1.Protocol_PROTOCOL_HTTPS, resp.Connection.Protocol)
}

func TestUpsertConnection_MissingTenant(t *testing.T) {
	svc, _ := newTestConnService(t)

	_, err := svc.UpsertConnection(context.Background(), &opgatewayv1.UpsertConnectionRequest{
		ProviderName: "Stripe",
		ProviderType: "payment_gateway",
		Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
		BaseUrl:      "https://api.stripe.com",
		AuthConfig: &opgatewayv1.UpsertConnectionRequest_ApiKey{
			ApiKey: &opgatewayv1.ApiKeyAuth{HeaderName: "X-API-Key", SecretRef: "key"},
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestUpsertConnection_WithExplicitConnectionID(t *testing.T) {
	svc, _ := newTestConnService(t)
	ctx := tenantContext("test-tenant")

	connID := uuid.New().String()
	resp, err := svc.UpsertConnection(ctx, &opgatewayv1.UpsertConnectionRequest{
		ProviderName: "Modulr",
		ProviderType: "payment_gateway",
		Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
		BaseUrl:      "https://api.modulrfinance.com",
		ConnectionId: connID,
		AuthConfig: &opgatewayv1.UpsertConnectionRequest_Basic{
			Basic: &opgatewayv1.BasicAuth{Username: "user", PasswordSecretRef: "pass-ref"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, connID, resp.Connection.ConnectionId)
}

// ========== GetConnection tests ==========

func TestGetConnection_Success(t *testing.T) {
	svc, connRepo := newTestConnService(t)
	ctx := tenantContext("test-tenant")
	tid := tenantIDToUUID(tenant.TenantID("test-tenant"))

	conn, err := domain.NewProviderConnection(tid, "Stripe", "payment_gateway", domain.ProtocolHTTPS, "https://api.stripe.com",
		&domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "key"}, domain.RetryPolicy{MaxAttempts: 3}, domain.RateLimitConfig{})
	require.NoError(t, err)
	connRepo.connections[tid+":"+conn.ConnectionID] = conn

	resp, err := svc.GetConnection(ctx, &opgatewayv1.GetConnectionRequest{ConnectionId: conn.ConnectionID})
	require.NoError(t, err)
	assert.Equal(t, conn.ConnectionID, resp.Connection.ConnectionId)
}

func TestGetConnection_NotFound(t *testing.T) {
	svc, _ := newTestConnService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.GetConnection(ctx, &opgatewayv1.GetConnectionRequest{
		ConnectionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ========== ListConnections tests ==========

func TestListConnections_Success(t *testing.T) {
	svc, connRepo := newTestConnService(t)
	ctx := tenantContext("test-tenant")
	tid := tenantIDToUUID(tenant.TenantID("test-tenant"))

	for i := 0; i < 3; i++ {
		conn, err := domain.NewProviderConnection(tid, "Provider", "type", domain.ProtocolHTTPS, "https://api.example.com",
			&domain.APIKeyAuth{HeaderName: "X-Key", SecretRef: "ref"}, domain.RetryPolicy{MaxAttempts: 3}, domain.RateLimitConfig{})
		require.NoError(t, err)
		connRepo.connections[tid+":"+conn.ConnectionID] = conn
	}

	resp, err := svc.ListConnections(ctx, &opgatewayv1.ListConnectionsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Connections, 3)
}

func TestListConnections_MissingTenant(t *testing.T) {
	svc, _ := newTestConnService(t)

	_, err := svc.ListConnections(context.Background(), &opgatewayv1.ListConnectionsRequest{})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// ========== Constructor tests ==========

func TestNewOperationalGatewayService_NilRepo(t *testing.T) {
	_, err := NewOperationalGatewayService(nil, newMockConnectionRepo(), nil)
	assert.ErrorIs(t, err, ErrInstructionRepoNil)
}

func TestNewOperationalGatewayService_NilConnRepo(t *testing.T) {
	_, err := NewOperationalGatewayService(newMockInstructionRepo(), nil, nil)
	assert.ErrorIs(t, err, ErrConnectionRepoNil)
}

func TestNewProviderConnectionService_NilConnRepo(t *testing.T) {
	_, err := NewProviderConnectionService(nil, newMockInstructionRepo(), nil)
	assert.ErrorIs(t, err, ErrConnectionRepoNil)
}

// ========== ProcessCallback tests ==========

// makeDeliveredInstruction creates an instruction in DELIVERED state and stores it in the repo.
func makeDeliveredInstruction(t *testing.T, instRepo *mockInstructionRepo) *domain.Instruction {
	t.Helper()
	tid := testTenantID()
	tenantUUID, err := uuid.Parse(tid)
	require.NoError(t, err)

	inst, err := domain.NewInstruction(tenantUUID, "payment.initiate", uuid.Nil.String(), map[string]any{"amount": 100.0})
	require.NoError(t, err)
	inst.ID = uuid.New()
	require.NoError(t, inst.MarkDispatching())
	require.NoError(t, inst.MarkDelivered())
	stored := *inst
	instRepo.instructions[inst.ID] = &stored
	return inst
}

func TestProcessCallback_AlreadyAcked(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	inst := makeDeliveredInstruction(t, instRepo)
	// Pre-transition to ACKNOWLEDGED.
	require.NoError(t, inst.MarkAcknowledged())
	instRepo.instructions[inst.ID] = inst

	resp, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  inst.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "idem-ack"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED, resp.Instruction.Status)
}

func TestProcessCallback_Success(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	inst := makeDeliveredInstruction(t, instRepo)

	resp, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  inst.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "idem-cb-1"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED, resp.Instruction.Status)
}

func TestProcessCallback_ProviderReferenceUnimplemented(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		ProviderReference: "ext-ref-123",
		IdempotencyKey:    &commonpb.IdempotencyKey{Key: "idem-ref"},
		Callback:          &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestProcessCallback_DuplicateIdempotency(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	inst := makeDeliveredInstruction(t, instRepo)
	instRepo.saveErr = ports.ErrDuplicateIdempotency

	resp, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  inst.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "idem-dup"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
}
