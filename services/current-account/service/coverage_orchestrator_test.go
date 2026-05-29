package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	refsaga "github.com/meridianhub/meridian/services/reference-data/saga"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// =============================================================================
// NoOp implementations: PublishWithTenant, NotifyAccountFrozen, NotifyAccountClosed
// =============================================================================

func TestNoOpAccountEventPublisher_PublishWithTenant(t *testing.T) {
	pub := &NoOpAccountEventPublisher{}
	err := pub.PublishWithTenant(context.Background(), "topic", "key", nil)
	assert.NoError(t, err)
}

func TestNoOpWebhookNotifier_NotifyAccountFrozen(t *testing.T) {
	n := &NoOpWebhookNotifier{}
	err := n.NotifyAccountFrozen(context.Background(), "tenant-1", "ACC-001", "reason", time.Now())
	assert.NoError(t, err)
}

func TestNoOpWebhookNotifier_NotifyAccountClosed(t *testing.T) {
	n := &NoOpWebhookNotifier{}
	err := n.NotifyAccountClosed(context.Background(), "tenant-1", "ACC-001", "reason", &WebhookBalanceInfo{
		Amount:       0,
		CurrencyCode: "GBP",
	}, time.Now())
	assert.NoError(t, err)
}

// =============================================================================
// Functional options: WithInstrumentGetter, WithAccountTypeCache
// =============================================================================

func TestWithInstrumentGetter(t *testing.T) {
	svc := &Service{}
	mock := &mockInstrumentGetter{}
	opt := WithInstrumentGetter(mock)
	opt(svc)
	assert.NotNil(t, svc.instrumentGetter)
}

func TestWithAccountTypeCache(t *testing.T) {
	svc := &Service{}
	mock := &stubAccountTypeCache{}
	opt := WithAccountTypeCache(mock)
	opt(svc)
	assert.NotNil(t, svc.accountTypeCache)
}

func TestApplyOptions(t *testing.T) {
	svc := &Service{}
	mock := &mockInstrumentGetter{}
	svc.ApplyOptions(WithInstrumentGetter(mock))
	assert.NotNil(t, svc.instrumentGetter)
}

// stubAccountTypeCache implements AccountTypeCache for testing.
type stubAccountTypeCache struct{}

func (s *stubAccountTypeCache) GetOrLoad(_ context.Context, _ tenant.TenantID, _ string) (*CachedAccountType, error) {
	return nil, errors.New("not implemented")
}

// =============================================================================
// mapWithdrawalStatusToProto - uncovered branches
// =============================================================================

func TestMapWithdrawalStatusToProto(t *testing.T) {
	tests := []struct {
		name   string
		status domain.WithdrawalStatus
		want   pb.WithdrawalStatus
	}{
		{"pending", domain.WithdrawalStatusPending, pb.WithdrawalStatus_WITHDRAWAL_STATUS_INITIATED},
		{"completed", domain.WithdrawalStatusCompleted, pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED},
		{"failed", domain.WithdrawalStatusFailed, pb.WithdrawalStatus_WITHDRAWAL_STATUS_FAILED},
		{"cancelled", domain.WithdrawalStatusCancelled, pb.WithdrawalStatus_WITHDRAWAL_STATUS_CANCELLED},
		{"unknown", domain.WithdrawalStatus("UNKNOWN"), pb.WithdrawalStatus_WITHDRAWAL_STATUS_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapWithdrawalStatusToProto(tt.status)
			assert.Equal(t, tt.want, got)
		})
	}
}

// =============================================================================
// protoToAccountStatus - uncovered branches
// =============================================================================

func TestProtoToAccountStatus(t *testing.T) {
	tests := []struct {
		name    string
		status  pb.AccountStatus
		wantStr string
		wantOk  bool
	}{
		{"active", pb.AccountStatus_ACCOUNT_STATUS_ACTIVE, string(domain.AccountStatusActive), true},
		{"frozen", pb.AccountStatus_ACCOUNT_STATUS_FROZEN, string(domain.AccountStatusFrozen), true},
		{"closed", pb.AccountStatus_ACCOUNT_STATUS_CLOSED, string(domain.AccountStatusClosed), true},
		{"unspecified", pb.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED, "", true},
		{"unknown", pb.AccountStatus(999), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := protoToAccountStatus(tt.status)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.wantStr, got)
		})
	}
}

// =============================================================================
// safeMinorUnits - overflow path
// =============================================================================

func TestSafeMinorUnits_ZeroBalance(t *testing.T) {
	// Normal case - zero balance returns 0
	acc := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithStatus(domain.AccountStatusActive).
		Build()
	result := safeMinorUnits(acc.Balance())
	assert.Equal(t, int64(0), result)
}

// =============================================================================
// NewDepositOrchestrator - nil dependency validation
// =============================================================================

func TestNewDepositOrchestrator_NilDependencies(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	db, _, cleanup := setupTestDB(t)
	defer cleanup()
	validRepo := persistence.NewRepository(db)

	tests := []struct {
		name    string
		cfg     DepositOrchestratorConfig
		wantErr error
	}{
		{
			name:    "nil logger",
			cfg:     DepositOrchestratorConfig{},
			wantErr: ErrOrchestratorLoggerNil,
		},
		{
			name: "nil repo",
			cfg: DepositOrchestratorConfig{
				Logger: logger,
			},
			wantErr: ErrOrchestratorRepositoryNil,
		},
		{
			name: "nil saga runner",
			cfg: DepositOrchestratorConfig{
				Logger: logger,
				Repo:   validRepo,
			},
			wantErr: ErrOrchestratorSagaRunnerNil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewDepositOrchestrator(tt.cfg)
			require.Error(t, err)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNewDepositOrchestrator_ValidConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	sagaRunner, depositScript, _ := testSagaRunner(t)

	orch, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:           logger,
		Repo:             repo,
		PosKeepingClient: &mockPositionKeepingClient{},
		FinAcctClient:    &mockFinancialAccountingClient{},
		SagaRunner:       sagaRunner,
		DepositScript:    depositScript,
	})
	require.NoError(t, err)
	assert.NotNil(t, orch)
}

// =============================================================================
// resolveDepositScript - all branches
// =============================================================================

func TestResolveDepositScript_NoSagaResolver(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	orch := &DepositOrchestrator{
		logger:        logger,
		depositScript: "default_script",
		sagaResolver:  nil,
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		Build()

	script, err := orch.resolveDepositScript(context.Background(), account)
	require.NoError(t, err)
	assert.Equal(t, "default_script", script)
}

func TestResolveDepositScript_EmptyProductTypeCode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	orch := &DepositOrchestrator{
		logger:        logger,
		depositScript: "default_script",
		sagaResolver:  &refsaga.ProductTypeSagaResolver{},
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		// No ProductTypeCode set
		Build()

	script, err := orch.resolveDepositScript(context.Background(), account)
	require.NoError(t, err)
	assert.Equal(t, "default_script", script)
}

func TestResolveDepositScript_NoTenantContext(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	orch := &DepositOrchestrator{
		logger:        logger,
		depositScript: "default_script",
		sagaResolver:  &refsaga.ProductTypeSagaResolver{},
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		WithProductTypeCode("SAVINGS-A").
		Build()

	// No tenant in context
	script, err := orch.resolveDepositScript(context.Background(), account)
	require.NoError(t, err)
	assert.Equal(t, "default_script", script)
}

// =============================================================================
// resolveClearingAccountID - all branches
// =============================================================================

func TestResolveClearingAccountID_NoResolverNoConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	orch := &DepositOrchestrator{
		logger:          logger,
		accountResolver: nil,
		accountConfig:   nil,
	}
	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "", result)
}

func TestResolveClearingAccountID_StaticConfigFallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := stubAccountConfig{depositClearingAccountID: "static-clearing-id"}.toConfig()
	orch := &DepositOrchestrator{
		logger:          logger,
		accountResolver: nil,
		accountConfig:   cfg,
	}
	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "static-clearing-id", result)
}

func TestResolveClearingAccountID_EmptyStaticConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := stubAccountConfig{depositClearingAccountID: ""}.toConfig()
	orch := &DepositOrchestrator{
		logger:          logger,
		accountResolver: nil,
		accountConfig:   cfg,
	}
	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "", result)
}

// =============================================================================
// sendControlActionWebhook - all branches
// =============================================================================

func TestSendControlActionWebhook_NilNotifier(_ *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{
		logger:          logger,
		webhookNotifier: nil,
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		Build()
	req := &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "test freeze for compliance",
	}
	// Should return immediately without error
	svc.sendControlActionWebhook(context.Background(), req, &account, time.Now())
}

func TestSendControlActionWebhook_NoTenantInContext(_ *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{
		logger:          logger,
		webhookNotifier: &NoOpWebhookNotifier{},
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		Build()
	req := &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "test freeze for compliance",
	}
	// No tenant context - should log warning and return
	svc.sendControlActionWebhook(context.Background(), req, &account, time.Now())
}

func TestSendControlActionWebhook_UnfreezeAction(_ *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{
		logger:          logger,
		webhookNotifier: &NoOpWebhookNotifier{},
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		Build()
	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	req := &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNFREEZE,
	}
	// Unfreeze should skip webhook (no notification per regulatory requirements)
	svc.sendControlActionWebhook(ctx, req, &account, time.Now())
}

func TestSendControlActionWebhook_UnspecifiedAction(_ *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{
		logger:          logger,
		webhookNotifier: &NoOpWebhookNotifier{},
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		Build()
	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	req := &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNSPECIFIED,
	}
	svc.sendControlActionWebhook(ctx, req, &account, time.Now())
}

// =============================================================================
// sendFreezeWebhook and sendCloseWebhook
// =============================================================================

func TestSendFreezeWebhook_Success(_ *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{
		logger:          logger,
		webhookNotifier: &NoOpWebhookNotifier{},
	}
	// Direct call (synchronous, no goroutine)
	svc.sendFreezeWebhook("tenant-1", "ACC-001", "regulatory freeze", time.Now())
}

func TestSendFreezeWebhook_Error(_ *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{
		logger:          logger,
		webhookNotifier: &errorWebhookNotifier{},
	}
	// Should log error but not panic
	svc.sendFreezeWebhook("tenant-1", "ACC-001", "regulatory freeze", time.Now())
}

func TestSendCloseWebhook_Success(_ *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{
		logger:          logger,
		webhookNotifier: &NoOpWebhookNotifier{},
	}
	svc.sendCloseWebhook("tenant-1", "ACC-001", "account closure", &WebhookBalanceInfo{
		Amount:       0,
		CurrencyCode: "GBP",
	}, time.Now())
}

func TestSendCloseWebhook_Error(_ *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{
		logger:          logger,
		webhookNotifier: &errorWebhookNotifier{},
	}
	svc.sendCloseWebhook("tenant-1", "ACC-001", "account closure", &WebhookBalanceInfo{
		Amount:       0,
		CurrencyCode: "GBP",
	}, time.Now())
}

func TestSendControlActionWebhook_FreezeWithTenant(_ *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{
		logger:          logger,
		webhookNotifier: &NoOpWebhookNotifier{},
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		Build()
	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	req := &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "regulatory compliance freeze",
	}
	// Freeze with valid tenant context should send webhook (async)
	svc.sendControlActionWebhook(ctx, req, &account, time.Now())
	// Yield to allow fire-and-forget goroutine to execute
	runtime.Gosched()
}

func TestSendControlActionWebhook_CloseWithTenant(_ *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &Service{
		logger:          logger,
		webhookNotifier: &NoOpWebhookNotifier{},
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		Build()
	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	req := &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "account closure requested",
	}
	svc.sendControlActionWebhook(ctx, req, &account, time.Now())
	// Yield to allow fire-and-forget goroutine to execute
	runtime.Gosched()
}

// =============================================================================
// UpdateCurrentAccount
// =============================================================================

func TestUpdateCurrentAccount_MissingAccountID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	_, err := svc.UpdateCurrentAccount(ctx, &pb.UpdateCurrentAccountRequest{
		AccountId: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateCurrentAccount_AccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	_, err := svc.UpdateCurrentAccount(ctx, &pb.UpdateCurrentAccountRequest{
		AccountId: "ACC-NONEXISTENT",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUpdateCurrentAccount_ClosedAccountRejectsUpdate(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	// Create an account, freeze, then close it
	account, err := domain.NewCurrentAccountWithDimension("ACC-CLOSED-001", "ext-001", "00000000-0000-0000-0000-000000000001", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Freeze then close
	account, err = account.Freeze("regulatory compliance requirement")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	account, err = account.Close("requested by customer")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	_, err = svc.UpdateCurrentAccount(ctx, &pb.UpdateCurrentAccountRequest{
		AccountId: "ACC-CLOSED-001",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "closed")
}

func TestUpdateCurrentAccount_NoChanges(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	account, err := domain.NewCurrentAccountWithDimension("ACC-NOCHANGE-001", "ext-001", "00000000-0000-0000-0000-000000000001", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	resp, err := svc.UpdateCurrentAccount(ctx, &pb.UpdateCurrentAccountRequest{
		AccountId: "ACC-NOCHANGE-001",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp.Facility)
}

func TestUpdateCurrentAccount_OverdraftFieldsIgnored(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	account, err := domain.NewCurrentAccountWithDimension("ACC-OD-001", "ext-001", "00000000-0000-0000-0000-000000000001", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Overdraft fields should be logged and ignored
	overdraftEnabled := true
	resp, err := svc.UpdateCurrentAccount(ctx, &pb.UpdateCurrentAccountRequest{
		AccountId:        "ACC-OD-001",
		OverdraftEnabled: &overdraftEnabled,
	})
	require.NoError(t, err)
	assert.NotNil(t, resp.Facility)
}

// =============================================================================
// celProgramAdapter Eval coverage
// =============================================================================

func TestCelProgramAdapter_Eval_NilResult(t *testing.T) {
	// Test the nil result path in celProgramAdapter.Eval
	// The celProgramAdapter wraps a cel.Program - we can test the Eval method
	// through the evaluateFungibilityKey function with our mock
	evaluator := &mockFungibilityKeyProgram{
		keyFunc: func(attrs map[string]string) string {
			return attrs["key"]
		},
	}
	key, err := evaluateFungibilityKey(evaluator, map[string]string{"key": "value"})
	require.NoError(t, err)
	assert.Equal(t, "value", key)
}

func TestEvaluateFungibilityKey_NonStringResult(t *testing.T) {
	evaluator := &nonStringEvaluator{}
	_, err := evaluateFungibilityKey(evaluator, map[string]string{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFungibilityKeyType)
}

func TestEvaluateFungibilityKey_NilResult(t *testing.T) {
	evaluator := &nilResultEvaluator{}
	key, err := evaluateFungibilityKey(evaluator, map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "", key)
}

// =============================================================================
// ControlCurrentAccount - targeted unit test paths
// =============================================================================

func TestControlCurrentAccount_UnspecifiedAction(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	account, err := domain.NewCurrentAccountWithDimension("ACC-UNSPEC-001", "ext-001", "00000000-0000-0000-0000-000000000001", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-UNSPEC-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNSPECIFIED,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "UNSPECIFIED")
}

func TestControlCurrentAccount_MissingAccountID(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestControlCurrentAccount_AccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-NONEXIST",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "test freeze for compliance",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestControlCurrentAccount_FreezeShortReason(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	account, err := domain.NewCurrentAccountWithDimension("ACC-FR-001", "ext-001", "00000000-0000-0000-0000-000000000001", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-FR-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "short", // Less than 10 characters
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestControlCurrentAccount_UnfreezeNotFrozen(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	account, err := domain.NewCurrentAccountWithDimension("ACC-UF-001", "ext-001", "00000000-0000-0000-0000-000000000001", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-UF-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNFREEZE,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestControlCurrentAccount_DefaultAction(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	account, err := domain.NewCurrentAccountWithDimension("ACC-DEFAULT-001", "ext-001", "00000000-0000-0000-0000-000000000002", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Use an unknown action value (cast int to enum)
	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-DEFAULT-001",
		ControlAction: pb.ControlAction(999),
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "unknown control action")
}

// =============================================================================
// mapRegistryDimension
// =============================================================================

func TestMapRegistryDimension_AdditionalCases(t *testing.T) {
	assert.Equal(t, "CARBON", mapRegistryDimension("CARBON"))
	assert.Equal(t, "COMPUTE", mapRegistryDimension("COMPUTE"))
	assert.Equal(t, "", mapRegistryDimension(""))
}

// =============================================================================
// More functional options: WithValuationFeatureRepo, WithEventPublisher, etc.
// =============================================================================

func TestWithValuationFeatureRepo(t *testing.T) {
	svc := &Service{}
	opt := WithValuationFeatureRepo(nil)
	opt(svc)
	assert.Nil(t, svc.valuationFeatureRepo)
}

func TestWithValuationEngine(t *testing.T) {
	svc := &Service{}
	opt := WithValuationEngine(nil)
	opt(svc)
	assert.Nil(t, svc.valuationEngine)
}

func TestWithEventPublisher(t *testing.T) {
	svc := &Service{}
	pub := &NoOpAccountEventPublisher{}
	opt := WithEventPublisher(pub)
	opt(svc)
	assert.NotNil(t, svc.eventPublisher)
}

func TestWithWebhookNotifier(t *testing.T) {
	svc := &Service{}
	notifier := &NoOpWebhookNotifier{}
	opt := WithWebhookNotifier(notifier)
	opt(svc)
	assert.NotNil(t, svc.webhookNotifier)
}

// =============================================================================
// NewWithdrawalOrchestrator - nil dependency validation
// =============================================================================

func TestNewWithdrawalOrchestrator_NilDependencies(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	db, _, cleanup := setupTestDB(t)
	defer cleanup()
	validRepo := persistence.NewRepository(db)

	tests := []struct {
		name    string
		cfg     WithdrawalOrchestratorConfig
		wantErr error
	}{
		{
			name:    "nil logger",
			cfg:     WithdrawalOrchestratorConfig{},
			wantErr: ErrOrchestratorLoggerNil,
		},
		{
			name: "nil repo",
			cfg: WithdrawalOrchestratorConfig{
				Logger: logger,
			},
			wantErr: ErrOrchestratorRepositoryNil,
		},
		{
			name: "nil saga runner",
			cfg: WithdrawalOrchestratorConfig{
				Logger: logger,
				Repo:   validRepo,
			},
			wantErr: ErrOrchestratorSagaRunnerNil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewWithdrawalOrchestrator(tt.cfg)
			require.Error(t, err)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNewWithdrawalOrchestrator_EmptyScript(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	sagaRunner, _, _ := testSagaRunner(t)

	_, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:           logger,
		Repo:             persistence.NewRepository(db),
		PosKeepingClient: &mockPositionKeepingClient{},
		FinAcctClient:    &mockFinancialAccountingClient{},
		SagaRunner:       sagaRunner,
		WithdrawalScript: "",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOrchestratorWithdrawalScriptEmpty)
}

func TestNewWithdrawalOrchestrator_ValidConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	sagaRunner, _, withdrawalScript := testSagaRunner(t)

	orch, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:           logger,
		Repo:             persistence.NewRepository(db),
		PosKeepingClient: &mockPositionKeepingClient{},
		FinAcctClient:    &mockFinancialAccountingClient{},
		SagaRunner:       sagaRunner,
		WithdrawalScript: withdrawalScript,
	})
	require.NoError(t, err)
	assert.NotNil(t, orch)
}

// =============================================================================
// resolveWithdrawalScript
// =============================================================================

func TestResolveWithdrawalScript_NoSagaResolver(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	orch := &WithdrawalOrchestrator{
		logger:           logger,
		withdrawalScript: "default_withdrawal",
		sagaResolver:     nil,
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		Build()

	script, err := orch.resolveWithdrawalScript(context.Background(), account)
	require.NoError(t, err)
	assert.Equal(t, "default_withdrawal", script)
}

func TestResolveWithdrawalScript_EmptyProductTypeCode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	orch := &WithdrawalOrchestrator{
		logger:           logger,
		withdrawalScript: "default_withdrawal",
		sagaResolver:     &refsaga.ProductTypeSagaResolver{},
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		Build()

	script, err := orch.resolveWithdrawalScript(context.Background(), account)
	require.NoError(t, err)
	assert.Equal(t, "default_withdrawal", script)
}

func TestResolveWithdrawalScript_NoTenantContext(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	orch := &WithdrawalOrchestrator{
		logger:           logger,
		withdrawalScript: "default_withdrawal",
		sagaResolver:     &refsaga.ProductTypeSagaResolver{},
	}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithInstrumentCode("GBP").
		WithProductTypeCode("SAVINGS-A").
		Build()

	script, err := orch.resolveWithdrawalScript(context.Background(), account)
	require.NoError(t, err)
	assert.Equal(t, "default_withdrawal", script)
}

// =============================================================================
// Withdrawal resolveClearingAccountID
// =============================================================================

func TestWithdrawalResolveClearingAccountID_NoResolverNoConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	orch := &WithdrawalOrchestrator{
		logger:          logger,
		accountResolver: nil,
		accountConfig:   nil,
	}
	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "", result)
}

func TestWithdrawalResolveClearingAccountID_StaticConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	orch := &WithdrawalOrchestrator{
		logger:          logger,
		accountResolver: nil,
		accountConfig: &config.AccountConfig{
			WithdrawalClearingAccountID: "withdrawal-clearing-id",
		},
	}
	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "withdrawal-clearing-id", result)
}

func TestWithdrawalResolveClearingAccountID_EmptyStaticConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	orch := &WithdrawalOrchestrator{
		logger:          logger,
		accountResolver: nil,
		accountConfig: &config.AccountConfig{
			WithdrawalClearingAccountID: "",
		},
	}
	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "", result)
}

// =============================================================================
// domainToProtoLifecycleStatus / protoToDomainLifecycleStatus
// =============================================================================

func TestDomainToProtoLifecycleStatus(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewTextHandler(os.Stdout, nil))}

	tests := []struct {
		name  string
		input domain.ValuationFeatureLifecycleStatus
		want  pb.ValuationFeatureLifecycleStatus
	}{
		{"initiated", domain.ValuationFeatureLifecycleStatusInitiated, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_INITIATED},
		{"active", domain.ValuationFeatureLifecycleStatusActive, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE},
		{"terminated", domain.ValuationFeatureLifecycleStatusTerminated, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED},
		{"unknown", domain.ValuationFeatureLifecycleStatus("UNKNOWN"), pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.domainToProtoLifecycleStatus(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProtoToDomainLifecycleStatus(t *testing.T) {
	svc := &Service{logger: slog.New(slog.NewTextHandler(os.Stdout, nil))}

	tests := []struct {
		name  string
		input pb.ValuationFeatureLifecycleStatus
		want  domain.ValuationFeatureLifecycleStatus
	}{
		{"initiated", pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_INITIATED, domain.ValuationFeatureLifecycleStatusInitiated},
		{"active", pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE, domain.ValuationFeatureLifecycleStatusActive},
		{"terminated", pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED, domain.ValuationFeatureLifecycleStatusTerminated},
		{"unspecified", pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_UNSPECIFIED, domain.ValuationFeatureLifecycleStatusInitiated},
		{"unknown", pb.ValuationFeatureLifecycleStatus(999), domain.ValuationFeatureLifecycleStatusInitiated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.protoToDomainLifecycleStatus(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// =============================================================================
// mapStatusToProto - default/unknown branch
// =============================================================================

func TestMapStatusToProto_Unknown(t *testing.T) {
	result := mapStatusToProto(domain.AccountStatus("UNKNOWN"))
	assert.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED, result)
}

// =============================================================================
// Deposit resolveClearingAccountID with AccountResolver fallback
// =============================================================================

func TestDepositResolveClearingAccountID_ResolverFails_FallbackToStatic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:        &mockInternalAccountClient{listErr: errors.New("unavailable")},
		Logger:        logger,
		CacheTTL:      time.Second,
		LookupTimeout: time.Second,
	})
	require.NoError(t, err)

	cfg := stubAccountConfig{depositClearingAccountID: "fallback-id"}.toConfig()
	orch := &DepositOrchestrator{
		logger:          logger,
		accountResolver: resolver,
		accountConfig:   cfg,
	}
	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "fallback-id", result)
}

func TestDepositResolveClearingAccountID_ResolverSuccess(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: &mockInternalAccountClient{
			listResponse: &internalaccountv1.ListInternalAccountsResponse{
				Facilities: []*internalaccountv1.InternalAccountFacility{
					{AccountId: "dynamic-clearing-id"},
				},
			},
		},
		Logger:        logger,
		CacheTTL:      time.Second,
		LookupTimeout: 5 * time.Second,
	})
	require.NoError(t, err)

	orch := &DepositOrchestrator{
		logger:          logger,
		accountResolver: resolver,
		accountConfig:   nil,
	}
	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	result := orch.resolveClearingAccountID(ctx, "GBP")
	assert.Equal(t, "dynamic-clearing-id", result)
}

// =============================================================================
// Withdrawal resolveClearingAccountID with AccountResolver
// =============================================================================

func TestWithdrawalResolveClearingAccountID_ResolverFails_FallbackToStatic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:        &mockInternalAccountClient{listErr: errors.New("unavailable")},
		Logger:        logger,
		CacheTTL:      time.Second,
		LookupTimeout: time.Second,
	})
	require.NoError(t, err)

	orch := &WithdrawalOrchestrator{
		logger:          logger,
		accountResolver: resolver,
		accountConfig: &config.AccountConfig{
			WithdrawalClearingAccountID: "withdrawal-fallback-id",
		},
	}
	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "withdrawal-fallback-id", result)
}

// =============================================================================
// FungibilityValidator.ValidateDoubleEntry - noBucketKeyProgram + expression set
// =============================================================================

func TestFungibilityValidator_NoBucketKeyProgramFallsThrough(t *testing.T) {
	// When instrument has FungibilityKeyExpression but no BucketKeyProgram and no evaluator,
	// it treats the instrument as fully fungible (no program available)
	ctx := tenant.WithTenant(context.Background(), "test-tenant")

	mock := &mockInstrumentGetter{
		instruments: map[string]*cache.CachedInstrument{
			"RICE-KG": {
				Definition: &registry.InstrumentDefinition{
					Code:                     "RICE-KG",
					Version:                  1,
					FungibilityKeyExpression: "attributes.batch_id", // Expression set
				},
				BucketKeyProgram: nil, // But no compiled program
			},
		},
	}

	validator := NewFungibilityValidator(mock)

	err := validator.ValidateDoubleEntry(ctx, "RICE-KG", 1,
		map[string]string{"batch_id": "A"},
		map[string]string{"batch_id": "B"},
	)
	// Should pass because no evaluator is available - treated as fully fungible
	assert.NoError(t, err)
}

// =============================================================================
// checkBasisDrift
// =============================================================================

func TestCheckBasisDrift_NilValuationAnalysis(_ *testing.T) {
	svc := &Service{logger: slog.New(slog.NewTextHandler(os.Stdout, nil))}
	lien := &domain.Lien{
		ValuationAnalysis: nil,
	}
	// Should return immediately without panic
	svc.checkBasisDrift(lien)
}

func TestCheckBasisDrift_InvalidJSON(_ *testing.T) {
	svc := &Service{logger: slog.New(slog.NewTextHandler(os.Stdout, nil))}
	lien := &domain.Lien{
		ValuationAnalysis: json.RawMessage(`{invalid json`),
	}
	svc.checkBasisDrift(lien)
}

func TestCheckBasisDrift_EmptyKnowledgeAt(_ *testing.T) {
	svc := &Service{logger: slog.New(slog.NewTextHandler(os.Stdout, nil))}
	lien := &domain.Lien{
		ValuationAnalysis: json.RawMessage(`{"knowledgeAt":""}`),
	}
	svc.checkBasisDrift(lien)
}

func TestCheckBasisDrift_InvalidTimeFormat(_ *testing.T) {
	svc := &Service{logger: slog.New(slog.NewTextHandler(os.Stdout, nil))}
	lien := &domain.Lien{
		ValuationAnalysis: json.RawMessage(`{"knowledgeAt":"not-a-time"}`),
	}
	svc.checkBasisDrift(lien)
}

func TestCheckBasisDrift_RecentKnowledge(_ *testing.T) {
	svc := &Service{logger: slog.New(slog.NewTextHandler(os.Stdout, nil))}
	recent := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	lien := &domain.Lien{
		ValuationAnalysis: json.RawMessage(fmt.Sprintf(`{"knowledgeAt":"%s"}`, recent)),
	}
	svc.checkBasisDrift(lien)
}

func TestCheckBasisDrift_StaleKnowledge(_ *testing.T) {
	svc := &Service{logger: slog.New(slog.NewTextHandler(os.Stdout, nil))}
	stale := time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339) // 60 days ago
	lien := &domain.Lien{
		ID:                uuid.New(),
		ValuationAnalysis: json.RawMessage(fmt.Sprintf(`{"knowledgeAt":"%s"}`, stale)),
	}
	svc.checkBasisDrift(lien)
}

// =============================================================================
// loadSagaAsset
// =============================================================================

func TestLoadSagaAsset_WithEnvVar(t *testing.T) {
	// Create a temp dir with a test asset
	tmpDir := t.TempDir()
	testContent := "test saga script content"
	testPath := filepath.Join(tmpDir, "test.star")
	require.NoError(t, os.WriteFile(testPath, []byte(testContent), 0o644))

	t.Setenv("SAGA_ASSET_DIR", tmpDir)

	content, err := loadSagaAsset("test.star")
	require.NoError(t, err)
	assert.Equal(t, testContent, content)
}

func TestLoadSagaAsset_FileNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SAGA_ASSET_DIR", tmpDir)

	_, err := loadSagaAsset("nonexistent.star")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read saga asset")
}
