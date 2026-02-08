package client

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

// mockCurrentAccountServer implements the CurrentAccountServiceServer interface for testing
type mockCurrentAccountServer struct {
	currentaccountv1.UnimplementedCurrentAccountServiceServer

	lastIdempotencyKey string
	lastKnowledgeAt    time.Time
	lastCorrelationID  uuid.UUID

	initiateLienCalled  bool
	executeLienCalled   bool
	terminateLienCalled bool
	updateAccountCalled bool

	// Control response behavior
	shouldError  bool
	errorMessage string
}

func (m *mockCurrentAccountServer) InitiateLien(ctx context.Context, req *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
	m.initiateLienCalled = true

	// Extract metadata from context
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if keys := md.Get("x-idempotency-key"); len(keys) > 0 {
			m.lastIdempotencyKey = keys[0]
		}
		if correlationIDs := md.Get("x-correlation-id"); len(correlationIDs) > 0 {
			m.lastCorrelationID, _ = uuid.Parse(correlationIDs[0])
		}
		if knowledgeAts := md.Get("x-knowledge-at"); len(knowledgeAts) > 0 {
			m.lastKnowledgeAt, _ = time.Parse(time.RFC3339, knowledgeAts[0])
		}
	}

	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMessage)
	}

	return &currentaccountv1.InitiateLienResponse{
		Lien: &currentaccountv1.Lien{
			LienId:    "test-lien-id",
			AccountId: req.GetAccountId(),
			Amount:    req.GetAmount(),
			Status:    currentaccountv1.LienStatus_LIEN_STATUS_ACTIVE,
		},
	}, nil
}

func (m *mockCurrentAccountServer) ExecuteLien(_ context.Context, req *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error) {
	m.executeLienCalled = true

	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMessage)
	}

	return &currentaccountv1.ExecuteLienResponse{
		Lien: &currentaccountv1.Lien{
			LienId: req.GetLienId(),
			Status: currentaccountv1.LienStatus_LIEN_STATUS_EXECUTED,
		},
		NewBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        900,
				Nanos:        0,
			},
		},
	}, nil
}

func (m *mockCurrentAccountServer) TerminateLien(_ context.Context, req *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error) {
	m.terminateLienCalled = true

	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMessage)
	}

	return &currentaccountv1.TerminateLienResponse{
		Lien: &currentaccountv1.Lien{
			LienId: req.GetLienId(),
			Status: currentaccountv1.LienStatus_LIEN_STATUS_TERMINATED,
		},
		AvailableBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        1000,
				Nanos:        0,
			},
		},
	}, nil
}

func (m *mockCurrentAccountServer) UpdateCurrentAccount(_ context.Context, req *currentaccountv1.UpdateCurrentAccountRequest) (*currentaccountv1.UpdateCurrentAccountResponse, error) {
	m.updateAccountCalled = true

	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMessage)
	}

	return &currentaccountv1.UpdateCurrentAccountResponse{
		Facility: &currentaccountv1.CurrentAccountFacility{
			AccountId: req.GetAccountId(),
		},
		Version: 1,
	}, nil
}

// setupMockServer creates a mock gRPC server and client for testing
func setupMockServer(t *testing.T, mockServer *mockCurrentAccountServer) (*Client, func()) {
	// Create in-memory listener
	buffer := 1024 * 1024
	listener := bufconn.Listen(buffer)

	// Create and start gRPC server
	server := grpc.NewServer()
	currentaccountv1.RegisterCurrentAccountServiceServer(server, mockServer)

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = server.Serve(listener)
	}()

	// Create client connection
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	client := &Client{
		conn:           conn,
		currentAccount: currentaccountv1.NewCurrentAccountServiceClient(conn),
		timeout:        5 * time.Second,
	}

	cleanup := func() {
		conn.Close()
		server.GracefulStop()
		<-serveDone
		listener.Close()
	}

	return client, cleanup
}

func TestRegisterStarlarkHandlers(t *testing.T) {
	t.Run("registers all handlers successfully", func(t *testing.T) {
		registry := saga.NewHandlerRegistry()
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		// Verify all handlers are registered
		handlers := []string{
			"current_account.create_lien",
			"current_account.execute_lien",
			"current_account.terminate_lien",
			"current_account.save",
		}

		for _, name := range handlers {
			handler, err := registry.Get(name)
			assert.NoError(t, err, "Handler %s should be registered", name)
			assert.NotNil(t, handler, "Handler %s should not be nil", name)
		}
	})
}

func TestCreateLienHandler(t *testing.T) {
	t.Run("successful lien creation", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		handler := createLienHandler(client)

		idempotencyKey := "test-idempotency-key"
		correlationID := uuid.New()
		knowledgeAt := time.Now().Truncate(time.Second)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: idempotencyKey,
			KnowledgeAt:    knowledgeAt,
			CorrelationID:  correlationID,
		}

		params := map[string]any{
			"account_id": "acc-123",
			"amount":     decimal.NewFromFloat(100.50),
			"currency":   "USD",
		}

		result, err := handler(ctx, params)
		require.NoError(t, err)
		assert.True(t, mockServer.initiateLienCalled)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "test-lien-id", resultMap["lien_id"])
		assert.Equal(t, "acc-123", resultMap["account_id"])
		assert.Equal(t, "ACTIVE", resultMap["status"])
	})

	t.Run("missing account_id parameter", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		handler := createLienHandler(client)
		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		params := map[string]any{
			"amount":   decimal.NewFromFloat(100.50),
			"currency": "USD",
		}

		_, err := handler(ctx, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "account_id")
	})

	t.Run("missing amount parameter", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		handler := createLienHandler(client)
		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		params := map[string]any{
			"account_id": "acc-123",
			"currency":   "USD",
		}

		_, err := handler(ctx, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "amount")
	})

	t.Run("gRPC error is propagated", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{
			shouldError:  true,
			errorMessage: "insufficient funds",
		}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		handler := createLienHandler(client)
		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		params := map[string]any{
			"account_id": "acc-123",
			"amount":     decimal.NewFromFloat(100.50),
			"currency":   "USD",
		}

		_, err := handler(ctx, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insufficient funds")
	})
}

func TestExecuteLienHandler(t *testing.T) {
	t.Run("successful lien execution", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		handler := executeLienHandler(client)
		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		params := map[string]any{
			"lien_id": "lien-123",
		}

		result, err := handler(ctx, params)
		require.NoError(t, err)
		assert.True(t, mockServer.executeLienCalled)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "lien-123", resultMap["lien_id"])
		assert.Equal(t, "EXECUTED", resultMap["status"])
	})

	t.Run("missing lien_id parameter", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		handler := executeLienHandler(client)
		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		params := map[string]any{}

		_, err := handler(ctx, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lien_id")
	})
}

func TestTerminateLienHandler(t *testing.T) {
	t.Run("successful lien termination", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		handler := terminateLienHandler(client)
		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		params := map[string]any{
			"lien_id": "lien-123",
			"reason":  "payment cancelled",
		}

		result, err := handler(ctx, params)
		require.NoError(t, err)
		assert.True(t, mockServer.terminateLienCalled)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "lien-123", resultMap["lien_id"])
		assert.Equal(t, "TERMINATED", resultMap["status"])
	})

	t.Run("missing lien_id parameter", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		handler := terminateLienHandler(client)
		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		params := map[string]any{}

		_, err := handler(ctx, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lien_id")
	})
}

func TestSaveHandler(t *testing.T) {
	t.Run("successful account update", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		handler := saveHandler(client)
		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		params := map[string]any{
			"account_id": "acc-123",
		}

		result, err := handler(ctx, params)
		require.NoError(t, err)
		assert.True(t, mockServer.updateAccountCalled)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "acc-123", resultMap["account_id"])
		assert.Equal(t, "SAVED", resultMap["status"])
	})

	t.Run("missing account_id parameter", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		handler := saveHandler(client)
		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		params := map[string]any{}

		_, err := handler(ctx, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "account_id")
	})
}

func TestLienLifecycle(t *testing.T) {
	t.Run("complete lifecycle: create -> execute", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		registry := saga.NewHandlerRegistry()
		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		// Step 1: Create lien
		createHandler, _ := registry.Get("current_account.create_lien")
		createResult, err := createHandler(ctx, map[string]any{
			"account_id": "acc-123",
			"amount":     decimal.NewFromFloat(100.00),
			"currency":   "USD",
		})
		require.NoError(t, err)
		createMap := createResult.(map[string]any)
		lienID := createMap["lien_id"].(string)

		// Step 2: Execute lien
		executeHandler, _ := registry.Get("current_account.execute_lien")
		executeResult, err := executeHandler(ctx, map[string]any{
			"lien_id": lienID,
		})
		require.NoError(t, err)
		executeMap := executeResult.(map[string]any)
		assert.Equal(t, "EXECUTED", executeMap["status"])
	})

	t.Run("compensation flow: create -> terminate", func(t *testing.T) {
		mockServer := &mockCurrentAccountServer{}
		client, cleanup := setupMockServer(t, mockServer)
		defer cleanup()

		registry := saga.NewHandlerRegistry()
		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			KnowledgeAt:    time.Now(),
			CorrelationID:  uuid.New(),
		}

		// Step 1: Create lien
		createHandler, _ := registry.Get("current_account.create_lien")
		createResult, err := createHandler(ctx, map[string]any{
			"account_id": "acc-123",
			"amount":     decimal.NewFromFloat(100.00),
			"currency":   "USD",
		})
		require.NoError(t, err)
		createMap := createResult.(map[string]any)
		lienID := createMap["lien_id"].(string)

		// Step 2: Terminate lien (compensation)
		terminateHandler, _ := registry.Get("current_account.terminate_lien")
		terminateResult, err := terminateHandler(ctx, map[string]any{
			"lien_id": lienID,
			"reason":  "saga compensation",
		})
		require.NoError(t, err)
		terminateMap := terminateResult.(map[string]any)
		assert.Equal(t, "TERMINATED", terminateMap["status"])
	})
}
