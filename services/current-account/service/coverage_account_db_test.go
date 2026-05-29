package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	pq "github.com/lib/pq"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// =============================================================================
// UpdateCurrentAccount tests (DB-backed)
// =============================================================================

func TestUpdateCurrentAccount_AccountNotFoundDB(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	svc := &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.UpdateCurrentAccount(ctx, &pb.UpdateCurrentAccountRequest{
		AccountId: "NONEXISTENT-001",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUpdateCurrentAccount_ClosedAccount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)

	// Create an account and close it
	account := createTestAccountWithBalance(t, ctx, repo, "UPD-CLOSE-001", 0)
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "0.00", InstrumentCode: "GBP"},
		},
	}
	closeSvc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := closeSvc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "UPD-CLOSE-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Account no longer needed",
	})
	require.NoError(t, err)

	// Attempt to update the closed account
	_ = account
	svc := &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err = svc.UpdateCurrentAccount(ctx, &pb.UpdateCurrentAccountRequest{
		AccountId: "UPD-CLOSE-001",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "closed")
}

func TestUpdateCurrentAccount_OverdraftIgnoredAndNoChanges(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "UPD-OVR-001", 10000)

	svc := &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	resp, err := svc.UpdateCurrentAccount(ctx, &pb.UpdateCurrentAccountRequest{
		AccountId:      "UPD-OVR-001",
		OverdraftLimit: &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Facility)
}

func TestUpdateCurrentAccount_MissingAccountIDUnit(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.UpdateCurrentAccount(context.Background(), &pb.UpdateCurrentAccountRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// =============================================================================
// ControlCurrentAccount additional paths
// =============================================================================

func TestControlCurrentAccount_FreezeShortReasonUnit(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "CTL-FRZS-001", 0)

	svc := &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-FRZS-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "short",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestControlCurrentAccount_FreezeAlreadyFrozenUnit(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "CTL-FRZA-001", 0)

	svc := &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// First freeze
	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-FRZA-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "Suspicious activity detected on this account",
	})
	require.NoError(t, err)

	// Second freeze - should fail
	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-FRZA-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "Second freeze attempt on frozen account",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestControlCurrentAccount_UnfreezeNotFrozenUnit(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "CTL-UFRZ-002", 0)

	svc := &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-UFRZ-002",
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNFREEZE,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestControlCurrentAccount_CloseAlreadyClosedUnit(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "CTL-CCLOSE-001", 0)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "0.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// First close
	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-CCLOSE-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Account no longer needed",
	})
	require.NoError(t, err)

	// Second close - should fail
	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-CCLOSE-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// =============================================================================
// Withdrawal tests with DB (UpdateWithdrawal, RetrieveWithdrawal by account)
// =============================================================================

func setupWithdrawalTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db := openSharedDB(t)
	tid := uniqueTenantID()
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)
	err = db.Exec(fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)
	err = db.AutoMigrate(&persistence.CurrentAccountEntity{}, &persistence.LienEntity{}, &persistence.WithdrawalEntity{})
	require.NoError(t, err)

	// Also create lien table with full schema (same as setupLienTestDB)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.lien (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL,
		instrument_code VARCHAR(32) NOT NULL DEFAULT '',
		dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
		precision INT NOT NULL DEFAULT 2,
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

func TestUpdateWithdrawal_NotFound(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.UpdateWithdrawal(ctx, &pb.UpdateWithdrawalRequest{
		WithdrawalId: "WTH-NONEXIST-001",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUpdateWithdrawal_WithWarnings(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	// Create account and withdrawal
	createTestAccountWithBalance(t, ctx, repo, "UPD-WTH-001", 100000)
	account, err := repo.FindByID(ctx, "UPD-WTH-001")
	require.NoError(t, err)

	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(account.ID(), amount, "WTH-UPD-REF-001")
	require.NoError(t, err)
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "1000.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		withdrawalRepo:   withdrawalRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	resp, err := svc.UpdateWithdrawal(ctx, &pb.UpdateWithdrawalRequest{
		WithdrawalId: "WTH-UPD-REF-001",
		Amount:       &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 100}},
		Description:  "updated description",
		Reference:    "NEW-REF",
	})
	require.NoError(t, err)
	assert.False(t, resp.ValidationPassed)
	assert.Len(t, resp.ValidationMessages, 3)
}

func TestRetrieveWithdrawal_ByAccountIDUnit(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	// Create account and withdrawal
	createTestAccountWithBalance(t, ctx, repo, "RET-WTH-ACC-001", 100000)
	account, err := repo.FindByID(ctx, "RET-WTH-ACC-001")
	require.NoError(t, err)

	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(account.ID(), amount, "WTH-RET-REF-001")
	require.NoError(t, err)
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	resp, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		AccountId: "RET-WTH-ACC-001",
	})
	require.NoError(t, err)
	assert.Len(t, resp.Withdrawals, 1)
	assert.Equal(t, int64(1), resp.Pagination.TotalCount)
}

func TestRetrieveWithdrawal_ByAccountID_AccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		AccountId: "RET-WTH-NONEXIST",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestRetrieveWithdrawal_SingleByReference(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	createTestAccountWithBalance(t, ctx, repo, "RET-WTH-SINGLE-001", 100000)
	account, err := repo.FindByID(ctx, "RET-WTH-SINGLE-001")
	require.NoError(t, err)

	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(account.ID(), amount, "WTH-SINGLE-REF-001")
	require.NoError(t, err)
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	resp, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		WithdrawalId: "WTH-SINGLE-REF-001",
	})
	require.NoError(t, err)
	assert.Len(t, resp.Withdrawals, 1)
}

func TestRetrieveWithdrawal_ByAccountWithPagination(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	createTestAccountWithBalance(t, ctx, repo, "RET-WTH-PAGE-001", 100000)
	account, err := repo.FindByID(ctx, "RET-WTH-PAGE-001")
	require.NoError(t, err)

	// Create 3 withdrawals
	for i := 0; i < 3; i++ {
		amt, err := domain.NewMoney("GBP", 1000)
		require.NoError(t, err)
		w, err := domain.NewWithdrawal(account.ID(), amt, fmt.Sprintf("WTH-PAGE-REF-%03d", i))
		require.NoError(t, err)
		require.NoError(t, withdrawalRepo.Create(ctx, w))
	}

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// Request with page size 2
	resp, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		AccountId: "RET-WTH-PAGE-001",
		Pagination: &commonpb.Pagination{
			PageSize: 2,
		},
	})
	require.NoError(t, err)
	assert.Len(t, resp.Withdrawals, 2)
	assert.NotEmpty(t, resp.Pagination.NextPageToken)

	// Second page
	resp2, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		AccountId: "RET-WTH-PAGE-001",
		Pagination: &commonpb.Pagination{
			PageSize:  2,
			PageToken: resp.Pagination.NextPageToken,
		},
	})
	require.NoError(t, err)
	assert.Len(t, resp2.Withdrawals, 1)
}

func TestRetrieveWithdrawal_InvalidPageToken(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "RET-WTH-BAD-001", 100000)

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		AccountId: "RET-WTH-BAD-001",
		Pagination: &commonpb.Pagination{
			PageToken: "not-a-number",
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRetrieveWithdrawal_NotFoundByReference(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		WithdrawalId: "WTH-DOES-NOT-EXIST",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUpdateWithdrawal_NoChanges(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	createTestAccountWithBalance(t, ctx, repo, "UPD-WTH-NC-001", 100000)
	account, err := repo.FindByID(ctx, "UPD-WTH-NC-001")
	require.NoError(t, err)

	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(account.ID(), amount, "WTH-NC-REF-001")
	require.NoError(t, err)
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// Update with no field changes - should succeed with no warnings
	resp, err := svc.UpdateWithdrawal(ctx, &pb.UpdateWithdrawalRequest{
		WithdrawalId: "WTH-NC-REF-001",
	})
	require.NoError(t, err)
	assert.True(t, resp.ValidationPassed)
	assert.Empty(t, resp.ValidationMessages)
}

func TestDatabaseHealthChecker_Error(t *testing.T) {
	// Create a checker with a repository backed by a closed DB
	db := openSharedDB(t)
	repo := persistence.NewRepository(db)
	sqlDB, _ := db.DB()
	_ = sqlDB.Close() // Close the DB connection to force errors

	checker := NewDatabaseHealthChecker(repo, 2*time.Second)
	result := checker.Check(context.Background())
	assert.Equal(t, health.StatusUnhealthy, result.Status)
	assert.Contains(t, result.Message, "database check failed")
}

func TestDatabaseHealthChecker_Success(t *testing.T) {
	db := openSharedDB(t)
	repo := persistence.NewRepository(db)
	checker := NewDatabaseHealthChecker(repo, 2*time.Second)

	result := checker.Check(context.Background())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Equal(t, "database connection successful", result.Message)
}

func TestInitiateLien_PKBalanceFetchError(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-PK-ERR-001", 10000)

	mockPK := &stubPKClient{
		getBalanceErr: fmt.Errorf("PK unavailable"),
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-PK-ERR-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-PK-ERR-001",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "balance")
}

func TestInitiateLien_LegacyMode_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-LEG-SUCC-001", 100000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "1000.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-LEG-SUCC-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-LEG-SUCC-001",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Lien)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)
}

func TestInitiateLien_LegacyMode_InsufficientBalance(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-LEG-INSUF-001", 1000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "10.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-LEG-INSUF-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 500}},
		PaymentOrderReference: "POR-LEG-INSUF-001",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestExecuteWithdrawal_MissingAccountIDDirect(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.ExecuteWithdrawal(ctx, &pb.ExecuteWithdrawalRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestExecuteWithdrawal_MissingAmountDirect(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.ExecuteWithdrawal(ctx, &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-EXEC-001",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestExecuteWithdrawal_WithdrawalNotFound(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.ExecuteWithdrawal(ctx, &pb.ExecuteWithdrawalRequest{
		WithdrawalId: "WTH-NOT-EXIST-001",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestExecuteWithdrawal_NotPending(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	createTestAccountWithBalance(t, ctx, repo, "EW-NPEND-001", 100000)
	account, err := repo.FindByID(ctx, "EW-NPEND-001")
	require.NoError(t, err)

	// Create a withdrawal and mark it as completed
	amt, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	w, err := domain.NewWithdrawal(account.ID(), amt, "WTH-NPEND-REF-001")
	require.NoError(t, err)
	w.Status = domain.WithdrawalStatusCompleted
	require.NoError(t, withdrawalRepo.Create(ctx, w))

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err = svc.ExecuteWithdrawal(ctx, &pb.ExecuteWithdrawalRequest{
		WithdrawalId: "WTH-NPEND-REF-001",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestInitiateWithdrawal_AmountOverflow(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "WTH-OVERFLOW-001", 10000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "100.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// Amount with overflow units
	_, err := svc.InitiateWithdrawal(ctx, &pb.InitiateWithdrawalRequest{
		AccountId: "WTH-OVERFLOW-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 9223372036854775807},
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestInitiateLien_LegacyMode_AccountFrozen(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-FROZEN-001", 10000)

	// Freeze the account
	freezeSvc := &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := freezeSvc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "LIEN-FROZEN-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "Suspicious activity investigation pending",
	})
	require.NoError(t, err)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "100.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err = svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-FROZEN-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-FROZEN-001",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestExecuteLien_SuccessWithStubPK(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-EXEC-001", 100000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "1000.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// First create a lien
	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-EXEC-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-EXEC-001",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Lien)

	// Now execute the lien
	execResp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{
		LienId: resp.Lien.LienId,
	})
	require.NoError(t, err)
	require.NotNil(t, execResp.Lien)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, execResp.Lien.Status)
}

func TestTerminateLien_SuccessWithStubPK(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-TERM-001", 100000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "1000.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// Create a lien
	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-TERM-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-TERM-001",
	})
	require.NoError(t, err)

	// Terminate it
	termResp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: resp.Lien.LienId,
		Reason: "No longer needed",
	})
	require.NoError(t, err)
	require.NotNil(t, termResp.Lien)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, termResp.Lien.Status)
}

func TestRetrieveLien_SuccessWithStubPK(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-RET-001", 100000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "1000.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// Create a lien
	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-RET-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-RET-001",
	})
	require.NoError(t, err)

	// Retrieve it
	retResp, err := svc.RetrieveLien(ctx, &pb.RetrieveLienRequest{
		LienId: resp.Lien.LienId,
	})
	require.NoError(t, err)
	require.NotNil(t, retResp.Lien)
	assert.Equal(t, resp.Lien.LienId, retResp.Lien.LienId)
}

func TestGetActiveAmountBlocks_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-BLOCKS-001", 100000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "1000.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// Create a lien
	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-BLOCKS-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-BLOCKS-001",
	})
	require.NoError(t, err)

	// Get active amount blocks
	blocksResp, err := svc.GetActiveAmountBlocks(ctx, &pb.GetActiveAmountBlocksRequest{
		AccountId: "LIEN-BLOCKS-001",
	})
	require.NoError(t, err)
	assert.Len(t, blocksResp.Blocks, 1)
}

func TestTerminateLien_IdempotentRetry(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-TERM-IDEMP-001", 100000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "1000.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// Create and terminate a lien
	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-TERM-IDEMP-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-TERM-IDEMP-001",
	})
	require.NoError(t, err)

	_, err = svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: resp.Lien.LienId,
		Reason: "No longer needed",
	})
	require.NoError(t, err)

	// Terminate again - should be idempotent
	termResp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: resp.Lien.LienId,
		Reason: "No longer needed (retry)",
	})
	require.NoError(t, err)
	require.NotNil(t, termResp.Lien)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, termResp.Lien.Status)
}

func TestExecuteLien_IdempotentRetry(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-EXEC-IDEMP-001", 100000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "1000.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// Create and execute a lien
	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-EXEC-IDEMP-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-EXEC-IDEMP-001",
	})
	require.NoError(t, err)

	_, err = svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{
		LienId: resp.Lien.LienId,
	})
	require.NoError(t, err)

	// Execute again - should be idempotent
	execResp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{
		LienId: resp.Lien.LienId,
	})
	require.NoError(t, err)
	require.NotNil(t, execResp.Lien)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, execResp.Lien.Status)
}

func TestInitiateLien_LegacyMode_Idempotent(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "LIEN-INIT-IDEMP-001", 100000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "1000.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	// Create a lien
	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-INIT-IDEMP-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-INIT-IDEMP-001",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Lien)

	// Create again with same POR - should be idempotent
	resp2, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             "LIEN-INIT-IDEMP-001",
		Amount:                &commonpb.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50}},
		PaymentOrderReference: "POR-INIT-IDEMP-001",
	})
	require.NoError(t, err)
	require.NotNil(t, resp2.Lien)
	assert.Equal(t, resp.Lien.LienId, resp2.Lien.LienId)
}

func TestDatabaseHealthChecker_ContextTimeout(t *testing.T) {
	db := openSharedDB(t)
	repo := persistence.NewRepository(db)

	// Create checker with very short timeout and use already-cancelled context
	checker := NewDatabaseHealthChecker(repo, 1*time.Nanosecond)
	// Use a pre-cancelled context to trigger the timeout path
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	result := checker.Check(ctx)
	assert.Equal(t, health.StatusUnhealthy, result.Status)
	assert.Contains(t, result.Message, "database check")
}

// =============================================================================
// InitiateCurrentAccount: instrument lookup error branches (lines 59-81)
// =============================================================================

func TestInitiateCurrentAccount_InstrumentNotFoundRefData(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)

	// Mock instrument getter returns ErrNotFound for "FAKECUR"
	svc := &Service{
		repo:             repo,
		instrumentGetter: &mockInstrumentGetter{instruments: map[string]*cache.CachedInstrument{}},
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.InitiateCurrentAccount(ctx, &pb.InitiateCurrentAccountRequest{
		InstrumentCode: "FAKECUR",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "unknown instrument_code")
}

func TestInitiateCurrentAccount_InstrumentLookupTransientError(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)

	// Mock instrument getter returns a generic error (transient)
	svc := &Service{
		repo:             repo,
		instrumentGetter: &mockInstrumentGetter{err: fmt.Errorf("connection refused")},
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.InitiateCurrentAccount(ctx, &pb.InitiateCurrentAccountRequest{
		InstrumentCode: "GBP",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
	assert.Contains(t, st.Message(), "instrument lookup failed")
}

func TestInitiateCurrentAccount_InstrumentLookupContextCanceled(t *testing.T) {
	db, _, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)

	// Create a cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	tid := uniqueTenantID()
	cancelledCtx = tenant.WithTenant(cancelledCtx, tid)

	svc := &Service{
		repo:             repo,
		instrumentGetter: &mockInstrumentGetter{err: context.Canceled},
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.InitiateCurrentAccount(cancelledCtx, &pb.InitiateCurrentAccountRequest{
		InstrumentCode: "GBP",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Canceled, st.Code())
}

func TestInitiateCurrentAccount_InstrumentLookupTimeout(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)

	svc := &Service{
		repo:             repo,
		instrumentGetter: &mockInstrumentGetter{err: context.DeadlineExceeded},
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.InitiateCurrentAccount(ctx, &pb.InitiateCurrentAccountRequest{
		InstrumentCode: "GBP",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.DeadlineExceeded, st.Code())
	assert.Contains(t, st.Message(), "timed out")
}

// =============================================================================
// stubPKClient: minimal mock for PositionKeepingClient used in unit tests
// =============================================================================

type stubPKClient struct {
	getBalanceResp *positionkeepingv1.GetAccountBalanceResponse
	getBalanceErr  error
	releaseResp    *positionkeepingv1.ReleaseReservationResponse
	releaseErr     error
}

func (s *stubPKClient) InitiateFinancialPositionLog(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *stubPKClient) UpdateFinancialPositionLog(_ context.Context, _ *positionkeepingv1.UpdateFinancialPositionLogRequest) (*positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *stubPKClient) RetrieveFinancialPositionLog(_ context.Context, _ *positionkeepingv1.RetrieveFinancialPositionLogRequest) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *stubPKClient) BulkImportTransactions(_ context.Context, _ *positionkeepingv1.BulkImportTransactionsRequest) (*positionkeepingv1.BulkImportTransactionsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *stubPKClient) ListFinancialPositionLogs(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *stubPKClient) GetAccountBalance(_ context.Context, _ *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	return s.getBalanceResp, s.getBalanceErr
}

func (s *stubPKClient) GetAccountBalances(_ context.Context, _ *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *stubPKClient) ReleaseReservation(_ context.Context, _ *positionkeepingv1.ReleaseReservationRequest) (*positionkeepingv1.ReleaseReservationResponse, error) {
	return s.releaseResp, s.releaseErr
}
func (s *stubPKClient) Close() error { return nil }

// =============================================================================
// NewHealthChecker: with optional health clients
// =============================================================================

func TestNewHealthChecker_WithOptionalClients(t *testing.T) {
	db := openSharedDB(t)
	repo := persistence.NewRepository(db)

	pkHealthClient := &stubGRPCHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING},
	}
	faHealthClient := &stubGRPCHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING},
	}

	checker, err := NewHealthChecker(HealthCheckerConfig{
		Repository:                      repo,
		PositionKeepingHealthClient:     pkHealthClient,
		FinancialAccountingHealthClient: faHealthClient,
	})
	require.NoError(t, err)
	assert.NotNil(t, checker)
	assert.Equal(t, "current-account", checker.serviceName)
}
