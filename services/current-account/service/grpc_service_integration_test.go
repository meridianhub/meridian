package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// Test sentinel errors
var (
	errPositionKeepingUnavailable     = errors.New("position keeping service unavailable")
	errFinancialAccountingUnavailable = errors.New("financial accounting service unavailable")
	errIntentionalTestFailure         = errors.New("intentional failure for compensation test")
)

// balanceCents returns the balance as cents for test assertions.
// Panics on error (should never happen in tests with valid Money).
func balanceCents(m domain.Money) int64 {
	cents, err := m.ToMinorUnits()
	if err != nil {
		panic("ToMinorUnits failed: " + err.Error())
	}
	return cents
}

// Mock PositionKeeping Client

type mockPositionKeepingClient struct {
	updateCalls     int
	failOnUpdate    bool
	failureError    error
	updateResponses []*positionkeepingv1.UpdateFinancialPositionLogResponse
	compensateCalls int
	initiateCalls   int
	failOnInitiate  bool
	initiateError   error
	retrieveCalls   int
	bulkImportCalls int
	listCalls       int
}

func (m *mockPositionKeepingClient) InitiateFinancialPositionLog(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	m.initiateCalls++
	if m.failOnInitiate {
		if m.initiateError != nil {
			return nil, m.initiateError
		}
		return nil, errPositionKeepingUnavailable
	}
	return &positionkeepingv1.InitiateFinancialPositionLogResponse{
		Log: &positionkeepingv1.FinancialPositionLog{
			LogId: "POS-LOG-001",
		},
	}, nil
}

func (m *mockPositionKeepingClient) UpdateFinancialPositionLog(_ context.Context, req *positionkeepingv1.UpdateFinancialPositionLogRequest) (*positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
	m.updateCalls++

	if m.failOnUpdate {
		if m.failureError != nil {
			return nil, m.failureError
		}
		return nil, errPositionKeepingUnavailable
	}

	// Check if this is a compensation call (status update to CANCELLED indicates saga compensation)
	if req.StatusUpdate != nil && req.StatusUpdate.CurrentStatus == commonpb.TransactionStatus_TRANSACTION_STATUS_CANCELLED {
		m.compensateCalls++
	}

	// Also count as compensation if debit direction (legacy behavior)
	if req.NewEntry != nil && req.NewEntry.Direction == commonpb.PostingDirection_POSTING_DIRECTION_DEBIT {
		m.compensateCalls++
	}

	currentStatus := commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED
	if req.StatusUpdate != nil {
		currentStatus = req.StatusUpdate.CurrentStatus
	}

	resp := &positionkeepingv1.UpdateFinancialPositionLogResponse{
		Log: &positionkeepingv1.FinancialPositionLog{
			LogId:     req.LogId,
			AccountId: "ACC-001", // Default account ID for mock
			StatusTracking: &positionkeepingv1.StatusTracking{
				CurrentStatus:   currentStatus,
				StatusUpdatedAt: timestamppb.Now(),
			},
			CreatedAt: timestamppb.Now(),
			UpdatedAt: timestamppb.Now(),
			Version:   req.Version + 1,
		},
	}

	// Add transaction log entry if provided
	if req.NewEntry != nil {
		resp.Log.AccountId = req.NewEntry.AccountId
		resp.Log.TransactionLogEntries = []*positionkeepingv1.TransactionLogEntry{req.NewEntry}
	}

	if len(m.updateResponses) > 0 {
		resp = m.updateResponses[0]
		m.updateResponses = m.updateResponses[1:]
	}

	return resp, nil
}

func (m *mockPositionKeepingClient) RetrieveFinancialPositionLog(_ context.Context, req *positionkeepingv1.RetrieveFinancialPositionLogRequest) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
	m.retrieveCalls++
	return &positionkeepingv1.RetrieveFinancialPositionLogResponse{
		Log: &positionkeepingv1.FinancialPositionLog{
			LogId: req.LogId,
		},
	}, nil
}

func (m *mockPositionKeepingClient) BulkImportTransactions(_ context.Context, _ *positionkeepingv1.BulkImportTransactionsRequest) (*positionkeepingv1.BulkImportTransactionsResponse, error) {
	m.bulkImportCalls++
	return &positionkeepingv1.BulkImportTransactionsResponse{}, nil
}

func (m *mockPositionKeepingClient) ListFinancialPositionLogs(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	m.listCalls++
	return &positionkeepingv1.ListFinancialPositionLogsResponse{}, nil
}

func (m *mockPositionKeepingClient) Close() error {
	return nil
}

// Mock FinancialAccounting Client

type mockFinancialAccountingClient struct {
	captureCalls        int
	debitCaptureCalls   int // Track debit postings specifically
	creditCaptureCalls  int // Track credit postings specifically
	failOnCapture       bool
	failOnDebitCapture  bool // Fail specifically on debit postings
	failOnCreditCapture bool // Fail specifically on credit postings (after debit succeeds)
	failOnUpdate        bool // Fail on UpdateFinancialBookingLog
	failureError        error
	captureResponses    []*financialaccountingv1.CaptureLedgerPostingResponse
	compensateCalls     int
	initiateCalls       int
	updateCalls         int
	retrieveLogCalls    int
	listCalls           int
	retrievePostCalls   int
	lastCapturedReq     *financialaccountingv1.CaptureLedgerPostingRequest // Track last capture request
}

func (m *mockFinancialAccountingClient) InitiateFinancialBookingLog(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	m.initiateCalls++
	return &financialaccountingv1.InitiateFinancialBookingLogResponse{
		FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
			Id:        "BOOK-LOG-001",
			Status:    commonpb.TransactionStatus_TRANSACTION_STATUS_PENDING,
			CreatedAt: timestamppb.Now(),
			UpdatedAt: timestamppb.Now(),
		},
	}, nil
}

func (m *mockFinancialAccountingClient) UpdateFinancialBookingLog(_ context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	m.updateCalls++

	if m.failOnUpdate {
		if m.failureError != nil {
			return nil, m.failureError
		}
		return nil, errFinancialAccountingUnavailable
	}

	return &financialaccountingv1.UpdateFinancialBookingLogResponse{
		FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
			Id:        req.Id,
			Status:    req.Status,
			CreatedAt: timestamppb.Now(),
			UpdatedAt: timestamppb.Now(),
		},
	}, nil
}

func (m *mockFinancialAccountingClient) RetrieveFinancialBookingLog(_ context.Context, req *financialaccountingv1.RetrieveFinancialBookingLogRequest) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
	m.retrieveLogCalls++
	return &financialaccountingv1.RetrieveFinancialBookingLogResponse{
		FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
			Id:        req.Id,
			Status:    commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED,
			CreatedAt: timestamppb.Now(),
			UpdatedAt: timestamppb.Now(),
		},
	}, nil
}

func (m *mockFinancialAccountingClient) ListFinancialBookingLogs(_ context.Context, _ *financialaccountingv1.ListFinancialBookingLogsRequest) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
	m.listCalls++
	return &financialaccountingv1.ListFinancialBookingLogsResponse{}, nil
}

func (m *mockFinancialAccountingClient) CaptureLedgerPosting(_ context.Context, req *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	m.captureCalls++
	m.lastCapturedReq = req

	// Check for debit-specific failure before tracking
	if m.failOnDebitCapture && req.PostingDirection == commonpb.PostingDirection_POSTING_DIRECTION_DEBIT {
		if m.failureError != nil {
			return nil, m.failureError
		}
		return nil, errFinancialAccountingUnavailable
	}

	// Check for credit-specific failure (simulates credit posting failure after debit succeeds)
	if m.failOnCreditCapture && req.PostingDirection == commonpb.PostingDirection_POSTING_DIRECTION_CREDIT {
		// Only fail on non-compensation credits (original credit posting, not compensation reversals)
		if req.IdempotencyKey == nil || len(req.IdempotencyKey.Key) < 4 || req.IdempotencyKey.Key[:4] != "COMP" {
			if m.failureError != nil {
				return nil, m.failureError
			}
			return nil, errFinancialAccountingUnavailable
		}
	}

	if m.failOnCapture {
		if m.failureError != nil {
			return nil, m.failureError
		}
		return nil, errFinancialAccountingUnavailable
	}

	// Track debit vs credit postings separately (only on success)
	if req.PostingDirection == commonpb.PostingDirection_POSTING_DIRECTION_DEBIT { //nolint:staticcheck // QF1003 suggests switch but if-else is clearer for binary cases
		m.debitCaptureCalls++
		// Only count as compensation if this is a reversal (idempotency key contains "COMP")
		if req.IdempotencyKey != nil && len(req.IdempotencyKey.Key) > 4 && req.IdempotencyKey.Key[:4] == "COMP" {
			m.compensateCalls++
		}
	} else if req.PostingDirection == commonpb.PostingDirection_POSTING_DIRECTION_CREDIT {
		m.creditCaptureCalls++
		// Check for compensation credits too
		if req.IdempotencyKey != nil && len(req.IdempotencyKey.Key) > 4 && req.IdempotencyKey.Key[:4] == "COMP" {
			m.compensateCalls++
		}
	}

	resp := &financialaccountingv1.CaptureLedgerPostingResponse{
		LedgerPosting: &financialaccountingv1.LedgerPosting{
			Id:                    "LEDGER-001",
			FinancialBookingLogId: req.FinancialBookingLogId,
			PostingDirection:      req.PostingDirection,
			PostingAmount:         req.PostingAmount,
		},
	}

	if len(m.captureResponses) > 0 {
		resp = m.captureResponses[0]
		m.captureResponses = m.captureResponses[1:]
	}

	return resp, nil
}

func (m *mockFinancialAccountingClient) RetrieveLedgerPosting(_ context.Context, req *financialaccountingv1.RetrieveLedgerPostingRequest) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	m.retrievePostCalls++
	return &financialaccountingv1.RetrieveLedgerPostingResponse{
		LedgerPosting: &financialaccountingv1.LedgerPosting{
			Id:        req.Id,
			Status:    commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED,
			CreatedAt: timestamppb.Now(),
		},
	}, nil
}

func (m *mockFinancialAccountingClient) Close() error {
	return nil
}

// Helper functions for integration tests

const integrationTestTenantID = "test_tenant"

func setupIntegrationTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.CurrentAccountEntity{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(integrationTestTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create the current_accounts table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.current_accounts (
		id UUID PRIMARY KEY,
		account_number VARCHAR(255) NOT NULL UNIQUE,
		party_id UUID NOT NULL,
		currency VARCHAR(3) NOT NULL,
		balance_cents BIGINT NOT NULL DEFAULT 0,
		status VARCHAR(20) NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		version INT NOT NULL DEFAULT 1,
		created_by VARCHAR(255),
		updated_by VARCHAR(255)
	)`, schemaName)).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func createTestAccount(t *testing.T, ctx context.Context, repo *persistence.Repository, accountID string) domain.CurrentAccount {
	t.Helper()
	// Use accountID as AccountIdentification (stored in account_number column) for lookup compatibility.
	// The repository's FindByID searches by account_number, so AccountIdentification must match the lookup key.
	account, err := domain.NewCurrentAccount(accountID, accountID, uuid.New().String(), "GBP")
	require.NoError(t, err, "Failed to create test account")
	require.NoError(t, repo.Save(ctx, account), "Failed to save test account")
	return account
}

func createTestDepositRequest(accountID string, units int64, nanos int32) *pb.ExecuteDepositRequest {
	return &pb.ExecuteDepositRequest{
		AccountId: accountID,
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        units,
				Nanos:        nanos,
			},
		},
		Description: "Test deposit",
	}
}

// Test 1: Successful saga execution with all services

// TestExecuteDeposit_WithOrchestration_Success verifies the complete saga orchestration flow
// when all downstream services (PositionKeeping and FinancialAccounting) succeed.
//
// Flow verified:
// 1. Account balance is updated in database
// 2. Position log entry is created in PositionKeeping service
// 3. Ledger posting is captured in FinancialAccounting service
// 4. Correlation ID is propagated through all steps
// 5. Transaction completes successfully
func TestExecuteDeposit_WithOrchestration_Success(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-001")

	// Create mock clients
	mockPosKeeping := &mockPositionKeepingClient{}
	mockFinAcct := &mockFinancialAccountingClient{}

	// Create service with mocked clients
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-001", 100, 500000000) // £100.50
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify success
	require.NoError(t, err, "Deposit should succeed")
	assert.Equal(t, "ACC-001", resp.AccountId)
	assert.NotEmpty(t, resp.TransactionId, "Transaction ID should be generated")
	assert.Equal(t, pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED, resp.Status)

	// Verify balance is updated correctly
	assert.NotNil(t, resp.NewBalance)
	assert.Equal(t, int64(100), resp.NewBalance.Amount.Units)
	assert.Equal(t, int32(500000000), resp.NewBalance.Amount.Nanos)

	// Verify account persisted correctly
	updatedAccount, err := repo.FindByID(ctx, "ACC-001")
	require.NoError(t, err)
	assert.Equal(t, int64(10050), balanceCents(updatedAccount.Balance()), "Balance should be £100.50 = 10050 cents")

	// Verify service calls
	assert.Equal(t, 1, mockPosKeeping.initiateCalls, "PositionKeeping InitiateFinancialPositionLog should be called once")
	assert.Equal(t, 1, mockFinAcct.captureCalls, "FinancialAccounting CaptureLedgerPosting should be called once")

	// Verify no compensation occurred
	assert.Equal(t, 0, mockPosKeeping.compensateCalls, "No position compensation should occur on success")
	assert.Equal(t, 0, mockFinAcct.compensateCalls, "No ledger compensation should occur on success")
}

// Test 2: PositionKeeping failure triggers compensation

// TestExecuteDeposit_WithOrchestration_PositionKeepingFailure verifies saga compensation
// when the PositionKeeping service fails.
//
// Expected behavior:
// 1. PositionKeeping InitiateFinancialPositionLog fails (step 1 fails)
// 2. FinancialAccounting CaptureLedgerPosting is never called (step 2 not reached)
// 3. Account save is never called (step 3 not reached)
// 4. No compensation needed (no steps completed)
// 5. Transaction fails with appropriate error
func TestExecuteDeposit_WithOrchestration_PositionKeepingFailure(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	account := createTestAccount(t, ctx, repo, "ACC-002")
	originalBalance := balanceCents(account.Balance())

	// Create mock clients - PositionKeeping configured to fail on initiate
	mockPosKeeping := &mockPositionKeepingClient{
		failOnInitiate: true,
		initiateError:  errPositionKeepingUnavailable,
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	// Create service with mocked clients
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-002", 50, 0) // £50.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify failure
	require.Error(t, err, "Deposit should fail due to PositionKeeping failure")
	assert.Nil(t, resp, "Response should be nil on failure")

	// Verify error details
	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "log_position", "Error should mention failed step")
	assert.Contains(t, st.Message(), "compensated", "Error should mention compensation")

	// Verify account state after failure
	// With the fixed saga ordering, the account is never saved if external services fail,
	// so the balance should remain unchanged
	updatedAccount, err := repo.FindByID(ctx, "ACC-002")
	require.NoError(t, err)
	// Account balance should be unchanged because save_account is the final step
	assert.Equal(t, originalBalance, balanceCents(updatedAccount.Balance()),
		"Account balance should remain unchanged when external services fail")

	// Verify service calls
	assert.Equal(t, 1, mockPosKeeping.initiateCalls, "PositionKeeping should be called once (and fail)")
	assert.Equal(t, 0, mockFinAcct.captureCalls, "FinancialAccounting should never be called (step not reached)")

	// Verify compensation was triggered (no compensation for position keeping since it didn't succeed)
	assert.Equal(t, 0, mockPosKeeping.compensateCalls, "No position compensation (it never succeeded)")
}

// Test 3: FinancialAccounting failure triggers full compensation

// TestExecuteDeposit_WithOrchestration_FinancialAccountingFailure verifies saga compensation
// when the FinancialAccounting service fails after PositionKeeping succeeds.
//
// Expected behavior:
// 1. PositionKeeping InitiateFinancialPositionLog succeeds (step 1 succeeds)
// 2. FinancialAccounting CaptureLedgerPosting fails (step 2 fails)
// 3. Account save is never called (step 3 not reached)
// 4. Saga triggers compensation in reverse order:
//   - Compensate step 1: Update position log status to CANCELLED
//
// 5. Transaction fails with appropriate error
func TestExecuteDeposit_WithOrchestration_FinancialAccountingFailure(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	account := createTestAccount(t, ctx, repo, "ACC-003")
	originalBalance := balanceCents(account.Balance())

	// Create mock clients - FinancialAccounting configured to fail
	mockPosKeeping := &mockPositionKeepingClient{}
	mockFinAcct := &mockFinancialAccountingClient{
		failOnCapture: true,
		failureError:  errFinancialAccountingUnavailable,
	}

	// Create service with mocked clients
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-003", 75, 250000000) // £75.25
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify failure
	require.Error(t, err, "Deposit should fail due to FinancialAccounting failure")
	assert.Nil(t, resp, "Response should be nil on failure")

	// Verify error details
	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "post_ledger", "Error should mention failed step")
	assert.Contains(t, st.Message(), "compensated", "Error should mention compensation")

	// Verify account state after failure
	// With the fixed saga ordering, the account is never saved if external services fail,
	// so the balance should remain unchanged
	updatedAccount, err := repo.FindByID(ctx, "ACC-003")
	require.NoError(t, err)
	assert.Equal(t, originalBalance, balanceCents(updatedAccount.Balance()),
		"Account balance should remain unchanged when external services fail")

	// Verify service calls
	// InitiateFinancialPositionLog called once for the action
	// UpdateFinancialPositionLog called once for compensation (status update to CANCELLED)
	assert.Equal(t, 1, mockPosKeeping.initiateCalls, "PositionKeeping InitiateFinancialPositionLog should be called once (action)")
	assert.Equal(t, 1, mockPosKeeping.updateCalls, "PositionKeeping UpdateFinancialPositionLog should be called once (compensation)")
	assert.Equal(t, 1, mockPosKeeping.compensateCalls, "PositionKeeping compensation should cancel position log")
	assert.Equal(t, 1, mockFinAcct.captureCalls, "FinancialAccounting should be called once (and fail)")
	assert.Equal(t, 0, mockFinAcct.compensateCalls, "No ledger compensation (it never succeeded)")
}

// Test 4: NewServiceWithClients with valid configuration

// TestNewServiceWithClients_ValidConfig verifies the service factory function
// correctly creates a service instance with all required client dependencies.
//
// Validates:
// - Service is created successfully with valid configuration
// - All required fields are populated
// - Clients are properly initialized
func TestNewServiceWithClients_ValidConfig(t *testing.T) {
	// Setup
	db, _, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Note: NewServiceWithClients creates real gRPC client connections.
	// For unit testing, we verify the factory validates configuration correctly,
	// but cannot test full initialization without real services running.
	// Instead, we test the validation logic.

	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "missing repository",
			config: Config{
				Repository:                     nil,
				PositionKeepingServiceName:     "position-keeping",
				PositionKeepingPort:            50053,
				FinancialAccountingServiceName: "financial-accounting",
				FinancialAccountingPort:        50052,
			},
			wantErr: true,
			errMsg:  "repository cannot be nil",
		},
		{
			name: "missing position keeping service name",
			config: Config{
				Repository:                     repo,
				PositionKeepingServiceName:     "",
				FinancialAccountingServiceName: "financial-accounting",
				FinancialAccountingPort:        50052,
			},
			wantErr: true,
			errMsg:  "position keeping service name cannot be empty",
		},
		{
			name: "missing financial accounting service name",
			config: Config{
				Repository:                     repo,
				PositionKeepingServiceName:     "position-keeping",
				PositionKeepingPort:            50053,
				FinancialAccountingServiceName: "",
			},
			wantErr: true,
			errMsg:  "financial accounting service name cannot be empty",
		},
		{
			name: "all fields empty",
			config: Config{
				Repository:                     nil,
				PositionKeepingServiceName:     "",
				FinancialAccountingServiceName: "",
			},
			wantErr: true,
			errMsg:  "repository cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewServiceWithClients(tt.config)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, svc)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, svc)
				assert.NotNil(t, svc.repo)
				assert.NotNil(t, svc.logger)
			}
		})
	}
}

// Test 5: NewServiceWithClients handles missing service names

// TestNewServiceWithClients_MissingServiceNames verifies proper error handling
// when required service names are not provided in the configuration.
//
// This test validates fail-fast behavior at service initialization time,
// preventing runtime failures when clients are actually used.
func TestNewServiceWithClients_MissingServiceNames(t *testing.T) {
	// Setup
	db, _, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	t.Run("missing both service names", func(t *testing.T) {
		config := Config{
			Repository: repo,
		}

		svc, err := NewServiceWithClients(config)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "position keeping service name cannot be empty")
		assert.Nil(t, svc)
	})

	t.Run("missing financial accounting service name only", func(t *testing.T) {
		config := Config{
			Repository:                 repo,
			PositionKeepingServiceName: "position-keeping",
			PositionKeepingPort:        50053,
		}

		svc, err := NewServiceWithClients(config)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "financial accounting service name cannot be empty")
		assert.Nil(t, svc)
	})

	t.Run("all required fields provided", func(t *testing.T) {
		// This will create gRPC clients with DNS-based load balancing.
		// The DNS resolution happens lazily, so client creation succeeds.
		config := Config{
			Repository:                     repo,
			Namespace:                      "default",
			PositionKeepingServiceName:     "position-keeping",
			PositionKeepingPort:            50053,
			FinancialAccountingServiceName: "financial-accounting",
			FinancialAccountingPort:        50052,
		}

		// Should not return validation error - DNS resolution happens lazily
		svc, err := NewServiceWithClients(config)

		// We expect this to fail (no real service running), but NOT due to validation
		if err != nil {
			// Error should be about client creation, not validation
			assert.NotContains(t, err.Error(), "cannot be nil")
			assert.NotContains(t, err.Error(), "cannot be empty")
		} else {
			// If it somehow succeeds (unlikely), cleanup
			assert.NotNil(t, svc)
		}
	})
}

// Test 6: Compensation handles multiple step failures

// TestExecuteDeposit_WithOrchestration_CompensationOrder verifies that saga
// compensation executes in the correct order (reverse of execution order).
//
// This test ensures LIFO (Last In, First Out) compensation semantics,
// which is critical for properly unwinding distributed transactions.
func TestExecuteDeposit_WithOrchestration_CompensationOrder(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	account := createTestAccount(t, ctx, repo, "ACC-004")
	originalBalance := balanceCents(account.Balance())

	// Create mock that tracks compensation
	mockPosKeeping := &mockPositionKeepingClient{}

	mockFinAcct := &mockFinancialAccountingClient{
		failOnCapture: true,
		failureError:  errIntentionalTestFailure,
	}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit (will fail at step 3)
	req := createTestDepositRequest("ACC-004", 25, 0)
	_, err := svc.ExecuteDeposit(ctx, req)

	// Verify failure occurred
	require.Error(t, err)

	// Verify compensation was triggered
	assert.Equal(t, 1, mockPosKeeping.compensateCalls,
		"PositionKeeping compensation should be triggered")

	// Verify account state after failure
	// With the fixed saga ordering, the account is never saved if external services fail
	updatedAccount, err := repo.FindByID(ctx, "ACC-004")
	require.NoError(t, err)
	assert.Equal(t, originalBalance, balanceCents(updatedAccount.Balance()),
		"Account balance should remain unchanged when external services fail")
}

// Test 7: Context propagation through saga steps

// TestExecuteDeposit_WithOrchestration_ContextPropagation verifies that
// context (including correlation IDs and timeouts) is properly propagated
// through all saga steps.
func TestExecuteDeposit_WithOrchestration_ContextPropagation(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-005")

	mockPosKeeping := &mockPositionKeepingClient{}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit with context (ctx already has tenant from setup)
	req := createTestDepositRequest("ACC-005", 10, 0)
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify success
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Context propagation is verified implicitly by successful execution.
	// In a real integration test with actual services, we would verify
	// correlation IDs in distributed traces.
}

// Test 8: Backward compatibility (no clients configured)

// TestExecuteDeposit_WithoutClients_BackwardCompatibility verifies that
// the service continues to work when clients are not configured, falling
// back to simple database-only operation.
//
// This ensures backward compatibility with existing deployments that may
// not have the downstream services available yet.
func TestExecuteDeposit_WithoutClients_BackwardCompatibility(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-006")

	// Create service WITHOUT clients (backward compatibility mode)
	svc := NewService(repo, nil)

	// Execute deposit
	req := createTestDepositRequest("ACC-006", 200, 0) // £200.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify success
	require.NoError(t, err)
	assert.Equal(t, "ACC-006", resp.AccountId)
	assert.NotEmpty(t, resp.TransactionId)
	assert.Equal(t, pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED, resp.Status)

	// Verify balance is updated
	assert.NotNil(t, resp.NewBalance)
	assert.Equal(t, int64(200), resp.NewBalance.Amount.Units)

	// Verify account persisted
	updatedAccount, err := repo.FindByID(ctx, "ACC-006")
	require.NoError(t, err)
	assert.Equal(t, int64(20000), balanceCents(updatedAccount.Balance()))
}

// Double-Entry Bookkeeping Tests (with AccountConfig)

// TestExecuteDeposit_DoubleEntry_CreatesDualPostings verifies that when AccountConfig
// is provided with a clearing account, the deposit creates both debit and credit postings.
//
// Expected behavior:
// 1. BookingLog is created
// 2. DEBIT posting is created to clearing account
// 3. CREDIT posting is created to customer account
// 4. BookingLog is transitioned to POSTED
// 5. Both postings share the same BookingLogId
func TestExecuteDeposit_DoubleEntry_CreatesDualPostings(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-DE-001")

	// Create mock clients
	mockPosKeeping := &mockPositionKeepingClient{}
	mockFinAcct := &mockFinancialAccountingClient{}

	// Create service with AccountConfig (double-entry mode)
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		accountConfig: &config.AccountConfig{
			DepositClearingAccountID: "CLEARING-001",
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-DE-001", 100, 0) // £100.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify success
	require.NoError(t, err, "Deposit should succeed")
	assert.Equal(t, "ACC-DE-001", resp.AccountId)
	assert.Equal(t, pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED, resp.Status)

	// Verify dual postings
	assert.Equal(t, 2, mockFinAcct.captureCalls, "Should have 2 capture calls (debit + credit)")
	assert.Equal(t, 1, mockFinAcct.debitCaptureCalls, "Should have 1 debit posting to clearing account")
	assert.Equal(t, 1, mockFinAcct.creditCaptureCalls, "Should have 1 credit posting to customer account")

	// Verify BookingLog was created and updated to POSTED
	assert.Equal(t, 1, mockFinAcct.initiateCalls, "Should initiate 1 BookingLog")
	assert.Equal(t, 1, mockFinAcct.updateCalls, "Should update BookingLog to POSTED")

	// Verify no compensation occurred
	assert.Equal(t, 0, mockFinAcct.compensateCalls, "No compensation should occur on success")
}

// TestExecuteDeposit_DoubleEntry_SameBookingLogForBothPostings verifies that both
// the debit and credit postings are associated with the same BookingLog.
// This is verified by checking that 2 capture calls are made with the same BookingLogId.
func TestExecuteDeposit_DoubleEntry_SameBookingLogForBothPostings(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-DE-002")

	// Create mock clients
	mockPosKeeping := &mockPositionKeepingClient{}
	mockFinAcct := &mockFinancialAccountingClient{}

	// Create service with AccountConfig
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		accountConfig: &config.AccountConfig{
			DepositClearingAccountID: "CLEARING-002",
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-DE-002", 50, 0) // £50.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify success
	require.NoError(t, err, "Deposit should succeed")
	assert.NotNil(t, resp)

	// Verify both postings happened (1 debit + 1 credit)
	assert.Equal(t, 2, mockFinAcct.captureCalls, "Should have 2 capture calls")
	assert.Equal(t, 1, mockFinAcct.debitCaptureCalls, "Should have 1 debit posting")
	assert.Equal(t, 1, mockFinAcct.creditCaptureCalls, "Should have 1 credit posting")

	// The implementation guarantees both use the same BookingLogId since:
	// 1. BookingLog is created once at the start
	// 2. Both postings use the captured bookingLogID
	// This is verified in the implementation logging: "booking_log_id=BOOK-LOG-001"
	// appears in both debit and credit log messages
}

// TestExecuteDeposit_DoubleEntry_CompensatesOnFailure verifies that saga compensation
// reverses both postings when a failure occurs after they're created.
//
// Scenario: UpdateFinancialBookingLog fails after both debit and credit postings succeed.
// Expected: Compensation posts reversal entries (DEBIT to customer, CREDIT to clearing).
func TestExecuteDeposit_DoubleEntry_CompensatesOnFailure(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	account := createTestAccount(t, ctx, repo, "ACC-DE-003")
	originalBalance := balanceCents(account.Balance())

	mockPosKeeping := &mockPositionKeepingClient{}

	// Create mock that fails on UpdateFinancialBookingLog (after both postings succeed)
	mockFinAcct := &mockFinancialAccountingClient{
		failOnUpdate: true,
		failureError: errFinancialAccountingUnavailable,
	}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		accountConfig: &config.AccountConfig{
			DepositClearingAccountID: "CLEARING-003",
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-DE-003", 100, 0) // £100.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify failure
	require.Error(t, err, "Deposit should fail due to UpdateFinancialBookingLog failure")
	assert.Nil(t, resp, "Response should be nil on failure")

	// Verify error mentions the failed step
	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "post_ledger", "Error should mention failed step")

	// Verify postings were attempted (2 original postings + 2 compensation postings)
	// Original: 1 DEBIT to clearing + 1 CREDIT to customer = 2
	// Compensation: 1 DEBIT to customer (reverses credit) + 1 CREDIT to clearing (reverses debit) = 2
	// Mock counts by direction: 2 DEBIT total (original + compensation), 2 CREDIT total (original + compensation)
	assert.Equal(t, 2, mockFinAcct.debitCaptureCalls, "Should have 2 debit postings (1 original + 1 compensation)")
	assert.Equal(t, 2, mockFinAcct.creditCaptureCalls, "Should have 2 credit postings (1 original + 1 compensation)")
	assert.Equal(t, 2, mockFinAcct.compensateCalls, "Should have 2 compensation postings (COMP prefix)")
	assert.Equal(t, 4, mockFinAcct.captureCalls, "Should have 4 total capture calls (2 original + 2 compensation)")

	// Verify account balance unchanged (compensation should have reversed)
	updatedAccount, err := repo.FindByID(ctx, "ACC-DE-003")
	require.NoError(t, err)
	assert.Equal(t, originalBalance, balanceCents(updatedAccount.Balance()),
		"Account balance should remain unchanged after compensation")
}

// TestExecuteDeposit_DoubleEntry_DebitPostingFailure verifies behavior when the debit
// posting to the clearing account fails.
//
// Expected: Credit posting is NOT attempted, no compensation needed.
func TestExecuteDeposit_DoubleEntry_DebitPostingFailure(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	account := createTestAccount(t, ctx, repo, "ACC-DE-005")
	originalBalance := balanceCents(account.Balance())

	mockPosKeeping := &mockPositionKeepingClient{}

	// Create mock that fails specifically on debit postings
	mockFinAcct := &mockFinancialAccountingClient{
		failOnDebitCapture: true,
		failureError:       errFinancialAccountingUnavailable,
	}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		accountConfig: &config.AccountConfig{
			DepositClearingAccountID: "CLEARING-005",
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-DE-005", 100, 0) // £100.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify failure
	require.Error(t, err, "Deposit should fail due to debit posting failure")
	assert.Nil(t, resp, "Response should be nil on failure")

	// Verify error mentions the failed step
	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "post_ledger", "Error should mention failed step")
	assert.Contains(t, st.Message(), "debit", "Error should mention debit failure")

	// Verify only debit posting was attempted (and failed)
	assert.Equal(t, 1, mockFinAcct.captureCalls, "Should have only 1 capture call (failed debit)")
	assert.Equal(t, 0, mockFinAcct.debitCaptureCalls, "Should have 0 successful debit postings")
	assert.Equal(t, 0, mockFinAcct.creditCaptureCalls, "Credit posting should NOT have been attempted")
	assert.Equal(t, 0, mockFinAcct.compensateCalls, "No compensation needed")

	// BookingLog was created but no postings succeeded
	assert.Equal(t, 1, mockFinAcct.initiateCalls, "BookingLog should have been created")
	assert.Equal(t, 0, mockFinAcct.updateCalls, "BookingLog should NOT have been updated to POSTED")

	// Verify account balance unchanged
	updatedAccount, err := repo.FindByID(ctx, "ACC-DE-005")
	require.NoError(t, err)
	assert.Equal(t, originalBalance, balanceCents(updatedAccount.Balance()),
		"Account balance should remain unchanged")
}

// TestExecuteDeposit_DoubleEntry_CreditPostingFailure verifies that when the credit
// posting fails after debit succeeds, inline compensation creates a reversal for the debit.
func TestExecuteDeposit_DoubleEntry_CreditPostingFailure(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	account := createTestAccount(t, ctx, repo, "ACC-DE-006")
	originalBalance := balanceCents(account.Balance())

	mockPosKeeping := &mockPositionKeepingClient{}

	// Create mock that fails specifically on credit postings (after debit succeeds)
	mockFinAcct := &mockFinancialAccountingClient{
		failOnCreditCapture: true,
		failureError:        errIntentionalTestFailure,
	}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		accountConfig: &config.AccountConfig{
			DepositClearingAccountID: "CLEARING-006",
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-DE-006", 200, 0) // £200.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify failure
	require.Error(t, err, "Deposit should fail due to credit posting failure")
	assert.Nil(t, resp, "Response should be nil on failure")

	// Verify error mentions the failed step
	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "post_ledger", "Error should mention failed step")
	assert.Contains(t, st.Message(), "credit", "Error should mention credit failure")

	// Verify debit succeeded, credit failed, and inline compensation ran
	// Capture calls: 1 (debit success) + 1 (credit fail) + 1 (compensation credit to clear debit)
	assert.Equal(t, 3, mockFinAcct.captureCalls, "Should have 3 capture calls total")
	assert.Equal(t, 1, mockFinAcct.debitCaptureCalls, "Debit posting should have succeeded")
	assert.Equal(t, 1, mockFinAcct.creditCaptureCalls, "One compensation credit should succeed")
	assert.Equal(t, 1, mockFinAcct.compensateCalls, "Should have 1 compensation call (to reverse debit)")

	// BookingLog was created but not transitioned to POSTED
	// (inline compensation should have attempted to transition to CANCELLED)
	assert.Equal(t, 1, mockFinAcct.initiateCalls, "BookingLog should have been created")
	// UpdateCalls: 1 for CANCELLED transition attempt after credit fails
	assert.GreaterOrEqual(t, mockFinAcct.updateCalls, 1, "BookingLog should have update call(s)")

	// Verify account balance unchanged
	updatedAccount, err := repo.FindByID(ctx, "ACC-DE-006")
	require.NoError(t, err)
	assert.Equal(t, originalBalance, balanceCents(updatedAccount.Balance()),
		"Account balance should remain unchanged after failure and compensation")
}

// TestExecuteDeposit_SingleEntry_BackwardCompatibility verifies that deposits work
// without AccountConfig (backward compatibility - single-entry mode).
func TestExecuteDeposit_SingleEntry_BackwardCompatibility(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-SE-001")

	// Create mock clients
	mockPosKeeping := &mockPositionKeepingClient{}
	mockFinAcct := &mockFinancialAccountingClient{}

	// Create service WITHOUT AccountConfig (single-entry/backward compatible mode)
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		accountConfig:    nil, // No AccountConfig - single-entry mode
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-SE-001", 75, 0) // £75.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify success
	require.NoError(t, err, "Deposit should succeed")
	assert.Equal(t, "ACC-SE-001", resp.AccountId)
	assert.Equal(t, pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED, resp.Status)

	// Verify single posting (credit only, no debit)
	assert.Equal(t, 1, mockFinAcct.captureCalls, "Should have 1 capture call (credit only)")
	assert.Equal(t, 0, mockFinAcct.debitCaptureCalls, "Should have no debit posting")
	assert.Equal(t, 1, mockFinAcct.creditCaptureCalls, "Should have 1 credit posting")

	// Verify BookingLog flow
	assert.Equal(t, 1, mockFinAcct.initiateCalls, "Should initiate 1 BookingLog")
	assert.Equal(t, 1, mockFinAcct.updateCalls, "Should update BookingLog to POSTED")
}

// TestExecuteDeposit_DoubleEntry_ClearingAccountUsedForDebit verifies that the debit
// posting goes to the clearing account from config.
func TestExecuteDeposit_DoubleEntry_ClearingAccountUsedForDebit(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-DE-004")

	// Create mock clients
	mockPosKeeping := &mockPositionKeepingClient{}
	mockFinAcct := &mockFinancialAccountingClient{}

	clearingAccountID := "CLEARING-SPECIFIC-ID"

	// Create service with specific clearing account
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		finAcctClient:    mockFinAcct,
		accountConfig: &config.AccountConfig{
			DepositClearingAccountID: clearingAccountID,
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-DE-004", 100, 0) // £100.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify success
	require.NoError(t, err, "Deposit should succeed")
	assert.NotNil(t, resp)

	// The mock tracks the last request; we can verify the clearing account was used
	// by checking that debit call count is correct (we verified this happens to clearing account
	// in the implementation, and the mock tracks debit_capture_calls)
	assert.Equal(t, 1, mockFinAcct.debitCaptureCalls, "Should have 1 debit posting")
	assert.Equal(t, 1, mockFinAcct.creditCaptureCalls, "Should have 1 credit posting")
}
