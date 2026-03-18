package audit

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
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

// testHelper provides a common interface for both testing.T and testing.B
// to enable shared setup code between tests and benchmarks.
type testHelper interface {
	Helper()
	Fatalf(format string, args ...interface{})
}

// setupAuditDB creates a PostgreSQL container with GORM for testing/benchmarking.
// PostgreSQL is used instead of SQLite to match production CockroachDB behavior.
// Returns the database connection and a cleanup function.
func setupAuditDB(h testHelper) (*gorm.DB, func()) {
	h.Helper()

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
	if err != nil {
		h.Fatalf("Failed to start postgres container: %v", err)
	}

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		h.Fatalf("Failed to get connection string: %v", err)
	}

	// Connect with GORM
	db, err := gorm.Open(postgresdriver.Open(connStr), &gorm.Config{})
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		h.Fatalf("Failed to connect to test database: %v", err)
	}

	// Enable pgcrypto extension for gen_random_uuid() function
	if err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto").Error; err != nil {
		_ = pgContainer.Terminate(ctx)
		h.Fatalf("Failed to enable pgcrypto extension: %v", err)
	}

	// Create audit_outbox table (unqualified, uses public schema)
	if err = db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id VARCHAR(50) NOT NULL,
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
	`).Error; err != nil {
		_ = pgContainer.Terminate(ctx)
		h.Fatalf("Failed to create audit_outbox table: %v", err)
	}

	// Create indexes for audit_outbox
	if err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_audit_outbox_status_created
		ON audit_outbox(status, created_at)
	`).Error; err != nil {
		_ = pgContainer.Terminate(ctx)
		h.Fatalf("Failed to create audit_outbox indexes: %v", err)
	}

	// Create audit_log table (unqualified for search_path routing)
	if err = db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id VARCHAR(50) NOT NULL,
			old_values TEXT,
			new_values TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT
		)
	`).Error; err != nil {
		_ = pgContainer.Terminate(ctx)
		h.Fatalf("Failed to create audit_log table: %v", err)
	}

	// Create indexes for audit_log (ignore errors for non-critical indexes)
	_ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_log_table_name ON audit_log(table_name)`).Error
	_ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_log_operation ON audit_log(operation)`).Error
	_ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_log_record_id ON audit_log(record_id)`).Error

	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = pgContainer.Terminate(ctx)
	}

	return db, cleanup
}

// setupTestDB creates a PostgreSQL container with GORM for testing.
// PostgreSQL is used instead of SQLite to match production CockroachDB behavior.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, cleanup := setupAuditDB(t)
	t.Cleanup(cleanup)
	return db
}

// createTestEntry creates a single audit outbox entry for testing.
func createTestEntry(t *testing.T, db *gorm.DB, status string) *AuditOutbox {
	t.Helper()

	entry := &AuditOutbox{
		ID:        uuid.New(),
		Table:     "customer", // singular table name for search_path routing
		Operation: "INSERT",
		RecordID:  uuid.New().String(),
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
		_ = createTestEntry(t, db, StatusPending)
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
		err := db.Model(&AuditOutbox{}).
			Where("status = ?", StatusCompleted).
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
	worker := NewAuditWorker(db, "", nil)
	worker.pollInterval = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker.Start(ctx)
	defer worker.Stop()

	// Wait for processing (max 10 seconds)
	waitForProcessing(t, db, 10, 10*time.Second)

	// Verify all have status='completed'
	var completed int64
	err := db.Model(&AuditOutbox{}).
		Where("status = ?", StatusCompleted).
		Count(&completed).Error
	require.NoError(t, err)
	assert.Equal(t, int64(10), completed, "All entries should be completed")

	// Verify no pending entries remain
	var pending int64
	err = db.Model(&AuditOutbox{}).
		Where("status = ?", StatusPending).
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
	worker := NewAuditWorker(db, "", nil)
	worker.batchSize = 100
	ctx := context.Background()

	// Process one batch manually
	err := worker.processBatch(ctx)
	require.NoError(t, err)

	// Verify exactly 100 were processed
	var completed int64
	err = db.Model(&AuditOutbox{}).
		Where("status = ?", StatusCompleted).
		Count(&completed).Error
	require.NoError(t, err)
	assert.Equal(t, int64(100), completed, "Exactly 100 entries should be completed")

	// Verify 150 remain pending
	var pending int64
	err = db.Model(&AuditOutbox{}).
		Where("status = ?", StatusPending).
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
	entry := createTestEntry(t, db, StatusCompleted)
	originalUpdatedAt := entry.CreatedAt

	// Create worker
	worker := NewAuditWorker(db, "", nil)
	ctx := context.Background()

	// Process batch (should skip completed entry)
	err := worker.processBatch(ctx)
	require.NoError(t, err)

	// Verify entry is still completed and unchanged
	var updated AuditOutbox
	err = db.First(&updated, entry.ID).Error
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, updated.Status, "Status should still be completed")
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
	entry := createTestEntry(t, db, StatusPending)

	// Create worker with custom max retries
	worker := NewAuditWorker(db, "", nil)
	worker.maxRetries = 3
	ctx := context.Background()

	// Manually simulate what happens on processing errors
	// Set entry to processing state
	entry.Status = StatusProcessing
	err := db.Save(entry).Error
	require.NoError(t, err)

	// Simulate first failure - manually call handleProcessingError
	err = worker.handleProcessingError(ctx, entry, ErrSimulatedProcessingFailure)
	require.Error(t, err)

	// Reload entry from database
	err = db.First(entry, entry.ID).Error
	require.NoError(t, err)

	// Verify status goes back to 'pending'
	assert.Equal(t, StatusPending, entry.Status, "Status should be pending after first retry")
	// Verify RetryCount incremented
	assert.Equal(t, 1, entry.RetryCount, "RetryCount should be 1")
	// Verify LastError set
	require.NotNil(t, entry.LastError, "LastError should be set")
	assert.Contains(t, *entry.LastError, "simulated processing error", "LastError should contain error message")

	// Simulate second failure
	entry.Status = StatusProcessing
	err = db.Save(entry).Error
	require.NoError(t, err)

	err = worker.handleProcessingError(ctx, entry, ErrSimulatedProcessingFailure)
	require.Error(t, err)
	err = db.First(entry, entry.ID).Error
	require.NoError(t, err)
	assert.Equal(t, StatusPending, entry.Status, "Status should still be pending")
	assert.Equal(t, 2, entry.RetryCount, "RetryCount should be 2")

	// Simulate third failure - should move to 'failed' state
	entry.Status = StatusProcessing
	err = db.Save(entry).Error
	require.NoError(t, err)

	err = worker.handleProcessingError(ctx, entry, ErrSimulatedProcessingFailure)
	require.Error(t, err)
	err = db.First(entry, entry.ID).Error
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, entry.Status, "Status should be failed after max retries")
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
	worker := NewAuditWorker(db, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker.Start(ctx)

	//nolint:forbidigo // Intentional: worker has no observable "started" state to poll
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
	stuckEntry := &AuditOutbox{
		ID:        uuid.New(),
		Table:     "customers",
		Operation: "INSERT",
		RecordID:  uuid.New().String(),
		NewValues: `{"id": "123", "name": "Test Customer"}`,
		Status:    StatusProcessing,
		CreatedAt: time.Now().Add(-10 * time.Minute), // 10 minutes ago (older than defaultProcessingAge)
	}
	err := db.Create(stuckEntry).Error
	require.NoError(t, err)

	// Create recent processing entry (should not be reset)
	recentEntry := &AuditOutbox{
		ID:        uuid.New(),
		Table:     "customers",
		Operation: "INSERT",
		RecordID:  uuid.New().String(),
		NewValues: `{"id": "456", "name": "Recent Customer"}`,
		Status:    StatusProcessing,
		CreatedAt: time.Now().Add(-1 * time.Minute), // 1 minute ago (newer than defaultProcessingAge)
	}
	err = db.Create(recentEntry).Error
	require.NoError(t, err)

	// Create worker and run resetStuckEntries
	worker := NewAuditWorker(db, "", nil)
	ctx := context.Background()

	err = worker.resetStuckEntries(ctx)
	require.NoError(t, err)

	// Verify stuck entry status back to 'pending'
	var updatedStuckEntry AuditOutbox
	err = db.First(&updatedStuckEntry, stuckEntry.ID).Error
	require.NoError(t, err)
	assert.Equal(t, StatusPending, updatedStuckEntry.Status, "Stuck entry should be reset to pending")

	// Verify recent entry is still 'processing'
	var updatedRecentEntry AuditOutbox
	err = db.First(&updatedRecentEntry, recentEntry.ID).Error
	require.NoError(t, err)
	assert.Equal(t, StatusProcessing, updatedRecentEntry.Status, "Recent entry should still be processing")
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
		worker := NewAuditWorker(db, "", nil)
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
	err := db.Model(&AuditOutbox{}).
		Where("status = ?", StatusCompleted).
		Count(&completed).Error
	require.NoError(t, err)
	assert.Equal(t, int64(100), completed, "All 100 entries should be completed")

	// Verify no entries were left in other states
	var total int64
	err = db.Model(&AuditOutbox{}).Count(&total).Error
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
	worker := NewAuditWorker(db, "", nil)
	ctx := context.Background()

	// Process batch with empty queue
	err := worker.processBatch(ctx)
	require.NoError(t, err, "Processing empty queue should not error")

	// Verify no entries exist
	var count int64
	err = db.Model(&AuditOutbox{}).Count(&count).Error
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
	entry := createTestEntry(t, db, StatusPending)

	// Create worker
	worker := NewAuditWorker(db, "", nil)

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
	worker := NewAuditWorker(db, "", nil)

	// Start and stop
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	//nolint:forbidigo // Intentional: worker has no observable "started" state to poll
	time.Sleep(100 * time.Millisecond)
	worker.Stop()
	cancel()

	// Verify Stop() is idempotent - calling it again should not panic
	// This is verified by defer in Stop() which prevents double-close panics
	// Note: This will still panic on the channel close, so we cannot test multiple stops
	// on the same worker instance. Instead, we verify that a single Stop() works correctly.

	// For a fresh start, create a new worker instance
	worker2 := NewAuditWorker(db, "", nil)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	worker2.Start(ctx2)
	//nolint:forbidigo // Intentional: worker has no observable "started" state to poll
	time.Sleep(100 * time.Millisecond)
	worker2.Stop()

	// Test passed - workers can be created, started, and stopped correctly
	// However, individual worker instances cannot be restarted
}

// TestAuditWorker_Configuration verifies custom configuration options.
func TestAuditWorker_Configuration(t *testing.T) {
	db := setupTestDB(t)

	// Create worker with custom settings (legacy direct assignment)
	worker := NewAuditWorker(db, "", nil)
	worker.batchSize = 50
	worker.pollInterval = 10 * time.Second
	worker.maxRetries = 5

	assert.Equal(t, 50, worker.batchSize, "BatchSize should be customizable")
	assert.Equal(t, 10*time.Second, worker.pollInterval, "PollInterval should be customizable")
	assert.Equal(t, 5, worker.maxRetries, "MaxRetries should be customizable")
}

// TestAuditWorker_FunctionalOptions verifies functional options pattern.
func TestAuditWorker_FunctionalOptions(t *testing.T) {
	db := setupTestDB(t)

	// Create worker with functional options
	worker := NewAuditWorker(db, "test_schema", nil,
		WithBatchSize(200),
		WithPollInterval(15*time.Second),
		WithMaxRetries(5),
	)

	assert.Equal(t, 200, worker.batchSize, "BatchSize should be set via option")
	assert.Equal(t, 15*time.Second, worker.pollInterval, "PollInterval should be set via option")
	assert.Equal(t, 5, worker.maxRetries, "MaxRetries should be set via option")
	assert.Equal(t, "test_schema", worker.schema, "Schema should be preserved")
	assert.False(t, worker.adaptivePolling, "AdaptivePolling should be false by default")
}

// TestAuditWorker_FunctionalOptions_AdaptivePolling verifies adaptive polling configuration.
func TestAuditWorker_FunctionalOptions_AdaptivePolling(t *testing.T) {
	db := setupTestDB(t)

	// Create worker with adaptive polling enabled
	worker := NewAuditWorker(db, "", nil,
		WithAdaptivePolling(50*time.Millisecond, 10*time.Second),
	)

	assert.True(t, worker.adaptivePolling, "AdaptivePolling should be enabled")
	assert.Equal(t, 50*time.Millisecond, worker.minPollInterval, "MinPollInterval should be set")
	assert.Equal(t, 10*time.Second, worker.maxPollInterval, "MaxPollInterval should be set")
}

// TestAuditWorker_FunctionalOptions_InvalidValues verifies invalid values are ignored.
func TestAuditWorker_FunctionalOptions_InvalidValues(t *testing.T) {
	db := setupTestDB(t)

	// Create worker with invalid options (should use defaults)
	worker := NewAuditWorker(db, "", nil,
		WithBatchSize(0),    // Invalid - should keep default
		WithBatchSize(-1),   // Invalid - should keep default
		WithPollInterval(0), // Invalid - should keep default
		WithMaxRetries(-1),  // Invalid - should keep default (but 0 is valid)
	)

	assert.Equal(t, defaultBatchSize, worker.batchSize, "Invalid batch size should keep default")
	assert.Equal(t, defaultPollInterval, worker.pollInterval, "Invalid poll interval should keep default")
	assert.Equal(t, defaultMaxRetries, worker.maxRetries, "Invalid max retries should keep default")
}

// TestAuditWorker_AdaptivePolling_IntervalCalculation verifies adaptive interval logic.
func TestAuditWorker_AdaptivePolling_IntervalCalculation(t *testing.T) {
	db := setupTestDB(t)

	worker := NewAuditWorker(db, "", nil,
		WithAdaptivePolling(100*time.Millisecond, 5*time.Second),
	)

	// When entries are processed, interval should be minimum
	interval := worker.calculateAdaptiveInterval(10)
	assert.Equal(t, 100*time.Millisecond, interval, "With entries, should use min interval")
	assert.Equal(t, 0, worker.emptyPollCount, "Empty poll count should reset to 0")

	// First empty poll - should increase interval
	interval = worker.calculateAdaptiveInterval(0)
	assert.Equal(t, 1, worker.emptyPollCount, "Empty poll count should be 1")
	assert.True(t, interval > 100*time.Millisecond, "Interval should increase after empty poll")

	// More empty polls - interval should continue increasing
	for i := 0; i < 5; i++ {
		interval = worker.calculateAdaptiveInterval(0)
	}
	assert.Equal(t, 6, worker.emptyPollCount, "Empty poll count should be 6")

	// Eventually should cap at max interval
	for i := 0; i < 20; i++ {
		interval = worker.calculateAdaptiveInterval(0)
	}
	assert.Equal(t, 5*time.Second, interval, "Interval should cap at max")

	// Processing entries again should reset to min
	interval = worker.calculateAdaptiveInterval(5)
	assert.Equal(t, 100*time.Millisecond, interval, "Should reset to min after processing entries")
	assert.Equal(t, 0, worker.emptyPollCount, "Empty poll count should reset to 0")
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
	worker := NewAuditWorker(db, "", nil)
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

	worker := NewAuditWorker(db, "", logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	worker.Start(ctx)

	//nolint:forbidigo // Intentional: worker has no observable "started" state to poll
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
	//nolint:forbidigo // Intentional: verifying worker does not process entries after stop (negative assertion) requires waiting
	time.Sleep(200 * time.Millisecond)

	var processed int64
	db.Model(&AuditOutbox{}).Where("status = ?", "completed").Count(&processed)
	if processed > 0 {
		t.Errorf("Worker processed entries after Stop(), expected 0 but got %d", processed)
	}
}

// TestAuditWorker_InsertsIntoAuditLog verifies that the worker successfully
// creates audit_log entries from outbox entries (end-to-end test).
func TestAuditWorker_InsertsIntoAuditLog(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Create test outbox entries
	createTestEntries(t, db, 5)

	// Fetch the created entries for verification later
	var entries []AuditOutbox
	err := db.Where("status = ?", StatusPending).Find(&entries).Error
	if err != nil {
		t.Fatalf("Failed to fetch outbox entries: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("Expected 5 entries, got %d", len(entries))
	}

	// Verify audit_log table starts empty
	// Note: Using unqualified table name since test worker uses empty schema (public schema)
	var initialCount int64
	db.Table("audit_log").Count(&initialCount)
	if initialCount != 0 {
		t.Fatalf("Expected audit_log to be empty, but found %d entries", initialCount)
	}

	// Create and start worker
	worker := NewAuditWorker(db, "", logger)
	worker.pollInterval = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	worker.Start(ctx)

	// Wait for processing to complete
	waitForProcessing(t, db, 5, 5*time.Second)

	worker.Stop()

	// Verify all outbox entries were processed
	var completed int64
	db.Model(&AuditOutbox{}).Where("status = ?", "completed").Count(&completed)
	if completed != 5 {
		t.Errorf("Expected 5 completed outbox entries, got %d", completed)
	}

	// Verify audit_log entries were created
	var auditLogCount int64
	db.Table("audit_log").Count(&auditLogCount)
	if auditLogCount != 5 {
		t.Errorf("Expected 5 audit_log entries, got %d", auditLogCount)
	}

	// Verify audit_log entries match outbox entries
	for _, entry := range entries {
		var auditLog struct {
			ID            uuid.UUID
			Table         string `gorm:"column:table_name"`
			Operation     string
			RecordID      string  `gorm:"column:record_id"`
			OldValues     string  `gorm:"column:old_values"`
			NewValues     string  `gorm:"column:new_values"`
			ChangedBy     *string `gorm:"column:changed_by"`
			TransactionID *string `gorm:"column:transaction_id"`
			ClientIP      *string `gorm:"column:client_ip"`
			UserAgent     *string `gorm:"column:user_agent"`
		}

		err := db.Table("audit_log").
			Where("record_id = ? AND operation = ?", entry.RecordID, entry.Operation).
			First(&auditLog).Error
		if err != nil {
			t.Errorf("Failed to find audit_log entry for outbox %s: %v", entry.ID, err)
			continue
		}

		// Verify key fields match
		if auditLog.Table != entry.Table {
			t.Errorf("Table mismatch: expected %s, got %s", entry.Table, auditLog.Table)
		}
		if auditLog.Operation != entry.Operation {
			t.Errorf("Operation mismatch: expected %s, got %s", entry.Operation, auditLog.Operation)
		}
		if auditLog.RecordID != entry.RecordID {
			t.Errorf("RecordID mismatch: expected %s, got %s", entry.RecordID, auditLog.RecordID)
		}
	}
}

// setupBenchmarkDB creates a PostgreSQL container for benchmarks.
// Returns the database connection and a cleanup function.
// Uses the shared setupAuditDB helper to avoid code duplication.
func setupBenchmarkDB(b *testing.B) (*gorm.DB, func()) {
	return setupAuditDB(b)
}

// createBenchmarkEntries creates N audit outbox entries for benchmarking.
func createBenchmarkEntries(b *testing.B, db *gorm.DB, count int) {
	b.Helper()

	for i := 0; i < count; i++ {
		entry := &AuditOutbox{
			ID:        uuid.New(),
			Table:     "customer",
			Operation: "INSERT",
			RecordID:  uuid.New().String(),
			NewValues: `{"id": "123", "customer_number": "CUST001", "first_name": "Benchmark", "last_name": "Test", "email": "bench@example.com", "status": "active"}`,
			Status:    StatusPending,
			CreatedAt: time.Now(),
		}

		if err := db.Create(entry).Error; err != nil {
			b.Fatalf("Failed to create benchmark entry: %v", err)
		}
	}
}

// BenchmarkAuditWorkerThroughput measures the worker's processing throughput.
// ADR-0009 target: >1000 records/second.
//
// This benchmark measures how fast the worker can process audit outbox entries
// by calling processBatch directly. It tests batch sizes of 10, 100, and 1000.
//
// Run with: go test -bench=BenchmarkAuditWorkerThroughput -benchmem ./shared/platform/audit/
func BenchmarkAuditWorkerThroughput(b *testing.B) {
	batchSizes := []int{10, 100, 1000}

	for _, batchSize := range batchSizes {
		batchSize := batchSize // capture for closure
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			runWorkerThroughputBenchmark(b, batchSize)
		})
	}
}

func runWorkerThroughputBenchmark(b *testing.B, batchSize int) {
	db, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create worker with specified batch size
	worker := NewAuditWorker(db, "", logger)
	worker.batchSize = batchSize

	ctx := context.Background()

	// Total records to process: we create entries per iteration to ensure
	// each processBatch call has fresh entries to process.
	// We stop timer during entry creation to measure only processBatch.
	totalRecords := 0

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Stop timer during entry creation
		b.StopTimer()
		createBenchmarkEntries(b, db, batchSize)
		totalRecords += batchSize
		b.StartTimer()

		// Process the batch (fresh entries created above)
		if err := worker.processBatch(ctx); err != nil {
			b.Fatalf("Failed to process batch: %v", err)
		}
	}

	b.StopTimer()

	// Calculate throughput
	elapsedSec := b.Elapsed().Seconds()
	throughput := float64(totalRecords) / elapsedSec

	b.ReportMetric(throughput, "records/sec")
	b.Logf("ADR-0009 target: >1000 records/sec, actual: %.0f records/sec (batch size: %d, total records: %d)",
		throughput, batchSize, totalRecords)
}

// BenchmarkAuditWorkerProcessEntry measures single entry processing time.
// This helps identify per-entry overhead vs batch overhead.
func BenchmarkAuditWorkerProcessEntry(b *testing.B) {
	db, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	worker := NewAuditWorker(db, "", logger)
	ctx := context.Background()

	// Create all entries upfront
	entries := make([]*AuditOutbox, b.N)
	for i := 0; i < b.N; i++ {
		entry := &AuditOutbox{
			ID:        uuid.New(),
			Table:     "customer",
			Operation: "INSERT",
			RecordID:  uuid.New().String(),
			NewValues: `{"id": "123", "name": "Test"}`,
			Status:    StatusPending,
			CreatedAt: time.Now(),
		}
		if err := db.Create(entry).Error; err != nil {
			b.Fatalf("Failed to create entry: %v", err)
		}
		entries[i] = entry
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := worker.processEntry(ctx, entries[i]); err != nil {
			b.Fatalf("Failed to process entry: %v", err)
		}
	}

	b.StopTimer()

	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	msPerOp := nsPerOp / 1_000_000
	b.ReportMetric(msPerOp, "ms/op")
	b.Logf("Per-entry processing time: %.3fms", msPerOp)
}

// BenchmarkAuditWorkerE2E measures end-to-end throughput with the worker
// running in the background. This simulates real-world conditions.
func BenchmarkAuditWorkerE2E(b *testing.B) {
	db, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create 1000 entries before starting
	const totalEntries = 1000
	createBenchmarkEntries(b, db, totalEntries)

	// Create and start worker with fast poll interval
	worker := NewAuditWorker(db, "", logger)
	worker.pollInterval = 10 * time.Millisecond
	worker.batchSize = 100

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b.ResetTimer()

	// Start worker
	worker.Start(ctx)

	// Wait for all entries to be processed
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			b.Fatalf("Timeout waiting for entries to be processed")
		case <-ticker.C:
			var completed int64
			if err := db.Model(&AuditOutbox{}).
				Where("status = ?", StatusCompleted).
				Count(&completed).Error; err != nil {
				b.Fatalf("Failed to count completed entries: %v", err)
			}
			if completed >= totalEntries {
				goto done
			}
		}
	}
done:

	b.StopTimer()
	worker.Stop()

	// Calculate throughput
	elapsedSec := b.Elapsed().Seconds()
	throughput := float64(totalEntries) / elapsedSec

	b.ReportMetric(throughput, "records/sec")
	b.Logf("ADR-0009 target: >1000 records/sec, E2E actual: %.0f records/sec (%d records in %.2fs)",
		throughput, totalEntries, elapsedSec)
}
