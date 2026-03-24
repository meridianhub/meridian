package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewPaymentOrchestrator_NilLogger verifies that a nil logger returns ErrOrchestratorLoggerNil.
func TestNewPaymentOrchestrator_NilLogger(t *testing.T) {
	t.Parallel()

	_, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger: nil,
		Repo:   NewMockRepository(),
	})

	assert.ErrorIs(t, err, ErrOrchestratorLoggerNil)
}

// TestNewPaymentOrchestrator_NilRepo verifies that a nil repo returns ErrOrchestratorRepoNil.
func TestNewPaymentOrchestrator_NilRepo(t *testing.T) {
	t.Parallel()

	_, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger: testLogger(),
		Repo:   nil,
	})

	assert.ErrorIs(t, err, ErrOrchestratorRepoNil)
}

// TestNewPaymentOrchestrator_MinimalConfig verifies that an orchestrator can be created
// with only the required logger and repo dependencies.
func TestNewPaymentOrchestrator_MinimalConfig(t *testing.T) {
	t.Parallel()

	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger: testLogger(),
		Repo:   NewMockRepository(),
	})

	require.NoError(t, err)
	assert.NotNil(t, o)
	assert.NotNil(t, o.logger)
	assert.NotNil(t, o.repo)
	assert.NotNil(t, o.starlarkRunner)
	assert.NotNil(t, o.handlerRegistry)
	assert.NotNil(t, o.bucketEvaluator)
}

// TestNewPaymentOrchestrator_FullConfig verifies that an orchestrator is created with all optional deps.
func TestNewPaymentOrchestrator_FullConfig(t *testing.T) {
	t.Parallel()

	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                    testLogger(),
		Repo:                      NewMockRepository(),
		CurrentAccountClient:      &MockCurrentAccountClient{},
		PaymentGateway:            &MockPaymentGateway{},
		FinancialAccountingClient: &MockFinancialAccountingClient{},
		GatewayAccountConfig:      testGatewayAccountConfig(),
		SagaOrchestrationEnabled:  true,
	})

	require.NoError(t, err)
	assert.NotNil(t, o)
	assert.NotNil(t, o.currentAccountClient)
	assert.NotNil(t, o.paymentGateway)
	assert.NotNil(t, o.financialAccountingClient)
	assert.NotNil(t, o.gatewayAccountConfig)
	assert.True(t, o.sagaOrchestrationEnabled)
}

// TestNewPaymentOrchestrator_AutoCreatesAccountResolver verifies that when InternalAccountClient
// is provided without an AccountResolver, one is created automatically.
func TestNewPaymentOrchestrator_AutoCreatesAccountResolver(t *testing.T) {
	t.Parallel()

	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                testLogger(),
		Repo:                  NewMockRepository(),
		InternalAccountClient: &mockInternalAccountClient{},
		// AccountResolver intentionally omitted to trigger auto-creation
	})

	require.NoError(t, err)
	assert.NotNil(t, o.accountResolver)
}

// TestNewPaymentOrchestrator_InternalClearingDisabledByDefault verifies feature flag defaults.
func TestNewPaymentOrchestrator_InternalClearingDisabledByDefault(t *testing.T) {
	t.Parallel()

	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger: testLogger(),
		Repo:   NewMockRepository(),
	})

	require.NoError(t, err)
	assert.False(t, o.internalClearingEnabled)
	assert.False(t, o.sagaOrchestrationEnabled)
}
