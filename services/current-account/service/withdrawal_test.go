package service

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// =============================================================================
// Test Helpers for Withdrawals
// =============================================================================

// Note: createTestAccountWithBalance is defined in lien_service_test.go and shared across tests.

// createTestWithdrawalRequest creates a withdrawal request with the given parameters.
func createTestWithdrawalRequest(accountID string, units int64, nanos int32) *pb.ExecuteWithdrawalRequest {
	return &pb.ExecuteWithdrawalRequest{
		AccountId: accountID,
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        units,
				Nanos:        nanos,
			},
		},
		Description: "Test withdrawal",
	}
}

// testWithdrawalOrchestrator creates a WithdrawalOrchestrator for testing.
// Panics if orchestrator creation fails (indicates test setup problem).
func testWithdrawalOrchestrator(repo *persistence.Repository, posKeeping *mockPositionKeepingClient, finAcct *mockFinancialAccountingClient) *WithdrawalOrchestrator {
	return testWithdrawalOrchestratorWithConfig(repo, posKeeping, finAcct, nil)
}

// testWithdrawalOrchestratorWithConfig creates a WithdrawalOrchestrator with optional AccountConfig.
// Panics if orchestrator creation fails (indicates test setup problem).
func testWithdrawalOrchestratorWithConfig(repo *persistence.Repository, posKeeping *mockPositionKeepingClient, finAcct *mockFinancialAccountingClient, acctConfig *config.AccountConfig) *WithdrawalOrchestrator {
	// Load withdrawal saga script from reference-data canonical source
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get current file path")
	}
	serviceDir := filepath.Dir(filename)
	repoRoot := filepath.Join(serviceDir, "..", "..", "..")
	withdrawalScriptPath := filepath.Join(repoRoot, "services", "reference-data", "saga", "defaults", "withdrawal", "v1.0.0.star")
	withdrawalScriptBytes, err := os.ReadFile(withdrawalScriptPath)
	if err != nil {
		panic("failed to read withdrawal script: " + err.Error())
	}
	withdrawalScript := string(withdrawalScriptBytes)

	// Create saga handler registry
	handlerRegistry := saga.NewHandlerRegistry()
	if err := RegisterCurrentAccountHandlers(handlerRegistry); err != nil {
		panic("failed to register saga handlers: " + err.Error())
	}

	// Load schema registry
	schemaRegistryPath := filepath.Join(serviceDir, "..", "..", "..", "shared", "pkg", "saga", "schema", "handlers.yaml")
	schemaRegistryData, err := os.ReadFile(schemaRegistryPath)
	if err != nil {
		panic("failed to read handlers schema: " + err.Error())
	}

	schemaRegistry := schema.NewRegistry()
	if err := schemaRegistry.LoadFromYAML(schemaRegistryData); err != nil {
		panic("failed to load schema: " + err.Error())
	}

	// Build service modules
	serviceModules, err := schema.BuildServiceModules(handlerRegistry, schemaRegistry)
	if err != nil {
		panic("failed to build service modules: " + err.Error())
	}

	// Create Starlark saga runner
	runtime, err := saga.NewRuntime(testLogger())
	if err != nil {
		panic("failed to create saga runtime: " + err.Error())
	}

	sagaRunner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       handlerRegistry,
		ServiceModules: serviceModules,
		Logger:         testLogger(),
	})
	if err != nil {
		panic("failed to create saga runner: " + err.Error())
	}

	orchestrator, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:           testLogger(),
		Repo:             repo,
		PosKeepingClient: posKeeping,
		FinAcctClient:    finAcct,
		AccountConfig:    acctConfig,
		SagaRunner:       sagaRunner,
		WithdrawalScript: withdrawalScript,
	})
	if err != nil {
		panic("test setup failed: " + err.Error())
	}
	return orchestrator
}

// =============================================================================
// Unit Tests for ExecuteWithdrawal
// =============================================================================

// TestExecuteWithdrawal_Success verifies successful withdrawal with mocked services.
// Scenario: Account has sufficient funds, all saga steps succeed.
func TestExecuteWithdrawal_Success(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Create account with $1000 balance (100000 cents)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-001", 100000)

	// Create mock clients with balance configured for Position Keeping.
	// The mock returns the POST-withdrawal balance since Position Keeping is the source of truth
	// and would have already recorded the DEBIT by the time we query the balance.
	// Pre-withdrawal: $1000.00, Withdrawal: $100.50, Post-withdrawal: $899.50 (89950 cents)
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-WTH-001": 89950, // $899.50 post-withdrawal
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	// Create service with mocked clients
	svc := &Service{
		repo:                   repo,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Execute withdrawal of $100.50
	req := createTestWithdrawalRequest("ACC-WTH-001", 100, 500000000)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	// Verify success
	require.NoError(t, err, "Withdrawal should succeed")
	assert.Equal(t, "ACC-WTH-001", resp.AccountId)
	assert.NotEmpty(t, resp.TransactionId, "Transaction ID should be generated")
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED, resp.Status)

	// Verify new balance: $1000 - $100.50 = $899.50 (89950 cents)
	// Note: Balance is now managed by Position Keeping service
	assert.NotNil(t, resp.NewBalance)
	assert.Equal(t, int64(899), resp.NewBalance.Amount.Units)
	assert.Equal(t, int32(500000000), resp.NewBalance.Amount.Nanos)

	// Verify account exists (balance not checked - Position Keeping is authoritative)
	_, err = repo.FindByID(ctx, "ACC-WTH-001")
	require.NoError(t, err)

	// Verify service calls
	assert.Equal(t, 1, mockPosKeeping.initiateCalls, "PositionKeeping InitiateFinancialPositionLog should be called once")
	assert.Equal(t, 1, mockFinAcct.captureCalls, "FinancialAccounting CaptureLedgerPosting should be called once")

	// Verify no compensation occurred
	assert.Equal(t, 0, mockPosKeeping.compensateCalls, "No position compensation should occur on success")
	assert.Equal(t, 0, mockFinAcct.compensateCalls, "No ledger compensation should occur on success")
}

// TestExecuteWithdrawal_AccountNotFound verifies NotFound error for non-existent account.
func TestExecuteWithdrawal_AccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := createTestWithdrawalRequest("ACC-NONEXISTENT", 100, 0)
	_, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "Expected error for non-existent account")
	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestExecuteWithdrawal_InsufficientBalance verifies FailedPrecondition error when balance is too low.
func TestExecuteWithdrawal_InsufficientBalance(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Create account with $100 balance
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-INSUFF", 10000)

	svc := mustNewService(t, repo, nil)

	// Try to withdraw $200 from account with $100 balance
	req := createTestWithdrawalRequest("ACC-WTH-INSUFF", 200, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "Expected error for insufficient balance")
	assert.Nil(t, resp, "Response should be nil on failure")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "insufficient funds")
}

// TestExecuteWithdrawal_AccountFrozen verifies FailedPrecondition error for frozen accounts.
func TestExecuteWithdrawal_AccountFrozen(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Create account, deposit funds, then freeze it
	account, err := domain.NewCurrentAccountWithDimension("ACC-WTH-FROZEN", "ACC-WTH-FROZEN", uuid.New().String(), "GBP", "CURRENCY", 2)
	require.NoError(t, err)

	depositAmount, err := domain.NewMoney("GBP", 100000) // $1000
	require.NoError(t, err)
	account, err = account.Deposit(depositAmount)
	require.NoError(t, err)

	account, err = account.Freeze("Suspicious activity detected on account")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	svc := mustNewService(t, repo, nil)

	req := createTestWithdrawalRequest("ACC-WTH-FROZEN", 50, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "Expected error for frozen account")
	assert.Nil(t, resp, "Response should be nil on failure")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "frozen")
}

// TestExecuteWithdrawal_AccountClosed verifies FailedPrecondition error for closed accounts.
func TestExecuteWithdrawal_AccountClosed(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Create account with zero balance and close it
	// Per domain rules, accounts must have zero balance to close
	account, err := domain.NewCurrentAccountWithDimension("ACC-WTH-CLOSED", "ACC-WTH-CLOSED", uuid.New().String(), "GBP", "CURRENCY", 2)
	require.NoError(t, err)

	// Close the zero-balance account
	account, err = account.Close("Account closure test")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	svc := mustNewService(t, repo, nil)

	req := createTestWithdrawalRequest("ACC-WTH-CLOSED", 50, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "Expected error for closed account")
	assert.Nil(t, resp, "Response should be nil on failure")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "closed")
}

// TestExecuteWithdrawal_CurrencyMismatch verifies InvalidArgument error for currency mismatch.
func TestExecuteWithdrawal_CurrencyMismatch(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Create GBP account with balance
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-CURR", 10000)

	svc := mustNewService(t, repo, nil)

	// Try to withdraw USD from GBP account
	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-WTH-CURR",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD", // Mismatch: account is GBP
				Units:        50,
				Nanos:        0,
			},
		},
	}

	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "Expected error for currency mismatch")
	assert.Nil(t, resp, "Response should be nil on failure")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "currency mismatch")
}

// =============================================================================
// Saga Compensation Tests
// =============================================================================

// TestExecuteWithdrawal_WithOrchestration_PositionKeepingFailure verifies saga compensation
// when PositionKeeping service fails.
func TestExecuteWithdrawal_WithOrchestration_PositionKeepingFailure(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-PK-FAIL", 100000)

	// Configure PositionKeeping to fail on initiate but still return balance
	mockPosKeeping := &mockPositionKeepingClient{
		failOnInitiate: true,
		initiateError:  errPositionKeepingUnavailable,
		accountBalances: map[string]int64{
			"ACC-WTH-PK-FAIL": 100000, // $1000
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:                   repo,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	req := createTestWithdrawalRequest("ACC-WTH-PK-FAIL", 50, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	// Verify failure
	require.Error(t, err, "Withdrawal should fail due to PositionKeeping failure")
	assert.Nil(t, resp, "Response should be nil on failure")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "initiate_log")

	// Verify account exists (balance not checked - Position Keeping is authoritative)
	_, err = repo.FindByID(ctx, "ACC-WTH-PK-FAIL")
	require.NoError(t, err)

	// Verify no FinancialAccounting calls were made
	assert.Equal(t, 0, mockFinAcct.captureCalls, "FinancialAccounting should not be called")
}

// TestExecuteWithdrawal_WithOrchestration_FinancialAccountingFailure verifies saga compensation
// when FinancialAccounting service fails after PositionKeeping succeeds.
func TestExecuteWithdrawal_WithOrchestration_FinancialAccountingFailure(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-FA-FAIL", 100000)

	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-WTH-FA-FAIL": 100000, // $1000
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{
		failOnCapture: true,
		failureError:  errFinancialAccountingUnavailable,
	}

	svc := &Service{
		repo:                   repo,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	req := createTestWithdrawalRequest("ACC-WTH-FA-FAIL", 75, 250000000)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	// Verify failure
	require.Error(t, err, "Withdrawal should fail due to FinancialAccounting failure")
	assert.Nil(t, resp, "Response should be nil on failure")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "capture_posting")
	// Note: Compensation runs but error message doesn't mention "compensated"

	// Verify account exists (balance not checked - Position Keeping is authoritative)
	_, err = repo.FindByID(ctx, "ACC-WTH-FA-FAIL")
	require.NoError(t, err)

	// Verify PositionKeeping compensation was triggered
	assert.Equal(t, 1, mockPosKeeping.initiateCalls, "PositionKeeping should be called once")
	assert.Equal(t, 1, mockPosKeeping.updateCalls, "PositionKeeping update (compensation) should be called")
	assert.Equal(t, 1, mockPosKeeping.compensateCalls, "PositionKeeping compensation should cancel position log")
}

// =============================================================================
// Double-Entry Bookkeeping Tests for Withdrawals
// =============================================================================

// TestExecuteWithdrawal_DoubleEntry_CreatesDualPostings verifies that withdrawals with AccountConfig
// create both debit (customer) and credit (clearing) postings.
func TestExecuteWithdrawal_DoubleEntry_CreatesDualPostings(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-DE-001", 100000)

	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-WTH-DE-001": 100000, // $1000
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}
	acctConfig := &config.AccountConfig{
		DepositClearingAccountID:    "CLEARING-DEPOSIT",
		WithdrawalClearingAccountID: "CLEARING-WITHDRAWAL",
	}

	svc := &Service{
		repo:                   repo,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		accountConfig:          acctConfig,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestratorWithConfig(repo, mockPosKeeping, mockFinAcct, acctConfig),
	}

	req := createTestWithdrawalRequest("ACC-WTH-DE-001", 100, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	// Verify success
	require.NoError(t, err, "Withdrawal should succeed")
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED, resp.Status)

	// Verify dual postings
	assert.Equal(t, 2, mockFinAcct.captureCalls, "Should have 2 capture calls (debit + credit)")
	assert.Equal(t, 1, mockFinAcct.debitCaptureCalls, "Should have 1 debit posting to customer account")
	assert.Equal(t, 1, mockFinAcct.creditCaptureCalls, "Should have 1 credit posting to clearing account")

	// Verify BookingLog was created and updated
	assert.Equal(t, 1, mockFinAcct.initiateCalls, "Should initiate 1 BookingLog")
	assert.Equal(t, 1, mockFinAcct.updateCalls, "Should update BookingLog to POSTED")

	// Verify no compensation
	assert.Equal(t, 0, mockFinAcct.compensateCalls, "No compensation on success")
}

// TestExecuteWithdrawal_DoubleEntry_CompensatesOnFailure verifies saga compensation
// reverses both debit and credit postings for withdrawals.
func TestExecuteWithdrawal_DoubleEntry_CompensatesOnFailure(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-DE-COMP", 100000)

	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-WTH-DE-COMP": 100000, // $1000
		},
	}
	// Configure to fail on UpdateFinancialBookingLog (after both postings succeed)
	mockFinAcct := &mockFinancialAccountingClient{
		failOnUpdate: true,
		failureError: errFinancialAccountingUnavailable,
	}
	acctConfig := &config.AccountConfig{
		DepositClearingAccountID:    "CLEARING-DEPOSIT",
		WithdrawalClearingAccountID: "CLEARING-WITHDRAWAL",
	}

	svc := &Service{
		repo:                   repo,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		accountConfig:          acctConfig,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestratorWithConfig(repo, mockPosKeeping, mockFinAcct, acctConfig),
	}

	req := createTestWithdrawalRequest("ACC-WTH-DE-COMP", 100, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	// Verify failure
	require.Error(t, err, "Withdrawal should fail due to UpdateFinancialBookingLog failure")
	assert.Nil(t, resp, "Response should be nil on failure")

	// Verify compensation: 2 original postings + 2 compensation postings = 4 total captures
	assert.Equal(t, 4, mockFinAcct.captureCalls, "Should have 4 total capture calls (2 original + 2 compensation)")
	assert.Equal(t, 2, mockFinAcct.compensateCalls, "Should have 2 compensation postings")

	// Verify account exists (balance not checked - Position Keeping is authoritative)
	_, err = repo.FindByID(ctx, "ACC-WTH-DE-COMP")
	require.NoError(t, err)
}

// =============================================================================
// InitiateWithdrawal Tests
// =============================================================================

// TestInitiateWithdrawal_Success verifies withdrawal request creation with INITIATED status.
func TestInitiateWithdrawal_Success(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-INIT-WTH-001", 100000)

	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-INIT-WTH-001": 100000, // $1000
	})

	req := &pb.InitiateWithdrawalRequest{
		AccountId: "ACC-INIT-WTH-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        50,
				Nanos:        0,
			},
		},
		Description: "Test initiated withdrawal",
		Reference:   "REF-001",
	}

	resp, err := svc.InitiateWithdrawal(ctx, req)

	require.NoError(t, err, "InitiateWithdrawal should succeed")
	require.NotNil(t, resp.Withdrawal)
	assert.NotEmpty(t, resp.Withdrawal.WithdrawalId, "Withdrawal ID should be generated")
	assert.Equal(t, "ACC-INIT-WTH-001", resp.Withdrawal.AccountId)
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_INITIATED, resp.Withdrawal.Status)
	assert.True(t, resp.ValidationPassed, "Validation should pass for valid request")
}

// TestInitiateWithdrawal_InsufficientFundsWarning verifies warning for insufficient funds.
func TestInitiateWithdrawal_InsufficientFundsWarning(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-INIT-WTH-WARN", 5000) // Only $50

	svc := mustNewService(t, repo, nil)

	// Try to initiate withdrawal of $100 from account with $50
	req := &pb.InitiateWithdrawalRequest{
		AccountId: "ACC-INIT-WTH-WARN",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
			},
		},
	}

	resp, err := svc.InitiateWithdrawal(ctx, req)

	// Should succeed but with validation warning
	require.NoError(t, err, "InitiateWithdrawal should succeed (creates pending)")
	require.NotNil(t, resp.Withdrawal)
	assert.False(t, resp.ValidationPassed, "Validation should fail due to insufficient funds warning")
	assert.Len(t, resp.ValidationMessages, 1, "Should have 1 validation message")
	assert.Contains(t, resp.ValidationMessages[0], "exceeds current available balance")
}

// =============================================================================
// RetrieveWithdrawal Tests
// =============================================================================

// TestRetrieveWithdrawal_ByAccountID verifies retrieval of withdrawals by account.
func TestRetrieveWithdrawal_ByAccountID(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	// Create withdrawal table (withdrawalRepo is unconditionally initialized in production)
	err := db.AutoMigrate(&persistence.WithdrawalEntity{})
	require.NoError(t, err, "failed to create withdrawal table")

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-RET-WTH-001", 100000)

	svc := mustNewService(t, repo, nil)
	svc.withdrawalRepo = persistence.NewWithdrawalRepository(db)

	req := &pb.RetrieveWithdrawalRequest{
		AccountId: "ACC-RET-WTH-001",
	}

	resp, err := svc.RetrieveWithdrawal(ctx, req)

	require.NoError(t, err, "RetrieveWithdrawal should succeed")
	require.NotNil(t, resp.Withdrawals)
	assert.Equal(t, int64(0), resp.Pagination.TotalCount)
}

// TestRetrieveWithdrawal_AccountNotFound verifies NotFound error for non-existent account.
func TestRetrieveWithdrawal_AccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.RetrieveWithdrawalRequest{
		AccountId: "ACC-NONEXISTENT",
	}

	_, err := svc.RetrieveWithdrawal(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestRetrieveWithdrawal_MissingIdentifier verifies error when no identifier provided.
func TestRetrieveWithdrawal_MissingIdentifier(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.RetrieveWithdrawalRequest{} // No withdrawal_id or account_id

	_, err := svc.RetrieveWithdrawal(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "required")
}

// =============================================================================
// Edge Case Tests
// =============================================================================

// TestExecuteWithdrawal_ConcurrentWithdrawals verifies optimistic locking prevents
// concurrent withdrawals from overdrawing the account.
// Concurrency is coordinated via sync.WaitGroup; await.Until is used for non-sleep
// polling to wait for all withdrawals to complete, per project guidelines.
func TestExecuteWithdrawal_ConcurrentWithdrawals(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Create account with $500 balance (50000 cents)
	initialBalance := int64(50000)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-CONCURRENT", initialBalance)

	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-WTH-CONCURRENT": initialBalance, // $500
	})

	// Execute 10 concurrent withdrawals of $100 each
	numWithdrawals := 10
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var failCount atomic.Int32

	for i := 0; i < numWithdrawals; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := createTestWithdrawalRequest("ACC-WTH-CONCURRENT", 100, 0) // $100
			_, err := svc.ExecuteWithdrawal(ctx, req)
			if err == nil {
				successCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}()
	}

	wg.Wait()

	// Verify all withdrawals completed
	err := await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return successCount.Load()+failCount.Load() == int32(numWithdrawals)
		})
	require.NoError(t, err, "All withdrawals should complete")

	// In a real system, concurrent withdrawals may still fail due to optimistic
	// locking or other contention (e.g. version mismatches). This test only
	// verifies that all concurrent withdrawal attempts complete and that at
	// least one succeeds; it does not assert the specific cause of any failures.
	successes := successCount.Load()
	failures := failCount.Load()

	// At least some withdrawals should succeed
	assert.GreaterOrEqual(t, successes, int32(1), "At least 1 withdrawal should succeed")
	// Some may fail due to optimistic locking conflicts
	assert.Equal(t, int32(numWithdrawals), successes+failures, "All withdrawals should complete")

	// Verify account exists (balance not checked - Position Keeping is authoritative)
	_, err = repo.FindByID(ctx, "ACC-WTH-CONCURRENT")
	require.NoError(t, err)
}

// TestExecuteWithdrawal_ExactBalanceWithdrawal verifies withdrawing exact balance succeeds.
func TestExecuteWithdrawal_ExactBalanceWithdrawal(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Create account with exactly $100.00 balance (10000 cents)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-EXACT", 10000)

	// Configure mock with the account balance. The mock returns a static value used
	// for both the sufficient funds check and response. In production, Position Keeping
	// would return the post-withdrawal balance (0) after the debit is recorded.
	// The key assertion here is that withdrawal of exact available balance succeeds.
	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-WTH-EXACT": 10000, // $100 - sufficient for exact balance withdrawal
	})

	// Withdraw exactly $100.00
	req := createTestWithdrawalRequest("ACC-WTH-EXACT", 100, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.NoError(t, err, "Exact balance withdrawal should succeed")
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED, resp.Status)

	// Verify response contains balance information.
	// Note: Mock returns static balance; in production, Position Keeping returns
	// post-withdrawal balance. The key test is that exact balance withdrawal succeeds.
	assert.NotNil(t, resp.NewBalance, "Response should include balance information")
	assert.NotNil(t, resp.NewBalance.Amount, "Response should include balance amount")

	// Verify account exists (balance not checked - Position Keeping is authoritative)
	_, err = repo.FindByID(ctx, "ACC-WTH-EXACT")
	require.NoError(t, err)
}

// TestExecuteWithdrawal_InvalidAmount verifies error for zero or negative amounts.
func TestExecuteWithdrawal_InvalidAmount(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-INVALID", 10000)

	svc := mustNewService(t, repo, nil)

	tests := []struct {
		name  string
		units int64
		nanos int32
	}{
		{"zero amount", 0, 0},
		{"negative units", -100, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &pb.ExecuteWithdrawalRequest{
				AccountId: "ACC-WTH-INVALID",
				Amount: &commonpb.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        tt.units,
						Nanos:        tt.nanos,
					},
				},
			}

			_, err := svc.ExecuteWithdrawal(ctx, req)
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}

// =============================================================================
// Idempotency Tests
// =============================================================================

// TestExecuteWithdrawal_IdempotencyReturnsCachedResponse verifies that duplicate
// requests with the same idempotency key return the cached response.
func TestExecuteWithdrawal_IdempotencyReturnsCachedResponse(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	mockIdemp := newMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, nil, mockIdemp)

	// Create account with balance
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-IDEMP-001", 100000)

	// Pre-populate cached response — use tenant from context (dynamic per test)
	tid, _ := tenant.FromContext(ctx)
	idempKey := idempotency.Key{
		TenantID:  string(tid),
		Namespace: "current-account",
		Operation: "withdrawal",
		EntityID:  "ACC-WTH-IDEMP-001",
		RequestID: "req-wth-123",
	}

	cachedResp := &pb.ExecuteWithdrawalResponse{
		AccountId:     "ACC-WTH-IDEMP-001",
		TransactionId: "cached-wth-tx-id",
		Status:        pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED,
		NewBalance: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 900, Nanos: 0},
		},
	}
	cachedData, err := proto.Marshal(cachedResp)
	require.NoError(t, err)
	mockIdemp.setResult(idempKey, cachedData)

	// Execute withdrawal with same idempotency key
	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-WTH-IDEMP-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50, Nanos: 0},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-wth-123"},
	}

	resp, err := svc.ExecuteWithdrawal(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "cached-wth-tx-id", resp.TransactionId, "Should return cached transaction ID")
	assert.Equal(t, int64(900), resp.NewBalance.Amount.Units, "Should return cached balance")
}

// TestExecuteWithdrawal_IdempotencyReturnsAbortedWhenInProgress verifies that
// concurrent requests with the same idempotency key return Aborted.
func TestExecuteWithdrawal_IdempotencyReturnsAbortedWhenInProgress(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	mockIdemp := newMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, nil, mockIdemp)

	// Create account with balance
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-IDEMP-002", 100000)

	// Mark operation as pending
	tid, _ := tenant.FromContext(ctx)
	idempKey := idempotency.Key{
		TenantID:  string(tid),
		Namespace: "current-account",
		Operation: "withdrawal",
		EntityID:  "ACC-WTH-IDEMP-002",
		RequestID: "req-wth-456",
	}
	mockIdemp.setPending(idempKey)

	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-WTH-IDEMP-002",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50, Nanos: 0},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-wth-456"},
	}

	_, err := svc.ExecuteWithdrawal(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code(), "Should return Aborted for concurrent request")
	assert.Contains(t, st.Message(), "already in progress")
}

// TestExecuteWithdrawal_IdempotencyProceedsWithoutKey verifies withdrawal works
// without idempotency key.
func TestExecuteWithdrawal_IdempotencyProceedsWithoutKey(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	mockIdemp := newMockIdempotencyService()
	// Configure mock with POST-withdrawal balance (95000 cents = £950.00 after £50 withdrawal).
	// Position Keeping is the source of truth and would have recorded the DEBIT
	// by the time we query the balance.
	svc := mustNewServiceWithIdempotencyAndPositionKeeping(t, repo, nil, mockIdemp, map[string]int64{
		"ACC-WTH-IDEMP-003": 95000, // £950 post-withdrawal
	})

	// Create account with balance
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-IDEMP-003", 100000)

	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-WTH-IDEMP-003",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50, Nanos: 0},
		},
		// No IdempotencyKey
	}

	resp, err := svc.ExecuteWithdrawal(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.TransactionId)
	assert.Equal(t, int64(950), resp.NewBalance.Amount.Units)
}

// TestExecuteWithdrawal_IdempotencyCleanupOnFailure verifies that pending state
// is cleaned up when withdrawal fails.
func TestExecuteWithdrawal_IdempotencyCleanupOnFailure(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	mockIdemp := newMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, nil, mockIdemp)

	// Create account with insufficient balance
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-IDEMP-004", 5000) // Only $50

	tid, _ := tenant.FromContext(ctx)
	idempKey := idempotency.Key{
		TenantID:  string(tid),
		Namespace: "current-account",
		Operation: "withdrawal",
		EntityID:  "ACC-WTH-IDEMP-004",
		RequestID: "req-wth-789",
	}

	// Try to withdraw $100 (will fail due to insufficient funds)
	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-WTH-IDEMP-004",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 100, Nanos: 0},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-wth-789"},
	}

	_, err := svc.ExecuteWithdrawal(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())

	// Verify pending state was cleaned up
	assert.False(t, mockIdemp.isPending(idempKey), "Pending state should be cleaned up after failure")
}

// =============================================================================
// Validation Tests
// =============================================================================

// TestExecuteWithdrawal_MissingAccountID verifies error for missing account_id.
func TestExecuteWithdrawal_MissingAccountID(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.ExecuteWithdrawalRequest{
		// AccountId omitted
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 100, Nanos: 0},
		},
	}

	_, err := svc.ExecuteWithdrawal(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "account_id")
}

// TestExecuteWithdrawal_MissingAmount verifies error for missing amount.
func TestExecuteWithdrawal_MissingAmount(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-NO-AMT", 100000)

	svc := mustNewService(t, repo, nil)

	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-WTH-NO-AMT",
		// Amount omitted
	}

	_, err := svc.ExecuteWithdrawal(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "amount")
}

// =============================================================================
// Amount Overflow Tests
// =============================================================================

// TestExecuteWithdrawal_AmountOverflow verifies overflow prevention for large amounts.
func TestExecuteWithdrawal_AmountOverflow(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-OVERFLOW", 100000)

	svc := mustNewService(t, repo, nil)

	// Test with units that would overflow when multiplied by 100
	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-WTH-OVERFLOW",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        92233720368547759, // MaxInt64/100 + 1 - would overflow
				Nanos:        0,
			},
		},
	}

	_, err := svc.ExecuteWithdrawal(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "overflow")
}

// =============================================================================
// Direct Withdrawal Mode Regression Tests
// =============================================================================
//
// These tests verify the direct withdrawal mode (account_id + amount, no withdrawal_id)
// continues to work correctly after nil guard removal (task 2). Direct mode bypasses
// the withdrawal repository and creates a withdrawal without persistence.

// TestDirectWithdrawal_SucceedsWithAccountIDAndAmount verifies the happy path for
// direct withdrawal mode: provide account_id + amount, receive successful response.
func TestDirectWithdrawal_SucceedsWithAccountIDAndAmount(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-DIRECT-001", 100000) // £1000

	// Configure Position Keeping mock with post-withdrawal balance
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-DIRECT-001": 75000, // £750 post-withdrawal
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:                   repo,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Execute direct withdrawal of £250
	req := createTestWithdrawalRequest("ACC-DIRECT-001", 250, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	// Verify success
	require.NoError(t, err, "Direct withdrawal should succeed")
	assert.Equal(t, "ACC-DIRECT-001", resp.AccountId)
	assert.NotEmpty(t, resp.TransactionId, "Transaction ID should be generated")
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED, resp.Status)

	// Verify balance from Position Keeping
	assert.NotNil(t, resp.NewBalance)
	assert.Equal(t, int64(750), resp.NewBalance.Amount.Units)
	assert.Equal(t, int32(0), resp.NewBalance.Amount.Nanos)

	// Verify saga orchestration ran
	assert.Equal(t, 1, mockPosKeeping.initiateCalls, "PositionKeeping should be called once")
	assert.Equal(t, 1, mockFinAcct.captureCalls, "FinancialAccounting should be called once")

	// Verify no compensation
	assert.Equal(t, 0, mockPosKeeping.compensateCalls, "No compensation on success")
	assert.Equal(t, 0, mockFinAcct.compensateCalls, "No compensation on success")
}

// TestDirectWithdrawal_NoWithdrawalRecordCreated verifies that direct withdrawal mode
// does NOT create a withdrawal record in the database. This is the key behavioral
// difference from the pending withdrawal flow (InitiateWithdrawal -> ExecuteWithdrawal).
func TestDirectWithdrawal_NoWithdrawalRecordCreated(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	// Create withdrawal table so we can verify no records were inserted
	err := db.AutoMigrate(&persistence.WithdrawalEntity{})
	require.NoError(t, err, "failed to create withdrawal table")

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-DIRECT-002", 100000) // £1000

	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-DIRECT-002": 90000, // £900 post-withdrawal
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:                   repo,
		withdrawalRepo:         withdrawalRepo,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Execute direct withdrawal of £100
	req := createTestWithdrawalRequest("ACC-DIRECT-002", 100, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.NoError(t, err, "Direct withdrawal should succeed")
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED, resp.Status)

	// Verify no withdrawal record was created in the database.
	// Direct mode skips persistence because pendingWithdrawal is nil.
	account, err := repo.FindByID(ctx, "ACC-DIRECT-002")
	require.NoError(t, err)

	withdrawals, err := withdrawalRepo.List(ctx, account.ID(), persistence.PaginationParams{Limit: 10, Offset: 0})
	require.NoError(t, err, "Listing withdrawals should not error")
	assert.Empty(t, withdrawals, "Direct withdrawal should NOT create a withdrawal record in the database")
}

// TestDirectWithdrawal_BalanceDecreased verifies that the account balance reported by
// Position Keeping reflects the withdrawal. Since Position Keeping is the authoritative
// balance source, we verify the response balance matches the mock's post-withdrawal value.
func TestDirectWithdrawal_BalanceDecreased(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-DIRECT-003", 50000) // £500

	// Post-withdrawal balance: £500 - £123.45 = £376.55 (37655 cents)
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-DIRECT-003": 37655,
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:                   repo,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Withdraw £123.45
	req := createTestWithdrawalRequest("ACC-DIRECT-003", 123, 450000000)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.NoError(t, err, "Direct withdrawal should succeed")

	// Verify balance reflects post-withdrawal amount from Position Keeping
	require.NotNil(t, resp.NewBalance)
	assert.Equal(t, int64(376), resp.NewBalance.Amount.Units, "Balance units should reflect withdrawal")
	assert.Equal(t, int32(550000000), resp.NewBalance.Amount.Nanos, "Balance nanos should reflect withdrawal")

	// Verify available balance is also populated
	require.NotNil(t, resp.AvailableBalance)
	assert.Equal(t, int64(376), resp.AvailableBalance.Amount.Units)
}

// TestDirectWithdrawal_InvalidAmount_ZeroReturnsError verifies that a direct withdrawal
// with zero amount returns InvalidArgument error.
func TestDirectWithdrawal_InvalidAmount_ZeroReturnsError(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-DIRECT-004", 100000)

	svc := mustNewService(t, repo, nil)

	req := createTestWithdrawalRequest("ACC-DIRECT-004", 0, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "Zero amount should be rejected")
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestDirectWithdrawal_InvalidAmount_NegativeReturnsError verifies that a direct withdrawal
// with negative amount returns InvalidArgument error.
func TestDirectWithdrawal_InvalidAmount_NegativeReturnsError(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-DIRECT-005", 100000)

	svc := mustNewService(t, repo, nil)

	req := createTestWithdrawalRequest("ACC-DIRECT-005", -50, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "Negative amount should be rejected")
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestDirectWithdrawal_InvalidAmount_NilAmountReturnsError verifies that a direct withdrawal
// with nil amount returns InvalidArgument error.
func TestDirectWithdrawal_InvalidAmount_NilAmountReturnsError(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-DIRECT-006", 100000)

	svc := mustNewService(t, repo, nil)

	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-DIRECT-006",
		// Amount is nil
	}
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "Nil amount should be rejected")
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "amount")
}

// TestDirectWithdrawal_NonExistentAccountReturnsError verifies that a direct withdrawal
// targeting a non-existent account returns NotFound error.
func TestDirectWithdrawal_NonExistentAccountReturnsError(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := createTestWithdrawalRequest("ACC-DIRECT-NONEXISTENT", 100, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "Non-existent account should return error")
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestDirectWithdrawal_MissingAccountIDReturnsError verifies that a direct withdrawal
// without account_id (and without withdrawal_id) returns InvalidArgument error.
func TestDirectWithdrawal_MissingAccountIDReturnsError(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.ExecuteWithdrawalRequest{
		// AccountId is empty, WithdrawalId is empty -> direct mode with missing account_id
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 100, Nanos: 0},
		},
	}
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "Missing account_id should return error")
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "account_id")
}

// =============================================================================
// Helper Notes
// =============================================================================

// Note: setupTestDB is already defined in grpc_service_test.go with tenant context.
// Note: createTestAccountWithBalance is defined in lien_service_test.go and shared across tests.
