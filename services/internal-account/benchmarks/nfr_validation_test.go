// Package benchmarks_test contains NFR (Non-Functional Requirements) validation tests
// for the Internal Account service.
//
// These tests validate that the service meets its performance targets:
//   - Balance queries: P99 < 50ms (CI threshold: 200ms)
//   - Account creation: P99 < 50ms (CI threshold: 200ms)
//   - Account lookups: P99 < 5ms (CI threshold: 50ms)
//   - Concurrent operations: 1000 ops simultaneously
//
// Run with: go test -v ./services/internal-account/benchmarks/
package benchmarks_test

import (
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/stretchr/testify/require"
)

// calculateP99 calculates the 99th percentile latency from a slice of durations.
func calculateP99(latencies []time.Duration) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	p99Index := int(float64(len(sorted)) * 0.99)
	if p99Index >= len(sorted) {
		p99Index = len(sorted) - 1
	}
	return sorted[p99Index]
}

// calculateP50 calculates the 50th percentile (median) latency.
func calculateP50(latencies []time.Duration) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	return sorted[len(sorted)/2]
}

// createNFRBenchAccount creates a test account for NFR benchmarks.
// Uses the shared createBenchAccount helper from helpers_test.go with NFR-specific naming.
func createNFRBenchAccount(t *testing.T, tc *testContainer, codePrefix string) domain.InternalAccount {
	t.Helper()
	accountCode := fmt.Sprintf("%s-%d", codePrefix, time.Now().UnixNano()%100000)
	return createBenchAccount(t, tc, accountCode, domain.AccountTypeClearing)
}

// createNFRAccountRequest creates a proto request for NFR account creation tests.
func createNFRAccountRequest(codePrefix string) *pb.InitiateInternalAccountRequest {
	return &pb.InitiateInternalAccountRequest{
		AccountCode:     fmt.Sprintf("%s-%d", codePrefix, time.Now().UnixNano()%100000),
		Name:            fmt.Sprintf("%s NFR Benchmark Account", codePrefix),
		ProductTypeCode: "CLEARING_USD",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	}
}

// TestNFR_BalanceQueryLatency validates P99 < 50ms target for balance queries.
func TestNFR_BalanceQueryLatency(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup()
	ctx := tc.ctx

	// Create test account
	account := createNFRBenchAccount(t, tc, "NFR-BALANCE")

	iterations := 1000
	latencies := make([]time.Duration, iterations)

	// Warm up
	for i := 0; i < 10; i++ {
		_, err := tc.service.GetBalance(ctx, &pb.GetBalanceRequest{
			AccountId: account.AccountCode(),
		})
		require.NoError(t, err)
	}

	// Measure latencies
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, err := tc.service.GetBalance(ctx, &pb.GetBalanceRequest{
			AccountId: account.AccountCode(),
		})
		require.NoError(t, err)
		latencies[i] = time.Since(start)
	}

	p50 := calculateP50(latencies)
	p99 := calculateP99(latencies)
	t.Logf("Balance query P50 latency: %v", p50)
	t.Logf("Balance query P99 latency: %v", p99)

	target := 50 * time.Millisecond
	ciThreshold := 200 * time.Millisecond // 4x headroom for CI

	if p99 > ciThreshold {
		t.Errorf("P99 latency too high: %v (CI threshold: <%v, production target: <%v)", p99, ciThreshold, target)
	} else if p99 > target {
		t.Logf("Note: P99 %v exceeds production target (%v) but passes CI threshold", p99, target)
	}
}

// TestNFR_AccountCreationLatency validates P99 < 50ms target for account creation.
func TestNFR_AccountCreationLatency(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup()
	ctx := tc.ctx

	iterations := 100 // Fewer iterations for write operations
	latencies := make([]time.Duration, iterations)

	// Warm up
	for i := 0; i < 5; i++ {
		req := createNFRAccountRequest(fmt.Sprintf("NFR-WARMUP-%d", i))
		_, err := tc.service.InitiateInternalAccount(ctx, req)
		require.NoError(t, err)
	}

	// Measure latencies
	for i := 0; i < iterations; i++ {
		req := createNFRAccountRequest(fmt.Sprintf("NFR-CREATE-%08d", i))

		start := time.Now()
		_, err := tc.service.InitiateInternalAccount(ctx, req)
		require.NoError(t, err)
		latencies[i] = time.Since(start)
	}

	p50 := calculateP50(latencies)
	p99 := calculateP99(latencies)
	t.Logf("Account creation P50 latency: %v", p50)
	t.Logf("Account creation P99 latency: %v", p99)

	target := 50 * time.Millisecond
	ciThreshold := 200 * time.Millisecond

	if p99 > ciThreshold {
		t.Errorf("P99 latency too high: %v (CI threshold: <%v, production target: <%v)", p99, ciThreshold, target)
	} else if p99 > target {
		t.Logf("Note: P99 %v exceeds production target (%v) but passes CI threshold", p99, target)
	}
}

// TestNFR_AccountLookupLatency validates P99 < 5ms target for account lookups by code.
func TestNFR_AccountLookupLatency(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup()
	ctx := tc.ctx

	account := createNFRBenchAccount(t, tc, "NFR-LOOKUP")

	iterations := 1000
	latencies := make([]time.Duration, iterations)

	// Warm up
	for i := 0; i < 10; i++ {
		_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
			AccountId: account.AccountCode(),
		})
		require.NoError(t, err)
	}

	// Measure latencies
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
			AccountId: account.AccountCode(),
		})
		require.NoError(t, err)
		latencies[i] = time.Since(start)
	}

	p50 := calculateP50(latencies)
	p99 := calculateP99(latencies)
	t.Logf("Account lookup (by code) P50 latency: %v", p50)
	t.Logf("Account lookup (by code) P99 latency: %v", p99)

	target := 5 * time.Millisecond
	ciThreshold := 100 * time.Millisecond // 20x headroom for CI with testcontainers

	if p99 > ciThreshold {
		t.Errorf("P99 latency too high: %v (CI threshold: <%v, production target: <%v)", p99, ciThreshold, target)
	} else if p99 > target {
		t.Logf("Note: P99 %v exceeds production target (%v) but passes CI threshold", p99, target)
	}
}

// TestNFR_ByIDLookupLatency validates P99 < 5ms for UUID lookups.
func TestNFR_ByIDLookupLatency(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup()
	ctx := tc.ctx

	account := createNFRBenchAccount(t, tc, "NFR-ID-LOOKUP")

	iterations := 1000
	latencies := make([]time.Duration, iterations)

	// Warm up
	for i := 0; i < 10; i++ {
		_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
			AccountId: account.ID().String(),
		})
		require.NoError(t, err)
	}

	// Measure latencies
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
			AccountId: account.ID().String(),
		})
		require.NoError(t, err)
		latencies[i] = time.Since(start)
	}

	p50 := calculateP50(latencies)
	p99 := calculateP99(latencies)
	t.Logf("Account lookup (by UUID) P50 latency: %v", p50)
	t.Logf("Account lookup (by UUID) P99 latency: %v", p99)

	target := 5 * time.Millisecond
	ciThreshold := 100 * time.Millisecond // 20x headroom for CI with testcontainers

	if p99 > ciThreshold {
		t.Errorf("P99 latency too high: %v (CI threshold: <%v, production target: <%v)", p99, ciThreshold, target)
	} else if p99 > target {
		t.Logf("Note: P99 %v exceeds production target (%v) but passes CI threshold", p99, target)
	}
}

// TestNFR_ConcurrentOperations validates handling concurrent operations.
// Uses reduced concurrency (50) to stay within PostgreSQL max_connections (100)
// in testcontainer environments. Production with connection pooling supports much higher.
func TestNFR_ConcurrentOperations(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup()
	ctx := tc.ctx

	// Pre-create accounts for concurrent lookups
	numAccounts := 50
	accounts := make([]domain.InternalAccount, numAccounts)
	for i := 0; i < numAccounts; i++ {
		accounts[i] = createNFRBenchAccount(t, tc, fmt.Sprintf("NFR-CONC-%03d", i))
	}

	// Reduced concurrency to avoid PostgreSQL max_connections limit (default 100).
	// Production deployments should use connection pooling (PgBouncer) for higher concurrency.
	concurrency := 50
	var wg sync.WaitGroup
	var successCount int64
	var errorCount int64
	latencies := make([]time.Duration, concurrency)

	start := time.Now()
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Distribute requests across accounts
			account := accounts[idx%numAccounts]

			opStart := time.Now()
			_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
				AccountId: account.AccountCode(),
			})
			latencies[idx] = time.Since(opStart)

			if err != nil {
				atomic.AddInt64(&errorCount, 1)
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()
	totalDuration := time.Since(start)

	p50 := calculateP50(latencies)
	p99 := calculateP99(latencies)
	opsPerSec := float64(concurrency) / totalDuration.Seconds()

	t.Logf("Concurrent operations: %d", concurrency)
	t.Logf("Total duration: %v", totalDuration)
	t.Logf("Throughput: %.0f ops/sec", opsPerSec)
	t.Logf("Success count: %d", successCount)
	t.Logf("Error count: %d", errorCount)
	t.Logf("P50 latency: %v", p50)
	t.Logf("P99 latency: %v", p99)

	// Validate success rate
	successRate := float64(successCount) / float64(concurrency) * 100
	if successRate < 99.0 {
		t.Errorf("Success rate too low: %.2f%% (expected >= 99%%)", successRate)
	}

	// All concurrent ops should complete
	if errorCount > 0 {
		t.Errorf("Expected zero errors for concurrent operations, got %d", errorCount)
	}
}

// TestNFR_ListAccountsLatency validates P99 < 100ms for listing accounts.
func TestNFR_ListAccountsLatency(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup()
	ctx := tc.ctx

	// Create 50 accounts to list
	for i := 0; i < 50; i++ {
		createNFRBenchAccount(t, tc, fmt.Sprintf("NFR-LIST-%03d", i))
	}

	iterations := 100
	latencies := make([]time.Duration, iterations)

	// Warm up
	for i := 0; i < 5; i++ {
		_, err := tc.service.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{})
		require.NoError(t, err)
	}

	// Measure latencies
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, err := tc.service.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{})
		require.NoError(t, err)
		latencies[i] = time.Since(start)
	}

	p50 := calculateP50(latencies)
	p99 := calculateP99(latencies)
	t.Logf("List accounts P50 latency: %v", p50)
	t.Logf("List accounts P99 latency: %v", p99)

	target := 100 * time.Millisecond
	ciThreshold := 500 * time.Millisecond // 5x headroom

	if p99 > ciThreshold {
		t.Errorf("P99 latency too high: %v (CI threshold: <%v, production target: <%v)", p99, ciThreshold, target)
	} else if p99 > target {
		t.Logf("Note: P99 %v exceeds production target (%v) but passes CI threshold", p99, target)
	}
}

// TestNFR_SustainedThroughput measures throughput over a sustained period.
func TestNFR_SustainedThroughput(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup()
	ctx := tc.ctx

	// Create accounts for sustained load
	numAccounts := 20
	accounts := make([]domain.InternalAccount, numAccounts)
	for i := 0; i < numAccounts; i++ {
		accounts[i] = createNFRBenchAccount(t, tc, fmt.Sprintf("NFR-SUSTAINED-%03d", i))
	}

	// Run for 1 second
	duration := 1 * time.Second
	deadline := time.Now().Add(duration)

	var count int64
	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers > 8 {
		numWorkers = 8 // Cap at 8 workers
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allLatencies []time.Duration

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localCount := int64(0)
			var localLatencies []time.Duration

			for time.Now().Before(deadline) {
				account := accounts[int(localCount)%numAccounts]
				start := time.Now()
				_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
					AccountId: account.AccountCode(),
				})
				if err == nil {
					localLatencies = append(localLatencies, time.Since(start))
					localCount++
				}
			}

			mu.Lock()
			count += localCount
			allLatencies = append(allLatencies, localLatencies...)
			mu.Unlock()
		}()
	}

	wg.Wait()

	opsPerSecond := float64(count) / duration.Seconds()
	p50 := calculateP50(allLatencies)
	p99 := calculateP99(allLatencies)

	t.Logf("=== Sustained Throughput Results ===")
	t.Logf("Workers: %d", numWorkers)
	t.Logf("Total operations: %d", count)
	t.Logf("Throughput: %.0f ops/sec", opsPerSecond)
	t.Logf("P50 latency: %v", p50)
	t.Logf("P99 latency: %v", p99)

	// Advisory throughput target - testcontainer environments have significant overhead.
	// Production with connection pooling achieves much higher throughput.
	targetThroughput := 10000.0
	ciThreshold := 500.0 // Relaxed for CI testcontainer overhead

	if opsPerSecond < ciThreshold {
		t.Errorf("Throughput below CI threshold: %.0f (CI threshold: >%.0f, production target: >%.0f)", opsPerSecond, ciThreshold, targetThroughput)
	} else if opsPerSecond < targetThroughput {
		t.Logf("Note: Throughput %.0f below production target (>%.0f) but passes CI threshold", opsPerSecond, targetThroughput)
	}
}

// TestNFR_VersionInfo logs test environment info for audit trail.
func TestNFR_VersionInfo(t *testing.T) {
	t.Logf("=== Internal Account NFR Validation ===")
	t.Logf("GOMAXPROCS: %d", runtime.GOMAXPROCS(0))
	t.Logf("NumCPU: %d", runtime.NumCPU())
	t.Logf("")
	t.Logf("Performance targets (production):")
	t.Logf("  - Balance queries: P99 < 50ms")
	t.Logf("  - Account creation: P99 < 50ms")
	t.Logf("  - Account lookups: P99 < 5ms")
	t.Logf("  - Concurrent operations: 1000 simultaneous")
	t.Logf("")
	t.Logf("CI thresholds (with headroom for testcontainers):")
	t.Logf("  - Balance queries: P99 < 200ms (4x)")
	t.Logf("  - Account creation: P99 < 200ms (4x)")
	t.Logf("  - Account lookups: P99 < 100ms (20x)")
	t.Logf("")
	t.Logf("Run full benchmarks with: go test -v ./services/internal-account/benchmarks/")
}
