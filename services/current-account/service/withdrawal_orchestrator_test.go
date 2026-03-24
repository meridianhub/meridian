package service

import (
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// NewWithdrawalOrchestrator - constructor validation
// =============================================================================

func TestNewWithdrawalOrchestrator_MissingScript(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, _, cleanup := setupTestDB(t)
	defer cleanup()
	repo := newTestRepo(db)
	sagaRunner, _, _ := testSagaRunner(t)

	_, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:           logger,
		Repo:             repo,
		SagaRunner:       sagaRunner,
		WithdrawalScript: "", // missing
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOrchestratorWithdrawalScriptEmpty)
}

func TestNewWithdrawalOrchestrator_AllOptionalFieldsNil(t *testing.T) {
	// Valid orchestrator with no optional fields set
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, _, cleanup := setupTestDB(t)
	defer cleanup()
	repo := newTestRepo(db)
	sagaRunner, _, withdrawalScript := testSagaRunner(t)

	orch, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:           logger,
		Repo:             repo,
		SagaRunner:       sagaRunner,
		WithdrawalScript: withdrawalScript,
		// All optional fields nil
	})
	require.NoError(t, err)
	assert.NotNil(t, orch)
}

// =============================================================================
// WithdrawalOrchestrator.Orchestrate - fungibility validation failure
// =============================================================================

func TestWithdrawalOrchestrator_Orchestrate_FungibilityValidationFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := newTestRepo(db)
	sagaRunner, _, withdrawalScript := testSagaRunner(t)

	// Use a getter that always errors to force fungibility validation failure
	failingGetter := &mockInstrumentGetter{
		err: errors.New("instrument lookup failed"),
	}
	failingValidator := NewFungibilityValidator(failingGetter)

	orch, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:               logger,
		Repo:                 repo,
		PosKeepingClient:     &mockPositionKeepingClient{},
		FinAcctClient:        &mockFinancialAccountingClient{},
		SagaRunner:           sagaRunner,
		WithdrawalScript:     withdrawalScript,
		FungibilityValidator: failingValidator,
	})
	require.NoError(t, err)

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-KWH-001").
		WithInstrumentCode("KWH").
		Build()

	amount, err := domain.NewAmountFromInstrument("KWH", "ENERGY", 3, 1000)
	require.NoError(t, err)

	_, err = orch.Orchestrate(ctx, account, amount, uuid.New().String(), map[string]string{"batch_id": "BATCH-001"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fungibility validation failed")
}

// =============================================================================
// WithdrawalOrchestrator.resolveClearingAccountID - nil configuration
// =============================================================================

func TestWithdrawalOrchestrator_ResolveClearingAccountID_NilAll(t *testing.T) {
	orch := &WithdrawalOrchestrator{
		logger:          slog.Default(),
		accountConfig:   nil,
		accountResolver: nil,
	}

	result := orch.resolveClearingAccountID(t.Context(), "GBP")
	assert.Equal(t, "", result, "should return empty string when no resolver or config")
}
