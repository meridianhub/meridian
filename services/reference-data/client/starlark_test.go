package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestRegisterStarlarkHandlers_Success verifies handler is registered.
func TestRegisterStarlarkHandlers_Success(t *testing.T) {
	registry := saga.NewHandlerRegistry()

	// Create a mock client (nil is fine for registration test)
	client := &Client{}

	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	// Verify handler is registered
	handlers := registry.List()
	assert.Contains(t, handlers, "reference_data.retrieve_instrument")
}

// TestRetrieveInstrumentHandler_Success verifies instrument retrieval with fungibility_key_expression.
func TestRetrieveInstrumentHandler_Success(t *testing.T) {
	// Start mock gRPC server
	server, addr := startMockServer(t)

	// Configure mock response
	mockInstrument := &referencedatav1.InstrumentDefinition{
		Id:                       uuid.New().String(),
		Code:                     "USD",
		Version:                  1,
		Dimension:                referencedatav1.Dimension_DIMENSION_CURRENCY,
		Precision:                2,
		Status:                   referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		FungibilityKeyExpression: "code + ':' + string(version)",
		DisplayName:              "US Dollar",
		CreatedAt:                timestamppb.Now(),
	}
	server.RegisterInstrumentFunc = func(_ context.Context, _ *referencedatav1.RegisterInstrumentRequest) (*referencedatav1.RegisterInstrumentResponse, error) {
		return &referencedatav1.RegisterInstrumentResponse{
			Instrument: mockInstrument,
		}, nil
	}
	server.RetrieveInstrumentFunc = func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
		return &referencedatav1.RetrieveInstrumentResponse{
			Instrument: mockInstrument,
		}, nil
	}

	// Create client
	ctx := context.Background()
	client, cleanup, err := New(ctx, Config{
		Target:  addr,
		Timeout: 5 * time.Second,
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	require.NoError(t, err)
	defer func() { _ = cleanup() }()

	// Register handlers
	registry := saga.NewHandlerRegistry()
	err = RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	// Get handler
	handler, err := registry.Get("reference_data.retrieve_instrument")
	require.NoError(t, err)

	// Execute handler
	sagaCtx := &saga.StarlarkContext{
		Context:        ctx,
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-key",
		KnowledgeAt:    time.Now(),
	}
	params := map[string]any{
		"instrument_code": "USD",
		"version":         int64(1),
	}

	result, err := handler(sagaCtx, params)
	require.NoError(t, err)

	// Verify response structure
	resultMap, ok := result.(map[string]any)
	require.True(t, ok, "result should be a map")

	assert.Equal(t, "USD", resultMap["instrument_code"])
	assert.Equal(t, int32(1), resultMap["version"])
	assert.Equal(t, "code + ':' + string(version)", resultMap["fungibility_key_expression"])
	assert.Equal(t, true, resultMap["is_fungible"])
	assert.Equal(t, "CURRENCY", resultMap["dimension"])
}

// TestRetrieveInstrumentHandler_NotFound verifies NOT_FOUND error handling.
func TestRetrieveInstrumentHandler_NotFound(t *testing.T) {
	// Start mock gRPC server
	server, addr := startMockServer(t)

	// Configure mock to return NOT_FOUND
	server.RetrieveInstrumentFunc = func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
		return nil, status.Error(codes.NotFound, "instrument not found")
	}

	// Create client
	ctx := context.Background()
	client, cleanup, err := New(ctx, Config{
		Target:  addr,
		Timeout: 5 * time.Second,
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	require.NoError(t, err)
	defer func() { _ = cleanup() }()

	// Register handlers
	registry := saga.NewHandlerRegistry()
	err = RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	// Get handler
	handler, err := registry.Get("reference_data.retrieve_instrument")
	require.NoError(t, err)

	// Execute handler
	sagaCtx := &saga.StarlarkContext{
		Context:        ctx,
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-key",
		KnowledgeAt:    time.Now(),
	}
	params := map[string]any{
		"instrument_code": "NONEXISTENT",
		"version":         int64(1),
	}

	_, err = handler(sagaCtx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestRetrieveInstrumentHandler_MissingInstrumentCode verifies parameter validation.
func TestRetrieveInstrumentHandler_MissingInstrumentCode(t *testing.T) {
	client := &Client{}

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("reference_data.retrieve_instrument")
	require.NoError(t, err)

	sagaCtx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-key",
		KnowledgeAt:    time.Now(),
	}
	params := map[string]any{
		"version": int64(1),
		// missing instrument_code
	}

	_, err = handler(sagaCtx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instrument_code")
}

// TestHandlerMetadata_Category verifies Category is HandlerCategoryValuation.
func TestHandlerMetadata_Category(t *testing.T) {
	client := &Client{}

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	_, metadata, err := registry.GetWithMetadata("reference_data.retrieve_instrument")
	require.NoError(t, err)
	require.NotNil(t, metadata)
	assert.Equal(t, saga.HandlerCategoryValuation, metadata.Category)
}

// TestHandlerMetadata_ProducesInstruments verifies ProducesInstruments is empty.
func TestHandlerMetadata_ProducesInstruments(t *testing.T) {
	client := &Client{}

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	_, metadata, err := registry.GetWithMetadata("reference_data.retrieve_instrument")
	require.NoError(t, err)
	require.NotNil(t, metadata)
	assert.Empty(t, metadata.ProducesInstruments)
}

// ========================================
// Mock gRPC Server
// ========================================

type mockReferenceDataServer struct {
	referencedatav1.UnimplementedReferenceDataServiceServer
	RegisterInstrumentFunc  func(context.Context, *referencedatav1.RegisterInstrumentRequest) (*referencedatav1.RegisterInstrumentResponse, error)
	RetrieveInstrumentFunc  func(context.Context, *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error)
	ListInstrumentsFunc     func(context.Context, *referencedatav1.ListInstrumentsRequest) (*referencedatav1.ListInstrumentsResponse, error)
	UpdateInstrumentFunc    func(context.Context, *referencedatav1.UpdateInstrumentRequest) (*referencedatav1.UpdateInstrumentResponse, error)
	ActivateInstrumentFunc  func(context.Context, *referencedatav1.ActivateInstrumentRequest) (*referencedatav1.ActivateInstrumentResponse, error)
	DeprecateInstrumentFunc func(context.Context, *referencedatav1.DeprecateInstrumentRequest) (*referencedatav1.DeprecateInstrumentResponse, error)
}

func (m *mockReferenceDataServer) RegisterInstrument(ctx context.Context, req *referencedatav1.RegisterInstrumentRequest) (*referencedatav1.RegisterInstrumentResponse, error) {
	if m.RegisterInstrumentFunc != nil {
		return m.RegisterInstrumentFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockReferenceDataServer) RetrieveInstrument(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
	if m.RetrieveInstrumentFunc != nil {
		return m.RetrieveInstrumentFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockReferenceDataServer) ListInstruments(ctx context.Context, req *referencedatav1.ListInstrumentsRequest) (*referencedatav1.ListInstrumentsResponse, error) {
	if m.ListInstrumentsFunc != nil {
		return m.ListInstrumentsFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func startMockServer(t *testing.T) (*mockReferenceDataServer, string) {
	t.Helper()

	// Create TCP listener on random port
	lc := &net.ListenConfig{}
	lis, err := lc.Listen(context.Background(), "tcp", "localhost:0")
	require.NoError(t, err)

	server := &mockReferenceDataServer{}
	grpcServer := grpc.NewServer()
	referencedatav1.RegisterReferenceDataServiceServer(grpcServer, server)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	t.Cleanup(func() {
		grpcServer.GracefulStop()
	})

	return server, lis.Addr().String()
}
