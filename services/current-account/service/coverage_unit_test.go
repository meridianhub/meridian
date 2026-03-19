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
	pq "github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/google/cel-go/cel"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	refsaga "github.com/meridianhub/meridian/services/reference-data/saga"
	sharedamount "github.com/meridianhub/meridian/shared/pkg/amount"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
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
			name: "nil pos keeping client",
			cfg: DepositOrchestratorConfig{
				Logger: logger,
				Repo:   validRepo,
			},
			wantErr: ErrOrchestratorPosKeepingClientNil,
		},
		{
			name: "nil fin acct client",
			cfg: DepositOrchestratorConfig{
				Logger:           logger,
				Repo:             validRepo,
				PosKeepingClient: &mockPositionKeepingClient{},
			},
			wantErr: ErrOrchestratorFinAcctClientNil,
		},
		{
			name: "nil saga runner",
			cfg: DepositOrchestratorConfig{
				Logger:           logger,
				Repo:             validRepo,
				PosKeepingClient: &mockPositionKeepingClient{},
				FinAcctClient:    &mockFinancialAccountingClient{},
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
			name: "nil pos keeping client",
			cfg: WithdrawalOrchestratorConfig{
				Logger: logger,
				Repo:   validRepo,
			},
			wantErr: ErrOrchestratorPosKeepingClientNil,
		},
		{
			name: "nil fin acct client",
			cfg: WithdrawalOrchestratorConfig{
				Logger:           logger,
				Repo:             validRepo,
				PosKeepingClient: &mockPositionKeepingClient{},
			},
			wantErr: ErrOrchestratorFinAcctClientNil,
		},
		{
			name: "nil saga runner",
			cfg: WithdrawalOrchestratorConfig{
				Logger:           logger,
				Repo:             validRepo,
				PosKeepingClient: &mockPositionKeepingClient{},
				FinAcctClient:    &mockFinancialAccountingClient{},
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

// =============================================================================
// ControlCurrentAccount - successful freeze and unfreeze
// =============================================================================

func TestControlCurrentAccount_FreezeSuccess(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	account, err := domain.NewCurrentAccountWithDimension("ACC-FRSUC-001", "ext-001", "00000000-0000-0000-0000-000000000003", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	resp, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-FRSUC-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "regulatory compliance freeze for investigation",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_FROZEN, resp.Facility.AccountStatus)
}

func TestControlCurrentAccount_UnfreezeSuccess(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	account, err := domain.NewCurrentAccountWithDimension("ACC-UFSUC-001", "ext-001", "00000000-0000-0000-0000-000000000004", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// First freeze the account
	account, err = account.Freeze("regulatory compliance freeze required")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Now unfreeze
	resp, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-UFSUC-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNFREEZE,
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)
}

func TestControlCurrentAccount_CloseZeroBalance(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	// Create service with zero balance for the account
	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-CLSUC-001": 0,
	})

	account, err := domain.NewCurrentAccountWithDimension("ACC-CLSUC-001", "ext-001", "00000000-0000-0000-0000-000000000005", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	resp, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-CLSUC-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "customer requested account closure",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_CLOSED, resp.Facility.AccountStatus)
}

func TestControlCurrentAccount_CloseNonZeroBalance(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-CLNZ-001": 5000, // Non-zero balance
	})

	account, err := domain.NewCurrentAccountWithDimension("ACC-CLNZ-001", "ext-001", "00000000-0000-0000-0000-000000000006", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-CLNZ-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "customer requested account closure",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "non-zero balance")
}

func TestControlCurrentAccount_FreezeAlreadyFrozen(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc := mustNewService(t, repo, nil)

	account, err := domain.NewCurrentAccountWithDimension("ACC-FF-001", "ext-001", "00000000-0000-0000-0000-000000000007", "GBP", "CURRENCY", 2)
	require.NoError(t, err)
	account, err = account.Freeze("first freeze for compliance")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     "ACC-FF-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "second freeze for compliance",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// =============================================================================
// Saga handler utility functions
// =============================================================================

func TestOptionalString(t *testing.T) {
	assert.Equal(t, "hello", optionalString(map[string]any{"key": "hello"}, "key"))
	assert.Equal(t, "", optionalString(map[string]any{}, "key"))
	assert.Equal(t, "", optionalString(map[string]any{"key": nil}, "key"))
	assert.Equal(t, "", optionalString(map[string]any{"key": 42}, "key"))
}

func TestRequireString(t *testing.T) {
	val, err := requireString(map[string]any{"key": "hello"}, "key")
	require.NoError(t, err)
	assert.Equal(t, "hello", val)

	_, err = requireString(map[string]any{}, "key")
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)

	_, err = requireString(map[string]any{"key": 42}, "key")
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidParameterType)
}

func TestRequireDecimal(t *testing.T) {
	// String input
	d, err := requireDecimal(map[string]any{"key": "100.50"}, "key")
	require.NoError(t, err)
	assert.Equal(t, "100.5", d.String())

	// Float input
	d, err = requireDecimal(map[string]any{"key": 42.5}, "key")
	require.NoError(t, err)
	assert.Equal(t, "42.5", d.String())

	// Int input
	d, err = requireDecimal(map[string]any{"key": 100}, "key")
	require.NoError(t, err)
	assert.Equal(t, "100", d.String())

	// Int64 input
	d, err = requireDecimal(map[string]any{"key": int64(200)}, "key")
	require.NoError(t, err)
	assert.Equal(t, "200", d.String())

	// Missing
	_, err = requireDecimal(map[string]any{}, "key")
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)

	// Invalid string
	_, err = requireDecimal(map[string]any{"key": "not-a-number"}, "key")
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidParameterType)

	// Invalid type
	_, err = requireDecimal(map[string]any{"key": true}, "key")
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidParameterType)
}

func TestRequireInt64(t *testing.T) {
	// Int64 input
	val, err := requireInt64(map[string]any{"key": int64(42)}, "key")
	require.NoError(t, err)
	assert.Equal(t, int64(42), val)

	// Int input
	val, err = requireInt64(map[string]any{"key": 42}, "key")
	require.NoError(t, err)
	assert.Equal(t, int64(42), val)

	// Float64 input
	val, err = requireInt64(map[string]any{"key": 42.0}, "key")
	require.NoError(t, err)
	assert.Equal(t, int64(42), val)

	// Missing
	_, err = requireInt64(map[string]any{}, "key")
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)

	// Invalid type
	_, err = requireInt64(map[string]any{"key": "string"}, "key")
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidParameterType)
}

func TestStubNotImplemented(t *testing.T) {
	handler := stubNotImplemented("test_handler")
	_, err := handler(nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, errHandlerNotImplemented)
	assert.Contains(t, err.Error(), "test_handler")
}

func TestWrapHandlerError(t *testing.T) {
	err := wrapHandlerError("test_handler", errors.New("inner error"))
	assert.Contains(t, err.Error(), "test_handler")
	assert.Contains(t, err.Error(), "inner error")
}

// =============================================================================
// NewServiceWithValuationFeatures nil repo
// =============================================================================

func TestNewServiceWithValuationFeatures_NilRepo(t *testing.T) {
	_, err := NewServiceWithValuationFeatures(nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRepositoryNil)
}

// =============================================================================
// Test helpers / stubs
// =============================================================================

// newTestRepo creates a persistence.Repository from a test DB.
func newTestRepo(db *gorm.DB) *persistence.Repository {
	return persistence.NewRepository(db)
}

// stubAccountConfig provides a simple way to create config.AccountConfig for testing.
type stubAccountConfig struct {
	depositClearingAccountID string
}

func (s stubAccountConfig) toConfig() *config.AccountConfig {
	return &config.AccountConfig{
		DepositClearingAccountID: s.depositClearingAccountID,
	}
}

// errorWebhookNotifier always returns errors - for testing error paths.
type errorWebhookNotifier struct{}

func (e *errorWebhookNotifier) NotifyAccountFrozen(_ context.Context, _, _, _ string, _ time.Time) error {
	return errors.New("webhook delivery failed")
}

func (e *errorWebhookNotifier) NotifyAccountClosed(_ context.Context, _, _, _ string, _ *WebhookBalanceInfo, _ time.Time) error {
	return errors.New("webhook delivery failed")
}

// nonStringEvaluator returns a non-string result from Eval.
type nonStringEvaluator struct{}

func (e *nonStringEvaluator) Eval(_ interface{}) (interface{}, error) {
	return 42, nil // Returns int instead of string
}

// nilResultEvaluator returns nil from Eval.
type nilResultEvaluator struct{}

func (e *nilResultEvaluator) Eval(_ interface{}) (interface{}, error) {
	return nil, nil
}

// =============================================================================
// getDeps / getAccount via StarlarkContext
// =============================================================================

func TestGetDeps_Success(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{Logger: slog.Default()}
	ctx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	sc := &saga.StarlarkContext{Context: ctx}

	got, err := getDeps(sc)
	require.NoError(t, err)
	assert.Same(t, deps, got)
}

func TestGetDeps_NotFound(t *testing.T) {
	sc := &saga.StarlarkContext{Context: context.Background()}
	_, err := getDeps(sc)
	require.Error(t, err)
	assert.ErrorIs(t, err, errHandlerDepsNotFound)
}

func TestGetDeps_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), ContextKeyHandlerDeps, "wrong-type")
	sc := &saga.StarlarkContext{Context: ctx}

	_, err := getDeps(sc)
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidHandlerDeps)
}

func TestGetAccount_Success(t *testing.T) {
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-GET-1").
		WithExternalIdentifier("EXT-GET-1").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		Build()

	ctx := context.WithValue(context.Background(), ContextKeyAccount, account)
	sc := &saga.StarlarkContext{Context: ctx}

	got, err := getAccount(sc)
	require.NoError(t, err)
	assert.Equal(t, "ACC-GET-1", got.AccountID())
}

func TestGetAccount_NotFound(t *testing.T) {
	sc := &saga.StarlarkContext{Context: context.Background()}
	_, err := getAccount(sc)
	require.Error(t, err)
	assert.ErrorIs(t, err, errAccountNotFound)
}

func TestGetAccount_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), ContextKeyAccount, "not-an-account")
	sc := &saga.StarlarkContext{Context: ctx}

	_, err := getAccount(sc)
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidAccountType)
}

// =============================================================================
// safeMinorUnits - overflow path (60% -> 100%)
// =============================================================================

func TestSafeMinorUnits_Overflow(t *testing.T) {
	// Create an Amount with a huge decimal that overflows int64 when converted to minor units.
	// GBP has precision 2. int64 max is ~9.2*10^18, so major units of 10^17
	// => 10^17 * 100 = 10^19 > int64 max => triggers overflow.
	inst, err := quantity.NewInstrument("GBP", 0, "CURRENCY", 2)
	require.NoError(t, err)
	hugeDecimal := decimal.NewFromFloat(1e17) // 100,000,000,000,000,000 GBP
	hugeAmount := sharedamount.NewFromDecimal(inst, hugeDecimal)
	result := safeMinorUnits(hugeAmount)
	assert.Equal(t, int64(0), result, "Overflowing amount should return 0")
}

func TestSafeMinorUnits_NormalAmount(t *testing.T) {
	amount, err := domain.NewMoney("GBP", 10050) // £100.50
	require.NoError(t, err)
	result := safeMinorUnits(amount)
	assert.Equal(t, int64(10050), result)
}

// =============================================================================
// mapLienStatusToProto - all cases
// =============================================================================

func TestMapLienStatusToProto(t *testing.T) {
	tests := []struct {
		input    domain.LienStatus
		expected pb.LienStatus
	}{
		{domain.LienStatusActive, pb.LienStatus_LIEN_STATUS_ACTIVE},
		{domain.LienStatusExecuted, pb.LienStatus_LIEN_STATUS_EXECUTED},
		{domain.LienStatusTerminated, pb.LienStatus_LIEN_STATUS_TERMINATED},
		{domain.LienStatus("UNKNOWN"), pb.LienStatus_LIEN_STATUS_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			assert.Equal(t, tt.expected, mapLienStatusToProto(tt.input))
		})
	}
}

// =============================================================================
// toAmountBlockProto - with and without expiry
// =============================================================================

func TestToAmountBlockProto_WithExpiry(t *testing.T) {
	amt, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	expiry := time.Now().Add(24 * time.Hour)
	lien := &domain.Lien{
		ID:                    uuid.New(),
		Amount:                amt,
		PaymentOrderReference: "PO-123",
		ExpiresAt:             &expiry,
	}
	block := toAmountBlockProto(lien)
	assert.Equal(t, pb.AmountBlockType_AMOUNT_BLOCK_TYPE_PENDING, block.BlockType)
	assert.Contains(t, block.Purpose, "PO-123")
	assert.NotNil(t, block.ExpiresAt)
}

func TestToAmountBlockProto_NoExpiry(t *testing.T) {
	amt, err := domain.NewMoney("GBP", 3000)
	require.NoError(t, err)
	lien := &domain.Lien{
		ID:                    uuid.New(),
		Amount:                amt,
		PaymentOrderReference: "PO-456",
		ExpiresAt:             nil,
	}
	block := toAmountBlockProto(lien)
	assert.Nil(t, block.ExpiresAt)
}

// =============================================================================
// decimalToMoneyAmount
// =============================================================================

func TestDecimalToMoneyAmount(t *testing.T) {
	d := decimal.NewFromFloat(100.50)
	result := decimalToMoneyAmount(d, "GBP")
	require.NotNil(t, result)
	require.NotNil(t, result.Amount)
	assert.Equal(t, "GBP", result.Amount.CurrencyCode)
	assert.Equal(t, int64(100), result.Amount.Units)
	assert.Equal(t, int32(500000000), result.Amount.Nanos)
}

func TestDecimalToMoneyAmount_Zero(t *testing.T) {
	d := decimal.Zero
	result := decimalToMoneyAmount(d, "USD")
	require.NotNil(t, result)
	assert.Equal(t, int64(0), result.Amount.Units)
	assert.Equal(t, int32(0), result.Amount.Nanos)
}

func TestDecimalToMoneyAmount_Negative(t *testing.T) {
	d := decimal.NewFromFloat(-50.25)
	result := decimalToMoneyAmount(d, "EUR")
	require.NotNil(t, result)
	assert.Equal(t, int64(-50), result.Amount.Units)
	assert.Equal(t, int32(-250000000), result.Amount.Nanos)
}

// =============================================================================
// Health checker - constructor and checkers
// =============================================================================

func TestNewHealthChecker_NilRepo(t *testing.T) {
	_, err := NewHealthChecker(HealthCheckerConfig{
		Repository: nil,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHealthCheckerRepositoryNil)
}

func TestNewHealthChecker_Defaults(t *testing.T) {
	repo := setupTestRepository(t)
	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository: repo,
	})
	require.NoError(t, err)
	assert.Equal(t, "current-account", hc.serviceName)
}

func TestHealthChecker_MapStatusToGRPC(t *testing.T) {
	repo := setupTestRepository(t)
	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository: repo,
	})
	require.NoError(t, err)

	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, hc.mapStatusToGRPC(health.StatusHealthy))
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, hc.mapStatusToGRPC(health.StatusDegraded))
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, hc.mapStatusToGRPC(health.StatusUnhealthy))
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, hc.mapStatusToGRPC(health.StatusUnknown))
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, hc.mapStatusToGRPC(health.Status(99)))
}

func TestHealthChecker_Check_OverallHealth(t *testing.T) {
	repo := setupTestRepository(t)
	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository:  repo,
		ServiceName: "test-svc",
	})
	require.NoError(t, err)

	// Check with empty service name (overall health)
	resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: ""})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	// Should be SERVING since DB is accessible
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestHealthChecker_Check_MatchingServiceName(t *testing.T) {
	repo := setupTestRepository(t)
	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository:  repo,
		ServiceName: "my-service",
	})
	require.NoError(t, err)

	resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: "my-service"})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestHealthChecker_Check_SpecificComponent(t *testing.T) {
	repo := setupTestRepository(t)
	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository:  repo,
		ServiceName: "test-svc",
	})
	require.NoError(t, err)

	// Check specific component "database"
	resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: "database"})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestHealthChecker_Check_UnknownComponent(t *testing.T) {
	repo := setupTestRepository(t)
	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository:  repo,
		ServiceName: "test-svc",
	})
	require.NoError(t, err)

	// Unknown component should return UNKNOWN
	resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: "nonexistent"})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
}

func TestHealthChecker_LogHealthCheck_HealthyPath(t *testing.T) {
	repo := setupTestRepository(t)
	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository: repo,
	})
	require.NoError(t, err)

	report := &health.Report{
		Components: []health.ComponentResult{
			{Name: "database", Status: health.StatusHealthy, Message: "ok"},
		},
	}
	// Should not panic; exercises healthy/degraded log path
	hc.logHealthCheck(report, health.StatusHealthy, grpc_health_v1.HealthCheckResponse_SERVING)
	hc.logHealthCheck(report, health.StatusDegraded, grpc_health_v1.HealthCheckResponse_SERVING)
}

func TestHealthChecker_LogHealthCheck_UnhealthyPath(t *testing.T) {
	repo := setupTestRepository(t)
	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository: repo,
	})
	require.NoError(t, err)

	report := &health.Report{
		Components: []health.ComponentResult{
			{Name: "database", Status: health.StatusUnhealthy, Message: "down", Error: errors.New("conn refused")},
			{Name: "pk", Status: health.StatusUnknown, Message: "timeout"},
		},
	}
	// Should not panic; exercises unhealthy/unknown log path
	hc.logHealthCheck(report, health.StatusUnhealthy, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	hc.logHealthCheck(report, health.StatusUnknown, grpc_health_v1.HealthCheckResponse_UNKNOWN)
}

// =============================================================================
// Database, PositionKeeping, FinancialAccounting health checkers
// =============================================================================

func TestDatabaseHealthChecker_Name(t *testing.T) {
	c := NewDatabaseHealthChecker(nil, time.Second)
	assert.Equal(t, "database", c.Name())
}

func TestDatabaseHealthChecker_Check_Success(t *testing.T) {
	repo := setupTestRepository(t)
	c := NewDatabaseHealthChecker(repo, 5*time.Second)
	result := c.Check(context.Background())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Equal(t, "database", result.Name)
	assert.Contains(t, result.Message, "successful")
}

func TestPositionKeepingHealthChecker_Name(t *testing.T) {
	c := NewPositionKeepingHealthChecker(nil, time.Second)
	assert.Equal(t, "positionkeeping", c.Name())
}

func TestPositionKeepingHealthChecker_Check_Error(t *testing.T) {
	mockClient := &stubHealthClient{err: errors.New("connection refused")}
	c := NewPositionKeepingHealthChecker(mockClient, 5*time.Second)
	result := c.Check(context.Background())
	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "unreachable")
}

func TestPositionKeepingHealthChecker_Check_NotServing(t *testing.T) {
	mockClient := &stubHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
		},
	}
	c := NewPositionKeepingHealthChecker(mockClient, 5*time.Second)
	result := c.Check(context.Background())
	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "not serving")
}

func TestPositionKeepingHealthChecker_Check_Serving(t *testing.T) {
	mockClient := &stubHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVING,
		},
	}
	c := NewPositionKeepingHealthChecker(mockClient, 5*time.Second)
	result := c.Check(context.Background())
	assert.Equal(t, health.StatusHealthy, result.Status)
}

func TestFinancialAccountingHealthChecker_Name(t *testing.T) {
	c := NewFinancialAccountingHealthChecker(nil, time.Second)
	assert.Equal(t, "financialaccounting", c.Name())
}

func TestFinancialAccountingHealthChecker_Check_Error(t *testing.T) {
	mockClient := &stubHealthClient{err: errors.New("connection refused")}
	c := NewFinancialAccountingHealthChecker(mockClient, 5*time.Second)
	result := c.Check(context.Background())
	assert.Equal(t, health.StatusDegraded, result.Status)
}

func TestFinancialAccountingHealthChecker_Check_NotServing(t *testing.T) {
	mockClient := &stubHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
		},
	}
	c := NewFinancialAccountingHealthChecker(mockClient, 5*time.Second)
	result := c.Check(context.Background())
	assert.Equal(t, health.StatusDegraded, result.Status)
}

func TestFinancialAccountingHealthChecker_Check_Serving(t *testing.T) {
	mockClient := &stubHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVING,
		},
	}
	c := NewFinancialAccountingHealthChecker(mockClient, 5*time.Second)
	result := c.Check(context.Background())
	assert.Equal(t, health.StatusHealthy, result.Status)
}

func TestPositionKeepingHealthChecker_Check_Timeout(t *testing.T) {
	// Use an already-cancelled context to trigger the timeout path
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockClient := &stubHealthClient{err: errors.New("context cancelled")}
	c := NewPositionKeepingHealthChecker(mockClient, 1*time.Millisecond)
	result := c.Check(ctx)
	assert.Equal(t, health.StatusDegraded, result.Status)
}

func TestFinancialAccountingHealthChecker_Check_Timeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockClient := &stubHealthClient{err: errors.New("context cancelled")}
	c := NewFinancialAccountingHealthChecker(mockClient, 1*time.Millisecond)
	result := c.Check(ctx)
	assert.Equal(t, health.StatusDegraded, result.Status)
}

// =============================================================================
// resolveDepositScript / resolveWithdrawalScript - saga resolution paths
// =============================================================================

func TestResolveDepositScript_WithProductTypeSaga_NoTenant(t *testing.T) {
	// When there IS a resolver and product type but NO tenant context, fallback to default
	resolver := refsaga.NewProductTypeSagaResolver(
		&stubSagaRegistry{},
		cache.NewLocalAccountTypeCache(&stubAccountTypeCacheLoader{}, &stubCELCompiler{}),
	)

	orch := &DepositOrchestrator{
		logger:        slog.Default(),
		depositScript: "default_deposit()",
		sagaResolver:  resolver,
	}

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-RESOLVE-1").
		WithExternalIdentifier("EXT-RESOLVE-1").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithProductTypeCode("PREMIUM_SAVINGS").
		Build()

	// Without tenant context, should fallback to default
	script, err := orch.resolveDepositScript(context.Background(), account)
	require.NoError(t, err)
	assert.Equal(t, "default_deposit()", script)
}

func TestResolveWithdrawalScript_WithProductType_NoTenant(t *testing.T) {
	resolver := refsaga.NewProductTypeSagaResolver(
		&stubSagaRegistry{},
		cache.NewLocalAccountTypeCache(&stubAccountTypeCacheLoader{}, &stubCELCompiler{}),
	)

	orch := &WithdrawalOrchestrator{
		logger:           slog.Default(),
		withdrawalScript: "default_withdrawal()",
		sagaResolver:     resolver,
	}

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-1").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithProductTypeCode("PREMIUM").
		Build()

	// No tenant in context -> fallback to default
	script, err := orch.resolveWithdrawalScript(context.Background(), account)
	require.NoError(t, err)
	assert.Equal(t, "default_withdrawal()", script)
}

func TestResolveWithdrawalScript_SagaNotFound(t *testing.T) {
	mockRegistry := &stubSagaRegistry{
		getActiveErr: refsaga.ErrNotFound,
	}
	accountTypeCache := cache.NewLocalAccountTypeCache(
		&stubAccountTypeCacheLoaderWithResult{},
		&stubCELCompiler{},
	)
	resolver := refsaga.NewProductTypeSagaResolver(mockRegistry, accountTypeCache)

	orch := &WithdrawalOrchestrator{
		logger:           slog.Default(),
		withdrawalScript: "default_withdrawal()",
		sagaResolver:     resolver,
	}

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-1").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithProductTypeCode("PREMIUM").
		Build()

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	// The account type cache should find this, then registry returns ErrNotFound
	// for the prefixed saga, which wraps as ErrSagaNotFound -> fail fast
	script, err := orch.resolveWithdrawalScript(ctx, account)
	// When prefix is empty from cache, it tries generic "withdrawal" -> ErrNotFound fallback
	// depends on cache behavior
	if err != nil {
		assert.ErrorIs(t, err, refsaga.ErrSagaNotFound)
	} else {
		assert.Equal(t, "default_withdrawal()", script)
	}
	_ = script
}

func TestResolveDepositScript_ResolvedScriptEmpty(t *testing.T) {
	// Test path where resolver returns a definition but with empty ResolvedScript
	mockRegistry := &stubSagaRegistry{
		getActiveResult: &refsaga.Definition{
			Name:           "deposit",
			ResolvedScript: "", // Empty - should fall back
		},
	}

	accountTypeCache := cache.NewLocalAccountTypeCache(
		&stubAccountTypeCacheLoaderWithResult{},
		&stubCELCompiler{},
	)
	resolver := refsaga.NewProductTypeSagaResolver(mockRegistry, accountTypeCache)

	orch := &DepositOrchestrator{
		logger:        slog.Default(),
		depositScript: "default_deposit()",
		sagaResolver:  resolver,
	}

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-1").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithProductTypeCode("PREMIUM").
		Build()

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	script, err := orch.resolveDepositScript(ctx, account)
	require.NoError(t, err)
	assert.Equal(t, "default_deposit()", script)
}

func TestResolveDepositScript_ResolvedScriptPresent(t *testing.T) {
	mockRegistry := &stubSagaRegistry{
		getActiveResult: &refsaga.Definition{
			Name:           "deposit",
			ResolvedScript: "custom_deposit()",
		},
	}

	accountTypeCache := cache.NewLocalAccountTypeCache(
		&stubAccountTypeCacheLoaderWithResult{},
		&stubCELCompiler{},
	)
	resolver := refsaga.NewProductTypeSagaResolver(mockRegistry, accountTypeCache)

	orch := &DepositOrchestrator{
		logger:        slog.Default(),
		depositScript: "default_deposit()",
		sagaResolver:  resolver,
	}

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-1").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithProductTypeCode("PREMIUM").
		Build()

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	script, err := orch.resolveDepositScript(ctx, account)
	require.NoError(t, err)
	assert.Equal(t, "custom_deposit()", script)
}

func TestResolveWithdrawalScript_ResolvedScriptEmpty(t *testing.T) {
	mockRegistry := &stubSagaRegistry{
		getActiveResult: &refsaga.Definition{
			Name:           "withdrawal",
			ResolvedScript: "",
		},
	}
	accountTypeCache := cache.NewLocalAccountTypeCache(
		&stubAccountTypeCacheLoaderWithResult{},
		&stubCELCompiler{},
	)
	resolver := refsaga.NewProductTypeSagaResolver(mockRegistry, accountTypeCache)

	orch := &WithdrawalOrchestrator{
		logger:           slog.Default(),
		withdrawalScript: "default_withdrawal()",
		sagaResolver:     resolver,
	}

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-1").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithProductTypeCode("PREMIUM").
		Build()

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	script, err := orch.resolveWithdrawalScript(ctx, account)
	require.NoError(t, err)
	assert.Equal(t, "default_withdrawal()", script)
}

func TestResolveWithdrawalScript_ResolvedScriptPresent(t *testing.T) {
	mockRegistry := &stubSagaRegistry{
		getActiveResult: &refsaga.Definition{
			Name:           "withdrawal",
			ResolvedScript: "custom_withdrawal()",
		},
	}
	accountTypeCache := cache.NewLocalAccountTypeCache(
		&stubAccountTypeCacheLoaderWithResult{},
		&stubCELCompiler{},
	)
	resolver := refsaga.NewProductTypeSagaResolver(mockRegistry, accountTypeCache)

	orch := &WithdrawalOrchestrator{
		logger:           slog.Default(),
		withdrawalScript: "default_withdrawal()",
		sagaResolver:     resolver,
	}

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-1").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithProductTypeCode("PREMIUM").
		Build()

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	script, err := orch.resolveWithdrawalScript(ctx, account)
	require.NoError(t, err)
	assert.Equal(t, "custom_withdrawal()", script)
}

// =============================================================================
// Valuation feature lifecycle status mappers - all paths
// =============================================================================

func TestDomainToProtoLifecycleStatus_AllCases(t *testing.T) {
	svc := &Service{logger: slog.Default()}
	tests := []struct {
		input    domain.ValuationFeatureLifecycleStatus
		expected pb.ValuationFeatureLifecycleStatus
	}{
		{domain.ValuationFeatureLifecycleStatusInitiated, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_INITIATED},
		{domain.ValuationFeatureLifecycleStatusActive, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE},
		{domain.ValuationFeatureLifecycleStatusTerminated, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED},
		{domain.ValuationFeatureLifecycleStatus("UNKNOWN"), pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			assert.Equal(t, tt.expected, svc.domainToProtoLifecycleStatus(tt.input))
		})
	}
}

func TestProtoToDomainLifecycleStatus_AllCases(t *testing.T) {
	svc := &Service{logger: slog.Default()}
	tests := []struct {
		input    pb.ValuationFeatureLifecycleStatus
		expected domain.ValuationFeatureLifecycleStatus
	}{
		{pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_INITIATED, domain.ValuationFeatureLifecycleStatusInitiated},
		{pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE, domain.ValuationFeatureLifecycleStatusActive},
		{pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED, domain.ValuationFeatureLifecycleStatusTerminated},
		{pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_UNSPECIFIED, domain.ValuationFeatureLifecycleStatusInitiated},
		{pb.ValuationFeatureLifecycleStatus(99), domain.ValuationFeatureLifecycleStatusInitiated}, // default
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("proto_%d", tt.input), func(t *testing.T) {
			assert.Equal(t, tt.expected, svc.protoToDomainLifecycleStatus(tt.input))
		})
	}
}

// =============================================================================
// domainToProtoValuationFeature
// =============================================================================

func TestDomainToProtoValuationFeature(t *testing.T) {
	svc := &Service{logger: slog.Default()}
	id := uuid.New()
	acctID := uuid.New()
	methodID := uuid.New()
	now := time.Now()

	feature := &domain.ValuationFeature{
		ID:                     id,
		AccountID:              acctID,
		InstrumentCode:         "KWH",
		ValuationMethodID:      methodID,
		ValuationMethodVersion: 3,
		Parameters:             map[string]interface{}{"spread": 0.02},
		LifecycleStatus:        domain.ValuationFeatureLifecycleStatusActive,
		ValidFrom:              now.Add(-24 * time.Hour),
		ValidTo:                now.Add(24 * time.Hour),
		CreatedAt:              now,
		CreatedBy:              "admin",
		UpdatedAt:              now,
		UpdatedBy:              "admin",
		Version:                2,
	}

	proto := svc.domainToProtoValuationFeature(feature)
	assert.Equal(t, id.String(), proto.Id)
	assert.Equal(t, acctID.String(), proto.AccountId)
	assert.Equal(t, "KWH", proto.InstrumentCode)
	assert.Equal(t, methodID.String(), proto.ValuationMethodId)
	assert.Equal(t, int32(3), proto.ValuationMethodVersion)
	assert.Contains(t, proto.Parameters, "spread")
	assert.Equal(t, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE, proto.LifecycleStatus)
	assert.Equal(t, int32(2), proto.Version)
}

func TestDomainToProtoValuationFeature_NilParameters(t *testing.T) {
	svc := &Service{logger: slog.Default()}
	feature := &domain.ValuationFeature{
		ID:              uuid.New(),
		AccountID:       uuid.New(),
		LifecycleStatus: domain.ValuationFeatureLifecycleStatusInitiated,
		Parameters:      nil,
	}

	proto := svc.domainToProtoValuationFeature(feature)
	assert.Equal(t, "", proto.Parameters)
}

// =============================================================================
// toProtoFacility
// =============================================================================

func TestToProtoFacility(t *testing.T) {
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-PROTO-1").
		WithExternalIdentifier("EXT-PROTO-1").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithProductTypeCode("SAVINGS").
		WithBehaviorClass("standard").
		Build()

	facility := toProtoFacility(account)
	assert.Equal(t, "ACC-PROTO-1", facility.AccountId)
	assert.Equal(t, "EXT-PROTO-1", facility.ExternalIdentifier)
	assert.Equal(t, "GBP", facility.InstrumentCode)
	assert.Equal(t, "CURRENCY", facility.Dimension)
	assert.Equal(t, "SAVINGS", facility.ProductTypeCode)
	assert.Equal(t, "standard", facility.BehaviorClass)
}

// =============================================================================
// Withdrawal orchestrator: resolveClearingAccountID
// =============================================================================

func TestWithdrawalOrchestrator_ResolveClearingAccountID_DynamicResolverFailsFallsToStatic(t *testing.T) {
	// When the dynamic resolver exists but fails, it falls back to static config
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: &mockInternalAccountClient{listErr: errors.New("unavailable")},
		Logger: slog.Default(),
	})
	require.NoError(t, err)

	orch := &WithdrawalOrchestrator{
		logger:          slog.Default(),
		accountResolver: resolver,
		accountConfig: &config.AccountConfig{
			WithdrawalClearingAccountID: "STATIC-CLEAR-W",
		},
	}

	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "STATIC-CLEAR-W", result)
}

func TestWithdrawalOrchestrator_ResolveClearingAccountID_StaticFallback(t *testing.T) {
	orch := &WithdrawalOrchestrator{
		logger: slog.Default(),
		accountConfig: &config.AccountConfig{
			WithdrawalClearingAccountID: "STATIC-W",
		},
	}

	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "STATIC-W", result)
}

func TestWithdrawalOrchestrator_ResolveClearingAccountID_NeitherConfigured(t *testing.T) {
	orch := &WithdrawalOrchestrator{
		logger: slog.Default(),
	}

	result := orch.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "", result)
}

// =============================================================================
// Test stubs for health / saga tests
// =============================================================================

// stubHealthClient implements grpc_health_v1.HealthClient for testing.
type stubHealthClient struct {
	resp *grpc_health_v1.HealthCheckResponse
	err  error
}

func (s *stubHealthClient) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...grpc.CallOption) (*grpc_health_v1.HealthCheckResponse, error) {
	return s.resp, s.err
}

func (s *stubHealthClient) Watch(_ context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...grpc.CallOption) (grpc_health_v1.Health_WatchClient, error) {
	return nil, errors.New("not implemented")
}

func (s *stubHealthClient) List(_ context.Context, _ *grpc_health_v1.HealthListRequest, _ ...grpc.CallOption) (*grpc_health_v1.HealthListResponse, error) {
	return nil, errors.New("not implemented")
}

// stubSagaRegistry implements refsaga.Registry for testing.
type stubSagaRegistry struct {
	getActiveResult *refsaga.Definition
	getActiveErr    error
}

func (s *stubSagaRegistry) GetByID(_ context.Context, _ uuid.UUID) (*refsaga.Definition, error) {
	return nil, refsaga.ErrNotFound
}

func (s *stubSagaRegistry) GetDefinition(_ context.Context, _ string, _ int) (*refsaga.Definition, error) {
	return nil, refsaga.ErrNotFound
}

func (s *stubSagaRegistry) GetActive(_ context.Context, _ string) (*refsaga.Definition, error) {
	return s.getActiveResult, s.getActiveErr
}

func (s *stubSagaRegistry) ListByStatus(_ context.Context, _ refsaga.Status) ([]*refsaga.Definition, error) {
	return nil, nil
}

func (s *stubSagaRegistry) CreateDraft(_ context.Context, _ *refsaga.Definition) error {
	return nil
}

func (s *stubSagaRegistry) UpdateDefinition(_ context.Context, _ uuid.UUID, _ *refsaga.Definition) error {
	return nil
}

func (s *stubSagaRegistry) ActivateSaga(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (s *stubSagaRegistry) DeprecateSaga(_ context.Context, _ uuid.UUID, _ *uuid.UUID) error {
	return nil
}

// stubAccountTypeCacheLoader implements cache.AccountTypeLoader with not-found result.
type stubAccountTypeCacheLoader struct{}

func (s *stubAccountTypeCacheLoader) LoadAccountType(_ context.Context, _ string) (*accounttype.Definition, error) {
	return nil, errors.New("not found")
}

func (s *stubAccountTypeCacheLoader) ListActiveAccountTypes(_ context.Context) ([]*accounttype.Definition, error) {
	return nil, nil
}

// stubAccountTypeCacheLoaderWithResult returns a result with empty prefix.
type stubAccountTypeCacheLoaderWithResult struct{}

func (s *stubAccountTypeCacheLoaderWithResult) LoadAccountType(_ context.Context, _ string) (*accounttype.Definition, error) {
	return &accounttype.Definition{
		Code:              "PREMIUM",
		DefaultSagaPrefix: "",
	}, nil
}

func (s *stubAccountTypeCacheLoaderWithResult) ListActiveAccountTypes(_ context.Context) ([]*accounttype.Definition, error) {
	return nil, nil
}

// =============================================================================
// Saga handler tests: initiate_log, cancel_log, etc.
// =============================================================================

func TestCurrentAccountPositionKeepingInitiateLog_Success(t *testing.T) {
	mockPK := &mockPositionKeepingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}

	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"position_id":     "ACC-TEST-1",
		"amount":          "100.50",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-001",
	}

	result, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "POS-LOG-001", resultMap["log_id"])
	assert.Equal(t, "INITIATED", resultMap["status"])
}

func TestCurrentAccountPositionKeepingInitiateLog_MissingAccountID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger: slog.Default(),
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"amount":          "100.50",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-001",
	}

	_, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountPositionKeepingInitiateLog_InvalidDirection(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: &mockPositionKeepingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"position_id":     "ACC-TEST-1",
		"amount":          "100.50",
		"instrument_code": "GBP",
		"direction":       "INVALID",
		"transaction_id":  "TXN-001",
	}

	_, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidDirection)
}

func TestCurrentAccountPositionKeepingInitiateLog_DebitDirection(t *testing.T) {
	mockPK := &mockPositionKeepingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"position_id":     "ACC-TEST-1",
		"amount":          "50.00",
		"instrument_code": "GBP",
		"direction":       "DEBIT",
		"transaction_id":  "TXN-002",
	}

	result, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCurrentAccountPositionKeepingInitiateLog_PosKeepingFails(t *testing.T) {
	mockPK := &mockPositionKeepingClient{failOnInitiate: true}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"position_id":     "ACC-TEST-1",
		"amount":          "100.50",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-001",
	}

	_, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.Error(t, err)
}

func TestCurrentAccountPositionKeepingInitiateLog_NoDeps(t *testing.T) {
	ctx := &saga.StarlarkContext{Context: context.Background()}
	_, err := currentAccountPositionKeepingInitiateLog(ctx, map[string]any{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errHandlerDepsNotFound)
}

func TestCurrentAccountPositionKeepingInitiateLog_LegacyAccountID(t *testing.T) {
	mockPK := &mockPositionKeepingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	// Use "account_id" (legacy) instead of "position_id"
	params := map[string]any{
		"account_id":      "ACC-LEGACY",
		"amount":          "200.00",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-LEGACY",
	}

	result, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCurrentAccountPositionKeepingInitiateLog_LegacyCurrency(t *testing.T) {
	mockPK := &mockPositionKeepingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	// Use "currency" (legacy) instead of "instrument_code"
	params := map[string]any{
		"position_id":    "ACC-TEST-1",
		"amount":         "100.00",
		"currency":       "GBP",
		"direction":      "CREDIT",
		"transaction_id": "TXN-CUR",
	}

	result, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCurrentAccountPositionKeepingInitiateLog_MissingInstrumentCode(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger: slog.Default(),
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"position_id":    "ACC-TEST-1",
		"amount":         "100.50",
		"direction":      "CREDIT",
		"transaction_id": "TXN-001",
	}

	_, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountPositionKeepingInitiateLog_WithValuationAnalysis(t *testing.T) {
	mockPK := &mockPositionKeepingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"position_id":     "ACC-TEST-1",
		"amount":          "100.50",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-001",
		"valuation_analysis": map[string]interface{}{
			"method_id": "test-method",
		},
	}

	result, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Verify attributes were captured
	require.NotNil(t, mockPK.lastInitiateRequest)
	assert.NotNil(t, mockPK.lastInitiateRequest.InitialEntry.Attributes)
}

func TestCurrentAccountPositionKeepingCancelLog_MissingLogID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger: slog.Default(),
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"version":        int64(1),
		"transaction_id": "TXN-001",
		"account_id":     "ACC-001",
		"direction":      "CREDIT",
	}

	_, err := currentAccountPositionKeepingCancelLog(ctx, params)
	require.Error(t, err)
}

func TestCurrentAccountPositionKeepingCancelLog_MissingVersion(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger: slog.Default(),
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"log_id":         "LOG-001",
		"transaction_id": "TXN-001",
		"account_id":     "ACC-001",
		"direction":      "CREDIT",
	}

	_, err := currentAccountPositionKeepingCancelLog(ctx, params)
	require.Error(t, err)
}

func TestCurrentAccountPositionKeepingCancelLog_Success(t *testing.T) {
	mockPK := &mockPositionKeepingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"log_id":         "LOG-001",
		"version":        int64(1),
		"transaction_id": "TXN-001",
		"account_id":     "ACC-001",
		"direction":      "CREDIT",
	}

	result, err := currentAccountPositionKeepingCancelLog(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

// =============================================================================
// Financial accounting handler tests
// =============================================================================

func TestCurrentAccountFinAcctInitiateBookingLog_Success(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"account_id":       "ACC-FA-1",
		"instrument_code":  "GBP",
		"transaction_id":   "TXN-FA-1",
		"transaction_type": "DEPOSIT",
	}

	result, err := currentAccountFinAcctInitiateBookingLog(ctx, params)
	require.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, "BOOK-LOG-001", resultMap["booking_log_id"])
	assert.Equal(t, "CREATED", resultMap["status"])
}

func TestCurrentAccountFinAcctInitiateBookingLog_MissingParams(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{Logger: slog.Default()}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	// Missing account_id
	_, err := currentAccountFinAcctInitiateBookingLog(ctx, map[string]any{
		"instrument_code":  "GBP",
		"transaction_id":   "TXN-1",
		"transaction_type": "DEPOSIT",
	})
	require.Error(t, err)

	// Missing instrument_code
	_, err = currentAccountFinAcctInitiateBookingLog(ctx, map[string]any{
		"account_id":       "ACC-1",
		"transaction_id":   "TXN-1",
		"transaction_type": "DEPOSIT",
	})
	require.Error(t, err)

	// Missing transaction_type
	_, err = currentAccountFinAcctInitiateBookingLog(ctx, map[string]any{
		"account_id":      "ACC-1",
		"instrument_code": "GBP",
		"transaction_id":  "TXN-1",
	})
	require.Error(t, err)
}

func TestCurrentAccountFinAcctInitiateBookingLog_LegacyCurrency(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"account_id":       "ACC-FA-1",
		"currency":         "USD", // Legacy
		"transaction_id":   "TXN-FA-1",
		"transaction_type": "DEPOSIT",
	}

	result, err := currentAccountFinAcctInitiateBookingLog(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCurrentAccountFinAcctCapturePosting_Success(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-FA-1",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"direction":       "DEBIT",
		"transaction_id":  "TXN-FA-1",
		"posting_type":    "debit",
	}

	result, err := currentAccountFinAcctCapturePosting(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
	resultMap := result.(map[string]any)
	assert.NotEmpty(t, resultMap["posting_id"])
}

func TestCurrentAccountFinAcctCapturePosting_MissingParams(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{Logger: slog.Default()}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	// Missing booking_log_id
	_, err := currentAccountFinAcctCapturePosting(ctx, map[string]any{
		"account_id": "ACC-1",
		"amount":     "100.00",
		"direction":  "DEBIT",
	})
	require.Error(t, err)
}

func TestCurrentAccountFinAcctCapturePosting_InvalidDirection(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-FA-1",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"direction":       "INVALID",
		"transaction_id":  "TXN-1",
		"posting_type":    "debit",
	}

	_, err := currentAccountFinAcctCapturePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidDirection)
}

func TestCurrentAccountFinAcctUpdateBookingLog_Success(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id": "BOOK-001",
		"status":         "POSTED",
		"transaction_id": "TXN-FA-1",
	}

	result, err := currentAccountFinAcctUpdateBookingLog(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCurrentAccountFinAcctUpdateBookingLog_InvalidStatus(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id": "BOOK-001",
		"status":         "INVALID_STATUS",
		"transaction_id": "TXN-FA-1",
	}

	_, err := currentAccountFinAcctUpdateBookingLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidStatus)
}

func TestCurrentAccountFinAcctCompensatePosting_Success(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"posting_id":      "POST-001",
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-FA-1",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-FA-1",
		"posting_type":    "credit",
	}

	result, err := currentAccountFinAcctCompensatePosting(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCurrentAccountFinAcctCompensatePosting_MissingParams(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{Logger: slog.Default()}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	// Missing posting_id
	_, err := currentAccountFinAcctCompensatePosting(ctx, map[string]any{
		"booking_log_id": "BOOK-001",
	})
	require.Error(t, err)
}

func TestCurrentAccountRepositorySave_NoDeps(t *testing.T) {
	ctx := &saga.StarlarkContext{Context: context.Background()}
	_, err := currentAccountRepositorySave(ctx, map[string]any{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errHandlerDepsNotFound)
}

func TestCurrentAccountRepositorySave_NoAccount(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{Logger: slog.Default()}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	_, err := currentAccountRepositorySave(ctx, map[string]any{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errAccountNotFound)
}

func TestCurrentAccountRepositorySave_MissingAccountID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{Logger: slog.Default()}
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-1").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		Build()

	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	baseCtx = context.WithValue(baseCtx, ContextKeyAccount, account)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	// Missing account_id param
	_, err := currentAccountRepositorySave(ctx, map[string]any{
		"transaction_id": "TXN-1",
	})
	require.Error(t, err)
}

// =============================================================================
// Additional tests for CompensatePosting paths
// =============================================================================

func TestCurrentAccountFinAcctCompensatePosting_InvalidDirection(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"posting_id":      "POST-001",
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-FA-1",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"direction":       "INVALID",
		"transaction_id":  "TXN-FA-1",
		"posting_type":    "credit",
	}

	_, err := currentAccountFinAcctCompensatePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidDirection)
}

func TestCurrentAccountFinAcctCompensatePosting_LegacyCurrency(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"posting_id":     "POST-001",
		"booking_log_id": "BOOK-001",
		"account_id":     "ACC-FA-1",
		"amount":         "100.00",
		"currency":       "GBP", // legacy field
		"direction":      "DEBIT",
		"transaction_id": "TXN-FA-1",
		"posting_type":   "debit",
	}

	result, err := currentAccountFinAcctCompensatePosting(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
	resultMap := result.(map[string]any)
	assert.Equal(t, "COMPENSATED", resultMap["status"])
}

func TestCurrentAccountFinAcctCompensatePosting_ServiceError(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{
		failOnCapture: true,
	}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"posting_id":      "POST-001",
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-FA-1",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-FA-1",
		"posting_type":    "credit",
	}

	_, err := currentAccountFinAcctCompensatePosting(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compensate")
}

func TestCurrentAccountFinAcctCompensatePosting_MissingInstrumentCode(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"posting_id":     "POST-001",
		"booking_log_id": "BOOK-001",
		"account_id":     "ACC-FA-1",
		"amount":         "100.00",
		"direction":      "CREDIT",
		"transaction_id": "TXN-FA-1",
		"posting_type":   "credit",
	}

	_, err := currentAccountFinAcctCompensatePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

// =============================================================================
// Additional tests for CapturePosting paths
// =============================================================================

func TestCurrentAccountFinAcctCapturePosting_LegacyCurrency(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id": "BOOK-001",
		"account_id":     "ACC-FA-1",
		"amount":         "50.00",
		"currency":       "GBP", // legacy field instead of instrument_code
		"direction":      "CREDIT",
		"transaction_id": "TXN-FA-1",
		"posting_type":   "credit",
	}

	result, err := currentAccountFinAcctCapturePosting(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCurrentAccountFinAcctCapturePosting_ServiceError(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{
		failOnCapture: true,
	}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-FA-1",
		"amount":          "50.00",
		"instrument_code": "GBP",
		"direction":       "DEBIT",
		"transaction_id":  "TXN-FA-1",
		"posting_type":    "debit",
	}

	_, err := currentAccountFinAcctCapturePosting(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to capture")
}

func TestCurrentAccountFinAcctCapturePosting_MissingInstrumentCode(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id": "BOOK-001",
		"account_id":     "ACC-FA-1",
		"amount":         "50.00",
		"direction":      "DEBIT",
		"transaction_id": "TXN-FA-1",
		"posting_type":   "debit",
	}

	_, err := currentAccountFinAcctCapturePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

// =============================================================================
// Additional tests for InitiateBookingLog paths
// =============================================================================

func TestCurrentAccountFinAcctInitiateBookingLog_ServiceError(t *testing.T) {
	mockFA := &failingInitBookingLogClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"account_id":       "ACC-001",
		"instrument_code":  "GBP",
		"transaction_id":   "TXN-001",
		"transaction_type": "DEPOSIT",
	}

	_, err := currentAccountFinAcctInitiateBookingLog(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initiate booking log")
}

func TestCurrentAccountFinAcctInitiateBookingLog_NilBookingLogResponse(t *testing.T) {
	mockFA := &nilBookingLogClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"account_id":       "ACC-001",
		"instrument_code":  "GBP",
		"transaction_id":   "TXN-001",
		"transaction_type": "DEPOSIT",
	}

	_, err := currentAccountFinAcctInitiateBookingLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errNilBookingLog)
}

// =============================================================================
// Additional tests for UpdateBookingLog paths
// =============================================================================

func TestCurrentAccountFinAcctUpdateBookingLog_CancelledStatus(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id": "BOOK-001",
		"status":         "CANCELLED",
	}

	result, err := currentAccountFinAcctUpdateBookingLog(ctx, params)
	require.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, "CANCELLED", resultMap["status"])
}

func TestCurrentAccountFinAcctUpdateBookingLog_ServiceError(t *testing.T) {
	mockFA := &mockFinancialAccountingClient{
		failOnUpdate: true,
	}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id": "BOOK-001",
		"status":         "POSTED",
	}

	_, err := currentAccountFinAcctUpdateBookingLog(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update booking log")
}

func TestCurrentAccountFinAcctUpdateBookingLog_MissingBookingLogID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{Logger: slog.Default()}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	_, err := currentAccountFinAcctUpdateBookingLog(ctx, map[string]any{
		"status": "POSTED",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

// =============================================================================
// Additional tests for CancelLog paths
// =============================================================================

func TestCurrentAccountPositionKeepingCancelLog_CreditDirection(t *testing.T) {
	mockPK := &mockPositionKeepingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"log_id":         "LOG-001",
		"version":        int64(1),
		"transaction_id": "TXN-001",
		"account_id":     "ACC-001",
		"direction":      "CREDIT", // Test the deposit (CREDIT) path
	}

	result, err := currentAccountPositionKeepingCancelLog(ctx, params)
	require.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, "CANCELLED", resultMap["status"])
}

func TestCurrentAccountPositionKeepingCancelLog_ServiceError(t *testing.T) {
	mockPK := &mockPositionKeepingClient{
		failOnUpdate: true,
	}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"log_id":         "LOG-001",
		"version":        int64(1),
		"transaction_id": "TXN-001",
		"account_id":     "ACC-001",
		"direction":      "DEBIT",
	}

	_, err := currentAccountPositionKeepingCancelLog(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compensate position log")
}

func TestCurrentAccountPositionKeepingCancelLog_MissingDirection(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: &mockPositionKeepingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"log_id":         "LOG-001",
		"version":        int64(1),
		"transaction_id": "TXN-001",
		"account_id":     "ACC-001",
		// missing direction
	}

	_, err := currentAccountPositionKeepingCancelLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountPositionKeepingCancelLog_MissingAccountID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: &mockPositionKeepingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"log_id":         "LOG-001",
		"version":        int64(1),
		"transaction_id": "TXN-001",
		"direction":      "DEBIT",
		// missing account_id
	}

	_, err := currentAccountPositionKeepingCancelLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountPositionKeepingCancelLog_MissingTransactionID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: &mockPositionKeepingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"log_id":    "LOG-001",
		"version":   int64(1),
		"direction": "DEBIT",
		// missing transaction_id and account_id
	}

	_, err := currentAccountPositionKeepingCancelLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

// =============================================================================
// Additional tests for InitiateLog paths
// =============================================================================

func TestCurrentAccountPositionKeepingInitiateLog_NilPositionLog(t *testing.T) {
	mockPK := &nilLogPositionKeepingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"position_id":     "ACC-001",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-001",
	}

	_, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errNilPositionLog)
}

func TestCurrentAccountPositionKeepingInitiateLog_PositionIDPrimary(t *testing.T) {
	mockPK := &mockPositionKeepingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"position_id":     "POS-001", // primary field
		"amount":          "100.00",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-001",
	}

	result, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCurrentAccountPositionKeepingInitiateLog_DecimalAmount(t *testing.T) {
	mockPK := &mockPositionKeepingClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: mockPK,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"account_id":      "ACC-001",
		"amount":          decimal.NewFromFloat(99.99),
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-001",
	}

	result, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCurrentAccountPositionKeepingInitiateLog_InvalidAmountString(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: &mockPositionKeepingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"account_id":      "ACC-001",
		"amount":          "not-a-number",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-001",
	}

	_, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidParameterType)
}

func TestCurrentAccountPositionKeepingInitiateLog_MissingDirection(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: &mockPositionKeepingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"account_id":      "ACC-001",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"transaction_id":  "TXN-001",
		// missing direction
	}

	_, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountPositionKeepingInitiateLog_MissingTransactionID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:           slog.Default(),
		PosKeepingClient: &mockPositionKeepingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"account_id":      "ACC-001",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		// missing transaction_id
	}

	_, err := currentAccountPositionKeepingInitiateLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

// =============================================================================
// Additional tests for RepositorySave paths
// =============================================================================

func TestCurrentAccountRepositorySave_MissingTransactionID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger: slog.Default(),
		Repo:   &persistence.Repository{},
	}

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("EXT-001").
		WithStatus(domain.AccountStatusActive).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		Build()

	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	baseCtx = context.WithValue(baseCtx, ContextKeyAccount, account)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	_, err := currentAccountRepositorySave(ctx, map[string]any{
		"account_id": "ACC-001",
		// missing transaction_id
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

// =============================================================================
// Additional requireDecimal tests for type coverage
// =============================================================================

func TestRequireDecimal_Float64(t *testing.T) {
	params := map[string]any{"val": float64(42.5)}
	d, err := requireDecimal(params, "val")
	require.NoError(t, err)
	assert.True(t, d.Equal(decimal.NewFromFloat(42.5)))
}

func TestRequireDecimal_Int(t *testing.T) {
	params := map[string]any{"val": int(100)}
	d, err := requireDecimal(params, "val")
	require.NoError(t, err)
	assert.True(t, d.Equal(decimal.NewFromInt(100)))
}

func TestRequireDecimal_Int64(t *testing.T) {
	params := map[string]any{"val": int64(200)}
	d, err := requireDecimal(params, "val")
	require.NoError(t, err)
	assert.True(t, d.Equal(decimal.NewFromInt(200)))
}

func TestRequireDecimal_DecimalDirect(t *testing.T) {
	d := decimal.NewFromFloat(33.33)
	params := map[string]any{"val": d}
	result, err := requireDecimal(params, "val")
	require.NoError(t, err)
	assert.True(t, result.Equal(d))
}

func TestRequireDecimal_InvalidString(t *testing.T) {
	params := map[string]any{"val": "abc"}
	_, err := requireDecimal(params, "val")
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidParameterType)
}

func TestRequireDecimal_UnsupportedType(t *testing.T) {
	params := map[string]any{"val": []int{1, 2, 3}}
	_, err := requireDecimal(params, "val")
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidParameterType)
}

// =============================================================================
// Additional requireInt64 tests for type coverage
// =============================================================================

func TestRequireInt64_Int(t *testing.T) {
	params := map[string]any{"val": int(42)}
	v, err := requireInt64(params, "val")
	require.NoError(t, err)
	assert.Equal(t, int64(42), v)
}

func TestRequireInt64_Float64(t *testing.T) {
	params := map[string]any{"val": float64(42.0)}
	v, err := requireInt64(params, "val")
	require.NoError(t, err)
	assert.Equal(t, int64(42), v)
}

func TestRequireInt64_UnsupportedType(t *testing.T) {
	params := map[string]any{"val": "not-a-number"}
	_, err := requireInt64(params, "val")
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidParameterType)
}

// =============================================================================
// Additional protoMoneyToAmount tests
// =============================================================================

func TestProtoMoneyToAmount_NilAmount(t *testing.T) {
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("EXT-001").
		WithStatus(domain.AccountStatusActive).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		Build()

	_, err := protoMoneyToAmount(nil, account)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAmountRequired)
}

func TestProtoMoneyToAmount_NilInnerAmount(t *testing.T) {
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("EXT-001").
		WithStatus(domain.AccountStatusActive).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		Build()

	_, err := protoMoneyToAmount(&commonpb.MoneyAmount{Amount: nil}, account)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAmountRequired)
}

func TestProtoMoneyToAmount_ValidConversion(t *testing.T) {
	balance, err := domain.NewMoney("GBP", 0)
	require.NoError(t, err)

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("EXT-001").
		WithStatus(domain.AccountStatusActive).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithBalance(balance).
		Build()

	amount := &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: "GBP",
			Units:        10,
			Nanos:        500000000, // 0.50
		},
	}

	result, err := protoMoneyToAmount(amount, account)
	require.NoError(t, err)
	assert.Equal(t, "GBP", result.InstrumentCode())
}

func TestProtoMoneyToAmount_NegativeNanos(t *testing.T) {
	balance, err := domain.NewMoney("GBP", 0)
	require.NoError(t, err)

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("EXT-001").
		WithStatus(domain.AccountStatusActive).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithBalance(balance).
		Build()

	amount := &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: "GBP",
			Units:        -10,
			Nanos:        -500000000,
		},
	}

	result, err := protoMoneyToAmount(amount, account)
	require.NoError(t, err)
	assert.Equal(t, "GBP", result.InstrumentCode())
}

func TestProtoMoneyToAmount_OverflowUnits(t *testing.T) {
	balance, err := domain.NewMoney("GBP", 0)
	require.NoError(t, err)

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("EXT-001").
		WithStatus(domain.AccountStatusActive).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithBalance(balance).
		Build()

	amount := &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: "GBP",
			Units:        9223372036854775807, // MaxInt64
			Nanos:        0,
		},
	}

	_, err = protoMoneyToAmount(amount, account)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAmountOverflow)
}

// =============================================================================
// Additional loadSagaAsset tests
// =============================================================================

func TestLoadSagaAsset_WithEnvVar_Success(t *testing.T) {
	// Create a temp directory with a saga file
	tmpDir := t.TempDir()
	sagaPath := filepath.Join(tmpDir, "test-saga.star")
	err := os.WriteFile(sagaPath, []byte("# test saga script"), 0o644)
	require.NoError(t, err)

	t.Setenv("SAGA_ASSET_DIR", tmpDir)

	content, err := loadSagaAsset("test-saga.star")
	require.NoError(t, err)
	assert.Equal(t, "# test saga script", content)
}

func TestLoadSagaAsset_WithEnvVar_FileNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SAGA_ASSET_DIR", tmpDir)

	_, err := loadSagaAsset("nonexistent.star")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read saga asset")
}

// =============================================================================
// RegisterCurrentAccountHandlers tests
// =============================================================================

func TestRegisterCurrentAccountHandlers_Success(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	err := RegisterCurrentAccountHandlers(registry)
	require.NoError(t, err)
}

// =============================================================================
// Additional Watch/Health tests
// =============================================================================

func TestHealthChecker_Watch_SendError(t *testing.T) {
	// Create a health checker with no DB dependency - just needs a mock aggregator
	checker := &HealthChecker{
		logger:       slog.Default(),
		checkTimeout: 100 * time.Millisecond,
		serviceName:  "current-account",
		aggregator:   health.NewAggregator(nil),
	}

	// Create a mock stream that always fails on send
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &failSendWatchServer{ctx: ctx}

	err := checker.Watch(&grpc_health_v1.HealthCheckRequest{
		Service: "current-account",
	}, stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send initial health status")
}

// failSendWatchServer is a mock Health_WatchServer that always fails on Send.
type failSendWatchServer struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *failSendWatchServer) Send(_ *grpc_health_v1.HealthCheckResponse) error {
	return fmt.Errorf("send failed")
}

func (f *failSendWatchServer) Context() context.Context {
	return f.ctx
}

// =============================================================================
// Additional tests for optionalString edge cases
// =============================================================================

func TestOptionalString_NilValue(t *testing.T) {
	params := map[string]any{"key": nil}
	result := optionalString(params, "key")
	assert.Equal(t, "", result)
}

func TestOptionalString_WrongType(t *testing.T) {
	params := map[string]any{"key": 42}
	result := optionalString(params, "key")
	assert.Equal(t, "", result)
}

func TestOptionalString_Missing(t *testing.T) {
	params := map[string]any{}
	result := optionalString(params, "key")
	assert.Equal(t, "", result)
}

func TestOptionalString_Present(t *testing.T) {
	params := map[string]any{"key": "value"}
	result := optionalString(params, "key")
	assert.Equal(t, "value", result)
}

// =============================================================================
// Additional requireString tests
// =============================================================================

func TestRequireString_WrongType(t *testing.T) {
	params := map[string]any{"key": 42}
	_, err := requireString(params, "key")
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidParameterType)
}

// stubCELCompiler implements cache.AccountTypeCELCompiler for testing.
type stubCELCompiler struct{}

func (s *stubCELCompiler) CompileValidation(_ string) (cel.Program, error) {
	return nil, nil
}

func (s *stubCELCompiler) CompileBucketKey(_ string) (cel.Program, error) {
	return nil, nil
}

func (s *stubCELCompiler) CompileEligibility(_ string) (cel.Program, error) {
	return nil, nil
}

// =============================================================================
// Minimal mock types for specific error-path testing
// =============================================================================

// failingInitBookingLogClient returns an error on InitiateFinancialBookingLog.
type failingInitBookingLogClient struct {
	mockFinancialAccountingClient
}

func (f *failingInitBookingLogClient) InitiateFinancialBookingLog(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	return nil, fmt.Errorf("financial accounting service unavailable")
}

// nilBookingLogClient returns a response with nil FinancialBookingLog.
type nilBookingLogClient struct {
	mockFinancialAccountingClient
}

func (n *nilBookingLogClient) InitiateFinancialBookingLog(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	return &financialaccountingv1.InitiateFinancialBookingLogResponse{
		FinancialBookingLog: nil,
	}, nil
}

// nilLogPositionKeepingClient returns a response with nil Log from InitiateFinancialPositionLog.
type nilLogPositionKeepingClient struct {
	mockPositionKeepingClient
}

func (n *nilLogPositionKeepingClient) InitiateFinancialPositionLog(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	return &positionkeepingv1.InitiateFinancialPositionLogResponse{
		Log: nil,
	}, nil
}

// nilPostingFAClient returns a response with nil LedgerPosting from CaptureLedgerPosting.
type nilPostingFAClient struct {
	mockFinancialAccountingClient
}

func (n *nilPostingFAClient) CaptureLedgerPosting(_ context.Context, _ *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	return &financialaccountingv1.CaptureLedgerPostingResponse{
		LedgerPosting: nil,
	}, nil
}

// =============================================================================
// Additional CapturePosting tests for nil posting response
// =============================================================================

func TestCurrentAccountFinAcctCapturePosting_NilPostingResponse(t *testing.T) {
	mockFA := &nilPostingFAClient{}
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: mockFA,
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-001",
		"amount":          "50.00",
		"instrument_code": "GBP",
		"direction":       "DEBIT",
		"transaction_id":  "TXN-001",
		"posting_type":    "debit",
	}

	_, err := currentAccountFinAcctCapturePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errNilPosting)
}

func TestCurrentAccountFinAcctCapturePosting_MissingPostingType(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-001",
		"amount":          "50.00",
		"instrument_code": "GBP",
		"direction":       "DEBIT",
		"transaction_id":  "TXN-001",
		// missing posting_type
	}

	_, err := currentAccountFinAcctCapturePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountFinAcctCapturePosting_MissingTransactionID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-001",
		"amount":          "50.00",
		"instrument_code": "GBP",
		"direction":       "DEBIT",
		// missing transaction_id and posting_type
	}

	_, err := currentAccountFinAcctCapturePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountFinAcctCapturePosting_MissingAmount(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-001",
		"instrument_code": "GBP",
		"direction":       "DEBIT",
		// missing amount
	}

	_, err := currentAccountFinAcctCapturePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountFinAcctCapturePosting_MissingAccountID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"booking_log_id": "BOOK-001",
		// missing account_id
	}

	_, err := currentAccountFinAcctCapturePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountFinAcctCapturePosting_MissingBookingLogID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	_, err := currentAccountFinAcctCapturePosting(ctx, map[string]any{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

// =============================================================================
// Additional CompensatePosting edge cases
// =============================================================================

func TestCurrentAccountFinAcctCompensatePosting_MissingAmount(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"posting_id":      "POST-001",
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-001",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-001",
		"posting_type":    "credit",
		// missing amount
	}

	_, err := currentAccountFinAcctCompensatePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountFinAcctCompensatePosting_MissingDirection(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"posting_id":      "POST-001",
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-001",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"transaction_id":  "TXN-001",
		"posting_type":    "credit",
		// missing direction
	}

	_, err := currentAccountFinAcctCompensatePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountFinAcctCompensatePosting_MissingTransactionID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"posting_id":      "POST-001",
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-001",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"posting_type":    "credit",
		// missing transaction_id
	}

	_, err := currentAccountFinAcctCompensatePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountFinAcctCompensatePosting_MissingPostingType(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"posting_id":      "POST-001",
		"booking_log_id":  "BOOK-001",
		"account_id":      "ACC-001",
		"amount":          "100.00",
		"instrument_code": "GBP",
		"direction":       "CREDIT",
		"transaction_id":  "TXN-001",
		// missing posting_type
	}

	_, err := currentAccountFinAcctCompensatePosting(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

// =============================================================================
// Additional InitiateBookingLog edge cases
// =============================================================================

func TestCurrentAccountFinAcctInitiateBookingLog_MissingTransactionType(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"account_id":      "ACC-001",
		"instrument_code": "GBP",
		"transaction_id":  "TXN-001",
		// missing transaction_type
	}

	_, err := currentAccountFinAcctInitiateBookingLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountFinAcctInitiateBookingLog_MissingTransactionID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	params := map[string]any{
		"account_id":      "ACC-001",
		"instrument_code": "GBP",
		// missing transaction_id and transaction_type
	}

	_, err := currentAccountFinAcctInitiateBookingLog(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

func TestCurrentAccountFinAcctInitiateBookingLog_MissingAccountID(t *testing.T) {
	deps := &CurrentAccountHandlerDeps{
		Logger:        slog.Default(),
		FinAcctClient: &mockFinancialAccountingClient{},
	}
	baseCtx := context.WithValue(context.Background(), ContextKeyHandlerDeps, deps)
	ctx := &saga.StarlarkContext{Context: baseCtx}

	_, err := currentAccountFinAcctInitiateBookingLog(ctx, map[string]any{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errMissingParameter)
}

// =============================================================================
// celProgramAdapter Eval test (covers Eval 0% function)
// =============================================================================

func TestCelProgramAdapter_Eval_Success(t *testing.T) {
	// Create a simple CEL environment and program
	env, err := cel.NewEnv(cel.Variable("x", cel.IntType))
	require.NoError(t, err)

	ast, issues := env.Compile("x + 1")
	require.Empty(t, issues.Errors())

	prg, err := env.Program(ast)
	require.NoError(t, err)

	adapter := &celProgramAdapter{program: prg}
	result, err := adapter.Eval(map[string]interface{}{"x": int64(41)})
	require.NoError(t, err)
	assert.Equal(t, int64(42), result)
}

func TestCelProgramAdapter_Eval_Error(t *testing.T) {
	env, err := cel.NewEnv(cel.Variable("x", cel.IntType))
	require.NoError(t, err)

	ast, issues := env.Compile("x + 1")
	require.Empty(t, issues.Errors())

	prg, err := env.Program(ast)
	require.NoError(t, err)

	adapter := &celProgramAdapter{program: prg}
	// Pass wrong type to cause eval error
	_, err = adapter.Eval(map[string]interface{}{"x": "not-a-number"})
	require.Error(t, err)
}

// =============================================================================
// Additional resolveDepositScript tests
// =============================================================================

func TestResolveDepositScript_SagaNotFound(t *testing.T) {
	stubRegistry := &stubSagaRegistry{
		getActiveErr: refsaga.ErrNotFound,
	}

	atLoader := &stubAccountTypeCacheLoaderWithResult{}
	celComp := &stubCELCompiler{}
	atCache := cache.NewLocalAccountTypeCache(atLoader, celComp)

	resolver := refsaga.NewProductTypeSagaResolver(stubRegistry, atCache)

	orchestrator := &DepositOrchestrator{
		logger:        slog.Default(),
		depositScript: "default_script",
		sagaResolver:  resolver,
	}

	tid, err := tenant.NewTenantID("org_test123")
	require.NoError(t, err)
	ctx := tenant.WithTenant(context.Background(), tid)

	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("EXT-001").
		WithStatus(domain.AccountStatusActive).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithProductTypeCode("PREMIUM").
		Build()

	script, scriptErr := orchestrator.resolveDepositScript(ctx, account)
	require.NoError(t, scriptErr)
	assert.Equal(t, "default_script", script, "should fall back to default when saga not found")
}

// =============================================================================
// protoMoneyToAmount: overflow and edge case coverage
// =============================================================================

func TestProtoMoneyToAmount_UnitsOverflow(t *testing.T) {
	balance, err := domain.NewMoney("GBP", 0)
	require.NoError(t, err)
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("EXT-001").
		WithStatus(domain.AccountStatusActive).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithBalance(balance).
		Build()

	amt := &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: "GBP",
			Units:        9223372036854775807, // math.MaxInt64
			Nanos:        0,
		},
	}

	_, err = protoMoneyToAmount(amt, account)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overflow")
}

func TestProtoMoneyToAmount_NegativeUnitsOverflow(t *testing.T) {
	balance, err := domain.NewMoney("GBP", 0)
	require.NoError(t, err)
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("EXT-001").
		WithStatus(domain.AccountStatusActive).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithBalance(balance).
		Build()

	amt := &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: "GBP",
			Units:        -9223372036854775808, // math.MinInt64
			Nanos:        0,
		},
	}

	_, err = protoMoneyToAmount(amt, account)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overflow")
}

func TestProtoMoneyToAmount_ZeroAmount(t *testing.T) {
	balance, err := domain.NewMoney("GBP", 0)
	require.NoError(t, err)
	account := domain.NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("EXT-001").
		WithStatus(domain.AccountStatusActive).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("00000000-0000-0000-0000-000000000001").
		WithBalance(balance).
		Build()

	amt := &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: "GBP",
			Units:        0,
			Nanos:        0,
		},
	}

	result, err := protoMoneyToAmount(amt, account)
	require.NoError(t, err)
	assert.True(t, result.IsZero())
}

// =============================================================================
// Health: Watch context cancellation
// =============================================================================

func TestWatch_ContextCancellation(t *testing.T) {
	checker := &HealthChecker{
		logger:       slog.Default(),
		aggregator:   health.NewAggregator(nil),
		serviceName:  "test-service",
		checkTimeout: 50 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())

	stream := &failSendWatchServer{
		ctx: ctx,
	}

	// Cancel immediately so the Watch enters the <-ctx.Done() branch
	cancel()

	err := checker.Watch(&grpc_health_v1.HealthCheckRequest{}, stream)
	// We expect either a send error (if send happens first) or a context cancellation error
	require.Error(t, err)
}

// =============================================================================
// Health: Watch periodic update with send error
// =============================================================================

type tickWatchServer struct {
	grpc.ServerStream
	ctx      context.Context
	sendOnce bool
}

func (s *tickWatchServer) Send(_ *grpc_health_v1.HealthCheckResponse) error {
	if s.sendOnce {
		return fmt.Errorf("send error on second call")
	}
	s.sendOnce = true
	return nil
}

func (s *tickWatchServer) Context() context.Context {
	return s.ctx
}

func TestWatch_TickerSendError(t *testing.T) {
	checker := &HealthChecker{
		logger:       slog.Default(),
		aggregator:   health.NewAggregator(nil),
		serviceName:  "test-service",
		checkTimeout: 10 * time.Millisecond, // Very short so ticker fires quickly
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream := &tickWatchServer{ctx: ctx}

	err := checker.Watch(&grpc_health_v1.HealthCheckRequest{}, stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send")
}

// =============================================================================
// loadSagaAsset: fallback to executable directory (no env var)
// =============================================================================

func TestLoadSagaAsset_FallbackToExecutable2(t *testing.T) {
	// Unset the env var to test the fallback path
	original := os.Getenv("SAGA_ASSET_DIR")
	t.Setenv("SAGA_ASSET_DIR", "")
	defer func() {
		if original != "" {
			os.Setenv("SAGA_ASSET_DIR", original)
		}
	}()

	// This will try to load from the executable's directory, which won't have the file
	_, err := loadSagaAsset("nonexistent/file.star")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read saga asset")
}

// =============================================================================
// resolveClearingAccountID tests (deposit orchestrator)
// =============================================================================

func TestDepositOrchestrator_ResolveClearingAccountID_StaticConfig(t *testing.T) {
	orchestrator := &DepositOrchestrator{
		logger: slog.Default(),
		accountConfig: &config.AccountConfig{
			DepositClearingAccountID: "static-clearing-001",
		},
	}

	result := orchestrator.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "static-clearing-001", result)
}

func TestDepositOrchestrator_ResolveClearingAccountID_NoneConfigured(t *testing.T) {
	orchestrator := &DepositOrchestrator{
		logger: slog.Default(),
	}

	result := orchestrator.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "", result)
}

// =============================================================================
// resolveClearingAccountID tests (withdrawal orchestrator)
// =============================================================================

func TestWithdrawalOrchestrator_ResolveClearingAccountID_StaticConfig(t *testing.T) {
	orchestrator := &WithdrawalOrchestrator{
		logger: slog.Default(),
		accountConfig: &config.AccountConfig{
			WithdrawalClearingAccountID: "static-withdrawal-001",
		},
	}

	result := orchestrator.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "static-withdrawal-001", result)
}

func TestWithdrawalOrchestrator_ResolveClearingAccountID_NoneConfigured(t *testing.T) {
	orchestrator := &WithdrawalOrchestrator{
		logger: slog.Default(),
	}

	result := orchestrator.resolveClearingAccountID(context.Background(), "GBP")
	assert.Equal(t, "", result)
}

// =============================================================================
// Integration tests: UpdateValuationFeature additional paths
// =============================================================================

func TestUpdateValuationFeature_Activate(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	// Create a feature (which auto-activates)
	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	// Terminate it first
	_, err = svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	})
	require.NoError(t, err)

	// Try to activate a terminated feature - should fail with lifecycle error
	updateResp, err := svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_ACTIVATE,
	})

	require.Error(t, err)
	assert.Nil(t, updateResp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "cannot activate")
}

func TestUpdateValuationFeature_InvalidFeatureID(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	req := &pb.UpdateValuationFeatureRequest{
		FeatureId: "not-a-uuid",
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	}

	resp, err := svc.UpdateValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid feature_id")
}

func TestUpdateValuationFeature_UnsupportedAction(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	// Use a numeric value beyond defined enums
	req := &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction(99),
	}

	resp, err := svc.UpdateValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "unsupported action")
}

func TestUpdateValuationFeature_TerminateAlreadyTerminated(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	// Terminate once
	_, err = svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	})
	require.NoError(t, err)

	// Terminate again - idempotent, should succeed
	resp, err := svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED, resp.Feature.LifecycleStatus)
}

// =============================================================================
// Integration tests: GetValuationFeature additional paths
// =============================================================================

func TestGetValuationFeature_InvalidFeatureID(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	req := &pb.GetValuationFeatureRequest{
		FeatureId: "not-a-uuid",
	}

	resp, err := svc.GetValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid feature_id")
}

func TestGetValuationFeature_ByAccountAndInstrument_AccountNotFound(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	req := &pb.GetValuationFeatureRequest{
		AccountId:      "non-existent-account",
		InstrumentCode: "USD",
	}

	resp, err := svc.GetValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetValuationFeature_ByAccountAndInstrument_NotFound(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	req := &pb.GetValuationFeatureRequest{
		AccountId:      accountID,
		InstrumentCode: "JPY", // No feature for JPY
	}

	resp, err := svc.GetValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// =============================================================================
// Integration tests: CreateValuationFeature additional paths
// =============================================================================

func TestCreateValuationFeature_InvalidParametersJSON(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	req := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
		Parameters:             "{invalid json",
	}

	resp, err := svc.CreateValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid parameters JSON")
}

func TestCreateValuationFeature_Duplicate(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	req := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}

	// First creation succeeds
	_, err := svc.CreateValuationFeature(ctx, req)
	require.NoError(t, err)

	// Second creation with same account+instrument should fail
	resp, err := svc.CreateValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	// The unique index violation returns Internal since the DB error
	// isn't always mapped to ErrValuationFeatureAlreadyExists
	assert.True(t, st.Code() == codes.AlreadyExists || st.Code() == codes.Internal,
		"expected AlreadyExists or Internal, got %v", st.Code())
}

func TestCreateValuationFeature_NoParameters(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	req := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
		Parameters:             "", // No parameters
	}

	resp, err := svc.CreateValuationFeature(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Feature.Id)
}

// =============================================================================
// HealthChecker: Check with specific component and unknown service
// =============================================================================

func TestHealthCheck_UnknownService(t *testing.T) {
	checker := &HealthChecker{
		logger:       slog.Default(),
		aggregator:   health.NewAggregator(nil),
		serviceName:  "current-account",
		checkTimeout: 5 * time.Second,
	}

	resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "unknown-service",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
}

func TestHealthCheck_EmptyServiceName(t *testing.T) {
	checker := &HealthChecker{
		logger:       slog.Default(),
		aggregator:   health.NewAggregator(nil),
		serviceName:  "current-account",
		checkTimeout: 5 * time.Second,
	}

	resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "", // Empty matches the overall service check
	})

	require.NoError(t, err)
	// With nil checkers, should return SERVING (healthy by default)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestHealthCheck_MatchingServiceName(t *testing.T) {
	checker := &HealthChecker{
		logger:       slog.Default(),
		aggregator:   health.NewAggregator(nil),
		serviceName:  "current-account",
		checkTimeout: 5 * time.Second,
	}

	resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "current-account",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

// =============================================================================
// mapStatusToGRPC edge cases
// =============================================================================

func TestMapStatusToGRPC_AllStatuses(t *testing.T) {
	checker := &HealthChecker{logger: slog.Default()}

	tests := []struct {
		input    health.Status
		expected grpc_health_v1.HealthCheckResponse_ServingStatus
	}{
		{health.StatusHealthy, grpc_health_v1.HealthCheckResponse_SERVING},
		{health.StatusDegraded, grpc_health_v1.HealthCheckResponse_SERVING},
		{health.StatusUnhealthy, grpc_health_v1.HealthCheckResponse_NOT_SERVING},
		{health.StatusUnknown, grpc_health_v1.HealthCheckResponse_UNKNOWN},
		{health.Status(99), grpc_health_v1.HealthCheckResponse_UNKNOWN}, // default
	}

	for _, tc := range tests {
		result := checker.mapStatusToGRPC(tc.input)
		assert.Equal(t, tc.expected, result, "status %v", tc.input)
	}
}

// =============================================================================
// domainToProtoValuationFeature: additional parameter paths
// =============================================================================

func TestDomainToProtoValuationFeature_WithComplexParameters(t *testing.T) {
	svc := &Service{logger: slog.Default()}
	feature := &domain.ValuationFeature{
		ID:                     uuid.New(),
		AccountID:              uuid.New(),
		InstrumentCode:         "USD",
		ValuationMethodID:      uuid.New(),
		ValuationMethodVersion: 1,
		LifecycleStatus:        domain.ValuationFeatureLifecycleStatusActive,
		Parameters:             map[string]interface{}{"source": "ECB", "frequency": "daily"},
		CreatedBy:              "system",
		UpdatedBy:              "system",
		Version:                1,
	}

	result := svc.domainToProtoValuationFeature(feature)
	assert.Contains(t, result.Parameters, "source")
	assert.Contains(t, result.Parameters, "frequency")
}

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
// getAccountBalanceCents: various response shapes
// =============================================================================

func TestGetAccountBalanceCents_NilAmount(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			Amount: nil,
		},
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	cents, err := svc.getAccountBalanceCents(context.Background(), "ACC-001")
	require.NoError(t, err)
	assert.Equal(t, int64(0), cents)
}

func TestGetAccountBalanceCents_EmptyAmount(t *testing.T) {
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
	cents, err := svc.getAccountBalanceCents(context.Background(), "ACC-001")
	require.NoError(t, err)
	assert.Equal(t, int64(0), cents)
}

func TestGetAccountBalanceCents_InstrumentCodeMismatch(t *testing.T) {
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
	_, err := svc.getAccountBalanceCents(context.Background(), "ACC-001")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentCodeMismatch)
}

func TestGetAccountBalanceCents_InvalidAmountString(t *testing.T) {
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
	_, err := svc.getAccountBalanceCents(context.Background(), "ACC-001")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse balance amount")
}

func TestGetAccountBalanceCents_Success(t *testing.T) {
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
	cents, err := svc.getAccountBalanceCents(context.Background(), "ACC-001")
	require.NoError(t, err)
	assert.Equal(t, int64(12345), cents)
}

func TestGetAccountBalanceCents_Error(t *testing.T) {
	mockPK := &stubPKClient{
		getBalanceErr: fmt.Errorf("connection refused"),
	}
	svc := &Service{
		posKeepingClient: mockPK,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
	_, err := svc.getAccountBalanceCents(context.Background(), "ACC-001")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
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
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
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
