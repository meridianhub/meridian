package service

import (
	"context"
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
// NewDepositOrchestrator - constructor validation
// =============================================================================

func TestNewDepositOrchestrator_MissingDepositScript(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, _, cleanup := setupTestDB(t)
	defer cleanup()
	repo := newTestRepo(db)
	sagaRunner, _, _ := testSagaRunner(t)

	_, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:        logger,
		Repo:          repo,
		SagaRunner:    sagaRunner,
		DepositScript: "", // missing
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOrchestratorDepositScriptEmpty)
}

func TestNewDepositOrchestrator_AllOptionalFieldsNil(t *testing.T) {
	// Orchestrator is valid with no optional fields (AccountConfig, AccountResolver,
	// FungibilityValidator, SagaResolver) - they all have nil-safe defaults.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, _, cleanup := setupTestDB(t)
	defer cleanup()
	repo := newTestRepo(db)
	sagaRunner, depositScript, _ := testSagaRunner(t)

	orch, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:        logger,
		Repo:          repo,
		SagaRunner:    sagaRunner,
		DepositScript: depositScript,
		// All optional fields nil
	})
	require.NoError(t, err)
	assert.NotNil(t, orch)
}

// =============================================================================
// DepositOrchestrator.Orchestrate - invalid clearing account override
// =============================================================================

func TestDepositOrchestrator_Orchestrate_InvalidClearingAccountOverride(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := newTestRepo(db)
	sagaRunner, depositScript, _ := testSagaRunner(t)

	orch, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:           logger,
		Repo:             repo,
		PosKeepingClient: &mockPositionKeepingClient{},
		FinAcctClient:    &mockFinancialAccountingClient{},
		SagaRunner:       sagaRunner,
		DepositScript:    depositScript,
	})
	require.NoError(t, err)

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		Build()

	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Pass a non-UUID string as the clearing account override
	_, err = orch.Orchestrate(ctx, account, amount, uuid.New().String(), nil, "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid UUID")
}

// =============================================================================
// DepositOrchestrator.Orchestrate - fungibility validation failure
// =============================================================================

func TestDepositOrchestrator_Orchestrate_FungibilityValidationFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := newTestRepo(db)
	sagaRunner, depositScript, _ := testSagaRunner(t)

	// Use a getter that always errors to force fungibility validation failure
	failingGetter := &mockInstrumentGetter{
		err: errors.New("instrument lookup failed"),
	}
	failingValidator := NewFungibilityValidator(failingGetter)

	orch, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:               logger,
		Repo:                 repo,
		PosKeepingClient:     &mockPositionKeepingClient{},
		FinAcctClient:        &mockFinancialAccountingClient{},
		SagaRunner:           sagaRunner,
		DepositScript:        depositScript,
		FungibilityValidator: failingValidator,
	})
	require.NoError(t, err)

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-RICE-001").
		WithInstrumentCode("KWH").
		Build()

	amount, err := domain.NewAmountFromInstrument("KWH", "ENERGY", 3, 1000)
	require.NoError(t, err)

	_, err = orch.Orchestrate(ctx, account, amount, uuid.New().String(), map[string]string{"batch_id": "BATCH-001"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fungibility validation failed")
}

// =============================================================================
// DepositOrchestrator.resolveClearingAccountID - dynamic resolver success path
// (already tested in coverage_unit_test.go, but add explicit test for orchestrator struct)
// =============================================================================

func TestDepositOrchestrator_ResolveClearingAccountID_NilAll(t *testing.T) {
	orch := &DepositOrchestrator{
		logger:        slog.Default(),
		accountConfig: nil,
		accountResolver: nil,
	}

	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "", result, "should return empty string when no resolver or config")
}
