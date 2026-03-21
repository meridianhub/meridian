package service

import (
	"context"
	"encoding/json"
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
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- InitiateLien error path tests ---

func TestInitiateLien_NilAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-NIL-AMT": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-NIL-AMT", 100000)

	// nil Amount field (legacy mode with no input)
	req := &pb.InitiateLienRequest{
		AccountId:             "ACC-NIL-AMT",
		Amount:                nil,
		PaymentOrderReference: "PO-NIL-AMT",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "amount is required")
}

func TestInitiateLien_NilMoneyInAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-NIL-MONEY": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-NIL-MONEY", 100000)

	// Amount with nil Money
	req := &pb.InitiateLienRequest{
		AccountId:             "ACC-NIL-MONEY",
		Amount:                &commonpb.MoneyAmount{Amount: nil},
		PaymentOrderReference: "PO-NIL-MONEY",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "amount is required")
}

func TestInitiateLien_ZeroAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-ZERO-AMT": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-ZERO-AMT", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-ZERO-AMT",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 0, Nanos: 0},
		},
		PaymentOrderReference: "PO-ZERO-AMT",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "positive")
}

func TestInitiateLien_NegativeAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-NEG-AMT": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-NEG-AMT", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-NEG-AMT",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: -100, Nanos: 0},
		},
		PaymentOrderReference: "PO-NEG-AMT",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestInitiateLien_AccountNotActive_Frozen(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-FROZEN-LIEN": 100000,
	})

	// Create and freeze the account
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-FROZEN-LIEN", 100000)
	frozenAccount, err := account.Freeze("Account frozen for investigation - compliance requirement")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, frozenAccount))

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-FROZEN-LIEN",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 100},
		},
		PaymentOrderReference: "PO-FROZEN",
	}
	_, err = svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "not active")
}

// --- ExecuteLien error path tests ---

func TestExecuteLien_InvalidLienID_Lifecycle(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	req := &pb.ExecuteLienRequest{
		LienId: "not-a-uuid",
	}
	_, err := svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "invalid lien ID")
}

func TestExecuteLien_LienAlreadyTerminated(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-EXEC-TERM": 100000,
	})

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-EXEC-TERM", 100000)

	// Create lien and terminate it
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-EXEC-TERM", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Terminate first
	_, err = svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "Cancelled before execution",
	})
	require.NoError(t, err)

	// Try to execute a terminated lien
	_, err = svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{
		LienId: lien.ID.String(),
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "cannot be executed")
}

// --- TerminateLien error path tests ---

func TestTerminateLien_InvalidLienID_Lifecycle(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	req := &pb.TerminateLienRequest{
		LienId: "not-a-valid-uuid",
	}
	_, err := svc.TerminateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "invalid lien ID")
}

func TestTerminateLien_LienNotFound_Lifecycle(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewService(t, repo, lienRepo)

	req := &pb.TerminateLienRequest{
		LienId: uuid.New().String(),
	}
	_, err := svc.TerminateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestTerminateLien_AlreadyExecuted_CannotTerminate(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-TERM-EXEC": 100000,
	})

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-TERM-EXEC", 100000)

	// Create lien and execute it
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-TERM-EXEC", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Execute first
	_, err = svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{
		LienId: lien.ID.String(),
	})
	require.NoError(t, err)

	// Try to terminate an executed lien
	_, err = svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "Trying to terminate after execution",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "cannot be terminated")
}

func TestTerminateLien_DefaultReason(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-DEF-REASON": 100000,
	})

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-DEF-REASON", 100000)

	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-DEF-REASON", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Terminate without a reason (should use default)
	resp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "", // empty -> uses default
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp.Lien.Status)
}

// --- ExecuteLien idempotency edge cases ---

func TestExecuteLien_IdempotencyCheckError_Lifecycle(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	mockIdemp.checkErr = errTestInternal // non-standard error
	svc := mustNewServiceWithIdempotency(t, repo, lienRepo, mockIdemp)

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-IDEMP-ERR", 100000)

	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-IDEMP-ERR", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	req := &pb.ExecuteLienRequest{
		LienId:         lien.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "idemp-check-err-lc"},
	}
	_, err = svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Internal, st.Code())
	require.Contains(t, st.Message(), "idempotency")
}

func TestExecuteLien_IdempotencyStoreError_DoesNotFailExecution(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	mockIdemp.storeErr = errTestInternal
	svc := mustNewServiceWithIdempotencyAndPositionKeeping(t, repo, lienRepo, mockIdemp, map[string]int64{
		"ACC-IDEMP-STORE": 100000,
	})

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-IDEMP-STORE", 100000)

	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-IDEMP-STORE", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	req := &pb.ExecuteLienRequest{
		LienId:         lien.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "idemp-store-err-lc"},
	}
	resp, err := svc.ExecuteLien(ctx, req)
	require.NoError(t, err, "store error should not fail the execution")
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
}

func TestExecuteLien_IdempotencyDeleteError_DoesNotPanic(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	mockIdemp.deleteErr = errTestInternal
	svc := mustNewServiceWithIdempotency(t, repo, lienRepo, mockIdemp)

	req := &pb.ExecuteLienRequest{
		LienId:         uuid.New().String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "idemp-del-err-lc"},
	}
	_, err := svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestExecuteLien_IdempotencyCacheCorruptData(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	mockIdemp := newLienMockIdempotencyService()
	svc := mustNewServiceWithIdempotencyAndPositionKeeping(t, repo, lienRepo, mockIdemp, map[string]int64{
		"ACC-IDEMP-CORRUPT": 100000,
	})

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-IDEMP-CORRUPT", 100000)

	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-IDEMP-CORRUPT", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// Store corrupt data in the cache
	idempKey := buildTestIdempKey(ctx, lien.ID.String(), "idemp-corrupt-data-lc")
	mockIdemp.setResult(idempKey, []byte("not-valid-protobuf"))

	// Should fall through to normal execution since unmarshal fails
	req := &pb.ExecuteLienRequest{
		LienId:         lien.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "idemp-corrupt-data-lc"},
	}
	resp, err := svc.ExecuteLien(ctx, req)
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
}

// --- Valuation path tests for InitiateLien ---

func TestInitiateLien_AtomicValuation_InvalidInputAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-VAL-INV": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-VAL-INV", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-VAL-INV",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "not-a-number",
			InstrumentCode: "kWh",
		},
		PaymentOrderReference: "PO-VAL-INV",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "invalid input amount")
}

func TestInitiateLien_AtomicValuation_NonPositiveInputAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-VAL-NEG": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-VAL-NEG", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-VAL-NEG",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "-10.00",
			InstrumentCode: "kWh",
		},
		PaymentOrderReference: "PO-VAL-NEG",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "positive")
}

func TestInitiateLien_AtomicValuation_ZeroInputAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-VAL-ZERO": 100000,
	})
	createTestAccountWithBalance(t, ctx, repo, "ACC-VAL-ZERO", 100000)

	req := &pb.InitiateLienRequest{
		AccountId: "ACC-VAL-ZERO",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "0",
			InstrumentCode: "kWh",
		},
		PaymentOrderReference: "PO-VAL-ZERO",
	}
	_, err := svc.InitiateLien(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "positive")
}

// --- Helper tests for lien_helpers.go ---

func TestCheckBasisDrift_NoAnalysis_Lifecycle(t *testing.T) {
	db, _, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	lien := &domain.Lien{
		ID:                uuid.New(),
		ValuationAnalysis: nil,
	}
	svc.checkBasisDrift(lien)
}

func TestCheckBasisDrift_InvalidTimestamp(t *testing.T) {
	db, _, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	analysis, _ := json.Marshal(map[string]string{"knowledgeAt": "not-a-timestamp"})
	lien := &domain.Lien{
		ID:                uuid.New(),
		ValuationAnalysis: analysis,
	}
	svc.checkBasisDrift(lien)
}

func TestCheckBasisDrift_RecentAnalysis(t *testing.T) {
	db, _, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	analysis, _ := json.Marshal(map[string]string{
		"knowledgeAt": time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	})
	lien := &domain.Lien{
		ID:                uuid.New(),
		ValuationAnalysis: analysis,
	}
	svc.checkBasisDrift(lien)
}

func TestCheckBasisDrift_StaleAnalysis(t *testing.T) {
	db, _, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	// 60 days old - should trigger warning (threshold is 30 days)
	analysis, _ := json.Marshal(map[string]string{
		"knowledgeAt": time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339),
	})
	lien := &domain.Lien{
		ID:                uuid.New(),
		ValuationAnalysis: analysis,
	}
	svc.checkBasisDrift(lien)
}

func TestToLienProto_WithValuationFields(t *testing.T) {
	lien := &domain.Lien{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.LienStatusActive,
		ReservedQuantity: &domain.InstrumentAmount{
			Amount:         decimal.NewFromFloat(100.0),
			InstrumentCode: "kWh",
		},
		ValuedAmount: &domain.InstrumentAmount{
			Amount:         decimal.NewFromFloat(500.0),
			InstrumentCode: "GBP",
		},
		PaymentOrderReference: "PO-PROTO-001",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien.Amount = lienAmount

	pbLien := toLienProto(lien)
	require.NotNil(t, pbLien.ReservedQuantity)
	require.Equal(t, "kWh", pbLien.ReservedQuantity.InstrumentCode)
	require.Equal(t, "100", pbLien.ReservedQuantity.Amount)
	require.NotNil(t, pbLien.ValuedAmount)
	require.Equal(t, "GBP", pbLien.ValuedAmount.InstrumentCode)
	require.Equal(t, "500", pbLien.ValuedAmount.Amount)
}

func TestToLienProto_WithExpiresAt(t *testing.T) {
	expiry := time.Now().Add(24 * time.Hour)
	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		Status:                domain.LienStatusActive,
		PaymentOrderReference: "PO-EXPIRY",
		ExpiresAt:             &expiry,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	lienAmount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)
	lien.Amount = lienAmount

	block := toAmountBlockProto(lien)
	require.NotNil(t, block.ExpiresAt)
}

func TestMapLienStatusToProto_AllStatuses(t *testing.T) {
	require.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, mapLienStatusToProto(domain.LienStatusActive))
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, mapLienStatusToProto(domain.LienStatusExecuted))
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, mapLienStatusToProto(domain.LienStatusTerminated))
	require.Equal(t, pb.LienStatus_LIEN_STATUS_UNSPECIFIED, mapLienStatusToProto(domain.LienStatus("UNKNOWN")))
}

// --- ExecuteLien with bucket-scoped balance ---

func TestExecuteLien_WithBucket_CalculatesAvailableBalance(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-EXEC-BUCKET": 100000,
	})

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-EXEC-BUCKET", 100000)

	// Create a bucketed lien
	lienAmount, err := domain.NewMoney("GBP", 30000) // 300 GBP
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "bucket-exec", "PO-EXEC-BUCKET", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	resp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{
		LienId: lien.ID.String(),
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
	require.NotNil(t, resp.AvailableBalance)
}

// --- TerminateLien with bucket-scoped balance ---

func TestTerminateLien_WithBucket_CalculatesAvailableBalance(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-TERM-BUCKET": 100000,
	})

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-TERM-BUCKET", 100000)

	lienAmount, err := domain.NewMoney("GBP", 30000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "bucket-term", "PO-TERM-BUCKET", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	resp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "bucket test termination",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp.Lien.Status)
	require.NotNil(t, resp.AvailableBalance)
}

// --- Idempotent TerminateLien edge cases ---

func TestTerminateLien_DoubleTermination_Idempotent(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		"ACC-TERM-DBL": 100000,
	})

	account := createTestAccountWithBalance(t, ctx, repo, "ACC-TERM-DBL", 100000)

	lienAmount, err := domain.NewMoney("GBP", 50000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "PO-TERM-DBL", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	// First termination
	resp1, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "first termination call",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp1.Lien.Status)

	// Second termination (idempotent) - should succeed with available balance
	resp2, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lien.ID.String(),
		Reason: "second termination call",
	})
	require.NoError(t, err)
	require.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp2.Lien.Status)
	require.NotNil(t, resp2.AvailableBalance)
}

// errTestInternal is a sentinel error for idempotency mock failures.
var errTestInternal = status.Error(codes.Internal, "mock internal error")

// buildTestIdempKey constructs an idempotency key matching the format used by ExecuteLien.
// Includes TenantID from context to match the key structure built inside ExecuteLien.
func buildTestIdempKey(ctx context.Context, lienID, requestID string) idempotency.Key {
	tid, _ := tenant.FromContext(ctx)
	return idempotency.Key{
		TenantID:  string(tid),
		Namespace: idempotencyNamespace,
		Operation: "execute_lien",
		EntityID:  lienID,
		RequestID: requestID,
	}
}
