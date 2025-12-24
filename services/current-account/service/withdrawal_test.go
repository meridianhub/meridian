package service

import (
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
	"github.com/meridianhub/meridian/shared/platform/await"
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
func testWithdrawalOrchestrator(repo *persistence.Repository, posKeeping *mockPositionKeepingClient, finAcct *mockFinancialAccountingClient) *WithdrawalOrchestrator {
	return testWithdrawalOrchestratorWithConfig(repo, posKeeping, finAcct, nil)
}

// testWithdrawalOrchestratorWithConfig creates a WithdrawalOrchestrator with optional AccountConfig.
func testWithdrawalOrchestratorWithConfig(repo *persistence.Repository, posKeeping *mockPositionKeepingClient, finAcct *mockFinancialAccountingClient, acctConfig *config.AccountConfig) *WithdrawalOrchestrator {
	return NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:           testLogger(),
		Repo:             repo,
		PosKeepingClient: posKeeping,
		FinAcctClient:    finAcct,
		AccountConfig:    acctConfig,
	})
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

	// Create mock clients
	mockPosKeeping := &mockPositionKeepingClient{}
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
	assert.NotNil(t, resp.NewBalance)
	assert.Equal(t, int64(899), resp.NewBalance.Amount.Units)
	assert.Equal(t, int32(500000000), resp.NewBalance.Amount.Nanos)

	// Verify account persisted correctly
	updatedAccount, err := repo.FindByID(ctx, "ACC-WTH-001")
	require.NoError(t, err)
	assert.Equal(t, int64(89950), balanceCents(updatedAccount.Balance()), "Balance should be 89950 cents after withdrawal")

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
	account, err := domain.NewCurrentAccount("ACC-WTH-FROZEN", "ACC-WTH-FROZEN", uuid.New().String(), "GBP")
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
	// Create account, deposit funds, then close it
	account, err := domain.NewCurrentAccount("ACC-WTH-CLOSED", "ACC-WTH-CLOSED", uuid.New().String(), "GBP")
	require.NoError(t, err)

	depositAmount, err := domain.NewMoney("GBP", 100000) // $1000
	require.NoError(t, err)
	account, err = account.Deposit(depositAmount)
	require.NoError(t, err)

	account, err = account.Close()
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
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-PK-FAIL", 100000)
	originalBalance := balanceCents(account.Balance())

	// Configure PositionKeeping to fail on initiate
	mockPosKeeping := &mockPositionKeepingClient{
		failOnInitiate: true,
		initiateError:  errPositionKeepingUnavailable,
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
	assert.Contains(t, st.Message(), "log_position")

	// Verify account state unchanged
	updatedAccount, err := repo.FindByID(ctx, "ACC-WTH-PK-FAIL")
	require.NoError(t, err)
	assert.Equal(t, originalBalance, balanceCents(updatedAccount.Balance()),
		"Account balance should remain unchanged when saga fails")

	// Verify no FinancialAccounting calls were made
	assert.Equal(t, 0, mockFinAcct.captureCalls, "FinancialAccounting should not be called")
}

// TestExecuteWithdrawal_WithOrchestration_FinancialAccountingFailure verifies saga compensation
// when FinancialAccounting service fails after PositionKeeping succeeds.
func TestExecuteWithdrawal_WithOrchestration_FinancialAccountingFailure(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-FA-FAIL", 100000)
	originalBalance := balanceCents(account.Balance())

	mockPosKeeping := &mockPositionKeepingClient{}
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
	assert.Contains(t, st.Message(), "post_ledger")
	assert.Contains(t, st.Message(), "compensated")

	// Verify account state unchanged
	updatedAccount, err := repo.FindByID(ctx, "ACC-WTH-FA-FAIL")
	require.NoError(t, err)
	assert.Equal(t, originalBalance, balanceCents(updatedAccount.Balance()),
		"Account balance should remain unchanged after compensation")

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

	mockPosKeeping := &mockPositionKeepingClient{}
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
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-DE-COMP", 100000)
	originalBalance := balanceCents(account.Balance())

	mockPosKeeping := &mockPositionKeepingClient{}
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

	// Verify account balance unchanged
	updatedAccount, err := repo.FindByID(ctx, "ACC-WTH-DE-COMP")
	require.NoError(t, err)
	assert.Equal(t, originalBalance, balanceCents(updatedAccount.Balance()),
		"Account balance should remain unchanged after compensation")
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

	svc := mustNewService(t, repo, nil)

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

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-RET-WTH-001", 100000)

	svc := mustNewService(t, repo, nil)

	req := &pb.RetrieveWithdrawalRequest{
		AccountId: "ACC-RET-WTH-001",
	}

	resp, err := svc.RetrieveWithdrawal(ctx, req)

	require.NoError(t, err, "RetrieveWithdrawal should succeed")
	require.NotNil(t, resp.Withdrawals)
	// Currently returns empty list as persistence is not implemented
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
// Uses await.Until instead of time.Sleep per project guidelines.
func TestExecuteWithdrawal_ConcurrentWithdrawals(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Create account with $500 balance (50000 cents)
	initialBalance := int64(50000)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-CONCURRENT", initialBalance)

	svc := mustNewService(t, repo, nil)

	// Execute 10 concurrent withdrawals of $100 each
	numWithdrawals := 10
	withdrawalAmountCents := int64(10000) // $100 = 10000 cents each
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

	// Key invariants with optimistic locking:
	// 1. Some withdrawals succeed, some fail (not all succeed which would overdraft)
	// 2. Final balance is non-negative (no overdraft)
	// 3. Final balance is consistent: initial - (successes * amount)

	successes := successCount.Load()
	failures := failCount.Load()

	// Must have at least 1 success and at least some failures (can't withdraw all $1000 from $500)
	assert.GreaterOrEqual(t, successes, int32(1), "At least 1 withdrawal should succeed")
	assert.GreaterOrEqual(t, failures, int32(1), "At least 1 withdrawal should fail (insufficient funds)")
	assert.Equal(t, int32(numWithdrawals), successes+failures, "All withdrawals should complete")

	// Verify no overdraft - final balance >= 0
	finalAccount, err := repo.FindByID(ctx, "ACC-WTH-CONCURRENT")
	require.NoError(t, err)
	finalBalanceCents := balanceCents(finalAccount.Balance())
	assert.GreaterOrEqual(t, finalBalanceCents, int64(0), "Final balance should be non-negative (no overdraft)")

	// Verify balance consistency: balance = initial - (successes * withdrawal_amount)
	expectedBalance := initialBalance - (int64(successes) * withdrawalAmountCents)
	assert.Equal(t, expectedBalance, finalBalanceCents, "Final balance should equal initial - (successes * amount)")
}

// TestExecuteWithdrawal_ExactBalanceWithdrawal verifies withdrawing exact balance succeeds.
func TestExecuteWithdrawal_ExactBalanceWithdrawal(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Create account with exactly $100.00 balance (10000 cents)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-EXACT", 10000)

	svc := mustNewService(t, repo, nil)

	// Withdraw exactly $100.00
	req := createTestWithdrawalRequest("ACC-WTH-EXACT", 100, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.NoError(t, err, "Exact balance withdrawal should succeed")
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED, resp.Status)

	// Verify balance is exactly $0.00
	assert.Equal(t, int64(0), resp.NewBalance.Amount.Units)
	assert.Equal(t, int32(0), resp.NewBalance.Amount.Nanos)

	// Verify in database
	updatedAccount, err := repo.FindByID(ctx, "ACC-WTH-EXACT")
	require.NoError(t, err)
	assert.Equal(t, int64(0), balanceCents(updatedAccount.Balance()), "Final balance should be $0")
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

	// Pre-populate cached response
	idempKey := idempotency.Key{
		TenantID:  svcTestTenantID,
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
	idempKey := idempotency.Key{
		TenantID:  svcTestTenantID,
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
	svc := mustNewServiceWithIdempotency(t, repo, nil, mockIdemp)

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

	idempKey := idempotency.Key{
		TenantID:  svcTestTenantID,
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
// Backward Compatibility Tests
// =============================================================================

// TestExecuteWithdrawal_WithoutClients_BackwardCompatibility verifies that
// withdrawal works without external clients (backward compatibility mode).
func TestExecuteWithdrawal_WithoutClients_BackwardCompatibility(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-WTH-COMPAT", 100000)

	// Create service WITHOUT clients (backward compatibility mode)
	svc := mustNewService(t, repo, nil)

	req := createTestWithdrawalRequest("ACC-WTH-COMPAT", 200, 0)
	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.NoError(t, err)
	assert.Equal(t, "ACC-WTH-COMPAT", resp.AccountId)
	assert.NotEmpty(t, resp.TransactionId)
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED, resp.Status)

	// Verify balance update: $1000 - $200 = $800
	assert.Equal(t, int64(800), resp.NewBalance.Amount.Units)

	// Verify in database
	updatedAccount, err := repo.FindByID(ctx, "ACC-WTH-COMPAT")
	require.NoError(t, err)
	assert.Equal(t, int64(80000), balanceCents(updatedAccount.Balance()))
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
// Helper Notes
// =============================================================================

// Note: setupTestDB is already defined in grpc_service_test.go with tenant context.
// Note: createTestAccountWithBalance is defined in lien_service_test.go and shared across tests.
