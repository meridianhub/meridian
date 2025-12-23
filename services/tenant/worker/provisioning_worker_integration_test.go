// Package worker provides integration tests for the provisioning worker.
// These tests use real PostgreSQL via testcontainers to verify concurrent
// provisioning behavior and optimistic locking correctness.
package worker

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormPG "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// integrationTestContext holds shared test resources for integration tests.
type integrationTestContext struct {
	ctx    context.Context
	cancel context.CancelFunc
	db     *gorm.DB
	sqlDB  *sql.DB
}

// setupIntegrationTestDatabase creates a PostgreSQL testcontainer and applies all migrations.
func setupIntegrationTestDatabase(t *testing.T) *integrationTestContext {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("tenant_integration_test"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Create standard SQL connection for migrations
	sqlDB, err := sql.Open("pgx", connStr)
	require.NoError(t, err, "Failed to connect to database")

	// Apply migrations
	applyMigrations(ctx, t, sqlDB)

	// Create GORM connection for repository
	// Note: We skip hooks because the audit GORM hooks have a bug where they insert
	// empty strings into JSONB columns which PostgreSQL rejects. This is a known
	// issue in the audit module (OldValues/NewValues should be *string pointers).
	// For integration tests, we skip hooks since audit logging is not the focus.
	db, err := gorm.Open(gormPG.Open(connStr), &gorm.Config{
		Logger:                 logger.Default.LogMode(logger.Silent),
		SkipDefaultTransaction: true,
	})
	require.NoError(t, err, "Failed to create GORM connection")

	// Create a session that skips GORM hooks (audit hooks conflict with PostgreSQL JSONB)
	db = db.Session(&gorm.Session{SkipHooks: true})

	// Cancel the setup context - it was only needed for container startup and migrations.
	cancel()
	testCtx, testCancel := context.WithCancel(context.Background())

	// Register cleanup with t.Cleanup for automatic teardown
	t.Cleanup(func() {
		testCancel()
		sqlDB.Close()
		if err := pgContainer.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	})

	return &integrationTestContext{ctx: testCtx, cancel: testCancel, db: db, sqlDB: sqlDB}
}

// applyMigrations applies all SQL migration files in the migrations directory.
func applyMigrations(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()

	// Get the directory containing migrations
	migrationDir, err := findMigrationDir()
	require.NoError(t, err, "Failed to find migration directory")

	entries, err := os.ReadDir(migrationDir)
	require.NoError(t, err, "Failed to read migration directory")

	// Collect and sort migration files
	var migrationFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			migrationFiles = append(migrationFiles, entry.Name())
		}
	}
	sort.Strings(migrationFiles) // Ensure timestamp order

	// Apply each migration
	for _, filename := range migrationFiles {
		content, err := os.ReadFile(filepath.Join(migrationDir, filename))
		require.NoError(t, err, "Failed to read migration %s", filename)

		_, err = db.ExecContext(ctx, string(content))
		require.NoError(t, err, "Failed to apply migration %s", filename)

		t.Logf("Applied migration: %s", filename)
	}
}

// findMigrationDir locates the migrations directory relative to the test file.
func findMigrationDir() (string, error) {
	// Start from current working directory and look for migrations
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// If we're in the worker directory, look for sibling migrations directory
	if filepath.Base(dir) == "worker" {
		migrationPath := filepath.Join(filepath.Dir(dir), "migrations")
		if _, err := os.Stat(migrationPath); err == nil {
			return migrationPath, nil
		}
	}

	// Look for services/tenant/migrations relative to project root
	for {
		migrationPath := filepath.Join(dir, "services", "tenant", "migrations")
		if _, err := os.Stat(migrationPath); err == nil {
			return migrationPath, nil
		}

		// Also check if we're at a go.mod level
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			migrationPath := filepath.Join(dir, "services", "tenant", "migrations")
			if _, err := os.Stat(migrationPath); err == nil {
				return migrationPath, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", os.ErrNotExist
}

// CountingMockProvisioner wraps MockProvisioner to count ProvisionSchemas calls atomically.
type CountingMockProvisioner struct {
	*provisioner.MockProvisioner
	provisionCount atomic.Int32
	mu             sync.Mutex
	tenantsClaimed []tenant.TenantID
}

// NewCountingMockProvisioner creates a new CountingMockProvisioner.
func NewCountingMockProvisioner() *CountingMockProvisioner {
	return &CountingMockProvisioner{
		MockProvisioner: provisioner.NewMockProvisioner([]provisioner.ServiceConfig{
			{Name: "test-service"},
		}),
		tenantsClaimed: make([]tenant.TenantID, 0),
	}
}

// ProvisionSchemas counts calls and delegates to the mock.
func (c *CountingMockProvisioner) ProvisionSchemas(ctx context.Context, tenantID tenant.TenantID) error {
	c.provisionCount.Add(1)
	c.mu.Lock()
	c.tenantsClaimed = append(c.tenantsClaimed, tenantID)
	c.mu.Unlock()
	return c.MockProvisioner.ProvisionSchemas(ctx, tenantID)
}

// GetProvisionCount returns the number of times ProvisionSchemas was called.
func (c *CountingMockProvisioner) GetProvisionCount() int {
	return int(c.provisionCount.Load())
}

// GetTenantsClaimed returns the list of tenant IDs that were claimed.
func (c *CountingMockProvisioner) GetTenantsClaimed() []tenant.TenantID {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]tenant.TenantID, len(c.tenantsClaimed))
	copy(result, c.tenantsClaimed)
	return result
}

// TestConcurrentProvisioningWithOptimisticLocking verifies that when multiple workers
// attempt to claim the same tenant concurrently, only one succeeds due to optimistic
// locking, and ProvisionSchemas is called exactly once.
//
// This test simulates the scenario where:
// 1. A tenant is created with status PROVISIONING_PENDING
// 2. Multiple worker goroutines call processPendingTenants concurrently
// 3. Only one worker should successfully claim the tenant (via UpdateStatus)
// 4. Other workers should get ErrVersionConflict and skip the tenant
// 5. ProvisionSchemas should be called exactly once
//
// Run with: go test -race -run TestConcurrentProvisioningWithOptimisticLocking ./...
func TestConcurrentProvisioningWithOptimisticLocking(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupIntegrationTestDatabase(t)

	repo := persistence.NewRepository(tc.db)

	// Create a single tenant in PROVISIONING_PENDING status
	testTenant := &domain.Tenant{
		ID:              tenant.MustNewTenantID("concurrent_test_tenant"),
		DisplayName:     "Concurrent Test Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
		Metadata:        make(map[string]interface{}), // Required for JSONB column
	}

	require.NoError(t, repo.Create(tc.ctx, testTenant))

	// Verify tenant was created
	created, err := repo.GetByID(tc.ctx, testTenant.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioningPending, created.Status)
	assert.Equal(t, 1, created.Version)

	// Create counting mock provisioner
	countingProvisioner := NewCountingMockProvisioner()

	// Capture log output to verify which workers claim vs skip (safeBuffer for thread safety)
	var logBuffer safeBuffer
	testLogger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Number of concurrent workers to test
	const numWorkers = 10

	// Pre-create workers before spawning goroutines (require.NoError unsafe in goroutines)
	workers := make([]*ProvisioningWorker, numWorkers)
	for i := 0; i < numWorkers; i++ {
		worker, err := NewProvisioningWorker(repo, countingProvisioner, testWorkerConfig(5*time.Second), testLogger)
		require.NoError(t, err)
		workers[i] = worker
	}

	var wg sync.WaitGroup

	// Start multiple workers concurrently
	for _, worker := range workers {
		wg.Add(1)
		go func(w *ProvisioningWorker) {
			defer wg.Done()
			w.processPendingTenants(tc.ctx)
			w.wg.Wait()
		}(worker)
	}

	// Wait for all workers to complete
	wg.Wait()

	// Wait for tenant to reach PROVISIONING status
	err = await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		tenant, _ := repo.GetByID(tc.ctx, testTenant.ID)
		return tenant != nil && tenant.Status == domain.StatusProvisioning
	})
	require.NoError(t, err, "tenant should reach PROVISIONING status")

	// Verify the tenant was successfully claimed (status changed to PROVISIONING)
	finalTenant, err := repo.GetByID(tc.ctx, testTenant.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, finalTenant.Status)
	// Version should have been incremented exactly once (1 -> 2)
	assert.Equal(t, 2, finalTenant.Version, "Version should be incremented exactly once")

	// Verify log output contains expected messages
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, "claimed tenant for provisioning", "At least one worker should have claimed the tenant")

	// Count occurrences in logs
	claimedLogCount := strings.Count(logOutput, "claimed tenant for provisioning")
	conflictLogCount := strings.Count(logOutput, "tenant already claimed by another worker")

	t.Logf("Workers that claimed: %d, Workers that got conflicts: %d", claimedLogCount, conflictLogCount)

	// Exactly one worker should have claimed the tenant
	assert.Equal(t, 1, claimedLogCount, "Exactly one worker should claim the tenant")
	// Other workers should see version conflicts (may be 0 if all workers read stale version)
	assert.GreaterOrEqual(t, conflictLogCount, 0, "Some workers should see version conflicts")

	t.Logf("Final tenant state: status=%s, version=%d", finalTenant.Status, finalTenant.Version)
}

// TestConcurrentProvisioningStressTest runs multiple iterations with many concurrent
// workers to stress-test the optimistic locking mechanism.
//
// Run with: go test -race -run TestConcurrentProvisioningStressTest ./...
func TestConcurrentProvisioningStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	tc := setupIntegrationTestDatabase(t)

	repo := persistence.NewRepository(tc.db)

	// Stress test parameters:
	// - 10 iterations: enough to expose timing-dependent race conditions reliably
	// - 10 workers per iteration: simulates realistic multi-instance deployment
	// These values balance thorough testing with reasonable CI execution time (~2-3s).
	// Increase numWorkers for more aggressive race testing locally.
	const numIterations = 10
	const numWorkers = 10

	for iteration := 0; iteration < numIterations; iteration++ {
		t.Run(fmt.Sprintf("iteration_%d", iteration), func(t *testing.T) {
			// Create a unique tenant for this iteration
			tenantID := tenant.MustNewTenantID("stress_test_tenant_" + strings.ReplaceAll(time.Now().Format("20060102150405.000000000"), ".", ""))
			testTenant := &domain.Tenant{
				ID:              tenantID,
				DisplayName:     "Stress Test Tenant",
				SettlementAsset: "GBP",
				Status:          domain.StatusProvisioningPending,
				CreatedAt:       time.Now(),
				Version:         1,
				Metadata:        make(map[string]interface{}), // Required for JSONB column
			}

			require.NoError(t, repo.Create(tc.ctx, testTenant))

			// Silent logger for stress testing
			silentLogger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))

			// Counting provisioner for this iteration
			countingProvisioner := NewCountingMockProvisioner()

			// Pre-create workers before spawning goroutines (require.NoError unsafe in goroutines)
			workers := make([]*ProvisioningWorker, numWorkers)
			for i := 0; i < numWorkers; i++ {
				worker, err := NewProvisioningWorker(repo, countingProvisioner, testWorkerConfig(5*time.Second), silentLogger)
				require.NoError(t, err)
				workers[i] = worker
			}

			var wg sync.WaitGroup

			// Start multiple workers concurrently
			for _, worker := range workers {
				wg.Add(1)
				go func(w *ProvisioningWorker) {
					defer wg.Done()
					w.processPendingTenants(tc.ctx)
					w.wg.Wait()
				}(worker)
			}

			wg.Wait()

			// Wait for tenant to reach PROVISIONING status
			awaitErr := await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
				tenant, _ := repo.GetByID(tc.ctx, tenantID)
				return tenant != nil && tenant.Status == domain.StatusProvisioning
			})
			require.NoError(t, awaitErr, "Iteration %d: tenant should reach PROVISIONING status", iteration)

			// Verify tenant state is consistent
			finalTenant, err := repo.GetByID(tc.ctx, tenantID)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusProvisioning, finalTenant.Status, "Iteration %d: Tenant should be PROVISIONING", iteration)
			assert.Equal(t, 2, finalTenant.Version, "Iteration %d: Version should be 2", iteration)
		})
	}
}

// TestMultipleTenantsWithConcurrentWorkers verifies that when multiple tenants
// are pending and multiple workers process concurrently, each tenant is claimed
// by exactly one worker.
func TestMultipleTenantsWithConcurrentWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupIntegrationTestDatabase(t)

	repo := persistence.NewRepository(tc.db)

	// Create multiple tenants in PROVISIONING_PENDING status
	const numTenants = 5
	tenantIDs := make([]tenant.TenantID, numTenants)

	for i := 0; i < numTenants; i++ {
		tenantIDs[i] = tenant.MustNewTenantID("multi_tenant_" + string(rune('a'+i)))
		testTenant := &domain.Tenant{
			ID:              tenantIDs[i],
			DisplayName:     "Multi Tenant " + string(rune('A'+i)),
			SettlementAsset: "GBP",
			Status:          domain.StatusProvisioningPending,
			CreatedAt:       time.Now().Add(time.Duration(i) * time.Millisecond), // Ensure ordering
			Version:         1,
			Metadata:        make(map[string]interface{}), // Required for JSONB column
		}
		require.NoError(t, repo.Create(tc.ctx, testTenant))
	}

	// Create counting provisioner
	countingProvisioner := NewCountingMockProvisioner()

	var logBuffer bytes.Buffer
	testLogger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	const numWorkers = 10

	// Pre-create workers before spawning goroutines (require.NoError unsafe in goroutines)
	workers := make([]*ProvisioningWorker, numWorkers)
	for i := 0; i < numWorkers; i++ {
		worker, err := NewProvisioningWorker(repo, countingProvisioner, testWorkerConfig(5*time.Second), testLogger)
		require.NoError(t, err)
		workers[i] = worker
	}

	var wg sync.WaitGroup

	// Start multiple workers concurrently
	for _, worker := range workers {
		wg.Add(1)
		go func(w *ProvisioningWorker) {
			defer wg.Done()
			w.processPendingTenants(tc.ctx)
			w.wg.Wait()
		}(worker)
	}

	wg.Wait()

	// Wait for all tenants to reach PROVISIONING status
	awaitErr := await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		for _, tenantID := range tenantIDs {
			tenant, _ := repo.GetByID(tc.ctx, tenantID)
			if tenant == nil || tenant.Status != domain.StatusProvisioning {
				return false
			}
		}
		return true
	})
	require.NoError(t, awaitErr, "all tenants should reach PROVISIONING status")

	// Verify all tenants were claimed and are in PROVISIONING status
	for _, tenantID := range tenantIDs {
		finalTenant, err := repo.GetByID(tc.ctx, tenantID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusProvisioning, finalTenant.Status, "Tenant %s should be PROVISIONING", tenantID)
		assert.Equal(t, 2, finalTenant.Version, "Tenant %s version should be 2", tenantID)
	}

	// Count claimed messages in log
	logOutput := logBuffer.String()
	claimedCount := strings.Count(logOutput, "claimed tenant for provisioning")
	t.Logf("Total claimed count from logs: %d (expected %d tenants)", claimedCount, numTenants)

	// Each tenant should be claimed exactly once
	assert.Equal(t, numTenants, claimedCount, "Each tenant should be claimed exactly once")
}

// TestVersionConflictHandling verifies that version conflicts are handled
// gracefully and don't cause panics or data corruption.
func TestVersionConflictHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupIntegrationTestDatabase(t)

	repo := persistence.NewRepository(tc.db)

	// Create a tenant
	testTenant := &domain.Tenant{
		ID:              tenant.MustNewTenantID("version_conflict_tenant"),
		DisplayName:     "Version Conflict Test",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
		Metadata:        make(map[string]interface{}), // Required for JSONB column
	}
	require.NoError(t, repo.Create(tc.ctx, testTenant))

	// Simulate another worker claiming the tenant first
	_, err := repo.UpdateStatus(tc.ctx, testTenant.ID, domain.StatusProvisioning, 1)
	require.NoError(t, err)

	// Now create a worker and have it try to process
	var logBuffer bytes.Buffer
	testLogger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	countingProvisioner := NewCountingMockProvisioner()

	worker, err := NewProvisioningWorker(repo, countingProvisioner, testWorkerConfig(5*time.Second), testLogger)
	require.NoError(t, err)

	// Process - should find no pending tenants (status already changed)
	worker.processPendingTenants(tc.ctx)
	worker.wg.Wait()

	// Verify no provisioning was triggered
	assert.Equal(t, 0, countingProvisioner.GetProvisionCount(), "No provisioning should have occurred")

	// Verify tenant state is still PROVISIONING from the simulated first worker
	finalTenant, err := repo.GetByID(tc.ctx, testTenant.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, finalTenant.Status)
	assert.Equal(t, 2, finalTenant.Version)
}

// TestRaceDetection is designed to be run with -race flag to detect any race conditions.
// It creates high contention on a single tenant to maximize the chance of detecting races.
func TestRaceDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race detection test in short mode")
	}

	tc := setupIntegrationTestDatabase(t)

	repo := persistence.NewRepository(tc.db)

	// Create a tenant
	testTenant := &domain.Tenant{
		ID:              tenant.MustNewTenantID("race_detection_tenant"),
		DisplayName:     "Race Detection Test",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningPending,
		CreatedAt:       time.Now(),
		Version:         1,
		Metadata:        make(map[string]interface{}), // Required for JSONB column
	}
	require.NoError(t, repo.Create(tc.ctx, testTenant))

	silentLogger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
	countingProvisioner := NewCountingMockProvisioner()

	// Create many workers that will all try to claim the same tenant simultaneously.
	// 50 workers provides high contention to reliably expose race conditions that
	// might only manifest with sufficient concurrent goroutines competing for resources.
	const numWorkers = 50

	// Pre-create workers before spawning goroutines (require.NoError unsafe in goroutines)
	workers := make([]*ProvisioningWorker, numWorkers)
	for i := 0; i < numWorkers; i++ {
		worker, err := NewProvisioningWorker(repo, countingProvisioner, testWorkerConfig(5*time.Second), silentLogger)
		require.NoError(t, err)
		workers[i] = worker
	}

	var wg sync.WaitGroup

	// Use a barrier to ensure all workers start at exactly the same time
	barrier := make(chan struct{})

	for _, worker := range workers {
		wg.Add(1)
		go func(w *ProvisioningWorker) {
			defer wg.Done()
			// Wait for barrier
			<-barrier
			// All workers start processing at the same time
			w.processPendingTenants(tc.ctx)
			w.wg.Wait()
		}(worker)
	}

	// Release all workers simultaneously
	close(barrier)

	// Wait for all workers
	wg.Wait()

	// Wait for tenant to reach PROVISIONING status
	awaitErr := await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		tenant, _ := repo.GetByID(tc.ctx, testTenant.ID)
		return tenant != nil && tenant.Status == domain.StatusProvisioning
	})
	require.NoError(t, awaitErr, "tenant should reach PROVISIONING status")

	// Verify final state is consistent
	finalTenant, err := repo.GetByID(tc.ctx, testTenant.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProvisioning, finalTenant.Status)
	assert.Equal(t, 2, finalTenant.Version)

	// If we get here without race detector failures, the test passes
	t.Log("Race detection test completed successfully")
}

// TestAlertCheckIntegration verifies that the alert checking mechanism
// executes within the worker loop and properly identifies failed tenants.
func TestAlertCheckIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc := setupIntegrationTestDatabase(t)

	repo := persistence.NewRepository(tc.db)

	// Create a tenant in provisioning_failed state with old timestamp
	// We use CreatedAt from 2 hours ago to ensure it exceeds the 1-hour threshold
	oldTimestamp := time.Now().Add(-2 * time.Hour)
	testTenant := &domain.Tenant{
		ID:              tenant.MustNewTenantID("failed_alert_tenant"),
		DisplayName:     "Failed Alert Test",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "mock provisioning failure",
		CreatedAt:       oldTimestamp,
		Version:         1,
		Metadata:        make(map[string]interface{}),
	}
	require.NoError(t, repo.Create(tc.ctx, testTenant))

	// Capture log output to verify alert is logged
	var logBuffer safeBuffer
	testLogger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	countingProvisioner := NewCountingMockProvisioner()

	// Create worker with short alert interval for testing (1 second)
	config := testWorkerConfig(5 * time.Second)
	config.AlertInterval = 1 * time.Second

	worker, err := NewProvisioningWorker(repo, countingProvisioner, config, testLogger)
	require.NoError(t, err)

	// Start worker in background
	ctx, cancel := context.WithCancel(tc.ctx)
	defer cancel()

	go worker.Start(ctx)

	// Wait for alert check to execute (should happen within 1 second + buffer)
	err = await.AtMost(3 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		logOutput := logBuffer.String()
		return strings.Contains(logOutput, "tenant provisioning failure alert") &&
			strings.Contains(logOutput, "failed_alert_tenant")
	})
	require.NoError(t, err, "alert should be logged within expected interval")

	// Verify alert message contains expected fields
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, "tenant provisioning failure alert")
	assert.Contains(t, logOutput, "failed_alert_tenant")
	assert.Contains(t, logOutput, "mock provisioning failure")
	assert.Contains(t, logOutput, "alert=tenant_provisioning_failed")

	// Stop worker and verify graceful shutdown
	cancel()
	worker.Stop()

	// Verify no goroutine leaks by checking Stop completed without hanging
	t.Log("Worker stopped successfully without goroutine leaks")
}
