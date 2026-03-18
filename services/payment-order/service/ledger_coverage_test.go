package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/services/payment-order/domain/testfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearingMockInternalAccountClient returns a mock that provides a valid clearing account.
func clearingMockInternalAccountClient() *mockInternalAccountClient {
	return &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{
					AccountId:       "clearing-acc-001",
					AccountCode:     "CLR-GBP-SETTLEMENT",
					Name:            "GBP Settlement Clearing",
					ClearingPurpose: internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT,
				},
			},
		},
	}
}

// =============================================================================
// extractInt64Param
// =============================================================================

func TestExtractInt64Param_Int64(t *testing.T) {
	t.Parallel()
	params := map[string]any{"key": int64(42)}
	v, err := extractInt64Param(params, "key")
	require.NoError(t, err)
	assert.Equal(t, int64(42), v)
}

func TestExtractInt64Param_Int(t *testing.T) {
	t.Parallel()
	params := map[string]any{"key": 42}
	v, err := extractInt64Param(params, "key")
	require.NoError(t, err)
	assert.Equal(t, int64(42), v)
}

func TestExtractInt64Param_Float64(t *testing.T) {
	t.Parallel()
	params := map[string]any{"key": float64(42.9)}
	v, err := extractInt64Param(params, "key")
	require.NoError(t, err)
	assert.Equal(t, int64(42), v)
}

func TestExtractInt64Param_MissingKey(t *testing.T) {
	t.Parallel()
	params := map[string]any{}
	_, err := extractInt64Param(params, "key")
	assert.ErrorIs(t, err, ErrParamKeyNotFound)
}

func TestExtractInt64Param_InvalidType(t *testing.T) {
	t.Parallel()
	params := map[string]any{"key": "not-a-number"}
	_, err := extractInt64Param(params, "key")
	assert.ErrorIs(t, err, ErrParamInvalidType)
}

// =============================================================================
// PostLedgerEntriesFromParams
// =============================================================================

func ledgerTestOrchestrator(t *testing.T, faClient *MockFinancialAccountingClient) *PaymentOrchestrator {
	t.Helper()
	repo := NewMockRepository()
	gwConfig := testGatewayAccountConfig()

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                    testLogger(),
		Repo:                      repo,
		FinancialAccountingClient: faClient,
		GatewayAccountConfig:      gwConfig,
		CurrentAccountClient:      &MockCurrentAccountClient{},
		PaymentGateway:            &MockPaymentGateway{},
	})
	require.NoError(t, err)
	return orchestrator
}

func TestPostLedgerEntriesFromParams_Success(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{}
	o := ledgerTestOrchestrator(t, fa)

	poID := uuid.New()
	params := map[string]any{
		"payment_order_id":     poID.String(),
		"debtor_account_id":    "acc-123",
		"gateway_reference_id": "GW-ref-456",
		"amount_cents":         int64(1000),
		"currency":             "GBP",
		"idempotency_key":      "idem-key-1",
	}

	bookingID, err := o.PostLedgerEntriesFromParams(context.Background(), params)
	require.NoError(t, err)
	assert.NotEmpty(t, bookingID)
	assert.True(t, fa.initiateCalled)
	assert.True(t, fa.captureCalled)
	assert.True(t, fa.updateCalled)
}

func TestPostLedgerEntriesFromParams_MissingPaymentOrderID(t *testing.T) {
	t.Parallel()
	fa := &MockFinancialAccountingClient{}
	o := ledgerTestOrchestrator(t, fa)

	params := map[string]any{
		"debtor_account_id":    "acc-123",
		"gateway_reference_id": "GW-ref",
		"amount_cents":         int64(1000),
		"currency":             "GBP",
		"idempotency_key":      "key",
	}

	_, err := o.PostLedgerEntriesFromParams(context.Background(), params)
	assert.ErrorIs(t, err, ErrMissingPaymentOrderID)
}

func TestPostLedgerEntriesFromParams_InvalidPaymentOrderID(t *testing.T) {
	t.Parallel()
	fa := &MockFinancialAccountingClient{}
	o := ledgerTestOrchestrator(t, fa)

	params := map[string]any{
		"payment_order_id":     "not-a-uuid",
		"debtor_account_id":    "acc-123",
		"gateway_reference_id": "GW-ref",
		"amount_cents":         int64(1000),
		"currency":             "GBP",
		"idempotency_key":      "key",
	}

	_, err := o.PostLedgerEntriesFromParams(context.Background(), params)
	assert.ErrorIs(t, err, ErrMissingPaymentOrderID)
}

func TestPostLedgerEntriesFromParams_MissingDebtorAccountID(t *testing.T) {
	t.Parallel()
	fa := &MockFinancialAccountingClient{}
	o := ledgerTestOrchestrator(t, fa)

	params := map[string]any{
		"payment_order_id":     uuid.New().String(),
		"gateway_reference_id": "GW-ref",
		"amount_cents":         int64(1000),
		"currency":             "GBP",
		"idempotency_key":      "key",
	}

	_, err := o.PostLedgerEntriesFromParams(context.Background(), params)
	assert.ErrorIs(t, err, ErrMissingDebtorAccountID)
}

func TestPostLedgerEntriesFromParams_MissingGatewayReferenceID(t *testing.T) {
	t.Parallel()
	fa := &MockFinancialAccountingClient{}
	o := ledgerTestOrchestrator(t, fa)

	params := map[string]any{
		"payment_order_id":  uuid.New().String(),
		"debtor_account_id": "acc-123",
		"amount_cents":      int64(1000),
		"currency":          "GBP",
		"idempotency_key":   "key",
	}

	_, err := o.PostLedgerEntriesFromParams(context.Background(), params)
	assert.ErrorIs(t, err, ErrMissingGatewayReferenceID)
}

func TestPostLedgerEntriesFromParams_MissingAmountCents(t *testing.T) {
	t.Parallel()
	fa := &MockFinancialAccountingClient{}
	o := ledgerTestOrchestrator(t, fa)

	params := map[string]any{
		"payment_order_id":     uuid.New().String(),
		"debtor_account_id":    "acc-123",
		"gateway_reference_id": "GW-ref",
		"currency":             "GBP",
		"idempotency_key":      "key",
	}

	_, err := o.PostLedgerEntriesFromParams(context.Background(), params)
	assert.ErrorIs(t, err, ErrMissingAmountCents)
}

func TestPostLedgerEntriesFromParams_MissingCurrency(t *testing.T) {
	t.Parallel()
	fa := &MockFinancialAccountingClient{}
	o := ledgerTestOrchestrator(t, fa)

	params := map[string]any{
		"payment_order_id":     uuid.New().String(),
		"debtor_account_id":    "acc-123",
		"gateway_reference_id": "GW-ref",
		"amount_cents":         int64(1000),
		"idempotency_key":      "key",
	}

	_, err := o.PostLedgerEntriesFromParams(context.Background(), params)
	assert.ErrorIs(t, err, ErrMissingCurrency)
}

func TestPostLedgerEntriesFromParams_MissingIdempotencyKey(t *testing.T) {
	t.Parallel()
	fa := &MockFinancialAccountingClient{}
	o := ledgerTestOrchestrator(t, fa)

	params := map[string]any{
		"payment_order_id":     uuid.New().String(),
		"debtor_account_id":    "acc-123",
		"gateway_reference_id": "GW-ref",
		"amount_cents":         int64(1000),
		"currency":             "GBP",
	}

	_, err := o.PostLedgerEntriesFromParams(context.Background(), params)
	assert.ErrorIs(t, err, ErrMissingIdempotencyKey)
}

// =============================================================================
// PostLedgerEntries — error paths
// =============================================================================

func TestPostLedgerEntries_NilGatewayAccountConfig(t *testing.T) {
	t.Parallel()
	fa := &MockFinancialAccountingClient{}

	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                    testLogger(),
		Repo:                      NewMockRepository(),
		FinancialAccountingClient: fa,
		CurrentAccountClient:      &MockCurrentAccountClient{},
		PaymentGateway:            &MockPaymentGateway{},
	})
	require.NoError(t, err)

	po := testfixtures.NewPaymentOrder(t)
	_, postErr := o.PostLedgerEntries(context.Background(), po)
	assert.ErrorIs(t, postErr, ErrGatewayAccountConfigNotSet)
}

func TestPostLedgerEntries_NilFAClient(t *testing.T) {
	t.Parallel()

	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:               testLogger(),
		Repo:                 NewMockRepository(),
		GatewayAccountConfig: testGatewayAccountConfig(),
		CurrentAccountClient: &MockCurrentAccountClient{},
		PaymentGateway:       &MockPaymentGateway{},
	})
	require.NoError(t, err)

	po := testfixtures.NewPaymentOrder(t)
	_, postErr := o.PostLedgerEntries(context.Background(), po)
	assert.ErrorIs(t, postErr, ErrFinancialAccountingClientNotSet)
}

func TestPostLedgerEntries_InitiateBookingLogFails(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{
		initiateErr: errors.New("fa service down"),
	}
	o := ledgerTestOrchestrator(t, fa)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	_, err := o.PostLedgerEntries(context.Background(), po)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create booking log")
}

func TestPostLedgerEntries_NilBookingLogResponse(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{
		initiateResp: &financialaccountingv1.InitiateFinancialBookingLogResponse{
			FinancialBookingLog: nil,
		},
	}
	o := ledgerTestOrchestrator(t, fa)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	_, err := o.PostLedgerEntries(context.Background(), po)
	assert.ErrorIs(t, err, ErrNilBookingLogResponse)
}

func TestPostLedgerEntries_DebitPostingFails(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{
		captureErr:       errors.New("debit posting failed"),
		captureErrOnCall: 1, // fail on first capture (debit)
	}
	o := ledgerTestOrchestrator(t, fa)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	_, err := o.PostLedgerEntries(context.Background(), po)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create debit posting")
}

func TestPostLedgerEntries_CreditPostingFails(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{
		captureErr:       errors.New("credit posting failed"),
		captureErrOnCall: 2, // fail on second capture (credit)
	}
	o := ledgerTestOrchestrator(t, fa)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	_, err := o.PostLedgerEntries(context.Background(), po)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create credit posting")
}

func TestPostLedgerEntries_UpdateBookingLogFails(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{
		updateErr: errors.New("status update failed"),
	}
	o := ledgerTestOrchestrator(t, fa)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	_, err := o.PostLedgerEntries(context.Background(), po)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update booking log to POSTED")
}

func TestPostLedgerEntries_StandardFlowSuccess(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{}
	o := ledgerTestOrchestrator(t, fa)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	bookingID, err := o.PostLedgerEntries(context.Background(), po)
	require.NoError(t, err)
	assert.NotEmpty(t, bookingID)
	assert.Equal(t, 2, fa.captureCallCount) // debit + credit
	assert.True(t, fa.updateCalled)
}

// =============================================================================
// PostLedgerEntries — clearing flow
// =============================================================================

func TestPostLedgerEntries_ClearingFlowSuccess(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{}
	repo := NewMockRepository()
	gwConfig := testGatewayAccountConfig()

	// Create orchestrator with internal clearing enabled and a mock account resolver
	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                    testLogger(),
		Repo:                      repo,
		FinancialAccountingClient: fa,
		GatewayAccountConfig:      gwConfig,
		CurrentAccountClient:      &MockCurrentAccountClient{},
		PaymentGateway:            &MockPaymentGateway{},
		InternalClearingEnabled:   true,
		InternalAccountClient:     clearingMockInternalAccountClient(),
	})
	require.NoError(t, err)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	bookingID, postErr := o.PostLedgerEntries(context.Background(), po)
	require.NoError(t, postErr)
	assert.NotEmpty(t, bookingID)
	assert.Equal(t, 4, fa.captureCallCount) // debit customer + credit clearing + debit clearing + credit gateway
	assert.True(t, fa.updateCalled)
}

func TestPostLedgerEntries_ClearingFlowCreditClearingFails(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{
		captureErr:       errors.New("clearing credit failed"),
		captureErrOnCall: 2, // second capture is the clearing credit
	}
	repo := NewMockRepository()
	gwConfig := testGatewayAccountConfig()

	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                    testLogger(),
		Repo:                      repo,
		FinancialAccountingClient: fa,
		GatewayAccountConfig:      gwConfig,
		CurrentAccountClient:      &MockCurrentAccountClient{},
		PaymentGateway:            &MockPaymentGateway{},
		InternalClearingEnabled:   true,
		InternalAccountClient:     clearingMockInternalAccountClient(),
	})
	require.NoError(t, err)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	_, postErr := o.PostLedgerEntries(context.Background(), po)
	assert.Error(t, postErr)
	assert.Contains(t, postErr.Error(), "failed to create credit posting for clearing account")
}

func TestPostLedgerEntries_ClearingFlowDebitClearingFails(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{
		captureErr:       errors.New("clearing debit failed"),
		captureErrOnCall: 3, // third capture is the clearing debit
	}
	repo := NewMockRepository()
	gwConfig := testGatewayAccountConfig()

	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                    testLogger(),
		Repo:                      repo,
		FinancialAccountingClient: fa,
		GatewayAccountConfig:      gwConfig,
		CurrentAccountClient:      &MockCurrentAccountClient{},
		PaymentGateway:            &MockPaymentGateway{},
		InternalClearingEnabled:   true,
		InternalAccountClient:     clearingMockInternalAccountClient(),
	})
	require.NoError(t, err)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	_, postErr := o.PostLedgerEntries(context.Background(), po)
	assert.Error(t, postErr)
	assert.Contains(t, postErr.Error(), "failed to create debit posting for clearing account")
}

func TestPostLedgerEntries_ClearingFlowGatewayCreditFails(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{
		captureErr:       errors.New("gateway credit failed"),
		captureErrOnCall: 4, // fourth capture is the gateway credit
	}
	repo := NewMockRepository()
	gwConfig := testGatewayAccountConfig()

	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                    testLogger(),
		Repo:                      repo,
		FinancialAccountingClient: fa,
		GatewayAccountConfig:      gwConfig,
		CurrentAccountClient:      &MockCurrentAccountClient{},
		PaymentGateway:            &MockPaymentGateway{},
		InternalClearingEnabled:   true,
		InternalAccountClient:     clearingMockInternalAccountClient(),
	})
	require.NoError(t, err)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	_, postErr := o.PostLedgerEntries(context.Background(), po)
	assert.Error(t, postErr)
	assert.Contains(t, postErr.Error(), "failed to create credit posting for gateway account")
}

func TestPostLedgerEntries_ClearingFlowUpdateFails(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{
		updateErr: errors.New("update failed"),
	}
	repo := NewMockRepository()
	gwConfig := testGatewayAccountConfig()

	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                    testLogger(),
		Repo:                      repo,
		FinancialAccountingClient: fa,
		GatewayAccountConfig:      gwConfig,
		CurrentAccountClient:      &MockCurrentAccountClient{},
		PaymentGateway:            &MockPaymentGateway{},
		InternalClearingEnabled:   true,
		InternalAccountClient:     clearingMockInternalAccountClient(),
	})
	require.NoError(t, err)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	_, postErr := o.PostLedgerEntries(context.Background(), po)
	assert.Error(t, postErr)
	assert.Contains(t, postErr.Error(), "failed to update booking log to POSTED")
}

func TestPostLedgerEntries_ClearingEnabledButNoResolver(t *testing.T) {
	t.Parallel()

	fa := &MockFinancialAccountingClient{}

	// Create with clearing enabled but no InternalAccountClient → no resolver
	o, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                    testLogger(),
		Repo:                      NewMockRepository(),
		FinancialAccountingClient: fa,
		GatewayAccountConfig:      testGatewayAccountConfig(),
		CurrentAccountClient:      &MockCurrentAccountClient{},
		PaymentGateway:            &MockPaymentGateway{},
		InternalClearingEnabled:   true,
		// No InternalAccountClient → accountResolver stays nil
	})
	require.NoError(t, err)

	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-ref-123"),
	)

	// Should fall back to standard 2-posting flow
	bookingID, postErr := o.PostLedgerEntries(context.Background(), po)
	require.NoError(t, postErr)
	assert.NotEmpty(t, bookingID)
	assert.Equal(t, 2, fa.captureCallCount) // standard flow
}
