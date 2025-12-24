package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testTenantID = "test_tenant"

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&PaymentOrderEntity{}, &audit.AuditOutbox{}})

	// Create tenant schema
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create payment_order table in tenant schema (singular per entity TableName())
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.payment_order (
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
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		reserved_at TIMESTAMP WITH TIME ZONE,
		executing_at TIMESTAMP WITH TIME ZONE,
		completed_at TIMESTAMP WITH TIME ZONE,
		failed_at TIMESTAMP WITH TIME ZONE,
		cancelled_at TIMESTAMP WITH TIME ZONE,
		reversed_at TIMESTAMP WITH TIME ZONE
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create audit_outbox table in tenant schema (required for audit hooks)
	// Uses TEXT for old_values/new_values to match shared audit infrastructure
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
		record_id VARCHAR(50) NOT NULL,
		old_values TEXT,
		new_values TEXT,
		status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by VARCHAR(100),
		transaction_id VARCHAR(100),
		client_ip VARCHAR(45),
		user_agent TEXT
	)`, schemaName)).Error
	require.NoError(t, err)

	// Set search_path to tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func TestPaymentOrderRepository_Create(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)

	err = repo.Create(ctx, po)
	require.NoError(t, err)

	// Verify payment order was saved
	retrieved, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	assert.Equal(t, po.ID, retrieved.ID)
	assert.Equal(t, "acc-123", retrieved.DebtorAccountID)
	assert.Equal(t, "creditor-ref", retrieved.CreditorReference)
	amountCents, err := retrieved.Amount.ToMinorUnits()
	require.NoError(t, err)
	assert.Equal(t, int64(10000), amountCents)
	assert.Equal(t, domain.CurrencyGBP, retrieved.Amount.Currency())
	assert.Equal(t, domain.PaymentOrderStatusInitiated, retrieved.Status)
	assert.Equal(t, "idem-key-001", retrieved.IdempotencyKey)
	assert.Equal(t, "corr-001", retrieved.CorrelationID)
}

func TestPaymentOrderRepository_FindByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrPaymentOrderNotFound)
}

func TestPaymentOrderRepository_FindByIdempotencyKey(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"unique-idem-key",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	retrieved, err := repo.FindByIdempotencyKey(ctx, "unique-idem-key")
	require.NoError(t, err)

	assert.Equal(t, po.ID, retrieved.ID)
}

func TestPaymentOrderRepository_FindByIdempotencyKey_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	_, err := repo.FindByIdempotencyKey(ctx, "nonexistent-key")
	assert.ErrorIs(t, err, ErrPaymentOrderNotFound)
}

func TestPaymentOrderRepository_FindByGatewayReferenceID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Reserve and execute to set gateway reference
	require.NoError(t, po.Reserve("lien-123"))
	require.NoError(t, repo.Update(ctx, po))

	require.NoError(t, po.Execute("gateway-ref-001"))
	require.NoError(t, repo.Update(ctx, po))

	retrieved, err := repo.FindByGatewayReferenceID(ctx, "gateway-ref-001")
	require.NoError(t, err)

	assert.Equal(t, po.ID, retrieved.ID)
	assert.Equal(t, "gateway-ref-001", retrieved.GatewayReferenceID)
}

func TestPaymentOrderRepository_FindByGatewayReferenceID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	_, err := repo.FindByGatewayReferenceID(ctx, "nonexistent-gateway-ref")
	assert.ErrorIs(t, err, ErrPaymentOrderNotFound)
}

func TestPaymentOrderRepository_Update_Reserve(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Reserve the payment order
	require.NoError(t, po.Reserve("lien-123"))
	require.NoError(t, repo.Update(ctx, po))

	// Verify status was updated
	retrieved, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentOrderStatusReserved, retrieved.Status)
	assert.Equal(t, "lien-123", retrieved.LienID)
	assert.NotNil(t, retrieved.ReservedAt)
	assert.Equal(t, 2, retrieved.Version)
}

func TestPaymentOrderRepository_Update_Execute(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Progress through states
	require.NoError(t, po.Reserve("lien-123"))
	require.NoError(t, repo.Update(ctx, po))

	require.NoError(t, po.Execute("gateway-ref-001"))
	require.NoError(t, repo.Update(ctx, po))

	// Verify
	retrieved, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentOrderStatusExecuting, retrieved.Status)
	assert.Equal(t, "gateway-ref-001", retrieved.GatewayReferenceID)
	assert.NotNil(t, retrieved.ExecutingAt)
	assert.Equal(t, 3, retrieved.Version)
}

func TestPaymentOrderRepository_Update_Complete(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Progress through states
	require.NoError(t, po.Reserve("lien-123"))
	require.NoError(t, repo.Update(ctx, po))

	require.NoError(t, po.Execute("gateway-ref-001"))
	require.NoError(t, repo.Update(ctx, po))

	require.NoError(t, po.Complete("ledger-booking-001"))
	require.NoError(t, repo.Update(ctx, po))

	// Verify
	retrieved, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentOrderStatusCompleted, retrieved.Status)
	assert.Equal(t, "ledger-booking-001", retrieved.LedgerBookingID)
	assert.NotNil(t, retrieved.CompletedAt)
	assert.Equal(t, 4, retrieved.Version)
}

func TestPaymentOrderRepository_Update_Fail(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Fail the payment order
	require.NoError(t, po.Fail("Insufficient funds", "INSUFFICIENT_FUNDS"))
	require.NoError(t, repo.Update(ctx, po))

	// Verify
	retrieved, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentOrderStatusFailed, retrieved.Status)
	assert.Equal(t, "Insufficient funds", retrieved.FailureReason)
	assert.Equal(t, "INSUFFICIENT_FUNDS", retrieved.ErrorCode)
	assert.NotNil(t, retrieved.FailedAt)
	assert.Equal(t, 2, retrieved.Version)
}

func TestPaymentOrderRepository_Update_Cancel(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Cancel the payment order
	require.NoError(t, po.Cancel("User cancelled"))
	require.NoError(t, repo.Update(ctx, po))

	// Verify
	retrieved, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentOrderStatusCancelled, retrieved.Status)
	assert.Equal(t, "User cancelled", retrieved.FailureReason)
	assert.NotNil(t, retrieved.CancelledAt)
	assert.Equal(t, 2, retrieved.Version)
}

func TestPaymentOrderRepository_Update_Reverse(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Progress to completed
	require.NoError(t, po.Reserve("lien-123"))
	require.NoError(t, repo.Update(ctx, po))

	require.NoError(t, po.Execute("gateway-ref-001"))
	require.NoError(t, repo.Update(ctx, po))

	require.NoError(t, po.Complete("ledger-booking-001"))
	require.NoError(t, repo.Update(ctx, po))

	// Reverse the payment order
	require.NoError(t, po.Reverse("Chargeback requested"))
	require.NoError(t, repo.Update(ctx, po))

	// Verify
	retrieved, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentOrderStatusReversed, retrieved.Status)
	assert.Equal(t, "Chargeback requested", retrieved.FailureReason)
	assert.NotNil(t, retrieved.ReversedAt)
	assert.Equal(t, 5, retrieved.Version)
}

func TestPaymentOrderRepository_OptimisticLocking(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create initial payment order
	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Load same payment order twice (simulating concurrent access)
	po1, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	po2, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	// First update succeeds
	require.NoError(t, po1.Reserve("lien-123"))
	require.NoError(t, repo.Update(ctx, po1))

	// Second update fails due to version conflict
	require.NoError(t, po2.Fail("Should fail", "TEST_ERROR"))
	err = repo.Update(ctx, po2)
	assert.ErrorIs(t, err, ErrPaymentOrderVersionConflict)

	// Verify first transaction's changes persisted
	final, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentOrderStatusReserved, final.Status)
	assert.Equal(t, 2, final.Version)
}

func TestPaymentOrderRepository_IdempotencyKeyUniqueness(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create first payment order
	po1, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"same-idem-key",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po1))

	// Create second payment order with same idempotency key should fail
	po2, err := domain.NewPaymentOrder(
		"acc-456",
		"different-creditor",
		amount,
		"same-idem-key", // Same key
		"corr-002",
	)
	require.NoError(t, err)

	err = repo.Create(ctx, po2)
	assert.Error(t, err) // Should fail due to unique constraint
}

func TestPaymentOrderRepository_Update_NonExistent_ReturnsError(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	// Create a payment order in memory but don't save it
	amount, _ := domain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("acc-123", "creditor-ref", amount, "idem-key", "corr-001")

	// Try to update non-existent payment order
	err := repo.Update(ctx, po)

	// Should fail with not found (audit trail fetch fails before update)
	assert.True(t, errors.Is(err, ErrPaymentOrderNotFound))
}

// Defensive tests for toDomain error handling

func TestToDomain_InvalidCurrency_ReturnsError(t *testing.T) {
	entity := &PaymentOrderEntity{
		ID:                uuid.New(),
		DebtorAccountID:   "acc-123",
		CreditorReference: "creditor-ref",
		AmountCents:       10000,
		Currency:          "", // Invalid: empty currency
		Status:            "INITIATED",
		IdempotencyKey:    "idem-key",
		CorrelationID:     "corr-001",
		Version:           1,
	}

	_, err := toDomain(entity)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}

func TestPaymentOrderRepository_FindByID_CorruptedData_ReturnsError(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	// Manually insert corrupted data (empty currency)
	corruptedEntity := &PaymentOrderEntity{
		ID:                uuid.New(),
		DebtorAccountID:   "acc-123",
		CreditorReference: "creditor-ref",
		AmountCents:       10000,
		Currency:          "", // Corrupted
		Status:            "INITIATED",
		IdempotencyKey:    "corrupt-idem-key",
		CorrelationID:     "corr-001",
		Version:           1,
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	_, err := repo.FindByID(ctx, corruptedEntity.ID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}

func TestPaymentOrderRepository_CausationID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Set causation ID
	po.SetCausationID("event-123")
	require.NoError(t, repo.Update(ctx, po))

	// Verify
	retrieved, err := repo.FindByID(ctx, po.ID)
	require.NoError(t, err)

	assert.Equal(t, "event-123", retrieved.CausationID)
}

func TestPaymentOrderRepository_FindByDebtorAccountID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create two payment orders for same account
	po1, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref-1",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po1))

	po2, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref-2",
		amount,
		"idem-key-002",
		"corr-002",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po2))

	// Create payment order for different account
	po3, err := domain.NewPaymentOrder(
		"acc-456",
		"creditor-ref-3",
		amount,
		"idem-key-003",
		"corr-003",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po3))

	// Find by debtor account ID
	paymentOrders, err := repo.FindByDebtorAccountID(ctx, "acc-123")
	require.NoError(t, err)

	assert.Len(t, paymentOrders, 2)
}

func TestPaymentOrderRepository_FindByDebtorAccountID_Empty(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)

	// Find for non-existent account
	paymentOrders, err := repo.FindByDebtorAccountID(ctx, "nonexistent-acc")
	require.NoError(t, err)

	assert.Len(t, paymentOrders, 0)
}

func TestPaymentOrderRepository_FindByDebtorAccountID_CorruptedData_ReturnsError(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create valid payment order
	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Manually insert corrupted data for same account
	corruptedEntity := &PaymentOrderEntity{
		ID:                uuid.New(),
		DebtorAccountID:   "acc-123",
		CreditorReference: "creditor-ref",
		AmountCents:       10000,
		Currency:          "", // Corrupted
		Status:            "INITIATED",
		IdempotencyKey:    "corrupt-idem-key",
		CorrelationID:     "corr-001",
		Version:           1,
	}
	require.NoError(t, db.Create(corruptedEntity).Error)

	_, err = repo.FindByDebtorAccountID(ctx, "acc-123")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}
