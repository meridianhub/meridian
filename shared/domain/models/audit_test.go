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

var errForcedTransactionFailure = gorm.ErrInvalidTransaction

// setupTestDB creates a PostgreSQL container with GORM for testing
// PostgreSQL is used instead of SQLite to match production CockroachDB behavior
func setupTestDB(t *testing.T) (*gorm.DB, func()) {
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
	// TableName method now returns unqualified "audit_outbox" for search_path routing
	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id UUID NOT NULL,
			old_values TEXT,
			new_values TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'failed')),
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

	// Migrate Customer and Account tables
	err = db.AutoMigrate(&Customer{}, &Account{})
	require.NoError(t, err, "Failed to migrate Customer and Account tables")

	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = pgContainer.Terminate(ctx)
	}

	return db, cleanup
}

// TestAuditOutbox_AtomicCommit verifies that audit outbox entry is created atomically
// with the business operation within the same transaction.
//
// Critical Guarantee: Atomicity - Audit intent committed with business operation
func TestAuditOutbox_AtomicCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create customer (should create outbox entry in same transaction)
	customer := &Customer{
		CustomerNumber: "C001",
		FirstName:      "John",
		LastName:       "Doe",
		Email:          "john.doe@example.com",
		Status:         "active",
	}

	err := db.Create(customer).Error
	require.NoError(t, err, "Failed to create customer")
	assert.NotEqual(t, uuid.Nil, customer.ID, "Customer ID should be set")

	// Verify outbox entry exists
	var outbox AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ?", customer.ID).
		First(&outbox).Error
	require.NoError(t, err, "Audit outbox entry should exist")

	// Verify outbox content
	assert.Equal(t, "customer", outbox.Table, "Table name should be 'customer'")
	assert.Equal(t, "INSERT", outbox.Operation, "Operation should be 'INSERT'")
	assert.Equal(t, "pending", outbox.Status, "Status should be 'pending'")
	assert.NotEmpty(t, outbox.NewValues, "NewValues should contain customer data")
	assert.Empty(t, outbox.OldValues, "OldValues should be empty for INSERT")
}

// TestAuditOutbox_RollbackOnBusinessFailure verifies that audit outbox entry is
// rolled back when the business transaction fails.
//
// Critical Guarantee: Atomicity - Outbox entry rolled back with failed business operation
func TestAuditOutbox_RollbackOnBusinessFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Force transaction failure
	err := db.Transaction(func(tx *gorm.DB) error {
		customer := &Customer{
			CustomerNumber: "C002",
			FirstName:      "Jane",
			LastName:       "Smith",
			Email:          "jane.smith@example.com",
			Status:         "active",
		}

		err := tx.Create(customer).Error
		require.NoError(t, err, "Customer creation should succeed within transaction")

		// Verify outbox entry exists within transaction
		var count int64
		tx.Table("audit_outbox").
			Where("record_id = ?", customer.ID).
			Count(&count)
		assert.Equal(t, int64(1), count, "Outbox entry should exist within transaction")

		// Force rollback
		return errForcedTransactionFailure
	})
	require.Error(t, err, "Transaction should fail")
	assert.ErrorIs(t, err, errForcedTransactionFailure, "Should be a forced transaction failure")

	// Verify outbox entry was rolled back
	var count int64
	db.Table("audit_outbox").Count(&count)
	assert.Equal(t, int64(0), count, "Outbox should be empty after rollback")

	// Verify customer was also rolled back
	var customerCount int64
	db.Model(&Customer{}).Count(&customerCount)
	assert.Equal(t, int64(0), customerCount, "Customer should not exist after rollback")
}

// TestAuditOutbox_CapturesInsertUpdateDelete verifies that all operations
// (INSERT, UPDATE, DELETE) are captured in the audit outbox.
//
// Critical Guarantee: Complete audit trail - All INSERT/UPDATE/DELETE captured
func TestAuditOutbox_CapturesInsertUpdateDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// INSERT
	customer := &Customer{
		CustomerNumber: "C003",
		FirstName:      "Alice",
		LastName:       "Johnson",
		Email:          "alice.johnson@example.com",
		Status:         "active",
	}
	err := db.Create(customer).Error
	require.NoError(t, err, "Failed to create customer")

	// Verify INSERT audit
	var insertAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", customer.ID, "INSERT").
		First(&insertAudit).Error
	require.NoError(t, err, "INSERT audit should exist")
	assert.Equal(t, "INSERT", insertAudit.Operation)
	assert.NotEmpty(t, insertAudit.NewValues)
	assert.Empty(t, insertAudit.OldValues)

	// UPDATE
	customer.FirstName = "Alicia"
	customer.Status = "suspended"
	err = db.Save(customer).Error
	require.NoError(t, err, "Failed to update customer")

	// Verify UPDATE audit
	var updateAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", customer.ID, "UPDATE").
		First(&updateAudit).Error
	require.NoError(t, err, "UPDATE audit should exist")
	assert.Equal(t, "UPDATE", updateAudit.Operation)
	assert.NotEmpty(t, updateAudit.OldValues, "UPDATE should capture old values")
	assert.NotEmpty(t, updateAudit.NewValues, "UPDATE should capture new values")

	// DELETE
	err = db.Delete(customer).Error
	require.NoError(t, err, "Failed to delete customer")

	// Verify DELETE audit
	var deleteAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", customer.ID, "DELETE").
		First(&deleteAudit).Error
	require.NoError(t, err, "DELETE audit should exist")
	assert.Equal(t, "DELETE", deleteAudit.Operation)
	assert.NotEmpty(t, deleteAudit.OldValues, "DELETE should capture old values")
	assert.Empty(t, deleteAudit.NewValues, "DELETE should have empty new values")

	// Verify total audit count
	var totalCount int64
	db.Table("audit_outbox").
		Where("record_id = ?", customer.ID).
		Count(&totalCount)
	assert.Equal(t, int64(3), totalCount, "Should have exactly 3 audit records (INSERT, UPDATE, DELETE)")
}

// TestAuditOutbox_CapturesChangedBy verifies that the audit records capture
// the user who made the change from the context.
func TestAuditOutbox_CapturesChangedBy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create context with user ID (this will be properly implemented with JWT context)
	// For now, we test that it defaults to SystemUser
	customer := &Customer{
		CustomerNumber: "C004",
		FirstName:      "Bob",
		LastName:       "Brown",
		Email:          "bob.brown@example.com",
		Status:         "active",
	}

	err := db.Create(customer).Error
	require.NoError(t, err, "Failed to create customer")

	// Verify audit captures changed_by
	var outbox AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ?", customer.ID).
		First(&outbox).Error
	require.NoError(t, err, "Audit outbox entry should exist")

	// Should default to SystemUser when no JWT context present
	require.NotNil(t, outbox.ChangedBy, "ChangedBy should not be nil")
	assert.Equal(t, SystemUser, *outbox.ChangedBy, "ChangedBy should default to SystemUser")
}

// TestAccountAudit_Insert verifies that Account INSERT creates outbox entry
// with correct new_values.
func TestAccountAudit_Insert(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a customer first (required for foreign key)
	customer := &Customer{
		CustomerNumber: "C100",
		FirstName:      "Test",
		LastName:       "User",
		Email:          "test.user@example.com",
		Status:         "active",
	}
	err := db.Create(customer).Error
	require.NoError(t, err, "Failed to create customer")

	// Create account
	account := &Account{
		AccountNumber: "GB82WEST12345698765432",
		AccountType:   "current",
		Currency:      "GBP",
		Status:        "active",
		CustomerID:    customer.ID,
		Balance:       10000, // £100.00
	}

	err = db.Create(account).Error
	require.NoError(t, err, "Failed to create account")
	assert.NotEqual(t, uuid.Nil, account.ID, "Account ID should be set")

	// Verify outbox entry exists
	var outbox AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND table_name = ?", account.ID, "account").
		First(&outbox).Error
	require.NoError(t, err, "Audit outbox entry should exist for account")

	// Verify outbox content
	assert.Equal(t, "account", outbox.Table, "Table name should be 'account'")
	assert.Equal(t, "INSERT", outbox.Operation, "Operation should be 'INSERT'")
	assert.Equal(t, "pending", outbox.Status, "Status should be 'pending'")
	assert.NotEmpty(t, outbox.NewValues, "NewValues should contain account data")
	assert.Empty(t, outbox.OldValues, "OldValues should be empty for INSERT")
}

// TestAccountAudit_Update verifies that Account UPDATE creates outbox entry
// with old_values and new_values showing changed fields.
func TestAccountAudit_Update(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a customer first
	customer := &Customer{
		CustomerNumber: "C101",
		FirstName:      "Update",
		LastName:       "Test",
		Email:          "update.test@example.com",
		Status:         "active",
	}
	err := db.Create(customer).Error
	require.NoError(t, err, "Failed to create customer")

	// Create account
	account := &Account{
		AccountNumber: "GB82WEST12345698765433",
		AccountType:   "current",
		Currency:      "GBP",
		Status:        "active",
		CustomerID:    customer.ID,
		Balance:       10000,
	}
	err = db.Create(account).Error
	require.NoError(t, err, "Failed to create account")

	// Update the account
	account.Balance = 20000 // £200.00
	account.Status = "suspended"
	err = db.Save(account).Error
	require.NoError(t, err, "Failed to update account")

	// Verify UPDATE audit
	var updateAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", account.ID, "UPDATE").
		First(&updateAudit).Error
	require.NoError(t, err, "UPDATE audit should exist")
	assert.Equal(t, "UPDATE", updateAudit.Operation)
	assert.Equal(t, "account", updateAudit.Table)
	assert.NotEmpty(t, updateAudit.OldValues, "UPDATE should capture old values")
	assert.NotEmpty(t, updateAudit.NewValues, "UPDATE should capture new values")

	// Verify old values contain original balance
	assert.Contains(t, updateAudit.OldValues, "10000", "Old values should contain original balance")
	// Verify new values contain updated balance
	assert.Contains(t, updateAudit.NewValues, "20000", "New values should contain new balance")
}

// TestAccountAudit_Delete verifies that Account DELETE creates outbox entry
// with old_values capturing the deleted state.
func TestAccountAudit_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a customer first
	customer := &Customer{
		CustomerNumber: "C102",
		FirstName:      "Delete",
		LastName:       "Test",
		Email:          "delete.test@example.com",
		Status:         "active",
	}
	err := db.Create(customer).Error
	require.NoError(t, err, "Failed to create customer")

	// Create account
	account := &Account{
		AccountNumber: "GB82WEST12345698765434",
		AccountType:   "savings",
		Currency:      "GBP",
		Status:        "active",
		CustomerID:    customer.ID,
		Balance:       50000,
	}
	err = db.Create(account).Error
	require.NoError(t, err, "Failed to create account")

	accountID := account.ID

	// Delete the account (using Unscoped to permanently delete, not soft delete)
	err = db.Unscoped().Delete(account).Error
	require.NoError(t, err, "Failed to delete account")

	// Verify DELETE audit
	var deleteAudit AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", accountID, "DELETE").
		First(&deleteAudit).Error
	require.NoError(t, err, "DELETE audit should exist")
	assert.Equal(t, "DELETE", deleteAudit.Operation)
	assert.Equal(t, "account", deleteAudit.Table)
	assert.NotEmpty(t, deleteAudit.OldValues, "DELETE should capture old values")
	assert.Empty(t, deleteAudit.NewValues, "DELETE should have empty new values")
}

// TestAccountAudit_TransactionalConsistency verifies that Account audit records
// are rolled back with the business transaction on failure.
func TestAccountAudit_TransactionalConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a customer first
	customer := &Customer{
		CustomerNumber: "C103",
		FirstName:      "Txn",
		LastName:       "Test",
		Email:          "txn.test@example.com",
		Status:         "active",
	}
	err := db.Create(customer).Error
	require.NoError(t, err, "Failed to create customer")

	// Count outbox entries before (should have 1 from customer create)
	var countBefore int64
	db.Table("audit_outbox").Count(&countBefore)

	// Force transaction failure
	err = db.Transaction(func(tx *gorm.DB) error {
		account := &Account{
			AccountNumber: "GB82WEST12345698765435",
			AccountType:   "current",
			Currency:      "GBP",
			Status:        "active",
			CustomerID:    customer.ID,
			Balance:       10000,
		}

		err := tx.Create(account).Error
		require.NoError(t, err, "Account creation should succeed within transaction")

		// Verify outbox entry exists within transaction
		var count int64
		tx.Table("audit_outbox").
			Where("record_id = ? AND table_name = ?", account.ID, "account").
			Count(&count)
		assert.Equal(t, int64(1), count, "Account outbox entry should exist within transaction")

		// Force rollback
		return errForcedTransactionFailure
	})
	require.Error(t, err, "Transaction should fail")

	// Verify account outbox entry was rolled back
	var countAfter int64
	db.Table("audit_outbox").Count(&countAfter)
	assert.Equal(t, countBefore, countAfter, "Account outbox should be rolled back")

	// Verify account was also rolled back
	var accountCount int64
	db.Model(&Account{}).Count(&accountCount)
	assert.Equal(t, int64(0), accountCount, "Account should not exist after rollback")
}

// TestAccountAudit_CapturesAllOperations verifies that all Account operations
// (INSERT, UPDATE, DELETE) are captured in the audit outbox within a single test.
func TestAccountAudit_CapturesAllOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a customer first
	customer := &Customer{
		CustomerNumber: "C104",
		FirstName:      "AllOps",
		LastName:       "Test",
		Email:          "allops.test@example.com",
		Status:         "active",
	}
	err := db.Create(customer).Error
	require.NoError(t, err, "Failed to create customer")

	// INSERT
	account := &Account{
		AccountNumber: "GB82WEST12345698765436",
		AccountType:   "current",
		Currency:      "GBP",
		Status:        "active",
		CustomerID:    customer.ID,
		Balance:       10000,
	}
	err = db.Create(account).Error
	require.NoError(t, err, "Failed to create account")

	// UPDATE
	account.Balance = 15000
	err = db.Save(account).Error
	require.NoError(t, err, "Failed to update account")

	// DELETE
	err = db.Unscoped().Delete(account).Error
	require.NoError(t, err, "Failed to delete account")

	// Verify total audit count for this account
	var totalCount int64
	db.Table("audit_outbox").
		Where("record_id = ? AND table_name = ?", account.ID, "account").
		Count(&totalCount)
	assert.Equal(t, int64(3), totalCount, "Should have exactly 3 audit records for account (INSERT, UPDATE, DELETE)")
}

// setupBenchmarkDB creates a PostgreSQL container for benchmarks.
// Returns the database connection and a cleanup function.
func setupBenchmarkDB(b *testing.B) (*gorm.DB, func()) {
	b.Helper()

	ctx := context.Background()

	// Create PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("bench_db"),
		postgres.WithUsername("bench_user"),
		postgres.WithPassword("bench_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		b.Fatalf("Failed to start postgres container: %v", err)
	}

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		b.Fatalf("Failed to get connection string: %v", err)
	}

	// Connect with GORM
	db, err := gorm.Open(postgresdriver.Open(connStr), &gorm.Config{})
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		b.Fatalf("Failed to connect to test database: %v", err)
	}

	// Enable pgcrypto extension for gen_random_uuid() function
	err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto").Error
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		b.Fatalf("Failed to enable pgcrypto extension: %v", err)
	}

	// Create audit_outbox table
	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id UUID NOT NULL,
			old_values TEXT,
			new_values TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'failed')),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT
		)
	`).Error
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		b.Fatalf("Failed to create audit_outbox table: %v", err)
	}

	// Create indexes
	err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_audit_outbox_status_created
		ON audit_outbox(status, created_at)
	`).Error
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		b.Fatalf("Failed to create audit_outbox indexes: %v", err)
	}

	// Migrate Customer and Account tables
	err = db.AutoMigrate(&Customer{}, &Account{})
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		b.Fatalf("Failed to migrate Customer and Account tables: %v", err)
	}

	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = pgContainer.Terminate(ctx)
	}

	return db, cleanup
}

// BenchmarkAuditOutboxOverhead measures the overhead of writing audit entries
// within a transaction. ADR-0009 target: <5ms per operation.
//
// This benchmark measures the Customer INSERT operation which includes:
// 1. Customer INSERT to customers table
// 2. Audit outbox INSERT to audit_outbox table (via AfterCreate hook)
//
// Run with: go test -bench=BenchmarkAuditOutboxOverhead -benchmem ./shared/domain/models/
func BenchmarkAuditOutboxOverhead(b *testing.B) {
	db, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Use UUID suffix to guarantee uniqueness across iterations
		uniqueID := uuid.New().String()[:8]
		customer := &Customer{
			CustomerNumber: "CUST" + uniqueID,
			FirstName:      "Benchmark",
			LastName:       "Customer",
			Email:          "bench" + uniqueID + "@example.com",
			Status:         "active",
		}
		if err := db.Create(customer).Error; err != nil {
			b.Fatalf("Failed to create customer: %v", err)
		}
	}

	b.StopTimer()

	// Report results with ADR-0009 target
	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	msPerOp := nsPerOp / 1_000_000
	b.ReportMetric(msPerOp, "ms/op")

	// Log target comparison (informational, not enforced in benchmark)
	b.Logf("ADR-0009 target: <5ms, actual: %.3fms per operation", msPerOp)
}

// BenchmarkAuditOutboxOverhead_Account measures Account INSERT with audit.
// Account has more fields than Customer, testing serialization overhead.
func BenchmarkAuditOutboxOverhead_Account(b *testing.B) {
	db, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	// Create a customer first (required for foreign key)
	customer := &Customer{
		CustomerNumber: "CUSTBENCH001",
		FirstName:      "Benchmark",
		LastName:       "Parent",
		Email:          "benchmark.parent@example.com",
		Status:         "active",
	}
	if err := db.Create(customer).Error; err != nil {
		b.Fatalf("Failed to create customer: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Use UUID suffix to guarantee uniqueness across iterations
		uniqueID := uuid.New().String()[:8]
		account := &Account{
			AccountNumber: "GB82WEST" + uniqueID,
			AccountType:   "current",
			Currency:      "GBP",
			Status:        "active",
			CustomerID:    customer.ID,
			Balance:       int64(i * 100),
		}
		if err := db.Create(account).Error; err != nil {
			b.Fatalf("Failed to create account: %v", err)
		}
	}

	b.StopTimer()

	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	msPerOp := nsPerOp / 1_000_000
	b.ReportMetric(msPerOp, "ms/op")
	b.Logf("ADR-0009 target: <5ms, actual: %.3fms per operation", msPerOp)
}

// BenchmarkAuditOutboxOverhead_Update measures UPDATE operation overhead.
// UPDATE requires BeforeUpdate hook to capture old values, which adds
// an additional SELECT query.
func BenchmarkAuditOutboxOverhead_Update(b *testing.B) {
	db, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	// Create customers to update
	customers := make([]*Customer, b.N)
	for i := 0; i < b.N; i++ {
		// Use UUID suffix to guarantee uniqueness across iterations
		uniqueID := uuid.New().String()[:8]
		customer := &Customer{
			CustomerNumber: "CUSTUPD" + uniqueID,
			FirstName:      "Update",
			LastName:       "Test",
			Email:          "update" + uniqueID + "@example.com",
			Status:         "active",
		}
		if err := db.Create(customer).Error; err != nil {
			b.Fatalf("Failed to create customer for update: %v", err)
		}
		customers[i] = customer
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		customers[i].FirstName = "Updated"
		customers[i].Status = "suspended"
		if err := db.Save(customers[i]).Error; err != nil {
			b.Fatalf("Failed to update customer: %v", err)
		}
	}

	b.StopTimer()

	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	msPerOp := nsPerOp / 1_000_000
	b.ReportMetric(msPerOp, "ms/op")
	b.Logf("ADR-0009 target: <5ms, UPDATE actual: %.3fms per operation", msPerOp)
}
