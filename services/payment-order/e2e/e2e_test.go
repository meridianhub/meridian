//go:build integration

// Package e2e provides end-to-end integration tests for the payment-order service.
// These tests verify the complete payment saga pattern including fund reservation,
// gateway communication, ledger posting, and compensation scenarios.
//
// The tests use:
//   - CockroachDB testcontainer for persistence (production parity)
//   - Real payment-order service with saga orchestrator
//   - Mock service clients for cross-service boundaries (current-account, financial-accounting)
//   - Mock payment gateway with configurable behavior
//
// Test scenarios cover:
//   - Happy path: initiate -> reserve -> execute -> settle -> complete
//   - Gateway failure: reserve -> gateway rejects -> compensation (lien release)
//   - Lien failure: insufficient funds -> gateway never called
//   - Concurrent payments: saga isolation with separate idempotency keys
//   - Timeout: gateway delay exceeds saga timeout -> compensation
package e2e

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
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/services/payment-order/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"gorm.io/gorm"
)

// ============================================================================
// Test Infrastructure
// ============================================================================

// E2ETestEnvironment encapsulates all dependencies for payment-order E2E tests.
// It creates a real CockroachDB instance with multi-service schema, a real
// payment-order service with saga orchestrator, and configurable mock clients
// for external service boundaries.
type E2ETestEnvironment struct {
	DB   *gorm.DB
	Repo persistence.Repository

	// Real payment-order service with saga orchestrator
	Service *service.Service

	// Mock service clients (configurable per test)
	CurrentAccountClient      *mockCurrentAccountClient
	FinancialAccountingClient *mockFinancialAccountingClient
	PaymentGateway            *mockPaymentGateway

	// Test context
	Ctx      context.Context
	TenantID tenant.TenantID

	Cleanup func()
}

// setupE2E creates a complete E2E test environment.
func setupE2E(t *testing.T, opts ...e2eOption) *E2ETestEnvironment {
	t.Helper()

	cfg := &e2eConfig{
		gatewayApprove: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Create CockroachDB testcontainer
	db, cleanup := testdb.SetupCockroachDB(t, nil)

	// Create tenant schema with all service tables
	tenantID := tenant.TenantID(fmt.Sprintf("e2e_pay_%d", time.Now().UnixNano()))
	tenantCtx := setupMultiServiceSchema(t, db, tenantID)

	// Create mock clients
	caClient := newMockCurrentAccountClient()
	if cfg.insufficientFunds {
		caClient.insufficientFunds = true
	}

	faClient := newMockFinancialAccountingClient()
	gw := newMockPaymentGateway()
	gw.approvePayment = cfg.gatewayApprove
	if cfg.gatewayReject {
		gw.rejectPayment = true
		gw.approvePayment = false
	}
	if cfg.gatewayDelay > 0 {
		gw.delayResponse = cfg.gatewayDelay
	}

	// Create gateway account config
	gwAccountConfig, err := config.NewGatewayAccountConfig(map[string]*config.GatewayAccountMapping{
		"mock": {
			GatewayID:       "mock",
			ContraAccountID: "GATEWAY-MOCK-NOSTRO-001",
			AccountType:     config.AccountTypeNostro,
		},
	})
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Create real payment-order service with all dependencies
	sagaTimeout := 30 * time.Second
	if cfg.sagaTimeout > 0 {
		sagaTimeout = cfg.sagaTimeout
	}

	repo := persistence.NewPaymentOrderRepository(db)

	// Auto-wire saga execution logger when saga orchestration is enabled.
	var sagaExecLogger domain.SagaExecutionLogger
	if cfg.sagaOrchestrationEnabled {
		sagaExecLogger = persistence.NewSagaExecutionRepository(db)
	}

	svc, err := service.NewServiceWithConfig(service.Config{
		Repository:                repo,
		CurrentAccountClient:      caClient,
		FinancialAccountingClient: faClient,
		ReferenceDataClient:       newMockReferenceDataClient(),
		PaymentGateway:            gw,
		GatewayAccountConfig:      gwAccountConfig,
		IdempotencyService:        newMockIdempotencyService(),
		Logger:                    logger,
		SagaTimeout:               sagaTimeout,
		SagaOrchestrationEnabled:  cfg.sagaOrchestrationEnabled,
		SagaExecutionLogger:       sagaExecLogger,
	})
	require.NoError(t, err)

	return &E2ETestEnvironment{
		DB:                        db,
		Repo:                      repo,
		Service:                   svc,
		CurrentAccountClient:      caClient,
		FinancialAccountingClient: faClient,
		PaymentGateway:            gw,
		Ctx:                       tenantCtx,
		TenantID:                  tenantID,
		Cleanup:                   cleanup,
	}
}

// e2eConfig holds configuration options for E2E test setup.
type e2eConfig struct {
	gatewayApprove           bool
	gatewayReject            bool
	gatewayDelay             time.Duration
	insufficientFunds        bool
	sagaTimeout              time.Duration
	sagaOrchestrationEnabled bool
}

type e2eOption func(*e2eConfig)

func withGatewayReject() e2eOption {
	return func(c *e2eConfig) {
		c.gatewayReject = true
		c.gatewayApprove = false
	}
}

func withGatewayDelay(d time.Duration) e2eOption {
	return func(c *e2eConfig) {
		c.gatewayDelay = d
	}
}

func withInsufficientFunds() e2eOption {
	return func(c *e2eConfig) {
		c.insufficientFunds = true
	}
}

func withSagaTimeout(d time.Duration) e2eOption {
	return func(c *e2eConfig) {
		c.sagaTimeout = d
	}
}

// ============================================================================
// Schema Setup
// ============================================================================

// setupMultiServiceSchema creates tenant schema with tables for all services
// involved in the payment saga.
func setupMultiServiceSchema(t *testing.T, db *gorm.DB, tenantID tenant.TenantID) context.Context {
	t.Helper()

	schemaName := tenantID.SchemaName()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	// Create tenant schema
	_, err = sqlDB.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	// Apply schemas for all services
	applyPositionKeepingSchema(t, db, schemaName)
	applyFinancialAccountingSchema(t, db, schemaName)
	applyCurrentAccountSchema(t, db, schemaName)
	applyPaymentOrderSchema(t, db, schemaName)

	// Set search_path to tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	t.Cleanup(func() {
		_, _ = sqlDB.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
	})

	return ctx
}

func applyPositionKeepingSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	positionTable := fmt.Sprintf("%s.position", pq.QuoteIdentifier(schemaName))
	_, err = sqlDB.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			created_by VARCHAR(100) NOT NULL,
			deleted_at TIMESTAMPTZ NULL,
			account_id VARCHAR(34) NOT NULL,
			instrument_code VARCHAR(32) NOT NULL,
			bucket_key VARCHAR(256) NOT NULL,
			amount DECIMAL(38, 18) NOT NULL,
			dimension VARCHAR(32) NOT NULL DEFAULT 'Monetary',
			reference_id UUID NULL,
			status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
			UNIQUE (account_id, instrument_code, bucket_key, deleted_at)
		)`, positionTable))
	require.NoError(t, err)
}

func applyFinancialAccountingSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	bookingLogTable := fmt.Sprintf("%s.financial_booking_log", pq.QuoteIdentifier(schemaName))
	_, err = sqlDB.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY,
			financial_account_type TEXT NOT NULL,
			product_service_reference TEXT NOT NULL,
			business_unit_reference TEXT NOT NULL,
			chart_of_accounts_rules TEXT,
			base_currency TEXT NOT NULL,
			status TEXT NOT NULL,
			idempotency_key TEXT UNIQUE NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			created_by VARCHAR(255),
			updated_by VARCHAR(255),
			version INT NOT NULL DEFAULT 1,
			deleted_at TIMESTAMP
		)`, bookingLogTable))
	require.NoError(t, err)

	postingTable := fmt.Sprintf("%s.ledger_posting", pq.QuoteIdentifier(schemaName))
	_, err = sqlDB.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY,
			financial_booking_log_id UUID NOT NULL REFERENCES %s(id) ON DELETE RESTRICT,
			posting_direction TEXT NOT NULL,
			amount_cents BIGINT NOT NULL,
			currency VARCHAR(32) NOT NULL,
			dimension_type VARCHAR(20) DEFAULT 'CURRENCY',
			instrument_version INTEGER DEFAULT 1,
			instrument_precision INTEGER DEFAULT 2,
			attributes JSONB DEFAULT '{}',
			account_id TEXT NOT NULL,
			value_date TIMESTAMP NOT NULL,
			posting_result TEXT,
			correlation_id TEXT,
			status TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP,
			created_by VARCHAR(255),
			updated_by VARCHAR(255),
			deleted_at TIMESTAMP
		)`, postingTable, bookingLogTable))
	require.NoError(t, err)

	auditOutboxTable := fmt.Sprintf("%s.audit_outbox", pq.QuoteIdentifier(schemaName))
	_, err = sqlDB.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id VARCHAR(50) NOT NULL,
			old_values TEXT,
			new_values TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			retry_count INT NOT NULL DEFAULT 0,
			last_error TEXT,
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT
		)`, auditOutboxTable))
	require.NoError(t, err)
}

func applyCurrentAccountSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	lienTable := fmt.Sprintf("%s.lien", pq.QuoteIdentifier(schemaName))
	_, err = sqlDB.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			account_id VARCHAR(34) NOT NULL,
			instrument_code VARCHAR(32) NOT NULL,
			amount DECIMAL(38, 18) NOT NULL,
			bucket_key VARCHAR(256) NOT NULL,
			status VARCHAR(32) NOT NULL,
			reference_id UUID NULL,
			idempotency_key VARCHAR(255) UNIQUE NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			created_by VARCHAR(100) NOT NULL,
			updated_at TIMESTAMPTZ,
			updated_by VARCHAR(100),
			deleted_at TIMESTAMPTZ NULL
		)`, lienTable))
	require.NoError(t, err)
}

func applyPaymentOrderSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	paymentOrderTable := fmt.Sprintf("%s.payment_order", pq.QuoteIdentifier(schemaName))
	_, err = sqlDB.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
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
			instrument_code VARCHAR(32),
			payment_attributes JSONB,
			bucket_id VARCHAR(255),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			reserved_at TIMESTAMPTZ,
			executing_at TIMESTAMPTZ,
			completed_at TIMESTAMPTZ,
			failed_at TIMESTAMPTZ,
			cancelled_at TIMESTAMPTZ,
			reversed_at TIMESTAMPTZ
		)`, paymentOrderTable))
	require.NoError(t, err)

	// Create audit_outbox in same schema for GORM hooks
	auditTable := fmt.Sprintf("%s.audit_outbox", pq.QuoteIdentifier(schemaName))
	// Ignore error if already exists from financial-accounting schema setup
	_, _ = sqlDB.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id VARCHAR(50) NOT NULL,
			old_values TEXT,
			new_values TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			retry_count INT NOT NULL DEFAULT 0,
			last_error TEXT,
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT
		)`, auditTable))

	// Create saga_executions table for saga audit trail
	sagaExecTable := fmt.Sprintf("%s.saga_executions", pq.QuoteIdentifier(schemaName))
	_, err = sqlDB.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			payment_order_id UUID NOT NULL,
			saga_name VARCHAR(128) NOT NULL,
			saga_version INT NOT NULL DEFAULT 0,
			status VARCHAR(32) NOT NULL DEFAULT 'RUNNING',
			correlation_id VARCHAR(128) NOT NULL DEFAULT '',
			input JSONB NOT NULL DEFAULT '{}',
			output JSONB NOT NULL DEFAULT '{}',
			error_message TEXT NOT NULL DEFAULT '',
			step_count INT NOT NULL DEFAULT 0,
			duration_ms BIGINT NOT NULL DEFAULT 0,
			started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			completed_at TIMESTAMPTZ
		)`, sagaExecTable))
	require.NoError(t, err)
}

// ============================================================================
// Mock Implementations
// ============================================================================

// mockCurrentAccountClient implements service.CurrentAccountClient for E2E tests.
type mockCurrentAccountClient struct {
	mu                 sync.RWMutex
	initiateLienCalls  int32
	terminateLienCalls int32
	executeLienCalls   int32
	insufficientFunds  bool
	failOnExecuteLien  bool
	executeLienErr     error
	lienCounter        int32
	executedLiens      map[string]bool
	terminatedLiens    map[string]bool
	lastLienID         string
}

func newMockCurrentAccountClient() *mockCurrentAccountClient {
	return &mockCurrentAccountClient{
		executedLiens:   make(map[string]bool),
		terminatedLiens: make(map[string]bool),
	}
}

func (m *mockCurrentAccountClient) InitiateLien(_ context.Context, req *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
	atomic.AddInt32(&m.initiateLienCalls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.insufficientFunds {
		return nil, fmt.Errorf("insufficient funds for account %s", req.AccountId)
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
		return nil, errors.New("mock execute lien failure")
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

func (m *mockCurrentAccountClient) Close() error { return nil }

// mockFinancialAccountingClient implements service.FinancialAccountingClient.
type mockFinancialAccountingClient struct {
	mu                        sync.RWMutex
	initiateBookingLogCalls   int32
	captureLedgerPostingCalls int32
	updateBookingLogCalls     int32
	lastBookingLogID          string
}

func newMockFinancialAccountingClient() *mockFinancialAccountingClient {
	return &mockFinancialAccountingClient{}
}

func (m *mockFinancialAccountingClient) InitiateFinancialBookingLog(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	atomic.AddInt32(&m.initiateBookingLogCalls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()

	bookingLogID := "BL-" + uuid.New().String()
	m.lastBookingLogID = bookingLogID

	return &financialaccountingv1.InitiateFinancialBookingLogResponse{
		FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
			Id: bookingLogID,
		},
	}, nil
}

func (m *mockFinancialAccountingClient) CaptureLedgerPosting(_ context.Context, _ *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	atomic.AddInt32(&m.captureLedgerPostingCalls, 1)

	return &financialaccountingv1.CaptureLedgerPostingResponse{
		LedgerPosting: &financialaccountingv1.LedgerPosting{
			Id: "LP-" + uuid.New().String(),
		},
	}, nil
}

func (m *mockFinancialAccountingClient) UpdateFinancialBookingLog(_ context.Context, _ *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	atomic.AddInt32(&m.updateBookingLogCalls, 1)
	m.mu.RLock()
	defer m.mu.RUnlock()

	return &financialaccountingv1.UpdateFinancialBookingLogResponse{
		FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
			Id: m.lastBookingLogID,
		},
	}, nil
}

func (m *mockFinancialAccountingClient) Close() error { return nil }

// mockPaymentGateway implements gateway.PaymentGateway.
type mockPaymentGateway struct {
	mu               sync.RWMutex
	sendPaymentCalls int32
	approvePayment   bool
	rejectPayment    bool
	failOnSend       bool
	sendErr          error
	delayResponse    time.Duration
	lastGatewayRefID string
}

func newMockPaymentGateway() *mockPaymentGateway {
	return &mockPaymentGateway{
		approvePayment: true,
	}
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
		return gateway.PaymentResponse{}, errors.New("mock gateway failure")
	}

	if m.rejectPayment {
		return gateway.PaymentResponse{
			Status:  gateway.StatusRejected,
			Message: "Payment rejected by gateway",
		}, nil
	}

	gatewayRefID := "GW-" + uuid.New().String()
	m.lastGatewayRefID = gatewayRefID

	return gateway.PaymentResponse{
		Status:             gateway.StatusAccepted,
		GatewayReferenceID: gatewayRefID,
		Message:            "Payment accepted",
	}, nil
}

// mockReferenceDataClient implements service.ReferenceDataClient.
type mockReferenceDataClient struct{}

func newMockReferenceDataClient() *mockReferenceDataClient {
	return &mockReferenceDataClient{}
}

func (m *mockReferenceDataClient) RetrieveInstrument(_ context.Context, code string) (*service.InstrumentInfo, error) {
	return &service.InstrumentInfo{Code: code, FungibilityKeyExpression: ""}, nil
}

func (m *mockReferenceDataClient) GetSaga(_ context.Context, name string, version int) (*service.SagaDefinition, error) {
	return &service.SagaDefinition{
		ID:      uuid.New().String(),
		Name:    name,
		Version: version,
		Script: `# Saga: payment_execution
def payment_execution():
    ctx = input_data
    step(name="reserve_funds")
    lien_result = payment_order.create_lien(
        account_id=ctx.get("debtor_account_id"),
        amount_cents=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        payment_order_id=ctx.get("payment_order_id"),
        instrument_code=ctx.get("instrument_code", ""),
        payment_attributes=ctx.get("payment_attributes", {}),
    )
    lien_id = lien_result.lien_id
    bucket_id = lien_result.bucket_id
    step(name="send_to_gateway")
    gateway_result = payment_order.send_to_gateway(
        payment_order_id=ctx.get("payment_order_id"),
        debtor_account_id=ctx.get("debtor_account_id"),
        creditor_reference=ctx.get("creditor_reference"),
        amount_cents=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        idempotency_key=ctx.get("idempotency_key"),
    )
    gateway_reference_id = gateway_result.gateway_reference_id
    gateway_status = gateway_result.gateway_status
    result = {
        "lien_id": lien_id,
        "bucket_id": bucket_id,
        "gateway_reference_id": gateway_reference_id,
        "gateway_status": gateway_status,
    }
    return result

output = payment_execution()
`,
		Status: "ACTIVE",
	}, nil
}

func (m *mockReferenceDataClient) Close() error { return nil }

// mockIdempotencyService implements idempotency.Service.
type mockIdempotencyService struct {
	mu      sync.RWMutex
	results map[string]*idempotency.Result
}

func newMockIdempotencyService() *mockIdempotencyService {
	return &mockIdempotencyService{
		results: make(map[string]*idempotency.Result),
	}
}

func (m *mockIdempotencyService) Check(_ context.Context, key idempotency.Key) (*idempotency.Result, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result, ok := m.results[key.String()]
	if !ok {
		return nil, idempotency.ErrResultNotFound
	}
	if result.Status == idempotency.StatusCompleted {
		return result, idempotency.ErrOperationAlreadyProcessed
	}
	return result, nil
}

func (m *mockIdempotencyService) MarkPending(_ context.Context, key idempotency.Key, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[key.String()] = &idempotency.Result{Key: key, Status: idempotency.StatusPending, TTL: ttl}
	return nil
}

func (m *mockIdempotencyService) StoreResult(_ context.Context, result idempotency.Result) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[result.Key.String()] = &result
	return nil
}

func (m *mockIdempotencyService) Delete(_ context.Context, key idempotency.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.results, key.String())
	return nil
}

func (m *mockIdempotencyService) Acquire(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
	return nil
}

func (m *mockIdempotencyService) Release(_ context.Context, _ idempotency.Key, _ string) error {
	return nil
}

func (m *mockIdempotencyService) Refresh(_ context.Context, _ idempotency.Key, _ string, _ time.Duration) error {
	return nil
}

func (m *mockIdempotencyService) IsHeld(_ context.Context, _ idempotency.Key) (bool, error) {
	return false, nil
}

// ============================================================================
// Test Helpers
// ============================================================================

const defaultSagaWaitTimeout = 15 * time.Second

// createPaymentRequest creates a standard payment order request for testing.
func createPaymentRequest(accountID string, units int64) *paymentorderv1.InitiatePaymentOrderRequest {
	return &paymentorderv1.InitiatePaymentOrderRequest{
		DebtorAccountId:   accountID,
		CreditorReference: "GB82WEST12345698765432",
		Amount: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        units,
				Nanos:        0,
			},
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	}
}

// waitForSagaTerminal polls until the payment order reaches a terminal or executing state.
func waitForSagaTerminal(ctx context.Context, t *testing.T, repo persistence.Repository, poID uuid.UUID) *domain.PaymentOrder {
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
			switch po.Status {
			case domain.PaymentOrderStatusCompleted,
				domain.PaymentOrderStatusFailed,
				domain.PaymentOrderStatusCancelled,
				domain.PaymentOrderStatusReversed,
				domain.PaymentOrderStatusExecuting:
				result = po
				return true
			case domain.PaymentOrderStatusInitiated,
				domain.PaymentOrderStatusReserved:
				// Non-terminal states, keep polling
				return false
			}
			return false
		})
	require.NoError(t, err, "Timeout waiting for saga to reach terminal state for %s", poID)
	return result
}

// ============================================================================
// Test: Happy Path - Initiate to Complete
// ============================================================================

func TestPaymentSaga_E2E_HappyPath(t *testing.T) {
	env := setupE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx

	// Step 1: Initiate payment of 500 GBP
	req := createPaymentRequest("ACC-E2E-HAPPY-001", 500)
	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err, "InitiatePaymentOrder should succeed")
	require.NotNil(t, initiateResp.PaymentOrder)
	assert.Equal(t, paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED,
		initiateResp.PaymentOrder.Status)

	paymentOrderID := initiateResp.PaymentOrder.PaymentOrderId
	poID, err := uuid.Parse(paymentOrderID)
	require.NoError(t, err)

	// Step 2: Wait for saga to reach EXECUTING (lien reserved + gateway accepted)
	po := waitForSagaTerminal(ctx, t, env.Repo, poID)
	assert.Equal(t, domain.PaymentOrderStatusExecuting, po.Status,
		"Payment should reach EXECUTING state after saga")
	assert.NotEmpty(t, po.LienID, "Lien ID should be set")
	assert.NotEmpty(t, po.GatewayReferenceID, "Gateway reference ID should be set")

	// Step 3: Verify mock service calls
	assert.Equal(t, int32(1), atomic.LoadInt32(&env.CurrentAccountClient.initiateLienCalls),
		"InitiateLien should be called exactly once")
	assert.Equal(t, int32(1), atomic.LoadInt32(&env.PaymentGateway.sendPaymentCalls),
		"SendPayment should be called exactly once")

	// Step 4: Simulate gateway SETTLED callback
	updateResp, err := env.Service.UpdatePaymentOrder(ctx, &paymentorderv1.UpdatePaymentOrderRequest{
		PaymentOrderId: paymentOrderID,
		GatewayStatus:  paymentorderv1.GatewayStatus_GATEWAY_STATUS_SETTLED,
		IdempotencyKey: &commonv1.IdempotencyKey{Key: uuid.New().String()},
	})
	require.NoError(t, err, "SETTLED callback should succeed")
	assert.Equal(t, paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
		updateResp.PaymentOrder.Status)

	// Step 5: Wait for async lien execution
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&env.CurrentAccountClient.executeLienCalls) >= 1
		})
	require.NoError(t, err, "ExecuteLien should be called within timeout")

	// Step 6: Verify financial accounting calls (ledger posting on SETTLED)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&env.FinancialAccountingClient.initiateBookingLogCalls),
		int32(1), "Booking log should be initiated")
	assert.GreaterOrEqual(t, atomic.LoadInt32(&env.FinancialAccountingClient.captureLedgerPostingCalls),
		int32(2), "At least 2 ledger postings (debit + credit)")

	// Step 7: Verify final DB state
	finalPO, err := env.Repo.FindByID(ctx, poID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusCompleted, finalPO.Status)
	assert.NotNil(t, finalPO.CompletedAt)
	assert.NotEmpty(t, finalPO.LedgerBookingID, "Ledger booking ID should be set")
}

// ============================================================================
// Test: Gateway Failure - Compensation Releases Lien
// ============================================================================

func TestPaymentSaga_E2E_GatewayFailure(t *testing.T) {
	env := setupE2E(t, withGatewayReject())
	defer env.Cleanup()

	ctx := env.Ctx

	// Initiate payment
	req := createPaymentRequest("ACC-E2E-GWFAIL-001", 500)
	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, err := uuid.Parse(initiateResp.PaymentOrder.PaymentOrderId)
	require.NoError(t, err)

	// Wait for saga to fail
	po := waitForSagaTerminal(ctx, t, env.Repo, poID)
	assert.Equal(t, domain.PaymentOrderStatusFailed, po.Status,
		"Payment should be FAILED when gateway rejects")
	assert.NotEmpty(t, po.FailureReason, "Failure reason should be set")

	// Verify lien was created then released (compensation)
	assert.Equal(t, int32(1), atomic.LoadInt32(&env.CurrentAccountClient.initiateLienCalls),
		"Lien should have been created")

	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&env.CurrentAccountClient.terminateLienCalls) >= 1
		})
	require.NoError(t, err, "Lien should be released during compensation")

	// Verify gateway was called
	assert.Equal(t, int32(1), atomic.LoadInt32(&env.PaymentGateway.sendPaymentCalls),
		"Gateway should have been called once")
}

// ============================================================================
// Test: Lien Failure - Gateway Never Called
// ============================================================================

func TestPaymentSaga_E2E_LienFailure(t *testing.T) {
	env := setupE2E(t, withInsufficientFunds())
	defer env.Cleanup()

	ctx := env.Ctx

	// Initiate payment that will fail at lien stage
	req := createPaymentRequest("ACC-E2E-LIENFAIL-001", 500)
	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, err := uuid.Parse(initiateResp.PaymentOrder.PaymentOrderId)
	require.NoError(t, err)

	// Wait for saga to fail
	po := waitForSagaTerminal(ctx, t, env.Repo, poID)
	assert.Equal(t, domain.PaymentOrderStatusFailed, po.Status,
		"Payment should be FAILED when lien creation fails")
	assert.Contains(t, po.FailureReason, "insufficient funds",
		"Failure reason should mention insufficient funds")

	// Gateway should NEVER be called when lien fails (saga short-circuit)
	assert.Equal(t, int32(0), atomic.LoadInt32(&env.PaymentGateway.sendPaymentCalls),
		"Gateway should not be called when lien creation fails")

	// No lien to terminate since creation failed
	assert.Equal(t, int32(0), atomic.LoadInt32(&env.CurrentAccountClient.terminateLienCalls),
		"No lien termination needed when creation failed")
}

// ============================================================================
// Test: Concurrent Payments - Saga Isolation
// ============================================================================

func TestConcurrentPayments_E2E_SagaIsolation(t *testing.T) {
	env := setupE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx
	numPayments := 5

	var wg sync.WaitGroup
	results := make([]*paymentorderv1.InitiatePaymentOrderResponse, numPayments)
	errs := make([]error, numPayments)

	// Launch concurrent payment requests with different idempotency keys
	for i := 0; i < numPayments; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := createPaymentRequest(
				fmt.Sprintf("ACC-E2E-CONC-%03d", idx),
				int64(100+idx*10),
			)
			resp, err := env.Service.InitiatePaymentOrder(ctx, req)
			results[idx] = resp
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// Verify all payments initiated successfully
	successCount := 0
	paymentIDs := make(map[string]bool)
	for i := 0; i < numPayments; i++ {
		if errs[i] == nil && results[i] != nil {
			successCount++
			poID := results[i].PaymentOrder.PaymentOrderId
			assert.False(t, paymentIDs[poID], "Payment order IDs should be unique")
			paymentIDs[poID] = true
		}
	}
	assert.Equal(t, numPayments, successCount,
		"All concurrent payments should be initiated successfully")

	// Wait for all sagas to reach terminal state
	for i := 0; i < numPayments; i++ {
		if results[i] != nil {
			poID, parseErr := uuid.Parse(results[i].PaymentOrder.PaymentOrderId)
			require.NoError(t, parseErr, "Failed to parse payment order ID")
			po := waitForSagaTerminal(ctx, t, env.Repo, poID)
			assert.Equal(t, domain.PaymentOrderStatusExecuting, po.Status,
				"Each concurrent payment should reach EXECUTING state")
		}
	}

	// Verify each payment got its own lien
	assert.Equal(t, int32(numPayments), atomic.LoadInt32(&env.CurrentAccountClient.initiateLienCalls),
		"Each payment should create its own lien")
	assert.Equal(t, int32(numPayments), atomic.LoadInt32(&env.PaymentGateway.sendPaymentCalls),
		"Each payment should call the gateway")
}

// ============================================================================
// Test: Payment Timeout - Compensation After Saga Timeout
// ============================================================================

func TestPaymentSaga_E2E_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping timing-sensitive timeout test in short mode")
	}

	// Gateway delay (5s) exceeds saga timeout (3s)
	env := setupE2E(t,
		withGatewayDelay(5*time.Second),
		withSagaTimeout(3*time.Second),
	)
	defer env.Cleanup()

	ctx := env.Ctx

	req := createPaymentRequest("ACC-E2E-TIMEOUT-001", 500)
	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, err := uuid.Parse(initiateResp.PaymentOrder.PaymentOrderId)
	require.NoError(t, err)

	// Wait for saga timeout and compensation
	err = await.New().
		AtMost(8 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			// Compensation should release the lien
			return atomic.LoadInt32(&env.CurrentAccountClient.terminateLienCalls) >= 1
		})
	require.NoError(t, err, "Lien should be released after saga timeout")

	// Verify payment is not in a successful state
	po, err := env.Repo.FindByID(ctx, poID)
	require.NoError(t, err)
	assert.NotEqual(t, domain.PaymentOrderStatusCompleted, po.Status,
		"Payment should not be COMPLETED after timeout")
	assert.NotEqual(t, domain.PaymentOrderStatusExecuting, po.Status,
		"Payment should not be EXECUTING after timeout")
}

// ============================================================================
// Test: Idempotency - Duplicate Requests Return Same Result
// ============================================================================

func TestPaymentSaga_E2E_Idempotency(t *testing.T) {
	env := setupE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx

	idempotencyKey := uuid.New().String()
	req := &paymentorderv1.InitiatePaymentOrderRequest{
		DebtorAccountId:   "ACC-E2E-IDEMP-001",
		CreditorReference: "GB82WEST12345698765432",
		Amount: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
			},
		},
		IdempotencyKey: &commonv1.IdempotencyKey{Key: idempotencyKey},
	}

	// First request
	resp1, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)
	poID1 := resp1.PaymentOrder.PaymentOrderId

	// Wait for saga to start
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&env.CurrentAccountClient.initiateLienCalls) >= 1
		})
	require.NoError(t, err)

	// Duplicate request with same idempotency key
	resp2, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)
	poID2 := resp2.PaymentOrder.PaymentOrderId

	assert.Equal(t, poID1, poID2, "Same idempotency key should return same payment order")

	// InitiateLien should only be called once (not for the duplicate)
	// Give some time for potential extra calls
	time.Sleep(200 * time.Millisecond) //nolint:forbidigo // gives time for potential duplicate calls to materialize
	assert.LessOrEqual(t, atomic.LoadInt32(&env.CurrentAccountClient.initiateLienCalls), int32(1),
		"InitiateLien should be called at most once for idempotent requests")
}

// ============================================================================
// Test: Reversal After Completion
// ============================================================================

func TestPaymentSaga_E2E_Reversal(t *testing.T) {
	env := setupE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx

	// Create and complete a payment
	req := createPaymentRequest("ACC-E2E-REVERSE-001", 200)
	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, err := uuid.Parse(initiateResp.PaymentOrder.PaymentOrderId)
	require.NoError(t, err)
	po := waitForSagaTerminal(ctx, t, env.Repo, poID)
	require.Equal(t, domain.PaymentOrderStatusExecuting, po.Status)

	// Settle via gateway callback
	_, err = env.Service.UpdatePaymentOrder(ctx, &paymentorderv1.UpdatePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		GatewayStatus:  paymentorderv1.GatewayStatus_GATEWAY_STATUS_SETTLED,
		IdempotencyKey: &commonv1.IdempotencyKey{Key: uuid.New().String()},
	})
	require.NoError(t, err)

	// Wait for lien execution to complete
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			p, findErr := env.Repo.FindByID(ctx, poID)
			return findErr == nil && p.LienExecutionStatus == domain.LienExecutionStatusSucceeded
		})
	require.NoError(t, err)

	// Reverse the completed payment
	reverseResp, err := env.Service.ReversePaymentOrder(ctx, &paymentorderv1.ReversePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		ReversalReason: "E2E test reversal",
		ReversedBy:     "e2e-test",
		IdempotencyKey: &commonv1.IdempotencyKey{Key: uuid.New().String()},
	})
	require.NoError(t, err)
	assert.Equal(t, paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_REVERSED,
		reverseResp.PaymentOrder.Status)

	// Verify reversal in DB
	reversedPO, err := env.Repo.FindByID(ctx, poID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusReversed, reversedPO.Status)
	assert.NotNil(t, reversedPO.ReversedAt)

	// Verify compensating ledger entries were created
	assert.GreaterOrEqual(t, atomic.LoadInt32(&env.FinancialAccountingClient.initiateBookingLogCalls),
		int32(2), "Should have original + reversal booking logs")
}
