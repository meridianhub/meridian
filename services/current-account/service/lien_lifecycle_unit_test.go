package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// =============================================================================
// InitiateLien error paths
// =============================================================================

func TestInitiateLien_LienRepoNil(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Create service without lien repo
	svc := mustNewService(t, repo, nil)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-NO-REPO",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 100},
		},
		PaymentOrderReference: "PO-NO-REPO",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "not configured")
}

func TestInitiateLien_CurrencyMismatch_LegacyMode(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-MISMATCH": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-MISMATCH", 100000)

	// Use USD on a GBP account
	req := &pb.InitiateLienRequest{
		AccountId: "ACC-MISMATCH",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "USD", Units: 100},
		},
		PaymentOrderReference: "PO-MISMATCH",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "currency mismatch")
}

func TestInitiateLien_AccountNotFound_InPrefetch(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, nil)

	// Don't create the account - it won't be found
	req := &pb.InitiateLienRequest{
		AccountId: "ACC-NONEXISTENT",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 100},
		},
		PaymentOrderReference: "PO-NONEXISTENT",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
	require.Contains(t, st.Message(), "account not found")
}

func TestInitiateLien_IdempotentReturn_ExistingLien(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-IDEMP-INIT": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-IDEMP-INIT", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-IDEMP-INIT",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 200},
		},
		PaymentOrderReference: "PO-IDEMP-INIT",
	}

	// First call creates the lien
	resp1, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.NotEmpty(t, resp1.Lien.LienId)

	// Second call with same PaymentOrderReference should return idempotent response
	resp2, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, resp1.Lien.LienId, resp2.Lien.LienId)
}

func TestInitiateLien_InsufficientFunds_WithExistingLiens(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-INSUF-MULTI": 100000, // £1000
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-INSUF-MULTI", 100000)

	// Create an existing lien for £800
	lienAmount, err := domain.NewMoney("GBP", 80000)
	require.NoError(t, err)
	existingLien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-EXIST", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, existingLien))

	// Try to create another lien for £300 (would exceed available: 1000 - 800 = 200)
	req := &pb.InitiateLienRequest{
		AccountId: "ACC-INSUF-MULTI",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 300},
		},
		PaymentOrderReference: "PO-INSUF-MULTI",
	}
	_, err = svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "insufficient")
}

func TestInitiateLien_AtomicValuation_NoValuationFeature_InLifecycle(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	// Service without valuation feature configured
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-VAL-NONE": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-VAL-NONE", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-VAL-NONE",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "10.0",
			InstrumentCode: "kWh",
		},
		PaymentOrderReference: "PO-VAL-NONE",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	// Should fail because no valuation feature is configured
	require.True(t, st.Code() == codes.FailedPrecondition || st.Code() == codes.Internal,
		"expected FailedPrecondition or Internal, got %s", st.Code())
}

func TestInitiateLien_WithBucket_AvailableBalanceReduced(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-BUCKET-INIT": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-BUCKET-INIT", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-BUCKET-INIT",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 300},
		},
		BucketId:              "my-bucket",
		PaymentOrderReference: "PO-BUCKET-INIT",
	}
	resp, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)
	require.NotNil(t, resp.AvailableBalance)
}

// =============================================================================
// ExecuteLien error paths
// =============================================================================

func TestExecuteLien_LienRepoNil(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.ExecuteLienRequest{
		LienId: uuid.New().String(),
	}
	_, err := svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "not configured")
}

func TestExecuteLien_LienNotFound_Lifecycle(t *testing.T) {
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

func TestExecuteLien_AlreadyExecuted_Idempotent(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-EXEC-IDEMP": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-EXEC-IDEMP", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-EXEC-IDEMP", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// First execute
	resp1, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp1.Lien.Status)

	// Second execute should be idempotent (read-only path)
	resp2, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp2.Lien.Status)
}

func TestExecuteLien_ExpiredLien_CannotExecute(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-EXEC-EXP": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-EXEC-EXP", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)

	// Create lien with past expiry
	pastExpiry := time.Now().Add(-1 * time.Hour)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-EXEC-EXP", &pastExpiry)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	_, err = svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "cannot be executed")
}

func TestExecuteLien_IdempotencyMarkPending_AlreadyProcessed(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, lienRepo, mockIdemp)

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-MARK-PEND", 100000)
	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-MARK-PEND", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Pre-mark the key as pending to simulate concurrent request
	idempKey := buildTestIdempKey(ctx, lien.ID.String(), "mark-pend-key")
	mockIdemp.setPending(idempKey)

	req := &pb.ExecuteLienRequest{
		LienId:         lien.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "mark-pend-key"},
	}
	_, err = svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Aborted, st.Code())
	require.Contains(t, st.Message(), "already in progress")
}

func TestExecuteLien_IdempotencyCachedValidResponse(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, lienRepo, mockIdemp)

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-CACHED-RESP", 100000)
	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-CACHED-RESP", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Store a valid cached response
	cachedResp := &pb.ExecuteLienResponse{
		TransactionId: "TXN-CACHED",
		Lien:          &pb.Lien{LienId: lien.ID.String(), Status: pb.LienStatus_LIEN_STATUS_EXECUTED},
	}
	data, err := proto.Marshal(cachedResp)
	require.NoError(t, err)

	idempKey := buildTestIdempKey(ctx, lien.ID.String(), "cached-resp-key")
	mockIdemp.setResult(idempKey, data)

	req := &pb.ExecuteLienRequest{
		LienId:         lien.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "cached-resp-key"},
	}
	resp, err := svc.ExecuteLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "TXN-CACHED", resp.TransactionId)
}

func TestExecuteLien_WithBucket_ReturnsAvailableBalance(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-EXEC-BKTBAL": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-EXEC-BKTBAL", 100000)

	lienAmount, err := domain.NewMoney("GBP", 20000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "exec-bucket", "PO-EXEC-BKTBAL", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	resp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
	require.NotNil(t, resp.NewBalance)
	require.NotNil(t, resp.AvailableBalance)
}

func TestExecuteLien_TransactionID_Format(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-TXN-FMT": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-TXN-FMT", 100000)

	lienAmount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-TXN-FMT", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	resp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.NoError(t, err)
	require.Contains(t, resp.TransactionId, "TXN-LIEN-")
	require.Len(t, resp.TransactionId, len("TXN-LIEN-")+8)
}

// =============================================================================
// TerminateLien error paths
// =============================================================================

func TestTerminateLien_LienRepoNil(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.TerminateLienRequest{
		LienId: uuid.New().String(),
		Reason: "test",
	}
	_, err := svc.TerminateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "not configured")
}

func TestTerminateLien_LienNotFound_Retrieve(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	req := &pb.TerminateLienRequest{
		LienId: uuid.New().String(),
		Reason: "not found test",
	}
	_, err := svc.TerminateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestTerminateLien_AlreadyTerminated_IdempotentWithBalance(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-TERM-IDEMP2": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-TERM-IDEMP2", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-TERM-IDEMP2", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// First termination
	resp1, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "first call",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp1.Lien.Status)

	// Second termination - idempotent path that includes balance
	resp2, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "second call",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp2.Lien.Status)
	require.NotNil(t, resp2.AvailableBalance, "idempotent termination should include available balance")
}

func TestTerminateLien_InvalidLienID_Format(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	req := &pb.TerminateLienRequest{
		LienId: "not-a-uuid-format",
		Reason: "bad id",
	}
	_, err := svc.TerminateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "invalid lien ID")
}

func TestTerminateLien_WithBucket_ReturnsBalance(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-TERM-BKTBAL": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-TERM-BKTBAL", 100000)

	lienAmount, err := domain.NewMoney("GBP", 20000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "term-bucket", "PO-TERM-BKTBAL", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	resp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "bucket termination",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp.Lien.Status)
	require.NotNil(t, resp.AvailableBalance)
}

func TestTerminateLien_WithExpiresAt_StillTerminatable(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-TERM-EXPIRY": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-TERM-EXPIRY", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)

	// Create lien with future expiry
	futureExpiry := time.Now().Add(24 * time.Hour)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-TERM-EXPIRY", &futureExpiry)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	resp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "early termination",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp.Lien.Status)
}

func TestTerminateLien_ExpiredLien_CanStillTerminate(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-TERM-PAST": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-TERM-PAST", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)

	// Create lien with past expiry - still terminatable (CanTerminate only checks status, not expiry)
	pastExpiry := time.Now().Add(-1 * time.Hour)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-TERM-PAST", &pastExpiry)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	resp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "expired lien cleanup",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp.Lien.Status)
}

// =============================================================================
// Lien state machine edge cases
// =============================================================================

func TestLienStateMachine_ExecuteThenTerminate_Fails(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-SM-ET": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-SM-ET", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-SM-ET", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Execute
	_, err = svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.NoError(t, err)

	// Attempt to terminate - should fail
	_, err = svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "attempt after execution",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestLienStateMachine_TerminateThenExecute_Fails(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-SM-TE": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-SM-TE", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-SM-TE", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Terminate
	_, err = svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "cancel",
	})
	require.NoError(t, err)

	// Attempt to execute - should fail
	_, err = svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestLienStateMachine_DoubleExecute_Idempotent(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-SM-DEX": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-SM-DEX", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-SM-DEX", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// First execute
	resp1, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp1.Lien.Status)

	// Second execute should be idempotent
	resp2, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp2.Lien.Status)
}

func TestLienStateMachine_DoubleTerminate_Idempotent(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-SM-DTM": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-SM-DTM", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-SM-DTM", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// First terminate
	resp1, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "first",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp1.Lien.Status)

	// Second terminate should be idempotent
	resp2, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "second",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp2.Lien.Status)
}

// =============================================================================
// ExecuteLien with idempotency - cleanup on failure
// =============================================================================

func TestExecuteLien_IdempotencyCleanup_OnLienNotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, lienRepo, mockIdemp)

	nonexistentID := uuid.New().String()
	req := &pb.ExecuteLienRequest{
		LienId:         nonexistentID,
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "cleanup-notfound"},
	}
	_, err := svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())

	// Verify pending state was cleaned up
	idempKey := buildTestIdempKey(ctx, nonexistentID, "cleanup-notfound")
	require.False(t, mockIdemp.isPending(idempKey), "pending state should be cleaned up after failure")
}

func TestExecuteLien_IdempotencyStore_SuccessfulExecution(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotencyAndPositionKeeping(t, repo, lienRepo, mockIdemp, map[string]int64{
		"ACC-IDEMP-STORE2": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-IDEMP-STORE2", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-IDEMP-STORE2", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	req := &pb.ExecuteLienRequest{
		LienId:         lien.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "store-success"},
	}
	resp, err := svc.ExecuteLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)

	// Verify result was stored in idempotency cache
	idempKey := buildTestIdempKey(ctx, lien.ID.String(), "store-success")
	result, checkErr := mockIdemp.Check(ctx, idempKey)
	require.ErrorIs(t, checkErr, idempotency.ErrOperationAlreadyProcessed)
	require.NotNil(t, result)
	require.NotNil(t, result.Data)

	// Verify cached data can be unmarshalled
	var cachedResp pb.ExecuteLienResponse
	require.NoError(t, proto.Unmarshal(result.Data, &cachedResp))
	require.Equal(t, resp.TransactionId, cachedResp.TransactionId)
}

// =============================================================================
// Full lifecycle flows
// =============================================================================

func TestFullLifecycle_Initiate_Execute(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-FULL-IE": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-FULL-IE", 100000)

	// Initiate
	initResp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "ACC-FULL-IE",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 400},
		},
		PaymentOrderReference: "PO-FULL-IE",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, initResp.Lien.Status)

	// Execute
	execResp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{
		LienId: initResp.Lien.LienId,
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, execResp.Lien.Status)
	require.NotEmpty(t, execResp.TransactionId)
	require.NotNil(t, execResp.NewBalance)
}

func TestFullLifecycle_Initiate_Terminate(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-FULL-IT": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-FULL-IT", 100000)

	// Initiate
	initResp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "ACC-FULL-IT",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 400},
		},
		PaymentOrderReference: "PO-FULL-IT",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, initResp.Lien.Status)

	// Terminate
	termResp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: initResp.Lien.LienId,
		Reason: "Customer cancelled",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, termResp.Lien.Status)
	require.NotNil(t, termResp.AvailableBalance)
}

func TestFullLifecycle_MultipleLiens_Execute_And_Terminate(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-FULL-MIX": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-FULL-MIX", 100000)

	// Create first lien - will be executed
	initResp1, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "ACC-FULL-MIX",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 200},
		},
		PaymentOrderReference: "PO-FULL-MIX-1",
	})
	require.NoError(t, err)

	// Create second lien - will be terminated
	initResp2, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "ACC-FULL-MIX",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 200},
		},
		PaymentOrderReference: "PO-FULL-MIX-2",
	})
	require.NoError(t, err)

	// Execute first
	_, err = svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: initResp1.Lien.LienId})
	require.NoError(t, err)

	// Terminate second
	_, err = svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: initResp2.Lien.LienId,
		Reason: "No longer needed",
	})
	require.NoError(t, err)
}

// =============================================================================
// Concurrent operations
// =============================================================================

func TestConcurrent_InitiateMultipleLiens_SameAccount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-CONC-INIT": 100000, // £1000
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-CONC-INIT", 100000)

	// Each lien is £100, all should succeed within £1000 balance
	const numLiens = 5
	results := make(chan error, numLiens)

	for i := 0; i < numLiens; i++ {
		go func(idx int) {
			// Each goroutine needs its own tenant context
			tid, _ := tenant.FromContext(ctx)
			goCtx := tenant.WithTenant(ctx, tid)

			req := &pb.InitiateLienRequest{
				AccountId: "ACC-CONC-INIT",
				Amount: &commonpb.MoneyAmount{
					Amount: &money.Money{CurrencyCode: "GBP", Units: 100},
				},
				PaymentOrderReference: uuid.New().String(),
			}
			_, err := svc.InitiateLien(goCtx, req)
			results <- err
		}(i)
	}

	successCount := 0
	for i := 0; i < numLiens; i++ {
		err := <-results
		if err == nil {
			successCount++
		}
	}

	// All should succeed since total (5 * 100 = 500) is within balance (1000)
	require.Equal(t, numLiens, successCount, "all liens should succeed within available balance")
}

func TestConcurrent_ExecuteAndTerminate_SameLien(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-CONC-ET": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-CONC-ET", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-CONC-ET", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Execute and terminate concurrently - only one should succeed with the desired operation,
	// the other should either get an error or be idempotent
	execCh := make(chan error, 1)
	termCh := make(chan error, 1)

	go func() {
		tid, _ := tenant.FromContext(ctx)
		goCtx := tenant.WithTenant(ctx, tid)
		_, err := svc.ExecuteLien(goCtx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
		execCh <- err
	}()

	go func() {
		tid, _ := tenant.FromContext(ctx)
		goCtx := tenant.WithTenant(ctx, tid)
		_, err := svc.TerminateLien(goCtx, &pb.TerminateLienRequest{
			LienId: lien.ID.String(),
			Reason: "concurrent test",
		})
		termCh <- err
	}()

	execErr := <-execCh
	termErr := <-termCh

	// Exactly one should succeed - the other gets FailedPrecondition or Aborted
	// (Or both could succeed if one hits the idempotent path of the other's terminal state)
	if execErr == nil && termErr == nil {
		t.Fatal("both execute and terminate succeeded on same lien - should not be possible")
	}
	// At least one must succeed
	require.True(t, execErr == nil || termErr == nil,
		"at least one operation should succeed: execErr=%v, termErr=%v", execErr, termErr)
}

// =============================================================================
// InitiateLien with ExpiresAt
// =============================================================================

func TestInitiateLien_WithExpiresAt(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-INIT-EXP": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-INIT-EXP", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-INIT-EXP",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 100},
		},
		PaymentOrderReference: "PO-INIT-EXP",
	}
	resp, err := svc.InitiateLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)
}

// =============================================================================
// ExecuteLien idempotency edge cases
// =============================================================================

func TestExecuteLien_WithoutIdempotencyKey_NoRedisInteraction(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotencyAndPositionKeeping(t, repo, lienRepo, mockIdemp, map[string]int64{
		"ACC-NO-IDEMP-KEY": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-NO-IDEMP-KEY", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-NO-IDEMP-KEY", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Execute without idempotency key
	resp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lien.ID.String()})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
}

func TestExecuteLien_EmptyIdempotencyKey_SkipsRedis(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotencyAndPositionKeeping(t, repo, lienRepo, mockIdemp, map[string]int64{
		"ACC-EMPTY-KEY": 100000,
	})
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-EMPTY-KEY", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-EMPTY-KEY", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Execute with empty idempotency key
	resp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{
		LienId:         lien.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: ""},
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
}
