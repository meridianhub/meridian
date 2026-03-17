package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/services/payment-order/domain/testfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockSagaExecutionLogger implements domain.SagaExecutionLogger for testing.
type MockSagaExecutionLogger struct {
	mu         sync.Mutex
	executions []*domain.SagaExecution
	err        error
}

func NewMockSagaExecutionLogger() *MockSagaExecutionLogger {
	return &MockSagaExecutionLogger{}
}

func (m *MockSagaExecutionLogger) PersistExecution(_ context.Context, execution *domain.SagaExecution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	// Store a copy to avoid mutations
	exec := *execution
	m.executions = append(m.executions, &exec)
	return nil
}

func (m *MockSagaExecutionLogger) Executions() []*domain.SagaExecution {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*domain.SagaExecution, len(m.executions))
	copy(result, m.executions)
	return result
}

// sagaTestOrchestrator creates a PaymentOrchestrator configured for saga integration testing.
func sagaTestOrchestrator(t *testing.T, opts ...func(*PaymentOrchestratorConfig)) (*PaymentOrchestrator, *MockRepository, *MockSagaExecutionLogger) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()
	sagaLogger := NewMockSagaExecutionLogger()

	cfg := PaymentOrchestratorConfig{
		Logger: logger,
		Repo:   repo,
		CurrentAccountClient: &MockCurrentAccountClient{
			initiateLienResp: &currentaccountv1.InitiateLienResponse{
				Lien: &currentaccountv1.Lien{
					LienId: "lien-" + uuid.New().String(),
				},
			},
		},
		PaymentGateway: &MockPaymentGateway{
			response: gateway.PaymentResponse{
				GatewayReferenceID: "gw-ref-" + uuid.New().String(),
				Status:             "PENDING",
			},
		},
		ReferenceDataClient:      NewMockReferenceDataClient(),
		SagaExecutionLogger:      sagaLogger,
		SagaOrchestrationEnabled: true,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	orchestrator, err := NewPaymentOrchestrator(cfg)
	require.NoError(t, err)
	return orchestrator, repo, sagaLogger
}

// TestExecutePaymentSaga_DisabledReturnsError verifies that ExecutePaymentSaga
// returns ErrSagaOrchestrationDisabled when the feature flag is off.
func TestExecutePaymentSaga_DisabledReturnsError(t *testing.T) {
	orchestrator, _, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.SagaOrchestrationEnabled = false
	})

	po := testfixtures.NewPaymentOrder(t)
	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	assert.Nil(t, output)
	assert.ErrorIs(t, err, ErrSagaOrchestrationDisabled)
}

// TestExecutePaymentSaga_EnabledExecutesSaga verifies that when enabled,
// ExecutePaymentSaga fetches the saga definition and executes it.
func TestExecutePaymentSaga_EnabledExecutesSaga(t *testing.T) {
	orchestrator, repo, sagaLogger := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	require.NoError(t, err)
	require.NotNil(t, output)
	assert.True(t, output.Success)

	// Verify saga execution was logged
	executions := sagaLogger.Executions()
	require.GreaterOrEqual(t, len(executions), 2, "expected at least RUNNING + COMPLETED execution records")

	// First record should be RUNNING
	assert.Equal(t, domain.SagaExecutionStatusRunning, executions[0].Status)
	assert.Equal(t, "payment_execution", executions[0].SagaName)
	assert.Equal(t, po.ID, executions[0].PaymentOrderID)

	// Last record should be COMPLETED
	last := executions[len(executions)-1]
	assert.Equal(t, domain.SagaExecutionStatusCompleted, last.Status)
	assert.Greater(t, last.StepCount, 0)
	assert.NotNil(t, last.CompletedAt)
}

// TestExecutePaymentSaga_GetSagaError verifies that a failure to fetch the saga
// definition marks the payment order as failed and returns an error.
func TestExecutePaymentSaga_GetSagaError(t *testing.T) {
	refClient := NewMockReferenceDataClient()
	refClient.getSagaErr = errors.New("reference-data unavailable")

	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.ReferenceDataClient = refClient
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	assert.Nil(t, output)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch saga definition")

	// Payment order should be marked as FAILED
	updated, findErr := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
	assert.Equal(t, "INTERNAL_ERROR", updated.ErrorCode)
}

// TestExecutePaymentSaga_NilReferenceDataClient verifies that ExecutePaymentSaga
// fails when no reference data client is configured.
func TestExecutePaymentSaga_NilReferenceDataClient(t *testing.T) {
	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.ReferenceDataClient = nil
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	assert.Nil(t, output)
	assert.ErrorIs(t, err, ErrRefDataClientNotConfigured)
}

// TestExecutePaymentSaga_PersistsExecutionOnFailure verifies that when the saga
// execution fails, the execution record is persisted with FAILED status.
func TestExecutePaymentSaga_PersistsExecutionOnFailure(t *testing.T) {
	// Use a saga script that will fail - reference a non-existent handler
	refClient := NewMockReferenceDataClient()
	refClient.sagaScript = `
def bad_saga():
    step(name="will_fail")
    nonexistent.handler()
    return {}

output = bad_saga()
`

	orchestrator, repo, sagaLogger := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.ReferenceDataClient = refClient
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	// The saga runner returns a result with Success=false (not a Go error)
	// when a handler is not found. ExecutePaymentSaga returns the result.
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.False(t, output.Success)

	// Verify execution records were logged
	executions := sagaLogger.Executions()
	require.GreaterOrEqual(t, len(executions), 2, "expected RUNNING + FAILED execution records")

	// First should be RUNNING
	assert.Equal(t, domain.SagaExecutionStatusRunning, executions[0].Status)

	// Last should be FAILED (because output.Success was false)
	last := executions[len(executions)-1]
	assert.Equal(t, domain.SagaExecutionStatusFailed, last.Status)
	assert.NotEmpty(t, last.ErrorMessage)
	assert.NotNil(t, last.CompletedAt)
}

// TestOrchestrate_DisabledFlagMarksPaymentFailed verifies that when
// sagaOrchestrationEnabled is false, Orchestrate marks the payment order as
// FAILED with error code SAGA_DISABLED.
func TestOrchestrate_DisabledFlagMarksPaymentFailed(t *testing.T) {
	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.SagaOrchestrationEnabled = false
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	orchestrator.Orchestrate(context.Background(), po)

	// Payment order should be FAILED with SAGA_DISABLED
	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
	assert.Equal(t, "SAGA_DISABLED", updated.ErrorCode)
	assert.Contains(t, updated.FailureReason, "saga orchestration is disabled")
}

// TestOrchestrate_EnabledDelegatestoExecutePaymentSaga verifies that when
// enabled, Orchestrate delegates to ExecutePaymentSaga and processes the result.
func TestOrchestrate_EnabledDelegatestoExecutePaymentSaga(t *testing.T) {
	orchestrator, repo, sagaLogger := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	orchestrator.Orchestrate(context.Background(), po)

	// Saga should have executed and logged records
	executions := sagaLogger.Executions()
	require.GreaterOrEqual(t, len(executions), 2)

	// Last execution should be COMPLETED (the default mock saga script succeeds)
	last := executions[len(executions)-1]
	assert.Equal(t, domain.SagaExecutionStatusCompleted, last.Status)
}

// TestExecutePaymentSaga_NilSagaExecutionLogger verifies that the orchestrator
// works correctly when no execution logger is configured (optional dependency).
func TestExecutePaymentSaga_NilSagaExecutionLogger(t *testing.T) {
	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.SagaExecutionLogger = nil
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	// Should succeed without panicking despite nil logger
	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)

	require.NoError(t, err)
	require.NotNil(t, output)
	assert.True(t, output.Success)
}

// TestExecutePaymentSaga_ExecutionRecordContainsInput verifies that the
// persisted execution record contains the correct input data from the payment order.
func TestExecutePaymentSaga_ExecutionRecordContainsInput(t *testing.T) {
	orchestrator, repo, sagaLogger := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t,
		testfixtures.WithDebtorAccountID("DEBTOR-TEST-123"),
		testfixtures.WithCreditorReference("CRED-REF-456"),
	)
	require.NoError(t, repo.Create(context.Background(), po))

	_, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)
	require.NoError(t, err)

	executions := sagaLogger.Executions()
	require.GreaterOrEqual(t, len(executions), 1)

	// Check the RUNNING record has correct input
	input := executions[0].Input
	assert.Equal(t, po.ID.String(), input["payment_order_id"])
	assert.Equal(t, "DEBTOR-TEST-123", input["debtor_account_id"])
	assert.Equal(t, "CRED-REF-456", input["creditor_reference"])
}
