package service_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/internal/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	"github.com/meridianhub/meridian/internal/current-account/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var errNotImplemented = errors.New("not implemented")

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	// Use a unique shared-cache in-memory database per test so pooled connections see the same schema
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}

	// Run migrations
	if err := db.AutoMigrate(&persistence.CurrentAccountEntity{}); err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	return db, cleanup
}

// mockPositionKeepingClient implements clients.PositionKeepingClient for testing
type mockPositionKeepingClient struct {
	initiateLogFn func(context.Context, *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error)
	updateLogFn   func(context.Context, *positionkeepingv1.UpdateFinancialPositionLogRequest) (*positionkeepingv1.UpdateFinancialPositionLogResponse, error)
	retrieveLogFn func(context.Context, *positionkeepingv1.RetrieveFinancialPositionLogRequest) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error)
	bulkImportFn  func(context.Context, *positionkeepingv1.BulkImportTransactionsRequest) (*positionkeepingv1.BulkImportTransactionsResponse, error)
	listLogsFn    func(context.Context, *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error)
	callCount     atomic.Int32
}

func (m *mockPositionKeepingClient) InitiateFinancialPositionLog(ctx context.Context, req *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	m.callCount.Add(1)
	if m.initiateLogFn != nil {
		return m.initiateLogFn(ctx, req)
	}
	return &positionkeepingv1.InitiateFinancialPositionLogResponse{
		Log: &positionkeepingv1.FinancialPositionLog{
			LogId: "POS-12345678",
		},
	}, nil
}

func (m *mockPositionKeepingClient) UpdateFinancialPositionLog(ctx context.Context, req *positionkeepingv1.UpdateFinancialPositionLogRequest) (*positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
	if m.updateLogFn != nil {
		return m.updateLogFn(ctx, req)
	}
	return nil, errNotImplemented
}

func (m *mockPositionKeepingClient) RetrieveFinancialPositionLog(ctx context.Context, req *positionkeepingv1.RetrieveFinancialPositionLogRequest) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
	if m.retrieveLogFn != nil {
		return m.retrieveLogFn(ctx, req)
	}
	return nil, errNotImplemented
}

func (m *mockPositionKeepingClient) BulkImportTransactions(ctx context.Context, req *positionkeepingv1.BulkImportTransactionsRequest) (*positionkeepingv1.BulkImportTransactionsResponse, error) {
	if m.bulkImportFn != nil {
		return m.bulkImportFn(ctx, req)
	}
	return nil, errNotImplemented
}

func (m *mockPositionKeepingClient) ListFinancialPositionLogs(ctx context.Context, req *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	if m.listLogsFn != nil {
		return m.listLogsFn(ctx, req)
	}
	return nil, errNotImplemented
}

func (m *mockPositionKeepingClient) Close() error {
	return nil
}

// mockFinancialAccountingClient implements clients.FinancialAccountingClient for testing
type mockFinancialAccountingClient struct {
	initiateBookingFn func(context.Context, *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error)
	updateBookingFn   func(context.Context, *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error)
	retrieveBookingFn func(context.Context, *financialaccountingv1.RetrieveFinancialBookingLogRequest) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error)
	listBookingsFn    func(context.Context, *financialaccountingv1.ListFinancialBookingLogsRequest) (*financialaccountingv1.ListFinancialBookingLogsResponse, error)
	capturePostingFn  func(context.Context, *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error)
	retrievePostingFn func(context.Context, *financialaccountingv1.RetrieveLedgerPostingRequest) (*financialaccountingv1.RetrieveLedgerPostingResponse, error)
	callCount         atomic.Int32
}

func (m *mockFinancialAccountingClient) InitiateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	if m.initiateBookingFn != nil {
		return m.initiateBookingFn(ctx, req)
	}
	return nil, errNotImplemented
}

func (m *mockFinancialAccountingClient) UpdateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	if m.updateBookingFn != nil {
		return m.updateBookingFn(ctx, req)
	}
	return nil, errNotImplemented
}

func (m *mockFinancialAccountingClient) RetrieveFinancialBookingLog(ctx context.Context, req *financialaccountingv1.RetrieveFinancialBookingLogRequest) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
	if m.retrieveBookingFn != nil {
		return m.retrieveBookingFn(ctx, req)
	}
	return nil, errNotImplemented
}

func (m *mockFinancialAccountingClient) ListFinancialBookingLogs(ctx context.Context, req *financialaccountingv1.ListFinancialBookingLogsRequest) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
	if m.listBookingsFn != nil {
		return m.listBookingsFn(ctx, req)
	}
	return nil, errNotImplemented
}

func (m *mockFinancialAccountingClient) CaptureLedgerPosting(ctx context.Context, req *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	m.callCount.Add(1)
	if m.capturePostingFn != nil {
		return m.capturePostingFn(ctx, req)
	}
	return &financialaccountingv1.CaptureLedgerPostingResponse{
		LedgerPosting: &financialaccountingv1.LedgerPosting{
			Id: "POST-12345678",
		},
	}, nil
}

func (m *mockFinancialAccountingClient) RetrieveLedgerPosting(ctx context.Context, req *financialaccountingv1.RetrieveLedgerPostingRequest) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	if m.retrievePostingFn != nil {
		return m.retrievePostingFn(ctx, req)
	}
	return nil, errNotImplemented
}

func (m *mockFinancialAccountingClient) Close() error {
	return nil
}

// TestExecuteDeposit_Success verifies successful deposit with full service integration
func TestExecuteDeposit_Success(t *testing.T) {
	t.Parallel()

	// Setup database and repository
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Create test account
	account, err := domain.NewCurrentAccount("ACC-12345678", "GB82WEST12345698765432", "CUST-123", "GBP")
	require.NoError(t, err)

	err = repo.Save(account)
	require.NoError(t, err)

	// Setup mock clients
	posClient := &mockPositionKeepingClient{}
	accClient := &mockFinancialAccountingClient{}

	// Create service
	svc := service.NewService(repo, posClient, accClient, slog.Default())

	// Execute deposit
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-12345678",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        500000000, // 100.50 GBP
			},
		},
		Description: "Test deposit",
		Reference:   "REF-001",
	}

	resp, err := svc.ExecuteDeposit(context.Background(), req)

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "ACC-12345678", resp.AccountId)
	assert.NotEmpty(t, resp.TransactionId)
	assert.Equal(t, int64(100), resp.NewBalance.Amount.Units)
	assert.Equal(t, int32(500000000), resp.NewBalance.Amount.Nanos)
	assert.Equal(t, pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED, resp.Status)

	// Verify downstream services were called
	assert.Equal(t, int32(1), posClient.callCount.Load(), "position keeping client should be called once")
	assert.Equal(t, int32(1), accClient.callCount.Load(), "financial accounting client should be called once")

	// Verify account balance updated
	finalAccount, err := repo.FindByID("ACC-12345678")
	require.NoError(t, err)
	assert.Equal(t, int64(10050), finalAccount.Balance.AmountCents())
}

// TestExecuteDeposit_PositionKeepingFailure verifies saga compensation on position keeping failure
func TestExecuteDeposit_PositionKeepingFailure(t *testing.T) {
	t.Parallel()

	// Setup database and repository
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Create test account
	account, err := domain.NewCurrentAccount("ACC-12345678", "GB82WEST12345698765432", "CUST-123", "GBP")
	require.NoError(t, err)

	err = repo.Save(account)
	require.NoError(t, err)

	// Setup mock clients - position keeping fails
	posClient := &mockPositionKeepingClient{
		initiateLogFn: func(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
			return nil, status.Error(codes.Unavailable, "position keeping service unavailable")
		},
	}
	accClient := &mockFinancialAccountingClient{}

	// Create service
	svc := service.NewService(repo, posClient, accClient, slog.Default())

	// Execute deposit
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-12345678",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
			},
		},
		Description: "Test deposit",
	}

	resp, err := svc.ExecuteDeposit(context.Background(), req)

	// Assert transaction failed
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "deposit transaction failed")

	// Verify account balance was compensated (rolled back to 0)
	finalAccount, err := repo.FindByID("ACC-12345678")
	require.NoError(t, err)
	assert.Equal(t, int64(0), finalAccount.Balance.AmountCents(), "balance should be rolled back to 0")

	// Verify position keeping was called but accounting was not
	assert.Equal(t, int32(1), posClient.callCount.Load())
	assert.Equal(t, int32(0), accClient.callCount.Load(), "accounting should not be called after position keeping fails")
}

// TestExecuteDeposit_FinancialAccountingFailure verifies saga compensation on ledger posting failure
func TestExecuteDeposit_FinancialAccountingFailure(t *testing.T) {
	t.Parallel()

	// Setup database and repository
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Create test account
	account, err := domain.NewCurrentAccount("ACC-12345678", "GB82WEST12345698765432", "CUST-123", "GBP")
	require.NoError(t, err)

	err = repo.Save(account)
	require.NoError(t, err)

	// Setup mock clients - financial accounting fails
	posClient := &mockPositionKeepingClient{}
	accClient := &mockFinancialAccountingClient{
		capturePostingFn: func(_ context.Context, _ *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
			return nil, status.Error(codes.Internal, "ledger posting failed")
		},
	}

	// Create service
	svc := service.NewService(repo, posClient, accClient, slog.Default())

	// Execute deposit
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-12345678",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        50,
				Nanos:        0,
			},
		},
		Description: "Test deposit",
	}

	resp, err := svc.ExecuteDeposit(context.Background(), req)

	// Assert transaction failed
	require.Error(t, err)
	assert.Nil(t, resp)

	// Verify account balance was compensated (rolled back)
	finalAccount, err := repo.FindByID("ACC-12345678")
	require.NoError(t, err)
	assert.Equal(t, int64(0), finalAccount.Balance.AmountCents(), "balance should be rolled back")

	// Verify both services were called (accounting failed after position succeeded)
	assert.Equal(t, int32(1), posClient.callCount.Load())
	assert.Equal(t, int32(1), accClient.callCount.Load())
}

// TestExecuteDeposit_ContextCancellation verifies handling of context cancellation
func TestExecuteDeposit_ContextCancellation(t *testing.T) {
	t.Parallel()

	// Setup database and repository
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Create test account
	account, err := domain.NewCurrentAccount("ACC-12345678", "GB82WEST12345698765432", "CUST-123", "GBP")
	require.NoError(t, err)

	err = repo.Save(account)
	require.NoError(t, err)

	// Setup mock clients - position keeping delays and cancels context
	ctx, cancel := context.WithCancel(context.Background())
	posClient := &mockPositionKeepingClient{
		initiateLogFn: func(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
			cancel() // Cancel context during execution
			time.Sleep(10 * time.Millisecond)
			return nil, status.Error(codes.Canceled, "context canceled")
		},
	}
	accClient := &mockFinancialAccountingClient{}

	// Create service
	svc := service.NewService(repo, posClient, accClient, slog.Default())

	// Execute deposit
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-12345678",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        75,
				Nanos:        0,
			},
		},
	}

	resp, err := svc.ExecuteDeposit(ctx, req)

	// Assert transaction failed
	require.Error(t, err)
	assert.Nil(t, resp)

	// Verify account balance was compensated
	finalAccount, err := repo.FindByID("ACC-12345678")
	require.NoError(t, err)
	assert.Equal(t, int64(0), finalAccount.Balance.AmountCents())
}

// TestExecuteDeposit_AccountNotFound verifies error handling for missing account
func TestExecuteDeposit_AccountNotFound(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	posClient := &mockPositionKeepingClient{}
	accClient := &mockFinancialAccountingClient{}

	svc := service.NewService(repo, posClient, accClient, slog.Default())

	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-NONEXISTENT",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
			},
		},
	}

	resp, err := svc.ExecuteDeposit(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "account not found")

	// Verify no downstream services were called
	assert.Equal(t, int32(0), posClient.callCount.Load())
	assert.Equal(t, int32(0), accClient.callCount.Load())
}

// TestExecuteDeposit_CurrencyMismatch verifies validation of currency
func TestExecuteDeposit_CurrencyMismatch(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	account, err := domain.NewCurrentAccount("ACC-12345678", "GB82WEST12345698765432", "CUST-123", "GBP")
	require.NoError(t, err)

	err = repo.Save(account)
	require.NoError(t, err)

	posClient := &mockPositionKeepingClient{}
	accClient := &mockFinancialAccountingClient{}

	svc := service.NewService(repo, posClient, accClient, slog.Default())

	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-12345678",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD", // Wrong currency
				Units:        100,
				Nanos:        0,
			},
		},
	}

	resp, err := svc.ExecuteDeposit(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "currency mismatch")

	// Verify no downstream services were called
	assert.Equal(t, int32(0), posClient.callCount.Load())
	assert.Equal(t, int32(0), accClient.callCount.Load())
}

// TestExecuteDeposit_NegativeAmount verifies rejection of negative amounts
func TestExecuteDeposit_NegativeAmount(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	account, err := domain.NewCurrentAccount("ACC-12345678", "GB82WEST12345698765432", "CUST-123", "GBP")
	require.NoError(t, err)

	err = repo.Save(account)
	require.NoError(t, err)

	posClient := &mockPositionKeepingClient{}
	accClient := &mockFinancialAccountingClient{}

	svc := service.NewService(repo, posClient, accClient, slog.Default())

	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-12345678",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        -100, // Negative amount
				Nanos:        0,
			},
		},
	}

	resp, err := svc.ExecuteDeposit(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "deposit amount must be positive")

	// Verify no downstream services were called
	assert.Equal(t, int32(0), posClient.callCount.Load())
	assert.Equal(t, int32(0), accClient.callCount.Load())
}
