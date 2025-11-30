package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/internal/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	"github.com/meridianhub/meridian/internal/platform/testdb"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func setupLienTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	return testdb.SetupPostgres(t, []interface{}{
		&persistence.CurrentAccountEntity{},
		&persistence.LienEntity{},
	})
}

func createTestAccountWithBalance(t *testing.T, repo *persistence.Repository, accountID string, balanceCents int64) *domain.CurrentAccount {
	t.Helper()
	account, err := domain.NewCurrentAccount(accountID, "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)

	if balanceCents > 0 {
		depositAmount, err := domain.NewMoney("GBP", balanceCents)
		require.NoError(t, err)
		require.NoError(t, account.Deposit(depositAmount))
	}

	require.NoError(t, repo.Save(account))
	return account
}

func TestInitiateLien_Success(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	// Create account with £1000 balance
	createTestAccountWithBalance(t, repo, "ACC-LIEN-001", 100000) // 100000 cents = £1000

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

	resp, err := svc.InitiateLien(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp.Lien)
	require.NotEmpty(t, resp.Lien.LienId)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)
	require.Equal(t, "PO-123", resp.Lien.PaymentOrderReference)

	// Available balance should be £500 (£1000 - £500 lien)
	require.Equal(t, int64(500), resp.AvailableBalance.Amount.Units)
}

func TestInitiateLien_InsufficientFunds(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	// Create account with £100 balance
	createTestAccountWithBalance(t, repo, "ACC-LIEN-002", 10000) // £100

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

	_, err := svc.InitiateLien(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "insufficient available balance")
}

func TestInitiateLien_AccountNotFound(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

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

	_, err := svc.InitiateLien(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestInitiateLien_CurrencyMismatch(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	// Create GBP account
	createTestAccountWithBalance(t, repo, "ACC-LIEN-003", 100000)

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

	_, err := svc.InitiateLien(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "currency")
}

func TestExecuteLien_Success(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, repo, "ACC-LIEN-004", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID, lienAmount, "PO-127", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(lien))

	// Execute the lien
	req := &pb.ExecuteLienRequest{
		LienId: lien.ID.String(),
	}

	resp, err := svc.ExecuteLien(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
	require.NotEmpty(t, resp.TransactionId)

	// Balance should be £500 (£1000 - £500 debited)
	require.Equal(t, int64(500), resp.NewBalance.Amount.Units)
}

func TestExecuteLien_Idempotent(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, repo, "ACC-LIEN-005", 100000)

	// Create and execute a lien
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID, lienAmount, "PO-128", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(lien))

	req := &pb.ExecuteLienRequest{LienId: lien.ID.String()}

	// First execution
	resp1, err := svc.ExecuteLien(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp1.Lien.Status)

	// Second execution should be idempotent
	resp2, err := svc.ExecuteLien(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp2.Lien.Status)
	require.Equal(t, resp1.TransactionId, resp2.TransactionId)
}

func TestExecuteLien_NotFound(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	req := &pb.ExecuteLienRequest{
		LienId: uuid.New().String(),
	}

	_, err := svc.ExecuteLien(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestTerminateLien_Success(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, repo, "ACC-LIEN-006", 100000)

	// Create lien for £500
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID, lienAmount, "PO-129", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(lien))

	// Terminate the lien
	req := &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "Payment cancelled",
	}

	resp, err := svc.TerminateLien(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp.Lien.Status)

	// Available balance should be restored to £1000
	require.Equal(t, int64(1000), resp.AvailableBalance.Amount.Units)
}

func TestTerminateLien_Idempotent(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	// Create account with £1000 balance
	account := createTestAccountWithBalance(t, repo, "ACC-LIEN-007", 100000)

	// Create lien
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID, lienAmount, "PO-130", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(lien))

	req := &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "Cancelled",
	}

	// First termination
	resp1, err := svc.TerminateLien(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp1.Lien.Status)

	// Second termination should be idempotent
	resp2, err := svc.TerminateLien(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp2.Lien.Status)
}

func TestRetrieveLien_Success(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	// Create account
	account := createTestAccountWithBalance(t, repo, "ACC-LIEN-008", 100000)

	// Create lien
	lienAmount, err := domain.NewMoney("GBP", 25000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID, lienAmount, "PO-131", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(lien))

	// Retrieve the lien
	req := &pb.RetrieveLienRequest{
		LienId: lien.ID.String(),
	}

	resp, err := svc.RetrieveLien(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, lien.ID.String(), resp.Lien.LienId)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)
	require.Equal(t, "PO-131", resp.Lien.PaymentOrderReference)
}

func TestRetrieveLien_NotFound(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	req := &pb.RetrieveLienRequest{
		LienId: uuid.New().String(),
	}

	_, err := svc.RetrieveLien(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestLienOperations_LienRepoNotConfigured(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil) // No lien repo

	t.Run("InitiateLien", func(t *testing.T) {
		req := &pb.InitiateLienRequest{
			AccountId: "ACC-001",
			Amount: &commonpb.MoneyAmount{
				Amount: &money.Money{CurrencyCode: "GBP", Units: 100},
			},
			PaymentOrderReference: "PO-001",
		}
		_, err := svc.InitiateLien(context.Background(), req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("ExecuteLien", func(t *testing.T) {
		req := &pb.ExecuteLienRequest{LienId: uuid.New().String()}
		_, err := svc.ExecuteLien(context.Background(), req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("TerminateLien", func(t *testing.T) {
		req := &pb.TerminateLienRequest{LienId: uuid.New().String()}
		_, err := svc.TerminateLien(context.Background(), req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("RetrieveLien", func(t *testing.T) {
		req := &pb.RetrieveLienRequest{LienId: uuid.New().String()}
		_, err := svc.RetrieveLien(context.Background(), req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.FailedPrecondition, st.Code())
	})
}

func TestMultipleLiens_AvailableBalanceCalculation(t *testing.T) {
	db, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := NewService(repo, lienRepo)

	// Create account with £1000 balance
	createTestAccountWithBalance(t, repo, "ACC-LIEN-MULTI", 100000)

	// Create first lien for £300
	req1 := &pb.InitiateLienRequest{
		AccountId: "ACC-LIEN-MULTI",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 300},
		},
		PaymentOrderReference: "PO-MULTI-1",
	}
	resp1, err := svc.InitiateLien(context.Background(), req1)
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
	resp2, err := svc.InitiateLien(context.Background(), req2)
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
	_, err = svc.InitiateLien(context.Background(), req3)
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
	resp4, err := svc.InitiateLien(context.Background(), req4)
	require.NoError(t, err)
	require.Equal(t, int64(100), resp4.AvailableBalance.Amount.Units) // £500 - £400 = £100
}
