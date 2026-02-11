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
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mockCurrentAccountServer implements the CurrentAccountServiceServer interface for testing
type mockCurrentAccountServer struct {
	currentaccountv1.UnimplementedCurrentAccountServiceServer

	lastIdempotencyKey string
	lastKnowledgeAt    time.Time
	lastCorrelationID  uuid.UUID

	initiateLienCalled   bool
	executeLienCalled    bool
	terminateLienCalled  bool
	updateAccountCalled  bool
	controlAccountCalled bool

	// Control response behavior
	shouldError  bool
	errorMessage string

	// Optional valuation basis to return with InitiateLien
	initiateLienBasis *currentaccountv1.ValuationAnalysis
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
		Basis: m.initiateLienBasis,
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

func (m *mockCurrentAccountServer) ControlCurrentAccount(_ context.Context, req *currentaccountv1.ControlCurrentAccountRequest) (*currentaccountv1.ControlCurrentAccountResponse, error) {
	m.controlAccountCalled = true

	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMessage)
	}

	// Determine resulting status based on action
	var newStatus currentaccountv1.AccountStatus
	switch req.ControlAction {
	case currentaccountv1.ControlAction_CONTROL_ACTION_FREEZE:
		newStatus = currentaccountv1.AccountStatus_ACCOUNT_STATUS_FROZEN
	case currentaccountv1.ControlAction_CONTROL_ACTION_UNFREEZE:
		newStatus = currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE
	case currentaccountv1.ControlAction_CONTROL_ACTION_CLOSE:
		newStatus = currentaccountv1.AccountStatus_ACCOUNT_STATUS_CLOSED
	case currentaccountv1.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		newStatus = currentaccountv1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED
	}

	return &currentaccountv1.ControlCurrentAccountResponse{
		Facility: &currentaccountv1.CurrentAccountFacility{
			AccountId:     req.AccountId,
			AccountStatus: newStatus,
		},
		ActionTimestamp: timestamppb.Now(),
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
			"current_account.control",
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

	t.Run("exposes valuation_analysis when basis is present", func(t *testing.T) {
		computedAt := timestamppb.New(time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC))
		knowledgeAt := timestamppb.New(time.Date(2026, 2, 8, 11, 59, 0, 0, time.UTC))
		observedAt := timestamppb.New(time.Date(2026, 2, 8, 11, 58, 0, 0, time.UTC))

		mockServer := &mockCurrentAccountServer{
			initiateLienBasis: &currentaccountv1.ValuationAnalysis{
				MethodId:      "fx_spot",
				MethodVersion: "1.2.0",
				AppliedRates: map[string]string{
					"fx_rate": "1.2750",
					"spread":  "0.0025",
				},
				ObservationIds:  []string{"obs-001", "obs-002"},
				ComputedAt:      computedAt,
				KnowledgeAt:     knowledgeAt,
				CalculationPath: []string{"lookup_rate", "apply_spread", "convert"},
				DegradedMode:    true,
				MarketDataQualities: []*currentaccountv1.MarketDataQuality{
					{
						Source:           "live_feed",
						QualityLevel:     "ACTUAL",
						ObservedAt:       observedAt,
						StalenessSeconds: 5,
					},
				},
				Warnings: []*currentaccountv1.ValuationWarning{
					{
						Code:     "STALE_MARKET_DATA",
						Message:  "Market data is 5 seconds old",
						Severity: "WARNING",
					},
				},
			},
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

		result, err := handler(ctx, params)
		require.NoError(t, err)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)

		// Verify standard fields are still present
		assert.Equal(t, "test-lien-id", resultMap["lien_id"])
		assert.Equal(t, "acc-123", resultMap["account_id"])
		assert.Equal(t, "ACTIVE", resultMap["status"])

		// Verify valuation_analysis is present
		vaRaw, exists := resultMap["valuation_analysis"]
		require.True(t, exists, "valuation_analysis should be present when basis is set")

		va, ok := vaRaw.(map[string]any)
		require.True(t, ok, "valuation_analysis should be a map")

		assert.Equal(t, "fx_spot", va["method_id"])
		assert.Equal(t, "1.2.0", va["method_version"])
		assert.Equal(t, true, va["degraded_mode"])
		assert.Equal(t, "2026-02-08T12:00:00Z", va["computed_at"])
		assert.Equal(t, "2026-02-08T11:59:00Z", va["knowledge_at"])

		// Verify applied_rates
		rates, ok := va["applied_rates"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "1.2750", rates["fx_rate"])
		assert.Equal(t, "0.0025", rates["spread"])

		// Verify observation_ids
		obsIDs, ok := va["observation_ids"].([]string)
		require.True(t, ok)
		assert.Equal(t, []string{"obs-001", "obs-002"}, obsIDs)

		// Verify calculation_path
		calcPath, ok := va["calculation_path"].([]string)
		require.True(t, ok)
		assert.Equal(t, []string{"lookup_rate", "apply_spread", "convert"}, calcPath)

		// Verify market_data_qualities
		qualitiesRaw, ok := va["market_data_qualities"].([]map[string]any)
		require.True(t, ok)
		require.Len(t, qualitiesRaw, 1)
		assert.Equal(t, "live_feed", qualitiesRaw[0]["source"])
		assert.Equal(t, "ACTUAL", qualitiesRaw[0]["quality_level"])
		assert.Equal(t, int64(5), qualitiesRaw[0]["staleness_seconds"])
		assert.Equal(t, "2026-02-08T11:58:00Z", qualitiesRaw[0]["observed_at"])

		// Verify warnings
		warningsRaw, ok := va["warnings"].([]map[string]any)
		require.True(t, ok)
		require.Len(t, warningsRaw, 1)
		assert.Equal(t, "STALE_MARKET_DATA", warningsRaw[0]["code"])
		assert.Equal(t, "Market data is 5 seconds old", warningsRaw[0]["message"])
		assert.Equal(t, "WARNING", warningsRaw[0]["severity"])
	})

	t.Run("backward compatibility - no valuation_analysis when basis is nil", func(t *testing.T) {
		// Default mock (no initiateLienBasis set) returns nil Basis
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
			"amount":     decimal.NewFromFloat(100.50),
			"currency":   "USD",
		}

		result, err := handler(ctx, params)
		require.NoError(t, err)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)

		// Standard fields present
		assert.Equal(t, "test-lien-id", resultMap["lien_id"])
		assert.Equal(t, "ACTIVE", resultMap["status"])

		// valuation_analysis should NOT be present
		_, exists := resultMap["valuation_analysis"]
		assert.False(t, exists, "valuation_analysis should not be present when basis is nil")
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
