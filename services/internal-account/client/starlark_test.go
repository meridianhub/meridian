package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mockInternalAccountServer implements a minimal mock for testing handlers
type mockInternalAccountServer struct {
	internalaccountv1.UnimplementedInternalAccountServiceServer
	initiateCalled   bool
	retrieveCalled   bool
	getBalanceCalled bool
	lastAccountID    string
}

func (m *mockInternalAccountServer) InitiateInternalAccount(_ context.Context, req *internalaccountv1.InitiateInternalAccountRequest) (*internalaccountv1.InitiateInternalAccountResponse, error) {
	m.initiateCalled = true
	m.lastAccountID = "test-account-123"

	return &internalaccountv1.InitiateInternalAccountResponse{
		AccountId: m.lastAccountID,
		Facility: &internalaccountv1.InternalAccountFacility{
			AccountId:      m.lastAccountID,
			AccountCode:    req.GetAccountCode(),
			Name:           req.GetName(),
			BehaviorClass:  req.GetProductTypeCode(),
			AccountStatus:  internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
			InstrumentCode: req.GetInstrumentCode(),
			CreatedAt:      timestamppb.Now(),
			UpdatedAt:      timestamppb.Now(),
			Version:        1,
		},
	}, nil
}

func (m *mockInternalAccountServer) RetrieveInternalAccount(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
	m.retrieveCalled = true
	m.lastAccountID = req.GetAccountId()

	return &internalaccountv1.RetrieveInternalAccountResponse{
		Facility: &internalaccountv1.InternalAccountFacility{
			AccountId:      req.GetAccountId(),
			AccountCode:    "NOSTRO-USD-001",
			Name:           "USD Nostro at Test Bank",
			BehaviorClass:  "NOSTRO",
			AccountStatus:  internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
			InstrumentCode: "USD",
			CreatedAt:      timestamppb.Now(),
			UpdatedAt:      timestamppb.Now(),
			Version:        1,
		},
	}, nil
}

func (m *mockInternalAccountServer) GetBalance(_ context.Context, req *internalaccountv1.GetBalanceRequest) (*internalaccountv1.GetBalanceResponse, error) {
	m.getBalanceCalled = true
	m.lastAccountID = req.GetAccountId()

	return &internalaccountv1.GetBalanceResponse{
		AccountId: req.GetAccountId(),
		CurrentBalance: &quantityv1.InstrumentAmount{
			InstrumentCode: "USD",
			Amount:         "1000.50",
			Version:        1,
		},
		AsOf: timestamppb.Now(),
	}, nil
}

func setupTestClient(t *testing.T) (*Client, *mockInternalAccountServer, func()) {
	t.Helper()

	mock := &mockInternalAccountServer{}

	// Create in-memory listener
	buffer := 1024 * 1024
	listener := bufconn.Listen(buffer)

	// Start in-memory gRPC server
	srv := grpc.NewServer()
	internalaccountv1.RegisterInternalAccountServiceServer(srv, mock)

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = srv.Serve(listener)
	}()

	// Create client connection using bufconn
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	c := &Client{
		conn:                conn,
		internalAccount: internalaccountv1.NewInternalAccountServiceClient(conn),
		timeout:             5 * time.Second,
	}

	fullCleanup := func() {
		conn.Close()
		srv.GracefulStop()
		<-serveDone
		listener.Close()
	}

	return c, mock, fullCleanup
}

func TestRetrieveHandler(t *testing.T) {
	c, mock, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, c)
	require.NoError(t, err)

	// Get the registered handler
	handler, err := registry.Get("internal_account.retrieve")
	require.NoError(t, err)

	// Prepare context
	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-idempotency-key",
		KnowledgeAt:    time.Now(),
	}

	// Call handler
	result, err := handler(ctx, map[string]any{
		"account_id": "test-account-456",
	})
	require.NoError(t, err)

	// Verify mock was called
	assert.True(t, mock.retrieveCalled)
	assert.Equal(t, "test-account-456", mock.lastAccountID)

	// Verify result structure
	resultMap, ok := result.(map[string]any)
	require.True(t, ok, "result should be map[string]any")

	assert.Equal(t, "test-account-456", resultMap["account_id"])
	assert.Equal(t, "NOSTRO-USD-001", resultMap["account_code"])
	assert.Equal(t, "USD Nostro at Test Bank", resultMap["name"])
	assert.Equal(t, "NOSTRO", resultMap["behavior_class"])
	assert.Equal(t, "ACTIVE", resultMap["status"])
	assert.Equal(t, "USD", resultMap["instrument_code"])
}

func TestRetrieveHandler_MissingAccountID(t *testing.T) {
	c, _, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, c)
	require.NoError(t, err)

	handler, err := registry.Get("internal_account.retrieve")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-key",
		KnowledgeAt:    time.Now(),
	}

	// Call without account_id should fail
	_, err = handler(ctx, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id")
}

func TestGetBalanceHandler(t *testing.T) {
	c, mock, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, c)
	require.NoError(t, err)

	handler, err := registry.Get("internal_account.get_balance")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-key",
		KnowledgeAt:    time.Now(),
	}

	result, err := handler(ctx, map[string]any{
		"account_id": "test-account-789",
	})
	require.NoError(t, err)

	// Verify mock was called
	assert.True(t, mock.getBalanceCalled)
	assert.Equal(t, "test-account-789", mock.lastAccountID)

	// Verify result structure
	resultMap, ok := result.(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "test-account-789", resultMap["account_id"])
	assert.Equal(t, "USD", resultMap["instrument_code"])
	assert.Equal(t, "1000.50", resultMap["amount"])
	assert.NotNil(t, resultMap["as_of"])
}

func TestGetBalanceHandler_MissingAccountID(t *testing.T) {
	c, _, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, c)
	require.NoError(t, err)

	handler, err := registry.Get("internal_account.get_balance")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-key",
		KnowledgeAt:    time.Now(),
	}

	_, err = handler(ctx, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id")
}

func TestInitiateHandler(t *testing.T) {
	c, mock, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, c)
	require.NoError(t, err)

	handler, err := registry.Get("internal_account.initiate")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-key",
		KnowledgeAt:    time.Now(),
	}

	result, err := handler(ctx, map[string]any{
		"account_code":      "NOSTRO-USD-001",
		"name":              "USD Nostro at Test Bank",
		"product_type_code": "NOSTRO_USD",
		"instrument_code":   "USD",
		"description":       "Test nostro account",
	})
	require.NoError(t, err)

	// Verify mock was called
	assert.True(t, mock.initiateCalled)

	// Verify result structure
	resultMap, ok := result.(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "test-account-123", resultMap["account_id"])
	assert.Equal(t, "NOSTRO-USD-001", resultMap["account_code"])
	assert.Equal(t, "USD Nostro at Test Bank", resultMap["name"])
	assert.Equal(t, "NOSTRO_USD", resultMap["behavior_class"])
	assert.Equal(t, "ACTIVE", resultMap["status"])
	assert.Equal(t, "USD", resultMap["instrument_code"])
}

func TestInitiateHandler_MinimalParams(t *testing.T) {
	c, mock, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, c)
	require.NoError(t, err)

	handler, err := registry.Get("internal_account.initiate")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-key",
		KnowledgeAt:    time.Now(),
	}

	// Only required fields
	result, err := handler(ctx, map[string]any{
		"account_code":      "VOSTRO-EUR-001",
		"name":              "EUR Vostro Account",
		"product_type_code": "VOSTRO_EUR",
		"instrument_code":   "EUR",
	})
	require.NoError(t, err)

	assert.True(t, mock.initiateCalled)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, resultMap["account_id"])
}

func TestInitiateHandler_MissingRequiredFields(t *testing.T) {
	c, _, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, c)
	require.NoError(t, err)

	handler, err := registry.Get("internal_account.initiate")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-key",
		KnowledgeAt:    time.Now(),
	}

	testCases := []struct {
		name   string
		params map[string]any
	}{
		{
			name: "missing account_code",
			params: map[string]any{
				"name":              "Test",
				"product_type_code": "NOSTRO_USD",
				"instrument_code":   "USD",
			},
		},
		{
			name: "missing name",
			params: map[string]any{
				"account_code":      "TEST-001",
				"product_type_code": "NOSTRO_USD",
				"instrument_code":   "USD",
			},
		},
		{
			name: "missing product_type_code",
			params: map[string]any{
				"account_code":    "TEST-001",
				"name":            "Test",
				"instrument_code": "USD",
			},
		},
		{
			name: "missing instrument_code",
			params: map[string]any{
				"account_code":      "TEST-001",
				"name":              "Test",
				"product_type_code": "NOSTRO_USD",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler(ctx, tc.params)
			require.Error(t, err)
		})
	}
}

func TestHandlerMetadata(t *testing.T) {
	c, _, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, c)
	require.NoError(t, err)

	// Test retrieve handler metadata
	_, retrieveMeta, err := registry.GetWithMetadata("internal_account.retrieve")
	require.NoError(t, err)
	require.NotNil(t, retrieveMeta)
	assert.Equal(t, saga.HandlerCategory(""), retrieveMeta.Category)
	assert.Empty(t, retrieveMeta.ProducesInstruments)

	// Test get_balance handler metadata
	_, balanceMeta, err := registry.GetWithMetadata("internal_account.get_balance")
	require.NoError(t, err)
	require.NotNil(t, balanceMeta)
	assert.Equal(t, saga.HandlerCategory(""), balanceMeta.Category)
	assert.Empty(t, balanceMeta.ProducesInstruments)

	// Test initiate handler metadata
	_, initiateMeta, err := registry.GetWithMetadata("internal_account.initiate")
	require.NoError(t, err)
	require.NotNil(t, initiateMeta)
	assert.Equal(t, saga.HandlerCategorySettlement, initiateMeta.Category)
	assert.Empty(t, initiateMeta.ProducesInstruments) // Internal accounts don't produce instruments
}
