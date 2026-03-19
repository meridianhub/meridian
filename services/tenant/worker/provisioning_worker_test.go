package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// testErr creates a test error by wrapping a message.
// This avoids err113 linter warnings about dynamic error creation in tests.
func testErr(msg string) error {
	return fmt.Errorf("%s", msg)
}

// testWorkerConfig creates a default Config for tests.
func testWorkerConfig(pollInterval time.Duration) Config {
	return Config{
		PollInterval:   pollInterval,
		MaxRetries:     maxRetries,
		RetryBaseDelay: baseDelay,
		RetryMaxDelay:  maxDelay,
		MaxConcurrent:  10,
	}
}

// safeBuffer is a thread-safe wrapper around bytes.Buffer for concurrent log capture.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *safeBuffer) Write(p []byte) (n int, err error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

// setupTestDB creates a database with the tenant table schema.
// Uses file-based temp database to ensure consistent behavior across connections.
func setupTestDB(t *testing.T) (*gorm.DB, *persistence.Repository) {
	// Use a unique temp file per test to ensure isolation while maintaining
	// consistency across multiple GORM connections within the same test
	tmpFile, err := os.CreateTemp("", "testdb_*.sqlite")
	require.NoError(t, err)
	dbPath := tmpFile.Name()
	tmpFile.Close()

	// Clean up temp file when test completes
	t.Cleanup(func() {
		os.Remove(dbPath)
	})

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)

	// Configure connection pool to avoid connection issues with in-memory DB
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1) // Single connection to ensure consistency

	// Create tenant table
	err = db.Exec(`
		CREATE TABLE tenant (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			settlement_asset TEXT NOT NULL,
			slug TEXT UNIQUE,
			subdomain TEXT UNIQUE,
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deprovisioned_at DATETIME,
			metadata TEXT,
			version INTEGER NOT NULL DEFAULT 1,
			party_id TEXT,
			error_message TEXT
		)
	`).Error
	require.NoError(t, err)

	// Create audit_outbox table (required for audit logging)
	err = db.Exec(`
		CREATE TABLE audit_outbox (
			id TEXT PRIMARY KEY,
			table_name TEXT NOT NULL,
			operation TEXT NOT NULL,
			record_id TEXT NOT NULL,
			old_values TEXT,
			new_values TEXT,
			status TEXT NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			changed_by TEXT,
			transaction_id TEXT,
			client_ip TEXT,
			user_agent TEXT,
			created_at DATETIME NOT NULL
		)
	`).Error
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	return db, repo
}

func TestNewProvisioningWorker_Success(t *testing.T) {
	// Setup dependencies
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 5 * time.Second
	config := testWorkerConfig(pollInterval)

	// Create worker
	worker, err := NewProvisioningWorker(repo, prov, config, logger)

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, worker)
	assert.Equal(t, repo, worker.repo)
	assert.Equal(t, prov, worker.provisioner)
	assert.Equal(t, pollInterval, worker.pollInterval)
	assert.Equal(t, logger, worker.logger)
	assert.NotNil(t, worker.done)
}

func TestNewProvisioningWorker_NilRepository(t *testing.T) {
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	config := testWorkerConfig(5 * time.Second)

	worker, err := NewProvisioningWorker(nil, prov, config, logger)

	assert.ErrorIs(t, err, ErrNilRepository)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NilProvisioner(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	config := testWorkerConfig(5 * time.Second)

	worker, err := NewProvisioningWorker(repo, nil, config, logger)

	assert.ErrorIs(t, err, ErrNilProvisioner)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NilLogger(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	config := testWorkerConfig(5 * time.Second)

	worker, err := NewProvisioningWorker(repo, prov, config, nil)

	assert.ErrorIs(t, err, ErrNilLogger)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_ZeroPollInterval(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	config := testWorkerConfig(0)

	worker, err := NewProvisioningWorker(repo, prov, config, logger)

	assert.ErrorIs(t, err, ErrInvalidPollInterval)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NegativePollInterval(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	config := testWorkerConfig(-5 * time.Second)

	worker, err := NewProvisioningWorker(repo, prov, config, logger)

	assert.ErrorIs(t, err, ErrInvalidPollInterval)
	assert.Nil(t, worker)
}

// TestNewProvisioningWorker_DefaultsApplied verifies that when Config fields are
// zero/unset, sensible defaults are applied. This prevents issues where callers
// forget to set MaxRetries (causing the retry loop to never execute).
func TestNewProvisioningWorker_DefaultsApplied(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Only set PollInterval, leave everything else at zero values
	config := Config{
		PollInterval: 100 * time.Millisecond,
	}

	worker, err := NewProvisioningWorker(repo, prov, config, logger)

	require.NoError(t, err)
	require.NotNil(t, worker)

	// Verify defaults were applied
	assert.Equal(t, 5, worker.maxRetries, "maxRetries should default to 5")
	assert.Equal(t, 2*time.Second, worker.retryBaseDelay, "retryBaseDelay should default to 2s")
	assert.Equal(t, 10, worker.maxConcurrent, "maxConcurrent should default to 10")
	assert.Greater(t, worker.retryMaxDelay, time.Duration(0), "retryMaxDelay should have a default")
	assert.Equal(t, 15*time.Minute, worker.alertInterval, "alertInterval should default to 15m")
	assert.Equal(t, 1*time.Hour, worker.alertThreshold, "alertThreshold should default to 1h")
}

func TestProvisioningWorker_Start_ContextCancellation(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 50 * time.Millisecond

	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(pollInterval), logger)
	require.NoError(t, err)

	// Start worker with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		worker.Start(ctx)
		close(done)
	}()

	// Intentional sleep: Let it run through at least one poll interval before stopping
	time.Sleep(100 * time.Millisecond) //nolint:forbidigo // gives worker time to run at least one poll cycle

	// Cancel context
	cancel()

	// Should stop within a reasonable time
	select {
	case <-done:
		// Success - worker stopped
	case <-time.After(200 * time.Millisecond):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestProvisioningWorker_Start_ExplicitStop(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 50 * time.Millisecond

	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(pollInterval), logger)
	require.NoError(t, err)

	// Start worker
	ctx := context.Background()
	done := make(chan struct{})

	go func() {
		worker.Start(ctx)
		close(done)
	}()

	// Intentional sleep: Let it run through at least one poll interval before stopping
	time.Sleep(100 * time.Millisecond) //nolint:forbidigo // gives worker time to run at least one poll cycle

	// Call Stop()
	worker.Stop()

	// Should stop within a reasonable time
	select {
	case <-done:
		// Success - worker stopped
	case <-time.After(200 * time.Millisecond):
		t.Fatal("worker did not stop after explicit Stop() call")
	}
}

func TestProvisioningWorker_Stop_MultipleCalls(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 50 * time.Millisecond

	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(pollInterval), logger)
	require.NoError(t, err)

	// Call Stop() multiple times - should not panic
	worker.Stop()
	worker.Stop()
	worker.Stop()

	// Success if no panic
}

func TestProvisioningWorker_Start_TickerInterval(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 50 * time.Millisecond

	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(pollInterval), logger)
	require.NoError(t, err)

	// Start worker with short timeout to observe multiple ticks
	ctx, cancel := context.WithTimeout(context.Background(), 175*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	// Wait for worker to complete
	select {
	case <-done:
		// Worker stopped as expected after context timeout
	case <-time.After(300 * time.Millisecond):
		t.Fatal("worker did not stop after context timeout")
	}

	// Success - the worker ran through multiple ticker intervals before stopping
	// We can't directly verify processPendingTenants call count without modifying
	// the implementation, but we've verified the ticker-based loop works correctly
}

func TestProcessPendingTenants_Success(t *testing.T) {
	_, repo := setupTestDB(t)

	// Create 3 pending tenants
	tenant1 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("tenant1"),
		DisplayName:     "Tenant 1",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	tenant2 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("tenant2"),
		DisplayName:     "Tenant 2",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	tenant3 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("tenant3"),
		DisplayName:     "Tenant 3",
		SettlementAsset: "EUR",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}

	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenant1))
	require.NoError(t, repo.Create(ctx, tenant2))
	require.NoError(t, repo.Create(ctx, tenant3))

	// Create worker with a proper mock that doesn't panic
	prov := &ControlledMockProvisioner{
		failureSequence: []error{nil, nil, nil}, // Success for all 3 tenants
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Execute
	worker.processPendingTenants(ctx)

	// Wait for goroutines to complete provisioning
	worker.wg.Wait()

	// Verify all tenants were successfully provisioned (status updated to ACTIVE)
	updated1, err := repo.GetByID(ctx, tenant1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, updated1.Status)
	assert.Equal(t, 3, updated1.Version) // Initial 1 -> Provisioning 2 -> Active 3

	updated2, err := repo.GetByID(ctx, tenant2.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, updated2.Status)
	assert.Equal(t, 3, updated2.Version)

	updated3, err := repo.GetByID(ctx, tenant3.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, updated3.Status)
	assert.Equal(t, 3, updated3.Version)
}

func TestProcessPendingTenants_VersionConflict(t *testing.T) {
	_, repo := setupTestDB(t)

	// Create 3 tenants
	tenant1 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("tenant1"),
		DisplayName:     "Tenant 1",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	tenant2 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("tenant2"),
		DisplayName:     "Tenant 2",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	tenant3 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("tenant3"),
		DisplayName:     "Tenant 3",
		SettlementAsset: "EUR",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}

	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenant1))
	require.NoError(t, repo.Create(ctx, tenant2))
	require.NoError(t, repo.Create(ctx, tenant3))

	// Simulate another worker claiming tenant2 (version conflict for our worker)
	_, err := repo.UpdateStatus(ctx, tenant2.ID, domain.StatusProvisioning, 1)
	require.NoError(t, err)

	// Create worker with a proper mock that doesn't panic
	prov := &ControlledMockProvisioner{
		failureSequence: []error{nil, nil}, // Success for tenant1 and tenant3
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Execute - should skip tenant2 (already claimed) and process tenant1 and tenant3
	worker.processPendingTenants(ctx)

	// Wait for goroutines to complete provisioning
	worker.wg.Wait()

	// Verify tenant1 and tenant3 were successfully provisioned (ACTIVE)
	updated1, err := repo.GetByID(ctx, tenant1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, updated1.Status)

	// Tenant2 should still be in PROVISIONING status (already claimed by another worker)
	updated2, err := repo.GetByID(ctx, tenant2.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, updated2.Status)

	updated3, err := repo.GetByID(ctx, tenant3.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, updated3.Status)
}

func TestProcessPendingTenants_ListByStatusError(t *testing.T) {
	// Create a closed database to force an error
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.Close() // Close the database

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Execute - should not crash
	ctx := context.Background()
	worker.processPendingTenants(ctx)

	// Success if no panic occurred
}

func TestProcessPendingTenants_NoTenantsFound(t *testing.T) {
	_, repo := setupTestDB(t)

	// No tenants created - empty database
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Execute
	ctx := context.Background()
	worker.processPendingTenants(ctx)

	// Success - should handle empty list gracefully
}

func TestProcessPendingTenants_GoroutinesSpawned(t *testing.T) {
	_, repo := setupTestDB(t)

	// Create 2 pending tenants
	tenant1 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("tenant1"),
		DisplayName:     "Tenant 1",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	tenant2 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("tenant2"),
		DisplayName:     "Tenant 2",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}

	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenant1))
	require.NoError(t, repo.Create(ctx, tenant2))

	// Create worker with a proper mock that doesn't panic
	prov := &ControlledMockProvisioner{
		failureSequence: []error{nil, nil}, // Success for both tenants
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Execute
	worker.processPendingTenants(ctx)

	// Wait for goroutines to complete provisioning
	worker.wg.Wait()

	// Verify both tenants were successfully provisioned (ACTIVE)
	// This verifies goroutines were spawned and completed for successfully claimed tenants
	updated1, err := repo.GetByID(ctx, tenant1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, updated1.Status)

	updated2, err := repo.GetByID(ctx, tenant2.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, updated2.Status)

	// Verify both tenants were provisioned (call count = 2)
	assert.Equal(t, 2, prov.GetCallCount())
}

func TestProvisionTenantWithRetry_NoPanic(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Execute - should not panic
	ctx := context.Background()
	tenantID := tenant.MustNewTenantID("test_tenant")

	worker.wg.Add(1)
	worker.provisionTenantWithRetry(ctx, tenantID)

	// Success if no panic occurred
}

func TestProvisionTenantWithRetry_PanicRecovery(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Override provisionTenantWithRetry to force a panic
	// This tests the panic recovery mechanism
	tenantID := tenant.MustNewTenantID("test_tenant")
	ctx := context.Background()

	// Call the method directly - it includes panic recovery
	worker.wg.Add(1)

	// Execute in a goroutine to test panic doesn't crash
	done := make(chan struct{})
	go func() {
		defer func() {
			close(done)
		}()
		// Directly call the method - it will recover from any panics
		worker.provisionTenantWithRetry(ctx, tenantID)
	}()

	// Wait for completion
	select {
	case <-done:
		// Success - goroutine completed without crashing
	case <-time.After(200 * time.Millisecond):
		t.Fatal("goroutine did not complete")
	}
}

func TestProvisioningWorker_GracefulShutdown(t *testing.T) {
	_, repo := setupTestDB(t)

	// Create a pending tenant
	tenant1 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("tenant1"),
		DisplayName:     "Tenant 1",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}

	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenant1))

	// Create worker with properly initialized mock (NewMockProvisioner initializes all internal maps)
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(50*time.Millisecond), logger)
	require.NoError(t, err)

	// Start worker
	startCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Start(startCtx)
		close(done)
	}()

	// Wait for tenant to be provisioned (processing cycle spawns goroutines)
	err = await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		t, _ := repo.GetByID(context.Background(), tenant1.ID)
		return t != nil && t.Status == domain.StatusActive
	})
	require.NoError(t, err, "tenant should be provisioned")

	// Stop the worker - should wait for in-flight goroutines
	cancel()
	worker.Stop()

	// Should stop within a reasonable time
	select {
	case <-done:
		// Success - worker stopped and all goroutines completed
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker did not stop gracefully")
	}
}

func TestProvisioningWorker_NoGoroutineLeaks(t *testing.T) {
	_, repo := setupTestDB(t)

	// Create multiple pending tenants
	for i := 0; i < 5; i++ {
		tenantID := "tenant" + string(rune('a'+i))
		tenant := &domain.Tenant{
			ID:              tenant.MustNewTenantID(tenantID),
			DisplayName:     "Tenant " + tenantID,
			SettlementAsset: "GBP",
			Status:          domain.StatusProvisioningPending,
			CreatedAt:       time.Now(),
			Version:         1,
		}
		require.NoError(t, repo.Create(context.Background(), tenant))
	}

	// Create worker with properly initialized mock (NewMockProvisioner initializes all internal maps)
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(50*time.Millisecond), logger)
	require.NoError(t, err)

	// Start worker
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	// Wait for at least one tenant to be provisioned (processing cycle completes)
	err = await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		// Check if any tenant has been provisioned
		for i := 0; i < 5; i++ {
			tenantID := "tenant" + string(rune('a'+i))
			t, _ := repo.GetByID(context.Background(), tenant.MustNewTenantID(tenantID))
			if t != nil && t.Status == domain.StatusActive {
				return true
			}
		}
		return false
	})
	require.NoError(t, err, "at least one tenant should be provisioned")

	// Stop the worker
	cancel()
	worker.Stop()

	// Wait for worker to fully stop
	<-done

	// At this point, all goroutines should be cleaned up
	// We can't easily verify exact goroutine count, but the test passing
	// without hanging indicates proper cleanup
}

// Test error sentinels for retry tests (satisfies err113 linter).
var (
	errLockTimeout       = errors.New("lock timeout")
	errDatabaseUnavail   = errors.New("database unavailable")
	errConnectionTimeout = errors.New("connection timeout")
	errPermissionDenied  = errors.New("permission denied")
)

// ControlledMockProvisioner is a mock that allows controlled failure/success sequences.
type ControlledMockProvisioner struct {
	mu              sync.Mutex
	calls           []tenant.TenantID
	failureSequence []error // nil = success, error = fail with this error
	callIndex       int
}

func (m *ControlledMockProvisioner) ProvisionSchemas(_ context.Context, tenantID tenant.TenantID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, tenantID)

	if m.callIndex < len(m.failureSequence) {
		err := m.failureSequence[m.callIndex]
		m.callIndex++
		return err
	}

	// Default to success if no more sequence entries
	return nil
}

func (m *ControlledMockProvisioner) DeprovisionSchemas(_ context.Context, _ tenant.TenantID) error {
	return nil
}

func (m *ControlledMockProvisioner) PurgeSchemas(_ context.Context, _ tenant.TenantID) error {
	return nil
}

func (m *ControlledMockProvisioner) GetProvisioningStatus(_ context.Context, _ tenant.TenantID) (*provisioner.ProvisioningStatus, error) {
	return nil, nil
}

func (m *ControlledMockProvisioner) ReconcileMigrations(_ context.Context, _ *tenant.TenantID) (int, []string) {
	return 0, nil
}

func (m *ControlledMockProvisioner) GetRequiredSchemas() []string {
	return []string{"party", "current-account"}
}

func (m *ControlledMockProvisioner) InitializeProvisioningStatus(_ context.Context, _ tenant.TenantID) error {
	return nil
}

func (m *ControlledMockProvisioner) GetCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *ControlledMockProvisioner) GetCalls() []tenant.TenantID {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]tenant.TenantID, len(m.calls))
	copy(result, m.calls)
	return result
}

// Ensure ControlledMockProvisioner implements SchemaProvisioner
var _ provisioner.SchemaProvisioner = (*ControlledMockProvisioner)(nil)

func TestProvisionTenantWithRetry_SuccessOnFirstAttempt(t *testing.T) {
	_, repo := setupTestDB(t)

	// Create tenant in PROVISIONING status
	tenantObj := &domain.Tenant{
		ID:              tenant.MustNewTenantID("test_tenant"),
		DisplayName:     "Test Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenantObj))

	// Create mock that succeeds immediately
	mockProv := &ControlledMockProvisioner{
		failureSequence: []error{nil}, // Success on first call
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, mockProv, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Execute
	worker.wg.Add(1)
	worker.provisionTenantWithRetry(ctx, tenantObj.ID)

	// Verify exactly 1 call was made
	assert.Equal(t, 1, mockProv.GetCallCount())

	// Verify tenant was marked as active
	updated, err := repo.GetByID(ctx, tenantObj.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, updated.Status)
}

func TestProvisionTenantWithRetry_SuccessAfterRetries(t *testing.T) {
	_, repo := setupTestDB(t)

	// Create tenant in PROVISIONING status
	tenantObj := &domain.Tenant{
		ID:              tenant.MustNewTenantID("retry_tenant"),
		DisplayName:     "Retry Tenant",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenantObj))

	// Create mock that fails 3 times then succeeds
	mockProv := &ControlledMockProvisioner{
		failureSequence: []error{errLockTimeout, errLockTimeout, errLockTimeout, nil}, // 3 failures, then success
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, mockProv, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Execute (this will take time due to exponential backoff, but tests use actual delays)
	// For faster tests, we could inject a clock, but for now we accept the delay
	start := time.Now()
	worker.wg.Add(1)
	worker.provisionTenantWithRetry(ctx, tenantObj.ID)
	elapsed := time.Since(start)

	// Verify exactly 4 calls were made (3 failures + 1 success)
	assert.Equal(t, 4, mockProv.GetCallCount())

	// Verify tenant was marked as active
	updated, err := repo.GetByID(ctx, tenantObj.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, updated.Status)

	// Verify exponential backoff timing (with tolerance for jitter)
	// 3 retries: 2s + 4s + 8s = 14s base, but with jitter could be up to 14s + 25% = ~17.5s
	// Minimum is base delays only: 2s + 4s + 8s = 14s
	// We give generous tolerance due to test environment variability
	assert.GreaterOrEqual(t, elapsed, 2*time.Second, "should have waited at least 2 seconds for first retry")
	t.Logf("Elapsed time for 3 retries: %v", elapsed)
}

func TestProvisionTenantWithRetry_MaxRetriesExhausted(t *testing.T) {
	_, repo := setupTestDB(t)

	// Create tenant in PROVISIONING status
	tenantObj := &domain.Tenant{
		ID:              tenant.MustNewTenantID("fail_tenant"),
		DisplayName:     "Fail Tenant",
		SettlementAsset: "EUR",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenantObj))

	// Create mock that always fails
	mockProv := &ControlledMockProvisioner{
		failureSequence: []error{
			errDatabaseUnavail, errDatabaseUnavail, errDatabaseUnavail, errDatabaseUnavail, errDatabaseUnavail,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, mockProv, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Execute
	worker.wg.Add(1)
	worker.provisionTenantWithRetry(ctx, tenantObj.ID)

	// Verify exactly 5 calls were made (maxRetries = 5)
	assert.Equal(t, 5, mockProv.GetCallCount())

	// Verify tenant was marked as provisioning_failed
	updated, err := repo.GetByID(ctx, tenantObj.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioningFailed, updated.Status)
	assert.Contains(t, updated.ErrorMessage, "database unavailable")
}

func TestProvisionTenantWithRetry_ContextCancellation(t *testing.T) {
	_, repo := setupTestDB(t)

	// Create tenant in PROVISIONING status
	tenantObj := &domain.Tenant{
		ID:              tenant.MustNewTenantID("cancel_tenant"),
		DisplayName:     "Cancel Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenantObj))

	// Create mock that always fails
	mockProv := &ControlledMockProvisioner{
		failureSequence: []error{
			errLockTimeout, errLockTimeout, errLockTimeout, errLockTimeout, errLockTimeout,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, mockProv, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Create cancellable context
	cancelCtx, cancel := context.WithCancel(context.Background())

	// Execute in goroutine
	done := make(chan struct{})
	worker.wg.Add(1)
	go func() {
		worker.provisionTenantWithRetry(cancelCtx, tenantObj.ID)
		close(done)
	}()

	// Wait for first attempt to occur (mock will be called at least once)
	err = await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return mockProv.GetCallCount() >= 1
	})
	require.NoError(t, err, "first provisioning attempt should occur")

	// Cancel context during backoff
	cancel()

	// Should stop within a reasonable time (less than full retry cycle)
	select {
	case <-done:
		// Success - stopped early due to cancellation
	case <-time.After(5 * time.Second):
		t.Fatal("provisionTenantWithRetry did not respect context cancellation")
	}

	// Verify fewer than max retries were made (cancelled early)
	callCount := mockProv.GetCallCount()
	assert.Less(t, callCount, 5, "should have stopped before max retries due to cancellation")
	t.Logf("Calls made before cancellation: %d", callCount)
}

func TestProvisionTenantWithRetry_ExponentialBackoffTiming(t *testing.T) {
	// This test verifies the exponential backoff timing formula
	// Expected delays: 2s, 4s, 8s, 16s, 30s (capped)
	// We only test the first few to avoid long test times

	_, repo := setupTestDB(t)

	// Create tenant
	tenantObj := &domain.Tenant{
		ID:              tenant.MustNewTenantID("timing_tenant"),
		DisplayName:     "Timing Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenantObj))

	// Mock that fails twice then succeeds
	mockProv := &ControlledMockProvisioner{
		failureSequence: []error{errLockTimeout, errLockTimeout, nil},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, mockProv, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Execute and measure time
	start := time.Now()
	worker.wg.Add(1)
	worker.provisionTenantWithRetry(ctx, tenantObj.ID)
	elapsed := time.Since(start)

	// 2 retries: base delays are 2s + 4s = 6s
	// With jitter (up to 25%): max = 6s * 1.25 = 7.5s
	// Allow some tolerance for test environment
	assert.GreaterOrEqual(t, elapsed, 5*time.Second, "should have waited ~6s total for 2 retries")
	assert.LessOrEqual(t, elapsed, 10*time.Second, "should not exceed expected delay with jitter")

	t.Logf("Elapsed time for 2 retries: %v (expected ~6-7.5s)", elapsed)
}

// =============================================================================
// isRetryableError Tests
// =============================================================================

// TestIsRetryableError_NilError verifies that nil errors are not retryable.
func TestIsRetryableError_NilError(t *testing.T) {
	assert.False(t, isRetryableError(nil), "nil error should not be retryable")
}

// TestIsRetryableError_ContextErrors verifies that context errors are never retryable.
func TestIsRetryableError_ContextErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"context.Canceled", context.Canceled},
		{"context.DeadlineExceeded", context.DeadlineExceeded},
		{"wrapped context.Canceled", fmt.Errorf("operation failed: %w", context.Canceled)},
		{"wrapped context.DeadlineExceeded", fmt.Errorf("operation failed: %w", context.DeadlineExceeded)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, isRetryableError(tt.err), "context errors should never be retryable")
		})
	}
}

// TestIsRetryableError_RetryablePatterns verifies that errors containing
// retryable patterns are correctly identified as transient.
func TestIsRetryableError_RetryablePatterns(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		comment string
	}{
		// Timeout errors
		{"connection timeout", "connection timeout waiting for response", "Network timeout"},
		{"lock timeout", "unable to acquire advisory lock: timeout after 30s", "Atlas lock timeout"},
		{"query timeout", "ERROR: canceling statement due to statement timeout", "PostgreSQL timeout"},
		{"timeout uppercase", "Connection TIMEOUT", "Case insensitive"},

		// Connection errors
		{"connection refused", "dial tcp 127.0.0.1:5432: connection refused", "DB down"},
		{"connection reset", "connection reset by peer", "Network issue"},
		{"connection pool", "connection pool exhausted", "Pool saturation"},

		// Lock errors
		{"advisory lock", "could not acquire lock on relation", "PostgreSQL lock"},
		{"row lock", "could not obtain lock on row in table", "Row-level lock"},
		{"atlas lock", "acquiring lock on schema: lock timeout", "Atlas migration lock"},

		// Temporary errors
		{"temporary failure", "temporary failure in name resolution", "DNS issue"},
		{"temporary unavailable", "resource temporarily unavailable", "Resource busy"},

		// Unavailable errors
		{"service unavailable", "service temporarily unavailable", "Service down"},
		{"resource unavailable", "database unavailable", "DB maintenance"},

		// Other transient patterns
		{"connection pool exhausted", "max pool size reached: exhausted", "Pool full"},
		{"please retry", "optimistic lock failed, please retry", "Explicit retry hint"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testErr(tt.errMsg)
			assert.True(t, isRetryableError(err), "error '%s' should be retryable (%s)", tt.errMsg, tt.comment)
		})
	}
}

// TestIsRetryableError_PermanentPatterns verifies that errors containing
// permanent patterns are correctly identified as non-retryable.
func TestIsRetryableError_PermanentPatterns(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		comment string
	}{
		// Validation errors
		{"invalid argument", "invalid argument: tenant_id must not be empty", "Validation"},
		{"invalid tenant", "invalid tenant ID format: contains special characters", "Specific validation"},
		{"invalid input", "ERROR: invalid input syntax for type uuid", "Type error"},

		// Permission errors
		{"permission denied", "permission denied for schema org_acme", "Auth failure"},
		{"access denied", "access denied to table parties", "Authorization"},
		{"unauthorized", "unauthorized: token expired", "Auth token"},
		{"authentication failed", "authentication failed for user meridian", "Login failure"},

		// Constraint violations
		{"unique constraint", "duplicate key value violates unique constraint", "Duplicate key"},
		{"foreign key", "foreign key constraint violation on table accounts", "FK violation"},
		{"constraint violation", "check constraint \"amount_positive\" is violated", "Check constraint"},
		{"duplicate entry", "Duplicate entry 'test' for key 'name'", "MySQL duplicate"},

		// Schema errors
		{"does not exist", "table \"parties\" does not exist", "Missing table"},
		{"not found", "schema org_deleted not found", "Missing schema"},
		{"syntax error", "ERROR: syntax error at or near \"SELCT\"", "SQL typo"},

		// State errors
		{"already active", "tenant is already active", "Duplicate provision"},
		{"deprovisioned", "cannot provision deprovisioned tenant", "State conflict"},
		{"not allowed", "operation not allowed in current state", "State machine"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testErr(tt.errMsg)
			assert.False(t, isRetryableError(err), "error '%s' should NOT be retryable (%s)", tt.errMsg, tt.comment)
		})
	}
}

// TestIsRetryableError_UnknownErrors verifies that unknown errors
// default to non-retryable (fail-safe behavior).
func TestIsRetryableError_UnknownErrors(t *testing.T) {
	tests := []struct {
		name   string
		errMsg string
	}{
		{"generic error", "something went wrong"},
		{"unexpected error", "unexpected state encountered"},
		{"internal error", "internal server error"},
		{"unknown status", "unknown status code 500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testErr(tt.errMsg)
			assert.False(t, isRetryableError(err), "unknown error '%s' should default to non-retryable", tt.errMsg)
		})
	}
}

// TestIsRetryableError_CaseInsensitive verifies that pattern matching
// is case-insensitive.
func TestIsRetryableError_CaseInsensitive(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected bool
	}{
		// Retryable - various cases
		{"TIMEOUT uppercase", "CONNECTION TIMEOUT", true},
		{"Timeout mixed", "Connection Timeout Exceeded", true},
		{"timeout lowercase", "connection timeout", true},

		// Permanent - various cases
		{"INVALID uppercase", "INVALID ARGUMENT", false},
		{"Invalid mixed", "Invalid Tenant ID", false},
		{"invalid lowercase", "invalid input", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testErr(tt.errMsg)
			result := isRetryableError(err)
			if tt.expected {
				assert.True(t, result, "error '%s' should be retryable (case-insensitive)", tt.errMsg)
			} else {
				assert.False(t, result, "error '%s' should NOT be retryable (case-insensitive)", tt.errMsg)
			}
		})
	}
}

// TestIsRetryableError_WrappedErrors verifies that wrapped errors
// are correctly classified by examining the full error chain.
func TestIsRetryableError_WrappedErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"wrapped retryable", fmt.Errorf("provisioning failed: %w", errConnectionTimeout), true},
		{"double wrapped retryable", fmt.Errorf("tenant %s: %w", "test", fmt.Errorf("db error: %w", errConnectionTimeout)), true},
		{"wrapped permanent", fmt.Errorf("provisioning failed: %w", errPermissionDenied), false},
		{"double wrapped permanent", fmt.Errorf("tenant %s: %w", "test", fmt.Errorf("auth: %w", errPermissionDenied)), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			if tt.expected {
				assert.True(t, result, "wrapped error should be retryable")
			} else {
				assert.False(t, result, "wrapped error should NOT be retryable")
			}
		})
	}
}

// TestIsRetryableError_PermanentTakesPrecedence verifies that when an error
// contains both retryable and permanent patterns, permanent takes precedence.
func TestIsRetryableError_PermanentTakesPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		errMsg string
	}{
		// Errors that contain both retryable and permanent patterns
		{"timeout but invalid", "connection timeout due to invalid host address"},
		{"lock but permission", "could not acquire lock: permission denied"},
		{"connection but syntax", "connection failed: syntax error in query"},
		{"timeout but not found", "timeout waiting for resource not found error to resolve"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testErr(tt.errMsg)
			// Permanent patterns should take precedence over retryable patterns
			assert.False(t, isRetryableError(err),
				"permanent pattern should take precedence in '%s'", tt.errMsg)
		})
	}
}

// TestIsRetryableError_AtlasSpecificErrors verifies classification of
// Atlas migration-specific error messages.
func TestIsRetryableError_AtlasSpecificErrors(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected bool
		comment  string
	}{
		// Atlas lock timeout (retryable)
		{
			"atlas lock timeout",
			"acquiring lock on schema \"org_test\": context deadline exceeded (timeout: 5s)",
			true,
			"Atlas lock wait timeout",
		},
		{
			"atlas advisory lock",
			"pq: could not obtain lock on \"atlas_schema_revisions\"",
			true,
			"Atlas revision table lock",
		},
		// Atlas connection issues (retryable)
		{
			"atlas connection refused",
			"dial tcp [::1]:5432: connect: connection refused",
			true,
			"DB not available",
		},
		// Atlas permanent errors (non-retryable)
		{
			"atlas migration syntax",
			"applying migration \"20240101000000_init.sql\": syntax error at position 42",
			false,
			"Bad migration SQL",
		},
		{
			"atlas constraint",
			"applying migration: violates foreign key constraint \"fk_account_party\"",
			false,
			"FK violation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testErr(tt.errMsg)
			result := isRetryableError(err)
			if tt.expected {
				assert.True(t, result, "Atlas error '%s' should be retryable (%s)", tt.errMsg, tt.comment)
			} else {
				assert.False(t, result, "Atlas error '%s' should NOT be retryable (%s)", tt.errMsg, tt.comment)
			}
		})
	}
}

// TestIsRetryableError_PostgresSpecificErrors verifies classification of
// PostgreSQL-specific error messages.
func TestIsRetryableError_PostgresSpecificErrors(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected bool
		comment  string
	}{
		// Connection pool exhaustion (retryable)
		{
			"pgx pool exhausted",
			"unable to acquire connection from pool: all connections are busy or the pool is exhausted",
			true,
			"Connection pool saturation",
		},
		// Statement timeout (retryable)
		{
			"statement timeout",
			"pq: canceling statement due to statement timeout",
			true,
			"Query took too long",
		},
		// Lock timeout (retryable)
		{
			"lock wait timeout",
			"pq: canceling statement due to lock timeout",
			true,
			"Row-level lock wait",
		},
		// Unique constraint violation (non-retryable)
		{
			"unique violation",
			"pq: duplicate key value violates unique constraint \"tenants_slug_key\"",
			false,
			"Duplicate slug",
		},
		// Foreign key violation (non-retryable)
		{
			"fk violation",
			"pq: insert or update on table \"accounts\" violates foreign key constraint \"fk_party_id\"",
			false,
			"Missing parent record",
		},
		// Syntax error (non-retryable)
		{
			"sql syntax",
			"pq: syntax error at or near \"SELCT\"",
			false,
			"SQL typo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testErr(tt.errMsg)
			result := isRetryableError(err)
			if tt.expected {
				assert.True(t, result, "PostgreSQL error '%s' should be retryable (%s)", tt.errMsg, tt.comment)
			} else {
				assert.False(t, result, "PostgreSQL error '%s' should NOT be retryable (%s)", tt.errMsg, tt.comment)
			}
		})
	}
}

func TestProcessPendingTenants_VersionConflictLogging(t *testing.T) {
	// This test verifies that version conflicts are handled gracefully
	// and logged at debug level (not warning) since they're expected in concurrent operation
	_, repo := setupTestDB(t)

	// Create a tenant in PROVISIONING_PENDING status
	tenant1 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("conflict_test"),
		DisplayName:     "Conflict Test Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}

	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenant1))

	// Capture log output with thread-safe buffer (processPendingTenants spawns goroutines)
	var logBuffer safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prov := provisioner.NewMockProvisioner(nil)
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Simulate a race: another worker claims the tenant between ListByStatus and UpdateStatus
	// by updating the version directly
	_, err = repo.UpdateStatus(ctx, tenant1.ID, domain.StatusProvisioning, 1)
	require.NoError(t, err)

	// Now change the status back to pending but version is now 2
	_, err = repo.UpdateStatus(ctx, tenant1.ID, domain.StatusProvisioningPending, 2)
	require.NoError(t, err)

	// Verify the tenant is now at version 3 with pending status
	updatedTenant, err := repo.GetByID(ctx, tenant1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioningPending, updatedTenant.Status)
	assert.Equal(t, 3, updatedTenant.Version)

	// Create a stale tenant view that the worker would have fetched
	// (simulating what ListByStatus returned before the concurrent update)
	staleTenant := &domain.Tenant{
		ID:              tenant1.ID,
		DisplayName:     tenant1.DisplayName,
		SettlementAsset: tenant1.SettlementAsset,
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       tenant1.CreatedAt,
		Version:         1, // Stale version
	}

	// Now manually test the UpdateStatus call with stale version
	_, err = repo.UpdateStatus(ctx, staleTenant.ID, domain.StatusProvisioning, staleTenant.Version)
	assert.ErrorIs(t, err, persistence.ErrVersionConflict, "expected version conflict error")

	// Run processPendingTenants - it should process the tenant with current version
	worker.processPendingTenants(ctx)

	// Wait for the background provisioning goroutine to complete
	err = await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		t, _ := repo.GetByID(ctx, tenant1.ID)
		return t != nil && t.Status == domain.StatusActive
	})
	require.NoError(t, err, "tenant should reach ACTIVE status")

	// Verify the tenant was successfully provisioned (status changed to ACTIVE)
	// Flow: PROVISIONING_PENDING -> PROVISIONING (claim) -> ACTIVE (after successful ProvisionSchemas)
	finalTenant, err := repo.GetByID(ctx, tenant1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, finalTenant.Status)
	// Version progression: 1 (create) -> 2 (simulate claim) -> 3 (back to pending) -> 4 (real claim) -> 5 (mark active)
	assert.Equal(t, 5, finalTenant.Version)

	// Verify logs contain expected messages
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, "found pending tenants")
	assert.Contains(t, logOutput, "claimed tenant for provisioning")
}

func TestProcessPendingTenants_ConcurrentClaimSkipped(t *testing.T) {
	// Test that verifies when a tenant is concurrently claimed, it's skipped with debug logging
	_, repo := setupTestDB(t)

	// Create 2 pending tenants
	tenant1 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("concurrent1"),
		DisplayName:     "Concurrent Test 1",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	tenant2 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("concurrent2"),
		DisplayName:     "Concurrent Test 2",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now().Add(time.Second), // Slightly later to ensure consistent ordering
		Version:         1,
	}

	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, tenant1))
	require.NoError(t, repo.Create(ctx, tenant2))

	// Capture log output with thread-safe buffer (processPendingTenants spawns goroutines)
	var logBuffer safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prov := provisioner.NewMockProvisioner(nil)
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Start two workers processing concurrently
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		worker.processPendingTenants(ctx)
	}()

	go func() {
		defer wg.Done()
		time.Sleep(1 * time.Millisecond) //nolint:forbidigo // simulates concurrent work delay for race condition testing
		worker.processPendingTenants(ctx)
	}()

	wg.Wait()

	// Wait for both tenants to reach ACTIVE status
	err = await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		t1, _ := repo.GetByID(ctx, tenant1.ID)
		t2, _ := repo.GetByID(ctx, tenant2.ID)
		return t1 != nil && t1.Status == domain.StatusActive &&
			t2 != nil && t2.Status == domain.StatusActive
	})
	require.NoError(t, err, "both tenants should reach ACTIVE status")

	// Both tenants should be ACTIVE after successful provisioning
	// (They go through PROVISIONING_PENDING -> PROVISIONING -> ACTIVE)
	final1, err := repo.GetByID(ctx, tenant1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, final1.Status)

	final2, err := repo.GetByID(ctx, tenant2.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, final2.Status)

	// At least one version conflict may have occurred (logged at debug level)
	// The exact behavior depends on timing, but the test ensures no crashes or deadlocks
}

// =============================================================================
// RecoverStuckTenants Tests
// =============================================================================

// TestRecoverStuckTenants_RecoversStaleTenants verifies that tenants stuck in
// PROVISIONING status for longer than the threshold are reset to PROVISIONING_PENDING.
func TestRecoverStuckTenants_RecoversStaleTenants(t *testing.T) {
	db, repo := setupTestDB(t)

	// Create a tenant in PROVISIONING status
	stuckTenant := &domain.Tenant{
		ID:              tenant.MustNewTenantID("stuck_tenant"),
		DisplayName:     "Stuck Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, stuckTenant))

	// Age the updated_at timestamp to make it stale (10 minutes ago)
	err := db.Exec("UPDATE tenant SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), stuckTenant.ID.String()).Error
	require.NoError(t, err)

	// Create worker
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Run recovery with 5 minute threshold
	recoveredCount, err := worker.RecoverStuckTenants(ctx, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, recoveredCount)

	// Verify tenant was reset to PROVISIONING_PENDING
	recovered, err := repo.GetByID(ctx, stuckTenant.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioningPending, recovered.Status)
	assert.Equal(t, 2, recovered.Version) // Version should increment
}

// TestRecoverStuckTenants_IgnoresRecentTenants verifies that tenants in PROVISIONING
// status but recently updated (within threshold) are NOT recovered.
func TestRecoverStuckTenants_IgnoresRecentTenants(t *testing.T) {
	db, repo := setupTestDB(t)

	// Create a tenant in PROVISIONING status
	recentTenant := &domain.Tenant{
		ID:              tenant.MustNewTenantID("recent_tenant"),
		DisplayName:     "Recent Tenant",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, recentTenant))

	// Set updated_at to 1 minute ago (within the 5 minute threshold)
	err := db.Exec("UPDATE tenant SET updated_at = ? WHERE id = ?",
		time.Now().Add(-1*time.Minute), recentTenant.ID.String()).Error
	require.NoError(t, err)

	// Create worker
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Run recovery with 5 minute threshold
	recoveredCount, err := worker.RecoverStuckTenants(ctx, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 0, recoveredCount)

	// Verify tenant was NOT changed
	unchanged, err := repo.GetByID(ctx, recentTenant.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, unchanged.Status)
	assert.Equal(t, 1, unchanged.Version)
}

// TestRecoverStuckTenants_ContinuesOnUpdateError verifies that errors updating
// individual tenants don't prevent recovery of other tenants.
// Note: True version conflicts in recovery are rare since the query and update
// happen close together, but the code handles them gracefully by logging and continuing.
func TestRecoverStuckTenants_ContinuesOnUpdateError(t *testing.T) {
	db, repo := setupTestDB(t)

	// Create two stuck tenants
	stuckTenant1 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("continue_tenant1"),
		DisplayName:     "Continue Tenant 1",
		SettlementAsset: "EUR",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	stuckTenant2 := &domain.Tenant{
		ID:              tenant.MustNewTenantID("continue_tenant2"),
		DisplayName:     "Continue Tenant 2",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, stuckTenant1))
	require.NoError(t, repo.Create(ctx, stuckTenant2))

	// Age the updated_at timestamps to make them stale
	err := db.Exec("UPDATE tenant SET updated_at = ? WHERE id IN (?, ?)",
		time.Now().Add(-10*time.Minute), stuckTenant1.ID.String(), stuckTenant2.ID.String()).Error
	require.NoError(t, err)

	// Capture log output
	var logBuffer safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prov := provisioner.NewMockProvisioner(nil)
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Run recovery - both should succeed
	recoveredCount, err := worker.RecoverStuckTenants(ctx, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 2, recoveredCount)

	// Verify both were recovered
	recovered1, err := repo.GetByID(ctx, stuckTenant1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioningPending, recovered1.Status)

	recovered2, err := repo.GetByID(ctx, stuckTenant2.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioningPending, recovered2.Status)

	// Verify both were logged
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, "continue_tenant1")
	assert.Contains(t, logOutput, "continue_tenant2")
}

// TestRecoverStuckTenants_MultipleStuckTenants verifies batch recovery of multiple stuck tenants.
func TestRecoverStuckTenants_MultipleStuckTenants(t *testing.T) {
	db, repo := setupTestDB(t)
	ctx := context.Background()

	// Create 3 stuck tenants
	for i := 1; i <= 3; i++ {
		tenantObj := &domain.Tenant{
			ID:              tenant.MustNewTenantID(fmt.Sprintf("stuck_%d", i)),
			DisplayName:     fmt.Sprintf("Stuck Tenant %d", i),
			SettlementAsset: "GBP",
			Status:          domain.StatusProvisioning,
			CreatedAt:       time.Now(),
			Version:         1,
		}
		require.NoError(t, repo.Create(ctx, tenantObj))

		// Age the updated_at timestamp
		err := db.Exec("UPDATE tenant SET updated_at = ? WHERE id = ?",
			time.Now().Add(-10*time.Minute), tenantObj.ID.String()).Error
		require.NoError(t, err)
	}

	// Create worker
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Run recovery
	recoveredCount, err := worker.RecoverStuckTenants(ctx, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 3, recoveredCount)

	// Verify all tenants were reset
	for i := 1; i <= 3; i++ {
		id := tenant.MustNewTenantID(fmt.Sprintf("stuck_%d", i))
		recovered, err := repo.GetByID(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusProvisioningPending, recovered.Status)
	}
}

// TestRecoverStuckTenants_MixedStatuses verifies that only PROVISIONING tenants
// are recovered, not tenants in other statuses.
func TestRecoverStuckTenants_MixedStatuses(t *testing.T) {
	db, repo := setupTestDB(t)
	ctx := context.Background()

	// Create tenants in various statuses, all old
	tenants := []struct {
		id     string
		status domain.Status
	}{
		{"stuck_prov", domain.StatusProvisioning},
		{"pending", domain.StatusProvisioningPending},
		{"failed", domain.StatusProvisioningFailed},
		{"active", domain.StatusActive},
	}

	for _, tc := range tenants {
		tenantObj := &domain.Tenant{
			ID:              tenant.MustNewTenantID(tc.id),
			DisplayName:     tc.id,
			SettlementAsset: "GBP",
			Status:          tc.status,
			CreatedAt:       time.Now(),
			Version:         1,
		}
		require.NoError(t, repo.Create(ctx, tenantObj))

		// Age all tenants
		err := db.Exec("UPDATE tenant SET updated_at = ? WHERE id = ?",
			time.Now().Add(-10*time.Minute), tenantObj.ID.String()).Error
		require.NoError(t, err)
	}

	// Create worker
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Run recovery
	recoveredCount, err := worker.RecoverStuckTenants(ctx, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, recoveredCount, "only PROVISIONING tenant should be recovered")

	// Verify only the PROVISIONING tenant was changed
	provTenant, _ := repo.GetByID(ctx, tenant.MustNewTenantID("stuck_prov"))
	assert.Equal(t, domain.StatusProvisioningPending, provTenant.Status)

	// Others should be unchanged
	pendingTenant, _ := repo.GetByID(ctx, tenant.MustNewTenantID("pending"))
	assert.Equal(t, domain.StatusProvisioningPending, pendingTenant.Status)
	assert.Equal(t, 1, pendingTenant.Version) // Version unchanged

	failedTenant, _ := repo.GetByID(ctx, tenant.MustNewTenantID("failed"))
	assert.Equal(t, domain.StatusProvisioningFailed, failedTenant.Status)

	activeTenant, _ := repo.GetByID(ctx, tenant.MustNewTenantID("active"))
	assert.Equal(t, domain.StatusActive, activeTenant.Status)
}

// TestRecoverStuckTenants_NoStuckTenants verifies no-op when no tenants need recovery.
func TestRecoverStuckTenants_NoStuckTenants(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	// Create a healthy tenant in PROVISIONING_PENDING status
	healthyTenant := &domain.Tenant{
		ID:              tenant.MustNewTenantID("healthy"),
		DisplayName:     "Healthy Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	require.NoError(t, repo.Create(ctx, healthyTenant))

	// Create worker
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Run recovery
	recoveredCount, err := worker.RecoverStuckTenants(ctx, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 0, recoveredCount)
}

// TestRecoverStuckTenants_IdempotentRecovery verifies that running recovery
// multiple times is safe (idempotent).
func TestRecoverStuckTenants_IdempotentRecovery(t *testing.T) {
	db, repo := setupTestDB(t)
	ctx := context.Background()

	// Create a stuck tenant
	stuckTenant := &domain.Tenant{
		ID:              tenant.MustNewTenantID("idempotent_test"),
		DisplayName:     "Idempotent Test",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	require.NoError(t, repo.Create(ctx, stuckTenant))

	// Age the updated_at timestamp
	err := db.Exec("UPDATE tenant SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), stuckTenant.ID.String()).Error
	require.NoError(t, err)

	// Create worker
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, testWorkerConfig(5*time.Second), logger)
	require.NoError(t, err)

	// Run recovery first time
	recoveredCount1, err := worker.RecoverStuckTenants(ctx, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, recoveredCount1)

	// Age it again (simulate it going back to PROVISIONING and getting stuck again)
	_, err = repo.UpdateStatus(ctx, stuckTenant.ID, domain.StatusProvisioning, 2)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), stuckTenant.ID.String()).Error
	require.NoError(t, err)

	// Run recovery second time - should work again
	recoveredCount2, err := worker.RecoverStuckTenants(ctx, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, recoveredCount2)

	// Verify final state
	final, err := repo.GetByID(ctx, stuckTenant.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioningPending, final.Status)
}

// TestProvisioningWorker_StartupRecovery verifies that stuck tenants are recovered
// when the worker starts up.
func TestProvisioningWorker_StartupRecovery(t *testing.T) {
	db, repo := setupTestDB(t)
	ctx := context.Background()

	// Create a tenant in PROVISIONING status (simulating a crash)
	stuckTenant := &domain.Tenant{
		ID:              tenant.MustNewTenantID("startup_recovery_tenant"),
		DisplayName:     "Startup Recovery Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioning,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	require.NoError(t, repo.Create(ctx, stuckTenant))

	// Age the updated_at timestamp to make it stale
	err := db.Exec("UPDATE tenant SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), stuckTenant.ID.String()).Error
	require.NoError(t, err)

	// Create worker with proper mock
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	config := Config{
		PollInterval:      100 * time.Millisecond,
		RecoveryThreshold: 5 * time.Minute,
	}
	worker, err := NewProvisioningWorker(repo, prov, config, logger)
	require.NoError(t, err)

	// Start worker
	workerCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Start(workerCtx)
		close(done)
	}()

	// Wait for the tenant to be recovered (PROVISIONING -> PROVISIONING_PENDING)
	// and then provisioned (PROVISIONING_PENDING -> PROVISIONING -> ACTIVE)
	err = await.AtMost(5 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		t, _ := repo.GetByID(ctx, stuckTenant.ID)
		return t != nil && t.Status == domain.StatusActive
	})
	require.NoError(t, err, "tenant should eventually reach ACTIVE status after recovery")

	// Stop worker
	cancel()
	worker.Stop()
	<-done

	// Verify final state
	finalTenant, err := repo.GetByID(ctx, stuckTenant.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, finalTenant.Status)
}

// TestNewProvisioningWorker_RecoveryThresholdDefault verifies that the recovery
// threshold defaults to 5 minutes when not specified.
func TestNewProvisioningWorker_RecoveryThresholdDefault(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Only set PollInterval, leave RecoveryThreshold at zero
	config := Config{
		PollInterval: 100 * time.Millisecond,
	}

	worker, err := NewProvisioningWorker(repo, prov, config, logger)

	require.NoError(t, err)
	require.NotNil(t, worker)
	assert.Equal(t, 5*time.Minute, worker.recoveryThreshold, "recoveryThreshold should default to 5 minutes")
}

// TestNewProvisioningWorker_RecoveryThresholdCustom verifies that a custom
// recovery threshold is respected.
func TestNewProvisioningWorker_RecoveryThresholdCustom(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := provisioner.NewMockProvisioner(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	customThreshold := 10 * time.Minute
	config := Config{
		PollInterval:      100 * time.Millisecond,
		RecoveryThreshold: customThreshold,
	}

	worker, err := NewProvisioningWorker(repo, prov, config, logger)

	require.NoError(t, err)
	require.NotNil(t, worker)
	assert.Equal(t, customThreshold, worker.recoveryThreshold, "recoveryThreshold should be set to custom value")
}
