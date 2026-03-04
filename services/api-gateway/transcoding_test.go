package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/shared/platform/await"
)

// ---------------------------------------------------------------------------
// Mock gRPC service implementations
// ---------------------------------------------------------------------------

// mockPartyService implements the PartyService gRPC server for testing.
type mockPartyService struct {
	partyv1.UnimplementedPartyServiceServer
	mu           sync.RWMutex
	lastMetadata metadata.MD // captured from most recent call
}

func (m *mockPartyService) captureMetadata(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	m.mu.Lock()
	m.lastMetadata = md
	m.mu.Unlock()
}

func (m *mockPartyService) LastMetadata() metadata.MD {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := metadata.MD{}
	for k, v := range m.lastMetadata {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func (m *mockPartyService) RegisterParty(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.RegisterPartyResponse, error) {
	m.captureMetadata(ctx)
	return &partyv1.RegisterPartyResponse{
		Party: &partyv1.Party{
			PartyId:     "party-001",
			PartyType:   req.PartyType,
			LegalName:   req.LegalName,
			DisplayName: req.DisplayName,
			Status:      partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
			CreatedAt:   timestamppb.Now(),
			UpdatedAt:   timestamppb.Now(),
			Version:     1,
		},
	}, nil
}

func (m *mockPartyService) RetrieveParty(ctx context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
	m.captureMetadata(ctx)
	switch req.PartyId {
	case "not-found":
		return nil, status.Errorf(codes.NotFound, "party %q not found", req.PartyId)
	case "invalid":
		return nil, status.Errorf(codes.InvalidArgument, "invalid party_id format")
	case "unauthenticated":
		return nil, status.Errorf(codes.Unauthenticated, "authentication required")
	case "permission-denied":
		return nil, status.Errorf(codes.PermissionDenied, "insufficient permissions")
	case "internal":
		return nil, status.Errorf(codes.Internal, "internal server error")
	case "deadline-exceeded":
		return nil, status.Errorf(codes.DeadlineExceeded, "deadline exceeded")
	case "unavailable":
		return nil, status.Errorf(codes.Unavailable, "service unavailable")
	case "data-loss":
		return nil, status.Errorf(codes.DataLoss, "data loss occurred")
	case "unknown":
		return nil, status.Errorf(codes.Unknown, "unknown error")
	case "already-exists":
		return nil, status.Errorf(codes.AlreadyExists, "party already exists")
	case "resource-exhausted":
		return nil, status.Errorf(codes.ResourceExhausted, "quota exceeded")
	case "unimplemented":
		return nil, status.Errorf(codes.Unimplemented, "method not implemented")
	}
	return &partyv1.RetrievePartyResponse{
		Party: &partyv1.Party{
			PartyId:     req.PartyId,
			PartyType:   partyv1.PartyType_PARTY_TYPE_PERSON,
			LegalName:   "Jane Doe",
			DisplayName: "Jane",
			Status:      partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
			CreatedAt:   timestamppb.New(time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)),
			UpdatedAt:   timestamppb.New(time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)),
			Version:     3,
		},
	}, nil
}

// mockTenantService implements the TenantService gRPC server for testing.
type mockTenantService struct {
	tenantv1.UnimplementedTenantServiceServer
}

func (m *mockTenantService) RetrieveTenant(_ context.Context, req *tenantv1.RetrieveTenantRequest) (*tenantv1.RetrieveTenantResponse, error) {
	if req.TenantId == "not-found" {
		return nil, status.Errorf(codes.NotFound, "tenant %q not found", req.TenantId)
	}
	return &tenantv1.RetrieveTenantResponse{
		Tenant: &tenantv1.Tenant{
			TenantId:        req.TenantId,
			DisplayName:     "Acme Corp",
			SettlementAsset: "GBP",
			Status:          tenantv1.TenantStatus_TENANT_STATUS_ACTIVE,
			CreatedAt:       timestamppb.Now(),
			Version:         1,
			Slug:            "acme-corp",
		},
	}, nil
}

func (m *mockTenantService) ListTenants(_ context.Context, _ *tenantv1.ListTenantsRequest) (*tenantv1.ListTenantsResponse, error) {
	return &tenantv1.ListTenantsResponse{
		Tenants: []*tenantv1.Tenant{
			{
				TenantId:        "tenant_1",
				DisplayName:     "Acme Corp",
				SettlementAsset: "GBP",
				Status:          tenantv1.TenantStatus_TENANT_STATUS_ACTIVE,
				Slug:            "acme-corp",
			},
		},
	}, nil
}

// mockSagaRegistryService implements the SagaRegistryService gRPC server for testing.
type mockSagaRegistryService struct {
	sagav1.UnimplementedSagaRegistryServiceServer
}

func (m *mockSagaRegistryService) ListSagas(_ context.Context, _ *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error) {
	return &sagav1.ListSagasResponse{
		Sagas: []*sagav1.SagaDefinition{
			{
				Id:          "550e8400-e29b-41d4-a716-446655440000",
				Name:        "test_saga",
				Version:     1,
				Status:      sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
				DisplayName: "Test Saga",
				Description: "A test saga for integration testing",
			},
		},
	}, nil
}

func (m *mockSagaRegistryService) ValidateSaga(_ context.Context, req *sagav1.ValidateSagaRequest) (*sagav1.ValidateSagaResponse, error) {
	return &sagav1.ValidateSagaResponse{
		Success: true,
		Metrics: &sagav1.ComplexityMetrics{
			HandlerCallCount: 1,
			OperationCount:   5,
			ComplexityScore:  2,
		},
		FormattedReport: fmt.Sprintf("Validation passed for %s", req.SagaName),
	}, nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// transcodingTestEnv bundles the gRPC server, gateway server, and test URLs
// needed by integration tests.
type transcodingTestEnv struct {
	grpcServer    *grpc.Server
	gatewayServer *Server
	baseURL       string
	mockParty     *mockPartyService
}

// startTranscodingTestEnv boots a mock gRPC server and a gateway with the
// Vanguard transcoder wired to it. It returns the test environment and a
// cleanup function.
func startTranscodingTestEnv(t *testing.T, backends []ServiceBackend) *transcodingTestEnv {
	t.Helper()
	ctx := context.Background()

	// Start mock gRPC server on a random port.
	grpcListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcPort := grpcListener.Addr().(*net.TCPAddr).Port

	grpcServer := grpc.NewServer()
	mockParty := &mockPartyService{}
	partyv1.RegisterPartyServiceServer(grpcServer, mockParty)
	tenantv1.RegisterTenantServiceServer(grpcServer, &mockTenantService{})
	sagav1.RegisterSagaRegistryServiceServer(grpcServer, &mockSagaRegistryService{})

	go func() { _ = grpcServer.Serve(grpcListener) }()

	// Rewrite backend addresses to point at the mock gRPC server.
	grpcAddr := fmt.Sprintf("127.0.0.1:%d", grpcPort)
	for i := range backends {
		backends[i].BackendAddr = grpcAddr
	}

	// Build transcoder and gateway.
	transcoder, err := NewTranscoder(testDescriptorBytes, backends)
	require.NoError(t, err)

	httpPort := getAvailablePort(ctx, t)
	config := &Config{
		Port:        httpPort,
		BaseDomain:  "api.test.io",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := NewServer(config, logger, nil, WithTranscoder(transcoder))

	serverCtx, cancel := context.WithCancel(ctx)

	go func() { _ = gwServer.Start(serverCtx) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			resp, err := httpGet(ctx, baseURL+"/health")
			if err != nil {
				return false
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		})
	require.NoError(t, err, "gateway failed to become ready")

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = gwServer.Shutdown(shutdownCtx)
		grpcServer.GracefulStop()
		cancel()
	})

	return &transcodingTestEnv{
		grpcServer:    grpcServer,
		gatewayServer: gwServer,
		baseURL:       baseURL,
		mockParty:     mockParty,
	}
}

// readJSONBody reads and unmarshals an HTTP response body into a map.
func readJSONBody(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	require.NoError(t, err, "failed to unmarshal JSON response: %s", string(body))
	return result
}

// ---------------------------------------------------------------------------
// Integration Tests
// ---------------------------------------------------------------------------

// TestTranscoding_PartyService_RegisterParty tests POST /v1/parties end-to-end.
func TestTranscoding_PartyService_RegisterParty(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	body := `{
		"partyType": "PARTY_TYPE_PERSON",
		"legalName": "Alice Smith",
		"displayName": "Alice"
	}`

	resp, err := httpPost(context.Background(), env.baseURL+"/v1/parties", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	result := readJSONBody(t, resp)
	party, ok := result["party"].(map[string]interface{})
	require.True(t, ok, "expected 'party' object in response, got: %v", result)

	assert.Equal(t, "party-001", party["partyId"])
	assert.Equal(t, "Alice Smith", party["legalName"])
	assert.Equal(t, "Alice", party["displayName"])
	// Enum serialized as string
	assert.Equal(t, "PARTY_TYPE_PERSON", party["partyType"])
	assert.Equal(t, "PARTY_STATUS_ACTIVE", party["status"])
	// Timestamps in RFC3339
	assert.NotEmpty(t, party["createdAt"])
	assert.NotEmpty(t, party["updatedAt"])
}

// TestTranscoding_PartyService_RetrieveParty tests GET /v1/parties/{party_id}.
func TestTranscoding_PartyService_RetrieveParty(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/party-123")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	result := readJSONBody(t, resp)
	party, ok := result["party"].(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, "party-123", party["partyId"])
	assert.Equal(t, "PARTY_TYPE_PERSON", party["partyType"])
	assert.Equal(t, "Jane Doe", party["legalName"])
	// RFC3339 timestamp format
	createdAt, ok := party["createdAt"].(string)
	require.True(t, ok)
	_, err = time.Parse(time.RFC3339Nano, createdAt)
	assert.NoError(t, err, "createdAt should be RFC3339: %s", createdAt)
}

// TestTranscoding_TenantService_RetrieveTenant tests GET /v1/tenants/{tenant_id}.
func TestTranscoding_TenantService_RetrieveTenant(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.tenant.v1.TenantService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/tenants/acme_corp")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	result := readJSONBody(t, resp)
	tenant, ok := result["tenant"].(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, "acme_corp", tenant["tenantId"])
	assert.Equal(t, "Acme Corp", tenant["displayName"])
	assert.Equal(t, "GBP", tenant["settlementAsset"])
	assert.Equal(t, "TENANT_STATUS_ACTIVE", tenant["status"])
	assert.Equal(t, "acme-corp", tenant["slug"])
}

// TestTranscoding_TenantService_ListTenants tests GET /v1/tenants.
func TestTranscoding_TenantService_ListTenants(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.tenant.v1.TenantService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/tenants")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	result := readJSONBody(t, resp)
	tenants, ok := result["tenants"].([]interface{})
	require.True(t, ok, "expected 'tenants' array in response, got: %v", result)
	assert.Len(t, tenants, 1)
}

// TestTranscoding_SagaRegistryService_ListSagas tests GET /v1/sagas.
func TestTranscoding_SagaRegistryService_ListSagas(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.saga.v1.SagaRegistryService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/sagas")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	result := readJSONBody(t, resp)
	sagas, ok := result["sagas"].([]interface{})
	require.True(t, ok, "expected 'sagas' array, got: %v", result)
	assert.Len(t, sagas, 1)

	saga := sagas[0].(map[string]interface{})
	assert.Equal(t, "test_saga", saga["name"])
	assert.Equal(t, "SAGA_STATUS_ACTIVE", saga["status"])
}

// TestTranscoding_SagaRegistryService_ValidateSaga tests POST /v1/sagas/validate.
func TestTranscoding_SagaRegistryService_ValidateSaga(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.saga.v1.SagaRegistryService"},
	})

	body := `{
		"sagaName": "test_saga",
		"script": "result = payment.create_lien(amount=\"100.00\")",
		"version": "1.0.0"
	}`

	resp, err := httpPost(context.Background(), env.baseURL+"/v1/sagas/validate", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, true, result["success"])
	metrics, ok := result["metrics"].(map[string]interface{})
	require.True(t, ok)
	assert.NotNil(t, metrics["handlerCallCount"])
}

// TestTranscoding_MultipleServices tests that multiple services can be registered
// and routed correctly through a single transcoder.
func TestTranscoding_MultipleServices(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
		{ServiceName: "meridian.tenant.v1.TenantService"},
		{ServiceName: "meridian.saga.v1.SagaRegistryService"},
	})

	ctx := context.Background()

	t.Run("party service via /v1/parties", func(t *testing.T) {
		resp, err := httpGet(ctx, env.baseURL+"/v1/parties/multi-test")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		result := readJSONBody(t, resp)
		party := result["party"].(map[string]interface{})
		assert.Equal(t, "multi-test", party["partyId"])
	})

	t.Run("tenant service via /v1/tenants", func(t *testing.T) {
		resp, err := httpGet(ctx, env.baseURL+"/v1/tenants")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		result := readJSONBody(t, resp)
		_, ok := result["tenants"].([]interface{})
		assert.True(t, ok)
	})

	t.Run("saga service via /v1/sagas", func(t *testing.T) {
		resp, err := httpGet(ctx, env.baseURL+"/v1/sagas")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		result := readJSONBody(t, resp)
		_, ok := result["sagas"].([]interface{})
		assert.True(t, ok)
	})
}

// ---------------------------------------------------------------------------
// Proto3 JSON Serialization Validation
// ---------------------------------------------------------------------------

// TestTranscoding_Proto3JSON_CamelCaseFields verifies that proto3 JSON uses
// camelCase field names (not snake_case).
func TestTranscoding_Proto3JSON_CamelCaseFields(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/camel-test")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	bodyStr := string(body)

	// Proto3 JSON uses camelCase
	assert.Contains(t, bodyStr, "partyId")
	assert.Contains(t, bodyStr, "partyType")
	assert.Contains(t, bodyStr, "legalName")
	assert.Contains(t, bodyStr, "displayName")
	assert.Contains(t, bodyStr, "createdAt")
	assert.Contains(t, bodyStr, "updatedAt")

	// Should NOT contain snake_case
	assert.NotContains(t, bodyStr, "party_id")
	assert.NotContains(t, bodyStr, "party_type")
	assert.NotContains(t, bodyStr, "legal_name")
	assert.NotContains(t, bodyStr, "display_name")
	assert.NotContains(t, bodyStr, "created_at")
	assert.NotContains(t, bodyStr, "updated_at")
}

// TestTranscoding_Proto3JSON_EnumAsStrings verifies that enum values are
// serialized as their string names, not numeric values.
func TestTranscoding_Proto3JSON_EnumAsStrings(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/enum-test")
	require.NoError(t, err)
	defer resp.Body.Close()

	result := readJSONBody(t, resp)
	party := result["party"].(map[string]interface{})

	// Enums as strings
	assert.Equal(t, "PARTY_TYPE_PERSON", party["partyType"])
	assert.Equal(t, "PARTY_STATUS_ACTIVE", party["status"])
}

// TestTranscoding_Proto3JSON_RFC3339Timestamps verifies that google.protobuf.Timestamp
// fields are serialized as RFC3339 strings.
func TestTranscoding_Proto3JSON_RFC3339Timestamps(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/ts-test")
	require.NoError(t, err)
	defer resp.Body.Close()

	result := readJSONBody(t, resp)
	party := result["party"].(map[string]interface{})

	for _, field := range []string{"createdAt", "updatedAt"} {
		ts, ok := party[field].(string)
		require.True(t, ok, "expected string for %s", field)
		parsed, err := time.Parse(time.RFC3339Nano, ts)
		assert.NoError(t, err, "%s should be RFC3339: %s", field, ts)
		assert.False(t, parsed.IsZero(), "%s should not be zero time", field)
	}
}

// TestTranscoding_Proto3JSON_NestedMessage verifies that nested proto messages
// are serialized correctly in JSON.
func TestTranscoding_Proto3JSON_NestedMessage(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	// RegisterParty returns a response with a nested Party message.
	body := `{
		"partyType": "PARTY_TYPE_ORGANIZATION",
		"legalName": "Nested Corp",
		"displayName": "NC"
	}`

	resp, err := httpPost(context.Background(), env.baseURL+"/v1/parties", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	result := readJSONBody(t, resp)
	// Response wraps party in a "party" field (nested message)
	party, ok := result["party"].(map[string]interface{})
	require.True(t, ok, "expected nested 'party' object")
	assert.Equal(t, "Nested Corp", party["legalName"])
}

// ---------------------------------------------------------------------------
// Error Response Mapping (gRPC code -> HTTP status)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Error Response Format Tests
// ---------------------------------------------------------------------------

// TestTranscoding_Error_NotFound tests gRPC NOT_FOUND -> HTTP 404 with canonical error body.
func TestTranscoding_Error_NotFound(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/not-found")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, "NOT_FOUND", result["code"], "error code should be string NOT_FOUND")
	assert.NotEmpty(t, result["error"], "error response should contain an error field")
	assert.Contains(t, result["error"], "not found")
}

// TestTranscoding_Error_InvalidArgument tests gRPC INVALID_ARGUMENT -> HTTP 400 with canonical error body.
func TestTranscoding_Error_InvalidArgument(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/invalid")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, "INVALID_ARGUMENT", result["code"])
	assert.NotEmpty(t, result["error"])
}

// TestTranscoding_Error_Unauthenticated tests gRPC UNAUTHENTICATED -> HTTP 401.
func TestTranscoding_Error_Unauthenticated(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/unauthenticated")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, "UNAUTHENTICATED", result["code"])
	assert.Equal(t, "authentication required", result["error"])
}

// TestTranscoding_Error_PermissionDenied tests gRPC PERMISSION_DENIED -> HTTP 403.
func TestTranscoding_Error_PermissionDenied(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/permission-denied")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, "PERMISSION_DENIED", result["code"])
	assert.NotEmpty(t, result["error"])
}

// TestTranscoding_Error_Internal tests gRPC INTERNAL -> HTTP 500.
func TestTranscoding_Error_Internal(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/internal")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, "INTERNAL", result["code"])
	assert.NotEmpty(t, result["error"])
}

// TestTranscoding_Error_DeadlineExceeded tests gRPC DEADLINE_EXCEEDED -> HTTP 504.
func TestTranscoding_Error_DeadlineExceeded(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/deadline-exceeded")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, "DEADLINE_EXCEEDED", result["code"])
	assert.NotEmpty(t, result["error"])
}

// TestTranscoding_Error_Unavailable tests gRPC UNAVAILABLE -> HTTP 503.
func TestTranscoding_Error_Unavailable(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/unavailable")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, "UNAVAILABLE", result["code"])
	assert.NotEmpty(t, result["error"])
}

// TestTranscoding_Error_DataLoss tests gRPC DATA_LOSS -> HTTP 500.
func TestTranscoding_Error_DataLoss(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/data-loss")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, "DATA_LOSS", result["code"])
	assert.NotEmpty(t, result["error"])
}

// TestTranscoding_Error_Unknown tests gRPC UNKNOWN -> HTTP 500.
func TestTranscoding_Error_Unknown(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/unknown")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, "UNKNOWN", result["code"])
	assert.NotEmpty(t, result["error"])
}

// TestTranscoding_Error_AllStandardCodes verifies that all standard gRPC->HTTP
// status code mappings produce the canonical error body format.
func TestTranscoding_Error_AllStandardCodes(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	cases := []struct {
		partyID    string
		httpStatus int
		grpcCode   string
	}{
		{"not-found", http.StatusNotFound, "NOT_FOUND"},
		{"invalid", http.StatusBadRequest, "INVALID_ARGUMENT"},
		{"unauthenticated", http.StatusUnauthorized, "UNAUTHENTICATED"},
		{"permission-denied", http.StatusForbidden, "PERMISSION_DENIED"},
		{"internal", http.StatusInternalServerError, "INTERNAL"},
		{"deadline-exceeded", http.StatusGatewayTimeout, "DEADLINE_EXCEEDED"},
		{"unavailable", http.StatusServiceUnavailable, "UNAVAILABLE"},
		{"data-loss", http.StatusInternalServerError, "DATA_LOSS"},
		{"unknown", http.StatusInternalServerError, "UNKNOWN"},
		{"already-exists", http.StatusConflict, "ALREADY_EXISTS"},
		{"resource-exhausted", http.StatusTooManyRequests, "RESOURCE_EXHAUSTED"},
		{"unimplemented", http.StatusNotImplemented, "UNIMPLEMENTED"},
	}

	for _, tc := range cases {
		t.Run(tc.grpcCode, func(t *testing.T) {
			resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/"+tc.partyID)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.httpStatus, resp.StatusCode, "HTTP status for %s", tc.grpcCode)

			result := readJSONBody(t, resp)
			assert.Equal(t, tc.grpcCode, result["code"], "code field for %s", tc.grpcCode)
			assert.NotEmpty(t, result["error"], "error field for %s", tc.grpcCode)
		})
	}
}

// TestTranscoding_Error_BodyFormat verifies the canonical error body has the
// correct fields: error (string), code (string), and no numeric code field.
func TestTranscoding_Error_BodyFormat(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/not-found")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	result := readJSONBody(t, resp)

	// Must have string code
	code, ok := result["code"].(string)
	require.True(t, ok, "code field must be a string, got %T", result["code"])
	assert.Equal(t, "NOT_FOUND", code)

	// Must have error message
	errMsg, ok := result["error"].(string)
	require.True(t, ok, "error field must be a string, got %T", result["error"])
	assert.NotEmpty(t, errMsg)

	// Must NOT have numeric code or message (Vanguard's original format)
	_, hasNumericCode := result["message"]
	assert.False(t, hasNumericCode, "response must not contain 'message' field (Vanguard raw format)")
}

// TestTranscoding_Error_ContentTypeJSON verifies that error responses are returned
// with application/json Content-Type.
func TestTranscoding_Error_ContentTypeJSON(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/not-found")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TestTranscoding_Error_MalformedRequestBody tests behavior with invalid JSON body.
// Vanguard attempts to transcode the body to proto; when that fails the gRPC backend
// receives an UNKNOWN/INTERNAL error which maps to HTTP 500.
func TestTranscoding_Error_MalformedRequestBody(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	// Send malformed JSON to a POST endpoint
	resp, err := httpPost(context.Background(), env.baseURL+"/v1/parties", "application/json", strings.NewReader("this is not json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Malformed JSON results in a proto parsing error; Vanguard maps this to UNKNOWN → 500.
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// Response should still be JSON with our canonical error format
	ct := resp.Header.Get("Content-Type")
	assert.Contains(t, ct, "application/json")

	result := readJSONBody(t, resp)
	assert.NotEmpty(t, result["error"], "error field must be present")
	code, ok := result["code"].(string)
	assert.True(t, ok, "code must be a string")
	assert.NotEmpty(t, code)
}

// TestTranscoding_Error_NotFoundTenant tests gRPC NOT_FOUND -> HTTP 404 for tenant.
func TestTranscoding_Error_NotFoundTenant(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.tenant.v1.TenantService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/tenants/not-found")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	result := readJSONBody(t, resp)
	assert.Equal(t, "NOT_FOUND", result["code"])
}

// TestTranscoding_Error_UnknownRoute tests that an unknown route returns 404.
func TestTranscoding_Error_UnknownRoute(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/nonexistent/path")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Vanguard returns 404 for unknown routes that don't match any registered service
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Metadata Propagation
// ---------------------------------------------------------------------------

// TestTranscoding_MetadataPropagation verifies that identity headers set in the
// request context are propagated as gRPC metadata to the backend.
func TestTranscoding_MetadataPropagation(t *testing.T) {
	ctx := context.Background()

	// Start mock gRPC server.
	grpcListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcPort := grpcListener.Addr().(*net.TCPAddr).Port

	grpcServer := grpc.NewServer()
	mockParty := &mockPartyService{}
	partyv1.RegisterPartyServiceServer(grpcServer, mockParty)

	go func() { _ = grpcServer.Serve(grpcListener) }()
	t.Cleanup(grpcServer.GracefulStop)

	grpcAddr := fmt.Sprintf("127.0.0.1:%d", grpcPort)

	// Build transcoder.
	transcoder, err := NewTranscoder(testDescriptorBytes, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService", BackendAddr: grpcAddr},
	})
	require.NoError(t, err)

	httpPort := getAvailablePort(ctx, t)
	config := &Config{
		Port:        httpPort,
		BaseDomain:  "api.test.io",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := NewServer(config, logger, nil, WithTranscoder(transcoder))

	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() { _ = gwServer.Start(serverCtx) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			resp, err := httpGet(ctx, baseURL+"/health")
			if err != nil {
				return false
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		})
	require.NoError(t, err)

	t.Run("identity headers stripped from client requests", func(t *testing.T) {
		// Send request with spoofed identity headers.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/parties/meta-test", nil)
		require.NoError(t, err)
		req.Header.Set("X-User-ID", "spoofed-user")
		req.Header.Set("X-Tenant-ID", "spoofed-tenant")
		req.Header.Set("X-Auth-Method", "jwt")
		req.Header.Set("X-Auth-Roles", "admin")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Without auth middleware, no identity is set in context, so the
		// metadataPropagationMiddleware strips incoming spoofed headers
		// and writes nothing (no auth context). The gRPC backend should
		// NOT receive the spoofed values.
		md := mockParty.LastMetadata()
		assert.Empty(t, md.Get("x-user-id"), "spoofed x-user-id should be stripped")
		assert.Empty(t, md.Get("x-tenant-id"), "spoofed x-tenant-id should be stripped")
	})

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()
	_ = gwServer.Shutdown(shutdownCtx)
}

// ---------------------------------------------------------------------------
// Request body with snake_case accepted
// ---------------------------------------------------------------------------

// TestTranscoding_RequestBody_AcceptsCamelCase verifies that the transcoder
// accepts camelCase field names in the JSON request body (proto3 canonical).
func TestTranscoding_RequestBody_AcceptsCamelCase(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	// camelCase field names
	body := `{"partyType":"PARTY_TYPE_PERSON","legalName":"Camel Alice","displayName":"CA"}`

	resp, err := httpPost(context.Background(), env.baseURL+"/v1/parties", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	result := readJSONBody(t, resp)
	party := result["party"].(map[string]interface{})
	assert.Equal(t, "Camel Alice", party["legalName"])
}

// TestTranscoding_RequestBody_AcceptsSnakeCase verifies that the transcoder
// also accepts snake_case field names in the JSON request body (proto3
// allows both by default).
func TestTranscoding_RequestBody_AcceptsSnakeCase(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	// snake_case field names
	body := `{"party_type":"PARTY_TYPE_PERSON","legal_name":"Snake Alice","display_name":"SA"}`

	resp, err := httpPost(context.Background(), env.baseURL+"/v1/parties", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	result := readJSONBody(t, resp)
	party := result["party"].(map[string]interface{})
	assert.Equal(t, "Snake Alice", party["legalName"])
}

// TestTranscoding_ContentType_JSON verifies that responses have the correct
// Content-Type header.
func TestTranscoding_ContentType_JSON(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	resp, err := httpGet(context.Background(), env.baseURL+"/v1/parties/content-type-test")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	ct := resp.Header.Get("Content-Type")
	assert.Contains(t, ct, "json", "Content-Type should indicate JSON: %s", ct)
}
