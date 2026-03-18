package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"gorm.io/gorm"
)

// ========== encodeOffsetToken / decodeOffsetToken ==========

func TestEncodeDecodeOffsetToken_Roundtrip(t *testing.T) {
	for _, offset := range []int{0, 1, 50, 1000} {
		token := encodeOffsetToken(offset)
		decoded, err := decodeOffsetToken(token)
		require.NoError(t, err)
		assert.Equal(t, offset, decoded)
	}
}

func TestDecodeOffsetToken_InvalidFormat(t *testing.T) {
	_, err := decodeOffsetToken("garbage")
	assert.ErrorIs(t, err, errInvalidPageTokenFormat)
}

func TestDecodeOffsetToken_InvalidNumber(t *testing.T) {
	_, err := decodeOffsetToken("offset:abc")
	assert.Error(t, err)
}

func TestDecodeOffsetToken_NegativeOffset(t *testing.T) {
	_, err := decodeOffsetToken("offset:-1")
	assert.ErrorIs(t, err, errNegativePageOffset)
}

// ========== parseDate ==========

func TestParseDate_Valid(t *testing.T) {
	d, err := parseDate("2025-03-15")
	require.NoError(t, err)
	assert.Equal(t, 2025, d.Year())
	assert.Equal(t, 3, int(d.Month()))
	assert.Equal(t, 15, d.Day())
}

func TestParseDate_Invalid(t *testing.T) {
	_, err := parseDate("not-a-date")
	assert.Error(t, err)
}

func TestParseDate_WrongFormat(t *testing.T) {
	_, err := parseDate("03/15/2025")
	assert.Error(t, err)
}

// ========== ListInstructions filter tests ==========

func TestListInstructions_StatusFilter(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")
	tid := uuid.MustParse(testTenantID())

	// Create instructions in different statuses.
	pending, err := domain.NewInstruction(tid, "device.command", "conn", map[string]any{"i": float64(1)})
	require.NoError(t, err)
	pending.ID = uuid.New()
	instRepo.instructions[pending.ID] = pending

	dispatching, err := domain.NewInstruction(tid, "device.command", "conn", map[string]any{"i": float64(2)})
	require.NoError(t, err)
	dispatching.ID = uuid.New()
	require.NoError(t, dispatching.MarkDispatching())
	instRepo.instructions[dispatching.ID] = dispatching

	resp, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		Status: []opgatewayv1.InstructionStatus{
			opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING,
		},
	})
	require.NoError(t, err)
	assert.Len(t, resp.Instructions, 1)
	assert.Equal(t, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING, resp.Instructions[0].Status)
}

func TestListInstructions_DateRange(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	resp, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		DateRange: &commonpb.DateRange{
			StartDate: "2025-01-01",
			EndDate:   "2025-12-31",
		},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestListInstructions_InvalidStartDate(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		DateRange: &commonpb.DateRange{
			StartDate: "not-a-date",
		},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListInstructions_InvalidEndDate(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		DateRange: &commonpb.DateRange{
			EndDate: "bad-date",
		},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListInstructions_InvertedDateRange(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		DateRange: &commonpb.DateRange{
			StartDate: "2025-12-31",
			EndDate:   "2025-01-01",
		},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "start_date")
}

func TestListInstructions_InstructionTypeFilter(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")
	tid := uuid.MustParse(testTenantID())

	inst1, _ := domain.NewInstruction(tid, "device.command", "conn", map[string]any{"i": float64(1)})
	inst1.ID = uuid.New()
	instRepo.instructions[inst1.ID] = inst1

	inst2, _ := domain.NewInstruction(tid, "kyc.verify", "conn", map[string]any{"i": float64(2)})
	inst2.ID = uuid.New()
	instRepo.instructions[inst2.ID] = inst2

	resp, err := svc.ListInstructions(ctx, &opgatewayv1.ListInstructionsRequest{
		InstructionType: "device.command",
	})
	require.NoError(t, err)
	assert.Len(t, resp.Instructions, 1)
}

// ========== GetInstruction - repo internal error ==========

func TestGetInstruction_RepoInternalError(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")
	instRepo.findErr = assert.AnError

	_, err := svc.GetInstruction(ctx, &opgatewayv1.GetInstructionRequest{
		InstructionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ========== ProcessCallback additional unhappy paths ==========

func TestProcessCallback_MissingTenant(t *testing.T) {
	svc, _, _ := newTestOGService(t)

	_, err := svc.ProcessCallback(context.Background(), &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  uuid.New().String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "k"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestProcessCallback_MissingIdempotencyKey(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId: uuid.New().String(),
		Callback:      &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProcessCallback_MissingCallback(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  uuid.New().String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "k"},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProcessCallback_InvalidUUID(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  "not-a-uuid",
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "k"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProcessCallback_NoIdentifier(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "k"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProcessCallback_NotFound(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  uuid.New().String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "k"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestProcessCallback_WrongTenant(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)

	otherTenantID := uuid.New()
	inst, err := domain.NewInstruction(otherTenantID, "device.command", "conn", map[string]any{"x": float64(1)})
	require.NoError(t, err)
	inst.ID = uuid.New()
	require.NoError(t, inst.MarkDispatching())
	require.NoError(t, inst.MarkDelivered())
	instRepo.instructions[inst.ID] = inst

	ctx := tenantContext("test-tenant")
	_, err = svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  inst.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "k"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestProcessCallback_NotDelivered(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	tid := uuid.MustParse(testTenantID())
	inst, err := domain.NewInstruction(tid, "device.command", "conn", map[string]any{"x": float64(1)})
	require.NoError(t, err)
	inst.ID = uuid.New()
	// Leave in PENDING — cannot be acknowledged.
	instRepo.instructions[inst.ID] = inst

	_, err = svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  inst.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "k"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestProcessCallback_RepoInternalError(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")
	instRepo.findErr = assert.AnError

	_, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  uuid.New().String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "k"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestProcessCallback_SaveConflict(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	inst := makeDeliveredInstruction(t, instRepo)
	instRepo.saveErr = ports.ErrInstructionConflict

	_, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  inst.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "k"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
}

func TestProcessCallback_SaveInternalError(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	inst := makeDeliveredInstruction(t, instRepo)
	instRepo.saveErr = assert.AnError

	_, err := svc.ProcessCallback(ctx, &opgatewayv1.ProcessCallbackRequest{
		InstructionId:  inst.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "k"},
		Callback:       &opgatewayv1.CallbackPayload{},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ========== TestConnection tests ==========

func TestTestConnection_Success(t *testing.T) {
	svc, connRepo := newTestConnService(t)
	ctx := tenantContext("test-tenant")
	tid := tenantIDToUUID("test-tenant")
	connID := uuid.New().String()

	conn, err := domain.NewProviderConnection(tid, "Provider", "type", domain.ProtocolHTTPS,
		"https://api.example.com", &domain.APIKeyAuth{HeaderName: "X-Key", SecretRef: "ref"},
		domain.RetryPolicy{MaxAttempts: 3}, domain.RateLimitConfig{})
	require.NoError(t, err)
	conn.ConnectionID = connID
	connRepo.connections[tid+":"+connID] = conn

	_, err = svc.TestConnection(ctx, &opgatewayv1.TestConnectionRequest{
		ConnectionId: connID,
	})

	// Should return Unimplemented (Phase 2 placeholder).
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestTestConnection_MissingTenant(t *testing.T) {
	svc, _ := newTestConnService(t)

	_, err := svc.TestConnection(context.Background(), &opgatewayv1.TestConnectionRequest{
		ConnectionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestTestConnection_NotFound(t *testing.T) {
	svc, _ := newTestConnService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.TestConnection(ctx, &opgatewayv1.TestConnectionRequest{
		ConnectionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestTestConnection_RepoError(t *testing.T) {
	svc, connRepo := newTestConnService(t)
	ctx := tenantContext("test-tenant")
	connRepo.findErr = assert.AnError

	_, err := svc.TestConnection(ctx, &opgatewayv1.TestConnectionRequest{
		ConnectionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ========== DispatchInstruction additional unhappy paths ==========

func TestDispatchInstruction_DuplicateIdempotencyKey(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	instRepo.saveErr = ports.ErrDuplicateIdempotency
	ctx := tenantContext("test-tenant")

	payload, _ := structpb.NewStruct(map[string]any{"x": float64(1)})
	_, err := svc.DispatchInstruction(ctx, &opgatewayv1.DispatchInstructionRequest{
		InstructionType: "device.command",
		Payload:         payload,
		IdempotencyKey:  &commonpb.IdempotencyKey{Key: "dup"},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.AlreadyExists, st.Code())
}

func TestDispatchInstruction_WithOptionalFields(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	payload, _ := structpb.NewStruct(map[string]any{"x": float64(1)})
	resp, err := svc.DispatchInstruction(ctx, &opgatewayv1.DispatchInstructionRequest{
		InstructionType: "device.command",
		Payload:         payload,
		IdempotencyKey:  &commonpb.IdempotencyKey{Key: "idem-opts"},
		Priority:        opgatewayv1.Priority_PRIORITY_HIGH,
		CorrelationId:   "corr-123",
		CausationId:     "cause-456",
		Metadata:        map[string]string{"env": "test"},
	})

	require.NoError(t, err)
	assert.Equal(t, opgatewayv1.Priority_PRIORITY_HIGH, resp.Instruction.Priority)
	assert.Equal(t, "corr-123", resp.Instruction.CorrelationId)
	assert.Equal(t, "cause-456", resp.Instruction.CausationId)
	assert.Equal(t, "test", resp.Instruction.Metadata["env"])
}

// ========== CancelInstruction additional unhappy paths ==========

func TestCancelInstruction_MissingTenant(t *testing.T) {
	svc, _, _ := newTestOGService(t)

	_, err := svc.CancelInstruction(context.Background(), &opgatewayv1.CancelInstructionRequest{
		InstructionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestCancelInstruction_InvalidUUID(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.CancelInstruction(ctx, &opgatewayv1.CancelInstructionRequest{
		InstructionId: "not-a-uuid",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCancelInstruction_RepoInternalError(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")
	instRepo.findErr = assert.AnError

	_, err := svc.CancelInstruction(ctx, &opgatewayv1.CancelInstructionRequest{
		InstructionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestCancelInstruction_SaveConflict(t *testing.T) {
	svc, instRepo, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")

	tid := uuid.MustParse(testTenantID())
	inst, err := domain.NewInstruction(tid, "device.command", "conn", map[string]any{"x": float64(1)})
	require.NoError(t, err)
	inst.ID = uuid.New()
	instRepo.instructions[inst.ID] = inst
	instRepo.saveErr = ports.ErrInstructionConflict

	_, err = svc.CancelInstruction(ctx, &opgatewayv1.CancelInstructionRequest{
		InstructionId: inst.ID.String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
}

// ========== UpsertConnection additional unhappy paths ==========

func TestUpsertConnection_MissingAuthConfig(t *testing.T) {
	svc, _ := newTestConnService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.UpsertConnection(ctx, &opgatewayv1.UpsertConnectionRequest{
		ProviderName: "Provider",
		ProviderType: "type",
		Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
		BaseUrl:      "https://api.example.com",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpsertConnection_InvalidConnectionID(t *testing.T) {
	svc, _ := newTestConnService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.UpsertConnection(ctx, &opgatewayv1.UpsertConnectionRequest{
		ProviderName: "Provider",
		ProviderType: "type",
		Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
		BaseUrl:      "https://api.example.com",
		ConnectionId: "not-a-uuid",
		AuthConfig: &opgatewayv1.UpsertConnectionRequest_ApiKey{
			ApiKey: &opgatewayv1.ApiKeyAuth{HeaderName: "X-Key", SecretRef: "ref"},
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpsertConnection_RepoError(t *testing.T) {
	svc, connRepo := newTestConnService(t)
	ctx := tenantContext("test-tenant")
	connRepo.upsertErr = assert.AnError

	_, err := svc.UpsertConnection(ctx, &opgatewayv1.UpsertConnectionRequest{
		ProviderName: "Provider",
		ProviderType: "type",
		Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
		BaseUrl:      "https://api.example.com",
		AuthConfig: &opgatewayv1.UpsertConnectionRequest_ApiKey{
			ApiKey: &opgatewayv1.ApiKeyAuth{HeaderName: "X-Key", SecretRef: "ref"},
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ========== GetConnection - repo internal error ==========

func TestGetConnection_RepoError(t *testing.T) {
	svc, connRepo := newTestConnService(t)
	ctx := tenantContext("test-tenant")
	connRepo.findErr = assert.AnError

	_, err := svc.GetConnection(ctx, &opgatewayv1.GetConnectionRequest{
		ConnectionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGetConnection_MissingTenant(t *testing.T) {
	svc, _ := newTestConnService(t)

	_, err := svc.GetConnection(context.Background(), &opgatewayv1.GetConnectionRequest{
		ConnectionId: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// ========== ListConnections additional tests ==========

func TestListConnections_Pagination(t *testing.T) {
	svc, connRepo := newTestConnService(t)
	ctx := tenantContext("test-tenant")
	tid := tenantIDToUUID("test-tenant")

	for i := 0; i < 5; i++ {
		conn, err := domain.NewProviderConnection(tid, "Provider", "type", domain.ProtocolHTTPS,
			"https://api.example.com", &domain.APIKeyAuth{HeaderName: "X-Key", SecretRef: "ref"},
			domain.RetryPolicy{MaxAttempts: 3}, domain.RateLimitConfig{})
		require.NoError(t, err)
		connRepo.connections[tid+":"+conn.ConnectionID] = conn
	}

	resp, err := svc.ListConnections(ctx, &opgatewayv1.ListConnectionsRequest{
		Pagination: &commonpb.Pagination{PageSize: 2},
	})
	require.NoError(t, err)
	assert.Len(t, resp.Connections, 2)
	assert.NotEmpty(t, resp.Pagination.NextPageToken)
}

func TestListConnections_ProtocolFilter(t *testing.T) {
	svc, connRepo := newTestConnService(t)
	ctx := tenantContext("test-tenant")
	tid := tenantIDToUUID("test-tenant")

	httpsConn, _ := domain.NewProviderConnection(tid, "HTTPS-Provider", "type", domain.ProtocolHTTPS,
		"https://api.example.com", &domain.APIKeyAuth{HeaderName: "X-Key", SecretRef: "ref"},
		domain.RetryPolicy{MaxAttempts: 3}, domain.RateLimitConfig{})
	connRepo.connections[tid+":"+httpsConn.ConnectionID] = httpsConn

	grpcConn, _ := domain.NewProviderConnection(tid, "GRPC-Provider", "type", domain.ProtocolGRPC,
		"grpc://api.example.com", &domain.APIKeyAuth{HeaderName: "X-Key", SecretRef: "ref"},
		domain.RetryPolicy{MaxAttempts: 3}, domain.RateLimitConfig{})
	connRepo.connections[tid+":"+grpcConn.ConnectionID] = grpcConn

	resp, err := svc.ListConnections(ctx, &opgatewayv1.ListConnectionsRequest{
		Protocol: opgatewayv1.Protocol_PROTOCOL_HTTPS,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Connections, 1)
	assert.Equal(t, "HTTPS-Provider", resp.Connections[0].ProviderName)
}

func TestListConnections_InvalidPageToken(t *testing.T) {
	svc, _ := newTestConnService(t)
	ctx := tenantContext("test-tenant")

	_, err := svc.ListConnections(ctx, &opgatewayv1.ListConnectionsRequest{
		Pagination: &commonpb.Pagination{PageToken: "garbage"},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ========== saveInstructionWithEvent ==========

func TestSaveInstructionWithEvent_PartialConfig(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	// Set only db but not others — partial configuration.
	svc.db = &gorm.DB{}

	tid := uuid.MustParse(testTenantID())
	inst, err := domain.NewInstruction(tid, "device.command", "conn", map[string]any{"x": float64(1)})
	require.NoError(t, err)

	err = svc.saveInstructionWithEvent(context.Background(), inst, "key", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEventPublishingPartialConfig)
}

func TestSaveInstructionWithEvent_NoPublishing(t *testing.T) {
	svc, _, _ := newTestOGService(t)

	tid := uuid.MustParse(testTenantID())
	inst, err := domain.NewInstruction(tid, "device.command", "conn", map[string]any{"x": float64(1)})
	require.NoError(t, err)

	// No event publishing configured — should fall back to plain save.
	err = svc.saveInstructionWithEvent(context.Background(), inst, "key", nil)
	require.NoError(t, err)
}

func TestSaveInstructionWithEvent_NilPublishFunc(t *testing.T) {
	svc, _, _ := newTestOGService(t)

	tid := uuid.MustParse(testTenantID())
	inst, err := domain.NewInstruction(tid, "device.command", "conn", map[string]any{"x": float64(1)})
	require.NoError(t, err)

	// No event publishing configured, publishEvent is nil.
	err = svc.saveInstructionWithEvent(context.Background(), inst, "idem-key", nil)
	require.NoError(t, err)
}

// ========== tenantIDToUUID ==========

func TestTenantIDToUUID_ValidUUID(t *testing.T) {
	id := uuid.New().String()
	// Pass a real UUID through — should come back unchanged.
	assert.Equal(t, id, tenantIDToUUID(tenant.TenantID(id)))
}

func TestTenantIDToUUID_AlphanumericSlug(t *testing.T) {
	result := tenantIDToUUID("my-org")
	_, err := uuid.Parse(result)
	assert.NoError(t, err, "should produce a valid UUID from a slug")
}

func TestTenantIDToUUID_Deterministic(t *testing.T) {
	a := tenantIDToUUID("same-slug")
	b := tenantIDToUUID("same-slug")
	assert.Equal(t, a, b)
}
