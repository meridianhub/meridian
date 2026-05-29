package service

import (
	"context"
	"log/slog"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
