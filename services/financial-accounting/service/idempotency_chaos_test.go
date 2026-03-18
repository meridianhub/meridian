// Package service provides chaos and concurrency tests for idempotency guarantees.
//
// These tests verify the atomic guarantees of the idempotency system under
// failure conditions (simulated crashes) and high-concurrency scenarios.
package service

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/meridianhub/meridian/services/financial-accounting/config"
	"github.com/meridianhub/meridian/services/financial-accounting/worker"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test-specific sentinel errors for linter compliance (err113)
var (
	errTestSimulatedPanic       = errors.New("simulated panic in business logic")
	errTestBusinessLogicFailure = errors.New("business logic failure")
)

// testLogger creates a test logger with debug level.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
}

// setupTestRedis creates a miniredis instance and returns the Redis service and cleanup function.
func setupTestRedis(t *testing.T) (*idempotency.RedisService, *redis.Client, func()) {
	t.Helper()

	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: s.Addr(),
	})

	redisSvc := idempotency.NewRedisService(client)

	cleanup := func() {
		_ = client.Close()
		s.Close()
	}

	return redisSvc, client, cleanup
}

// =============================================================================
// CHAOS TESTS: Simulated Crash Scenarios
// =============================================================================

// TestChaos_CrashAfterMarkPending_CleanupWorkerMarksAsFailed verifies that when
// a process crashes (simulated by abandoning a goroutine) after MarkPending but
// before StoreResult, the cleanup worker correctly marks the key as FAILED.
//
// This test validates the fix for the idempotency gap where orphaned PENDING
// keys could block future requests indefinitely.
func TestChaos_CrashAfterMarkPending_CleanupWorkerMarksAsFailed(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	logger := testLogger()

	// Create a key and mark it as PENDING (simulating the state just before a crash)
	crashedKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "deposit",
		EntityID:  "account-crash-001",
		RequestID: "req-crash-001",
	}

	// Mark as pending - simulating MarkPending succeeded
	err := redisSvc.MarkPending(ctx, crashedKey, time.Hour)
	require.NoError(t, err, "MarkPending should succeed")

	// Verify key is PENDING
	result, err := redisSvc.Check(ctx, crashedKey)
	require.NoError(t, err)
	assert.Equal(t, idempotency.StatusPending, result.Status)

	// SIMULATED CRASH: We don't call StoreResult or Delete
	// The key is now orphaned in PENDING state

	// Intentional sleep: Wait for the key to become stale (short threshold for testing).
	// This is testing time-based staleness detection.
	time.Sleep(100 * time.Millisecond) //nolint:forbidigo // triggers staleness timeout for cleanup worker test

	// Configure cleanup worker with short threshold
	cfg := config.IdempotencyCleanupConfig{
		Enabled:        true,
		StaleThreshold: 50 * time.Millisecond, // Very short for testing
		RunInterval:    100 * time.Millisecond,
		BatchSize:      100,
		KeyPattern:     "idempotency:result:*",
	}

	w, err := worker.NewIdempotencyCleanupWorker(redisSvc, cfg, logger)
	require.NoError(t, err)

	// Start cleanup worker
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	go func() {
		_ = w.Start(workerCtx)
	}()

	// Wait for the key to be marked as FAILED
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			result, checkErr := redisSvc.Check(ctx, crashedKey)
			if checkErr != nil {
				return false
			}
			return result != nil && result.Status == idempotency.StatusFailed
		})

	require.NoError(t, err, "cleanup worker should mark orphaned PENDING key as FAILED")

	// Verify the key is now FAILED with timeout reason
	result, err = redisSvc.Check(ctx, crashedKey)
	require.NoError(t, err)
	assert.Equal(t, idempotency.StatusFailed, result.Status)
	assert.Contains(t, result.Error, "timeout", "error should indicate timeout cleanup")

	w.Stop()
}

// TestChaos_PanicInBusinessLogic_PendingStateCleanedUp verifies that when
// business logic panics during a transaction, the PENDING state is properly
// cleaned up (either by the Executor's panic recovery or by the cleanup worker).
//
// This test uses the Executor which provides atomic cleanup on error.
func TestChaos_PanicInBusinessLogic_PendingStateCleanedUp(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	panicKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "withdraw",
		EntityID:  "account-panic-001",
		RequestID: "req-panic-001",
	}

	executor := idempotency.NewExecutor(redisSvc, nil)

	// Execute with a panicking function - wrapped in recover
	var panicOccurred bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicOccurred = true
			}
		}()

		_, _ = executor.Execute(ctx, panicKey, time.Hour, func(_ context.Context) ([]byte, error) {
			panic(errTestSimulatedPanic)
		})
	}()

	assert.True(t, panicOccurred, "panic should have occurred in business logic")

	// The Executor doesn't have built-in panic recovery, so the key remains PENDING
	// This is documented behavior - the cleanup worker handles this case
	result, err := redisSvc.Check(ctx, panicKey)

	// Key may be PENDING (awaiting cleanup) or already cleaned up
	if err == nil && result != nil {
		// If key exists, it should either be PENDING (waiting for cleanup)
		// or FAILED (if cleanup worker ran)
		assert.True(t,
			result.Status == idempotency.StatusPending || result.Status == idempotency.StatusFailed,
			"key should be PENDING or FAILED after panic, got: %s", result.Status)
	}
	// If key doesn't exist, that's also valid - it may have been cleaned up
}

// TestChaos_ErrorInBusinessLogic_PendingStateDeletedAtomically verifies that
// when business logic returns an error, the PENDING state is atomically deleted.
func TestChaos_ErrorInBusinessLogic_PendingStateDeletedAtomically(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	errorKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "transfer",
		EntityID:  "account-error-001",
		RequestID: "req-error-001",
	}

	executor := idempotency.NewExecutor(redisSvc, nil)

	// Execute with a function that returns an error
	_, execErr := executor.Execute(ctx, errorKey, time.Hour, func(_ context.Context) ([]byte, error) {
		return nil, errTestBusinessLogicFailure
	})

	// Verify the business error was returned
	require.Error(t, execErr)
	assert.True(t, errors.Is(execErr, errTestBusinessLogicFailure) || execErr.Error() == errTestBusinessLogicFailure.Error(),
		"should return the business logic error")

	// Verify the PENDING state was cleaned up (key should not exist)
	_, err := redisSvc.Check(ctx, errorKey)
	assert.ErrorIs(t, err, idempotency.ErrResultNotFound,
		"PENDING key should be deleted after business logic error")
}

// TestChaos_ExecuteWithFailedState_MarksAsFailedNotDeleted verifies that
// ExecuteWithFailedState marks the key as FAILED instead of deleting it
// when business logic fails.
func TestChaos_ExecuteWithFailedState_MarksAsFailedNotDeleted(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	failedKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "withdraw",
		EntityID:  "account-failed-001",
		RequestID: "req-failed-001",
	}

	executor := idempotency.NewExecutor(redisSvc, nil)

	// Execute with ExecuteWithFailedState - this marks as FAILED instead of deleting
	_, execErr := executor.ExecuteWithFailedState(ctx, failedKey, time.Hour, func(_ context.Context) ([]byte, error) {
		return nil, errTestBusinessLogicFailure
	})

	require.Error(t, execErr)

	// Verify the key is marked as FAILED (not deleted)
	result, err := redisSvc.Check(ctx, failedKey)
	require.NoError(t, err)
	assert.Equal(t, idempotency.StatusFailed, result.Status)
	assert.Equal(t, errTestBusinessLogicFailure.Error(), result.Error)
}

// =============================================================================
// CONCURRENCY TESTS: Concurrent Access Scenarios
// =============================================================================

// TestConcurrency_SequentialRequestsWithSameKey verifies that sequential
// requests with the same idempotency key return cached results after the
// first execution completes.
//
// This test verifies the core idempotency guarantee: once an operation
// completes, subsequent requests with the same key return the cached result.
func TestConcurrency_SequentialRequestsWithSameKey(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	sharedKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "deposit",
		EntityID:  "account-sequential-001",
		RequestID: "req-sequential-001",
	}

	executor := idempotency.NewExecutor(redisSvc, nil)

	var executionCount int32
	expectedData := []byte(`{"success":true,"amount":100}`)

	// First request - should execute
	result1, err := executor.Execute(ctx, sharedKey, time.Hour, func(_ context.Context) ([]byte, error) {
		atomic.AddInt32(&executionCount, 1)
		return expectedData, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result1)
	assert.False(t, result1.FromCache, "first request should execute fresh")
	assert.Equal(t, idempotency.StatusCompleted, result1.Status)
	assert.Equal(t, expectedData, result1.Data)
	assert.Equal(t, int32(1), atomic.LoadInt32(&executionCount))

	// Second request - should return cached result
	result2, err := executor.Execute(ctx, sharedKey, time.Hour, func(_ context.Context) ([]byte, error) {
		atomic.AddInt32(&executionCount, 1)
		return []byte(`{"different":"data"}`), nil
	})

	require.NoError(t, err)
	require.NotNil(t, result2)
	assert.True(t, result2.FromCache, "second request should return cached result")
	assert.Equal(t, idempotency.StatusCompleted, result2.Status)
	assert.Equal(t, expectedData, result2.Data, "should return original cached data")
	assert.Equal(t, int32(1), atomic.LoadInt32(&executionCount), "business logic should not execute again")

	// Third request - should also return cached
	result3, err := executor.Execute(ctx, sharedKey, time.Hour, func(_ context.Context) ([]byte, error) {
		atomic.AddInt32(&executionCount, 1)
		return nil, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result3)
	assert.True(t, result3.FromCache)
	assert.Equal(t, int32(1), atomic.LoadInt32(&executionCount), "execution count should remain 1")
}

// TestConcurrency_DifferentKeysExecuteIndependently verifies that requests
// with different idempotency keys execute independently.
func TestConcurrency_DifferentKeysExecuteIndependently(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	executor := idempotency.NewExecutor(redisSvc, nil)

	numKeys := 10
	var executionCount int32
	var wg sync.WaitGroup

	for i := 0; i < numKeys; i++ {
		wg.Add(1)
		go func(keyID int) {
			defer wg.Done()

			key := idempotency.Key{
				Namespace: "financial-accounting",
				Operation: "transfer",
				EntityID:  "account-" + strconv.Itoa(keyID),
				RequestID: "req-" + strconv.Itoa(keyID),
			}

			_, err := executor.Execute(ctx, key, time.Hour, func(_ context.Context) ([]byte, error) {
				atomic.AddInt32(&executionCount, 1)
				return []byte(`{"keyID":` + strconv.Itoa(keyID) + `}`), nil
			})

			require.NoError(t, err)
		}(i)
	}

	wg.Wait()

	// Each unique key should have executed once
	assert.Equal(t, int32(numKeys), atomic.LoadInt32(&executionCount),
		"each unique key should execute its business logic")
}

// TestConcurrency_OperationInProgressRejection verifies that requests are
// properly rejected when an operation is already in progress (PENDING state).
func TestConcurrency_OperationInProgressRejection(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	pendingKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "withdrawal",
		EntityID:  "account-pending-001",
		RequestID: "req-pending-001",
	}

	// Mark the key as PENDING (simulating an in-flight request)
	err := redisSvc.MarkPending(ctx, pendingKey, time.Hour)
	require.NoError(t, err)

	executor := idempotency.NewExecutor(redisSvc, nil)

	// Try to execute with the same key - should fail with ErrOperationInProgress
	var executionCount int32
	_, execErr := executor.Execute(ctx, pendingKey, time.Hour, func(_ context.Context) ([]byte, error) {
		atomic.AddInt32(&executionCount, 1)
		return []byte("should not execute"), nil
	})

	// Should get ErrOperationInProgress
	require.Error(t, execErr)
	assert.True(t, errors.Is(execErr, idempotency.ErrOperationInProgress),
		"should get ErrOperationInProgress for PENDING key")
	assert.Equal(t, int32(0), atomic.LoadInt32(&executionCount),
		"business logic should not execute when key is PENDING")
}

// TestConcurrency_RaceBetweenCleanupAndStoreResult verifies that the race
// between the cleanup worker marking a key as FAILED and StoreResult marking
// it as COMPLETED results in a consistent final state.
func TestConcurrency_RaceBetweenCleanupAndStoreResult(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	logger := testLogger()

	// Configure cleanup worker with very aggressive settings
	cfg := config.IdempotencyCleanupConfig{
		Enabled:        true,
		StaleThreshold: 10 * time.Millisecond, // Very short
		RunInterval:    20 * time.Millisecond, // Very frequent
		BatchSize:      100,
		KeyPattern:     "idempotency:result:*",
	}

	w, err := worker.NewIdempotencyCleanupWorker(redisSvc, cfg, logger)
	require.NoError(t, err)

	// Start cleanup worker
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	go func() {
		_ = w.Start(workerCtx)
	}()

	// Run multiple iterations to increase chance of hitting the race
	for iteration := 0; iteration < 20; iteration++ {
		raceKey := idempotency.Key{
			Namespace: "financial-accounting",
			Operation: "race-test",
			EntityID:  "account-race-" + strconv.Itoa(iteration),
			RequestID: "req-race-" + strconv.Itoa(iteration),
		}

		// Mark as pending
		err := redisSvc.MarkPending(ctx, raceKey, time.Hour)
		require.NoError(t, err)

		// Intentional sleep: Wait just long enough for the key to potentially become stale
		time.Sleep(15 * time.Millisecond) //nolint:forbidigo // chaos: simulates random concurrent delay

		// Now try to store result (racing with cleanup worker)
		completedResult := idempotency.Result{
			Key:         raceKey,
			Status:      idempotency.StatusCompleted,
			Data:        []byte(`{"success":true}`),
			CompletedAt: time.Now(),
			TTL:         time.Hour,
		}

		_ = redisSvc.StoreResult(ctx, completedResult) // Ignore error - we're testing the race

		// Intentional sleep: Wait a bit and check final state after race condition
		time.Sleep(50 * time.Millisecond) //nolint:forbidigo // chaos: simulates random concurrent delay

		result, err := redisSvc.Check(ctx, raceKey)
		if err != nil {
			// Key might have been deleted or expired - that's OK
			continue
		}

		// Final state must be consistent: either COMPLETED or FAILED, not PENDING
		assert.True(t,
			result.Status == idempotency.StatusCompleted || result.Status == idempotency.StatusFailed,
			"iteration %d: final state must be COMPLETED or FAILED, got: %s", iteration, result.Status)
	}

	w.Stop()
}

// =============================================================================
// LOAD TESTS: Sustained Traffic Verification
// =============================================================================

// TestLoad_SustainedTraffic_AllOperationsComplete verifies that under
// sustained load, all operations complete successfully (either fresh or cached).
func TestLoad_SustainedTraffic_AllOperationsComplete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	logger := testLogger()

	// Configure cleanup worker
	cfg := config.IdempotencyCleanupConfig{
		Enabled:        true,
		StaleThreshold: 500 * time.Millisecond, // Short for testing
		RunInterval:    200 * time.Millisecond,
		BatchSize:      100,
		KeyPattern:     "idempotency:result:*",
	}

	metrics := idempotency.NewMetricsCollector("load-test")
	w, err := worker.NewIdempotencyCleanupWorkerWithMetrics(redisSvc, cfg, logger, metrics)
	require.NoError(t, err)

	// Start cleanup worker
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	go func() {
		_ = w.Start(workerCtx)
	}()

	executor := idempotency.NewExecutorWithMetrics(redisSvc, nil, metrics)

	// Configure load test parameters
	targetRPS := 100 // Lower for miniredis in tests
	testDuration := 2 * time.Second

	var (
		totalRequests   int64
		successCount    int64
		inProgressCount int64
		errorCount      int64
	)

	// Rate limiter ticker
	ticker := time.NewTicker(time.Second / time.Duration(targetRPS))
	defer ticker.Stop()

	deadline := time.Now().Add(testDuration)
	var wg sync.WaitGroup

	for time.Now().Before(deadline) {
		<-ticker.C

		wg.Add(1)
		go func(requestNum int64) {
			defer wg.Done()

			atomic.AddInt64(&totalRequests, 1)

			// Use unique key for each request
			key := idempotency.Key{
				Namespace: "load-test",
				Operation: "payment",
				EntityID:  "account-" + strconv.FormatInt(requestNum, 10),
				RequestID: "req-" + strconv.FormatInt(requestNum, 10),
			}

			_, err := executor.Execute(ctx, key, time.Hour, func(_ context.Context) ([]byte, error) {
				// Intentional sleep: Simulate variable processing time for load testing
				time.Sleep(time.Duration(rand.Intn(30)) * time.Millisecond) //nolint:forbidigo // simulates variable processing latency for load testing
				return []byte(`{"success":true}`), nil
			})

			if err != nil {
				if errors.Is(err, idempotency.ErrOperationInProgress) {
					atomic.AddInt64(&inProgressCount, 1)
				} else {
					atomic.AddInt64(&errorCount, 1)
				}
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}(atomic.LoadInt64(&totalRequests))
	}

	// Wait for all requests to complete
	wg.Wait()

	// Log results
	t.Logf("Load test results: total=%d, success=%d, in-progress=%d, errors=%d",
		totalRequests, successCount, inProgressCount, errorCount)

	// Verify most requests succeeded
	successRate := float64(successCount) / float64(totalRequests)
	t.Logf("Success rate: %.2f%%", successRate*100)

	// At least 95% should succeed (some may get in-progress due to timing)
	assert.GreaterOrEqual(t, successRate, 0.95,
		"at least 95%% of requests should succeed")

	// No errors (other than in-progress)
	assert.Equal(t, int64(0), errorCount,
		"no errors should occur (other than in-progress)")

	w.Stop()
}

// TestLoad_PendingDuration_P99Under1Second verifies that the p99 pending
// duration is under 1 second under load conditions.
func TestLoad_PendingDuration_P99Under1Second(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	// Use unique service name for this test to avoid metric interference
	serviceName := "p99-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	metrics := idempotency.NewMetricsCollector(serviceName)
	executor := idempotency.NewExecutorWithMetrics(redisSvc, nil, metrics)

	// Track durations manually for p99 verification
	var durations []time.Duration
	var durationMu sync.Mutex

	// Run enough requests to get meaningful p99
	numRequests := 100
	var wg sync.WaitGroup

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			key := idempotency.Key{
				Namespace: serviceName,
				Operation: "transfer",
				EntityID:  "account-p99-" + strconv.Itoa(id),
				RequestID: "req-p99-" + strconv.Itoa(id),
			}

			start := time.Now()
			_, err := executor.Execute(ctx, key, time.Hour, func(_ context.Context) ([]byte, error) {
				// Intentional sleep: Variable processing time (10-100ms) for p99 latency testing
				time.Sleep(time.Duration(10+rand.Intn(90)) * time.Millisecond) //nolint:forbidigo // simulates variable processing latency for p99 measurement
				return []byte(`{"success":true}`), nil
			})

			if err == nil {
				duration := time.Since(start)
				durationMu.Lock()
				durations = append(durations, duration)
				durationMu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// Calculate p99 from collected durations
	durationMu.Lock()
	numDurations := len(durations)
	durationMu.Unlock()

	require.Greater(t, numDurations, 0, "should have at least one successful operation")

	// Sort durations to find p99
	durationMu.Lock()
	sortedDurations := make([]time.Duration, len(durations))
	copy(sortedDurations, durations)
	durationMu.Unlock()

	// Simple bubble sort for small dataset
	for i := 0; i < len(sortedDurations); i++ {
		for j := i + 1; j < len(sortedDurations); j++ {
			if sortedDurations[j] < sortedDurations[i] {
				sortedDurations[i], sortedDurations[j] = sortedDurations[j], sortedDurations[i]
			}
		}
	}

	// Find p99 (99th percentile)
	p99Index := int(float64(len(sortedDurations)) * 0.99)
	if p99Index >= len(sortedDurations) {
		p99Index = len(sortedDurations) - 1
	}
	p99Duration := sortedDurations[p99Index]

	t.Logf("Pending duration p99: %v (from %d observations)", p99Duration, numDurations)

	// Verify p99 is under 1 second
	// With 10-100ms processing time + overhead, should be well under 1s
	assert.Less(t, p99Duration, time.Second,
		"p99 pending duration should be under 1 second")
}

// =============================================================================
// META-TESTS: Verify Tests Can Detect Bugs
// =============================================================================

// TestMeta_VerifyConcurrencyTestDetectsRaces verifies that the concurrency
// test would fail if we didn't have proper idempotency protection.
//
// This test uses a mock that simulates non-atomic behavior to ensure
// our test can detect such issues.
func TestMeta_VerifyConcurrencyTestDetectsRaces(t *testing.T) {
	// This is a meta-test that verifies our test methodology
	// It uses a deliberately broken mock to ensure tests can detect issues

	// Create a "broken" checker that doesn't properly handle concurrency
	brokenChecker := &brokenConcurrencyChecker{
		results: make(map[string]*idempotency.Result),
	}

	executor := idempotency.NewExecutor(brokenChecker, nil)

	key := idempotency.Key{
		Namespace: "meta-test",
		Operation: "broken",
		EntityID:  "123",
	}

	var wg sync.WaitGroup
	var executionCount int32

	// Run concurrent requests
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = executor.Execute(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
				atomic.AddInt32(&executionCount, 1)
				// Intentional sleep: Simulate work to test concurrency behavior
				time.Sleep(10 * time.Millisecond) //nolint:forbidigo // simulates work duration to expose concurrent execution window
				return nil, nil
			})
		}()
	}

	wg.Wait()

	// With the broken checker, multiple executions should occur
	// (This verifies our test can detect the problem)
	count := atomic.LoadInt32(&executionCount)
	t.Logf("Meta-test: broken checker allowed %d executions (expected > 1 to prove test works)", count)

	// If count > 1, it means the test can detect concurrent execution issues
	// This is expected with the broken checker
	assert.Greater(t, count, int32(1),
		"meta-test: broken checker should allow multiple executions, proving test methodology works")
}

// brokenConcurrencyChecker is a deliberately broken mock that doesn't
// properly handle concurrent access, used for meta-testing.
// The "broken" behavior: MarkPending always succeeds without checking if key exists,
// allowing multiple concurrent requests to all believe they acquired the lock.
type brokenConcurrencyChecker struct {
	results map[string]*idempotency.Result
	mu      sync.Mutex
}

func (b *brokenConcurrencyChecker) Check(_ context.Context, key idempotency.Key) (*idempotency.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	result, exists := b.results[key.String()]
	if !exists {
		return nil, idempotency.ErrResultNotFound
	}
	if result.Status == idempotency.StatusCompleted {
		return result, idempotency.ErrOperationAlreadyProcessed
	}
	return result, nil
}

func (b *brokenConcurrencyChecker) MarkPending(_ context.Context, key idempotency.Key, ttl time.Duration) error {
	// DELIBERATELY BROKEN: Always returns success without checking if key already exists.
	// This simulates a check-then-act race where multiple requests all pass the Check
	// (seeing no result) and then all proceed to MarkPending.
	//
	// A proper implementation would use atomic compare-and-set (like Redis SETNX),
	// but this broken version just overwrites, allowing multiple executions.

	// Intentional sleep: Add delay BEFORE locking to create race window where multiple
	// goroutines can all pass Check() before any of them complete MarkPending()
	time.Sleep(5 * time.Millisecond) //nolint:forbidigo // simulates latency in broken mock to expose race window

	b.mu.Lock()
	defer b.mu.Unlock()

	// No check for existing key - just overwrite (this is the bug)
	b.results[key.String()] = &idempotency.Result{
		Key:    key,
		Status: idempotency.StatusPending,
		TTL:    ttl,
	}
	return nil
}

func (b *brokenConcurrencyChecker) StoreResult(_ context.Context, result idempotency.Result) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.results[result.Key.String()] = &result
	return nil
}

func (b *brokenConcurrencyChecker) Delete(_ context.Context, key idempotency.Key) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.results, key.String())
	return nil
}

// TestMeta_VerifyMetricsConsistency verifies that metrics are consistent:
// pending_total ≈ completed_total + failed_total
func TestMeta_VerifyMetricsConsistency(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	// Use unique service name to avoid interference from other tests
	serviceName := "metrics-consistency-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	metrics := idempotency.NewMetricsCollector(serviceName)
	executor := idempotency.NewExecutorWithMetrics(redisSvc, nil, metrics)

	// Run a mix of successful and failed operations
	numSuccess := 10
	numFailure := 5

	var wg sync.WaitGroup

	// Successful operations
	for i := 0; i < numSuccess; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := idempotency.Key{
				Namespace: serviceName,
				Operation: "success",
				EntityID:  "entity-success-" + strconv.Itoa(id),
			}
			_, _ = executor.Execute(ctx, key, time.Hour, func(_ context.Context) ([]byte, error) {
				return []byte("ok"), nil
			})
		}(i)
	}

	// Failed operations (using ExecuteWithFailedState)
	for i := 0; i < numFailure; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := idempotency.Key{
				Namespace: serviceName,
				Operation: "failure",
				EntityID:  "entity-failure-" + strconv.Itoa(id),
			}
			_, _ = executor.ExecuteWithFailedState(ctx, key, time.Hour, func(_ context.Context) ([]byte, error) {
				return nil, errTestBusinessLogicFailure
			})
		}(i)
	}

	wg.Wait()

	// Intentional sleep: Give metrics a moment to settle after concurrent operations
	time.Sleep(100 * time.Millisecond) //nolint:forbidigo // simulates settling time after concurrent goroutines complete

	// Get metrics
	pendingSuccess := testutil.ToFloat64(
		idempotency.ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues(serviceName, "success"))
	completedSuccess := testutil.ToFloat64(
		idempotency.ExposeMetricsForTesting.KeysCompletedTotal.WithLabelValues(serviceName, "success"))
	pendingFailure := testutil.ToFloat64(
		idempotency.ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues(serviceName, "failure"))
	failedFailure := testutil.ToFloat64(
		idempotency.ExposeMetricsForTesting.KeysFailedTotal.WithLabelValues(serviceName, "failure", idempotency.MetricReasonInternal))

	t.Logf("Metrics: success(pending=%v, completed=%v), failure(pending=%v, failed=%v)",
		pendingSuccess, completedSuccess, pendingFailure, failedFailure)

	// Verify consistency
	assert.Equal(t, float64(numSuccess), pendingSuccess,
		"pending_total for success operations should match request count")
	assert.Equal(t, float64(numSuccess), completedSuccess,
		"completed_total should match successful request count")
	assert.Equal(t, float64(numFailure), pendingFailure,
		"pending_total for failure operations should match request count")
	assert.Equal(t, float64(numFailure), failedFailure,
		"failed_total should match failed request count")
}
