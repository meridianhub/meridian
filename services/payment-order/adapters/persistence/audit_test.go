package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupTestDBWithAudit creates a test database with both payment_order and audit tables.
func setupTestDBWithAudit(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	return testdb.SetupTestDB(t,
		testdb.WithModels(&PaymentOrderEntity{}, &audit.AuditOutbox{}),
		testdb.WithTenant(testTenantID),
	)
}

// getAuditEntries retrieves all audit entries for a specific record from the outbox.
func getAuditEntries(t *testing.T, db *gorm.DB, recordID uuid.UUID) []audit.AuditOutbox {
	t.Helper()
	var entries []audit.AuditOutbox
	err := db.Where("record_id = ?", recordID).Order("created_at ASC").Find(&entries).Error
	require.NoError(t, err)
	return entries
}

// =============================================================================
// Audit Tests
// =============================================================================

func TestAudit_PaymentOrderCreation_IsAudited(t *testing.T) {
	db, ctx, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-audit-001",
		"corr-001",
	)
	require.NoError(t, err)

	err = repo.Create(ctx, po)
	require.NoError(t, err)

	// Verify audit entry was created
	entries := getAuditEntries(t, db, po.ID)
	require.Len(t, entries, 1, "Should have exactly one audit entry for INSERT")

	entry := entries[0]
	assert.Equal(t, "payment_order", entry.Table)
	assert.Equal(t, "INSERT", entry.Operation)
	assert.Equal(t, po.ID.String(), entry.RecordID)
	assert.Equal(t, "pending", entry.Status)
	assert.Empty(t, entry.OldValues, "INSERT should have no old values")
	assert.NotEmpty(t, entry.NewValues, "INSERT should have new values")

	// Verify ChangedBy defaults to system
	require.NotNil(t, entry.ChangedBy)
	assert.Equal(t, "system", *entry.ChangedBy)

	// Verify new values contain expected data
	var newValues map[string]interface{}
	err = json.Unmarshal([]byte(entry.NewValues), &newValues)
	require.NoError(t, err)
	assert.Equal(t, "acc-123", newValues["DebtorAccountID"])
	assert.Equal(t, "INITIATED", newValues["Status"])
}

func TestAudit_PaymentOrderStatusTransition_CreatesAuditTrail(t *testing.T) {
	db, ctx, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create payment order
	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-audit-002",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Transition: INITIATED -> RESERVED
	require.NoError(t, po.Reserve("lien-123"))
	require.NoError(t, repo.Update(ctx, po))

	// Transition: RESERVED -> EXECUTING
	require.NoError(t, po.Execute("gateway-ref-001"))
	require.NoError(t, repo.Update(ctx, po))

	// Transition: EXECUTING -> COMPLETED
	require.NoError(t, po.Complete("ledger-booking-001"))
	require.NoError(t, repo.Update(ctx, po))

	// Verify audit trail
	entries := getAuditEntries(t, db, po.ID)
	require.Len(t, entries, 4, "Should have 4 audit entries: INSERT + 3 UPDATEs")

	// Verify INSERT
	assert.Equal(t, "INSERT", entries[0].Operation)

	// Verify INITIATED -> RESERVED
	assert.Equal(t, "UPDATE", entries[1].Operation)
	assertStatusTransition(t, entries[1], "INITIATED", "RESERVED")

	// Verify RESERVED -> EXECUTING
	assert.Equal(t, "UPDATE", entries[2].Operation)
	assertStatusTransition(t, entries[2], "RESERVED", "EXECUTING")

	// Verify EXECUTING -> COMPLETED
	assert.Equal(t, "UPDATE", entries[3].Operation)
	assertStatusTransition(t, entries[3], "EXECUTING", "COMPLETED")
}

func TestAudit_PaymentOrderFailure_CapturesReasonAndErrorCode(t *testing.T) {
	db, ctx, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create payment order
	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-audit-003",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Fail the payment order
	require.NoError(t, po.Fail("Insufficient funds in account", "INSUFFICIENT_FUNDS"))
	require.NoError(t, repo.Update(ctx, po))

	// Verify audit trail
	entries := getAuditEntries(t, db, po.ID)
	require.Len(t, entries, 2, "Should have INSERT + UPDATE")

	// Verify failure audit entry captures critical fields
	failEntry := entries[1]
	assert.Equal(t, "UPDATE", failEntry.Operation)
	require.NotEmpty(t, failEntry.NewValues)

	var newValues map[string]interface{}
	err = json.Unmarshal([]byte(failEntry.NewValues), &newValues)
	require.NoError(t, err)

	assert.Equal(t, "FAILED", newValues["Status"])
	assert.Equal(t, "Insufficient funds in account", newValues["FailureReason"])
	assert.Equal(t, "INSUFFICIENT_FUNDS", newValues["ErrorCode"])
}

func TestAudit_PaymentOrderLienTracking_CapturesLienExecutionStatus(t *testing.T) {
	db, ctx, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Create and progress payment order through lifecycle
	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-audit-004",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Reserve with lien
	require.NoError(t, po.Reserve("lien-123"))
	require.NoError(t, repo.Update(ctx, po))

	// Execute
	require.NoError(t, po.Execute("gateway-ref-001"))
	require.NoError(t, repo.Update(ctx, po))

	// Complete
	require.NoError(t, po.Complete("ledger-booking-001"))
	require.NoError(t, repo.Update(ctx, po))

	// Mark lien execution as succeeded
	po.SetLienExecutionSucceeded()
	require.NoError(t, repo.Update(ctx, po))

	// Verify audit trail captures lien ID and execution status
	entries := getAuditEntries(t, db, po.ID)
	require.GreaterOrEqual(t, len(entries), 5, "Should have at least 5 audit entries")

	// Check that the reserved entry captures lien_id
	reservedEntry := entries[1]
	require.NotEmpty(t, reservedEntry.NewValues)
	var reservedNewValues map[string]interface{}
	err = json.Unmarshal([]byte(reservedEntry.NewValues), &reservedNewValues)
	require.NoError(t, err)
	assert.Equal(t, "lien-123", reservedNewValues["LienID"])

	// Check final entry captures lien execution status
	finalEntry := entries[len(entries)-1]
	require.NotEmpty(t, finalEntry.NewValues)
	var finalNewValues map[string]interface{}
	err = json.Unmarshal([]byte(finalEntry.NewValues), &finalNewValues)
	require.NoError(t, err)
	assert.Equal(t, "SUCCEEDED", finalNewValues["LienExecutionStatus"])
}

func TestAudit_PaymentOrderDeletion_IsAudited(t *testing.T) {
	db, _, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Create payment order directly via entity
	entity := &PaymentOrderEntity{
		ID:                uuid.New(),
		DebtorAccountID:   "acc-delete-test",
		CreditorReference: "creditor-ref",
		AmountCents:       10000,
		Currency:          "GBP",
		Status:            "INITIATED",
		IdempotencyKey:    "idem-key-delete-001",
		CorrelationID:     "corr-001",
		Version:           1,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	require.NoError(t, db.Create(entity).Error)

	// Delete the entity
	require.NoError(t, db.Delete(entity).Error)

	// Verify audit entries
	entries := getAuditEntries(t, db, entity.ID)
	require.Len(t, entries, 2, "Should have INSERT + DELETE")

	// Verify DELETE audit entry
	deleteEntry := entries[1]
	assert.Equal(t, "DELETE", deleteEntry.Operation)
	assert.NotEmpty(t, deleteEntry.OldValues, "DELETE should have old values")
	assert.Empty(t, deleteEntry.NewValues, "DELETE should have no new values")

	// Verify old values contain the deleted data
	var oldValues map[string]interface{}
	err := json.Unmarshal([]byte(deleteEntry.OldValues), &oldValues)
	require.NoError(t, err)
	assert.Equal(t, "acc-delete-test", oldValues["DebtorAccountID"])
}

func TestAudit_CriticalFields_AreAlwaysCaptured(t *testing.T) {
	// Verifies that critical fields mentioned in task requirements are always captured:
	// Status, AmountCents, DebtorAccountID, LienID, GatewayReferenceID
	db, ctx, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10050) // 100.50 GBP
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"debtor-account-001",
		"creditor-ref",
		amount,
		"idem-key-critical-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	// Progress to capture all critical fields
	require.NoError(t, po.Reserve("critical-lien-123"))
	require.NoError(t, repo.Update(ctx, po))

	require.NoError(t, po.Execute("critical-gateway-ref-456"))
	require.NoError(t, repo.Update(ctx, po))

	// Get final entry
	entries := getAuditEntries(t, db, po.ID)
	finalEntry := entries[len(entries)-1]
	require.NotEmpty(t, finalEntry.NewValues)

	var newValues map[string]interface{}
	err = json.Unmarshal([]byte(finalEntry.NewValues), &newValues)
	require.NoError(t, err)

	// Verify all critical fields are captured
	assert.Equal(t, "EXECUTING", newValues["Status"], "Status must be captured")
	assert.Equal(t, float64(10050), newValues["AmountCents"], "AmountCents must be captured")
	assert.Equal(t, "debtor-account-001", newValues["DebtorAccountID"], "DebtorAccountID must be captured")
	assert.Equal(t, "critical-lien-123", newValues["LienID"], "LienID must be captured")
	assert.Equal(t, "critical-gateway-ref-456", newValues["GatewayReferenceID"], "GatewayReferenceID must be captured")
}

func TestAudit_OutboxStatus_DefaultsToPending(t *testing.T) {
	db, ctx, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	repo := NewPaymentOrderRepository(db)
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := domain.NewPaymentOrder(
		"acc-123",
		"creditor-ref",
		amount,
		"idem-key-status-001",
		"corr-001",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, po))

	entries := getAuditEntries(t, db, po.ID)
	require.Len(t, entries, 1)

	// All entries should start as 'pending' for worker processing
	assert.Equal(t, "pending", entries[0].Status, "Audit entries should default to pending status")
	assert.Equal(t, 0, entries[0].RetryCount, "RetryCount should start at 0")
}

// =============================================================================
// Helper Functions
// =============================================================================

// assertStatusTransition verifies an audit entry captures a status transition.
func assertStatusTransition(t *testing.T, entry audit.AuditOutbox, oldStatus, newStatus string) {
	t.Helper()

	var oldValues, newValues map[string]interface{}

	if entry.OldValues != "" {
		err := json.Unmarshal([]byte(entry.OldValues), &oldValues)
		require.NoError(t, err)
		assert.Equal(t, oldStatus, oldValues["Status"], "Old status should be %s", oldStatus)
	}

	require.NotEmpty(t, entry.NewValues)
	err := json.Unmarshal([]byte(entry.NewValues), &newValues)
	require.NoError(t, err)
	assert.Equal(t, newStatus, newValues["Status"], "New status should be %s", newStatus)
}
