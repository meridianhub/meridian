package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/services/payment-order/domain/testfixtures"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Saga Orchestration — handleSagaFailure, handleSagaSuccess, validateSagaOutputs
// =============================================================================

func TestHandleSagaFailure_ExtractsPartialLienAndTransitionsToReserved(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	lienID := "lien-partial-" + uuid.New().String()
	result := &saga.RunnerOutput{
		Success: false,
		Error:   "send_to_gateway failed",
		StepResults: []saga.StepResult{
			{
				StepName: "payment_order.create_lien",
				Success:  true,
				Output: map[string]any{
					"lien_id": lienID,
				},
			},
			{
				StepName: "payment_order.send_to_gateway",
				Success:  false,
				Error:    "gateway timeout",
			},
		},
	}

	orchestrator.handleStarlarkSagaResult(context.Background(), po, result)

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
	assert.Equal(t, "SAGA_FAILED", updated.ErrorCode)
	// The lien_id should have been set during RESERVED transition before failure
	assert.Equal(t, lienID, updated.LienID)
}

func TestHandleSagaFailure_NoLienCreated(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	result := &saga.RunnerOutput{
		Success: false,
		Error:   "create_lien failed: insufficient funds",
		StepResults: []saga.StepResult{
			{
				StepName: "payment_order.create_lien",
				Success:  false,
				Error:    "insufficient funds",
			},
		},
	}

	orchestrator.handleStarlarkSagaResult(context.Background(), po, result)

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
	assert.Empty(t, updated.LienID) // No lien was created
}

func TestHandleSagaFailure_ReloadError(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	// Don't create the PO in repo so FindByID fails
	po := testfixtures.NewPaymentOrder(t)

	result := &saga.RunnerOutput{
		Success: false,
		Error:   "some failure",
		StepResults: []saga.StepResult{
			{StepName: "payment_order.create_lien", Success: true, Output: map[string]any{"lien_id": "lien-x"}},
		},
	}

	// Should not panic — handles reload error gracefully
	orchestrator.handleStarlarkSagaResult(context.Background(), po, result)

	// PO was never stored, so nothing to verify in repo
	_, err := repo.FindByID(context.Background(), po.ID)
	assert.Error(t, err)
}

func TestHandleSagaSuccess_MissingLienID(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	result := &saga.RunnerOutput{
		Success: true,
		Output:  map[string]any{},
		StepResults: []saga.StepResult{
			{StepName: "payment_order.create_lien", Success: true, Output: map[string]any{}},
		},
	}

	orchestrator.handleStarlarkSagaResult(context.Background(), po, result)

	// Should be marked FAILED because create_lien succeeded but no lien_id in output
	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
	assert.Equal(t, "SAGA_OUTPUT_INVALID", updated.ErrorCode)
}

func TestHandleSagaSuccess_MissingGatewayReferenceID(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	result := &saga.RunnerOutput{
		Success: true,
		Output: map[string]any{
			"lien_id": "lien-123",
		},
		StepResults: []saga.StepResult{
			{StepName: "payment_order.create_lien", Success: true, Output: map[string]any{"lien_id": "lien-123"}},
			{StepName: "payment_order.send_to_gateway", Success: true, Output: map[string]any{}},
		},
	}

	orchestrator.handleStarlarkSagaResult(context.Background(), po, result)

	// Should be marked FAILED because send_to_gateway succeeded but no gateway_reference_id
	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
	assert.Equal(t, "SAGA_OUTPUT_INVALID", updated.ErrorCode)
}

func TestHandleSagaSuccess_ReloadError(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	// PO not in repo — reload will fail
	po := testfixtures.NewPaymentOrder(t)

	result := &saga.RunnerOutput{
		Success: true,
		Output: map[string]any{
			"lien_id":              "lien-123",
			"gateway_reference_id": "gw-123",
		},
		StepResults: []saga.StepResult{
			{StepName: "payment_order.create_lien", Success: true},
			{StepName: "payment_order.send_to_gateway", Success: true},
		},
	}

	// Should handle gracefully
	orchestrator.handleStarlarkSagaResult(context.Background(), po, result)

	_, err := repo.FindByID(context.Background(), po.ID)
	assert.Error(t, err)
}

func TestApplyReservedTransition_NoLienID(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	orchestrator.applyReservedTransition(context.Background(), po, "", "")

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusInitiated, updated.Status)
}

func TestApplyReservedTransition_NotInInitiatedState(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusReserved)
	require.NoError(t, repo.Create(context.Background(), po))

	// Should no-op — already past INITIATED
	orchestrator.applyReservedTransition(context.Background(), po, "lien-new", "bucket-1")

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusReserved, updated.Status)
}

func TestApplyReservedTransition_WithBucketID(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	orchestrator.applyReservedTransition(context.Background(), po, "lien-abc", "bucket-42")

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusReserved, updated.Status)
	assert.Equal(t, "lien-abc", updated.LienID)
	assert.Equal(t, "bucket-42", updated.BucketID)
}

func TestApplyExecutingTransition_NoGatewayRef(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusReserved)
	require.NoError(t, repo.Create(context.Background(), po))

	orchestrator.applyExecutingTransition(context.Background(), po, "")

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusReserved, updated.Status)
}

func TestApplyExecutingTransition_NotReserved(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t) // INITIATED state
	require.NoError(t, repo.Create(context.Background(), po))

	orchestrator.applyExecutingTransition(context.Background(), po, "gw-ref-123")

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusInitiated, updated.Status)
}

func TestApplyExecutingTransition_Success(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusReserved)
	require.NoError(t, repo.Create(context.Background(), po))

	orchestrator.applyExecutingTransition(context.Background(), po, "gw-ref-456")

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusExecuting, updated.Status)
	assert.Equal(t, "gw-ref-456", updated.GatewayReferenceID)
}

// =============================================================================
// Orchestrator failPaymentOrder — lien release, compensation
// =============================================================================

func TestOrchestratorFailPaymentOrder_WithLienRelease(t *testing.T) {
	t.Parallel()

	mockCA := &MockCurrentAccountClient{
		terminateLienResp: &currentaccountv1.TerminateLienResponse{},
	}

	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.CurrentAccountClient = mockCA
	})

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusReserved,
		testfixtures.WithLienID("lien-to-release"),
	)
	require.NoError(t, repo.Create(context.Background(), po))

	err := orchestrator.failPaymentOrder(context.Background(), po, "test failure", "TEST_ERROR")
	require.NoError(t, err)

	assert.Equal(t, 1, mockCA.terminateLienCalls)

	updated, findErr := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
}

func TestOrchestratorFailPaymentOrder_NoLienRelease_Initiated(t *testing.T) {
	t.Parallel()

	mockCA := &MockCurrentAccountClient{
		terminateLienResp: &currentaccountv1.TerminateLienResponse{},
	}

	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.CurrentAccountClient = mockCA
	})

	po := testfixtures.NewPaymentOrder(t) // INITIATED — no lien
	require.NoError(t, repo.Create(context.Background(), po))

	err := orchestrator.failPaymentOrder(context.Background(), po, "test failure", "TEST_ERROR")
	require.NoError(t, err)

	// No lien to release
	assert.Equal(t, 0, mockCA.terminateLienCalls)
}

func TestOrchestratorFailPaymentOrder_AlreadyFailed_Idempotent(t *testing.T) {
	t.Parallel()

	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusFailed)
	require.NoError(t, repo.Create(context.Background(), po))

	// Calling failPaymentOrder on already-failed PO should be idempotent
	err := orchestrator.failPaymentOrder(context.Background(), po, "another reason", "ANOTHER_CODE")
	require.NoError(t, err)
}

func TestOrchestratorFailPaymentOrder_TerminateLienError(t *testing.T) {
	t.Parallel()

	mockCA := &MockCurrentAccountClient{
		terminateLienErr: errors.New("lien service unavailable"),
	}

	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.CurrentAccountClient = mockCA
	})

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusReserved,
		testfixtures.WithLienID("lien-fail-release"),
	)
	require.NoError(t, repo.Create(context.Background(), po))

	// Should not return error — lien release failure is logged but swallowed
	err := orchestrator.failPaymentOrder(context.Background(), po, "failure", "FAIL")
	require.NoError(t, err)

	updated, findErr := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
}

// =============================================================================
// ExecutePaymentSaga — nil dependencies
// =============================================================================

func TestExecutePaymentSaga_NilCurrentAccountClient(t *testing.T) {
	t.Parallel()

	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.CurrentAccountClient = nil
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	assert.Nil(t, output)
	assert.ErrorIs(t, err, ErrSagaDepsNotConfigured)

	updated, findErr := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
}

func TestExecutePaymentSaga_NilPaymentGateway(t *testing.T) {
	t.Parallel()

	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.PaymentGateway = nil
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	assert.Nil(t, output)
	assert.ErrorIs(t, err, ErrSagaDepsNotConfigured)
}

func TestExecutePaymentSaga_InvalidCorrelationID(t *testing.T) {
	t.Parallel()

	orchestrator, repo, sagaLogger := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t)
	po.CorrelationID = "not-a-uuid"
	require.NoError(t, repo.Create(context.Background(), po))

	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	// Should succeed — generates new correlation ID
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.True(t, output.Success)

	// Verify the logged execution has a valid correlation ID (not "not-a-uuid")
	executions := sagaLogger.Executions()
	require.GreaterOrEqual(t, len(executions), 1)
	_, parseErr := uuid.Parse(executions[0].CorrelationID)
	assert.NoError(t, parseErr, "should have generated a valid UUID for correlation ID")
}

func TestExecutePaymentSaga_SagaRunnerError(t *testing.T) {
	t.Parallel()

	// Division by zero in Starlark returns Success=false (not a Go error)
	refClient := NewMockReferenceDataClient()
	refClient.sagaScript = `
x = 1 / 0
output = {}
`

	orchestrator, repo, sagaLogger := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.ReferenceDataClient = refClient
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	// Starlark runtime errors result in Success=false, not a Go error
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.False(t, output.Success)
	assert.Contains(t, output.Error, "division by zero")

	// Execution records should include FAILED entry
	executions := sagaLogger.Executions()
	require.GreaterOrEqual(t, len(executions), 2)
	last := executions[len(executions)-1]
	assert.Equal(t, domain.SagaExecutionStatusFailed, last.Status)
	assert.NotEmpty(t, last.ErrorMessage)

	// Payment order should be FAILED
	updated, findErr := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
}

func TestExecutePaymentSaga_SagaExecutionLoggerError(t *testing.T) {
	t.Parallel()

	sagaLogger := NewMockSagaExecutionLogger()
	sagaLogger.err = errors.New("persistence error")

	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.SagaExecutionLogger = sagaLogger
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	// Should not fail — logger errors are logged and swallowed
	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	require.NoError(t, err)
	require.NotNil(t, output)
	assert.True(t, output.Success)
}

// =============================================================================
// Saga Handlers — unhappy path
// =============================================================================

func TestSendToGatewayHandler_UnexpectedGatewayStatus(t *testing.T) {
	t.Parallel()

	mockGateway := &MockPaymentGateway{
		response: gateway.PaymentResponse{
			Status:  gateway.Status("UNKNOWN_STATUS"),
			Message: "unexpected",
		},
	}

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		PaymentGateway: mockGateway,
		Logger:         testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.send_to_gateway")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          testLogger(),
	}

	_, err = handler(ctx, map[string]any{
		"payment_order_id":   uuid.New().String(),
		"debtor_account_id":  "debtor",
		"creditor_reference": "creditor",
		"amount_cents":       int64(1000),
		"currency":           "GBP",
		"idempotency_key":    "idemp-123",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnexpectedGatewayStatus)
}

func TestSendToGatewayHandler_GatewayError(t *testing.T) {
	t.Parallel()

	mockGateway := &MockPaymentGateway{
		err: errors.New("gateway unavailable"),
	}

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		PaymentGateway: mockGateway,
		Logger:         testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.send_to_gateway")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          testLogger(),
	}

	_, err = handler(ctx, map[string]any{
		"payment_order_id":   uuid.New().String(),
		"debtor_account_id":  "debtor",
		"creditor_reference": "creditor",
		"amount_cents":       int64(1000),
		"currency":           "GBP",
		"idempotency_key":    "idemp-123",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send payment")
}

func TestSendToGatewayHandler_InvalidPaymentOrderID(t *testing.T) {
	t.Parallel()

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		PaymentGateway: &MockPaymentGateway{response: gateway.PaymentResponse{Status: gateway.StatusAccepted}},
		Logger:         testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.send_to_gateway")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          testLogger(),
	}

	_, err = handler(ctx, map[string]any{
		"payment_order_id":   "not-a-uuid",
		"debtor_account_id":  "debtor",
		"creditor_reference": "creditor",
		"amount_cents":       int64(1000),
		"currency":           "GBP",
		"idempotency_key":    "idemp-123",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid payment_order_id")
}

func TestSendToGatewayHandler_MissingParams(t *testing.T) {
	t.Parallel()

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		PaymentGateway: &MockPaymentGateway{response: gateway.PaymentResponse{Status: gateway.StatusAccepted}},
		Logger:         testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.send_to_gateway")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          testLogger(),
	}

	testCases := []struct {
		name    string
		params  map[string]any
		missing string
	}{
		{
			name: "missing payment_order_id",
			params: map[string]any{
				"debtor_account_id":  "debtor",
				"creditor_reference": "creditor",
				"amount_cents":       int64(1000),
				"currency":           "GBP",
				"idempotency_key":    "idemp-123",
			},
			missing: "payment_order_id",
		},
		{
			name: "missing debtor_account_id",
			params: map[string]any{
				"payment_order_id":   uuid.New().String(),
				"creditor_reference": "creditor",
				"amount_cents":       int64(1000),
				"currency":           "GBP",
				"idempotency_key":    "idemp-123",
			},
			missing: "debtor_account_id",
		},
		{
			name: "missing amount_cents",
			params: map[string]any{
				"payment_order_id":   uuid.New().String(),
				"debtor_account_id":  "debtor",
				"creditor_reference": "creditor",
				"currency":           "GBP",
				"idempotency_key":    "idemp-123",
			},
			missing: "amount_cents",
		},
		{
			name: "missing currency",
			params: map[string]any{
				"payment_order_id":   uuid.New().String(),
				"debtor_account_id":  "debtor",
				"creditor_reference": "creditor",
				"amount_cents":       int64(1000),
				"idempotency_key":    "idemp-123",
			},
			missing: "currency",
		},
		{
			name: "missing idempotency_key",
			params: map[string]any{
				"payment_order_id":   uuid.New().String(),
				"debtor_account_id":  "debtor",
				"creditor_reference": "creditor",
				"amount_cents":       int64(1000),
				"currency":           "GBP",
			},
			missing: "idempotency_key",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler(ctx, tc.params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.missing)
		})
	}
}

func TestSendToGatewayHandler_PendingStatus(t *testing.T) {
	t.Parallel()

	mockGateway := &MockPaymentGateway{
		response: gateway.PaymentResponse{
			GatewayReferenceID: "gw-pending-123",
			Status:             gateway.StatusPending,
		},
	}

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		PaymentGateway: mockGateway,
		Logger:         testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.send_to_gateway")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          testLogger(),
	}

	result, err := handler(ctx, map[string]any{
		"payment_order_id":   uuid.New().String(),
		"debtor_account_id":  "debtor",
		"creditor_reference": "creditor",
		"amount_cents":       int64(1000),
		"currency":           "GBP",
		"idempotency_key":    "idemp-123",
	})

	require.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, "gw-pending-123", resultMap["gateway_reference_id"])
	assert.Equal(t, "PENDING", resultMap["gateway_status"])
}

func TestExecuteLienHandler_RetryExhaustion(t *testing.T) {
	t.Parallel()

	mockClient := &MockCurrentAccountClient{
		executeLienErr: errors.New("service unavailable"),
	}

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		CurrentAccountClient: mockClient,
		Logger:               testLogger(),
		LienExecutionRetryConfig: &sharedclients.RetryConfig{
			MaxRetries:          2,
			InitialInterval:     1 * time.Millisecond,
			MaxInterval:         5 * time.Millisecond,
			Multiplier:          1.5,
			RandomizationFactor: 0,
		},
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.execute_lien")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          testLogger(),
	}

	result, err := handler(ctx, map[string]any{
		"lien_id": "lien-fail-123",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "lien execution failed after")
	assert.Nil(t, result, "error path should return nil result")
}

func TestExecuteLienHandler_MissingLienID(t *testing.T) {
	t.Parallel()

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		CurrentAccountClient: &MockCurrentAccountClient{
			executeLienResp: &currentaccountv1.ExecuteLienResponse{},
		},
		Logger: testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.execute_lien")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          testLogger(),
	}

	_, err = handler(ctx, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lien_id")
}

func TestTerminateLienHandler_Failure(t *testing.T) {
	t.Parallel()

	mockClient := &MockCurrentAccountClient{
		terminateLienErr: errors.New("lien not found"),
	}

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		CurrentAccountClient: mockClient,
		Logger:               testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.terminate_lien")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          testLogger(),
	}

	_, err = handler(ctx, map[string]any{
		"lien_id": "lien-nonexistent",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to terminate lien")
}

func TestTerminateLienHandler_MissingLienID(t *testing.T) {
	t.Parallel()

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		CurrentAccountClient: &MockCurrentAccountClient{
			terminateLienResp: &currentaccountv1.TerminateLienResponse{},
		},
		Logger: testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.terminate_lien")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          testLogger(),
	}

	_, err = handler(ctx, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lien_id")
}

func TestTerminateLienHandler_ClientNotConfigured(t *testing.T) {
	t.Parallel()

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		CurrentAccountClient: nil,
		Logger:               testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.terminate_lien")
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
}

func TestTerminateLienHandler_CustomReason(t *testing.T) {
	t.Parallel()

	mockClient := &MockCurrentAccountClient{
		terminateLienResp: &currentaccountv1.TerminateLienResponse{},
	}

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		CurrentAccountClient: mockClient,
		Logger:               testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

	handler, err := registry.Get("payment_order.terminate_lien")
	require.NoError(t, err)

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          testLogger(),
	}

	result, err := handler(ctx, map[string]any{
		"lien_id": "lien-123",
		"reason":  "Custom compensation reason",
	})

	require.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, "TERMINATED", resultMap["status"])
}

func TestCreateLienHandler_MalformedResponse(t *testing.T) {
	t.Parallel()

	// Return a response with nil Lien
	mockClient := &MockCurrentAccountClient{
		initiateLienResp: &currentaccountv1.InitiateLienResponse{
			Lien: nil,
		},
	}

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		CurrentAccountClient: mockClient,
		Logger:               testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

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
		"payment_order_id": "po-456",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMalformedLienResponse)
}

func TestCreateLienHandler_EmptyLienID(t *testing.T) {
	t.Parallel()

	mockClient := &MockCurrentAccountClient{
		initiateLienResp: &currentaccountv1.InitiateLienResponse{
			Lien: &currentaccountv1.Lien{LienId: ""},
		},
	}

	registry := saga.NewHandlerRegistry()
	deps := &PaymentOrderHandlerDeps{
		CurrentAccountClient: mockClient,
		Logger:               testLogger(),
	}
	require.NoError(t, RegisterPaymentOrderHandlers(registry, deps))

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
		"payment_order_id": "po-456",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMalformedLienResponse)
}

// =============================================================================
// Parameter extraction helpers — additional coverage
// =============================================================================

func TestGetStringParamOrDefault(t *testing.T) {
	t.Parallel()

	params := map[string]any{
		"key1":    "value1",
		"numeric": 42,
	}

	assert.Equal(t, "value1", getStringParamOrDefault(params, "key1", "default"))
	assert.Equal(t, "default", getStringParamOrDefault(params, "missing", "default"))
	assert.Equal(t, "default", getStringParamOrDefault(params, "numeric", "default"))
}

func TestGetMapParamOrEmpty_NonStringValues(t *testing.T) {
	t.Parallel()

	params := map[string]any{
		"mixed_map": map[string]any{
			"str_val": "hello",
			"int_val": 123, // non-string value — should be skipped
		},
	}

	result := getMapParamOrEmpty(params, "mixed_map")
	assert.Equal(t, "hello", result["str_val"])
	_, hasIntVal := result["int_val"]
	assert.False(t, hasIntVal, "non-string values should be excluded")
}

func TestRequireInt64Param_MissingKey(t *testing.T) {
	t.Parallel()

	params := map[string]any{}
	_, err := requireInt64Param(params, "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, saga.ErrMissingParam)
}
