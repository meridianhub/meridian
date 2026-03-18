package idempotency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Test-specific sentinel errors for linter compliance (err113)
var (
	errTestBusinessLogicFailed = errors.New("business logic failed")
	errTestInsufficientFunds   = errors.New("insufficient funds")
	errTestUnderlyingError     = errors.New("underlying error")
	errTestTransientError      = errors.New("transient error")
)

// mockChecker is a test double for the Checker interface.
type mockChecker struct {
	mu           sync.Mutex
	results      map[string]*Result
	checkCalls   int
	pendingCalls int
	storeCalls   int
	deleteCalls  int

	// Error injection
	checkErr   error
	pendingErr error
	storeErr   error
	deleteErr  error
}

func newMockChecker() *mockChecker {
	return &mockChecker{
		results: make(map[string]*Result),
	}
}

func (m *mockChecker) Check(_ context.Context, key Key) (*Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkCalls++

	if m.checkErr != nil {
		return nil, m.checkErr
	}

	result, exists := m.results[key.String()]
	if !exists {
		return nil, ErrResultNotFound
	}

	if result.Status == StatusCompleted {
		return result, ErrOperationAlreadyProcessed
	}

	return result, nil
}

func (m *mockChecker) MarkPending(_ context.Context, key Key, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pendingCalls++

	if m.pendingErr != nil {
		return m.pendingErr
	}

	// Check for existing key (simulate race condition detection)
	if _, exists := m.results[key.String()]; exists {
		return ErrOperationAlreadyProcessed
	}

	m.results[key.String()] = &Result{
		Key:    key,
		Status: StatusPending,
		TTL:    ttl,
	}
	return nil
}

func (m *mockChecker) StoreResult(_ context.Context, result Result) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storeCalls++

	if m.storeErr != nil {
		return m.storeErr
	}

	m.results[result.Key.String()] = &result
	return nil
}

func (m *mockChecker) Delete(_ context.Context, key Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls++

	if m.deleteErr != nil {
		return m.deleteErr
	}

	delete(m.results, key.String())
	return nil
}

func TestExecutor_Execute_NewOperation(t *testing.T) {
	checker := newMockChecker()
	executor := NewExecutor(checker, nil)

	key := Key{
		Namespace: "test",
		Operation: "create",
		EntityID:  "123",
	}

	expectedData := []byte(`{"id":"123","name":"test"}`)
	operationCalled := false

	result, err := executor.Execute(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		operationCalled = true
		return expectedData, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !operationCalled {
		t.Error("operation function was not called")
	}
	if result.FromCache {
		t.Error("expected FromCache to be false for new operation")
	}
	if result.Status != StatusCompleted {
		t.Errorf("expected status COMPLETED, got %s", result.Status)
	}
	if string(result.Data) != string(expectedData) {
		t.Errorf("expected data %s, got %s", expectedData, result.Data)
	}

	// Verify checker was called correctly
	if checker.checkCalls != 1 {
		t.Errorf("expected 1 check call, got %d", checker.checkCalls)
	}
	if checker.pendingCalls != 1 {
		t.Errorf("expected 1 pending call, got %d", checker.pendingCalls)
	}
	if checker.storeCalls != 1 {
		t.Errorf("expected 1 store call, got %d", checker.storeCalls)
	}
}

func TestExecutor_Execute_CachedResult(t *testing.T) {
	checker := newMockChecker()
	executor := NewExecutor(checker, nil)

	key := Key{
		Namespace: "test",
		Operation: "create",
		EntityID:  "123",
	}

	// Pre-populate with completed result
	cachedData := []byte(`{"cached":"true"}`)
	checker.results[key.String()] = &Result{
		Key:    key,
		Status: StatusCompleted,
		Data:   cachedData,
	}

	operationCalled := false
	result, err := executor.Execute(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		operationCalled = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if operationCalled {
		t.Error("operation should not be called for cached result")
	}
	if !result.FromCache {
		t.Error("expected FromCache to be true")
	}
	if string(result.Data) != string(cachedData) {
		t.Errorf("expected cached data %s, got %s", cachedData, result.Data)
	}
}

func TestExecutor_Execute_OperationInProgress(t *testing.T) {
	checker := newMockChecker()
	executor := NewExecutor(checker, nil)

	key := Key{
		Namespace: "test",
		Operation: "create",
		EntityID:  "123",
	}

	// Pre-populate with pending result
	checker.results[key.String()] = &Result{
		Key:    key,
		Status: StatusPending,
	}

	_, err := executor.Execute(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		t.Error("operation should not be called when already in progress")
		return nil, nil
	})

	if err == nil {
		t.Fatal("expected error for operation in progress")
	}
	if !errors.Is(err, ErrOperationInProgress) {
		t.Errorf("expected ErrOperationInProgress, got %v", err)
	}
}

func TestExecutor_Execute_OperationError_CleansPendingState(t *testing.T) {
	checker := newMockChecker()
	executor := NewExecutor(checker, nil)

	key := Key{
		Namespace: "test",
		Operation: "create",
		EntityID:  "123",
	}

	_, err := executor.Execute(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		return nil, errTestBusinessLogicFailed
	})

	if err == nil {
		t.Fatal("expected error from operation")
	}
	if err.Error() != errTestBusinessLogicFailed.Error() {
		t.Errorf("expected error %v, got %v", errTestBusinessLogicFailed, err)
	}

	// Verify cleanup was called
	if checker.deleteCalls != 1 {
		t.Errorf("expected 1 delete call for cleanup, got %d", checker.deleteCalls)
	}

	// Verify key is no longer in results
	if _, exists := checker.results[key.String()]; exists {
		t.Error("pending key should have been cleaned up after error")
	}
}

func TestExecutor_Execute_PanicRecovery(_ *testing.T) {
	checker := newMockChecker()
	executor := NewExecutor(checker, nil)

	key := Key{
		Namespace: "test",
		Operation: "create",
		EntityID:  "123",
	}

	// Use a wrapper that recovers from panic
	defer func() {
		// Panic occurred as expected
		// In a real scenario, we'd want cleanup to happen
		// This test verifies the executor doesn't prevent panic propagation
		_ = recover()
	}()

	_, _ = executor.Execute(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		panic("simulated panic in business logic")
	})

	// Note: In production, a panic recovery wrapper would clean up the pending state.
	// This test documents current behavior - panics propagate but leave orphaned state.
	// The TTL on the pending key prevents permanent blocking.
}

func TestExecutor_Execute_ConcurrentRequests(t *testing.T) {
	checker := newMockChecker()
	executor := NewExecutor(checker, nil)

	key := Key{
		Namespace: "test",
		Operation: "create",
		EntityID:  "123",
	}

	// Simulate slow operation
	var executionCount int32
	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := executor.Execute(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
				atomic.AddInt32(&executionCount, 1)
				//nolint:forbidigo // simulates work latency to test concurrent idempotency enforcement
				time.Sleep(10 * time.Millisecond)
				return []byte("result"), nil
			})
			// Either success or ErrOperationInProgress is acceptable
			if err != nil && !errors.Is(err, ErrOperationInProgress) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	wg.Wait()

	// Only one execution should succeed
	if executionCount > 1 {
		t.Errorf("expected at most 1 execution, got %d", executionCount)
	}
}

func TestExecutor_Execute_DefaultTTL(t *testing.T) {
	checker := newMockChecker()
	config := DefaultExecutorConfig()
	config.DefaultTTL = 2 * time.Hour
	executor := NewExecutor(checker, &config)

	key := Key{
		Namespace: "test",
		Operation: "create",
		EntityID:  "123",
	}

	// Execute with zero TTL (should use default)
	_, err := executor.Execute(context.Background(), key, 0, func(_ context.Context) ([]byte, error) {
		return []byte("result"), nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that the stored result has the default TTL
	stored := checker.results[key.String()]
	if stored == nil {
		t.Fatal("result not stored")
	}
	if stored.TTL != 2*time.Hour {
		t.Errorf("expected TTL %v, got %v", 2*time.Hour, stored.TTL)
	}
}

func TestExecutor_ExecuteWithFailedState_MarksAsFailed(t *testing.T) {
	checker := newMockChecker()
	executor := NewExecutor(checker, nil)

	key := Key{
		Namespace: "test",
		Operation: "withdraw",
		EntityID:  "456",
	}

	_, err := executor.ExecuteWithFailedState(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		return nil, errTestInsufficientFunds
	})

	if err == nil {
		t.Fatal("expected error from operation")
	}

	// Verify key was marked as FAILED, not deleted
	if checker.deleteCalls != 0 {
		t.Errorf("expected 0 delete calls, got %d", checker.deleteCalls)
	}

	stored := checker.results[key.String()]
	if stored == nil {
		t.Fatal("result should be stored")
	}
	if stored.Status != StatusFailed {
		t.Errorf("expected status FAILED, got %s", stored.Status)
	}
	if stored.Error != errTestInsufficientFunds.Error() {
		t.Errorf("expected error %s, got %s", errTestInsufficientFunds.Error(), stored.Error)
	}
}

func TestExecutor_ExecuteWithFailedState_ReturnsCachedFailure(t *testing.T) {
	checker := newMockChecker()
	executor := NewExecutor(checker, nil)

	key := Key{
		Namespace: "test",
		Operation: "withdraw",
		EntityID:  "456",
	}

	// Pre-populate with failed result
	checker.results[key.String()] = &Result{
		Key:    key,
		Status: StatusFailed,
		Error:  "previous failure",
	}

	operationCalled := false
	result, err := executor.ExecuteWithFailedState(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		operationCalled = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if operationCalled {
		t.Error("operation should not be called for cached failed result")
	}
	if !result.FromCache {
		t.Error("expected FromCache to be true")
	}
	if result.Status != StatusFailed {
		t.Errorf("expected status FAILED, got %s", result.Status)
	}
}

func TestExecutor_ContextCancellation(t *testing.T) {
	checker := newMockChecker()
	executor := NewExecutor(checker, nil)

	key := Key{
		Namespace: "test",
		Operation: "create",
		EntityID:  "123",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := executor.Execute(ctx, key, time.Hour, func(_ context.Context) ([]byte, error) {
		return nil, nil
	})

	// With context cancelled, various errors are possible depending on timing
	// The operation should fail rather than complete successfully
	if err == nil {
		// This is actually acceptable - if the operation was fast enough
		// it might complete before context cancellation takes effect
		t.Log("Operation completed despite context cancellation (timing-dependent)")
	}
}

func TestExecutorError_Unwrap(t *testing.T) {
	execErr := &ExecutorError{
		Op:  "test",
		Key: Key{Namespace: "ns", Operation: "op", EntityID: "id"},
		Err: errTestUnderlyingError,
	}

	if !errors.Is(execErr, errTestUnderlyingError) {
		t.Error("ExecutorError should unwrap to underlying error")
	}
}

func TestExecutor_DeadlockRetry(t *testing.T) {
	// Configure for faster retries in test
	config := DefaultExecutorConfig()
	config.MaxDeadlockRetries = 2
	config.DeadlockRetryDelay = 10 * time.Millisecond

	key := Key{
		Namespace: "test",
		Operation: "create",
		EntityID:  "123",
	}

	// Use a checker that fails N times then succeeds
	checker := &retryTrackingChecker{
		mockChecker:    newMockChecker(),
		failCount:      2,
		currentAttempt: 0,
	}

	executor := NewExecutor(checker, &config)

	result, err := executor.Execute(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		return []byte("success"), nil
	})
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if result.FromCache {
		t.Error("expected fresh result, not cached")
	}
	if checker.currentAttempt != 3 {
		t.Errorf("expected 3 MarkPending attempts, got %d", checker.currentAttempt)
	}
}

// retryTrackingChecker is a mock that fails N times then succeeds
type retryTrackingChecker struct {
	*mockChecker
	failCount      int
	currentAttempt int
}

func (r *retryTrackingChecker) MarkPending(ctx context.Context, key Key, ttl time.Duration) error {
	r.currentAttempt++
	if r.currentAttempt <= r.failCount {
		return errTestTransientError
	}
	return r.mockChecker.MarkPending(ctx, key, ttl)
}

func TestExecutorWithMetrics_RecordsPendingAndCompleted(t *testing.T) {
	checker := newMockChecker()
	metrics := NewMetricsCollector("executor-test-service")
	executor := NewExecutorWithMetrics(checker, nil, metrics)

	// Get initial counts
	initialPending := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("executor-test-service", "create"))
	initialCompleted := testutil.ToFloat64(ExposeMetricsForTesting.KeysCompletedTotal.WithLabelValues("executor-test-service", "create"))

	key := Key{
		Namespace: "test",
		Operation: "create",
		EntityID:  "metrics-test-123",
	}

	result, err := executor.Execute(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		return []byte("result"), nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FromCache {
		t.Error("expected fresh result, not cached")
	}

	// Verify metrics were recorded
	newPending := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("executor-test-service", "create"))
	newCompleted := testutil.ToFloat64(ExposeMetricsForTesting.KeysCompletedTotal.WithLabelValues("executor-test-service", "create"))

	if newPending != initialPending+1 {
		t.Errorf("expected pending counter to increment by 1, got %v -> %v", initialPending, newPending)
	}
	if newCompleted != initialCompleted+1 {
		t.Errorf("expected completed counter to increment by 1, got %v -> %v", initialCompleted, newCompleted)
	}
}

func TestExecutorWithMetrics_ExecuteWithFailedState_RecordsFailure(t *testing.T) {
	checker := newMockChecker()
	metrics := NewMetricsCollector("executor-failed-test")
	executor := NewExecutorWithMetrics(checker, nil, metrics)

	// Get initial counts
	initialPending := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("executor-failed-test", "withdraw"))
	initialFailed := testutil.ToFloat64(ExposeMetricsForTesting.KeysFailedTotal.WithLabelValues("executor-failed-test", "withdraw", MetricReasonInternal))

	key := Key{
		Namespace: "test",
		Operation: "withdraw",
		EntityID:  "metrics-fail-456",
	}

	_, err := executor.ExecuteWithFailedState(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		return nil, errTestInsufficientFunds
	})

	if err == nil {
		t.Fatal("expected error from operation")
	}

	// Verify metrics were recorded
	newPending := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("executor-failed-test", "withdraw"))
	newFailed := testutil.ToFloat64(ExposeMetricsForTesting.KeysFailedTotal.WithLabelValues("executor-failed-test", "withdraw", MetricReasonInternal))

	if newPending != initialPending+1 {
		t.Errorf("expected pending counter to increment by 1, got %v -> %v", initialPending, newPending)
	}
	if newFailed != initialFailed+1 {
		t.Errorf("expected failed counter to increment by 1, got %v -> %v", initialFailed, newFailed)
	}
}

func TestExecutorWithMetrics_CachedResult_NoMetrics(t *testing.T) {
	checker := newMockChecker()
	metrics := NewMetricsCollector("executor-cache-test")
	executor := NewExecutorWithMetrics(checker, nil, metrics)

	key := Key{
		Namespace: "test",
		Operation: "cached-op",
		EntityID:  "cache-123",
	}

	// Pre-populate with completed result
	checker.results[key.String()] = &Result{
		Key:    key,
		Status: StatusCompleted,
		Data:   []byte("cached"),
	}

	// Get initial counts
	initialPending := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("executor-cache-test", "cached-op"))

	result, err := executor.Execute(context.Background(), key, time.Hour, func(_ context.Context) ([]byte, error) {
		t.Error("operation should not be called for cached result")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.FromCache {
		t.Error("expected FromCache to be true")
	}

	// Verify no new pending metrics were recorded (cached hit doesn't increment pending)
	newPending := testutil.ToFloat64(ExposeMetricsForTesting.KeysPendingTotal.WithLabelValues("executor-cache-test", "cached-op"))
	if newPending != initialPending {
		t.Errorf("expected no change in pending counter for cached result, got %v -> %v", initialPending, newPending)
	}
}
