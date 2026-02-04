package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mockMarketInformationServer implements MarketInformationServiceServer for testing
type mockMarketInformationServer struct {
	marketinformationv1.UnimplementedMarketInformationServiceServer

	lastIdempotencyKey string
	lastKnowledgeAt    time.Time
	lastCorrelationID  uuid.UUID

	listObservationsCalled bool

	// Control response behavior
	shouldError     bool
	errorMessage    string
	errorCode       codes.Code
	observations    []*marketinformationv1.MarketPriceObservation
	observationDate time.Time
}

func (m *mockMarketInformationServer) ListObservations(ctx context.Context, _ *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
	m.listObservationsCalled = true

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
		return nil, status.Error(m.errorCode, m.errorMessage)
	}

	return &marketinformationv1.ListObservationsResponse{
		Observations: m.observations,
	}, nil
}

// setupStarlarkMockServer creates a mock gRPC server and client for testing Starlark handlers
func setupStarlarkMockServer(t *testing.T, mockServer *mockMarketInformationServer) (*Client, func()) {
	// Create in-memory listener
	buffer := 1024 * 1024
	listener := bufconn.Listen(buffer)

	// Create and start gRPC server
	server := grpc.NewServer()
	marketinformationv1.RegisterMarketInformationServiceServer(server, mockServer)

	go func() {
		if err := server.Serve(listener); err != nil {
			t.Logf("Server error: %v", err)
		}
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
		conn:       conn,
		grpcClient: marketinformationv1.NewMarketInformationServiceClient(conn),
		timeout:    5 * time.Second,
	}

	cleanup := func() {
		conn.Close()
		server.Stop()
		listener.Close()
	}

	return client, cleanup
}

func TestRegisterStarlarkHandlers(t *testing.T) {
	t.Run("registers all handlers successfully", func(t *testing.T) {
		registry := saga.NewHandlerRegistry()
		mockServer := &mockMarketInformationServer{}
		client, cleanup := setupStarlarkMockServer(t, mockServer)
		defer cleanup()

		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		// Verify handler is registered
		handler, metadata, err := registry.GetWithMetadata("market_information.get_rate")
		assert.NoError(t, err, "Handler market_information.get_rate should be registered")
		assert.NotNil(t, handler, "Handler market_information.get_rate should not be nil")

		// Verify handler metadata
		require.NotNil(t, metadata)
		assert.Equal(t, saga.HandlerCategoryValuation, metadata.Category)
		assert.Empty(t, metadata.ProducesInstruments, "get_rate is read-only, should not produce instruments")
	})
}

func TestGetRateHandler(t *testing.T) {
	t.Run("successful rate lookup", func(t *testing.T) {
		mockServer := &mockMarketInformationServer{
			observationDate: time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			observations: []*marketinformationv1.MarketPriceObservation{
				{
					Id:                 "obs-123",
					DatasetCode:        "USD_EUR_FX",
					DatasetVersion:     1,
					ResolutionKeyValue: "spot",
					Value:              "1.0856",
					Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
					SourceId:           "source-ecb-id",
					ValidFrom:          timestamppb.New(time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)),
					ObservedAt:         timestamppb.New(time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)),
				},
			},
		}
		client, cleanup := setupStarlarkMockServer(t, mockServer)
		defer cleanup()

		registry := saga.NewHandlerRegistry()
		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		handler, err := registry.Get("market_information.get_rate")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-idempotency-key",
			CorrelationID:  uuid.New(),
			KnowledgeAt:    time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
		}

		params := map[string]any{
			"from_currency": "USD",
			"to_currency":   "EUR",
			"rate_date":     time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		}

		result, err := handler(ctx, params)
		require.NoError(t, err)
		require.NotNil(t, result)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok, "Result should be a map")

		assert.Equal(t, "USD", resultMap["from_currency"])
		assert.Equal(t, "EUR", resultMap["to_currency"])
		assert.Equal(t, decimal.RequireFromString("1.0856"), resultMap["rate"])
		assert.Equal(t, "source-ecb-id", resultMap["source"])
		assert.NotNil(t, resultMap["rate_date"])

		// Verify service was called
		assert.True(t, mockServer.listObservationsCalled)

		// Verify metadata propagation (CodeRabbit suggestion)
		assert.Equal(t, "test-idempotency-key", mockServer.lastIdempotencyKey, "idempotency key should be propagated")
		assert.Equal(t, ctx.CorrelationID, mockServer.lastCorrelationID, "correlation ID should be propagated")
		assert.Equal(t, ctx.KnowledgeAt, mockServer.lastKnowledgeAt, "knowledge_at should be propagated")
	})

	t.Run("same currency returns rate 1.0", func(t *testing.T) {
		mockServer := &mockMarketInformationServer{}
		client, cleanup := setupStarlarkMockServer(t, mockServer)
		defer cleanup()

		registry := saga.NewHandlerRegistry()
		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		handler, err := registry.Get("market_information.get_rate")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			CorrelationID:  uuid.New(),
			KnowledgeAt:    time.Now(),
		}

		params := map[string]any{
			"from_currency": "USD",
			"to_currency":   "USD",
		}

		result, err := handler(ctx, params)
		require.NoError(t, err)
		require.NotNil(t, result)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)

		assert.Equal(t, "USD", resultMap["from_currency"])
		assert.Equal(t, "USD", resultMap["to_currency"])
		assert.Equal(t, decimal.NewFromInt(1), resultMap["rate"])
		assert.Equal(t, "IDENTITY", resultMap["source"])

		// Should not call the service for same currency
		assert.False(t, mockServer.listObservationsCalled)
	})

	t.Run("missing from_currency returns error", func(t *testing.T) {
		mockServer := &mockMarketInformationServer{}
		client, cleanup := setupStarlarkMockServer(t, mockServer)
		defer cleanup()

		registry := saga.NewHandlerRegistry()
		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		handler, err := registry.Get("market_information.get_rate")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			CorrelationID:  uuid.New(),
			KnowledgeAt:    time.Now(),
		}

		params := map[string]any{
			"to_currency": "EUR",
		}

		result, err := handler(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "from_currency")
	})

	t.Run("missing to_currency returns error", func(t *testing.T) {
		mockServer := &mockMarketInformationServer{}
		client, cleanup := setupStarlarkMockServer(t, mockServer)
		defer cleanup()

		registry := saga.NewHandlerRegistry()
		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		handler, err := registry.Get("market_information.get_rate")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			CorrelationID:  uuid.New(),
			KnowledgeAt:    time.Now(),
		}

		params := map[string]any{
			"from_currency": "USD",
		}

		result, err := handler(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "to_currency")
	})

	t.Run("rate not found returns error", func(t *testing.T) {
		mockServer := &mockMarketInformationServer{
			shouldError:  true,
			errorCode:    codes.NotFound,
			errorMessage: "rate not found",
		}
		client, cleanup := setupStarlarkMockServer(t, mockServer)
		defer cleanup()

		registry := saga.NewHandlerRegistry()
		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		handler, err := registry.Get("market_information.get_rate")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			CorrelationID:  uuid.New(),
			KnowledgeAt:    time.Now(),
		}

		params := map[string]any{
			"from_currency": "USD",
			"to_currency":   "XYZ", // Invalid currency
		}

		result, err := handler(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "market_information.get_rate")
	})

	t.Run("future date returns error", func(t *testing.T) {
		mockServer := &mockMarketInformationServer{
			shouldError:  true,
			errorCode:    codes.InvalidArgument,
			errorMessage: "rate_date cannot be in the future",
		}
		client, cleanup := setupStarlarkMockServer(t, mockServer)
		defer cleanup()

		registry := saga.NewHandlerRegistry()
		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		handler, err := registry.Get("market_information.get_rate")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			CorrelationID:  uuid.New(),
			KnowledgeAt:    time.Now(),
		}

		// Future date (1 day from now)
		futureDate := time.Now().Add(24 * time.Hour)
		params := map[string]any{
			"from_currency": "USD",
			"to_currency":   "EUR",
			"rate_date":     futureDate,
		}

		result, err := handler(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("defaults to current date when rate_date not provided", func(t *testing.T) {
		now := time.Now()
		mockServer := &mockMarketInformationServer{
			observations: []*marketinformationv1.MarketPriceObservation{
				{
					Id:                 "obs-456",
					DatasetCode:        "USD_EUR_FX",
					DatasetVersion:     1,
					ResolutionKeyValue: "spot",
					Value:              "1.0900",
					Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
					SourceId:           "source-ecb-id",
					ValidFrom:          timestamppb.New(now),
					ObservedAt:         timestamppb.New(now),
				},
			},
		}
		client, cleanup := setupStarlarkMockServer(t, mockServer)
		defer cleanup()

		registry := saga.NewHandlerRegistry()
		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		handler, err := registry.Get("market_information.get_rate")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			CorrelationID:  uuid.New(),
			KnowledgeAt:    now,
		}

		params := map[string]any{
			"from_currency": "USD",
			"to_currency":   "EUR",
			// rate_date omitted - should default to now
		}

		result, err := handler(ctx, params)
		require.NoError(t, err)
		require.NotNil(t, result)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)

		assert.Equal(t, decimal.RequireFromString("1.0900"), resultMap["rate"])
		assert.True(t, mockServer.listObservationsCalled)
	})

	t.Run("uses knowledge_at for bi-temporal queries", func(t *testing.T) {
		knowledgeTime := time.Date(2024, 1, 10, 12, 0, 0, 0, time.UTC)
		mockServer := &mockMarketInformationServer{
			observations: []*marketinformationv1.MarketPriceObservation{
				{
					Id:                 "obs-historical",
					DatasetCode:        "USD_EUR_FX",
					DatasetVersion:     1,
					ResolutionKeyValue: "spot",
					Value:              "1.0750",
					Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
					SourceId:           "source-ecb-id",
					ValidFrom:          timestamppb.New(time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)),
					ObservedAt:         timestamppb.New(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)),
				},
			},
		}
		client, cleanup := setupStarlarkMockServer(t, mockServer)
		defer cleanup()

		registry := saga.NewHandlerRegistry()
		err := RegisterStarlarkHandlers(registry, client)
		require.NoError(t, err)

		handler, err := registry.Get("market_information.get_rate")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:        context.Background(),
			IdempotencyKey: "test-key",
			CorrelationID:  uuid.New(),
			KnowledgeAt:    knowledgeTime,
		}

		params := map[string]any{
			"from_currency": "USD",
			"to_currency":   "EUR",
			"rate_date":     time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		}

		result, err := handler(ctx, params)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify service was called
		assert.True(t, mockServer.listObservationsCalled)

		// Verify knowledge_at was propagated (CodeRabbit suggestion)
		assert.Equal(t, knowledgeTime, mockServer.lastKnowledgeAt, "knowledge_at should be propagated for bi-temporal queries")
		assert.Equal(t, ctx.CorrelationID, mockServer.lastCorrelationID, "correlation ID should be propagated")
	})
}

// TestGetRateHandler_DatasetCodeGeneration verifies the dataset code generation logic
func TestGetRateHandler_DatasetCodeGeneration(t *testing.T) {
	testCases := []struct {
		name            string
		fromCurrency    string
		toCurrency      string
		expectedDataset string
		expectedResKey  string
	}{
		{
			name:            "USD to EUR",
			fromCurrency:    "USD",
			toCurrency:      "EUR",
			expectedDataset: "USD_EUR_FX",
			expectedResKey:  "spot",
		},
		{
			name:            "GBP to JPY",
			fromCurrency:    "GBP",
			toCurrency:      "JPY",
			expectedDataset: "GBP_JPY_FX",
			expectedResKey:  "spot",
		},
		{
			name:            "EUR to USD (reverse pair)",
			fromCurrency:    "EUR",
			toCurrency:      "USD",
			expectedDataset: "EUR_USD_FX",
			expectedResKey:  "spot",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockServer := &mockMarketInformationServer{
				observations: []*marketinformationv1.MarketPriceObservation{
					{
						Id:                 "obs-test",
						DatasetCode:        tc.expectedDataset,
						DatasetVersion:     1,
						ResolutionKeyValue: tc.expectedResKey,
						Value:              "1.1234",
						Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
						SourceId:           "source-test-id",
						ValidFrom:          timestamppb.Now(),
						ObservedAt:         timestamppb.Now(),
					},
				},
			}
			client, cleanup := setupStarlarkMockServer(t, mockServer)
			defer cleanup()

			registry := saga.NewHandlerRegistry()
			err := RegisterStarlarkHandlers(registry, client)
			require.NoError(t, err)

			handler, err := registry.Get("market_information.get_rate")
			require.NoError(t, err)

			ctx := &saga.StarlarkContext{
				Context:        context.Background(),
				IdempotencyKey: "test-key",
				CorrelationID:  uuid.New(),
				KnowledgeAt:    time.Now(),
			}

			params := map[string]any{
				"from_currency": tc.fromCurrency,
				"to_currency":   tc.toCurrency,
			}

			result, err := handler(ctx, params)
			require.NoError(t, err)
			require.NotNil(t, result)

			resultMap, ok := result.(map[string]any)
			require.True(t, ok)

			assert.Equal(t, tc.fromCurrency, resultMap["from_currency"])
			assert.Equal(t, tc.toCurrency, resultMap["to_currency"])
			assert.True(t, mockServer.listObservationsCalled)
		})
	}
}
