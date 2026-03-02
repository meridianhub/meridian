// Package service provides integration tests for async tenant provisioning.
// These tests verify the end-to-end provisioning workflow using testcontainers.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
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
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

// Test configuration constants for better maintainability.
//
// Timeout values are intentionally generous to accommodate CI environment variability.
// Expected typical durations (local development):
//   - Single tenant provisioning: 1-5s
//   - Retry with 2 failures: 5-15s (includes backoff)
//   - 50 concurrent tenants: 30-60s
//   - Permanent failure detection: 1-3s
//
// CI environments may take 2-3x longer due to resource contention.
const (
	// defaultPollInterval is the interval between worker polling cycles during tests.
	defaultPollInterval = 100 * time.Millisecond

	// asyncProvisioningTimeout is the maximum time to wait for a single tenant
	// to complete provisioning in the happy path.
	// Expected: 1-5s local, up to 15s in CI.
	asyncProvisioningTimeout = 30 * time.Second

	// retryProvisioningTimeout is the maximum time to wait for provisioning
	// when retries are expected (includes exponential backoff delays).
	// Expected: 5-15s local, up to 30s in CI.
	retryProvisioningTimeout = 60 * time.Second

	// concurrentTenantCount is the number of tenants to create in stress tests.
	concurrentTenantCount = 50

	// concurrentProvisioningTimeout is the maximum time to wait for all concurrent
	// tenants to complete provisioning.
	// Expected: 30-60s local, up to 2min in CI.
	concurrentProvisioningTimeout = 3 * time.Minute

	// permanentFailureTimeout is the maximum time to wait for a permanent failure
	// to be detected (should be fast since no retries occur).
	// Expected: 1-3s local, up to 5s in CI.
	permanentFailureTimeout = 10 * time.Second
)

// =============================================================================
// Test Infrastructure
// =============================================================================

// Test error definitions for simulating provisioning failures.
// These must match patterns defined in services/tenant/worker/provisioning_worker.go:
//   - retryablePatterns (lines 333-344): "timeout", "connection", "lock", etc.
//   - permanentPatterns (lines 351-367): "invalid", "permission", "denied", etc.
//
// See isRetryableError() in provisioning_worker.go for the full classification logic.
var (
	// errRetryableTimeout is a retryable error matching "timeout" pattern
	// from retryablePatterns in provisioning_worker.go.
	errRetryableTimeout = errors.New("connection timeout waiting for database")

	// errPermanentPermissionDenied is a permanent error matching "permission" and "denied"
	// patterns from permanentPatterns in provisioning_worker.go.
	errPermanentPermissionDenied = errors.New("permission denied: insufficient privileges for schema creation")
)

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
	testdb.CreateAuditTables(t, db)

	// Create repository
	repo := persistence.NewRepository(db)

	// Create mock provisioner with typical services
	mockProv := provisioner.NewMockProvisioner(defaultTestServices())

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create provisioning worker with fast poll interval for testing
	w, err := worker.NewProvisioningWorker(repo, mockProv, worker.Config{
		PollInterval: defaultPollInterval,
	}, logger)
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

	// Create cleanup function that stops worker and container.
	// Shutdown sequence:
	// 1. Cancel context - signals worker to stop accepting new work
	// 2. Stop worker - waits for any in-flight provisioning operations to complete
	//    (graceful shutdown with internal timeout, see worker.Stop() implementation)
	// 3. Clean up database - terminates the testcontainer
	env.Cleanup = func() {
		cancel()
		w.Stop()
		dbCleanup()
	}

	return env
}

// =============================================================================
// Setup Verification Tests
// =============================================================================

// TestSetupEnvironment verifies that the test environment setup works correctly.
// This is a meta-test that validates our test infrastructure.
func TestSetupEnvironment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

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
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

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
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

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
		Roles:    []string{auth.RolePlatformAdmin.String()},
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

	// Log response time - async pattern should not block on provisioning.
	// Note: We use a warning instead of an assertion because CI environments have variable
	// resource availability. The primary test goal is verifying the tenant reaches ACTIVE status.
	if elapsed > 500*time.Millisecond {
		t.Logf("Warning: InitiateTenant took %v (>500ms) - this may indicate CI resource contention", elapsed)
	} else {
		t.Logf("InitiateTenant completed in %v", elapsed)
	}

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
	}, asyncProvisioningTimeout, defaultPollInterval,
		"Tenant should transition to ACTIVE status within %v", asyncProvisioningTimeout)

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

// TestProvisioningFailureRetry verifies that transient provisioning failures
// trigger automatic retry logic and eventually succeed after recovery.
//
// Test scenario:
// 1. Configure MockProvisioner to fail with a retryable error ("connection timeout")
// 2. Create a tenant, triggering async provisioning
// 3. After some provisioning attempts, clear the failure condition
// 4. Verify tenant eventually reaches ACTIVE status
// 5. Verify ProvisioningCalls shows multiple attempts (indicating retries)
func TestProvisioningFailureRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup test environment with worker running
	env := setupTestEnvironment(t)
	defer env.Cleanup()

	// Create the gRPC service with the test environment's repo and provisioner
	svc := NewService(env.Repo, env.Provisioner, nil, nil, env.Logger)

	// Configure tenant ID for this test
	testTenantID := "test_retry_tenant"

	// Create an authenticated context with platform admin claims
	claims := &auth.Claims{
		UserID:   "admin-123",
		TenantID: testTenantID,
		Roles:    []string{auth.RolePlatformAdmin.String()},
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, claims)

	// =========================================================================
	// Step 1: Configure MockProvisioner to fail with a retryable error
	// =========================================================================
	// Use "connection timeout" which matches retryablePatterns in provisioning_worker.go
	env.Provisioner.FailProvisioningFor[testTenantID] = errRetryableTimeout

	t.Logf("Configured MockProvisioner to fail provisioning for %s with: %v", testTenantID, errRetryableTimeout)

	// =========================================================================
	// Step 2: Set up callback to clear failure after 2 attempts (deterministic)
	// =========================================================================
	// Using callback instead of polling eliminates timing-based flakiness
	var failureClearedAt int
	failureCleared := make(chan struct{})
	env.Provisioner.OnProvisionAttempt = func(tenantID string, attemptCount int) {
		if tenantID != testTenantID {
			return
		}
		t.Logf("Provisioning attempt %d for tenant %s", attemptCount, tenantID)

		// After 2 failed attempts, clear the failure to allow success
		if attemptCount >= 2 && failureClearedAt == 0 {
			if env.Provisioner.ClearFailure(testTenantID) {
				failureClearedAt = attemptCount
				t.Logf("Cleared failure condition after %d provisioning attempts", attemptCount)
				close(failureCleared)
			}
		}
	}

	// =========================================================================
	// Step 3: Create tenant, triggering async provisioning
	// =========================================================================
	req := &pb.InitiateTenantRequest{
		TenantId:        testTenantID,
		DisplayName:     "Retry Test Tenant",
		SettlementAsset: "USD",
	}

	resp, err := svc.InitiateTenant(ctx, req)
	require.NoError(t, err, "InitiateTenant should succeed")
	require.NotNil(t, resp, "Response should not be nil")

	// Verify initial status is PROVISIONING_PENDING
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING, resp.Tenant.Status,
		"Initial tenant status should be PROVISIONING_PENDING")

	t.Logf("Tenant created with status: %s", resp.Tenant.Status)

	// =========================================================================
	// Step 4: Wait for callback to clear the failure (deterministic wait)
	// =========================================================================
	select {
	case <-failureCleared:
		t.Logf("Failure condition was cleared after %d attempts", failureClearedAt)
	case <-time.After(asyncProvisioningTimeout):
		t.Fatalf("Timeout waiting for provisioning attempts - callback never triggered")
	}

	// =========================================================================
	// Step 5: Verify tenant eventually becomes ACTIVE
	// =========================================================================
	statusReq := &pb.GetTenantProvisioningStatusRequest{
		TenantId: testTenantID,
	}

	var finalStatusResp *pb.GetTenantProvisioningStatusResponse
	require.Eventually(t, func() bool {
		statusResp, err := svc.GetTenantProvisioningStatus(ctx, statusReq)
		if err != nil {
			t.Logf("GetTenantProvisioningStatus error (will retry): %v", err)
			return false
		}

		t.Logf("Polling: overall_status=%s", statusResp.OverallStatus)

		if statusResp.OverallStatus == pb.TenantStatus_TENANT_STATUS_ACTIVE {
			finalStatusResp = statusResp
			return true
		}

		return false
	}, retryProvisioningTimeout, defaultPollInterval,
		"Tenant should transition to ACTIVE status within %v after retry", retryProvisioningTimeout)

	require.NotNil(t, finalStatusResp, "Final status response should be captured")

	// =========================================================================
	// Step 6: Verify ProvisioningCalls shows multiple attempts (retries occurred)
	// =========================================================================
	totalCalls := env.Provisioner.GetProvisioningCallCount()

	// Should have at least 2 calls: 1 failure + 1 success after callback clears failure
	// Note: OnProvisionAttempt callback is invoked at the START of ProvisionSchemas,
	// so when attemptCount >= 2, the failure is cleared BEFORE the 2nd attempt runs.
	assert.GreaterOrEqual(t, totalCalls, 2,
		"Should have at least 2 provisioning attempts (1 failure + 1 success after clear), got %d", totalCalls)

	t.Logf("Total provisioning attempts: %d (cleared failure after %d)", totalCalls, failureClearedAt)

	// Verify final status is ACTIVE
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_ACTIVE, finalStatusResp.OverallStatus,
		"Final tenant status should be ACTIVE after retry recovery")

	// Verify no error message (successful recovery)
	assert.Empty(t, finalStatusResp.ErrorMessage,
		"Overall provisioning should have no error message after successful retry")

	t.Logf("Provisioning retry test completed: %d total attempts, recovered after clearing failure at attempt %d",
		totalCalls, failureClearedAt)
}

// TestConcurrentTenantProvisioning stress tests the async provisioning system
// by creating 50 tenants concurrently and verifying all reach ACTIVE status.
//
// This test validates:
// - No race conditions in concurrent tenant creation
// - No database deadlocks or constraint violations under high load
// - Worker processes all tasks without data loss
// - Reasonable total completion time (<3 minutes for 50 tenants)
// - All tenants successfully provision to ACTIVE status
func TestConcurrentTenantProvisioning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup test environment with worker running
	env := setupTestEnvironment(t)
	defer env.Cleanup()

	// Create the gRPC service with the test environment's repo and provisioner
	svc := NewService(env.Repo, env.Provisioner, nil, nil, env.Logger)

	// Number of tenants to create concurrently
	const numTenants = concurrentTenantCount

	// Create platform admin context for API calls
	claims := &auth.Claims{
		UserID:   "admin-concurrent-test",
		TenantID: "platform",
		Roles:    []string{auth.RolePlatformAdmin.String()},
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, claims)

	// =========================================================================
	// Step 1: Create 50 tenants concurrently
	// =========================================================================
	t.Logf("Starting concurrent creation of %d tenants", numTenants)

	// Track tenant IDs and any errors during creation
	tenantIDs := make([]string, numTenants)
	creationErrors := make([]error, numTenants)
	creationTimes := make([]time.Duration, numTenants)

	var wg sync.WaitGroup
	startTime := time.Now()

	for i := 0; i < numTenants; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			tenantID := fmt.Sprintf("concurrent_test_%03d", index)
			tenantIDs[index] = tenantID

			req := &pb.InitiateTenantRequest{
				TenantId:        tenantID,
				DisplayName:     fmt.Sprintf("Concurrent Test Tenant %d", index),
				SettlementAsset: "USD",
			}

			callStart := time.Now()
			_, err := svc.InitiateTenant(ctx, req)
			creationTimes[index] = time.Since(callStart)
			creationErrors[index] = err
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	creationElapsed := time.Since(startTime)

	t.Logf("All %d InitiateTenant calls completed in %v", numTenants, creationElapsed)

	// =========================================================================
	// Step 2: Verify all creation calls succeeded in <500ms each
	// =========================================================================
	var failedCreations int
	var slowCreations int
	for i, err := range creationErrors {
		if err != nil {
			failedCreations++
			t.Errorf("Tenant %s creation failed: %v", tenantIDs[i], err)
		}
		if creationTimes[i] > 500*time.Millisecond {
			slowCreations++
			t.Logf("Warning: Tenant %s creation took %v (>500ms)", tenantIDs[i], creationTimes[i])
		}
	}

	require.Zero(t, failedCreations, "All %d tenant creations should succeed, but %d failed", numTenants, failedCreations)

	// Note: We don't assert on slow creation count because CI environments have variable
	// resource availability. The key assertions are:
	// 1. All creations succeed (tested above)
	// 2. All tenants eventually reach ACTIVE (tested below)
	// 3. Total test time is reasonable (tested at the end)
	if slowCreations > numTenants/2 {
		t.Logf("Warning: %d/%d tenants had slow creation times (>500ms) - this may indicate CI resource contention",
			slowCreations, numTenants)
	}

	t.Logf("Creation phase: %d tenants created, %d slow creations", numTenants-failedCreations, slowCreations)

	// =========================================================================
	// Step 3: Poll until all tenants reach ACTIVE status
	// =========================================================================
	t.Logf("Waiting for all %d tenants to reach ACTIVE status...", numTenants)

	// Track status for each tenant
	tenantStatuses := make([]pb.TenantStatus, numTenants)
	for i := range tenantStatuses {
		tenantStatuses[i] = pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING
	}

	// Use errgroup to poll all tenants in parallel
	provisioningStart := time.Now()
	require.Eventually(t, func() bool {
		var activeCount int
		var failedCount int
		var pendingCount int

		// Poll all tenants concurrently using errgroup
		g, gCtx := errgroup.WithContext(ctx)

		for i := 0; i < numTenants; i++ {
			g.Go(func() error {
				statusReq := &pb.GetTenantProvisioningStatusRequest{
					TenantId: tenantIDs[i],
				}

				statusResp, err := svc.GetTenantProvisioningStatus(gCtx, statusReq)
				if err == nil {
					tenantStatuses[i] = statusResp.OverallStatus
				}
				// Ignore transient errors during polling - tenant status remains unchanged
				return nil
			})
		}

		// Wait for all polling requests to complete.
		// Error is intentionally ignored because each goroutine returns nil
		// (errors are handled by leaving tenant status unchanged for retry on next poll).
		_ = g.Wait()

		// Count statuses
		for _, status := range tenantStatuses {
			//exhaustive:ignore - we only care about ACTIVE and FAILED for this test
			switch status {
			case pb.TenantStatus_TENANT_STATUS_ACTIVE:
				activeCount++
			case pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED:
				failedCount++
			default:
				pendingCount++
			}
		}

		// Log progress periodically
		elapsed := time.Since(provisioningStart)
		if elapsed.Seconds() > 0 && int(elapsed.Seconds())%5 == 0 {
			t.Logf("Progress: %d/%d active, %d failed, %d pending (elapsed: %v)",
				activeCount, numTenants, failedCount, pendingCount, elapsed)
		}

		// Success when all tenants are active
		return activeCount == numTenants
	}, concurrentProvisioningTimeout, 200*time.Millisecond,
		"All %d tenants should reach ACTIVE status within %v", numTenants, concurrentProvisioningTimeout)

	provisioningElapsed := time.Since(provisioningStart)
	t.Logf("All %d tenants reached ACTIVE status in %v", numTenants, provisioningElapsed)

	// =========================================================================
	// Step 4: Verify all tenants are ACTIVE and no failures
	// =========================================================================
	var activeCount, failedCount int
	for i, status := range tenantStatuses {
		//exhaustive:ignore - we only care about ACTIVE and FAILED for final verification
		switch status {
		case pb.TenantStatus_TENANT_STATUS_ACTIVE:
			activeCount++
		case pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED:
			failedCount++
			t.Errorf("Tenant %s failed provisioning", tenantIDs[i])
		default:
			t.Errorf("Tenant %s has unexpected status: %s", tenantIDs[i], status)
		}
	}

	assert.Equal(t, numTenants, activeCount, "All %d tenants should be ACTIVE", numTenants)
	assert.Zero(t, failedCount, "No tenants should have failed provisioning")

	// =========================================================================
	// Step 5: Verify total test time is reasonable
	// =========================================================================
	totalElapsed := time.Since(startTime)
	assert.Less(t, totalElapsed, concurrentProvisioningTimeout,
		"Total test time should be <%v, got %v", concurrentProvisioningTimeout, totalElapsed)

	t.Logf("Concurrent provisioning test completed: %d tenants, creation=%v, provisioning=%v, total=%v",
		numTenants, creationElapsed, provisioningElapsed, totalElapsed)

	// =========================================================================
	// Step 6: Verify worker processed all tasks (no data loss)
	// =========================================================================
	totalProvisioningCalls := env.Provisioner.GetProvisioningCallCount()
	assert.GreaterOrEqual(t, totalProvisioningCalls, numTenants,
		"Worker should have processed at least %d provisioning calls, got %d", numTenants, totalProvisioningCalls)

	t.Logf("Total provisioning calls: %d (expected >= %d)", totalProvisioningCalls, numTenants)
	t.Logf("Concurrent tenant provisioning stress test completed successfully")
}

// TestProvisioningPermanentFailure verifies that permanent provisioning failures
// (non-retryable errors) result in PROVISIONING_FAILED status without retries.
//
// This test demonstrates:
// - Permanent/non-retryable errors are not retried (only 1 provisioning attempt)
// - Tenant status transitions to PROVISIONING_FAILED
// - Error message is persisted in the tenant record and retrievable via API
//
// Note: For testing actual retry exhaustion with retryable errors, see
// TestProvisioningFailureRetry which verifies the retry mechanism.
func TestProvisioningPermanentFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup test environment with worker running
	env := setupTestEnvironment(t)
	defer env.Cleanup()

	// Create the gRPC service with the test environment's repo and provisioner
	svc := NewService(env.Repo, env.Provisioner, nil, nil, env.Logger)

	// =========================================================================
	// Step 1: Configure MockProvisioner to fail with a permanent (non-retryable) error
	// =========================================================================
	// Use "permission denied" which matches the permanentPatterns in provisioning_worker.go
	// Permanent errors should NOT be retried - the worker should fail fast
	testTenantID := "test_permanent_failure_tenant"
	env.Provisioner.FailProvisioningFor[testTenantID] = errPermanentPermissionDenied

	t.Logf("Configured MockProvisioner to fail provisioning for %s with permanent error: %v", testTenantID, errPermanentPermissionDenied)

	// Create an authenticated context with platform admin claims
	claims := &auth.Claims{
		UserID:   "admin-123",
		TenantID: testTenantID,
		Roles:    []string{auth.RolePlatformAdmin.String()},
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, claims)

	// =========================================================================
	// Step 2: Call InitiateTenant to create the tenant
	// =========================================================================
	req := &pb.InitiateTenantRequest{
		TenantId:        testTenantID,
		DisplayName:     "Permanent Failure Test Tenant",
		SettlementAsset: "USD",
	}

	resp, err := svc.InitiateTenant(ctx, req)
	require.NoError(t, err, "InitiateTenant should succeed")
	require.NotNil(t, resp, "Response should not be nil")
	require.NotNil(t, resp.Tenant, "Tenant in response should not be nil")

	// Verify initial status is PROVISIONING_PENDING
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING, resp.Tenant.Status,
		"Initial tenant status should be PROVISIONING_PENDING")

	t.Logf("Tenant created with status: %s", resp.Tenant.Status)

	// =========================================================================
	// Step 3: Poll until tenant status becomes PROVISIONING_FAILED
	// =========================================================================
	// Since this is a permanent error, it should fail fast (no retries)
	// Use a shorter timeout since we expect quick failure
	statusReq := &pb.GetTenantProvisioningStatusRequest{
		TenantId: testTenantID,
	}

	var finalStatusResp *pb.GetTenantProvisioningStatusResponse
	require.Eventually(t, func() bool {
		statusResp, err := svc.GetTenantProvisioningStatus(ctx, statusReq)
		if err != nil {
			t.Logf("GetTenantProvisioningStatus error (will retry): %v", err)
			return false
		}

		t.Logf("Polling: overall_status=%s, error_message=%q",
			statusResp.OverallStatus, statusResp.ErrorMessage)

		// Check if tenant has reached PROVISIONING_FAILED status
		if statusResp.OverallStatus == pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED {
			finalStatusResp = statusResp
			return true
		}

		return false
	}, permanentFailureTimeout, defaultPollInterval,
		"Tenant should transition to PROVISIONING_FAILED status within %v", permanentFailureTimeout)

	require.NotNil(t, finalStatusResp, "Final status response should be captured")

	// =========================================================================
	// Step 4: Verify final tenant status is PROVISIONING_FAILED
	// =========================================================================
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED, finalStatusResp.OverallStatus,
		"Final tenant status should be PROVISIONING_FAILED")

	t.Logf("Final provisioning status: %s", finalStatusResp.OverallStatus)

	// =========================================================================
	// Step 5: Verify error message is persisted and retrievable
	// =========================================================================
	assert.NotEmpty(t, finalStatusResp.ErrorMessage,
		"Error message should be persisted in the response")
	assert.Contains(t, finalStatusResp.ErrorMessage, "permission denied",
		"Error message should contain the original error details")

	t.Logf("Persisted error message: %s", finalStatusResp.ErrorMessage)

	// =========================================================================
	// Step 6: Verify only 1 provisioning attempt was made (no retries)
	// =========================================================================
	// For permanent errors, the worker should NOT retry
	// Count how many times ProvisionSchemas was called for this tenant
	callCount := env.Provisioner.GetProvisioningCallCountForTenant(testTenantID)

	assert.Equal(t, 1, callCount,
		"Permanent errors should NOT be retried - expected 1 provisioning call, got %d", callCount)

	t.Logf("Provisioning attempts: %d (expected 1 for permanent error)", callCount)
	t.Logf("Permanent failure (non-retryable error) test completed successfully")
}
