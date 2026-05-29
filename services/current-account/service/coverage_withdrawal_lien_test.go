package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// =============================================================================
// Withdrawal/Deposit orchestrator resolveClearingAccountID with dynamic resolver
// =============================================================================

type mockInternalAccountClientForResolver struct {
	resp *internalaccountv1.ListInternalAccountsResponse
	err  error
}

func (m *mockInternalAccountClientForResolver) ListInternalAccounts(_ context.Context, _ *internalaccountv1.ListInternalAccountsRequest) (*internalaccountv1.ListInternalAccountsResponse, error) {
	return m.resp, m.err
}

func (m *mockInternalAccountClientForResolver) RetrieveInternalAccount(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockInternalAccountClientForResolver) Close() error { return nil }

func TestDepositOrchestrator_ResolveClearingAccountID_DynamicSuccess(t *testing.T) {
	mockClient := &mockInternalAccountClientForResolver{
		resp: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "dynamic-clearing-001"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: slog.Default(),
	})
	require.NoError(t, err)

	orchestrator := &DepositOrchestrator{
		logger:          slog.Default(),
		accountResolver: resolver,
		accountConfig: &config.AccountConfig{
			DepositClearingAccountID: "static-fallback",
		},
	}

	result := orchestrator.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "dynamic-clearing-001", result)
}

func TestDepositOrchestrator_ResolveClearingAccountID_DynamicFailsFallback(t *testing.T) {
	mockClient := &mockInternalAccountClientForResolver{
		err: fmt.Errorf("connection refused"),
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:        mockClient,
		Logger:        slog.Default(),
		LookupTimeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	orchestrator := &DepositOrchestrator{
		logger:          slog.Default(),
		accountResolver: resolver,
		accountConfig: &config.AccountConfig{
			DepositClearingAccountID: "static-fallback",
		},
	}

	result := orchestrator.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "static-fallback", result)
}

// =============================================================================
// HealthChecker: PositionKeeping and FinancialAccounting health checkers
// =============================================================================

type stubGRPCHealthClient struct {
	resp *grpc_health_v1.HealthCheckResponse
	err  error
}

func (m *stubGRPCHealthClient) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...grpc.CallOption) (*grpc_health_v1.HealthCheckResponse, error) {
	return m.resp, m.err
}

func (m *stubGRPCHealthClient) Watch(_ context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...grpc.CallOption) (grpc_health_v1.Health_WatchClient, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *stubGRPCHealthClient) List(_ context.Context, _ *grpc_health_v1.HealthListRequest, _ ...grpc.CallOption) (*grpc_health_v1.HealthListResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// =============================================================================
// Lien service: nil repo and early validation unit tests
// =============================================================================

func TestInitiateLien_NilLienRepo(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.InitiateLien(context.Background(), &pb.InitiateLienRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "lien operations not configured")
}

func TestExecuteLien_NilLienRepo(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.ExecuteLien(context.Background(), &pb.ExecuteLienRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestExecuteLien_InvalidLienID(t *testing.T) {
	db := openSharedDB(t)
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := svc.ExecuteLien(context.Background(), &pb.ExecuteLienRequest{LienId: "not-a-uuid"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid lien ID")
}

func TestTerminateLien_NilLienRepo(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.TerminateLien(context.Background(), &pb.TerminateLienRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestTerminateLien_InvalidLienID(t *testing.T) {
	db := openSharedDB(t)
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := svc.TerminateLien(context.Background(), &pb.TerminateLienRequest{LienId: "bad"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRetrieveLien_NilLienRepo(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.RetrieveLien(context.Background(), &pb.RetrieveLienRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestRetrieveLien_InvalidLienID(t *testing.T) {
	db := openSharedDB(t)
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := svc.RetrieveLien(context.Background(), &pb.RetrieveLienRequest{LienId: "xyz"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetActiveAmountBlocks_NilLienRepo(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.GetActiveAmountBlocks(context.Background(), &pb.GetActiveAmountBlocksRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestTerminateLien_NotFoundUnit(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{LienId: uuid.New().String()})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestExecuteLien_NotFoundViaRepoUnit(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: uuid.New().String()})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// =============================================================================
// InitiateLien: multi-asset mode validation paths
// =============================================================================

func TestInitiateLien_MultiAsset_InvalidInputAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		repo:     repo,
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	createTestAccountWithBalance(t, ctx, repo, "LIEN-MA-001", 10000)

	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "LIEN-MA-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "not-a-number",
			InstrumentCode: "RICE-KG",
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid input amount")
}

func TestInitiateLien_MultiAsset_NonPositiveInputAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		repo:     repo,
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	createTestAccountWithBalance(t, ctx, repo, "LIEN-MA-002", 10000)

	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "LIEN-MA-002",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "-5.00",
			InstrumentCode: "RICE-KG",
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "input amount must be positive")
}

func TestInitiateLien_MultiAsset_NoValuationFeatureRepo(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		repo:     repo,
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		// No valuationFeatureRepo - will fail at valuateInternal
	}

	createTestAccountWithBalance(t, ctx, repo, "LIEN-MA-003", 10000)

	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "LIEN-MA-003",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "5.00",
			InstrumentCode: "RICE-KG",
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	// Should fail with FailedPrecondition because valuationFeatureRepo is nil
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestInitiateLien_Legacy_NilAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		repo:     repo,
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	createTestAccountWithBalance(t, ctx, repo, "LIEN-LEG-001", 10000)

	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "LIEN-LEG-001",
		// No Amount and no Input = should fail
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "amount is required")
}

func TestInitiateLien_Legacy_CurrencyMismatchUnit(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		repo:     repo,
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	createTestAccountWithBalance(t, ctx, repo, "LIEN-LEG-002", 10000)

	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "LIEN-LEG-002",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "USD", Units: 50, Nanos: 0},
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "currency mismatch")
}

func TestInitiateLien_AccountNotFoundUnit(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	svc := &Service{
		repo:     repo,
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId: "NONEXISTENT-LIEN-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50, Nanos: 0},
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// =============================================================================
// ExecuteWithdrawal: early validation paths
// =============================================================================

func TestExecuteWithdrawal_MissingAccountIDAndWithdrawalID(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.ExecuteWithdrawal(context.Background(), &pb.ExecuteWithdrawalRequest{
		// Neither account_id nor withdrawal_id set
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "account_id is required")
}

func TestExecuteWithdrawal_DirectMode_MissingAmount(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.ExecuteWithdrawal(context.Background(), &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-001",
		// Amount is nil
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "amount is required")
}

// =============================================================================
// InitiateWithdrawal: early validation + account state checks
// =============================================================================

func TestInitiateWithdrawal_MissingAccountID(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.InitiateWithdrawal(context.Background(), &pb.InitiateWithdrawalRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "account_id is required")
}

func TestInitiateWithdrawal_MissingAmount(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.InitiateWithdrawal(context.Background(), &pb.InitiateWithdrawalRequest{
		AccountId: "ACC-001",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "amount is required")
}

func TestInitiateWithdrawal_AccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	svc, err := NewService(repo, nil)
	require.NoError(t, err)

	_, err = svc.InitiateWithdrawal(ctx, &pb.InitiateWithdrawalRequest{
		AccountId: "NONEXISTENT-WTH-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50, Nanos: 0},
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// =============================================================================
// UpdateWithdrawal: early validation
// =============================================================================

func TestUpdateWithdrawal_MissingWithdrawalID(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.UpdateWithdrawal(context.Background(), &pb.UpdateWithdrawalRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "withdrawal_id is required")
}

// =============================================================================
// RetrieveWithdrawal: missing identifiers
// =============================================================================

func TestRetrieveWithdrawal_MissingBothIDs(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	_, err := svc.RetrieveWithdrawal(context.Background(), &pb.RetrieveWithdrawalRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "either withdrawal_id or account_id is required")
}

// =============================================================================
// GetActiveAmountBlocks: account not found
// =============================================================================

// =============================================================================
// getAccountBalanceMinorUnits: various response shapes
// =============================================================================

func TestGetAccountBalanceMinorUnits_NilAmount(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: nil,
		},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	cents, err := svc.getAccountBalanceMinorUnits(context.Background(), "ACC-001", "GBP", 2)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cents)
}

func TestGetAccountBalanceMinorUnits_EmptyAmount(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount: "",
			},
		},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	cents, err := svc.getAccountBalanceMinorUnits(context.Background(), "ACC-001", "GBP", 2)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cents)
}

func TestGetAccountBalanceMinorUnits_InstrumentCodeMismatch(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "100.00",
				InstrumentCode: "USD",
			},
		},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := svc.getAccountBalanceMinorUnits(context.Background(), "ACC-001", "GBP", 2)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentCodeMismatch)
}

func TestGetAccountBalanceMinorUnits_InvalidAmountString(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "not-a-number",
				InstrumentCode: "GBP",
			},
		},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := svc.getAccountBalanceMinorUnits(context.Background(), "ACC-001", "GBP", 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse balance amount")
}

func TestGetAccountBalanceMinorUnits_Success(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "123.45",
				InstrumentCode: "GBP",
			},
		},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	cents, err := svc.getAccountBalanceMinorUnits(context.Background(), "ACC-001", "GBP", 2)
	require.NoError(t, err)
	assert.Equal(t, int64(12345), cents)
}

func TestGetAccountBalanceMinorUnits_Error(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceErr: fmt.Errorf("connection refused"),
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := svc.getAccountBalanceMinorUnits(context.Background(), "ACC-001", "GBP", 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestGetAccountBalanceMinorUnits_KWH_Precision0(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "1500",
				InstrumentCode: "KWH",
			},
		},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	minorUnits, err := svc.getAccountBalanceMinorUnits(context.Background(), "ACC-KWH-001", "KWH", 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1500), minorUnits)
}

func TestGetAccountBalanceMinorUnits_KWH_Precision3(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "1.500",
				InstrumentCode: "KWH",
			},
		},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	minorUnits, err := svc.getAccountBalanceMinorUnits(context.Background(), "ACC-KWH-001", "KWH", 3)
	require.NoError(t, err)
	assert.Equal(t, int64(1500), minorUnits)
}

func TestGetAccountBalanceMinorUnits_CarbonCredit(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "CARBON_CREDIT",
			},
		},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	minorUnits, err := svc.getAccountBalanceMinorUnits(context.Background(), "ACC-CC-001", "CARBON_CREDIT", 0)
	require.NoError(t, err)
	assert.Equal(t, int64(100), minorUnits)
}

func TestGetAccountBalanceMinorUnits_InstrumentMismatch_NonFiat(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "GBP",
			},
		},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := svc.getAccountBalanceMinorUnits(context.Background(), "ACC-KWH-001", "KWH", 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentCodeMismatch)
}

// =============================================================================
// hydrateAccountWithBalance: non-fiat instruments
// =============================================================================

func TestHydrateAccountWithBalance_KWH_Success(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "1500",
				InstrumentCode: "KWH",
			},
		},
	}
	db := openSharedDB(t)
	repo := persistence.NewRepository(db)
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	account, err := domain.NewCurrentAccountWithDimension("ACC-KWH-001", "KWH-001", uuid.New().String(), "KWH", "ENERGY", 0)
	require.NoError(t, err)
	hydrated, err := svc.hydrateAccountWithBalance(context.Background(), account)
	require.NoError(t, err)
	balanceMinor, _ := hydrated.Balance().ToMinorUnits()
	assert.Equal(t, int64(1500), balanceMinor)
	assert.Equal(t, "KWH", hydrated.Balance().InstrumentCode())
	assert.Equal(t, "ENERGY", hydrated.Balance().Dimension())
}

func TestHydrateAccountWithBalance_KWH_Precision3_Success(t *testing.T) {
	// PK returns "1.500" (major units). With precision=3, minor units = 1500.
	// Hydration must reconstruct Amount with precision=3 so 1500 minor -> 1.500 KWH.
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "1.500",
				InstrumentCode: "KWH",
			},
		},
	}
	db := openSharedDB(t)
	repo := persistence.NewRepository(db)
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	account, err := domain.NewCurrentAccountWithDimension("ACC-KWH-P3", "KWH-P3-001", uuid.New().String(), "KWH", "ENERGY", 3)
	require.NoError(t, err)
	hydrated, err := svc.hydrateAccountWithBalance(context.Background(), account)
	require.NoError(t, err)
	balanceMinor, _ := hydrated.Balance().ToMinorUnits()
	assert.Equal(t, int64(1500), balanceMinor)
	assert.Equal(t, 3, hydrated.Balance().Precision())
	// Verify major units display correctly
	assert.Equal(t, "1.500 KWH", hydrated.Balance().String())
}

func TestHydrateAccountWithBalance_CarbonCredit_Success(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "50",
				InstrumentCode: "CARBON_CREDIT",
			},
		},
	}
	db := openSharedDB(t)
	repo := persistence.NewRepository(db)
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	account, err := domain.NewCurrentAccountWithDimension("ACC-CC-001", "CC-001", uuid.New().String(), "CARBON_CREDIT", "CARBON", 0)
	require.NoError(t, err)
	hydrated, err := svc.hydrateAccountWithBalance(context.Background(), account)
	require.NoError(t, err)
	balanceMinor, _ := hydrated.Balance().ToMinorUnits()
	assert.Equal(t, int64(50), balanceMinor)
	assert.Equal(t, "CARBON_CREDIT", hydrated.Balance().InstrumentCode())
	assert.Equal(t, "CARBON", hydrated.Balance().Dimension())
}

func TestHydrateAccountWithPrefetchedBalance_KWH_Success(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	account, err := domain.NewCurrentAccountWithDimension("ACC-KWH-PREF-001", "KWH-PREF-001", uuid.New().String(), "KWH", "ENERGY", 0)
	require.NoError(t, err)
	hydrated, err := svc.hydrateAccountWithPrefetchedBalance(account, 5000)
	require.NoError(t, err)
	balanceMinor, _ := hydrated.Balance().ToMinorUnits()
	assert.Equal(t, int64(5000), balanceMinor)
	assert.Equal(t, "KWH", hydrated.Balance().InstrumentCode())
}

// =============================================================================
// calculateAvailableBalanceByBucket: edge cases
// =============================================================================

func TestCalculateAvailableBalanceByBucket_NilLienRepo(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	balance, _ := domain.NewMoney("GBP", 10000)
	result := svc.calculateAvailableBalanceByBucket(context.Background(), uuid.New(), "", balance)
	// With nil lienRepo, should return balance unchanged
	assert.Equal(t, balance, result)
}

// =============================================================================
// hydrateAccountWithBalance: error and success paths
// =============================================================================

func TestHydrateAccountWithBalance_PKError(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceErr: fmt.Errorf("PK unavailable"),
	}
	db := openSharedDB(t)
	repo := persistence.NewRepository(db)
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	account, _ := domain.NewCurrentAccount("HYDRATE-001", "HYDRATE-001", uuid.New().String(), "GBP")
	_, err := svc.hydrateAccountWithBalance(context.Background(), account)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PK unavailable")
}

func TestHydrateAccountWithBalance_Success(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "500.00",
				InstrumentCode: "GBP",
			},
		},
	}
	db := openSharedDB(t)
	repo := persistence.NewRepository(db)
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	account, _ := domain.NewCurrentAccount("HYDRATE-002", "HYDRATE-002", uuid.New().String(), "GBP")
	hydrated, err := svc.hydrateAccountWithBalance(context.Background(), account)
	require.NoError(t, err)
	balanceCents, _ := hydrated.Balance().ToMinorUnits()
	assert.Equal(t, int64(50000), balanceCents)
}

// =============================================================================
// hydrateAccountWithPrefetchedBalance
// =============================================================================

func TestHydrateAccountWithPrefetchedBalance_Success(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	account, _ := domain.NewCurrentAccount("PREF-001", "PREF-001", uuid.New().String(), "GBP")
	hydrated, err := svc.hydrateAccountWithPrefetchedBalance(account, 25000)
	require.NoError(t, err)
	balanceCents, _ := hydrated.Balance().ToMinorUnits()
	assert.Equal(t, int64(25000), balanceCents)
}

// =============================================================================
// releaseReservation: nil client and error path
// =============================================================================

func TestReleaseReservation_NilClient(_ *testing.T) {
	svc := &Service{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	// Should not panic with nil client
	svc.releaseReservation(context.Background(), uuid.New().String(), positionkeepingv1.ReservationStatus_RESERVATION_STATUS_TERMINATED)
}

func TestReleaseReservation_Error(_ *testing.T) {
	mockPK := &stubPKClient{
		releaseErr: fmt.Errorf("release failed"),
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	// Should not panic - just logs the error
	svc.releaseReservation(context.Background(), uuid.New().String(), positionkeepingv1.ReservationStatus_RESERVATION_STATUS_TERMINATED)
}

func TestReleaseReservation_Success(_ *testing.T) {
	mockPK := &stubPKClient{
		releaseResp: &positionkeepingv1.ReleaseReservationResponse{},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	svc.releaseReservation(context.Background(), uuid.New().String(), positionkeepingv1.ReservationStatus_RESERVATION_STATUS_EXECUTED)
}

// =============================================================================
// InitiateWithdrawal: account state validation (needs DB + PK mock)
// =============================================================================

func TestInitiateWithdrawal_AccountFrozen(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)

	// Create an active account, then freeze it
	account := createTestAccountWithBalance(t, ctx, repo, "WTH-FREEZE-001", 0)
	frozen, err := account.Freeze("compliance reason for freeze action")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, frozen))

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

	_, err = svc.InitiateWithdrawal(ctx, &pb.InitiateWithdrawalRequest{
		AccountId: "WTH-FREEZE-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 10},
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "frozen")
}

func TestInitiateWithdrawal_AccountClosed(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)

	// Create an active account with zero balance, then close it
	account := createTestAccountWithBalance(t, ctx, repo, "WTH-CLOSE-001", 0)
	closed, err := account.Close("")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, closed))

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

	_, err = svc.InitiateWithdrawal(ctx, &pb.InitiateWithdrawalRequest{
		AccountId: "WTH-CLOSE-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 10},
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "closed")
}

func TestInitiateWithdrawal_CurrencyMismatch(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "WTH-CUR-001", 10000)

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

	_, err := svc.InitiateWithdrawal(ctx, &pb.InitiateWithdrawalRequest{
		AccountId: "WTH-CUR-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "USD", Units: 10},
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "currency mismatch")
}

func TestInitiateWithdrawal_NegativeAmount(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "WTH-NEG-001", 10000)

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

	_, err := svc.InitiateWithdrawal(ctx, &pb.InitiateWithdrawalRequest{
		AccountId: "WTH-NEG-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: -10},
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestInitiateWithdrawal_ExceedsBalance(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "WTH-BAL-001", 5000)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "50.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	resp, err := svc.InitiateWithdrawal(ctx, &pb.InitiateWithdrawalRequest{
		AccountId: "WTH-BAL-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 100},
		},
	})
	// Should succeed but with a validation warning
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.ValidationPassed)
	assert.NotEmpty(t, resp.ValidationMessages)
}

func TestInitiateWithdrawal_SuccessWithReference(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "WTH-SUC-001", 10000)

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

	resp, err := svc.InitiateWithdrawal(ctx, &pb.InitiateWithdrawalRequest{
		AccountId: "WTH-SUC-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50},
		},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.ValidationPassed)
	assert.NotNil(t, resp.Withdrawal)
}

func TestInitiateWithdrawal_HydrationError(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "WTH-HYD-001", 10000)

	mockPK := &stubPKClient{
		getBalanceErr: fmt.Errorf("PK unavailable"),
	}
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.InitiateWithdrawal(ctx, &pb.InitiateWithdrawalRequest{
		AccountId: "WTH-HYD-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50},
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

// =============================================================================
// ControlCurrentAccount: close with zero balance (needs DB + PK mock)
// =============================================================================

func TestControlCurrentAccount_CloseZeroBalanceWithPK(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "CTL-CLOSE-001", 0)

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "0.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	resp, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-CLOSE-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Account no longer needed",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.Facility)
}

func TestControlCurrentAccount_CloseNonZeroBalanceWithPK(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "CTL-CLOSE-002", 10000)

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

	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-CLOSE-002",
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "non-zero balance")
}

func TestControlCurrentAccount_CloseHydrationError(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "CTL-CLOSE-003", 0)

	mockPK := &stubPKClient{
		getBalanceErr: fmt.Errorf("PK unavailable"),
	}
	svc := &Service{
		repo:             repo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-CLOSE-003",
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestControlCurrentAccount_CloseWithActiveLiens(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	account := createTestAccountWithBalance(t, ctx, repo, "CTL-CLOSE-004", 10000)

	// Create an active lien for this account
	lienAmount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	lien, err := domain.NewLien(account.ID(), lienAmount, "", "POR-CLOSE-001", nil)
	require.NoError(t, err)
	require.NoError(t, lienRepo.Create(ctx, lien))

	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: &quantityv1.InstrumentAmount{Amount: "0.00", InstrumentCode: "GBP"},
		},
	}
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, closeErr := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-CLOSE-004",
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
	})
	require.Error(t, closeErr)
	st, _ := status.FromError(closeErr)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "active liens")
}

func TestControlCurrentAccount_FreezeWithDomainError(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)

	// Create account, freeze it, then try to freeze again
	account := createTestAccountWithBalance(t, ctx, repo, "CTL-FRZDUP-001", 0)
	frozen, err := account.Freeze("compliance reason for freeze action")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, frozen))

	svc := &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-FRZDUP-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "double freeze attempt reason",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestControlCurrentAccount_UnfreezeNotFrozenWithPK(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	repo := persistence.NewRepository(db)
	createTestAccountWithBalance(t, ctx, repo, "CTL-UFRZ-001", 0)

	svc := &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "CTL-UFRZ-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNFREEZE,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}
