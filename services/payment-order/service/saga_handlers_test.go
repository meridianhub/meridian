// Package service implements gRPC services for the payment order domain
package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterPaymentOrderHandlers(t *testing.T) {
	t.Parallel()

	t.Run("registers all handlers", func(t *testing.T) {
		registry := saga.NewHandlerRegistry()
		mockClient := &MockCurrentAccountClient{
			initiateLienResp: &currentaccountv1.InitiateLienResponse{
				Lien: &currentaccountv1.Lien{LienId: "lien-123"},
			},
		}
		deps := &PaymentOrderHandlerDeps{
			CurrentAccountClient: mockClient,
			PaymentGateway:       &MockPaymentGateway{response: gateway.PaymentResponse{Status: gateway.StatusAccepted}},
			Logger:               testLogger(),
		}

		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		// Verify all handlers are registered
		assert.True(t, registry.Has("payment_order.create_lien"))
		assert.True(t, registry.Has("payment_order.send_to_gateway"))
		assert.True(t, registry.Has("payment_order.post_ledger_entries"))
		assert.True(t, registry.Has("payment_order.execute_lien"))
		assert.True(t, registry.Has("payment_order.terminate_lien"))
	})

	t.Run("fails with nil registry", func(t *testing.T) {
		deps := &PaymentOrderHandlerDeps{}
		err := RegisterPaymentOrderHandlers(nil, deps)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "registry cannot be nil")
	})

	t.Run("fails with nil deps", func(t *testing.T) {
		registry := saga.NewHandlerRegistry()
		err := RegisterPaymentOrderHandlers(registry, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dependencies cannot be nil")
	})
}

func TestCreatePaymentOrderLienHandler(t *testing.T) {
	t.Parallel()

	t.Run("creates lien successfully", func(t *testing.T) {
		expectedLienID := uuid.New().String()
		mockClient := &MockCurrentAccountClient{
			initiateLienResp: &currentaccountv1.InitiateLienResponse{
				Lien: &currentaccountv1.Lien{LienId: expectedLienID},
			},
		}

		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			CurrentAccountClient: mockClient,
			Logger:               testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.create_lien")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		result, err := handler(ctx, map[string]any{
			"account_id":       "account-123",
			"amount_cents":     int64(10000),
			"currency":         "GBP",
			"payment_order_id": "payment-order-456",
		})

		require.NoError(t, err)
		resultMap := result.(map[string]any)
		assert.Equal(t, expectedLienID, resultMap["lien_id"])
		assert.Equal(t, "ACTIVE", resultMap["status"])

		// Verify the mock was called
		mockClient.mu.Lock()
		assert.True(t, mockClient.initiateLienCalled)
		assert.Equal(t, "account-123", mockClient.lastInitiateLienRequest.AccountId)
		assert.Equal(t, "payment-order-456", mockClient.lastInitiateLienRequest.PaymentOrderReference)
		mockClient.mu.Unlock()
	})

	t.Run("fails with missing params", func(t *testing.T) {
		mockClient := &MockCurrentAccountClient{
			initiateLienResp: &currentaccountv1.InitiateLienResponse{
				Lien: &currentaccountv1.Lien{LienId: "lien-123"},
			},
		}

		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			CurrentAccountClient: mockClient,
			Logger:               testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.create_lien")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		// Missing account_id
		_, err = handler(ctx, map[string]any{
			"amount_cents":     int64(10000),
			"currency":         "GBP",
			"payment_order_id": "payment-order-456",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "account_id")
	})

	t.Run("fails when current account client not configured", func(t *testing.T) {
		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			CurrentAccountClient: nil, // Not configured
			Logger:               testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.create_lien")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		_, err = handler(ctx, map[string]any{
			"account_id":       "account-123",
			"amount_cents":     int64(10000),
			"currency":         "GBP",
			"payment_order_id": "payment-order-456",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "current account client not configured")
	})

	t.Run("handles lien creation failure", func(t *testing.T) {
		mockClient := &MockCurrentAccountClient{
			initiateLienErr: ErrMalformedLienResponse, // Using an existing error
		}

		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			CurrentAccountClient: mockClient,
			Logger:               testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.create_lien")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		_, err = handler(ctx, map[string]any{
			"account_id":       "account-123",
			"amount_cents":     int64(10000),
			"currency":         "GBP",
			"payment_order_id": "payment-order-456",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create lien")
	})
}

func TestSendToGatewayHandler(t *testing.T) {
	t.Parallel()

	t.Run("sends payment successfully", func(t *testing.T) {
		expectedGatewayRef := "GW-" + uuid.New().String()
		mockGateway := &MockPaymentGateway{
			response: gateway.PaymentResponse{
				GatewayReferenceID: expectedGatewayRef,
				Status:             gateway.StatusAccepted,
			},
		}

		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			PaymentGateway: mockGateway,
			Logger:         testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.send_to_gateway")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		paymentOrderID := uuid.New().String()
		result, err := handler(ctx, map[string]any{
			"payment_order_id":   paymentOrderID,
			"debtor_account_id":  "debtor-account",
			"creditor_reference": "creditor-ref",
			"amount_cents":       int64(5000),
			"currency":           "GBP",
			"idempotency_key":    "idemp-key-123",
		})

		require.NoError(t, err)
		resultMap := result.(map[string]any)
		assert.Equal(t, expectedGatewayRef, resultMap["gateway_reference_id"])
		assert.Equal(t, "ACCEPTED", resultMap["gateway_status"])
		assert.Equal(t, 1, mockGateway.callCount)
	})

	t.Run("fails when gateway rejects payment", func(t *testing.T) {
		mockGateway := &MockPaymentGateway{
			response: gateway.PaymentResponse{
				Status:  gateway.StatusRejected,
				Message: "Insufficient funds",
			},
		}

		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			PaymentGateway: mockGateway,
			Logger:         testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.send_to_gateway")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		paymentOrderID := uuid.New().String()
		_, err = handler(ctx, map[string]any{
			"payment_order_id":   paymentOrderID,
			"debtor_account_id":  "debtor-account",
			"creditor_reference": "creditor-ref",
			"amount_cents":       int64(5000),
			"currency":           "GBP",
			"idempotency_key":    "idemp-key-123",
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPaymentRejected)
	})

	t.Run("fails when gateway not configured", func(t *testing.T) {
		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			PaymentGateway: nil, // Not configured
			Logger:         testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.send_to_gateway")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		paymentOrderID := uuid.New().String()
		_, err = handler(ctx, map[string]any{
			"payment_order_id":   paymentOrderID,
			"debtor_account_id":  "debtor-account",
			"creditor_reference": "creditor-ref",
			"amount_cents":       int64(5000),
			"currency":           "GBP",
			"idempotency_key":    "idemp-key-123",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "payment gateway not configured")
	})
}

func TestExecuteLienHandler(t *testing.T) {
	t.Parallel()

	t.Run("executes lien successfully", func(t *testing.T) {
		mockClient := &MockCurrentAccountClient{
			executeLienResp: &currentaccountv1.ExecuteLienResponse{},
		}

		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			CurrentAccountClient: mockClient,
			Logger:               testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.execute_lien")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		result, err := handler(ctx, map[string]any{
			"lien_id": "lien-123",
		})

		require.NoError(t, err)
		resultMap := result.(map[string]any)
		execStatus := resultMap["execution_status"].(map[string]any)
		assert.True(t, execStatus["success"].(bool))
		assert.Equal(t, 1, execStatus["attempts"])
	})

	t.Run("fails when client not configured", func(t *testing.T) {
		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			CurrentAccountClient: nil, // Not configured
			Logger:               testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.execute_lien")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		_, err = handler(ctx, map[string]any{
			"lien_id": "lien-123",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "current account client not configured")
	})
}

func TestTerminateLienHandler(t *testing.T) {
	t.Parallel()

	t.Run("terminates lien successfully", func(t *testing.T) {
		mockClient := &MockCurrentAccountClient{
			terminateLienResp: &currentaccountv1.TerminateLienResponse{},
		}

		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			CurrentAccountClient: mockClient,
			Logger:               testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.terminate_lien")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		result, err := handler(ctx, map[string]any{
			"lien_id": "lien-123",
		})

		require.NoError(t, err)
		resultMap := result.(map[string]any)
		assert.Equal(t, "lien-123", resultMap["lien_id"])
		assert.Equal(t, "TERMINATED", resultMap["status"])
		assert.Equal(t, 1, mockClient.terminateLienCalls)
	})
}

func TestPostLedgerEntriesHandler(t *testing.T) {
	t.Parallel()

	t.Run("fails when orchestrator not configured", func(t *testing.T) {
		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			Orchestrator: nil, // Not configured
			Logger:       testLogger(),
		}
		err := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, err)

		handler, err := registry.Get("payment_order.post_ledger_entries")
		require.NoError(t, err)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		_, err = handler(ctx, map[string]any{
			"payment_order_id":     uuid.New().String(),
			"debtor_account_id":    "account-123",
			"gateway_reference_id": "GW-123",
			"amount_cents":         int64(10000),
			"currency":             "GBP",
			"idempotency_key":      "idemp-key",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "orchestrator not configured")
	})

	t.Run("succeeds with valid orchestrator", func(t *testing.T) {
		fa := &MockFinancialAccountingClient{}
		gwConfig := testGatewayAccountConfig()

		orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
			Logger:                    testLogger(),
			Repo:                      NewMockRepository(),
			FinancialAccountingClient: fa,
			GatewayAccountConfig:      gwConfig,
			CurrentAccountClient:      &MockCurrentAccountClient{},
			PaymentGateway:            &MockPaymentGateway{},
		})
		require.NoError(t, err)

		registry := saga.NewHandlerRegistry()
		deps := &PaymentOrderHandlerDeps{
			Orchestrator: orchestrator,
			Logger:       testLogger(),
		}
		regErr := RegisterPaymentOrderHandlers(registry, deps)
		require.NoError(t, regErr)

		handler, getErr := registry.Get("payment_order.post_ledger_entries")
		require.NoError(t, getErr)

		ctx := &saga.StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          testLogger(),
		}

		result, handlerErr := handler(ctx, map[string]any{
			"payment_order_id":     uuid.New().String(),
			"debtor_account_id":    "account-123",
			"gateway_reference_id": "GW-ref-456",
			"amount_cents":         int64(10000),
			"currency":             "GBP",
			"idempotency_key":      "idemp-key",
		})

		require.NoError(t, handlerErr)
		resultMap, ok := result.(map[string]any)
		require.True(t, ok)
		assert.NotEmpty(t, resultMap["booking_log_id"])
		assert.Equal(t, "POSTED", resultMap["status"])
	})
}

// TestMustNewMoney tests the helper function.
func TestMustNewMoney(t *testing.T) {
	t.Parallel()

	t.Run("creates valid money", func(t *testing.T) {
		m := mustNewMoney("GBP", 10050)
		assert.Equal(t, "GBP", domain.CurrencyCode(m))
	})

	t.Run("returns zero money for invalid currency", func(_ *testing.T) {
		m := mustNewMoney("INVALID", 10000)
		// Should return zero money without panicking
		_ = m
	})
}

// TestParameterExtraction tests parameter extraction helpers.
func TestSagaParameterExtraction(t *testing.T) {
	t.Parallel()

	t.Run("requireStringParam", func(t *testing.T) {
		params := map[string]any{
			"key1": "value1",
			"key2": 123,
		}

		val, err := requireStringParam(params, "key1")
		require.NoError(t, err)
		assert.Equal(t, "value1", val)

		_, err = requireStringParam(params, "key2")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be string")

		_, err = requireStringParam(params, "missing")
		require.Error(t, err)
		assert.ErrorIs(t, err, saga.ErrMissingParam)
	})

	t.Run("requireInt64Param", func(t *testing.T) {
		params := map[string]any{
			"int64":   int64(100),
			"int":     42,
			"float64": 99.5,
			"string":  "not a number",
		}

		val, err := requireInt64Param(params, "int64")
		require.NoError(t, err)
		assert.Equal(t, int64(100), val)

		val, err = requireInt64Param(params, "int")
		require.NoError(t, err)
		assert.Equal(t, int64(42), val)

		val, err = requireInt64Param(params, "float64")
		require.NoError(t, err)
		assert.Equal(t, int64(99), val)

		_, err = requireInt64Param(params, "string")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be numeric")
	})

	t.Run("getStringParamOrEmpty", func(t *testing.T) {
		params := map[string]any{
			"key1": "value1",
			"key2": 123,
		}

		assert.Equal(t, "value1", getStringParamOrEmpty(params, "key1"))
		assert.Equal(t, "", getStringParamOrEmpty(params, "key2"))
		assert.Equal(t, "", getStringParamOrEmpty(params, "missing"))
	})

	t.Run("getMapParamOrEmpty", func(t *testing.T) {
		params := map[string]any{
			"map_any": map[string]any{
				"a": "1",
				"b": "2",
			},
			"map_string": map[string]string{
				"x": "y",
			},
			"not_map": "string",
		}

		result := getMapParamOrEmpty(params, "map_any")
		assert.Equal(t, "1", result["a"])
		assert.Equal(t, "2", result["b"])

		result = getMapParamOrEmpty(params, "map_string")
		assert.Equal(t, "y", result["x"])

		assert.Nil(t, getMapParamOrEmpty(params, "not_map"))
		assert.Nil(t, getMapParamOrEmpty(params, "missing"))
	})
}
