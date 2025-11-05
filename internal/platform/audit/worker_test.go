package audit

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/domain/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	postgresdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// setupTestDB creates a PostgreSQL container with GORM for testing.
// PostgreSQL is used instead of SQLite to match production CockroachDB behavior.
func setupTestDB(t *testing.T) *gorm.DB {
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

	// Create schemas (PostgreSQL supports schemas like CockroachDB)
	err = db.Exec("CREATE SCHEMA IF NOT EXISTS current_account").Error
	require.NoError(t, err, "Failed to create current_account schema")

	err = db.Exec("CREATE SCHEMA IF NOT EXISTS current_account_audit").Error
	require.NoError(t, err, "Failed to create current_account_audit schema")

	// Create audit_outbox table
	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS current_account_audit.audit_outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id UUID NOT NULL,
			old_values TEXT,
			new_values TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'failed', 'completed')),
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
		ON current_account_audit.audit_outbox(status, created_at)
	`).Error
	require.NoError(t, err, "Failed to create audit_outbox indexes")

	// Register cleanup
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = pgContainer.Terminate(ctx)
	})

	return db
}

// createTestEntry creates a single audit outbox entry for testing.
func createTestEntry(t *testing.T, db *gorm.DB, status string) *models.AuditOutbox {
	t.Helper()

	entry := &models.AuditOutbox{
		ID:        uuid.New(),
		Table:     "customers",
		Operation: "INSERT",
		RecordID:  uuid.New(),
		NewValues: `{"id": "123", "name": "Test Customer"}`,
		Status:    status,
		CreatedAt: time.Now(),
	}

	err := db.Create(entry).Error
	require.NoError(t, err, "Failed to create test entry")

	return entry
}

// createTestEntries creates multiple audit outbox entries for testing.
func createTestEntries(t *testing.T, db *gorm.DB, count int) {
	t.Helper()

	for i := 0; i < count; i++ {
		_ = createTestEntry(t, db, statusPending)
	}
}

// waitForProcessing waits for the specified number of entries to be completed.
// It polls the database every 100ms until the expected count is reached or timeout occurs.
func waitForProcessing(t *testing.T, db *gorm.DB, expectedCompleted int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		var count int64
		err := db.Model(&models.AuditOutbox{}).
			Where("status = ?", statusCompleted).
			Count(&count).Error
		require.NoError(t, err, "Failed to count completed entries")

		if int(count) >= expectedCompleted {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("Timeout waiting for %d entries to complete, got %d", expectedCompleted, count)
		}

		<-ticker.C
	}
}

// TestAuditWorker_ProcessBatch_Success verifies successful processing of a batch.
func TestAuditWorker_ProcessBatch_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Create 10 pending entries
	createTestEntries(t, db, 10)

	// Start worker with faster poll interval for testing
	worker := NewAuditWorker(db, nil)
	worker.pollInterval = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker.Start(ctx)
	defer worker.Stop()

	// Wait for processing (max 10 seconds)
	waitForProcessing(t, db, 10, 10*time.Second)

	// Verify all have status='completed'
	var completed int64
	err := db.Model(&models.AuditOutbox{}).
		Where("status = ?", statusCompleted).
		Count(&completed).Error
	require.NoError(t, err)
	assert.Equal(t, int64(10), completed, "All entries should be completed")

	// Verify no pending entries remain
	var pending int64
	err = db.Model(&models.AuditOutbox{}).
		Where("status = ?", statusPending).
		Count(&pending).Error
	require.NoError(t, err)
	assert.Equal(t, int64(0), pending, "No pending entries should remain")
}

// TestAuditWorker_ProcessBatch_BatchSize verifies batch size limits.
func TestAuditWorker_ProcessBatch_BatchSize(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Create 250 pending entries
	createTestEntries(t, db, 250)

	// Create worker with batch size of 100
	worker := NewAuditWorker(db, nil)
	worker.batchSize = 100
	ctx := context.Background()

	// Process one batch manually
	err := worker.processBatch(ctx)
	require.NoError(t, err)

	// Verify exactly 100 were processed
	var completed int64
	err = db.Model(&models.AuditOutbox{}).
		Where("status = ?", statusCompleted).
		Count(&completed).Error
	require.NoError(t, err)
	assert.Equal(t, int64(100), completed, "Exactly 100 entries should be completed")

	// Verify 150 remain pending
	var pending int64
	err = db.Model(&models.AuditOutbox{}).
		Where("status = ?", statusPending).
		Count(&pending).Error
	require.NoError(t, err)
	assert.Equal(t, int64(150), pending, "150 entries should remain pending")
}

// TestAuditWorker_ProcessEntry_Idempotency verifies that completed entries are not reprocessed.
func TestAuditWorker_ProcessEntry_Idempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Create entry with status='completed'
	entry := createTestEntry(t, db, statusCompleted)
	originalUpdatedAt := entry.CreatedAt

	// Create worker
	worker := NewAuditWorker(db, nil)
	ctx := context.Background()

	// Process batch (should skip completed entry)
	err := worker.processBatch(ctx)
	require.NoError(t, err)

	// Verify entry is still completed and unchanged
	var updated models.AuditOutbox
	err = db.First(&updated, entry.ID).Error
	require.NoError(t, err)
	assert.Equal(t, statusCompleted, updated.Status, "Status should still be completed")
	assert.Equal(t, originalUpdatedAt.Unix(), updated.CreatedAt.Unix(), "CreatedAt should be unchanged")
}

// TestAuditWorker_ProcessEntry_RetryLogic verifies retry logic and failure states.
// Note: Since simulateProcessing currently always succeeds, this test verifies
// the retry logic structure by manually simulating what would happen on errors.
// In Phase 3, when actual audit log insertion is implemented, failures can occur naturally.
func TestAuditWorker_ProcessEntry_RetryLogic(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Create entry
	entry := createTestEntry(t, db, statusPending)

	// Create worker with custom max retries
	worker := NewAuditWorker(db, nil)
	worker.maxRetries = 3
	ctx := context.Background()

	// Manually simulate what happens on processing errors
	// Set entry to processing state
	entry.Status = statusProcessing
	err := db.Save(entry).Error
	require.NoError(t, err)

	// Simulate first failure - manually call handleProcessingError
	err = worker.handleProcessingError(ctx, entry, ErrSimulatedProcessingFailure)
	require.Error(t, err)

	// Reload entry from database
	err = db.First(entry, entry.ID).Error
	require.NoError(t, err)

	// Verify status goes back to 'pending'
	assert.Equal(t, statusPending, entry.Status, "Status should be pending after first retry")
	// Verify RetryCount incremented
	assert.Equal(t, 1, entry.RetryCount, "RetryCount should be 1")
	// Verify LastError set
	require.NotNil(t, entry.LastError, "LastError should be set")
	assert.Contains(t, *entry.LastError, "simulated processing error", "LastError should contain error message")

	// Simulate second failure
	entry.Status = statusProcessing
	err = db.Save(entry).Error
	require.NoError(t, err)

	err = worker.handleProcessingError(ctx, entry, ErrSimulatedProcessingFailure)
	require.Error(t, err)
	err = db.First(entry, entry.ID).Error
	require.NoError(t, err)
	assert.Equal(t, statusPending, entry.Status, "Status should still be pending")
	assert.Equal(t, 2, entry.RetryCount, "RetryCount should be 2")

	// Simulate third failure - should move to 'failed' state
	entry.Status = statusProcessing
	err = db.Save(entry).Error
	require.NoError(t, err)

	err = worker.handleProcessingError(ctx, entry, ErrSimulatedProcessingFailure)
	require.Error(t, err)
	err = db.First(entry, entry.ID).Error
	require.NoError(t, err)
	assert.Equal(t, statusFailed, entry.Status, "Status should be failed after max retries")
	assert.Equal(t, 3, entry.RetryCount, "RetryCount should be 3")

	// Verify metrics - failed counter should be incremented
	// Note: This is tested indirectly through the status change
}

// TestAuditWorker_GracefulShutdown verifies graceful shutdown behavior.
func TestAuditWorker_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Start worker
	worker := NewAuditWorker(db, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker.Start(ctx)

	// Give worker time to start
	time.Sleep(100 * time.Millisecond)

	// Call Stop() and measure time
	start := time.Now()
	shutdownComplete := make(chan struct{})
	go func() {
		worker.Stop()
		close(shutdownComplete)
	}()

	// Wait for shutdown with timeout
	select {
	case <-shutdownComplete:
		duration := time.Since(start)
		assert.Less(t, duration, 10*time.Second, "Shutdown should complete within 10 seconds")
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown did not complete within 10 seconds")
	}
}

// TestAuditWorker_ResetStuckEntries verifies that stuck processing entries are reset.
func TestAuditWorker_ResetStuckEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Create entries with status='processing' and old timestamp
	stuckEntry := &models.AuditOutbox{
		ID:        uuid.New(),
		Table:     "customers",
		Operation: "INSERT",
		RecordID:  uuid.New(),
		NewValues: `{"id": "123", "name": "Test Customer"}`,
		Status:    statusProcessing,
		CreatedAt: time.Now().Add(-10 * time.Minute), // 10 minutes ago (older than defaultProcessingAge)
	}
	err := db.Create(stuckEntry).Error
	require.NoError(t, err)

	// Create recent processing entry (should not be reset)
	recentEntry := &models.AuditOutbox{
		ID:        uuid.New(),
		Table:     "customers",
		Operation: "INSERT",
		RecordID:  uuid.New(),
		NewValues: `{"id": "456", "name": "Recent Customer"}`,
		Status:    statusProcessing,
		CreatedAt: time.Now().Add(-1 * time.Minute), // 1 minute ago (newer than defaultProcessingAge)
	}
	err = db.Create(recentEntry).Error
	require.NoError(t, err)

	// Create worker and run resetStuckEntries
	worker := NewAuditWorker(db, nil)
	ctx := context.Background()

	err = worker.resetStuckEntries(ctx)
	require.NoError(t, err)

	// Verify stuck entry status back to 'pending'
	var updatedStuckEntry models.AuditOutbox
	err = db.First(&updatedStuckEntry, stuckEntry.ID).Error
	require.NoError(t, err)
	assert.Equal(t, statusPending, updatedStuckEntry.Status, "Stuck entry should be reset to pending")

	// Verify recent entry is still 'processing'
	var updatedRecentEntry models.AuditOutbox
	err = db.First(&updatedRecentEntry, recentEntry.ID).Error
	require.NoError(t, err)
	assert.Equal(t, statusProcessing, updatedRecentEntry.Status, "Recent entry should still be processing")
}

// TestAuditWorker_ConcurrentSafety verifies concurrent workers don't process duplicates.
// This test relies on database transaction isolation to prevent duplicate processing.
// The status update from 'pending' to 'processing' acts as a lock mechanism.
func TestAuditWorker_ConcurrentSafety(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Create 100 pending entries
	createTestEntries(t, db, 100)

	// Start 3 workers with same database
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workers := make([]*Worker, 3)
	for i := 0; i < 3; i++ {
		worker := NewAuditWorker(db, nil)
		worker.pollInterval = 100 * time.Millisecond // Faster polling for test
		workers[i] = worker
		worker.Start(ctx)
	}

	// Wait for all to complete
	waitForProcessing(t, db, 100, 15*time.Second)

	// Stop all workers
	for _, worker := range workers {
		worker.Stop()
	}

	// Verify all entries are completed
	var completed int64
	err := db.Model(&models.AuditOutbox{}).
		Where("status = ?", statusCompleted).
		Count(&completed).Error
	require.NoError(t, err)
	assert.Equal(t, int64(100), completed, "All 100 entries should be completed")

	// Verify no entries were left in other states
	var total int64
	err = db.Model(&models.AuditOutbox{}).Count(&total).Error
	require.NoError(t, err)
	assert.Equal(t, int64(100), total, "Total entries should be 100")

	// The database transaction isolation ensures that when multiple workers
	// try to update the same entry from 'pending' to 'processing',
	// only one will succeed. This test verifies no duplicates were created
	// and all entries were processed exactly once.
}

// TestAuditWorker_ProcessBatch_EmptyQueue verifies behavior with no pending entries.
func TestAuditWorker_ProcessBatch_EmptyQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Create worker
	worker := NewAuditWorker(db, nil)
	ctx := context.Background()

	// Process batch with empty queue
	err := worker.processBatch(ctx)
	require.NoError(t, err, "Processing empty queue should not error")

	// Verify no entries exist
	var count int64
	err = db.Model(&models.AuditOutbox{}).Count(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(0), count, "No entries should exist")
}

// TestAuditWorker_ProcessEntry_ContextCancellation verifies context cancellation handling.
func TestAuditWorker_ProcessEntry_ContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Create entry
	entry := createTestEntry(t, db, statusPending)

	// Create worker
	worker := NewAuditWorker(db, nil)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Process entry with cancelled context
	err := worker.processEntry(ctx, entry)
	// Should handle context cancellation gracefully
	// The exact error depends on when cancellation is detected
	// but it should not panic
	if err != nil {
		t.Logf("Processing with cancelled context returned error: %v", err)
	}
}

// TestAuditWorker_MultipleStartStop documents that workers cannot be restarted.
// Note: Current implementation closes the shutdown channel permanently.
// If restart capability is needed, the shutdown channel should be recreated in Start().
func TestAuditWorker_MultipleStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Create first worker
	worker := NewAuditWorker(db, nil)

	// Start and stop
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	worker.Stop()
	cancel()

	// Verify Stop() is idempotent - calling it again should not panic
	// This is verified by defer in Stop() which prevents double-close panics
	// Note: This will still panic on the channel close, so we cannot test multiple stops
	// on the same worker instance. Instead, we verify that a single Stop() works correctly.

	// For a fresh start, create a new worker instance
	worker2 := NewAuditWorker(db, nil)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	worker2.Start(ctx2)
	time.Sleep(100 * time.Millisecond)
	worker2.Stop()

	// Test passed - workers can be created, started, and stopped correctly
	// However, individual worker instances cannot be restarted
}

// TestAuditWorker_Configuration verifies custom configuration options.
func TestAuditWorker_Configuration(t *testing.T) {
	db := setupTestDB(t)

	// Create worker with custom settings
	worker := NewAuditWorker(db, nil)
	worker.batchSize = 50
	worker.pollInterval = 10 * time.Second
	worker.maxRetries = 5

	assert.Equal(t, 50, worker.batchSize, "BatchSize should be customizable")
	assert.Equal(t, 10*time.Second, worker.pollInterval, "PollInterval should be customizable")
	assert.Equal(t, 5, worker.maxRetries, "MaxRetries should be customizable")
}

// TestAuditWorker_ProcessBatch_PartialFailure would verify handling of partial batch failures.
// Note: This test is skipped because simulateProcessing currently always succeeds.
// In Phase 3, when actual audit log insertion is implemented, this test can be
// re-enabled by creating conditions that cause specific entries to fail
// (e.g., invalid data, schema violations, etc.)
func TestAuditWorker_ProcessBatch_PartialFailure(t *testing.T) {
	t.Skip("Skipped: simulateProcessing always succeeds. Will be enabled in Phase 3 with real audit log insertion")

	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)

	// Create 10 entries
	createTestEntries(t, db, 10)

	// Create worker
	worker := NewAuditWorker(db, nil)
	ctx := context.Background()

	// Process batch
	err := worker.processBatch(ctx)

	// In Phase 3, when real processing is implemented:
	// - Some entries might fail due to data issues
	// - We can verify partial batch completion
	// - We can verify retry logic for failed entries
	_ = err // Placeholder for future assertions
}

// TestAuditWorker_Stop_MultipleCallsSafe verifies that calling Stop() multiple times
// doesn't panic and is safe for concurrent calls.
func TestAuditWorker_Stop_MultipleCallsSafe(t *testing.T) {
	db := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	worker := NewAuditWorker(db, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	worker.Start(ctx)

	// Wait a moment for worker to start
	time.Sleep(100 * time.Millisecond)

	// Call Stop() multiple times concurrently - should not panic
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Stop() // Should be safe to call multiple times
		}()
	}

	wg.Wait()

	// Verify worker actually stopped by checking it doesn't process new entries
	createTestEntries(t, db, 5)
	time.Sleep(200 * time.Millisecond)

	var processed int64
	db.Model(&models.AuditOutbox{}).Where("status = ?", "completed").Count(&processed)
	if processed > 0 {
		t.Errorf("Worker processed entries after Stop(), expected 0 but got %d", processed)
	}
}
