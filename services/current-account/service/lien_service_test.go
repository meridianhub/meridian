package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

const lienSvcTestTenantID = "test_tenant"

func setupLienTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.CurrentAccountEntity{},
		&persistence.LienEntity{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(lienSvcTestTenantID)
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

	// Create the lien table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.lien (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(3) NOT NULL,
		status VARCHAR(20) NOT NULL,
		payment_order_reference VARCHAR(255) NOT NULL UNIQUE,
		termination_reason TEXT,
		expires_at TIMESTAMP WITH TIME ZONE,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		version INT NOT NULL DEFAULT 1
	)`, schemaName)).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func createTestAccountWithBalance(t *testing.T, ctx context.Context, repo *persistence.Repository, accountID string, balanceCents int64) domain.CurrentAccount {
	t.Helper()
	// Use accountID as AccountIdentification (stored in account_number column) for lookup compatibility.
	// The repository's FindByID searches by account_number, so AccountIdentification must match the lookup key.
	account, err := domain.NewCurrentAccount(accountID, accountID, uuid.New().String(), "GBP")
	require.NoError(t, err)

	if balanceCents > 0 {
		depositAmount, err := domain.NewMoney("GBP", balanceCents)
		require.NoError(t, err)
		account, err = account.Deposit(depositAmount)
		require.NoError(t, err)
	}

	require.NoError(t, repo.Save(ctx, account))
	return account
}

func TestInitiateLien_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account with £1000 balance
	createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-001", 100000) // 100000 cents = £1000

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-LIEN-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        500,
				Nanos:        0, // £500
			},
		},
		PaymentOrderReference: "PO-123",
	}

	resp, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Lien)
	require.NotEmpty(t, resp.Lien.LienId)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)
	require.Equal(t, "PO-123", resp.Lien.PaymentOrderReference)

	// Available balance should be £500 (£1000 - £500 lien)
	require.Equal(t, int64(500), resp.AvailableBalance.Amount.Units)
}

func TestInitiateLien_InsufficientFunds(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account with £100 balance
	createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-002", 10000) // £100

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-LIEN-002",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        500,
				Nanos:        0, // £500 - more than available
			},
		},
		PaymentOrderReference: "PO-124",
	}

	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "insufficient available balance")
}

func TestInitiateLien_AccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	req := &pb.InitiateLienRequest{
		AccountId: "NON-EXISTENT-ACC",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
			},
		},
		PaymentOrderReference: "PO-125",
	}

	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestInitiateLien_CurrencyMismatch(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create GBP account
	createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-003", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-LIEN-003",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD", // Different currency
				Units:        100,
				Nanos:        0,
			},
		},
		PaymentOrderReference: "PO-126",
	}

	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "currency")
}

func TestInitiateLien_Idempotent(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account with £1000 balance
	createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-IDEMP", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-LIEN-IDEMP",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        500,
				Nanos:        0,
			},
		},
		PaymentOrderReference: "PO-IDEMPOTENT-123",
	}

	// First call creates the lien
	resp1, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp1.Lien)
	lienID := resp1.Lien.LienId

	// Second call with same PaymentOrderReference should return the existing lien
	resp2, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp2.Lien)
	require.Equal(t, lienID, resp2.Lien.LienId, "idempotent call should return same lien")
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp2.Lien.Status)
}

func TestExecuteLien_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-004", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "PO-127", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Execute the lien
	req := &pb.ExecuteLienRequest{
		LienId: lien.ID.String(),
	}

	resp, err := svc.ExecuteLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
	require.NotEmpty(t, resp.TransactionId)

	// Balance should be £500 (£1000 - £500 debited)
	require.Equal(t, int64(500), resp.NewBalance.Amount.Units)
}

func TestExecuteLien_Idempotent(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-005", 100000)

	// Create and execute a lien
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "PO-128", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	req := &pb.ExecuteLienRequest{LienId: lien.ID.String()}

	// First execution
	resp1, err := svc.ExecuteLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp1.Lien.Status)

	// Second execution should be idempotent
	resp2, err := svc.ExecuteLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp2.Lien.Status)
	require.Equal(t, resp1.TransactionId, resp2.TransactionId)
}

func TestExecuteLien_NotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	req := &pb.ExecuteLienRequest{
		LienId: uuid.New().String(),
	}

	_, err := svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestTerminateLien_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-006", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "PO-129", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Terminate the lien
	req := &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "Payment cancelled",
	}

	resp, err := svc.TerminateLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp.Lien.Status)

	// Available balance should be restored to £1000
	require.Equal(t, int64(1000), resp.AvailableBalance.Amount.Units)
}

func TestTerminateLien_Idempotent(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-007", 100000)

	// Create lien
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "PO-130", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	req := &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "Cancelled",
	}

	// First termination
	resp1, err := svc.TerminateLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp1.Lien.Status)

	// Second termination should be idempotent
	resp2, err := svc.TerminateLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp2.Lien.Status)
}

func TestRetrieveLien_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-008", 100000)

	// Create lien
	lienAmount, err := domain.NewMoney("GBP", 25000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "PO-131", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Retrieve the lien
	req := &pb.RetrieveLienRequest{
		LienId: lien.ID.String(),
	}

	resp, err := svc.RetrieveLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, lien.ID.String(), resp.Lien.LienId)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)
	require.Equal(t, "PO-131", resp.Lien.PaymentOrderReference)
}

func TestRetrieveLien_NotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	req := &pb.RetrieveLienRequest{
		LienId: uuid.New().String(),
	}

	_, err := svc.RetrieveLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestLienOperations_LienRepoNotConfigured(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil) // No lien repo

	t.Run("InitiateLien", func(t *testing.T) {
		req := &pb.InitiateLienRequest{
			AccountId: "ACC-001",
			Amount: &commonpb.MoneyAmount{
				Amount: &money.Money{CurrencyCode: "GBP", Units: 100},
			},
			PaymentOrderReference: "PO-001",
		}
		_, err := svc.InitiateLien(ctx, req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("ExecuteLien", func(t *testing.T) {
		req := &pb.ExecuteLienRequest{LienId: uuid.New().String()}
		_, err := svc.ExecuteLien(ctx, req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("TerminateLien", func(t *testing.T) {
		req := &pb.TerminateLienRequest{LienId: uuid.New().String()}
		_, err := svc.TerminateLien(ctx, req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("RetrieveLien", func(t *testing.T) {
		req := &pb.RetrieveLienRequest{LienId: uuid.New().String()}
		_, err := svc.RetrieveLien(ctx, req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.FailedPrecondition, st.Code())
	})
}

func TestMultipleLiens_AvailableBalanceCalculation(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account with £1000 balance
	createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-MULTI", 100000)

	// Create first lien for £300
	req1 := &pb.InitiateLienRequest{
		AccountId: "ACC-LIEN-MULTI",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 300},
		},
		PaymentOrderReference: "PO-MULTI-1",
	}
	resp1, err := svc.InitiateLien(ctx, req1)
	require.NoError(t, err)
	require.Equal(t, int64(700), resp1.AvailableBalance.Amount.Units) // £1000 - £300 = £700

	// Create second lien for £200
	req2 := &pb.InitiateLienRequest{
		AccountId: "ACC-LIEN-MULTI",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 200},
		},
		PaymentOrderReference: "PO-MULTI-2",
	}
	resp2, err := svc.InitiateLien(ctx, req2)
	require.NoError(t, err)
	require.Equal(t, int64(500), resp2.AvailableBalance.Amount.Units) // £700 - £200 = £500

	// Try to create third lien for £600 (should fail - only £500 available)
	req3 := &pb.InitiateLienRequest{
		AccountId: "ACC-LIEN-MULTI",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 600},
		},
		PaymentOrderReference: "PO-MULTI-3",
	}
	_, err = svc.InitiateLien(ctx, req3)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())

	// Create third lien for £400 (should succeed - £500 available)
	req4 := &pb.InitiateLienRequest{
		AccountId: "ACC-LIEN-MULTI",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 400},
		},
		PaymentOrderReference: "PO-MULTI-4",
	}
	resp4, err := svc.InitiateLien(ctx, req4)
	require.NoError(t, err)
	require.Equal(t, int64(100), resp4.AvailableBalance.Amount.Units) // £500 - £400 = £100
}

// lienMockIdempotencyService implements idempotency.Service for testing ExecuteLien
type lienMockIdempotencyService struct {
	mu        sync.Mutex
	results   map[string]*idempotency.Result
	pending   map[string]bool
	checkErr  error
	storeErr  error
	deleteErr error
}

func newLienMockIdempotencyService() *lienMockIdempotencyService {
	return &lienMockIdempotencyService{
		results: make(map[string]*idempotency.Result),
		pending: make(map[string]bool),
	}
}

func (m *lienMockIdempotencyService) Check(_ context.Context, key idempotency.Key) (*idempotency.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.checkErr != nil {
		return nil, m.checkErr
	}

	keyStr := key.String()
	if result, ok := m.results[keyStr]; ok {
		if result.Status == idempotency.StatusCompleted {
			return result, idempotency.ErrOperationAlreadyProcessed
		}
		return result, nil
	}
	return nil, idempotency.ErrResultNotFound
}

func (m *lienMockIdempotencyService) MarkPending(_ context.Context, key idempotency.Key, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	keyStr := key.String()
	if m.pending[keyStr] {
		return idempotency.ErrOperationAlreadyProcessed
	}
	m.pending[keyStr] = true
	return nil
}

func (m *lienMockIdempotencyService) StoreResult(_ context.Context, result idempotency.Result) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.storeErr != nil {
		return m.storeErr
	}

	keyStr := result.Key.String()
	m.results[keyStr] = &result
	delete(m.pending, keyStr)
	return nil
}

func (m *lienMockIdempotencyService) Delete(_ context.Context, key idempotency.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteErr != nil {
		return m.deleteErr
	}

	keyStr := key.String()
	delete(m.results, keyStr)
	delete(m.pending, keyStr)
	return nil
}

func (m *lienMockIdempotencyService) Acquire(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
	return nil
}

func (m *lienMockIdempotencyService) Release(_ context.Context, _ idempotency.Key, _ string) error {
	return nil
}

func (m *lienMockIdempotencyService) Refresh(_ context.Context, _ idempotency.Key, _ string, _ time.Duration) error {
	return nil
}

func (m *lienMockIdempotencyService) IsHeld(_ context.Context, _ idempotency.Key) (bool, error) {
	return false, nil
}

func (m *lienMockIdempotencyService) setResult(key idempotency.Key, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[key.String()] = &idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusCompleted,
		Data:        data,
		CompletedAt: time.Now(),
	}
}

func (m *lienMockIdempotencyService) setPending(key idempotency.Key) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending[key.String()] = true
}

func (m *lienMockIdempotencyService) isPending(key idempotency.Key) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending[key.String()]
}

func TestExecuteLien_IdempotencyReturnsCachedResponse(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, lienRepo, mockIdemp)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-IDEMP-001", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "PO-IDEMP-1", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Pre-populate cached response
	idempKey := idempotency.Key{
		TenantID:  lienSvcTestTenantID,
		Namespace: "current-account",
		Operation: "execute_lien",
		EntityID:  lien.ID.String(),
		RequestID: "lien-req-123",
	}

	cachedResp := &pb.ExecuteLienResponse{
		Lien: &pb.Lien{
			LienId: lien.ID.String(),
			Status: pb.LienStatus_LIEN_STATUS_EXECUTED,
		},
		TransactionId: "cached-lien-tx-id",
		NewBalance: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 500, Nanos: 0},
		},
	}
	cachedData, err := proto.Marshal(cachedResp)
	require.NoError(t, err)
	mockIdemp.setResult(idempKey, cachedData)

	// Execute lien with same idempotency key
	req := &pb.ExecuteLienRequest{
		LienId:         lien.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "lien-req-123"},
	}

	resp, err := svc.ExecuteLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "cached-lien-tx-id", resp.TransactionId, "should return cached transaction ID")
	require.Equal(t, int64(500), resp.NewBalance.Amount.Units, "should return cached balance")
}

func TestExecuteLien_IdempotencyReturnsAbortedWhenInProgress(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, lienRepo, mockIdemp)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-IDEMP-002", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "PO-IDEMP-2", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Mark operation as pending
	idempKey := idempotency.Key{
		TenantID:  lienSvcTestTenantID,
		Namespace: "current-account",
		Operation: "execute_lien",
		EntityID:  lien.ID.String(),
		RequestID: "lien-req-456",
	}
	mockIdemp.setPending(idempKey)

	// Execute lien with same idempotency key
	req := &pb.ExecuteLienRequest{
		LienId:         lien.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "lien-req-456"},
	}

	_, err = svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Aborted, st.Code(), "should return Aborted for concurrent request")
	require.Contains(t, st.Message(), "already in progress")
}

func TestExecuteLien_IdempotencyProceedsWithoutKey(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, lienRepo, mockIdemp)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-IDEMP-003", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "PO-IDEMP-3", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Execute lien without idempotency key
	req := &pb.ExecuteLienRequest{
		LienId: lien.ID.String(),
		// No IdempotencyKey
	}

	resp, err := svc.ExecuteLien(ctx, req)
	require.NoError(t, err)
	require.NotEmpty(t, resp.TransactionId)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
}

func TestExecuteLien_IdempotencyCleanupOnFailure(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, lienRepo, mockIdemp)

	idempKey := idempotency.Key{
		TenantID:  lienSvcTestTenantID,
		Namespace: "current-account",
		Operation: "execute_lien",
		EntityID:  uuid.New().String(), // Non-existent lien ID
		RequestID: "lien-req-789",
	}

	// Execute lien for non-existent lien (will fail)
	req := &pb.ExecuteLienRequest{
		LienId:         idempKey.EntityID,
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "lien-req-789"},
	}

	_, err := svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())

	// Verify pending state was cleaned up
	require.False(t, mockIdemp.isPending(idempKey), "pending state should be cleaned up after failure")
}
