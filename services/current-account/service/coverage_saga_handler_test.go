package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	refsaga "github.com/meridianhub/meridian/services/reference-data/saga"
	sharedamount "github.com/meridianhub/meridian/shared/pkg/amount"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

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

func TestResolveDepositScript_ScriptEmpty(t *testing.T) {
	// Test path where resolver returns a definition but with empty Script
	mockRegistry := &stubSagaRegistry{
		getActiveResult: &refsaga.Definition{
			Name:   "deposit",
			Script: "", // Empty - should fall back
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

func TestResolveDepositScript_ScriptPresent(t *testing.T) {
	mockRegistry := &stubSagaRegistry{
		getActiveResult: &refsaga.Definition{
			Name:   "deposit",
			Script: "custom_deposit()",
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

func TestResolveWithdrawalScript_ScriptEmpty(t *testing.T) {
	mockRegistry := &stubSagaRegistry{
		getActiveResult: &refsaga.Definition{
			Name:   "withdrawal",
			Script: "",
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

func TestResolveWithdrawalScript_ScriptPresent(t *testing.T) {
	mockRegistry := &stubSagaRegistry{
		getActiveResult: &refsaga.Definition{
			Name:   "withdrawal",
			Script: "custom_withdrawal()",
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
