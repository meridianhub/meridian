package worker

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDB creates an in-memory database with the tenant table schema.
func setupTestDB(t *testing.T) (*gorm.DB, *persistence.Repository) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

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

	// Create worker
	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)

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
	pollInterval := 5 * time.Second

	worker, err := NewProvisioningWorker(nil, prov, pollInterval, logger)

	assert.ErrorIs(t, err, ErrNilRepository)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NilProvisioner(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 5 * time.Second

	worker, err := NewProvisioningWorker(repo, nil, pollInterval, logger)

	assert.ErrorIs(t, err, ErrNilProvisioner)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NilLogger(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	pollInterval := 5 * time.Second

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, nil)

	assert.ErrorIs(t, err, ErrNilLogger)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_ZeroPollInterval(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	worker, err := NewProvisioningWorker(repo, prov, 0, logger)

	assert.ErrorIs(t, err, ErrInvalidPollInterval)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NegativePollInterval(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	worker, err := NewProvisioningWorker(repo, prov, -5*time.Second, logger)

	assert.ErrorIs(t, err, ErrInvalidPollInterval)
	assert.Nil(t, worker)
}

func TestProvisioningWorker_Start_ContextCancellation(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 50 * time.Millisecond

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)
	require.NoError(t, err)

	// Start worker with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		worker.Start(ctx)
		close(done)
	}()

	// Let it run for a bit
	time.Sleep(100 * time.Millisecond)

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

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)
	require.NoError(t, err)

	// Start worker
	ctx := context.Background()
	done := make(chan struct{})

	go func() {
		worker.Start(ctx)
		close(done)
	}()

	// Let it run for a bit
	time.Sleep(100 * time.Millisecond)

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

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)
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

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)
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

	// Create worker
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, 5*time.Second, logger)
	require.NoError(t, err)

	// Execute
	worker.processPendingTenants(ctx)

	// Give goroutines a moment to spawn
	time.Sleep(50 * time.Millisecond)

	// Verify all tenants were claimed (status updated to PROVISIONING)
	updated1, err := repo.GetByID(ctx, tenant1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, updated1.Status)
	assert.Equal(t, 2, updated1.Version)

	updated2, err := repo.GetByID(ctx, tenant2.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, updated2.Status)
	assert.Equal(t, 2, updated2.Version)

	updated3, err := repo.GetByID(ctx, tenant3.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, updated3.Status)
	assert.Equal(t, 2, updated3.Version)
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

	// Create worker
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, 5*time.Second, logger)
	require.NoError(t, err)

	// Execute - should skip tenant2 (already claimed) and process tenant1 and tenant3
	worker.processPendingTenants(ctx)

	time.Sleep(50 * time.Millisecond)

	// Verify tenant1 and tenant3 were claimed
	updated1, err := repo.GetByID(ctx, tenant1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, updated1.Status)

	// Tenant2 should still be in PROVISIONING status (already claimed by another worker)
	updated2, err := repo.GetByID(ctx, tenant2.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, updated2.Status)

	updated3, err := repo.GetByID(ctx, tenant3.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, updated3.Status)
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
	worker, err := NewProvisioningWorker(repo, prov, 5*time.Second, logger)
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
	worker, err := NewProvisioningWorker(repo, prov, 5*time.Second, logger)
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

	// Create worker
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	worker, err := NewProvisioningWorker(repo, prov, 5*time.Second, logger)
	require.NoError(t, err)

	// Execute
	worker.processPendingTenants(ctx)

	// Give goroutines a moment to spawn
	time.Sleep(50 * time.Millisecond)

	// Verify both tenants were claimed (status updated to PROVISIONING)
	// This indirectly verifies goroutines were spawned for successfully claimed tenants
	updated1, err := repo.GetByID(ctx, tenant1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, updated1.Status)

	updated2, err := repo.GetByID(ctx, tenant2.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, updated2.Status)
}
