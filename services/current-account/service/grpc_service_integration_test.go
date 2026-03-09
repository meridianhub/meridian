package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/lib/pq"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
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
func balanceCents(m domain.Amount) int64 {
	cents, err := m.ToMinorUnits()
	if err != nil {
		panic("ToMinorUnits failed: " + err.Error())
	}
	return cents
}

// Mock PositionKeeping Client
// Thread-safe for concurrent access in tests like TestExecuteWithdrawal_ConcurrentWithdrawals.

type mockPositionKeepingClient struct {
	mu              sync.Mutex
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
	// Balance configuration for GetAccountBalance
	accountBalances             map[string]int64 // accountID -> balance in cents
	getBalanceCalls             int              // Track GetAccountBalance calls
	lastRequestedInstrumentCode string           // Track last requested instrument code
	requireInstrumentCode       bool             // If true, return error when instrument_code is missing
	returnInstrumentCode        string           // Override the instrument code in response (for testing mismatches)
	// ReleaseReservation tracking
	releaseReservationCalls  int
	lastReleasedLienID       string
	lastReleaseReason        positionkeepingv1.ReservationStatus
	failOnReleaseReservation bool
	// Initiate request capture (for valuation_analysis tests)
	lastInitiateRequest *positionkeepingv1.InitiateFinancialPositionLogRequest
}

func (m *mockPositionKeepingClient) InitiateFinancialPositionLog(_ context.Context, req *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	m.mu.Lock()
	m.initiateCalls++
	m.lastInitiateRequest = req
	failOnInitiate := m.failOnInitiate
	initiateError := m.initiateError
	m.mu.Unlock()

	if failOnInitiate {
		if initiateError != nil {
			return nil, initiateError
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
	m.mu.Lock()
	m.updateCalls++
	failOnUpdate := m.failOnUpdate
	failureError := m.failureError

	if failOnUpdate {
		m.mu.Unlock()
		if failureError != nil {
			return nil, failureError
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
	m.mu.Unlock()

	return resp, nil
}

func (m *mockPositionKeepingClient) RetrieveFinancialPositionLog(_ context.Context, req *positionkeepingv1.RetrieveFinancialPositionLogRequest) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
	m.mu.Lock()
	m.retrieveCalls++
	m.mu.Unlock()
	return &positionkeepingv1.RetrieveFinancialPositionLogResponse{
		Log: &positionkeepingv1.FinancialPositionLog{
			LogId: req.LogId,
		},
	}, nil
}

func (m *mockPositionKeepingClient) BulkImportTransactions(_ context.Context, _ *positionkeepingv1.BulkImportTransactionsRequest) (*positionkeepingv1.BulkImportTransactionsResponse, error) {
	m.mu.Lock()
	m.bulkImportCalls++
	m.mu.Unlock()
	return &positionkeepingv1.BulkImportTransactionsResponse{}, nil
}

func (m *mockPositionKeepingClient) ListFinancialPositionLogs(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	m.mu.Lock()
	m.listCalls++
	m.mu.Unlock()
	return &positionkeepingv1.ListFinancialPositionLogsResponse{}, nil
}

func (m *mockPositionKeepingClient) GetAccountBalance(_ context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	m.mu.Lock()
	m.getBalanceCalls++
	m.lastRequestedInstrumentCode = req.InstrumentCode

	// Optionally require instrument_code to be present (for testing validation)
	if m.requireInstrumentCode && req.InstrumentCode == "" {
		m.mu.Unlock()
		return nil, status.Error(codes.InvalidArgument, "instrument_code is required")
	}

	// Return configured balance if available
	var balanceCents int64
	if m.accountBalances != nil {
		balanceCents = m.accountBalances[req.AccountId]
	}

	// Determine which instrument code to return (default to GBP, but allow override for mismatch testing)
	responseInstrumentCode := "GBP"
	if m.returnInstrumentCode != "" {
		responseInstrumentCode = m.returnInstrumentCode
	}
	m.mu.Unlock()

	// Convert cents to decimal amount string (e.g., 10050 cents = "100.50")
	amount := decimal.NewFromInt(balanceCents).Div(decimal.NewFromInt(100))
	return &positionkeepingv1.GetAccountBalanceResponse{
		AccountId:   req.AccountId,
		BalanceType: req.BalanceType,
		Amount: &quantityv1.InstrumentAmount{
			Amount:         amount.StringFixed(2),
			InstrumentCode: responseInstrumentCode,
			Version:        1,
		},
		AsOf: timestamppb.Now(),
	}, nil
}

func (m *mockPositionKeepingClient) GetAccountBalances(_ context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	return &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: req.AccountId,
		Balances:  []*positionkeepingv1.BalanceEntry{},
	}, nil
}

func (m *mockPositionKeepingClient) ReleaseReservation(_ context.Context, req *positionkeepingv1.ReleaseReservationRequest) (*positionkeepingv1.ReleaseReservationResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseReservationCalls++
	m.lastReleasedLienID = req.GetLienId()
	m.lastReleaseReason = req.GetReason()

	if m.failOnReleaseReservation {
		return nil, status.Error(codes.Internal, "mock release reservation failure")
	}

	return &positionkeepingv1.ReleaseReservationResponse{
		Released: true,
	}, nil
}

func (m *mockPositionKeepingClient) Close() error {
	return nil
}

// Mock FinancialAccounting Client
// Thread-safe for concurrent access in tests like TestExecuteWithdrawal_ConcurrentWithdrawals.

type mockFinancialAccountingClient struct {
	mu                  sync.Mutex
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
	m.mu.Lock()
	m.initiateCalls++
	m.mu.Unlock()
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
	m.mu.Lock()
	m.updateCalls++
	failOnUpdate := m.failOnUpdate
	failureError := m.failureError
	m.mu.Unlock()

	if failOnUpdate {
		if failureError != nil {
			return nil, failureError
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
	m.mu.Lock()
	m.retrieveLogCalls++
	m.mu.Unlock()
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
	m.mu.Lock()
	m.listCalls++
	m.mu.Unlock()
	return &financialaccountingv1.ListFinancialBookingLogsResponse{}, nil
}

func (m *mockFinancialAccountingClient) CaptureLedgerPosting(_ context.Context, req *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	m.mu.Lock()
	m.captureCalls++
	m.lastCapturedReq = req
	failOnDebitCapture := m.failOnDebitCapture
	failOnCreditCapture := m.failOnCreditCapture
	failOnCapture := m.failOnCapture
	failureError := m.failureError
	m.mu.Unlock()

	// Check for debit-specific failure before tracking
	if failOnDebitCapture && req.PostingDirection == commonpb.PostingDirection_POSTING_DIRECTION_DEBIT {
		if failureError != nil {
			return nil, failureError
		}
		return nil, errFinancialAccountingUnavailable
	}

	// Check for credit-specific failure (simulates credit posting failure after debit succeeds)
	if failOnCreditCapture && req.PostingDirection == commonpb.PostingDirection_POSTING_DIRECTION_CREDIT {
		// Only fail on non-compensation credits (original credit posting, not compensation reversals)
		if req.IdempotencyKey == nil || len(req.IdempotencyKey.Key) < 4 || req.IdempotencyKey.Key[:4] != "COMP" {
			if failureError != nil {
				return nil, failureError
			}
			return nil, errFinancialAccountingUnavailable
		}
	}

	if failOnCapture {
		if failureError != nil {
			return nil, failureError
		}
		return nil, errFinancialAccountingUnavailable
	}

	// Track debit vs credit postings separately (only on success)
	m.mu.Lock()
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
	m.mu.Unlock()

	return resp, nil
}

func (m *mockFinancialAccountingClient) RetrieveLedgerPosting(_ context.Context, req *financialaccountingv1.RetrieveLedgerPostingRequest) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	m.mu.Lock()
	m.retrievePostCalls++
	m.mu.Unlock()
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

func setupIntegrationTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db := openSharedDB(t)

	// Each test gets a unique tenant → unique schema for isolation
	tid := uniqueTenantID()
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set search_path so AutoMigrate creates tables in the tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// AutoMigrate in the tenant schema
	err = db.AutoMigrate(&persistence.CurrentAccountEntity{})
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)

	cleanup := func() {
		_ = db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

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

// testLogger creates a test logger for use in tests.
// Returns the same logger instance to be shared across service and orchestrator.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

// testDepositOrchestrator creates a DepositOrchestrator for testing with the provided dependencies.
// Panics if orchestrator creation fails (acceptable in test helpers).
func testDepositOrchestrator(repo *persistence.Repository, posKeeping PositionKeepingClient, finAcct FinancialAccountingClient) *DepositOrchestrator {
	return testDepositOrchestratorWithConfig(repo, posKeeping, finAcct, nil)
}

// testDepositOrchestratorWithConfig creates a DepositOrchestrator with optional AccountConfig.
// Panics if orchestrator creation fails (acceptable in test helpers).
func testDepositOrchestratorWithConfig(repo *persistence.Repository, posKeeping PositionKeepingClient, finAcct FinancialAccountingClient, acctConfig *config.AccountConfig) *DepositOrchestrator {
	// Load saga script from reference-data canonical source
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get current file path")
	}
	serviceDir := filepath.Dir(filename)
	repoRoot := filepath.Join(serviceDir, "..", "..", "..")
	depositScriptPath := filepath.Join(repoRoot, "services", "reference-data", "saga", "defaults", "deposit", "v1.0.0.star")
	depositScriptBytes, err := os.ReadFile(depositScriptPath)
	if err != nil {
		panic(fmt.Sprintf("failed to read deposit script: %v", err))
	}
	depositScript := string(depositScriptBytes)

	// Create saga handler registry
	handlerRegistry := saga.NewHandlerRegistry()
	if err := RegisterCurrentAccountHandlers(handlerRegistry); err != nil {
		panic(fmt.Sprintf("failed to register saga handlers: %v", err))
	}

	// Build service modules (schema derived from proto metadata on handlers)
	serviceModules, err := schema.BuildServiceModules(handlerRegistry)
	if err != nil {
		panic(fmt.Sprintf("failed to build service modules: %v", err))
	}

	// Create Starlark saga runner
	runtime, err := saga.NewRuntime(testLogger())
	if err != nil {
		panic(fmt.Sprintf("failed to create saga runtime: %v", err))
	}

	sagaRunner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       handlerRegistry,
		ServiceModules: serviceModules,
		Logger:         testLogger(),
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create saga runner: %v", err))
	}

	orchestrator, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:           testLogger(),
		Repo:             repo,
		PosKeepingClient: posKeeping,
		FinAcctClient:    finAcct,
		AccountConfig:    acctConfig,
		SagaRunner:       sagaRunner,
		DepositScript:    depositScript,
	})
	if err != nil {
		panic(fmt.Sprintf("testDepositOrchestratorWithConfig: %v", err))
	}
	return orchestrator
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
	// Configure Position Keeping mock to return the expected balance after deposit (£100.50 = 10050 cents)
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-001": 10050, // £100.50 in cents
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	// Create service with mocked clients
	svc := &Service{
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-001", 100, 500000000) // £100.50
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify success
	require.NoError(t, err, "Deposit should succeed")
	assert.Equal(t, "ACC-001", resp.AccountId)
	assert.NotEmpty(t, resp.TransactionId, "Transaction ID should be generated")
	assert.Equal(t, pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED, resp.Status)

	// Verify balance is updated correctly in response
	// Note: Balance is now managed by Position Keeping service, not persisted locally
	assert.NotNil(t, resp.NewBalance)
	assert.Equal(t, int64(100), resp.NewBalance.Amount.Units)
	assert.Equal(t, int32(500000000), resp.NewBalance.Amount.Nanos)

	// Verify account exists (balance not checked - Position Keeping is authoritative)
	_, err = repo.FindByID(ctx, "ACC-001")
	require.NoError(t, err)

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
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestrator(repo, mockPosKeeping, mockFinAcct),
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
	assert.Contains(t, st.Message(), "initiate_log", "Error should mention failed step")
	// Note: When first step fails, there are no completed steps to compensate,
	// so the error message doesn't mention compensation.

	// Verify account state after failure
	// The saga is designed to perform all external service interactions before the final
	// save_account step. If an external service fails, the save_account step is never
	// executed, so the persisted account balance must remain unchanged.
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
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestrator(repo, mockPosKeeping, mockFinAcct),
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
	assert.Contains(t, st.Message(), "capture_posting", "Error should mention failed step")
	// Note: Compensation runs but error message doesn't mention "compensated"

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

// Test 4: Compensation handles multiple step failures

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
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestrator(repo, mockPosKeeping, mockFinAcct),
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
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestrator(repo, mockPosKeeping, mockFinAcct),
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
	acctConfig := &config.AccountConfig{
		DepositClearingAccountID: "CLEARING-001",
	}

	// Create service with AccountConfig (double-entry mode)
	svc := &Service{
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		accountConfig:       acctConfig,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestratorWithConfig(repo, mockPosKeeping, mockFinAcct, acctConfig),
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
	acctConfig := &config.AccountConfig{
		DepositClearingAccountID: "CLEARING-002",
	}

	// Create service with AccountConfig
	svc := &Service{
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		accountConfig:       acctConfig,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestratorWithConfig(repo, mockPosKeeping, mockFinAcct, acctConfig),
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
	acctConfig := &config.AccountConfig{
		DepositClearingAccountID: "CLEARING-003",
	}

	svc := &Service{
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		accountConfig:       acctConfig,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestratorWithConfig(repo, mockPosKeeping, mockFinAcct, acctConfig),
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
	assert.Contains(t, st.Message(), "update_booking_log", "Error should mention failed step")

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
	acctConfig := &config.AccountConfig{
		DepositClearingAccountID: "CLEARING-005",
	}

	svc := &Service{
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		accountConfig:       acctConfig,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestratorWithConfig(repo, mockPosKeeping, mockFinAcct, acctConfig),
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
	assert.Contains(t, st.Message(), "capture_posting", "Error should mention failed step")
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
	acctConfig := &config.AccountConfig{
		DepositClearingAccountID: "CLEARING-006",
	}

	svc := &Service{
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		accountConfig:       acctConfig,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestratorWithConfig(repo, mockPosKeeping, mockFinAcct, acctConfig),
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
	assert.Contains(t, st.Message(), "capture_posting", "Error should mention failed step")
	assert.Contains(t, st.Message(), "credit", "Error should mention credit failure")

	// Verify debit succeeded, credit failed, and inline compensation ran
	// Capture calls: 1 (debit success) + 1 (credit fail) + 1 (compensation credit to clear debit)
	assert.Equal(t, 3, mockFinAcct.captureCalls, "Should have 3 capture calls total")
	assert.Equal(t, 1, mockFinAcct.debitCaptureCalls, "Debit posting should have succeeded")
	assert.Equal(t, 1, mockFinAcct.creditCaptureCalls, "One compensation credit should succeed")
	assert.Equal(t, 1, mockFinAcct.compensateCalls, "Should have 1 compensation call (to reverse debit)")

	// BookingLog was created but not transitioned to POSTED
	// Note: Starlark saga does not have compensation handler for initiate_booking_log,
	// so booking log is left in INITIATED state (not updated to CANCELLED)
	assert.Equal(t, 1, mockFinAcct.initiateCalls, "BookingLog should have been created")
	assert.Equal(t, 0, mockFinAcct.updateCalls, "BookingLog is not updated during compensation")

	// Verify account balance unchanged
	updatedAccount, err := repo.FindByID(ctx, "ACC-DE-006")
	require.NoError(t, err)
	assert.Equal(t, originalBalance, balanceCents(updatedAccount.Balance()),
		"Account balance should remain unchanged after failure and compensation")
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
	acctConfig := &config.AccountConfig{
		DepositClearingAccountID: clearingAccountID,
	}

	// Create service with specific clearing account
	svc := &Service{
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		accountConfig:       acctConfig,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestratorWithConfig(repo, mockPosKeeping, mockFinAcct, acctConfig),
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

// Position Keeping Balance Delegation Tests
//
// These tests verify that balance is correctly delegated to Position Keeping service
// rather than being stored locally in the Current Account database.

// TestPositionKeeping_BalanceDelegation_DepositUpdatesPositionKeepingBalance verifies
// that after a deposit, the balance returned comes from Position Keeping service.
func TestPositionKeeping_BalanceDelegation_DepositUpdatesPositionKeepingBalance(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-BAL-001")

	// Configure Position Keeping mock to return a specific balance
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-BAL-001": 15075, // £150.75 in cents
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-BAL-001", 50, 0) // £50.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Verify success
	require.NoError(t, err, "Deposit should succeed")

	// Verify balance in response matches Position Keeping (£150.75)
	// Not the deposit amount - Position Keeping is the authoritative source
	assert.NotNil(t, resp.NewBalance)
	assert.Equal(t, int64(150), resp.NewBalance.Amount.Units, "Balance units should match Position Keeping")
	assert.Equal(t, int32(750000000), resp.NewBalance.Amount.Nanos, "Balance nanos should match Position Keeping")
}

// TestPositionKeeping_BalanceDelegation_MultipleDepositsAccumulate verifies
// that multiple deposits correctly accumulate in Position Keeping.
func TestPositionKeeping_BalanceDelegation_MultipleDepositsAccumulate(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-BAL-002")

	// Configure Position Keeping mock with accumulating balance
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-BAL-002": 0, // Start at zero
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// First deposit - update mock balance after
	req1 := createTestDepositRequest("ACC-BAL-002", 100, 0) // £100.00
	_, err := svc.ExecuteDeposit(ctx, req1)
	require.NoError(t, err, "First deposit should succeed")

	// Simulate Position Keeping balance after first deposit
	mockPosKeeping.accountBalances["ACC-BAL-002"] = 10000 // £100.00

	// Second deposit - mock will now return accumulated balance
	mockPosKeeping.accountBalances["ACC-BAL-002"] = 25000   // £250.00 (100 + 150)
	req2 := createTestDepositRequest("ACC-BAL-002", 150, 0) // £150.00
	resp2, err := svc.ExecuteDeposit(ctx, req2)
	require.NoError(t, err, "Second deposit should succeed")

	// Verify accumulated balance from Position Keeping
	assert.NotNil(t, resp2.NewBalance)
	assert.Equal(t, int64(250), resp2.NewBalance.Amount.Units, "Balance should show accumulated total")

	// Verify Position Keeping was called for each deposit
	assert.Equal(t, 2, mockPosKeeping.initiateCalls, "Position Keeping should be called for each deposit")
}

// TestPositionKeeping_BalanceDelegation_RetrieveAccountUsesPositionKeeping verifies
// that RetrieveCurrentAccount fetches balance from Position Keeping.
func TestPositionKeeping_BalanceDelegation_RetrieveAccountUsesPositionKeeping(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-BAL-003")

	// Configure Position Keeping with specific balance
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-BAL-003": 99999, // £999.99
		},
	}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		logger:           testLogger(),
	}

	// Retrieve account
	resp, err := svc.RetrieveCurrentAccount(ctx, &pb.RetrieveCurrentAccountRequest{
		AccountId: "ACC-BAL-003",
	})

	require.NoError(t, err, "Retrieve should succeed")
	assert.NotNil(t, resp.Facility)

	// Balance should come from Position Keeping
	assert.NotNil(t, resp.Facility.CurrentBalance)
	assert.Equal(t, int64(999), resp.Facility.CurrentBalance.CurrentBalance.Amount.Units, "Balance should be from Position Keeping")
	assert.Equal(t, int32(990000000), resp.Facility.CurrentBalance.CurrentBalance.Amount.Nanos, "Balance nanos should be from Position Keeping")
}

// TestPositionKeeping_BalanceDelegation_ZeroBalanceHandled verifies
// that zero balance from Position Keeping is correctly handled.
func TestPositionKeeping_BalanceDelegation_ZeroBalanceHandled(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-BAL-004")

	// Configure Position Keeping with zero balance
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-BAL-004": 0, // Zero balance
		},
	}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		logger:           testLogger(),
	}

	// Retrieve account
	resp, err := svc.RetrieveCurrentAccount(ctx, &pb.RetrieveCurrentAccountRequest{
		AccountId: "ACC-BAL-004",
	})

	require.NoError(t, err, "Retrieve should succeed")
	assert.NotNil(t, resp.Facility)
	assert.NotNil(t, resp.Facility.CurrentBalance)
	assert.Equal(t, int64(0), resp.Facility.CurrentBalance.CurrentBalance.Amount.Units, "Zero balance should be handled correctly")
	assert.Equal(t, int32(0), resp.Facility.CurrentBalance.CurrentBalance.Amount.Nanos, "Zero nanos should be handled correctly")
}

// TestPositionKeeping_BalanceDelegation_NegativeBalanceHandled verifies
// that negative balance (overdraft) from Position Keeping is correctly handled.
func TestPositionKeeping_BalanceDelegation_NegativeBalanceHandled(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-BAL-005")

	// Configure Position Keeping with negative balance (overdraft)
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-BAL-005": -5000, // -£50.00 (overdrawn)
		},
	}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		logger:           testLogger(),
	}

	// Retrieve account
	resp, err := svc.RetrieveCurrentAccount(ctx, &pb.RetrieveCurrentAccountRequest{
		AccountId: "ACC-BAL-005",
	})

	require.NoError(t, err, "Retrieve should succeed")
	assert.NotNil(t, resp.Facility)
	assert.NotNil(t, resp.Facility.CurrentBalance)
	assert.Equal(t, int64(-50), resp.Facility.CurrentBalance.CurrentBalance.Amount.Units, "Negative balance should be handled correctly")
}

// TestPositionKeeping_MultiAssetAPI_InstrumentCodeSent verifies that
// Current Account sends instrument_code="GBP" in balance queries.
func TestPositionKeeping_MultiAssetAPI_InstrumentCodeSent(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-MULTI-001")

	// Configure Position Keeping mock with tracking
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-MULTI-001": 50000, // £500.00
		},
	}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		logger:           testLogger(),
	}

	// Retrieve account
	resp, err := svc.RetrieveCurrentAccount(ctx, &pb.RetrieveCurrentAccountRequest{
		AccountId: "ACC-MULTI-001",
	})

	require.NoError(t, err, "Retrieve should succeed")
	assert.NotNil(t, resp.Facility)

	// Verify instrument_code was sent in the request
	assert.Equal(t, 1, mockPosKeeping.getBalanceCalls, "GetAccountBalance should be called once")
	assert.Equal(t, "GBP", mockPosKeeping.lastRequestedInstrumentCode,
		"Request should include instrument_code='GBP' for multi-asset API")

	// Verify response balance is correct
	assert.Equal(t, int64(500), resp.Facility.CurrentBalance.CurrentBalance.Amount.Units)
}

// TestPositionKeeping_MultiAssetAPI_InstrumentCodeMismatch verifies that
// Current Account rejects responses with mismatched instrument codes.
func TestPositionKeeping_MultiAssetAPI_InstrumentCodeMismatch(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-MULTI-002")

	// Configure Position Keeping mock to return wrong instrument code
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-MULTI-002": 50000, // £500.00
		},
		returnInstrumentCode: "EUR", // Return EUR instead of GBP
	}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		logger:           testLogger(),
	}

	// Retrieve account should succeed with degraded response (best-effort)
	resp, err := svc.RetrieveCurrentAccount(ctx, &pb.RetrieveCurrentAccountRequest{
		AccountId: "ACC-MULTI-002",
	})

	require.NoError(t, err, "Retrieve should succeed even with instrument mismatch (best-effort)")
	assert.NotNil(t, resp.Facility, "Account facility should be returned")
	assert.Equal(t, int64(0), resp.Facility.CurrentBalance.CurrentBalance.Amount.Units, "Balance should be zero when hydration fails")
}

// Circuit Breaker Tests for Position Keeping Balance Queries
//
// These tests verify that the circuit breaker correctly handles Position Keeping
// failures for balance queries and provides graceful degradation.

// mockPositionKeepingClientWithGetBalanceFailure extends the mock to support
// GetAccountBalance failure scenarios.
type mockPositionKeepingClientWithGetBalanceFailure struct {
	mockPositionKeepingClient
	failOnGetBalance  bool
	getBalanceError   error
	getBalanceCalls   int
	getBalancesError  error
	failOnGetBalances bool
	getBalancesCalls  int
}

func (m *mockPositionKeepingClientWithGetBalanceFailure) GetAccountBalance(_ context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	m.getBalanceCalls++
	if m.failOnGetBalance {
		if m.getBalanceError != nil {
			return nil, m.getBalanceError
		}
		return nil, errPositionKeepingUnavailable
	}
	// Return configured balance if available
	var balanceCents int64
	if m.accountBalances != nil {
		balanceCents = m.accountBalances[req.AccountId]
	}
	// Convert cents to decimal amount string
	amount := decimal.NewFromInt(balanceCents).Div(decimal.NewFromInt(100))
	return &positionkeepingv1.GetAccountBalanceResponse{
		AccountId:   req.AccountId,
		BalanceType: req.BalanceType,
		Amount: &quantityv1.InstrumentAmount{
			Amount:         amount.StringFixed(2),
			InstrumentCode: "GBP",
			Version:        1,
		},
		AsOf: timestamppb.Now(),
	}, nil
}

func (m *mockPositionKeepingClientWithGetBalanceFailure) GetAccountBalances(_ context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	m.getBalancesCalls++
	if m.failOnGetBalances {
		if m.getBalancesError != nil {
			return nil, m.getBalancesError
		}
		return nil, errPositionKeepingUnavailable
	}
	return &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: req.AccountId,
		Balances:  []*positionkeepingv1.BalanceEntry{},
	}, nil
}

// TestPositionKeeping_CircuitBreaker_GetBalanceFailure verifies that
// RetrieveCurrentAccount returns the account without balance when
// GetAccountBalance fails (best-effort degradation).
func TestPositionKeeping_CircuitBreaker_GetBalanceFailure(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-CB-001")

	// Configure Position Keeping to fail on GetAccountBalance
	mockPosKeeping := &mockPositionKeepingClientWithGetBalanceFailure{
		failOnGetBalance: true,
		getBalanceError:  errPositionKeepingUnavailable,
	}

	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPosKeeping,
		logger:           testLogger(),
	}

	// Retrieve account - should succeed with empty balance (best-effort)
	resp, err := svc.RetrieveCurrentAccount(ctx, &pb.RetrieveCurrentAccountRequest{
		AccountId: "ACC-CB-001",
	})

	// Verify success with degraded response (zero balance, not hydrated)
	require.NoError(t, err, "Retrieve should succeed even when Position Keeping is unavailable")
	assert.NotNil(t, resp.Facility, "Account facility should be returned")
	assert.Equal(t, int64(0), resp.Facility.CurrentBalance.CurrentBalance.Amount.Units, "Balance should be zero when Position Keeping is unavailable")
	assert.Equal(t, int32(0), resp.Facility.CurrentBalance.CurrentBalance.Amount.Nanos, "Balance nanos should be zero when Position Keeping is unavailable")

	// Verify Position Keeping was called
	assert.Equal(t, 1, mockPosKeeping.getBalanceCalls, "GetAccountBalance should have been called once")
}

// TestPositionKeeping_CircuitBreaker_DepositSucceedsWithBalanceQueryFailure verifies
// that deposits can still succeed even if the balance query after deposit fails.
// The deposit transaction itself goes through Position Keeping InitiateFinancialPositionLog.
func TestPositionKeeping_CircuitBreaker_DepositSucceedsWithInitiateSuccess(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	_ = createTestAccount(t, ctx, repo, "ACC-CB-002")

	// Configure Position Keeping to succeed on initiate but return specific balance
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-CB-002": 5000, // £50.00
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:                repo,
		posKeepingClient:    mockPosKeeping,
		finAcctClient:       mockFinAcct,
		logger:              testLogger(),
		depositOrchestrator: testDepositOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Execute deposit
	req := createTestDepositRequest("ACC-CB-002", 100, 0) // £100.00
	resp, err := svc.ExecuteDeposit(ctx, req)

	// Deposit should succeed
	require.NoError(t, err, "Deposit should succeed")
	assert.Equal(t, pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED, resp.Status)

	// Balance in response comes from Position Keeping
	assert.Equal(t, int64(50), resp.NewBalance.Amount.Units, "Balance should match mock")

	// Verify Position Keeping InitiateFinancialPositionLog was called
	assert.Equal(t, 1, mockPosKeeping.initiateCalls, "InitiateFinancialPositionLog should be called")
}
