package models

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	postgresdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// setupPositionKeepingTestDB creates a PostgreSQL container with GORM for testing
// the FinancialPositionLog and TransactionLogEntry audit hooks.
func setupPositionKeepingTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("test_db"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err, "Failed to start postgres container")

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Connect with GORM
	db, err := gorm.Open(postgresdriver.Open(connStr), &gorm.Config{})
	require.NoError(t, err, "Failed to connect to test database")

	// Enable pgcrypto extension for gen_random_uuid() function
	err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto").Error
	require.NoError(t, err, "Failed to enable pgcrypto extension")

	// Create audit_outbox table (unqualified, uses public schema)
	// Note: Uses TEXT for old_values/new_values to match AuditOutbox model (gorm type:text)
	// The production migration uses JSONB but the model uses string type, which works
	// because PostgreSQL can cast valid JSON strings to JSONB
	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id UUID NOT NULL,
			old_values TEXT,
			new_values TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT
		)
	`).Error
	require.NoError(t, err, "Failed to create audit_outbox table")

	// Create indexes
	err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_audit_outbox_status_created
		ON audit_outbox(status, created_at)
	`).Error
	require.NoError(t, err, "Failed to create audit_outbox indexes")

	// Migrate all required tables (Customer -> Account -> FinancialPositionLog -> TransactionLogEntry)
	err = db.AutoMigrate(&Customer{}, &Account{}, &FinancialPositionLog{}, &TransactionLogEntry{})
	require.NoError(t, err, "Failed to migrate tables")

	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = pgContainer.Terminate(ctx)
	}

	return db, cleanup
}

// createTestCustomer creates a test Customer record
func createTestCustomer(db *gorm.DB, t *testing.T) *Customer {
	t.Helper()
	customer := &Customer{
		CustomerNumber: "C" + uuid.New().String()[:8],
		FirstName:      "Test",
		LastName:       "User",
		Email:          "test_" + uuid.New().String()[:8] + "@example.com",
		Status:         "active",
	}
	err := db.Create(customer).Error
	require.NoError(t, err, "Failed to create test customer")
	return customer
}

// createTestAccount creates a test Account record linked to a Customer
func createTestAccount(db *gorm.DB, t *testing.T, customer *Customer, accountNumber string) *Account {
	t.Helper()
	account := &Account{
		AccountNumber: accountNumber,
		AccountType:   "current",
		Currency:      "GBP",
		Status:        "active",
		CustomerID:    customer.ID,
	}
	err := db.Create(account).Error
	require.NoError(t, err, "Failed to create test account")
	return account
}

// createTestFinancialPositionLog creates a test FinancialPositionLog with required fields
func createTestFinancialPositionLog(accountID string) *FinancialPositionLog {
	now := time.Now()
	return &FinancialPositionLog{
		LogID:                uuid.New(),
		AccountID:            accountID,
		Version:              1,
		CurrentStatus:        "PENDING",
		StatusUpdatedAt:      now,
		StatusReason:         "Initial creation",
		ReconciliationStatus: "UNRECONCILED",
	}
}

// createTestTransactionLogEntry creates a test TransactionLogEntry with required fields
func createTestTransactionLogEntry(logID uuid.UUID, accountID string) *TransactionLogEntry {
	now := time.Now()
	return &TransactionLogEntry{
		EntryID:                uuid.New(),
		FinancialPositionLogID: logID,
		TransactionID:          uuid.New(),
		AccountID:              accountID,
		AmountCents:            10000, // 100.00
		Currency:               "GBP",
		Direction:              "CREDIT",
		Timestamp:              now,
		Source:                 "MANUAL",
	}
}

// TestFinancialPositionLog_AuditOutbox_AtomicCommit verifies that audit outbox entry
// is created atomically with the FinancialPositionLog insert.
func TestFinancialPositionLog_AuditOutbox_AtomicCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Create prerequisite Customer and Account
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)

	// Create FinancialPositionLog
	log := createTestFinancialPositionLog(accountNumber)

	err := db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")
	assert.NotEqual(t, uuid.Nil, log.ID, "FinancialPositionLog ID should be set")

	// Verify outbox entry exists
	var outbox AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ?", log.ID).
		First(&outbox).Error
	require.NoError(t, err, "Audit outbox entry should exist")

	// Verify outbox content
	assert.Equal(t, "financial_position_log", outbox.Table, "Table name should be 'financial_position_log'")
	assert.Equal(t, "INSERT", outbox.Operation, "Operation should be 'INSERT'")
	assert.Equal(t, "pending", outbox.Status, "Status should be 'pending'")
	assert.NotEmpty(t, outbox.NewValues, "NewValues should contain log data")
	assert.Empty(t, outbox.OldValues, "OldValues should be empty for INSERT")
}

// TestFinancialPositionLog_AuditOutbox_CapturesUpdate verifies that UPDATE operations
// capture both old and new values.
func TestFinancialPositionLog_AuditOutbox_CapturesUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Create prerequisite Customer and Account
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)

	// Create FinancialPositionLog
	log := createTestFinancialPositionLog(accountNumber)
	err := db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Update the log
	log.CurrentStatus = "RECONCILED"
	log.StatusReason = "Reconciliation complete"
	log.ReconciliationStatus = "MATCHED"
	err = db.Save(log).Error
	require.NoError(t, err, "Failed to update FinancialPositionLog")

	// Verify UPDATE audit
	var updateAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", log.ID, "UPDATE").
		First(&updateAudit).Error
	require.NoError(t, err, "UPDATE audit should exist")
	assert.Equal(t, "UPDATE", updateAudit.Operation)
	assert.NotEmpty(t, updateAudit.OldValues, "UPDATE should capture old values")
	assert.NotEmpty(t, updateAudit.NewValues, "UPDATE should capture new values")
	assert.Contains(t, updateAudit.OldValues, "PENDING", "Old values should contain original status")
	assert.Contains(t, updateAudit.NewValues, "RECONCILED", "New values should contain updated status")
}

// TestFinancialPositionLog_AuditOutbox_CapturesDelete verifies that DELETE operations
// capture the old values.
func TestFinancialPositionLog_AuditOutbox_CapturesDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Create prerequisite Customer and Account
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)

	// Create FinancialPositionLog
	log := createTestFinancialPositionLog(accountNumber)
	err := db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Delete the log
	err = db.Delete(log).Error
	require.NoError(t, err, "Failed to delete FinancialPositionLog")

	// Verify DELETE audit
	var deleteAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", log.ID, "DELETE").
		First(&deleteAudit).Error
	require.NoError(t, err, "DELETE audit should exist")
	assert.Equal(t, "DELETE", deleteAudit.Operation)
	assert.NotEmpty(t, deleteAudit.OldValues, "DELETE should capture old values")
	assert.Empty(t, deleteAudit.NewValues, "DELETE should have empty new values")
}

// TestFinancialPositionLog_AuditOutbox_RollbackOnFailure verifies that audit outbox
// entries are rolled back when the business transaction fails.
func TestFinancialPositionLog_AuditOutbox_RollbackOnFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Create prerequisite Customer and Account
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)

	// Get initial count (includes customer + account audit entries)
	var initialOutboxCount int64
	db.Table("audit_outbox").Count(&initialOutboxCount)

	err := db.Transaction(func(tx *gorm.DB) error {
		log := createTestFinancialPositionLog(accountNumber)

		err := tx.Create(log).Error
		require.NoError(t, err, "Log creation should succeed within transaction")

		// Verify outbox entry exists within transaction
		var count int64
		tx.Table("audit_outbox").
			Where("record_id = ?", log.ID).
			Count(&count)
		assert.Equal(t, int64(1), count, "Outbox entry should exist within transaction")

		// Force rollback
		return errForcedTransactionFailure
	})
	require.Error(t, err, "Transaction should fail")

	// Verify outbox entry was rolled back (should still have customer + account audit entries)
	var count int64
	db.Table("audit_outbox").Count(&count)
	assert.Equal(t, initialOutboxCount, count, "Outbox should only contain pre-transaction entries after rollback")

	// Verify log was also rolled back
	var logCount int64
	db.Model(&FinancialPositionLog{}).Count(&logCount)
	assert.Equal(t, int64(0), logCount, "FinancialPositionLog should not exist after rollback")
}

// TestTransactionLogEntry_AuditOutbox_AtomicCommit verifies that audit outbox entry
// is created atomically with the TransactionLogEntry insert.
func TestTransactionLogEntry_AuditOutbox_AtomicCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Create prerequisite Customer and Account
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)

	// Create parent FinancialPositionLog first
	log := createTestFinancialPositionLog(accountNumber)
	err := db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Create TransactionLogEntry
	entry := createTestTransactionLogEntry(log.ID, "GB82WEST12345698765432")
	err = db.Create(entry).Error
	require.NoError(t, err, "Failed to create TransactionLogEntry")
	assert.NotEqual(t, uuid.Nil, entry.ID, "TransactionLogEntry ID should be set")

	// Verify outbox entry exists for the entry
	var outbox AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND table_name = ?", entry.ID, "transaction_log_entry").
		First(&outbox).Error
	require.NoError(t, err, "Audit outbox entry should exist")

	// Verify outbox content
	assert.Equal(t, "transaction_log_entry", outbox.Table, "Table name should be 'transaction_log_entry'")
	assert.Equal(t, "INSERT", outbox.Operation, "Operation should be 'INSERT'")
	assert.Equal(t, "pending", outbox.Status, "Status should be 'pending'")
	assert.NotEmpty(t, outbox.NewValues, "NewValues should contain entry data")
	assert.Empty(t, outbox.OldValues, "OldValues should be empty for INSERT")
}

// TestTransactionLogEntry_AuditOutbox_CapturesUpdate verifies that UPDATE operations
// capture both old and new values.
func TestTransactionLogEntry_AuditOutbox_CapturesUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Create prerequisite Customer and Account
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)

	// Create parent FinancialPositionLog first
	log := createTestFinancialPositionLog(accountNumber)
	err := db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Create TransactionLogEntry
	entry := createTestTransactionLogEntry(log.ID, "GB82WEST12345698765432")
	err = db.Create(entry).Error
	require.NoError(t, err, "Failed to create TransactionLogEntry")

	// Update the entry
	entry.AmountCents = 20000 // 200.00
	entry.Direction = "DEBIT"
	err = db.Save(entry).Error
	require.NoError(t, err, "Failed to update TransactionLogEntry")

	// Verify UPDATE audit
	var updateAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ? AND table_name = ?", entry.ID, "UPDATE", "transaction_log_entry").
		First(&updateAudit).Error
	require.NoError(t, err, "UPDATE audit should exist")
	assert.Equal(t, "UPDATE", updateAudit.Operation)
	assert.NotEmpty(t, updateAudit.OldValues, "UPDATE should capture old values")
	assert.NotEmpty(t, updateAudit.NewValues, "UPDATE should capture new values")
	assert.Contains(t, updateAudit.OldValues, "CREDIT", "Old values should contain original direction")
	assert.Contains(t, updateAudit.NewValues, "DEBIT", "New values should contain updated direction")
}

// TestTransactionLogEntry_AuditOutbox_CapturesDelete verifies that DELETE operations
// capture the old values.
func TestTransactionLogEntry_AuditOutbox_CapturesDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Create prerequisite Customer and Account
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)

	// Create parent FinancialPositionLog first
	log := createTestFinancialPositionLog(accountNumber)
	err := db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Create TransactionLogEntry
	entry := createTestTransactionLogEntry(log.ID, "GB82WEST12345698765432")
	err = db.Create(entry).Error
	require.NoError(t, err, "Failed to create TransactionLogEntry")

	// Delete the entry
	err = db.Delete(entry).Error
	require.NoError(t, err, "Failed to delete TransactionLogEntry")

	// Verify DELETE audit
	var deleteAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ? AND table_name = ?", entry.ID, "DELETE", "transaction_log_entry").
		First(&deleteAudit).Error
	require.NoError(t, err, "DELETE audit should exist")
	assert.Equal(t, "DELETE", deleteAudit.Operation)
	assert.NotEmpty(t, deleteAudit.OldValues, "DELETE should capture old values")
	assert.Empty(t, deleteAudit.NewValues, "DELETE should have empty new values")
}

// TestTransactionLogEntry_AuditOutbox_RollbackOnFailure verifies that audit outbox
// entries are rolled back when the business transaction fails.
func TestTransactionLogEntry_AuditOutbox_RollbackOnFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Create prerequisite Customer and Account
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)

	// Create parent FinancialPositionLog first (outside transaction to ensure FK exists)
	log := createTestFinancialPositionLog(accountNumber)
	err := db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Get initial count of outbox entries (includes the INSERT from log creation)
	var initialCount int64
	db.Table("audit_outbox").Count(&initialCount)

	err = db.Transaction(func(tx *gorm.DB) error {
		entry := createTestTransactionLogEntry(log.ID, "GB82WEST12345698765432")

		err := tx.Create(entry).Error
		require.NoError(t, err, "Entry creation should succeed within transaction")

		// Verify outbox entry exists within transaction
		var count int64
		tx.Table("audit_outbox").
			Where("record_id = ? AND table_name = ?", entry.ID, "transaction_log_entry").
			Count(&count)
		assert.Equal(t, int64(1), count, "Outbox entry should exist within transaction")

		// Force rollback
		return errForcedTransactionFailure
	})
	require.Error(t, err, "Transaction should fail")

	// Verify outbox entry was rolled back (should only have the log's INSERT audit)
	var finalCount int64
	db.Table("audit_outbox").Count(&finalCount)
	assert.Equal(t, initialCount, finalCount, "No new outbox entries after rollback")

	// Verify entry was also rolled back
	var entryCount int64
	db.Model(&TransactionLogEntry{}).Count(&entryCount)
	assert.Equal(t, int64(0), entryCount, "TransactionLogEntry should not exist after rollback")
}

// TestFinancialPositionLog_AuditOutbox_AllOperations verifies that all CRUD operations
// are captured correctly for a FinancialPositionLog lifecycle.
func TestFinancialPositionLog_AuditOutbox_AllOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Create prerequisite Customer and Account
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)

	// INSERT
	log := createTestFinancialPositionLog(accountNumber)
	err := db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// UPDATE
	log.CurrentStatus = "RECONCILED"
	err = db.Save(log).Error
	require.NoError(t, err, "Failed to update FinancialPositionLog")

	// DELETE
	err = db.Delete(log).Error
	require.NoError(t, err, "Failed to delete FinancialPositionLog")

	// Verify total audit count for this record
	var totalCount int64
	db.Table("audit_outbox").
		Where("record_id = ?", log.ID).
		Count(&totalCount)
	assert.Equal(t, int64(3), totalCount, "Should have exactly 3 audit records (INSERT, UPDATE, DELETE)")

	// Verify each operation exists
	var operations []string
	db.Table("audit_outbox").
		Where("record_id = ?", log.ID).
		Order("created_at ASC").
		Pluck("operation", &operations)
	assert.Equal(t, []string{"INSERT", "UPDATE", "DELETE"}, operations, "Operations should be in correct order")
}

// createTestTransactionLineage creates a test TransactionLineage with required fields
func createTestTransactionLineage(logID uuid.UUID) *TransactionLineage {
	return &TransactionLineage{
		FinancialPositionLogID: logID,
		TransactionID:          uuid.New(),
		TransactionType:        "PAYMENT",
		ChildTransactionIDs:    []byte("[]"),
		RelatedTransactionIDs:  []byte("[]"),
	}
}

// createTestAuditTrailEntry creates a test AuditTrailEntry with required fields
func createTestAuditTrailEntry(logID uuid.UUID) *AuditTrailEntry {
	return &AuditTrailEntry{
		AuditEntryID:           uuid.New(),
		FinancialPositionLogID: logID,
		Timestamp:              time.Now(),
		UserID:                 "test-user",
		Action:                 "CREATED",
	}
}

// TestTransactionLineage_AuditOutbox_AtomicCommit verifies that audit outbox entry
// is created atomically with the TransactionLineage insert.
func TestTransactionLineage_AuditOutbox_AtomicCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Migrate TransactionLineage
	err := db.AutoMigrate(&TransactionLineage{})
	require.NoError(t, err, "Failed to migrate TransactionLineage")

	// Create prerequisite Customer, Account, and FinancialPositionLog
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)
	log := createTestFinancialPositionLog(accountNumber)
	err = db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Create TransactionLineage
	lineage := createTestTransactionLineage(log.ID)
	err = db.Create(lineage).Error
	require.NoError(t, err, "Failed to create TransactionLineage")
	assert.NotEqual(t, uuid.Nil, lineage.ID, "TransactionLineage ID should be set")

	// Verify outbox entry exists
	var outbox AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND table_name = ?", lineage.ID, "transaction_lineage").
		First(&outbox).Error
	require.NoError(t, err, "Audit outbox entry should exist")

	// Verify outbox content
	assert.Equal(t, "transaction_lineage", outbox.Table, "Table name should be 'transaction_lineage'")
	assert.Equal(t, "INSERT", outbox.Operation, "Operation should be 'INSERT'")
	assert.Equal(t, "pending", outbox.Status, "Status should be 'pending'")
	assert.NotEmpty(t, outbox.NewValues, "NewValues should contain lineage data")
	assert.Empty(t, outbox.OldValues, "OldValues should be empty for INSERT")
}

// TestTransactionLineage_AuditOutbox_CapturesUpdate verifies that UPDATE operations
// capture both old and new values.
func TestTransactionLineage_AuditOutbox_CapturesUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Migrate TransactionLineage
	err := db.AutoMigrate(&TransactionLineage{})
	require.NoError(t, err, "Failed to migrate TransactionLineage")

	// Create prerequisite Customer, Account, and FinancialPositionLog
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)
	log := createTestFinancialPositionLog(accountNumber)
	err = db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Create TransactionLineage
	lineage := createTestTransactionLineage(log.ID)
	err = db.Create(lineage).Error
	require.NoError(t, err, "Failed to create TransactionLineage")

	// Update the lineage
	lineage.TransactionType = "REFUND"
	err = db.Save(lineage).Error
	require.NoError(t, err, "Failed to update TransactionLineage")

	// Verify UPDATE audit
	var updateAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ? AND table_name = ?", lineage.ID, "UPDATE", "transaction_lineage").
		First(&updateAudit).Error
	require.NoError(t, err, "UPDATE audit should exist")
	assert.Equal(t, "UPDATE", updateAudit.Operation)
	assert.NotEmpty(t, updateAudit.OldValues, "UPDATE should capture old values")
	assert.NotEmpty(t, updateAudit.NewValues, "UPDATE should capture new values")
	assert.Contains(t, updateAudit.OldValues, "PAYMENT", "Old values should contain original type")
	assert.Contains(t, updateAudit.NewValues, "REFUND", "New values should contain updated type")
}

// TestTransactionLineage_AuditOutbox_CapturesDelete verifies that DELETE operations
// capture the old values.
func TestTransactionLineage_AuditOutbox_CapturesDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Migrate TransactionLineage
	err := db.AutoMigrate(&TransactionLineage{})
	require.NoError(t, err, "Failed to migrate TransactionLineage")

	// Create prerequisite Customer, Account, and FinancialPositionLog
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)
	log := createTestFinancialPositionLog(accountNumber)
	err = db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Create TransactionLineage
	lineage := createTestTransactionLineage(log.ID)
	err = db.Create(lineage).Error
	require.NoError(t, err, "Failed to create TransactionLineage")

	// Delete the lineage
	err = db.Delete(lineage).Error
	require.NoError(t, err, "Failed to delete TransactionLineage")

	// Verify DELETE audit
	var deleteAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ? AND table_name = ?", lineage.ID, "DELETE", "transaction_lineage").
		First(&deleteAudit).Error
	require.NoError(t, err, "DELETE audit should exist")
	assert.Equal(t, "DELETE", deleteAudit.Operation)
	assert.NotEmpty(t, deleteAudit.OldValues, "DELETE should capture old values")
	assert.Empty(t, deleteAudit.NewValues, "DELETE should have empty new values")
}

// TestAuditTrailEntry_AuditOutbox_AtomicCommit verifies that audit outbox entry
// is created atomically with the AuditTrailEntry insert.
func TestAuditTrailEntry_AuditOutbox_AtomicCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Migrate AuditTrailEntry
	err := db.AutoMigrate(&AuditTrailEntry{})
	require.NoError(t, err, "Failed to migrate AuditTrailEntry")

	// Create prerequisite Customer, Account, and FinancialPositionLog
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)
	log := createTestFinancialPositionLog(accountNumber)
	err = db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Create AuditTrailEntry
	entry := createTestAuditTrailEntry(log.ID)
	err = db.Create(entry).Error
	require.NoError(t, err, "Failed to create AuditTrailEntry")
	assert.NotEqual(t, uuid.Nil, entry.ID, "AuditTrailEntry ID should be set")

	// Verify outbox entry exists
	var outbox AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND table_name = ?", entry.ID, "audit_trail_entry").
		First(&outbox).Error
	require.NoError(t, err, "Audit outbox entry should exist")

	// Verify outbox content
	assert.Equal(t, "audit_trail_entry", outbox.Table, "Table name should be 'audit_trail_entry'")
	assert.Equal(t, "INSERT", outbox.Operation, "Operation should be 'INSERT'")
	assert.Equal(t, "pending", outbox.Status, "Status should be 'pending'")
	assert.NotEmpty(t, outbox.NewValues, "NewValues should contain entry data")
	assert.Empty(t, outbox.OldValues, "OldValues should be empty for INSERT")
}

// TestAuditTrailEntry_AuditOutbox_CapturesUpdate verifies that UPDATE operations
// capture both old and new values.
func TestAuditTrailEntry_AuditOutbox_CapturesUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Migrate AuditTrailEntry
	err := db.AutoMigrate(&AuditTrailEntry{})
	require.NoError(t, err, "Failed to migrate AuditTrailEntry")

	// Create prerequisite Customer, Account, and FinancialPositionLog
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)
	log := createTestFinancialPositionLog(accountNumber)
	err = db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Create AuditTrailEntry
	entry := createTestAuditTrailEntry(log.ID)
	err = db.Create(entry).Error
	require.NoError(t, err, "Failed to create AuditTrailEntry")

	// Update the entry
	entry.Action = "UPDATED"
	details := "Modified status to reconciled"
	entry.Details = &details
	err = db.Save(entry).Error
	require.NoError(t, err, "Failed to update AuditTrailEntry")

	// Verify UPDATE audit
	var updateAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ? AND table_name = ?", entry.ID, "UPDATE", "audit_trail_entry").
		First(&updateAudit).Error
	require.NoError(t, err, "UPDATE audit should exist")
	assert.Equal(t, "UPDATE", updateAudit.Operation)
	assert.NotEmpty(t, updateAudit.OldValues, "UPDATE should capture old values")
	assert.NotEmpty(t, updateAudit.NewValues, "UPDATE should capture new values")
	assert.Contains(t, updateAudit.OldValues, "CREATED", "Old values should contain original action")
	assert.Contains(t, updateAudit.NewValues, "UPDATED", "New values should contain updated action")
}

// TestAuditTrailEntry_AuditOutbox_CapturesDelete verifies that DELETE operations
// capture the old values.
func TestAuditTrailEntry_AuditOutbox_CapturesDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Migrate AuditTrailEntry
	err := db.AutoMigrate(&AuditTrailEntry{})
	require.NoError(t, err, "Failed to migrate AuditTrailEntry")

	// Create prerequisite Customer, Account, and FinancialPositionLog
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)
	log := createTestFinancialPositionLog(accountNumber)
	err = db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// Create AuditTrailEntry
	entry := createTestAuditTrailEntry(log.ID)
	err = db.Create(entry).Error
	require.NoError(t, err, "Failed to create AuditTrailEntry")

	// Delete the entry
	err = db.Delete(entry).Error
	require.NoError(t, err, "Failed to delete AuditTrailEntry")

	// Verify DELETE audit
	var deleteAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ? AND table_name = ?", entry.ID, "DELETE", "audit_trail_entry").
		First(&deleteAudit).Error
	require.NoError(t, err, "DELETE audit should exist")
	assert.Equal(t, "DELETE", deleteAudit.Operation)
	assert.NotEmpty(t, deleteAudit.OldValues, "DELETE should capture old values")
	assert.Empty(t, deleteAudit.NewValues, "DELETE should have empty new values")
}

// TestTransactionLineage_AuditOutbox_AllOperations verifies that all CRUD operations
// are captured correctly for a TransactionLineage lifecycle.
func TestTransactionLineage_AuditOutbox_AllOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Migrate TransactionLineage
	err := db.AutoMigrate(&TransactionLineage{})
	require.NoError(t, err, "Failed to migrate TransactionLineage")

	// Create prerequisite Customer, Account, and FinancialPositionLog
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)
	log := createTestFinancialPositionLog(accountNumber)
	err = db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// INSERT
	lineage := createTestTransactionLineage(log.ID)
	err = db.Create(lineage).Error
	require.NoError(t, err, "Failed to create TransactionLineage")

	// UPDATE
	lineage.TransactionType = "REFUND"
	err = db.Save(lineage).Error
	require.NoError(t, err, "Failed to update TransactionLineage")

	// DELETE
	err = db.Delete(lineage).Error
	require.NoError(t, err, "Failed to delete TransactionLineage")

	// Verify total audit count for this record
	var totalCount int64
	db.Table("audit_outbox").
		Where("record_id = ? AND table_name = ?", lineage.ID, "transaction_lineage").
		Count(&totalCount)
	assert.Equal(t, int64(3), totalCount, "Should have exactly 3 audit records (INSERT, UPDATE, DELETE)")

	// Verify each operation exists
	var operations []string
	db.Table("audit_outbox").
		Where("record_id = ? AND table_name = ?", lineage.ID, "transaction_lineage").
		Order("created_at ASC").
		Pluck("operation", &operations)
	assert.Equal(t, []string{"INSERT", "UPDATE", "DELETE"}, operations, "Operations should be in correct order")
}

// TestAuditTrailEntry_AuditOutbox_AllOperations verifies that all CRUD operations
// are captured correctly for an AuditTrailEntry lifecycle.
func TestAuditTrailEntry_AuditOutbox_AllOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupPositionKeepingTestDB(t)
	defer cleanup()

	// Migrate AuditTrailEntry
	err := db.AutoMigrate(&AuditTrailEntry{})
	require.NoError(t, err, "Failed to migrate AuditTrailEntry")

	// Create prerequisite Customer, Account, and FinancialPositionLog
	accountNumber := "GB82WEST12345698765432"
	customer := createTestCustomer(db, t)
	createTestAccount(db, t, customer, accountNumber)
	log := createTestFinancialPositionLog(accountNumber)
	err = db.Create(log).Error
	require.NoError(t, err, "Failed to create FinancialPositionLog")

	// INSERT
	entry := createTestAuditTrailEntry(log.ID)
	err = db.Create(entry).Error
	require.NoError(t, err, "Failed to create AuditTrailEntry")

	// UPDATE
	entry.Action = "UPDATED"
	err = db.Save(entry).Error
	require.NoError(t, err, "Failed to update AuditTrailEntry")

	// DELETE
	err = db.Delete(entry).Error
	require.NoError(t, err, "Failed to delete AuditTrailEntry")

	// Verify total audit count for this record
	var totalCount int64
	db.Table("audit_outbox").
		Where("record_id = ? AND table_name = ?", entry.ID, "audit_trail_entry").
		Count(&totalCount)
	assert.Equal(t, int64(3), totalCount, "Should have exactly 3 audit records (INSERT, UPDATE, DELETE)")

	// Verify each operation exists
	var operations []string
	db.Table("audit_outbox").
		Where("record_id = ? AND table_name = ?", entry.ID, "audit_trail_entry").
		Order("created_at ASC").
		Pluck("operation", &operations)
	assert.Equal(t, []string{"INSERT", "UPDATE", "DELETE"}, operations, "Operations should be in correct order")
}
