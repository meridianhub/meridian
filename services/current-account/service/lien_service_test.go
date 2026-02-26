package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

func setupLienTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db := openSharedDB(t)

	// Each test gets a unique tenant → unique schema for isolation
	tid := uniqueTenantID()
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set search_path so tables are created in the tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// AutoMigrate account and lien entities
	err = db.AutoMigrate(&persistence.CurrentAccountEntity{}, &persistence.LienEntity{})
	require.NoError(t, err)

	// Ensure lien table has all required columns (AutoMigrate may not include all)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.lien (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(3) NOT NULL,
		bucket_id VARCHAR(255) NOT NULL DEFAULT '',
		status VARCHAR(20) NOT NULL,
		payment_order_reference VARCHAR(255) NOT NULL UNIQUE,
		termination_reason TEXT,
		expires_at TIMESTAMP WITH TIME ZONE,
		reserved_quantity JSONB,
		valued_amount JSONB,
		valuation_analysis JSONB,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		version INT NOT NULL DEFAULT 1
	)`, pq.QuoteIdentifier(schemaName))).Error
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
	// Configure Position Keeping mock with account balance (£1000 = 100000 cents)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-LIEN-001": 100000,
	})

	// Create account (balance comes from Position Keeping mock, not DB)
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
	// Configure Position Keeping mock with £100 balance (10000 cents)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-LIEN-002": 10000,
	})

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
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-LIEN-003": 100000,
	})

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
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-LIEN-IDEMP": 100000,
	})

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
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-LIEN-004": 100000,
	})

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-004", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-127", nil)
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
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-LIEN-005": 100000,
	})

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-005", 100000)

	// Create and execute a lien
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-128", nil)
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
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-LIEN-006": 100000,
	})

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-006", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-129", nil)
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
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-LIEN-007": 100000,
	})

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-007", 100000)

	// Create lien
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-130", nil)
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
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-131", nil)
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
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-LIEN-MULTI": 100000,
	})

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
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-IDEMP-1", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Pre-populate cached response
	tid, _ := tenant.FromContext(ctx)
	idempKey := idempotency.Key{
		TenantID:  string(tid),
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
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-IDEMP-2", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Mark operation as pending
	tid, _ := tenant.FromContext(ctx)
	idempKey := idempotency.Key{
		TenantID:  string(tid),
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
	svc := mustNewServiceWithIdempotencyAndPositionKeeping(t, repo, lienRepo, mockIdemp, map[string]int64{
		"ACC-LIEN-IDEMP-003": 100000, // £1000
	})

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-LIEN-IDEMP-003", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-IDEMP-3", nil)
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

	tid, _ := tenant.FromContext(ctx)
	idempKey := idempotency.Key{
		TenantID:  string(tid),
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

func TestInitiateLien_WithBucketID(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-BUCKET-001": 100000,
	})

	// Create account with balance
	createTestAccountWithBalance(t, ctx, repo, "ACC-BUCKET-001", 100000) // £1000

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-BUCKET-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        500,
				Nanos:        0, // £500
			},
		},
		PaymentOrderReference: "PO-BUCKET-123",
		BucketId:              "bucket-A",
	}

	resp, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Lien)
	require.NotEmpty(t, resp.Lien.LienId)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)
	require.Equal(t, "bucket-A", resp.Lien.BucketId, "bucket_id should be stored and returned")

	// Retrieve the lien and verify bucket_id persisted
	retrieveResp, err := svc.RetrieveLien(ctx, &pb.RetrieveLienRequest{LienId: resp.Lien.LienId})
	require.NoError(t, err)
	require.Equal(t, "bucket-A", retrieveResp.Lien.BucketId, "bucket_id should be persisted and retrievable")
}

func TestInitiateLien_WithoutBucketID_DefaultsToEmptyString(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-BUCKET-002": 100000,
	})

	// Create account with balance
	createTestAccountWithBalance(t, ctx, repo, "ACC-BUCKET-002", 100000) // £1000

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-BUCKET-002",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        500,
				Nanos:        0, // £500
			},
		},
		PaymentOrderReference: "PO-BUCKET-456",
		// BucketId not provided - should default to empty string
	}

	resp, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Lien)
	require.Equal(t, "", resp.Lien.BucketId, "bucket_id should default to empty string when not provided")
}

func TestInitiateLien_MultipleBuckets_IndependentLiens(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-BUCKET-003": 100000,
	})

	// Create account with £1000 balance
	createTestAccountWithBalance(t, ctx, repo, "ACC-BUCKET-003", 100000) // £1000

	// Create lien for bucket-A (£500)
	req1 := &pb.InitiateLienRequest{
		AccountId: "ACC-BUCKET-003",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 500},
		},
		PaymentOrderReference: "PO-BUCKET-A",
		BucketId:              "bucket-A",
	}
	resp1, err := svc.InitiateLien(ctx, req1)
	require.NoError(t, err)
	require.Equal(t, "bucket-A", resp1.Lien.BucketId)
	require.Equal(t, int64(500), resp1.AvailableBalance.Amount.Units) // £1000 - £500 = £500

	// Create lien for bucket-B (£300)
	req2 := &pb.InitiateLienRequest{
		AccountId: "ACC-BUCKET-003",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 300},
		},
		PaymentOrderReference: "PO-BUCKET-B",
		BucketId:              "bucket-B",
	}
	resp2, err := svc.InitiateLien(ctx, req2)
	require.NoError(t, err)
	require.Equal(t, "bucket-B", resp2.Lien.BucketId)
	// Phase 2: available balance is bucket-scoped, so for bucket-B: £1000 - £300 = £700
	// (bucket-A's £500 lien doesn't affect bucket-B's available balance)
	require.Equal(t, int64(700), resp2.AvailableBalance.Amount.Units)

	// Create lien for default bucket (empty string) (£100)
	req3 := &pb.InitiateLienRequest{
		AccountId: "ACC-BUCKET-003",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 100},
		},
		PaymentOrderReference: "PO-DEFAULT",
		// BucketId not provided
	}
	resp3, err := svc.InitiateLien(ctx, req3)
	require.NoError(t, err)
	require.Equal(t, "", resp3.Lien.BucketId)
	// Default bucket (empty string) considers ALL liens for available balance
	// £1000 - £500 (bucket-A) - £300 (bucket-B) - £100 (default) = £100
	require.Equal(t, int64(100), resp3.AvailableBalance.Amount.Units)
}

// TestInitiateLien_BucketAwareSolvency_UsesOnlyBucketLiens verifies that when a bucket_id
// is provided, solvency validation only considers liens from that specific bucket.
// This is Phase 2 bucket-aware solvency validation.
//
// Philosophy: Different buckets represent different fungibility domains (e.g., Grade A rice
// vs Grade B rice). Bucket-A liens should NOT affect bucket-B's available balance.
// This allows independent reservation within each fungibility bucket.
//
// Until Position Keeping supports bucket-scoped balances, we use total balance as a safety net.
// The formula is: Available = Total Balance - Bucket-Specific Liens
func TestInitiateLien_BucketAwareSolvency_UsesOnlyBucketLiens(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-BUCKET-SOLVENCY-001": 100000, // £1000
	})

	// Create account with £1000 balance
	createTestAccountWithBalance(t, ctx, repo, "ACC-BUCKET-SOLVENCY-001", 100000)

	// Step 1: Create lien for bucket-A (£600)
	req1 := &pb.InitiateLienRequest{
		AccountId: "ACC-BUCKET-SOLVENCY-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 600},
		},
		PaymentOrderReference: "PO-SOLVENCY-A1",
		BucketId:              "bucket-A",
	}
	resp1, err := svc.InitiateLien(ctx, req1)
	require.NoError(t, err)
	require.Equal(t, "bucket-A", resp1.Lien.BucketId)
	// Bucket-A available = £1000 - £600 (bucket-A liens) = £400
	require.Equal(t, int64(400), resp1.AvailableBalance.Amount.Units)

	// Step 2: Create lien for bucket-B (£600)
	// With bucket-aware solvency, bucket-B should be able to reserve £600 because:
	// - Total balance = £1000
	// - Bucket-B liens = £0 (bucket-A's £600 doesn't count for bucket-B)
	// - Bucket-B available = £1000 - £0 = £1000
	//
	// This ALLOWS "over-reservation" across buckets, which is INTENTIONAL.
	// In a multi-asset system, bucket-A (Grade A rice) and bucket-B (Grade B rice)
	// are different assets, so reservations should be independent.
	req2 := &pb.InitiateLienRequest{
		AccountId: "ACC-BUCKET-SOLVENCY-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 600},
		},
		PaymentOrderReference: "PO-SOLVENCY-B1",
		BucketId:              "bucket-B",
	}
	resp2, err := svc.InitiateLien(ctx, req2)
	require.NoError(t, err, "bucket-B should succeed with bucket-scoped solvency")
	require.Equal(t, "bucket-B", resp2.Lien.BucketId)
	// Bucket-B available = £1000 - £600 (bucket-B liens) = £400
	require.Equal(t, int64(400), resp2.AvailableBalance.Amount.Units)

	// Step 3: Try to create another bucket-A lien (£500)
	// Bucket-A already has £600 reserved
	// Bucket-A available = £1000 - £600 = £400
	// Requesting £500 should fail
	req3 := &pb.InitiateLienRequest{
		AccountId: "ACC-BUCKET-SOLVENCY-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 500},
		},
		PaymentOrderReference: "PO-SOLVENCY-A2",
		BucketId:              "bucket-A",
	}
	_, err = svc.InitiateLien(ctx, req3)
	require.Error(t, err, "should fail - bucket-A available is only £400")
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "insufficient available balance")

	// Step 4: But bucket-A can still reserve its remaining £400
	req4 := &pb.InitiateLienRequest{
		AccountId: "ACC-BUCKET-SOLVENCY-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 400},
		},
		PaymentOrderReference: "PO-SOLVENCY-A3",
		BucketId:              "bucket-A",
	}
	resp4, err := svc.InitiateLien(ctx, req4)
	require.NoError(t, err, "bucket-A should succeed with exactly remaining balance")
	require.Equal(t, "bucket-A", resp4.Lien.BucketId)
	// Bucket-A available = £1000 - £1000 (bucket-A liens: £600 + £400) = £0
	require.Equal(t, int64(0), resp4.AvailableBalance.Amount.Units)
}

// TestInitiateLien_BucketAwareSolvency_DefaultBucketUsesAllLiens verifies that when
// NO bucket_id is provided (empty string), solvency validation considers ALL liens
// regardless of their bucket. This maintains backward compatibility with Phase 1.
func TestInitiateLien_BucketAwareSolvency_DefaultBucketUsesAllLiens(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	// Configure Position Keeping mock with £1000 balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-DEFAULT-BUCKET-001": 100000, // £1000
	})

	// Create account with £1000 balance
	createTestAccountWithBalance(t, ctx, repo, "ACC-DEFAULT-BUCKET-001", 100000)

	// Create bucket-A lien (£600)
	req1 := &pb.InitiateLienRequest{
		AccountId: "ACC-DEFAULT-BUCKET-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 600},
		},
		PaymentOrderReference: "PO-DEFAULT-A1",
		BucketId:              "bucket-A",
	}
	_, err := svc.InitiateLien(ctx, req1)
	require.NoError(t, err)

	// Create lien WITHOUT bucket_id (should use ALL liens for solvency)
	// Available = £1000 - £600 (all liens) = £400
	req2 := &pb.InitiateLienRequest{
		AccountId: "ACC-DEFAULT-BUCKET-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 500}, // Exceeds £400 available
		},
		PaymentOrderReference: "PO-DEFAULT-NONE",
		// BucketId not set - defaults to empty string
	}
	_, err = svc.InitiateLien(ctx, req2)
	require.Error(t, err, "default bucket should consider ALL liens")
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())

	// But £400 should work
	req3 := &pb.InitiateLienRequest{
		AccountId: "ACC-DEFAULT-BUCKET-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 400},
		},
		PaymentOrderReference: "PO-DEFAULT-NONE2",
	}
	resp3, err := svc.InitiateLien(ctx, req3)
	require.NoError(t, err)
	require.Equal(t, "", resp3.Lien.BucketId)
	require.Equal(t, int64(0), resp3.AvailableBalance.Amount.Units)
}

// ============================================================================
// GetActiveAmountBlocks Tests
// ============================================================================

func TestGetActiveAmountBlocks_ReturnsAllActiveLiens(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-BLOCKS-001", 100000)

	// Create 3 active liens
	for i := 1; i <= 3; i++ {
		lienAmount, err := domain.NewMoney("GBP", int64(i*10000)) // £100, £200, £300
		require.NoError(t, err)
		lien, err := domain.NewLien(account.ID(), lienAmount, "", fmt.Sprintf("PO-BLOCKS-%d", i), nil)
		require.NoError(t, err)
		require.NoError(t, lienRepo.Create(ctx, lien))
	}

	// Query active amount blocks
	req := &pb.GetActiveAmountBlocksRequest{
		AccountId: "ACC-BLOCKS-001",
	}

	resp, err := svc.GetActiveAmountBlocks(ctx, req)
	require.NoError(t, err)
	require.Len(t, resp.Blocks, 3)

	// Verify all blocks have correct type and purpose
	for _, block := range resp.Blocks {
		require.NotEmpty(t, block.BlockId)
		require.Equal(t, pb.AmountBlockType_AMOUNT_BLOCK_TYPE_PENDING, block.BlockType)
		require.Contains(t, block.Purpose, "Payment Order:")
	}
}

func TestGetActiveAmountBlocks_FiltersOutExecutedLiens(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	// Configure Position Keeping mock with £1000 balance (needed for ExecuteLien response)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-BLOCKS-002": 100000,
	})

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-BLOCKS-002", 100000)

	// Create 3 liens
	var lienIDs []uuid.UUID
	for i := 1; i <= 3; i++ {
		lienAmount, err := domain.NewMoney("GBP", int64(i*10000))
		require.NoError(t, err)
		lien, err := domain.NewLien(account.ID(), lienAmount, "", fmt.Sprintf("PO-BLOCKS-EXEC-%d", i), nil)
		require.NoError(t, err)
		require.NoError(t, lienRepo.Create(ctx, lien))
		lienIDs = append(lienIDs, lien.ID)
	}

	// Execute one of the liens
	executeReq := &pb.ExecuteLienRequest{LienId: lienIDs[0].String()}
	_, err := svc.ExecuteLien(ctx, executeReq)
	require.NoError(t, err)

	// Query active amount blocks - should only return 2
	req := &pb.GetActiveAmountBlocksRequest{
		AccountId: "ACC-BLOCKS-002",
	}

	resp, err := svc.GetActiveAmountBlocks(ctx, req)
	require.NoError(t, err)
	require.Len(t, resp.Blocks, 2, "should only return active (non-executed) liens")

	// Verify the executed lien is not in the response
	executedLienID := lienIDs[0].String()
	for _, block := range resp.Blocks {
		require.NotEqual(t, executedLienID, block.BlockId, "executed lien should not be returned")
	}
}

func TestGetActiveAmountBlocks_FiltersOutTerminatedLiens(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	// Configure Position Keeping mock with £1000 balance (needed for TerminateLien response)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-BLOCKS-003": 100000,
	})

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-BLOCKS-003", 100000)

	// Create 3 liens
	var lienIDs []uuid.UUID
	for i := 1; i <= 3; i++ {
		lienAmount, err := domain.NewMoney("GBP", int64(i*10000))
		require.NoError(t, err)
		lien, err := domain.NewLien(account.ID(), lienAmount, "", fmt.Sprintf("PO-BLOCKS-TERM-%d", i), nil)
		require.NoError(t, err)
		require.NoError(t, lienRepo.Create(ctx, lien))
		lienIDs = append(lienIDs, lien.ID)
	}

	// Terminate one of the liens
	terminateReq := &pb.TerminateLienRequest{LienId: lienIDs[1].String(), Reason: "Test cancellation"}
	_, err := svc.TerminateLien(ctx, terminateReq)
	require.NoError(t, err)

	// Query active amount blocks - should only return 2
	req := &pb.GetActiveAmountBlocksRequest{
		AccountId: "ACC-BLOCKS-003",
	}

	resp, err := svc.GetActiveAmountBlocks(ctx, req)
	require.NoError(t, err)
	require.Len(t, resp.Blocks, 2, "should only return active (non-terminated) liens")

	// Verify the terminated lien is not in the response
	terminatedLienID := lienIDs[1].String()
	for _, block := range resp.Blocks {
		require.NotEqual(t, terminatedLienID, block.BlockId, "terminated lien should not be returned")
	}
}

func TestGetActiveAmountBlocks_ReturnsEmptyForAccountWithNoLiens(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account with no liens
	createTestAccountWithBalance(t, ctx, repo, "ACC-BLOCKS-EMPTY", 100000)

	// Query active amount blocks
	req := &pb.GetActiveAmountBlocksRequest{
		AccountId: "ACC-BLOCKS-EMPTY",
	}

	resp, err := svc.GetActiveAmountBlocks(ctx, req)
	require.NoError(t, err)
	require.Empty(t, resp.Blocks)
}

func TestGetActiveAmountBlocks_AccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	req := &pb.GetActiveAmountBlocksRequest{
		AccountId: "NON-EXISTENT-ACCOUNT",
	}

	_, err := svc.GetActiveAmountBlocks(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestGetActiveAmountBlocks_LienRepoNotConfigured(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil) // No lien repo

	// Create account first
	createTestAccountWithBalance(t, ctx, repo, "ACC-BLOCKS-NO-REPO", 100000)

	req := &pb.GetActiveAmountBlocksRequest{
		AccountId: "ACC-BLOCKS-NO-REPO",
	}

	_, err := svc.GetActiveAmountBlocks(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "lien operations not configured")
}

func TestGetActiveAmountBlocks_MapsAmountCorrectly(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	// Create account
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-BLOCKS-AMOUNT", 100000)

	// Create lien for £123.45 (12345 cents)
	lienAmount, err := domain.NewMoney("GBP", 12345)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-AMOUNT-TEST", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Query active amount blocks
	req := &pb.GetActiveAmountBlocksRequest{
		AccountId: "ACC-BLOCKS-AMOUNT",
	}

	resp, err := svc.GetActiveAmountBlocks(ctx, req)
	require.NoError(t, err)
	require.Len(t, resp.Blocks, 1)

	block := resp.Blocks[0]
	require.Equal(t, lien.ID.String(), block.BlockId)
	require.Equal(t, "GBP", block.Amount.Amount.CurrencyCode)
	require.Equal(t, int64(123), block.Amount.Amount.Units, "should map to £123")
	require.Equal(t, int32(450000000), block.Amount.Amount.Nanos, "should map to .45")
	require.Equal(t, "Payment Order: PO-AMOUNT-TEST", block.Purpose)
}

// ============================================================================
// Atomic Valuation in InitiateLien Tests
// ============================================================================

// setupAtomicValuationLienTest creates a test environment with lien repo, valuation feature repo,
// mock valuation engine, and mock position keeping client for testing atomic valuation in InitiateLien.
func setupAtomicValuationLienTest(t *testing.T, engine ValuationEngine, accountBalances map[string]int64) (*Service, context.Context, func()) {
	t.Helper()
	db := openSharedDB(t)

	tid := uniqueTenantID()
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set search_path so tables are created in the tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create account table (uses "account" table name like valuation engine tests)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.account (
		id UUID PRIMARY KEY,
		account_id VARCHAR(100) NOT NULL UNIQUE,
		account_identification VARCHAR(34) NOT NULL UNIQUE,
		party_id UUID NOT NULL,
		org_party_id UUID NULL,
		balance BIGINT NOT NULL DEFAULT 0,
		available_balance BIGINT NOT NULL DEFAULT 0,
		instrument_code VARCHAR(32) NOT NULL DEFAULT 'GBP',
		dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		overdraft_limit BIGINT NOT NULL DEFAULT 0,
		overdraft_enabled BOOLEAN NOT NULL DEFAULT FALSE,
		overdraft_rate NUMERIC(5,4) NOT NULL DEFAULT 0,
		balance_updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		created_by VARCHAR(100) NOT NULL DEFAULT 'system',
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		updated_by VARCHAR(100) NOT NULL DEFAULT 'system',
		deleted_at TIMESTAMP WITH TIME ZONE,
		opened_at TIMESTAMP WITH TIME ZONE,
		closed_at TIMESTAMP WITH TIME ZONE,
		freeze_reason TEXT,
		product_type_code VARCHAR(50) NULL,
		product_type_version INT NULL,
		behavior_class VARCHAR(50) NULL,
		version BIGINT NOT NULL DEFAULT 1
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create lien table
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.lien (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(3) NOT NULL,
		bucket_id VARCHAR(255) NOT NULL DEFAULT '',
		status VARCHAR(20) NOT NULL,
		payment_order_reference VARCHAR(255) NOT NULL UNIQUE,
		termination_reason TEXT,
		expires_at TIMESTAMP WITH TIME ZONE,
		reserved_quantity JSONB,
		valued_amount JSONB,
		valuation_analysis JSONB,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		version INT NOT NULL DEFAULT 1
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create valuation_features table
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.valuation_features (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		instrument_code VARCHAR(32) NOT NULL,
		valuation_method_id UUID NOT NULL,
		valuation_method_version INT NOT NULL,
		parameters JSONB,
		lifecycle_status VARCHAR(16) NOT NULL,
		valid_from TIMESTAMP WITH TIME ZONE NOT NULL,
		valid_to TIMESTAMP WITH TIME ZONE NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_by VARCHAR(100) NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_by VARCHAR(100) NOT NULL,
		version INT NOT NULL DEFAULT 1,
		CONSTRAINT chk_valuation_feature_lifecycle_status CHECK (lifecycle_status IN ('INITIATED','ACTIVE','TERMINATED'))
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create unique index for active features
	err = db.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS idx_valuation_feature_account_instrument_active
		ON %s.valuation_features (account_id, instrument_code)
		WHERE lifecycle_status = 'ACTIVE' AND valid_to = '9999-12-31 23:59:59+00'`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	valuationFeatureRepo := persistence.NewValuationFeatureRepository(db)

	svc, err := NewService(repo, lienRepo)
	require.NoError(t, err)

	// Inject valuation feature repo and engine
	svc.valuationFeatureRepo = valuationFeatureRepo
	if engine != nil {
		svc.valuationEngine = engine
	}

	// Inject position keeping mock
	if accountBalances == nil {
		accountBalances = make(map[string]int64)
	}
	mockPosKeeping := &mockPositionKeepingClient{accountBalances: accountBalances}
	svc.posKeepingClient = mockPosKeeping

	cleanup := func() {
		_ = db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	return svc, ctx, cleanup
}

func TestInitiateLien_AtomicValuation_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Mock engine: 100 kWh * 0.15 GBP/kWh = 15.00 GBP
	engine := &mockValuationEngine{
		evaluateFn: func(_ context.Context, params ValuationParams) (*ValuationResult, error) {
			return &ValuationResult{
				OutputAmount:    params.InputAmount.Mul(decimal.NewFromFloat(0.15)),
				OutputCode:      params.OutputCode,
				AppliedRates:    map[string]string{"energy_rate": "0.15"},
				ObservationIDs:  []string{"obs-energy-001"},
				ComputedAt:      time.Now(),
				CalculationPath: []string{"lookup_rate", "apply_energy_rate"},
			}, nil
		},
	}

	svc, ctx, cleanup := setupAtomicValuationLienTest(t, engine, map[string]int64{
		"ACC-VALUED-001": 500000, // £5000
	})
	defer cleanup()

	tid, _ := tenant.FromContext(ctx)
	schemaName := tid.SchemaName()

	// Create account and valuation feature
	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-VALUED-001", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "kWh")

	// Initiate lien with multi-asset input
	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "ACC-VALUED-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "kWh",
			Version:        1,
		},
		PaymentOrderReference: "PO-VALUED-001",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Lien)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)

	// Verify lien amount is the valued amount (100 * 0.15 = 15.00 GBP = 1500 cents)
	require.Equal(t, "GBP", resp.Lien.Amount.Amount.CurrencyCode)
	require.Equal(t, int64(15), resp.Lien.Amount.Amount.Units)

	// Verify reserved quantity on the lien
	require.NotNil(t, resp.Lien.ReservedQuantity, "reserved_quantity should be set on lien")
	require.Equal(t, "100", resp.Lien.ReservedQuantity.Amount)
	require.Equal(t, "kWh", resp.Lien.ReservedQuantity.InstrumentCode)

	// Verify valued amount on the lien
	require.NotNil(t, resp.Lien.ValuedAmount, "valued_amount should be set on lien")
	require.Equal(t, "GBP", resp.Lien.ValuedAmount.InstrumentCode)

	// Verify top-level response fields
	require.NotNil(t, resp.ValuedAmount, "response valued_amount should be set")
	require.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode)

	require.NotNil(t, resp.Basis, "response basis should be set")
	require.Equal(t, "0.15", resp.Basis.AppliedRates["energy_rate"])
	require.Contains(t, resp.Basis.ObservationIds, "obs-energy-001")
}

func TestInitiateLien_AtomicValuation_NoValuationFeature(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	engine := &mockValuationEngine{}

	svc, ctx, cleanup := setupAtomicValuationLienTest(t, engine, map[string]int64{
		"ACC-NOFEAT-001": 500000,
	})
	defer cleanup()

	tid, _ := tenant.FromContext(ctx)
	schemaName := tid.SchemaName()

	// Create account WITHOUT valuation feature
	createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-NOFEAT-001", "GBP")

	// Attempt InitiateLien with multi-asset input
	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "ACC-NOFEAT-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "kWh",
			Version:        1,
		},
		PaymentOrderReference: "PO-NOFEAT-001",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestInitiateLien_AtomicValuation_IdempotencyReturnsPriceLock(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Mock engine: 50 USD * 0.80 = 40.00 GBP
	engine := &mockValuationEngine{
		evaluateFn: func(_ context.Context, params ValuationParams) (*ValuationResult, error) {
			return &ValuationResult{
				OutputAmount:    params.InputAmount.Mul(decimal.NewFromFloat(0.80)),
				OutputCode:      params.OutputCode,
				AppliedRates:    map[string]string{"fx_rate": "0.80"},
				ObservationIDs:  []string{"obs-fx-001"},
				ComputedAt:      time.Now(),
				CalculationPath: []string{"lookup_fx", "apply_rate"},
			}, nil
		},
	}

	svc, ctx, cleanup := setupAtomicValuationLienTest(t, engine, map[string]int64{
		"ACC-IDEMP-001": 500000, // £5000
	})
	defer cleanup()

	tid, _ := tenant.FromContext(ctx)
	schemaName := tid.SchemaName()

	// Create account and valuation feature
	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-IDEMP-001", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "USD")

	// First call: creates the lien
	req := &pb.InitiateLienRequest{
		AccountId: "ACC-IDEMP-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "50.00",
			InstrumentCode: "USD",
			Version:        1,
		},
		PaymentOrderReference: "PO-IDEMP-001",
	}

	resp1, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp1.ValuedAmount)
	require.NotNil(t, resp1.Basis)

	// Second call (idempotent retry): should return same price lock
	resp2, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp2)

	// Verify same lien is returned
	require.Equal(t, resp1.Lien.LienId, resp2.Lien.LienId, "idempotent retry should return same lien")

	// Verify price lock is preserved (valued_amount on response)
	require.NotNil(t, resp2.ValuedAmount, "idempotent response should include valued_amount")
	require.Equal(t, resp1.ValuedAmount.Amount, resp2.ValuedAmount.Amount, "price lock should be identical")
	require.Equal(t, resp1.ValuedAmount.InstrumentCode, resp2.ValuedAmount.InstrumentCode)

	// Verify basis is preserved
	require.NotNil(t, resp2.Basis, "idempotent response should include basis")
	require.Equal(t, resp1.Basis.AppliedRates["fx_rate"], resp2.Basis.AppliedRates["fx_rate"])

	// Verify lien proto fields preserved
	require.NotNil(t, resp2.Lien.ReservedQuantity)
	require.Equal(t, "50", resp2.Lien.ReservedQuantity.Amount)
	require.Equal(t, "USD", resp2.Lien.ReservedQuantity.InstrumentCode)
}

func TestInitiateLien_AtomicValuation_InsufficientFunds(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Mock engine: 1000 kWh * 10.00 GBP/kWh = 10000.00 GBP (exceeds balance)
	engine := &mockValuationEngine{
		evaluateFn: func(_ context.Context, params ValuationParams) (*ValuationResult, error) {
			return &ValuationResult{
				OutputAmount:    params.InputAmount.Mul(decimal.NewFromFloat(10.00)),
				OutputCode:      params.OutputCode,
				AppliedRates:    map[string]string{"energy_rate": "10.00"},
				ObservationIDs:  []string{"obs-energy-002"},
				ComputedAt:      time.Now(),
				CalculationPath: []string{"lookup_rate"},
			}, nil
		},
	}

	svc, ctx, cleanup := setupAtomicValuationLienTest(t, engine, map[string]int64{
		"ACC-INSUF-001": 100000, // £1000 (valued amount will be £10000)
	})
	defer cleanup()

	tid, _ := tenant.FromContext(ctx)
	schemaName := tid.SchemaName()

	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-INSUF-001", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "kWh")

	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "ACC-INSUF-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "1000.00",
			InstrumentCode: "kWh",
			Version:        1,
		},
		PaymentOrderReference: "PO-INSUF-001",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "insufficient available balance")
}

func TestInitiateLien_AtomicValuation_LegacyModeStillWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Even with valuation engine configured, legacy MoneyAmount mode should still work
	engine := &mockValuationEngine{}
	svc, ctx, cleanup := setupAtomicValuationLienTest(t, engine, map[string]int64{
		"ACC-LEGACY-001": 500000,
	})
	defer cleanup()

	tid, _ := tenant.FromContext(ctx)
	schemaName := tid.SchemaName()
	createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-LEGACY-001", "GBP")

	// Legacy mode: use MoneyAmount, no Input field
	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "ACC-LEGACY-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
			},
		},
		PaymentOrderReference: "PO-LEGACY-001",
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Lien)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)

	// Legacy mode should NOT have valuation fields
	require.Nil(t, resp.Lien.ReservedQuantity, "legacy lien should not have reserved_quantity")
	require.Nil(t, resp.Lien.ValuedAmount, "legacy lien should not have valued_amount")
	require.Nil(t, resp.ValuedAmount, "legacy response should not have valued_amount")
	require.Nil(t, resp.Basis, "legacy response should not have basis")
}

// ============================================================================
// ExecuteLien & TerminateLien - ReleaseReservation Tests
// ============================================================================

func TestExecuteLien_CallsReleaseReservation_WithExecutedReason(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-RELEASE-001": 100000, // £1000
	})

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-RELEASE-001", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-RELEASE-001", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Execute the lien
	resp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)

	// Assert ReleaseReservation was called with EXECUTED reason
	mockPK := svc.posKeepingClient.(*mockPositionKeepingClient)
	mockPK.mu.Lock()
	defer mockPK.mu.Unlock()
	require.Equal(t, 1, mockPK.releaseReservationCalls, "ReleaseReservation should be called once")
	require.Equal(t, lien.ID.String(), mockPK.lastReleasedLienID, "should release the correct lien ID")
	require.Equal(t, positionkeepingv1.ReservationStatus_RESERVATION_STATUS_EXECUTED, mockPK.lastReleaseReason,
		"reason should be EXECUTED")
}

func TestTerminateLien_CallsReleaseReservation_WithTerminatedReason(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-RELEASE-002": 100000, // £1000
	})

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-RELEASE-002", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-RELEASE-002", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Terminate the lien
	resp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "Payment cancelled",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp.Lien.Status)

	// Assert ReleaseReservation was called with TERMINATED reason
	mockPK := svc.posKeepingClient.(*mockPositionKeepingClient)
	mockPK.mu.Lock()
	defer mockPK.mu.Unlock()
	require.Equal(t, 1, mockPK.releaseReservationCalls, "ReleaseReservation should be called once")
	require.Equal(t, lien.ID.String(), mockPK.lastReleasedLienID, "should release the correct lien ID")
	require.Equal(t, positionkeepingv1.ReservationStatus_RESERVATION_STATUS_TERMINATED, mockPK.lastReleaseReason,
		"reason should be TERMINATED")
}

func TestExecuteLien_ReleaseReservationFailure_DoesNotFailLienExecution(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-RELEASE-003": 100000, // £1000
	})

	// Configure mock to fail on ReleaseReservation
	mockPK := svc.posKeepingClient.(*mockPositionKeepingClient)
	mockPK.mu.Lock()
	mockPK.failOnReleaseReservation = true
	mockPK.mu.Unlock()

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-RELEASE-003", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-RELEASE-003", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Execute the lien - should succeed even though ReleaseReservation fails
	resp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.NoError(t, err, "ExecuteLien should succeed even when ReleaseReservation fails (best-effort)")
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
	require.NotEmpty(t, resp.TransactionId)

	// Verify ReleaseReservation was attempted
	mockPK.mu.Lock()
	defer mockPK.mu.Unlock()
	require.Equal(t, 1, mockPK.releaseReservationCalls, "ReleaseReservation should still be attempted")
}

func TestTerminateLien_ReleaseReservationFailure_DoesNotFailTermination(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-RELEASE-004": 100000, // £1000
	})

	// Configure mock to fail on ReleaseReservation
	mockPK := svc.posKeepingClient.(*mockPositionKeepingClient)
	mockPK.mu.Lock()
	mockPK.failOnReleaseReservation = true
	mockPK.mu.Unlock()

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-RELEASE-004", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-RELEASE-004", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Terminate the lien - should succeed even though ReleaseReservation fails
	resp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "Payment cancelled",
	})
	require.NoError(t, err, "TerminateLien should succeed even when ReleaseReservation fails (best-effort)")
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp.Lien.Status)

	// Verify ReleaseReservation was attempted
	mockPK.mu.Lock()
	defer mockPK.mu.Unlock()
	require.Equal(t, 1, mockPK.releaseReservationCalls, "ReleaseReservation should still be attempted")
}

// ============================================================================
// ExecuteLien - Price Lock Immutability Tests
// ============================================================================

func TestExecuteLien_UsesStoredPriceLock_NoRecalculation(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-PRICELOCK-001": 100000, // £1000
	})

	// Track if valuation engine is called during ExecuteLien.
	// Inject a mock engine that would fail if called (proving it's never invoked).
	callCount := 0
	svc.valuationEngine = &mockValuationEngine{
		evaluateFn: func(_ context.Context, _ ValuationParams) (*ValuationResult, error) {
			callCount++
			return &ValuationResult{
				OutputAmount: decimal.NewFromFloat(999.99),
				OutputCode:   "GBP",
			}, nil
		},
	}

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-PRICELOCK-001", 100000)

	// Create a valued lien (simulates what InitiateLien would produce for multi-asset)
	// The valued_amount is the price lock - it was set at InitiateLien time.
	lienAmount, err := domain.NewMoney("GBP", 50000) // £500 - the valued amount
	require.NoError(t, err)
	reservedQty := &domain.InstrumentAmount{
		Amount:         decimal.NewFromFloat(100.0),
		InstrumentCode: "kWh",
	}
	valuedAmt := &domain.InstrumentAmount{
		Amount:         decimal.NewFromFloat(500.0),
		InstrumentCode: "GBP",
	}
	lien, err := domain.NewValuedLien(account.ID(), lienAmount, "", "PO-PRICELOCK-001", nil, reservedQty, valuedAmt, nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Execute the lien - should NOT call valuation engine (price lock is immutable)
	execResp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{
		LienId: lien.ID.String(),
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, execResp.Lien.Status)
	require.Equal(t, 0, callCount, "valuation engine should NOT be called during ExecuteLien - price lock is immutable")

	// Verify the lien used the original £500 price lock (not recalculated)
	require.Equal(t, "GBP", execResp.Lien.Amount.Amount.CurrencyCode)
	require.Equal(t, int64(500), execResp.Lien.Amount.Amount.Units)

	// Verify the reserved quantity is preserved
	require.NotNil(t, execResp.Lien.ReservedQuantity)
	require.Equal(t, "kWh", execResp.Lien.ReservedQuantity.InstrumentCode)
	require.Equal(t, "100", execResp.Lien.ReservedQuantity.Amount)
}

func TestInitiateLien_AtomicValuation_ValuationImmutable(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a valued lien and verify that termination does not change valuation fields
	engine := &mockValuationEngine{
		evaluateFn: func(_ context.Context, params ValuationParams) (*ValuationResult, error) {
			return &ValuationResult{
				OutputAmount:    params.InputAmount.Mul(decimal.NewFromFloat(0.50)),
				OutputCode:      params.OutputCode,
				AppliedRates:    map[string]string{"rate": "0.50"},
				ObservationIDs:  []string{"obs-001"},
				ComputedAt:      time.Now(),
				CalculationPath: []string{"apply"},
			}, nil
		},
	}

	svc, ctx, cleanup := setupAtomicValuationLienTest(t, engine, map[string]int64{
		"ACC-IMMUT-001": 500000,
	})
	defer cleanup()

	tid, _ := tenant.FromContext(ctx)
	schemaName := tid.SchemaName()
	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-IMMUT-001", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "UNIT")

	// Create valued lien
	initResp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "ACC-IMMUT-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "200.00",
			InstrumentCode: "UNIT",
			Version:        1,
		},
		PaymentOrderReference: "PO-IMMUT-001",
	})
	require.NoError(t, err)
	lienID := initResp.Lien.LienId

	// Terminate the lien
	_, err = svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lienID,
		Reason: "test termination",
	})
	require.NoError(t, err)

	// Retrieve and verify valuation fields are still intact
	retrieveResp, err := svc.RetrieveLien(ctx, &pb.RetrieveLienRequest{LienId: lienID})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, retrieveResp.Lien.Status)
	require.NotNil(t, retrieveResp.Lien.ReservedQuantity, "valuation fields should survive termination")
	require.Equal(t, "200", retrieveResp.Lien.ReservedQuantity.Amount)
	require.Equal(t, "UNIT", retrieveResp.Lien.ReservedQuantity.InstrumentCode)
	require.NotNil(t, retrieveResp.Lien.ValuedAmount)
	require.Equal(t, "GBP", retrieveResp.Lien.ValuedAmount.InstrumentCode)
}
