package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/services/payment-order/domain/testfixtures"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// mockKafkaPublisher implements KafkaPublisher for testing.
type mockKafkaPublisher struct {
	lastTopic string
	lastKey   string
	err       error
}

func (m *mockKafkaPublisher) Publish(_ context.Context, topic string, key string, _ proto.Message) error {
	m.lastTopic = topic
	m.lastKey = key
	return m.err
}

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
// publishEvent — nil publisher, success, and error paths
// =============================================================================

func TestPublishEvent_NilPublisher(t *testing.T) {
	t.Parallel()

	s := newTestServiceWithMocks(t)
	s.kafkaPublisher = nil

	// Should not panic
	s.publishEvent(context.Background(), "topic", "key", nil)
}

func TestPublishEvent_Success(t *testing.T) {
	t.Parallel()

	s := newTestServiceWithMocks(t)
	mock := &mockKafkaPublisher{}
	s.kafkaPublisher = mock

	s.publishEvent(context.Background(), "test-topic", "test-key", nil)

	assert.Equal(t, "test-topic", mock.lastTopic)
	assert.Equal(t, "test-key", mock.lastKey)
}

func TestPublishEvent_Error(t *testing.T) {
	t.Parallel()

	s := newTestServiceWithMocks(t)
	mock := &mockKafkaPublisher{err: errors.New("kafka unavailable")}
	s.kafkaPublisher = mock

	// Should not panic, error is logged
	s.publishEvent(context.Background(), "test-topic", "test-key", nil)

	assert.Equal(t, "test-topic", mock.lastTopic)
}

// =============================================================================
// resultToString — additional type coverage
// =============================================================================

func TestResultToString_Uint(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	// CEL uint literal
	result, err := evaluator.Evaluate(context.Background(), `42u`, BucketEvalContext{
		InstrumentCode: "USD",
	})
	require.NoError(t, err)
	assert.Equal(t, "42", result)
}

func TestResultToString_Double(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	// CEL double literal
	result, err := evaluator.Evaluate(context.Background(), `3.14`, BucketEvalContext{
		InstrumentCode: "USD",
	})
	require.NoError(t, err)
	assert.Equal(t, "3.14", result)
}

func TestResultToString_List(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	_, err = evaluator.Evaluate(context.Background(), `["a", "b"]`, BucketEvalContext{
		InstrumentCode: "USD",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CEL result conversion failed")
}

func TestResultToString_Map(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	_, err = evaluator.Evaluate(context.Background(), `{"key": "value"}`, BucketEvalContext{
		InstrumentCode: "USD",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CEL result conversion failed")
}

// =============================================================================
// PaymentOrchestrator.publishEvent — success and error
// =============================================================================

func TestOrchestratorPublishEvent_NilPublisher(t *testing.T) {
	t.Parallel()

	o := testOrchestrator(NewMockRepository(), &MockCurrentAccountClient{}, nil, nil)
	o.kafkaPublisher = nil

	// Should not panic
	o.publishEvent(context.Background(), "topic", "key", nil)
}

func TestOrchestratorPublishEvent_Success(t *testing.T) {
	t.Parallel()

	o := testOrchestrator(NewMockRepository(), &MockCurrentAccountClient{}, nil, nil)
	mock := &mockKafkaPublisher{}
	o.kafkaPublisher = mock

	o.publishEvent(context.Background(), "orch-topic", "orch-key", nil)

	assert.Equal(t, "orch-topic", mock.lastTopic)
	assert.Equal(t, "orch-key", mock.lastKey)
}

func TestOrchestratorPublishEvent_Error(t *testing.T) {
	t.Parallel()

	o := testOrchestrator(NewMockRepository(), &MockCurrentAccountClient{}, nil, nil)
	mock := &mockKafkaPublisher{err: errors.New("broker down")}
	o.kafkaPublisher = mock

	// Should not panic, error is logged
	o.publishEvent(context.Background(), "orch-topic", "orch-key", nil)

	assert.Equal(t, "orch-topic", mock.lastTopic)
}

// =============================================================================
// storeIdempotencyFailure — error path
// =============================================================================

func TestStoreIdempotencyFailure_StoreError(t *testing.T) {
	t.Parallel()

	idempSvc := NewMockIdempotencyService()
	idempSvc.storeErr = errors.New("redis connection lost")

	s, err := NewService(NewMockRepository(), idempSvc)
	require.NoError(t, err)
	s.logger = testLogger()

	key := idempotency.Key{
		TenantID:  "tenant-1",
		Namespace: "payment-order",
		Operation: "initiate",
		EntityID:  "acc-123",
		RequestID: "req-abc",
	}

	// Should not panic — error is only logged
	s.storeIdempotencyFailure(context.Background(), key, "validation failed")
}

func TestStoreIdempotencyFailure_Success(t *testing.T) {
	t.Parallel()

	idempSvc := NewMockIdempotencyService()

	s, err := NewService(NewMockRepository(), idempSvc)
	require.NoError(t, err)
	s.logger = testLogger()

	key := idempotency.Key{
		TenantID:  "tenant-1",
		Namespace: "payment-order",
		Operation: "initiate",
		EntityID:  "acc-123",
		RequestID: "req-def",
	}

	s.storeIdempotencyFailure(context.Background(), key, "some error")

	// Verify result was stored
	result, checkErr := idempSvc.Check(context.Background(), key)
	require.NoError(t, checkErr)
	assert.Equal(t, idempotency.StatusFailed, result.Status)
}

// =============================================================================
// handleRejectedStatus — idempotent path (already failed)
// =============================================================================

func TestHandleRejectedStatus_AlreadyFailed(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusFailed,
		testfixtures.WithFailureReason("previous failure"),
		testfixtures.WithErrorCode("PREV_ERROR"),
	)
	require.NoError(t, repo.Create(context.Background(), po))

	s := newTestServiceWithMocksAndRepo(t, repo, &testMockClients{})

	result, err := s.handleRejectedStatus(context.Background(), po, "gateway rejected again")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.isIdempotent)
	assert.Equal(t, domain.PaymentOrderStatusFailed, result.po.Status)
}

func TestHandleRejectedStatus_ExecutingState(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting,
		testfixtures.WithGatewayReferenceID("GW-rej-123"),
	)
	require.NoError(t, repo.Create(context.Background(), po))

	s := newTestServiceWithMocksAndRepo(t, repo, &testMockClients{})

	result, err := s.handleRejectedStatus(context.Background(), po, "insufficient funds")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.isIdempotent)

	// Verify PO was marked as failed
	updated, findErr := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
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
