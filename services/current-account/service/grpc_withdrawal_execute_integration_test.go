package service

import (
	"testing"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// =============================================================================
// ExecuteWithdrawal with withdrawal_id Integration Tests
//
// These tests verify the two-phase withdrawal pattern:
// 1. InitiateWithdrawal creates a PENDING withdrawal
// 2. ExecuteWithdrawal with withdrawal_id executes it
// =============================================================================

// TestExecuteWithdrawal_WithdrawalID_Success verifies the complete two-phase withdrawal flow:
// 1. InitiateWithdrawal creates a PENDING withdrawal
// 2. ExecuteWithdrawal with withdrawal_id executes the pending withdrawal
// 3. Withdrawal transitions to COMPLETED status
// 4. Account balance is decreased (via Position Keeping)
func TestExecuteWithdrawal_WithdrawalID_Success(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	// Create withdrawal and event_outbox tables
	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}), "failed to create withdrawal table")
	require.NoError(t, db.AutoMigrate(&events.EventOutbox{}), "failed to create event_outbox table")

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create account with $1000 balance (100000 cents)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-EW-WID-001", 100000)

	// Configure Position Keeping mock:
	// - Pre-withdrawal balance: $1000 (100000 cents) - used during InitiateWithdrawal validation
	// - Post-withdrawal balance: $950 (95000 cents) - returned after ExecuteWithdrawal
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-EW-WID-001": 100000,
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	// Create service with withdrawal repository and outbox for the full withdrawal_id flow
	svc := &Service{
		repo:                   repo,
		withdrawalRepo:         withdrawalRepo,
		outboxRepo:             outboxRepo,
		db:                     db,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Phase 1: Initiate withdrawal (creates PENDING withdrawal)
	initiateReq := &pb.InitiateWithdrawalRequest{
		AccountId: "ACC-EW-WID-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        50,
				Nanos:        0,
			},
		},
		Description: "Test two-phase withdrawal",
		Reference:   "WTH-TEST-001",
	}

	initiateResp, err := svc.InitiateWithdrawal(ctx, initiateReq)
	require.NoError(t, err, "InitiateWithdrawal should succeed")
	require.NotNil(t, initiateResp.Withdrawal)
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_INITIATED, initiateResp.Withdrawal.Status,
		"Initiated withdrawal should have INITIATED status")
	assert.True(t, initiateResp.ValidationPassed, "Validation should pass with sufficient balance")

	withdrawalID := initiateResp.Withdrawal.WithdrawalId

	// Update mock to return post-withdrawal balance
	mockPosKeeping.mu.Lock()
	mockPosKeeping.accountBalances["ACC-EW-WID-001"] = 95000 // $950 after $50 withdrawal
	mockPosKeeping.mu.Unlock()

	// Phase 2: Execute withdrawal using withdrawal_id
	executeReq := &pb.ExecuteWithdrawalRequest{
		WithdrawalId: withdrawalID,
	}

	executeResp, err := svc.ExecuteWithdrawal(ctx, executeReq)

	// Verify successful execution
	require.NoError(t, err, "ExecuteWithdrawal with withdrawal_id should succeed")
	assert.Equal(t, "ACC-EW-WID-001", executeResp.AccountId)
	assert.NotEmpty(t, executeResp.TransactionId, "Transaction ID should be generated")
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED, executeResp.Status,
		"Executed withdrawal should have COMPLETED status")

	// Verify balance decreased (Position Keeping returns post-withdrawal balance)
	assert.NotNil(t, executeResp.NewBalance)
	assert.Equal(t, int64(950), executeResp.NewBalance.Amount.Units,
		"New balance should reflect the withdrawal")

	// Verify withdrawal status in database transitioned to COMPLETED
	withdrawal, err := withdrawalRepo.FindByReference(ctx, withdrawalID)
	require.NoError(t, err, "Should find withdrawal by reference")
	assert.Equal(t, domain.WithdrawalStatusCompleted, withdrawal.Status,
		"Withdrawal in database should be COMPLETED")

	// Verify saga services were called
	assert.Equal(t, 1, mockPosKeeping.initiateCalls,
		"PositionKeeping should be called once for the withdrawal execution")
	assert.Equal(t, 1, mockFinAcct.captureCalls,
		"FinancialAccounting should be called once for the withdrawal execution")
}

// TestExecuteWithdrawal_WithdrawalID_NotFound verifies NotFound error when withdrawal_id
// does not exist in the database.
func TestExecuteWithdrawal_WithdrawalID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}), "failed to create withdrawal table")

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := &Service{
		repo:             repo,
		withdrawalRepo:   withdrawalRepo,
		posKeepingClient: &mockPositionKeepingClient{},
		logger:           testLogger(),
	}

	req := &pb.ExecuteWithdrawalRequest{
		WithdrawalId: "WTH-NONEXISTENT",
	}

	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err, "ExecuteWithdrawal should fail for non-existent withdrawal")
	assert.Nil(t, resp, "Response should be nil on failure")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "withdrawal not found")
}

// TestExecuteWithdrawal_WithdrawalID_AlreadyCompleted verifies FailedPrecondition error
// when attempting to execute a withdrawal that is already in COMPLETED status.
func TestExecuteWithdrawal_WithdrawalID_AlreadyCompleted(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}), "failed to create withdrawal table")
	require.NoError(t, db.AutoMigrate(&events.EventOutbox{}), "failed to create event_outbox table")

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create account with balance
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-EW-WID-COMP", 100000)

	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-EW-WID-COMP": 100000,
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:                   repo,
		withdrawalRepo:         withdrawalRepo,
		outboxRepo:             outboxRepo,
		db:                     db,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Phase 1: Initiate
	initiateReq := &pb.InitiateWithdrawalRequest{
		AccountId: "ACC-EW-WID-COMP",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        50,
				Nanos:        0,
			},
		},
		Reference: "WTH-ALREADY-COMP",
	}

	initiateResp, err := svc.InitiateWithdrawal(ctx, initiateReq)
	require.NoError(t, err, "InitiateWithdrawal should succeed")
	withdrawalID := initiateResp.Withdrawal.WithdrawalId

	// Phase 2: Execute successfully (first time)
	mockPosKeeping.mu.Lock()
	mockPosKeeping.accountBalances["ACC-EW-WID-COMP"] = 95000
	mockPosKeeping.mu.Unlock()

	executeReq := &pb.ExecuteWithdrawalRequest{
		WithdrawalId: withdrawalID,
	}

	_, err = svc.ExecuteWithdrawal(ctx, executeReq)
	require.NoError(t, err, "First execution should succeed")

	// Phase 3: Attempt to execute again (should fail - withdrawal already completed)
	resp, err := svc.ExecuteWithdrawal(ctx, executeReq)

	require.Error(t, err, "Second execution should fail")
	assert.Nil(t, resp, "Response should be nil on failure")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "not pending")
}

// TestExecuteWithdrawal_WithdrawalID_SagaFailure verifies that when the withdrawal saga
// fails (e.g., PositionKeeping unavailable), the pending withdrawal remains in PENDING status
// and is not marked as completed.
func TestExecuteWithdrawal_WithdrawalID_SagaFailure(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}), "failed to create withdrawal table")

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	// Create account with balance
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-EW-WID-SFAIL", 100000)

	// Configure PositionKeeping to succeed for balance queries but fail on initiate
	mockPosKeeping := &mockPositionKeepingClient{
		failOnInitiate: true,
		initiateError:  errPositionKeepingUnavailable,
		accountBalances: map[string]int64{
			"ACC-EW-WID-SFAIL": 100000,
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

	// Phase 1: Initiate withdrawal
	initiateReq := &pb.InitiateWithdrawalRequest{
		AccountId: "ACC-EW-WID-SFAIL",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        50,
				Nanos:        0,
			},
		},
		Reference: "WTH-SAGA-FAIL",
	}

	initiateResp, err := svc.InitiateWithdrawal(ctx, initiateReq)
	require.NoError(t, err, "InitiateWithdrawal should succeed")
	withdrawalID := initiateResp.Withdrawal.WithdrawalId

	// Phase 2: Execute (saga will fail due to PositionKeeping failure)
	executeReq := &pb.ExecuteWithdrawalRequest{
		WithdrawalId: withdrawalID,
	}

	resp, err := svc.ExecuteWithdrawal(ctx, executeReq)
	require.Error(t, err, "ExecuteWithdrawal should fail due to saga failure")
	assert.Nil(t, resp, "Response should be nil on failure")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())

	// Verify withdrawal remains in PENDING status (not marked completed)
	withdrawal, err := withdrawalRepo.FindByReference(ctx, withdrawalID)
	require.NoError(t, err, "Should find withdrawal by reference")
	assert.Equal(t, domain.WithdrawalStatusPending, withdrawal.Status,
		"Withdrawal should remain PENDING after saga failure")
}

// TestExecuteWithdrawal_WithdrawalID_BalanceDecreased verifies that the post-withdrawal
// balance in the response reflects the withdrawal amount deducted from Position Keeping.
func TestExecuteWithdrawal_WithdrawalID_BalanceDecreased(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}), "failed to create withdrawal table")
	require.NoError(t, db.AutoMigrate(&events.EventOutbox{}), "failed to create event_outbox table")

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create account with $500.75 balance (50075 cents)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-EW-WID-BAL", 50075)

	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-EW-WID-BAL": 50075, // $500.75
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:                   repo,
		withdrawalRepo:         withdrawalRepo,
		outboxRepo:             outboxRepo,
		db:                     db,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Initiate withdrawal of $200.25
	initiateReq := &pb.InitiateWithdrawalRequest{
		AccountId: "ACC-EW-WID-BAL",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        200,
				Nanos:        250000000,
			},
		},
		Reference: "WTH-BAL-CHECK",
	}

	initiateResp, err := svc.InitiateWithdrawal(ctx, initiateReq)
	require.NoError(t, err, "InitiateWithdrawal should succeed")
	withdrawalID := initiateResp.Withdrawal.WithdrawalId

	// Update mock to return post-withdrawal balance: $500.75 - $200.25 = $300.50 (30050 cents)
	mockPosKeeping.mu.Lock()
	mockPosKeeping.accountBalances["ACC-EW-WID-BAL"] = 30050
	mockPosKeeping.mu.Unlock()

	// Execute withdrawal
	executeReq := &pb.ExecuteWithdrawalRequest{
		WithdrawalId: withdrawalID,
	}

	resp, err := svc.ExecuteWithdrawal(ctx, executeReq)

	require.NoError(t, err, "ExecuteWithdrawal should succeed")
	assert.NotNil(t, resp.NewBalance, "Response should include new balance")
	assert.Equal(t, int64(300), resp.NewBalance.Amount.Units,
		"Balance units should reflect post-withdrawal amount")
	assert.Equal(t, int32(500000000), resp.NewBalance.Amount.Nanos,
		"Balance nanos should reflect post-withdrawal amount")

	// Verify available balance is also populated
	assert.NotNil(t, resp.AvailableBalance, "Response should include available balance")
}
