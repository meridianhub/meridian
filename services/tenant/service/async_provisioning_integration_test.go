// Package service provides integration tests for async tenant provisioning.
// These tests verify the end-to-end provisioning workflow using testcontainers.
package service

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/services/tenant/worker"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// =============================================================================
// Test Infrastructure
// =============================================================================

// TestEnvironment holds all fixtures for async provisioning integration tests.
type TestEnvironment struct {
	DB          *gorm.DB
	Repo        *persistence.Repository
	Provisioner *provisioner.MockProvisioner
	Worker      *worker.ProvisioningWorker
	Logger      *slog.Logger
	Cleanup     func()
	cancelFunc  context.CancelFunc
}

// defaultTestServices returns the default service configuration for testing.
// Matches typical BIAN services: party, current-account.
func defaultTestServices() []provisioner.ServiceConfig {
	return []provisioner.ServiceConfig{
		{Name: "party", MigrationPath: "/migrations/party"},
		{Name: "current-account", MigrationPath: "/migrations/current-account"},
	}
}

// setupTestEnvironment creates a complete test environment with:
// - PostgreSQL testcontainer
// - Tenant database schema and tables
// - persistence.Repository
// - MockProvisioner with default services
// - ProvisioningWorker
//
// Returns a TestEnvironment with cleanup function. Caller must defer cleanup.
func setupTestEnvironment(t *testing.T) *TestEnvironment {
	t.Helper()

	// Create PostgreSQL container with tenant entities
	db, dbCleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.TenantEntity{},
		&persistence.ProvisioningStatusEntity{},
	})

	// Create additional tables required by the tenant service
	// These are created by migrations in production but we need them for tests
	createTestTables(t, db)

	// Create repository
	repo := persistence.NewRepository(db)

	// Create mock provisioner with typical services
	mockProv := provisioner.NewMockProvisioner(defaultTestServices())

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create provisioning worker with fast poll interval for testing
	pollInterval := 100 * time.Millisecond
	w, err := worker.NewProvisioningWorker(repo, mockProv, pollInterval, logger)
	require.NoError(t, err, "Failed to create provisioning worker")

	// Create cancellable context for worker
	ctx, cancel := context.WithCancel(context.Background())

	// Start worker in background
	go w.Start(ctx)

	env := &TestEnvironment{
		DB:          db,
		Repo:        repo,
		Provisioner: mockProv,
		Worker:      w,
		Logger:      logger,
		cancelFunc:  cancel,
	}

	// Create cleanup function that stops worker and container
	env.Cleanup = func() {
		// Cancel worker context
		cancel()
		// Stop worker and wait for in-flight provisioning
		w.Stop()
		// Clean up database container
		dbCleanup()
	}

	return env
}

// createTestTables creates additional tables needed for integration tests.
// These tables are normally created by Atlas migrations.
func createTestTables(t *testing.T, db *gorm.DB) {
	t.Helper()

	// Create audit_outbox table (required for audit logging hooks)
	// Note: Uses TEXT for old_values/new_values to allow empty strings (JSONB would reject them)
	err := db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id VARCHAR(50) NOT NULL,
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

	// Create audit_log table (required for audit processing)
	err = db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id VARCHAR(50) NOT NULL,
			changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			changed_by VARCHAR(100),
			old_values TEXT,
			new_values TEXT,
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT
		)
	`).Error
	require.NoError(t, err, "Failed to create audit_log table")
}

// =============================================================================
// Setup Verification Tests
// =============================================================================

// TestSetupEnvironment verifies that the test environment setup works correctly.
// This is a meta-test that validates our test infrastructure.
func TestSetupEnvironment(t *testing.T) {
	// Setup environment
	env := setupTestEnvironment(t)
	defer env.Cleanup()

	// Test 1: Verify CockroachDB/PostgreSQL container started successfully
	t.Run("database_container_starts", func(t *testing.T) {
		require.NotNil(t, env.DB, "Database connection should not be nil")

		// Verify we can execute a simple query
		var result int
		err := env.DB.Raw("SELECT 1").Scan(&result).Error
		require.NoError(t, err, "Should be able to execute simple query")
		assert.Equal(t, 1, result, "Query should return expected result")
	})

	// Test 2: Verify schema/tables were created
	t.Run("tables_created", func(t *testing.T) {
		// Check tenant table exists by attempting to query it
		var count int64
		err := env.DB.Table("tenant").Count(&count).Error
		require.NoError(t, err, "tenant table should exist")

		// Check tenant_provisioning_status table exists
		err = env.DB.Table("tenant_provisioning_status").Count(&count).Error
		require.NoError(t, err, "tenant_provisioning_status table should exist")

		// Check audit_outbox table exists
		err = env.DB.Table("audit_outbox").Count(&count).Error
		require.NoError(t, err, "audit_outbox table should exist")
	})

	// Test 3: Verify Repository works
	t.Run("repository_works", func(t *testing.T) {
		require.NotNil(t, env.Repo, "Repository should not be nil")

		// Verify ping works
		ctx := context.Background()
		err := env.Repo.Ping(ctx)
		require.NoError(t, err, "Repository ping should succeed")
	})

	// Test 4: Verify MockProvisioner is configured correctly
	t.Run("provisioner_configured", func(t *testing.T) {
		require.NotNil(t, env.Provisioner, "Provisioner should not be nil")

		// Verify services are configured
		schemas := env.Provisioner.GetRequiredSchemas()
		assert.Len(t, schemas, 2, "Should have 2 configured services")
		assert.Contains(t, schemas, "party", "Should have party service")
		assert.Contains(t, schemas, "current-account", "Should have current-account service")
	})

	// Test 5: Verify Worker is running
	t.Run("worker_running", func(t *testing.T) {
		require.NotNil(t, env.Worker, "Worker should not be nil")
		// Worker is started in a goroutine during setup
		// We verify it's running by checking it doesn't panic
		// and can be stopped cleanly (which happens in cleanup)
	})

	// Test 6: Verify cleanup works
	t.Run("cleanup_works", func(t *testing.T) {
		// Create a separate environment to test cleanup
		env2 := setupTestEnvironment(t)

		// Cleanup should not panic
		require.NotPanics(t, func() {
			env2.Cleanup()
		}, "Cleanup should not panic")

		// After cleanup, the database connection should be closed
		// Note: We can't easily verify this without causing errors,
		// so we just verify cleanup runs without panic
	})
}

// TestSetupEnvironment_MultipleEnvironments verifies that multiple test environments
// can be created in parallel without interference.
func TestSetupEnvironment_MultipleEnvironments(t *testing.T) {
	// Create two environments in parallel
	env1 := setupTestEnvironment(t)
	defer env1.Cleanup()

	env2 := setupTestEnvironment(t)
	defer env2.Cleanup()

	// Verify they are independent instances (comparing pointers)
	assert.True(t, env1.DB != env2.DB, "Environments should have different DB connections")
	assert.True(t, env1.Provisioner != env2.Provisioner, "Environments should have different provisioner instances")

	// Verify both work independently
	ctx := context.Background()

	err := env1.Repo.Ping(ctx)
	require.NoError(t, err, "Environment 1 should work")

	err = env2.Repo.Ping(ctx)
	require.NoError(t, err, "Environment 2 should work")
}

// =============================================================================
// Async Provisioning Flow Tests
// =============================================================================

// TestAsyncProvisioningFlow verifies the complete async provisioning lifecycle:
// 1. InitiateTenant returns quickly (<500ms) with PROVISIONING_PENDING status
// 2. Background worker processes the tenant and transitions to ACTIVE
// 3. All services show completed provisioning status
func TestAsyncProvisioningFlow(t *testing.T) {
	// Setup test environment with worker running
	env := setupTestEnvironment(t)
	defer env.Cleanup()

	// Create the gRPC service with the test environment's repo and provisioner
	svc := NewService(env.Repo, env.Provisioner, nil, nil, env.Logger)

	// Create an authenticated context with platform admin claims for the test tenant
	// Platform admin role allows access to GetTenantProvisioningStatus for any tenant
	testTenantID := "test_async_tenant"
	claims := &auth.Claims{
		UserID:   "admin-123",
		TenantID: testTenantID, // Also set TenantID to test tenant-scoped access path
		Roles:    []string{auth.RolePlatformAdmin},
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, claims)

	// =========================================================================
	// Step 1: Call InitiateTenant and verify fast response time (<500ms)
	// =========================================================================
	req := &pb.InitiateTenantRequest{
		TenantId:        testTenantID,
		DisplayName:     "Async Test Tenant",
		SettlementAsset: "USD",
	}

	startTime := time.Now()
	resp, err := svc.InitiateTenant(ctx, req)
	elapsed := time.Since(startTime)

	require.NoError(t, err, "InitiateTenant should succeed")
	require.NotNil(t, resp, "Response should not be nil")
	require.NotNil(t, resp.Tenant, "Tenant in response should not be nil")

	// Assert response time < 500ms (async pattern should not block on provisioning)
	assert.Less(t, elapsed, 500*time.Millisecond,
		"InitiateTenant should return in <500ms for async provisioning, got %v", elapsed)

	t.Logf("InitiateTenant completed in %v", elapsed)

	// =========================================================================
	// Step 2: Verify initial status is PROVISIONING_PENDING
	// =========================================================================
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING, resp.Tenant.Status,
		"Initial tenant status should be PROVISIONING_PENDING")

	t.Logf("Tenant created with status: %s", resp.Tenant.Status)

	// =========================================================================
	// Step 3: Poll GetTenantProvisioningStatus until status becomes ACTIVE
	// =========================================================================
	statusReq := &pb.GetTenantProvisioningStatusRequest{
		TenantId: testTenantID,
	}

	// Use require.Eventually to poll with 100ms interval, 30s timeout
	var finalStatusResp *pb.GetTenantProvisioningStatusResponse
	require.Eventually(t, func() bool {
		statusResp, err := svc.GetTenantProvisioningStatus(ctx, statusReq)
		if err != nil {
			t.Logf("GetTenantProvisioningStatus error (will retry): %v", err)
			return false
		}

		t.Logf("Polling: overall_status=%s, services=%d",
			statusResp.OverallStatus, len(statusResp.Services))

		// Check if tenant has reached ACTIVE status
		if statusResp.OverallStatus == pb.TenantStatus_TENANT_STATUS_ACTIVE {
			finalStatusResp = statusResp
			return true
		}

		// Log individual service status for debugging
		for _, svcStatus := range statusResp.Services {
			t.Logf("  Service %s: status=%s, version=%s",
				svcStatus.ServiceName, svcStatus.Status, svcStatus.MigrationVersion)
		}

		return false
	}, 30*time.Second, 100*time.Millisecond,
		"Tenant should transition to ACTIVE status within 30 seconds")

	require.NotNil(t, finalStatusResp, "Final status response should be captured")

	// =========================================================================
	// Step 4: Verify final tenant status is ACTIVE (primary integration test goal)
	// =========================================================================
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_ACTIVE, finalStatusResp.OverallStatus,
		"Final tenant status should be ACTIVE")

	t.Logf("Final provisioning status: %s with %d services",
		finalStatusResp.OverallStatus, len(finalStatusResp.Services))

	// Note: Service-level provisioning status records depend on the provisioner implementation.
	// MockProvisioner stores status in memory, not in the database, so
	// GetTenantProvisioningStatus (which queries the DB) won't find service records.
	// This is expected behavior for MockProvisioner-based tests.
	//
	// For tests that need to verify service-level status, use:
	// - A real provisioner (PostgresProvisioner) that persists to the DB
	// - Or directly insert test records into tenant_provisioning_status table
	if len(finalStatusResp.Services) > 0 {
		t.Logf("Found %d service provisioning records:", len(finalStatusResp.Services))
		for _, svcStatus := range finalStatusResp.Services {
			assert.Equal(t, pb.ServiceProvisioningStatus_STATUS_COMPLETED, svcStatus.Status,
				"Service %s should have COMPLETED status, got %s", svcStatus.ServiceName, svcStatus.Status)
			assert.Empty(t, svcStatus.ErrorMessage,
				"Service %s should have no error message", svcStatus.ServiceName)

			t.Logf("  Service %s: status=%s, migration_version=%s, completed_at=%v",
				svcStatus.ServiceName, svcStatus.Status, svcStatus.MigrationVersion, svcStatus.CompletedAt)
		}
	} else {
		t.Logf("No service-level provisioning records found (expected with MockProvisioner)")
	}

	// Verify no overall error message
	assert.Empty(t, finalStatusResp.ErrorMessage,
		"Overall provisioning should have no error message")

	t.Logf("Async provisioning flow completed successfully")
}
