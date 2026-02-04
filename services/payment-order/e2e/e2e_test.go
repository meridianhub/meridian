//go:build integration

// Package e2e provides end-to-end integration tests for the payment-order service.
// These tests verify the complete payment saga pattern with REAL current-account,
// financial-accounting, and position-keeping services, including compensation scenarios.
package e2e

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"gorm.io/gorm"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// ============================================================================
// Test Infrastructure (Subtask 4.1: Multi-Service E2E Setup)
// ============================================================================

// mockPaymentGateway simulates external payment gateway for testing.
// It tracks invocations to verify saga short-circuit behavior.
type mockPaymentGateway struct {
	approvalResponse bool
	errorResponse    error
	invocationCount  atomic.Int32
	responseDelay    time.Duration // For timeout testing
}

// ProcessPayment simulates gateway processing with configurable responses.
func (m *mockPaymentGateway) ProcessPayment(ctx context.Context, _ decimal.Decimal, _ string) error {
	m.invocationCount.Add(1)

	// Simulate processing delay (for timeout tests)
	if m.responseDelay > 0 {
		select {
		case <-time.After(m.responseDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if m.errorResponse != nil {
		return m.errorResponse
	}

	if !m.approvalResponse {
		return fmt.Errorf("payment declined")
	}

	return nil
}

// GetInvocationCount returns the number of times ProcessPayment was called.
func (m *mockPaymentGateway) GetInvocationCount() int32 {
	return m.invocationCount.Load()
}

// ResetInvocationCount resets the invocation counter (for test isolation).
func (m *mockPaymentGateway) ResetInvocationCount() {
	m.invocationCount.Store(0)
}

// E2ETestEnvironment encapsulates all dependencies for payment-order E2E tests
// with REAL service integration (current-account, financial-accounting, position-keeping).
type E2ETestEnvironment struct {
	// Database connection (shared across services in same testcontainer)
	DB *gorm.DB

	// Service clients (REAL gRPC clients)
	PaymentOrderClient        paymentorderv1.PaymentOrderServiceClient
	CurrentAccountClient      currentaccountv1.CurrentAccountServiceClient
	PositionKeepingClient     positionkeepingv1.PositionKeepingServiceClient
	FinancialAccountingClient financialaccountingv1.FinancialAccountingServiceClient

	// Mock payment gateway (only external dependency that's mocked)
	Gateway *mockPaymentGateway

	// Test context with tenant
	Ctx      context.Context
	TenantID tenant.TenantID

	// Test data
	AccountID string

	// Cleanup function
	Cleanup func()
}

// setupPaymentOrderE2E creates a complete E2E test environment with:
// - CockroachDB testcontainer (shared database)
// - position-keeping service with gRPC server
// - financial-accounting service with gRPC server
// - current-account service with gRPC server
// - payment-order service with gRPC server and mock gateway
// - Tenant schema with all required tables
// - gRPC clients for all services
func setupPaymentOrderE2E(t *testing.T) *E2ETestEnvironment {
	t.Helper()

	// Create CockroachDB testcontainer
	db, cleanup := testdb.SetupCockroachDB(t, nil)

	// Create tenant schema
	tenantID := tenant.TenantID(fmt.Sprintf("e2e_payment_%d", time.Now().UnixNano()))
	tenantCtx := setupMultiServiceTenantSchema(t, db, tenantID)

	// Create mock payment gateway
	gateway := &mockPaymentGateway{
		approvalResponse: true, // Default to approval
	}

	// TODO: Start position-keeping service with gRPC
	// TODO: Start financial-accounting service with gRPC
	// TODO: Start current-account service with gRPC
	// TODO: Start payment-order service with gRPC and inject mock gateway
	// TODO: Create gRPC clients for all services

	// Create test account
	accountID := "ACC-PAY-E2E-001"
	// TODO: Create account via current-account client

	env := &E2ETestEnvironment{
		DB:        db,
		Ctx:       tenantCtx,
		TenantID:  tenantID,
		Gateway:   gateway,
		AccountID: accountID,
		Cleanup:   cleanup,
	}

	return env
}

// setupMultiServiceTenantSchema creates tenant schema with tables for all services.
func setupMultiServiceTenantSchema(t *testing.T, db *gorm.DB, tenantID tenant.TenantID) context.Context {
	t.Helper()

	schemaName := tenantID.SchemaName()

	// Get raw DB connection for schema operations
	sqlDB, err := db.DB()
	require.NoError(t, err, "Failed to get SQL DB connection")

	// Create tenant schema
	_, err = sqlDB.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err, "Failed to create tenant schema %s", schemaName)

	// Apply schemas for all required services
	applyPositionKeepingSchema(t, db, schemaName)
	applyFinancialAccountingSchema(t, db, schemaName)
	applyCurrentAccountSchema(t, db, schemaName)
	applyPaymentOrderSchema(t, db, schemaName)

	// Set search_path to tenant schema for all subsequent GORM operations
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err, "Failed to set search_path to tenant schema")

	// Create context with tenant
	tenantCtx := tenant.WithTenant(context.Background(), tenantID)

	// Cleanup: drop tenant schema on test completion
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
	})

	return tenantCtx
}

// applyPositionKeepingSchema creates position table for balance tracking.
func applyPositionKeepingSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	positionTable := fmt.Sprintf("%s.position", pq.QuoteIdentifier(schemaName))
	createPositionSQL := fmt.Sprintf(`
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
		)`, positionTable)
	_, err = sqlDB.Exec(createPositionSQL)
	require.NoError(t, err, "Failed to create position table")
}

// applyFinancialAccountingSchema creates financial_booking_log and ledger_posting tables.
func applyFinancialAccountingSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	// Create financial_booking_log table
	bookingLogTable := fmt.Sprintf("%s.financial_booking_log", pq.QuoteIdentifier(schemaName))
	createBookingLogSQL := fmt.Sprintf(`
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
		)`, bookingLogTable)
	_, err = sqlDB.Exec(createBookingLogSQL)
	require.NoError(t, err, "Failed to create financial_booking_log table")

	// Create ledger_posting table
	postingTable := fmt.Sprintf("%s.ledger_posting", pq.QuoteIdentifier(schemaName))
	createPostingSQL := fmt.Sprintf(`
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
		)`, postingTable, bookingLogTable)
	_, err = sqlDB.Exec(createPostingSQL)
	require.NoError(t, err, "Failed to create ledger_posting table")

	// Create audit_outbox table for GORM hooks
	auditOutboxTable := fmt.Sprintf("%s.audit_outbox", pq.QuoteIdentifier(schemaName))
	createAuditOutboxSQL := fmt.Sprintf(`
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
		)`, auditOutboxTable)
	_, err = sqlDB.Exec(createAuditOutboxSQL)
	require.NoError(t, err, "Failed to create audit_outbox table")
}

// applyCurrentAccountSchema creates lien and account tables.
func applyCurrentAccountSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	// Create lien table
	lienTable := fmt.Sprintf("%s.lien", pq.QuoteIdentifier(schemaName))
	createLienSQL := fmt.Sprintf(`
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
		)`, lienTable)
	_, err = sqlDB.Exec(createLienSQL)
	require.NoError(t, err, "Failed to create lien table")

	// TODO: Add account table if needed for tests
}

// applyPaymentOrderSchema creates payment_order table.
func applyPaymentOrderSchema(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	paymentOrderTable := fmt.Sprintf("%s.payment_order", pq.QuoteIdentifier(schemaName))
	createPaymentOrderSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			from_account VARCHAR(100) NOT NULL,
			to_account VARCHAR(100) NOT NULL,
			amount_cents BIGINT NOT NULL,
			currency VARCHAR(32) NOT NULL,
			status VARCHAR(32) NOT NULL,
			failure_reason TEXT,
			idempotency_key VARCHAR(255) UNIQUE NOT NULL,
			transaction_id UUID,
			lien_id UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			created_by VARCHAR(100) NOT NULL,
			updated_at TIMESTAMPTZ,
			updated_by VARCHAR(100),
			deleted_at TIMESTAMPTZ NULL
		)`, paymentOrderTable)
	_, err = sqlDB.Exec(createPaymentOrderSQL)
	require.NoError(t, err, "Failed to create payment_order table")
}

// ============================================================================
// Subtask 4.2: TestPaymentSaga_E2E_HappyPath
// ============================================================================

// TestPaymentSaga_E2E_HappyPath tests the complete payment saga happy path:
// 1. Create account with balance 1000
// 2. Initiate payment 500
// 3. Verify lien created (reserve 500)
// 4. Mock gateway approval
// 5. Verify lien executed (debit 500 from position-keeping)
// 6. Verify financial booking log POSTED
// 7. Verify payment order status = COMPLETED
// 8. Verify lien status = EXECUTED
// Uses await.Until() for async lien execution status polling.
func TestPaymentSaga_E2E_HappyPath(t *testing.T) {
	t.Skip("TODO: Implement after service startup infrastructure is ready")

	env := setupPaymentOrderE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx

	// Step 1: Create account with balance 1000
	// TODO: Call current-account service to create account
	initialBalance := decimal.NewFromInt(1000)
	_ = initialBalance // Use when service client is ready

	// Step 2: Initiate payment of 500
	paymentAmount := decimal.NewFromInt(500)
	amountCents := paymentAmount.Mul(decimal.NewFromInt(100)).IntPart() // Convert to cents

	initiateResp, err := env.PaymentOrderClient.InitiatePaymentOrder(ctx, &paymentorderv1.InitiatePaymentOrderRequest{
		DebtorAccountId:   env.AccountID,
		CreditorReference: "GB82WEST12345698765432", // Valid IBAN format
		Amount: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        amountCents / 100,
				Nanos:        int32((amountCents % 100) * 10000000),
			},
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.NoError(t, err, "Failed to initiate payment")
	require.NotNil(t, initiateResp)
	require.NotNil(t, initiateResp.PaymentOrder)

	paymentOrderID := initiateResp.PaymentOrder.PaymentOrderId

	// Step 3: Verify lien created (status=RESERVED, amount=500)
	// TODO: Query lien via current-account client
	var lienID uuid.UUID
	err = await.Until(func() bool {
		// TODO: Query lien by payment order reference
		// For now, return false to fail the test
		return false
	})
	require.NoError(t, err, "Lien should be created in RESERVED status")

	// Step 4: Gateway approval is automatic (mock gateway approvalResponse=true)
	assert.Equal(t, int32(1), env.Gateway.GetInvocationCount(), "Gateway should be called once")

	// Step 5: Verify lien executed (debit 500 from position-keeping)
	err = await.New().AtMost(10 * time.Second).Until(func() bool {
		// TODO: Query lien status via current-account client
		// Status should transition from RESERVED → EXECUTED
		return false
	})
	require.NoError(t, err, "Lien should be executed after gateway approval")

	// Step 6: Verify financial booking log POSTED
	// TODO: Query financial-accounting for booking log with transaction_id matching payment order
	var bookingLogID uuid.UUID
	_ = bookingLogID

	// Step 7: Verify payment order status = COMPLETED
	// TODO: Query payment-order via gRPC or database
	_ = paymentOrderID

	// Step 8: Verify account balance = 500 (1000 - 500)
	// TODO: Query position-keeping for final balance
	expectedBalance := decimal.NewFromInt(500)
	_ = expectedBalance

	// Verify lien status = EXECUTED
	_ = lienID
}

// ============================================================================
// Subtask 4.3: TestPaymentSaga_E2E_GatewayFailure
// ============================================================================

// TestPaymentSaga_E2E_GatewayFailure tests saga compensation when gateway rejects payment:
// 1. Create account with balance 1000
// 2. Initiate payment 500
// 3. Verify lien created (status=RESERVED)
// 4. Mock gateway rejection (insufficient_funds)
// 5. Verify lien released (compensation logic)
// 6. Verify payment order status = FAILED
// 7. Verify account balance restored to 1000
func TestPaymentSaga_E2E_GatewayFailure(t *testing.T) {
	t.Skip("TODO: Implement after happy path is complete")
	// TODO: Implement test
}

// ============================================================================
// Subtask 4.4: TestPaymentSaga_E2E_LienFailure
// ============================================================================

// TestPaymentSaga_E2E_LienFailure tests saga short-circuit when lien creation fails:
// 1. Create account with balance 100
// 2. Initiate payment 500 (insufficient funds)
// 3. Verify lien creation fails
// 4. Verify gateway never called (invocationCount == 0)
// 5. Verify payment order status = FAILED
func TestPaymentSaga_E2E_LienFailure(t *testing.T) {
	t.Skip("TODO: Implement after happy path is complete")
	// TODO: Implement test
}

// ============================================================================
// Subtask 4.5: TestConcurrentLienExecution_E2E
// ============================================================================

// TestConcurrentLienExecution_E2E tests race condition handling with distributed locking.
// This test EXPOSES the race condition bug before Task 9 implements distributed locking.
// Expected behavior AFTER Task 9: only ONE execution succeeds, no duplicate debits.
func TestConcurrentLienExecution_E2E(t *testing.T) {
	t.Skip("TODO: Implement - will fail until Task 9 implements distributed locking")
	// TODO: Implement test
}

// ============================================================================
// Subtask 4.6: TestBucketEvaluation_E2E
// ============================================================================

// TestBucketEvaluation_E2E tests multi-bucket account solvency validation:
// 1. Create multi-bucket account (settlement bucket 1000, collateral bucket 500)
// 2. Initiate payment 500 from settlement bucket
// 3. Verify lien created in correct bucket
// 4. Verify payment succeeds (status=COMPLETED)
// 5. Test bucket evaluation failure with invalid bucket_id
func TestBucketEvaluation_E2E(t *testing.T) {
	t.Skip("TODO: Implement after basic saga pattern is verified")
	// TODO: Implement test
}

// ============================================================================
// Subtask 4.7: TestPaymentTimeout_E2E
// ============================================================================

// TestPaymentTimeout_E2E tests payment timeout handling and compensation:
// 1. Create account with balance 1000
// 2. Configure mock gateway timeout (35s delay)
// 3. Initiate payment 500
// 4. Verify lien created (status=RESERVED)
// 5. Verify payment order status becomes TIMEOUT
// 6. Verify lien eventually released after timeout threshold
// 7. Verify account balance restored to 1000
func TestPaymentTimeout_E2E(t *testing.T) {
	t.Skip("TODO: Implement after basic saga pattern is verified")
	// TODO: Implement test
}
