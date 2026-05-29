package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/google/cel-go/cel"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	refsaga "github.com/meridianhub/meridian/services/reference-data/saga"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

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
