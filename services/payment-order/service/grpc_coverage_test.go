package service

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/services/payment-order/domain/testfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// LockClient
// =============================================================================

func TestLockNotObtainedError(t *testing.T) {
	t.Parallel()

	err := LockNotObtainedError{}
	assert.Equal(t, "lock not obtained: already held by another process", err.Error())

	assert.True(t, IsLockNotObtained(LockNotObtainedError{}))
	assert.False(t, IsLockNotObtained(errors.New("other error")))
	assert.False(t, IsLockNotObtained(nil))
}

// =============================================================================
// grpc_mappers — mapLienExecutionStatusToProto, safeIntToInt32, extractGatewayIDFromRef
// =============================================================================

func TestMapLienExecutionStatusToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    domain.LienExecutionStatus
		expected pb.LienExecutionStatus
	}{
		{domain.LienExecutionStatusUnspecified, pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_UNSPECIFIED},
		{domain.LienExecutionStatusPending, pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_PENDING},
		{domain.LienExecutionStatusSucceeded, pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_SUCCEEDED},
		{domain.LienExecutionStatusFailed, pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_FAILED},
		{domain.LienExecutionStatus("INVALID"), pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_UNSPECIFIED},
	}

	for _, tc := range tests {
		t.Run(string(tc.input), func(t *testing.T) {
			assert.Equal(t, tc.expected, mapLienExecutionStatusToProto(tc.input))
		})
	}
}

func TestSafeIntToInt32(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int32(0), safeIntToInt32(0))
	assert.Equal(t, int32(42), safeIntToInt32(42))
	assert.Equal(t, int32(-1), safeIntToInt32(-1))
	assert.Equal(t, int32(math.MaxInt32), safeIntToInt32(math.MaxInt32))
	assert.Equal(t, int32(math.MaxInt32), safeIntToInt32(math.MaxInt32+1)) // overflow
	assert.Equal(t, int32(math.MinInt32), safeIntToInt32(math.MinInt32))
	assert.Equal(t, int32(math.MinInt32), safeIntToInt32(math.MinInt32-1)) // underflow
}

func TestExtractGatewayIDFromRef_AllPatterns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"", "unknown"},
		{"GW-abc123", "mock"},
		{"gateway-ref-456", "mock"},
		{"stripe-pm_1234", "stripe"},
		{"adyen-PSP-REF", "adyen"},
		{"STRIPE-pi_abc", "stripe"},
		{"nodash", "nodash"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, extractGatewayIDFromRef(tc.input))
		})
	}
}

// =============================================================================
// CancelPaymentOrder — additional edge cases
// =============================================================================

func TestCancelPaymentOrder_InvalidID(t *testing.T) {
	t.Parallel()

	s := newTestServiceWithMocks(t)

	_, err := s.CancelPaymentOrder(context.Background(), &pb.CancelPaymentOrderRequest{
		PaymentOrderId:     "not-a-uuid",
		CancellationReason: "test",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid payment order ID")
}

func TestCancelPaymentOrder_NotFound(t *testing.T) {
	t.Parallel()

	s := newTestServiceWithMocks(t)

	_, err := s.CancelPaymentOrder(context.Background(), &pb.CancelPaymentOrderRequest{
		PaymentOrderId:     uuid.New().String(),
		CancellationReason: "test",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "payment order not found")
}

func TestCancelPaymentOrder_LienTerminationFailure_ContinuesSuccessfully(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusReserved,
		testfixtures.WithLienID("lien-fail"),
	)
	require.NoError(t, repo.Create(context.Background(), po))

	s := newTestServiceWithMocksAndRepo(t, repo, &testMockClients{
		terminateLienErr: errors.New("lien service down"),
	})

	resp, err := s.CancelPaymentOrder(context.Background(), &pb.CancelPaymentOrderRequest{
		PaymentOrderId:     po.ID.String(),
		CancellationReason: "user requested",
		CancelledBy:        "user@example.com",
	})

	// Cancellation succeeds even if lien termination fails
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_CANCELLED, resp.PaymentOrder.Status)
}

// =============================================================================
// Service failPaymentOrder — with ledger reversal
// =============================================================================

func TestServiceFailPaymentOrder_WithLedgerBookingID(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithLienID("lien-exec"),
		testfixtures.WithLedgerBookingID("booking-123"),
		testfixtures.WithGatewayReferenceID("GW-ref"),
	)
	require.NoError(t, repo.Create(context.Background(), po))

	s := newTestServiceWithMocksAndRepo(t, repo, &testMockClients{
		hasFA: true,
	})

	err := s.failPaymentOrder(context.Background(), po, "gateway timeout", "GATEWAY_TIMEOUT")
	require.NoError(t, err)

	updated, findErr := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
}

func TestServiceFailPaymentOrder_NoLedgerBooking(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	s := newTestServiceWithMocksAndRepo(t, repo, &testMockClients{})

	err := s.failPaymentOrder(context.Background(), po, "test failure", "TEST")
	require.NoError(t, err)

	updated, findErr := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
}

// =============================================================================
// handlePendingStatus — edge cases
// =============================================================================

func TestHandlePendingStatus_NonExecutingState(t *testing.T) {
	t.Parallel()

	s := newTestServiceWithMocks(t)

	statuses := []domain.PaymentOrderStatus{
		domain.PaymentOrderStatusInitiated,
		domain.PaymentOrderStatusReserved,
		domain.PaymentOrderStatusCompleted,
		domain.PaymentOrderStatusFailed,
	}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			po := &domain.PaymentOrder{Status: status}
			err := s.handlePendingStatus(po, "gw-ref")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "cannot process PENDING callback")
		})
	}
}

func TestHandlePendingStatus_ExecutingState_Success(t *testing.T) {
	t.Parallel()

	s := newTestServiceWithMocks(t)

	po := &domain.PaymentOrder{
		Status: domain.PaymentOrderStatusExecuting,
	}
	err := s.handlePendingStatus(po, "gw-ref")
	assert.NoError(t, err)
}

// =============================================================================
// publishEvent — nil publisher
// =============================================================================

func TestPublishEvent_NilPublisher(t *testing.T) {
	t.Parallel()

	s := newTestServiceWithMocks(t)
	s.kafkaPublisher = nil

	// Should not panic
	s.publishEvent(context.Background(), "topic", "key", nil)
}

// =============================================================================
// Helpers
// =============================================================================

type testMockClients struct {
	terminateLienErr error
	hasFA            bool
}

func newTestServiceWithMocks(t *testing.T) *Service {
	t.Helper()
	return newTestServiceWithMocksAndRepo(t, NewMockRepository(), &testMockClients{})
}

func newTestServiceWithMocksAndRepo(t *testing.T, repo *MockRepository, clients *testMockClients) *Service {
	t.Helper()

	mockCA := &MockCurrentAccountClient{
		terminateLienErr: clients.terminateLienErr,
	}

	var fa FinancialAccountingClient
	if clients.hasFA {
		fa = &MockFinancialAccountingClient{}
	}

	var gwConfig *config.GatewayAccountConfig
	if clients.hasFA {
		gwConfig = testGatewayAccountConfig()
	}

	orchestrator := testOrchestrator(repo, mockCA, fa, gwConfig)

	s, err := NewService(repo, NewMockIdempotencyService())
	require.NoError(t, err)
	s.orchestrator = orchestrator
	s.currentAccountClient = mockCA
	s.logger = testLogger()
	return s
}
