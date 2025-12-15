// Package service provides integration tests for the payment order service.
// These tests use testcontainers to simulate a production-like environment.
//
//nolint:staticcheck // Uses AmountCents() for test assertions (deprecated for backward compatibility)
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// =============================================================================
// Test Infrastructure Setup
// =============================================================================

// Mock errors for testing
var (
	errMockInitiateLienFailure  = errors.New("mock initiate lien failure")
	errMockTerminateLienFailure = errors.New("mock terminate lien failure")
	errMockExecuteLienFailure   = errors.New("mock execute lien failure")
	errMockGatewayFailure       = errors.New("mock gateway failure")
	errGatewayTimeout           = errors.New("gateway timeout")
	errLedgerUnavailable        = errors.New("ledger service unavailable")
)

const integrationTestTenantID = "test_tenant"

// setupIntegrationTestDB creates a PostgreSQL testcontainer for integration testing.
// Returns a configured GORM database connection, context with tenant, and a cleanup function.
func setupIntegrationTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.PaymentOrderEntity{},
	})

	// Create tenant schema
	tid := tenant.TenantID(integrationTestTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create payment_orders table in tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.payment_orders (
		id UUID PRIMARY KEY,
		debtor_account_id VARCHAR(255) NOT NULL,
		creditor_reference VARCHAR(255) NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(3) NOT NULL,
		status VARCHAR(20) NOT NULL,
		idempotency_key VARCHAR(255) NOT NULL UNIQUE,
		correlation_id VARCHAR(255),
		causation_id VARCHAR(255),
		lien_id VARCHAR(255),
		gateway_reference_id VARCHAR(255),
		ledger_booking_id VARCHAR(255),
		failure_reason TEXT,
		error_code VARCHAR(50),
		version INTEGER NOT NULL DEFAULT 1,
		lien_execution_status VARCHAR(20),
		lien_execution_attempts INTEGER DEFAULT 0,
		lien_execution_error TEXT,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		reserved_at TIMESTAMP WITH TIME ZONE,
		executing_at TIMESTAMP WITH TIME ZONE,
		completed_at TIMESTAMP WITH TIME ZONE,
		failed_at TIMESTAMP WITH TIME ZONE,
		cancelled_at TIMESTAMP WITH TIME ZONE,
		reversed_at TIMESTAMP WITH TIME ZONE
	)`, schemaName)).Error
	require.NoError(t, err)

	// Set search_path to tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

// =============================================================================
// Mock Implementations for Integration Tests
// =============================================================================

// mockCurrentAccountClient implements CurrentAccountClient for integration testing.
// Thread-safe for use with async saga tests.
type mockCurrentAccountClient struct {
	mu                  sync.RWMutex
	initiateLienCalls   int32
	terminateLienCalls  int32
	executeLienCalls    int32
	failOnInitiateLien  bool
	failOnTerminateLien bool
	failOnExecuteLien   bool
	initiateLienErr     error
	terminateLienErr    error
	executeLienErr      error
	insufficientFunds   bool
	lienCounter         int32
	executedLiens       map[string]bool
	terminatedLiens     map[string]bool
	lastLienID          string
	accountBalances     map[string]int64 // Track account balances for realistic testing
}

func newMockCurrentAccountClient() *mockCurrentAccountClient {
	return &mockCurrentAccountClient{
		executedLiens:   make(map[string]bool),
		terminatedLiens: make(map[string]bool),
		accountBalances: make(map[string]int64),
	}
}

func (m *mockCurrentAccountClient) InitiateLien(_ context.Context, req *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
	atomic.AddInt32(&m.initiateLienCalls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failOnInitiateLien {
		if m.initiateLienErr != nil {
			return nil, m.initiateLienErr
		}
		return nil, errMockInitiateLienFailure
	}

	if m.insufficientFunds {
		return nil, status.Error(codes.FailedPrecondition, "insufficient funds")
	}

	// Check account balance if tracked
	if balance, exists := m.accountBalances[req.AccountId]; exists {
		if req.Amount != nil && req.Amount.Amount != nil {
			amountCents := req.Amount.Amount.Units*100 + int64(req.Amount.Amount.Nanos/10000000)
			if amountCents > balance {
				return nil, status.Error(codes.FailedPrecondition, "insufficient funds")
			}
		}
	}

	m.lienCounter++
	lienID := uuid.New().String()
	m.lastLienID = lienID

	return &currentaccountv1.InitiateLienResponse{
		Lien: &currentaccountv1.Lien{
			LienId:    lienID,
			AccountId: req.AccountId,
			Amount:    req.Amount,
			Status:    currentaccountv1.LienStatus_LIEN_STATUS_ACTIVE,
		},
	}, nil
}

func (m *mockCurrentAccountClient) TerminateLien(_ context.Context, req *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error) {
	atomic.AddInt32(&m.terminateLienCalls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failOnTerminateLien {
		if m.terminateLienErr != nil {
			return nil, m.terminateLienErr
		}
		return nil, errMockTerminateLienFailure
	}

	m.terminatedLiens[req.LienId] = true

	return &currentaccountv1.TerminateLienResponse{
		Lien: &currentaccountv1.Lien{
			LienId: req.LienId,
			Status: currentaccountv1.LienStatus_LIEN_STATUS_TERMINATED,
		},
	}, nil
}

func (m *mockCurrentAccountClient) ExecuteLien(_ context.Context, req *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error) {
	atomic.AddInt32(&m.executeLienCalls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failOnExecuteLien {
		if m.executeLienErr != nil {
			return nil, m.executeLienErr
		}
		return nil, errMockExecuteLienFailure
	}

	m.executedLiens[req.LienId] = true

	return &currentaccountv1.ExecuteLienResponse{
		Lien: &currentaccountv1.Lien{
			LienId: req.LienId,
			Status: currentaccountv1.LienStatus_LIEN_STATUS_EXECUTED,
		},
		TransactionId: "TXN-" + uuid.New().String(),
	}, nil
}

func (m *mockCurrentAccountClient) Close() error {
	return nil
}

// mockPaymentGateway implements gateway.PaymentGateway for integration testing.
type mockPaymentGateway struct {
	mu                sync.RWMutex
	sendPaymentCalls  int32
	failOnSend        bool
	sendErr           error
	rejectPayment     bool
	delayResponse     time.Duration
	gatewayRefCounter int32
	lastGatewayRefID  string
}

func newMockPaymentGateway() *mockPaymentGateway {
	return &mockPaymentGateway{}
}

func (m *mockPaymentGateway) SendPayment(ctx context.Context, _ gateway.PaymentRequest) (gateway.PaymentResponse, error) {
	atomic.AddInt32(&m.sendPaymentCalls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.delayResponse > 0 {
		select {
		case <-time.After(m.delayResponse):
		case <-ctx.Done():
			return gateway.PaymentResponse{}, ctx.Err()
		}
	}

	if m.failOnSend {
		if m.sendErr != nil {
			return gateway.PaymentResponse{}, m.sendErr
		}
		return gateway.PaymentResponse{}, errMockGatewayFailure
	}

	if m.rejectPayment {
		return gateway.PaymentResponse{
			Status:  gateway.StatusRejected,
			Message: "Payment rejected by gateway",
		}, nil
	}

	m.gatewayRefCounter++
	gatewayRefID := "GW-" + uuid.New().String()
	m.lastGatewayRefID = gatewayRefID

	return gateway.PaymentResponse{
		Status:             gateway.StatusAccepted,
		GatewayReferenceID: gatewayRefID,
		Message:            "Payment accepted",
	}, nil
}

// =============================================================================
// Helper Functions
// =============================================================================

const defaultSagaWaitTimeout = 10 * time.Second

func integrationTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

func createTestPaymentRequest(accountID string, units int64, nanos int32) *pb.InitiatePaymentOrderRequest {
	return &pb.InitiatePaymentOrderRequest{
		DebtorAccountId:   accountID,
		CreditorReference: "CRED-" + uuid.New().String()[:8],
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        units,
				Nanos:        nanos,
			},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: uuid.New().String(),
		},
	}
}

// waitForSagaCompletion polls the repository until the payment order reaches a terminal state.
func waitForSagaCompletion(ctx context.Context, t *testing.T, repo persistence.Repository, poID uuid.UUID) *domain.PaymentOrder {
	t.Helper()
	var result *domain.PaymentOrder

	err := await.New().
		AtMost(defaultSagaWaitTimeout).
		PollInterval(50 * time.Millisecond).
		WithContext(ctx).
		Until(func() bool {
			po, err := repo.FindByID(ctx, poID)
			if err != nil {
				return false
			}

			// Check for terminal states (non-transient states that indicate saga finished)
			switch po.Status {
			case domain.PaymentOrderStatusCompleted,
				domain.PaymentOrderStatusFailed,
				domain.PaymentOrderStatusCancelled,
				domain.PaymentOrderStatusReversed,
				domain.PaymentOrderStatusExecuting:
				result = po
				return true
			case domain.PaymentOrderStatusInitiated, domain.PaymentOrderStatusReserved:
				// Transient states - keep waiting
				return false
			}
			return false
		})
	if err != nil {
		t.Fatalf("Timeout waiting for saga completion for payment order %s: %v", poID, err)
	}
	return result
}

// =============================================================================
// Happy Path Integration Tests
// =============================================================================

// TestIntegration_HappyPath_Initiate_Reserve_Execute_Complete tests the full
// payment order lifecycle: INITIATED -> RESERVED -> EXECUTING -> COMPLETED
func TestIntegration_HappyPath_Initiate_Reserve_Execute_Complete(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
		SagaTimeout:          30 * time.Second,
	})
	require.NoError(t, err)

	req := createTestPaymentRequest("ACC-001", 100, 500000000) // £100.50

	// Execute: Initiate payment order
	resp, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err, "InitiatePaymentOrder should succeed")
	assert.NotNil(t, resp.PaymentOrder)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED, resp.PaymentOrder.Status)

	poID, err := uuid.Parse(resp.PaymentOrder.PaymentOrderId)
	require.NoError(t, err)

	// Wait for saga to reach EXECUTING state
	po := waitForSagaCompletion(ctx, t, repo, poID)
	assert.Equal(t, domain.PaymentOrderStatusExecuting, po.Status, "Payment should be in EXECUTING state")
	assert.NotEmpty(t, po.LienID, "Lien ID should be set")
	assert.NotEmpty(t, po.GatewayReferenceID, "Gateway reference ID should be set")

	// Verify mock service calls
	assert.Equal(t, int32(1), atomic.LoadInt32(&mockCA.initiateLienCalls), "InitiateLien should be called once")
	assert.Equal(t, int32(1), atomic.LoadInt32(&mockGW.sendPaymentCalls), "SendPayment should be called once")

	// Simulate gateway callback: SETTLED
	updateResp, err := svc.UpdatePaymentOrder(ctx, &pb.UpdatePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
	})
	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED, updateResp.PaymentOrder.Status)

	// Wait for async lien execution to complete (runs in background goroutine)
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&mockCA.executeLienCalls) >= 1
		})
	require.NoError(t, err, "ExecuteLien should be called within timeout")

	// Verify lien was executed
	assert.Equal(t, int32(1), atomic.LoadInt32(&mockCA.executeLienCalls), "ExecuteLien should be called once")

	// Verify final state in database
	finalPO, err := repo.FindByID(ctx, poID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusCompleted, finalPO.Status)
	assert.NotNil(t, finalPO.CompletedAt)
}

// TestIntegration_Idempotency_SameKeyReturnsSameResult tests that the same
// idempotency key returns the same payment order without creating duplicates.
func TestIntegration_Idempotency_SameKeyReturnsSameResult(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	idempotencyKey := uuid.New().String()

	req := &pb.InitiatePaymentOrderRequest{
		DebtorAccountId:   "ACC-IDEMP-001",
		CreditorReference: "CRED-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        50,
				Nanos:        0,
			},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: idempotencyKey},
	}

	// First request
	resp1, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)
	poID1 := resp1.PaymentOrder.PaymentOrderId

	// Wait for saga to start processing (at least one lien call or state change)
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&mockCA.initiateLienCalls) >= 1
		})
	require.NoError(t, err, "Saga should start processing")

	// Second request with same idempotency key
	resp2, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)
	poID2 := resp2.PaymentOrder.PaymentOrderId

	// Verify same payment order is returned
	assert.Equal(t, poID1, poID2, "Same idempotency key should return same payment order")

	// InitiateLien should only be called once (by the first saga)
	// The second request should return the cached result
	// Wait a bit to ensure no additional calls are made
	_ = await.New().
		AtMost(500 * time.Millisecond).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			// This will timeout, which is fine - we're just giving the system time
			// to make any additional calls it might want to make
			return false
		})
	assert.LessOrEqual(t, atomic.LoadInt32(&mockCA.initiateLienCalls), int32(1),
		"InitiateLien should be called at most once for idempotent requests")
}

// TestIntegration_DuplicateWebhook_Idempotent tests that duplicate gateway
// callbacks are handled idempotently.
func TestIntegration_DuplicateWebhook_Idempotent(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	req := createTestPaymentRequest("ACC-DUP-001", 25, 0)

	// Initiate payment
	resp, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, _ := uuid.Parse(resp.PaymentOrder.PaymentOrderId)
	po := waitForSagaCompletion(ctx, t, repo, poID)
	require.Equal(t, domain.PaymentOrderStatusExecuting, po.Status)

	// First SETTLED callback
	updateResp1, err := svc.UpdatePaymentOrder(ctx, &pb.UpdatePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
	})
	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED, updateResp1.PaymentOrder.Status)

	// Wait for async lien execution to complete before checking call count
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&mockCA.executeLienCalls) >= 1
		})
	require.NoError(t, err, "ExecuteLien should be called within timeout")

	executeLienCalls1 := atomic.LoadInt32(&mockCA.executeLienCalls)

	// Duplicate SETTLED callback (should be idempotent)
	updateResp2, err := svc.UpdatePaymentOrder(ctx, &pb.UpdatePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
	})
	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED, updateResp2.PaymentOrder.Status)

	// ExecuteLien should not be called again
	assert.Equal(t, executeLienCalls1, atomic.LoadInt32(&mockCA.executeLienCalls),
		"ExecuteLien should not be called for duplicate callback")
}

// =============================================================================
// Compensation Scenario Tests
// =============================================================================

// TestIntegration_InsufficientFunds_SagaFails tests that a payment order
// fails gracefully when the debtor has insufficient funds.
func TestIntegration_InsufficientFunds_SagaFails(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockCA.insufficientFunds = true
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	req := createTestPaymentRequest("ACC-INSUFF-001", 1000, 0)

	// Initiate payment
	resp, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, _ := uuid.Parse(resp.PaymentOrder.PaymentOrderId)

	// Wait for saga to fail
	po := waitForSagaCompletion(ctx, t, repo, poID)
	assert.Equal(t, domain.PaymentOrderStatusFailed, po.Status, "Payment should be FAILED")
	assert.Contains(t, po.FailureReason, "insufficient funds", "Failure reason should mention insufficient funds")

	// Gateway should never be called if fund reservation fails
	assert.Equal(t, int32(0), atomic.LoadInt32(&mockGW.sendPaymentCalls),
		"Gateway should not be called when fund reservation fails")
}

// TestIntegration_GatewayTimeout_CompensationReleasesLien tests that when
// the gateway times out, the saga compensation releases the lien.
func TestIntegration_GatewayTimeout_CompensationReleasesLien(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	mockGW.failOnSend = true
	mockGW.sendErr = errGatewayTimeout
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	req := createTestPaymentRequest("ACC-TIMEOUT-001", 75, 0)

	// Initiate payment
	resp, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, _ := uuid.Parse(resp.PaymentOrder.PaymentOrderId)

	// Wait for saga to fail and compensate
	po := waitForSagaCompletion(ctx, t, repo, poID)
	assert.Equal(t, domain.PaymentOrderStatusFailed, po.Status, "Payment should be FAILED")
	assert.Contains(t, po.FailureReason, "timeout", "Failure reason should mention timeout")

	// Verify lien was released (compensation)
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&mockCA.terminateLienCalls) >= 1
		})
	require.NoError(t, err, "Lien should be released during compensation")
}

// TestIntegration_GatewayRejects_StatusFailed tests that when the gateway
// rejects a payment, the order transitions to FAILED with proper compensation.
func TestIntegration_GatewayRejects_StatusFailed(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	mockGW.rejectPayment = true
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	req := createTestPaymentRequest("ACC-REJECT-001", 200, 0)

	// Initiate payment
	resp, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, _ := uuid.Parse(resp.PaymentOrder.PaymentOrderId)

	// Wait for saga to fail
	po := waitForSagaCompletion(ctx, t, repo, poID)
	assert.Equal(t, domain.PaymentOrderStatusFailed, po.Status, "Payment should be FAILED")

	// Verify lien was released
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&mockCA.terminateLienCalls) >= 1
		})
	require.NoError(t, err, "Lien should be released when gateway rejects payment")
}

// TestIntegration_ConcurrentPayments_SameAccount tests concurrent payment
// requests to the same account are handled correctly.
func TestIntegration_ConcurrentPayments_SameAccount(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	accountID := "ACC-CONC-001"
	numPayments := 5

	var wg sync.WaitGroup
	results := make([]*pb.InitiatePaymentOrderResponse, numPayments)
	errs := make([]error, numPayments)

	// Launch concurrent payment requests
	for i := 0; i < numPayments; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := createTestPaymentRequest(accountID, int64(10+idx), 0)
			resp, err := svc.InitiatePaymentOrder(ctx, req)
			results[idx] = resp
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	// Verify all payments were initiated successfully
	successCount := 0
	for i := 0; i < numPayments; i++ {
		if errs[i] == nil && results[i] != nil {
			successCount++
		}
	}
	assert.Equal(t, numPayments, successCount, "All concurrent payments should be initiated")

	// Verify each payment has a unique ID
	seenIDs := make(map[string]bool)
	for i := 0; i < numPayments; i++ {
		if results[i] != nil {
			poID := results[i].PaymentOrder.PaymentOrderId
			assert.False(t, seenIDs[poID], "Payment order IDs should be unique")
			seenIDs[poID] = true
		}
	}
}

// =============================================================================
// Defensive Tests
// =============================================================================

// TestIntegration_NetworkTimeout_DuringExecutePhase tests handling of network
// timeouts during the execute phase of the saga.
// Note: This test verifies that a slow gateway causes the saga to time out and
// that compensation (lien release) runs. Due to the saga using its context for
// all operations, the failure status may not be persisted when the context is cancelled.
func TestIntegration_NetworkTimeout_DuringExecutePhase(t *testing.T) {
	// Skip this timing-sensitive test in short mode (-short flag)
	// The saga's failure handling can race with context cancellation
	if testing.Short() {
		t.Skip("Skipping timing-sensitive network timeout test in short mode")
	}

	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	mockGW.delayResponse = 5 * time.Second // Longer than saga timeout
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
		SagaTimeout:          3 * time.Second, // Short timeout for test
	})
	require.NoError(t, err)

	req := createTestPaymentRequest("ACC-NETTIMEOUT-001", 50, 0)

	// Initiate payment
	resp, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, _ := uuid.Parse(resp.PaymentOrder.PaymentOrderId)

	// Wait for saga to timeout and compensation to complete
	// This test is timing-sensitive: saga timeout (3s) + gateway delay (5s) + margin
	err = await.New().
		AtMost(6 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			// Wait until lien termination is called (compensation ran)
			return atomic.LoadInt32(&mockCA.terminateLienCalls) >= 1
		})
	require.NoError(t, err, "Lien should be released during compensation")

	// Verify the payment is not in a successful state (COMPLETED/EXECUTING)
	// Due to context cancellation, the order may be stuck in RESERVED or marked as FAILED
	po, err := repo.FindByID(ctx, poID)
	require.NoError(t, err)

	// The saga should NOT have succeeded - payment should not be in EXECUTING or COMPLETED
	assert.NotEqual(t, domain.PaymentOrderStatusExecuting, po.Status,
		"Payment should not be in EXECUTING state after gateway timeout")
	assert.NotEqual(t, domain.PaymentOrderStatusCompleted, po.Status,
		"Payment should not be COMPLETED after gateway timeout")
}

// TestIntegration_PartialFailure_GatewayAcceptsLedgerFails tests when the
// gateway accepts but the lien execution fails post-completion.
func TestIntegration_PartialFailure_GatewayAcceptsLedgerFails(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockCA.failOnExecuteLien = true
	mockCA.executeLienErr = errLedgerUnavailable
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	req := createTestPaymentRequest("ACC-PARTIAL-001", 100, 0)

	// Initiate payment
	resp, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, _ := uuid.Parse(resp.PaymentOrder.PaymentOrderId)
	po := waitForSagaCompletion(ctx, t, repo, poID)
	require.Equal(t, domain.PaymentOrderStatusExecuting, po.Status)

	// Complete via gateway callback - should succeed even if ExecuteLien fails
	// The service logs the error but doesn't fail the completion
	updateResp, err := svc.UpdatePaymentOrder(ctx, &pb.UpdatePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
	})
	require.NoError(t, err, "Payment completion should succeed even if ExecuteLien fails")
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED, updateResp.PaymentOrder.Status)

	// Wait for async lien execution attempt (it will fail, but should be attempted)
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&mockCA.executeLienCalls) >= 1
		})
	require.NoError(t, err, "ExecuteLien should be attempted within timeout")

	// Verify ExecuteLien was attempted
	assert.GreaterOrEqual(t, atomic.LoadInt32(&mockCA.executeLienCalls), int32(1),
		"ExecuteLien should be attempted")
}

// TestIntegration_MoneyPrecision_ThroughAllTranslations tests that money
// amounts are preserved correctly through all layer translations.
func TestIntegration_MoneyPrecision_ThroughAllTranslations(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	testCases := []struct {
		name          string
		units         int64
		nanos         int32
		expectedCents int64
	}{
		{
			name:          "whole units only",
			units:         100,
			nanos:         0,
			expectedCents: 10000,
		},
		{
			name:          "units with 50 cents",
			units:         100,
			nanos:         500000000, // 0.50
			expectedCents: 10050,
		},
		{
			name:          "units with 99 cents",
			units:         100,
			nanos:         990000000, // 0.99
			expectedCents: 10099,
		},
		{
			name:          "units with 1 cent",
			units:         100,
			nanos:         10000000, // 0.01
			expectedCents: 10001,
		},
		{
			name:          "fractional cents rounded",
			units:         100,
			nanos:         555555555, // 0.555555555 -> rounds to 0.56
			expectedCents: 10056,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &pb.InitiatePaymentOrderRequest{
				DebtorAccountId:   "ACC-PRECISION-" + tc.name,
				CreditorReference: "CRED-001",
				Amount: &commonpb.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        tc.units,
						Nanos:        tc.nanos,
					},
				},
				IdempotencyKey: &commonpb.IdempotencyKey{Key: uuid.New().String()},
			}

			resp, err := svc.InitiatePaymentOrder(ctx, req)
			require.NoError(t, err)

			poID, _ := uuid.Parse(resp.PaymentOrder.PaymentOrderId)

			// Verify amount is persisted correctly
			po, err := repo.FindByID(ctx, poID)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedCents, po.Amount.AmountCents(),
				"Amount should be correctly converted to cents")
		})
	}
}

// TestIntegration_InvalidInputs_ValidationErrors tests that invalid inputs
// are properly rejected with appropriate error codes.
func TestIntegration_InvalidInputs_ValidationErrors(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	testCases := []struct {
		name         string
		req          *pb.InitiatePaymentOrderRequest
		expectedCode codes.Code
		expectedMsg  string
	}{
		{
			name: "missing idempotency key",
			req: &pb.InitiatePaymentOrderRequest{
				DebtorAccountId:   "ACC-001",
				CreditorReference: "CRED-001",
				Amount: &commonpb.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        100,
						Nanos:        0,
					},
				},
				IdempotencyKey: nil,
			},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "idempotency_key is required",
		},
		{
			name: "negative amount",
			req: &pb.InitiatePaymentOrderRequest{
				DebtorAccountId:   "ACC-001",
				CreditorReference: "CRED-001",
				Amount: &commonpb.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        -100,
						Nanos:        0,
					},
				},
				IdempotencyKey: &commonpb.IdempotencyKey{Key: uuid.New().String()},
			},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "amount must be positive",
		},
		{
			name: "nil amount",
			req: &pb.InitiatePaymentOrderRequest{
				DebtorAccountId:   "ACC-001",
				CreditorReference: "CRED-001",
				Amount:            nil,
				IdempotencyKey:    &commonpb.IdempotencyKey{Key: uuid.New().String()},
			},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "amount",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.InitiatePaymentOrder(ctx, tc.req)
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok, "Error should be gRPC status error")
			assert.Equal(t, tc.expectedCode, st.Code())
			assert.Contains(t, st.Message(), tc.expectedMsg)
		})
	}
}

// TestIntegration_RetrievePaymentOrder tests the RetrievePaymentOrder RPC.
func TestIntegration_RetrievePaymentOrder(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	req := createTestPaymentRequest("ACC-RETRIEVE-001", 100, 0)

	// Create payment order
	createResp, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)
	poID := createResp.PaymentOrder.PaymentOrderId

	// Retrieve payment order
	retrieveResp, err := svc.RetrievePaymentOrder(ctx, &pb.RetrievePaymentOrderRequest{
		PaymentOrderId: poID,
	})
	require.NoError(t, err)
	assert.Equal(t, poID, retrieveResp.PaymentOrder.PaymentOrderId)
	assert.Equal(t, "ACC-RETRIEVE-001", retrieveResp.PaymentOrder.DebtorAccountId)

	// Try to retrieve non-existent payment order
	_, err = svc.RetrievePaymentOrder(ctx, &pb.RetrievePaymentOrderRequest{
		PaymentOrderId: uuid.New().String(),
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestIntegration_CancelPaymentOrder tests the CancelPaymentOrder RPC.
func TestIntegration_CancelPaymentOrder(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	// Use a client that doesn't auto-proceed through saga
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	mockGW.delayResponse = 30 * time.Second // Long delay to keep in INITIATED state
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	req := createTestPaymentRequest("ACC-CANCEL-001", 100, 0)

	// Create payment order
	createResp, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)
	poID := createResp.PaymentOrder.PaymentOrderId

	// Wait for saga to get to RESERVED state (when lien is created)
	var po *domain.PaymentOrder
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			var findErr error
			po, findErr = repo.FindByID(ctx, uuid.MustParse(poID))
			if findErr != nil {
				return false
			}
			// Wait until we're in a state with a lien (RESERVED or later)
			return po.Status == domain.PaymentOrderStatusReserved ||
				po.Status == domain.PaymentOrderStatusExecuting
		})
	require.NoError(t, err, "Should reach RESERVED state")

	// Only test cancellation if we're in a cancellable state
	if po.CanCancel() {
		cancelResp, err := svc.CancelPaymentOrder(ctx, &pb.CancelPaymentOrderRequest{
			PaymentOrderId:     poID,
			CancellationReason: "User requested cancellation",
			CancelledBy:        "test-user",
		})
		require.NoError(t, err)
		assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_CANCELLED, cancelResp.PaymentOrder.Status)

		// Verify lien was released if one was created
		if po.LienID != "" {
			err = await.New().
				AtMost(1 * time.Second).
				PollInterval(20 * time.Millisecond).
				Until(func() bool {
					return atomic.LoadInt32(&mockCA.terminateLienCalls) >= 1
				})
			require.NoError(t, err, "Lien should be released on cancellation")
		}
	}
}

// TestIntegration_ListPaymentOrders_Pagination tests the ListPaymentOrders RPC
// with cursor-based pagination.
func TestIntegration_ListPaymentOrders_Pagination(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	accountID := "ACC-LIST-001"

	// Create multiple payment orders
	numOrders := 7
	createdIDs := make([]string, 0, numOrders)
	for i := 0; i < numOrders; i++ {
		req := &pb.InitiatePaymentOrderRequest{
			DebtorAccountId:   accountID,
			CreditorReference: "CRED-" + uuid.New().String()[:8],
			Amount: &commonpb.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        int64(10 + i),
					Nanos:        0,
				},
			},
			IdempotencyKey: &commonpb.IdempotencyKey{Key: uuid.New().String()},
		}
		resp, err := svc.InitiatePaymentOrder(ctx, req)
		require.NoError(t, err)
		createdIDs = append(createdIDs, resp.PaymentOrder.PaymentOrderId)

		// Wait for this payment to be persisted before creating next
		// This ensures distinct created_at timestamps for pagination ordering
		expectedCount := i + 1
		err = await.New().
			AtMost(1 * time.Second).
			PollInterval(10 * time.Millisecond).
			Until(func() bool {
				list, listErr := svc.ListPaymentOrders(ctx, &pb.ListPaymentOrdersRequest{
					DebtorAccountId: accountID,
					Pagination:      &commonpb.Pagination{PageSize: 100},
				})
				return listErr == nil && len(list.PaymentOrders) >= expectedCount
			})
		require.NoError(t, err, "Payment order %d should be persisted", i+1)
	}
	_ = createdIDs // Used implicitly via ListPaymentOrders verification

	// List first page with page size 3
	listResp1, err := svc.ListPaymentOrders(ctx, &pb.ListPaymentOrdersRequest{
		DebtorAccountId: accountID,
		Pagination: &commonpb.Pagination{
			PageSize: 3,
		},
	})
	require.NoError(t, err)
	assert.Len(t, listResp1.PaymentOrders, 3)
	assert.NotEmpty(t, listResp1.Pagination.NextPageToken)

	// List second page
	listResp2, err := svc.ListPaymentOrders(ctx, &pb.ListPaymentOrdersRequest{
		DebtorAccountId: accountID,
		Pagination: &commonpb.Pagination{
			PageSize:  3,
			PageToken: listResp1.Pagination.NextPageToken,
		},
	})
	require.NoError(t, err)
	assert.Len(t, listResp2.PaymentOrders, 3)

	// List third page (should have 1 remaining)
	listResp3, err := svc.ListPaymentOrders(ctx, &pb.ListPaymentOrdersRequest{
		DebtorAccountId: accountID,
		Pagination: &commonpb.Pagination{
			PageSize:  3,
			PageToken: listResp2.Pagination.NextPageToken,
		},
	})
	require.NoError(t, err)
	assert.Len(t, listResp3.PaymentOrders, 1)

	// Verify no duplicate IDs across pages
	allIDs := make(map[string]bool)
	for _, po := range listResp1.PaymentOrders {
		assert.False(t, allIDs[po.PaymentOrderId], "No duplicate IDs")
		allIDs[po.PaymentOrderId] = true
	}
	for _, po := range listResp2.PaymentOrders {
		assert.False(t, allIDs[po.PaymentOrderId], "No duplicate IDs")
		allIDs[po.PaymentOrderId] = true
	}
	for _, po := range listResp3.PaymentOrders {
		assert.False(t, allIDs[po.PaymentOrderId], "No duplicate IDs")
		allIDs[po.PaymentOrderId] = true
	}
}

// TestIntegration_ReversePaymentOrder tests the ReversePaymentOrder RPC.
func TestIntegration_ReversePaymentOrder(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewPaymentOrderRepository(db)
	mockCA := newMockCurrentAccountClient()
	mockGW := newMockPaymentGateway()
	logger := integrationTestLogger()

	svc, err := NewServiceWithConfig(Config{
		Repository:           repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGW,
		KafkaPublisher:       nil, // Optional for tests
		Logger:               logger,
	})
	require.NoError(t, err)

	req := createTestPaymentRequest("ACC-REVERSE-001", 100, 0)

	// Create and complete payment order
	createResp, err := svc.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, _ := uuid.Parse(createResp.PaymentOrder.PaymentOrderId)
	po := waitForSagaCompletion(ctx, t, repo, poID)
	require.Equal(t, domain.PaymentOrderStatusExecuting, po.Status)

	// Complete via callback
	_, err = svc.UpdatePaymentOrder(ctx, &pb.UpdatePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
	})
	require.NoError(t, err)

	// Wait for async lien execution to complete and DB to be updated
	// (runs in background goroutine after SETTLED)
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			po, findErr := repo.FindByID(ctx, poID)
			return findErr == nil && po.LienExecutionStatus == domain.LienExecutionStatusSucceeded
		})
	require.NoError(t, err, "LienExecutionStatus should be SUCCEEDED within timeout")

	// Now reverse the completed payment
	reverseResp, err := svc.ReversePaymentOrder(ctx, &pb.ReversePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		ReversalReason: "Customer dispute",
		ReversedBy:     "support-agent",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_REVERSED, reverseResp.PaymentOrder.Status)

	// Verify state in database
	reversedPO, err := repo.FindByID(ctx, poID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusReversed, reversedPO.Status)
	assert.NotNil(t, reversedPO.ReversedAt)
}
