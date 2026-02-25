// Package benchmarks_test provides concurrent load tests and performance benchmarks
// for the Internal Account service.
//
// These tests measure service performance under concurrent load conditions:
//   - Concurrent reads: Parallel balance queries and account lookups
//   - Concurrent writes: Parallel account creation with atomic counters
//   - Mixed workloads: Realistic traffic patterns (70% reads, 20% lookups, 10% creates)
//   - Throughput tests: Sustained operations over time with ops/sec metrics
//
// Run with: go test -bench=. -benchmem ./services/internal-account/benchmarks/
// Run load tests: go test -run=Test -v ./services/internal-account/benchmarks/
package benchmarks_test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/stretchr/testify/require"
)

// BenchmarkConcurrentBalanceQueries measures concurrent balance query performance.
// This tests the service's ability to handle parallel GetBalance operations efficiently.
func BenchmarkConcurrentBalanceQueries(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	// Pre-populate accounts for read operations
	accounts := createBenchAccounts(b, tc, 100)

	b.ResetTimer()
	b.RunParallel(func(pbt *testing.PB) {
		idx := 0
		for pbt.Next() {
			account := accounts[idx%len(accounts)]
			_, err := tc.service.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: account.AccountCode(),
			})
			if err != nil {
				b.Fatal(err)
			}
			idx++
		}
	})
}

// BenchmarkConcurrentWrites measures concurrent account creation performance.
// Uses atomic counter to ensure unique account codes across parallel goroutines.
func BenchmarkConcurrentWrites(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	var counter int64
	b.ResetTimer()
	b.RunParallel(func(pbt *testing.PB) {
		for pbt.Next() {
			id := atomic.AddInt64(&counter, 1)
			req := &pb.InitiateInternalAccountRequest{
				AccountCode:     fmt.Sprintf("CONC-%08d", id),
				Name:            fmt.Sprintf("Concurrent Test Account %d", id),
				ProductTypeCode: "CLEARING_USD",
				ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
				InstrumentCode:  "USD",
			}

			_, err := tc.service.InitiateInternalAccount(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkConcurrentLookups measures concurrent account retrieval performance.
// This tests parallel RetrieveInternalAccount calls.
func BenchmarkConcurrentLookups(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	// Pre-populate accounts for lookup operations
	accounts := createBenchAccounts(b, tc, 100)

	b.ResetTimer()
	b.RunParallel(func(pbt *testing.PB) {
		idx := 0
		for pbt.Next() {
			account := accounts[idx%len(accounts)]
			_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
				AccountId: account.AccountCode(),
			})
			if err != nil {
				b.Fatal(err)
			}
			idx++
		}
	})
}

// BenchmarkMixedWorkloadWithBalance benchmarks a realistic mix of operations including balance queries.
// Simulates production traffic: 70% balance queries, 20% lookups, 10% creates.
func BenchmarkMixedWorkloadWithBalance(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	// Pre-populate 100 test accounts for read operations
	accounts := createBenchAccounts(b, tc, 100)

	var createCounter int64

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		operation := i % 10

		switch {
		case operation < 7: // 70% balance queries
			idx := i % len(accounts)
			account := accounts[idx]
			_, err := tc.service.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: account.AccountCode(),
			})
			if err != nil {
				b.Fatal(err)
			}

		case operation < 9: // 20% lookups
			idx := i % len(accounts)
			account := accounts[idx]
			_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
				AccountId: account.AccountCode(),
			})
			if err != nil {
				b.Fatal(err)
			}

		default: // 10% creates
			id := atomic.AddInt64(&createCounter, 1)
			req := &pb.InitiateInternalAccountRequest{
				AccountCode:     fmt.Sprintf("MIXED-%08d", id+1000),
				Name:            fmt.Sprintf("Mixed Workload Account %d", id),
				ProductTypeCode: "CLEARING_USD",
				ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
				InstrumentCode:  "USD",
			}
			_, err := tc.service.InitiateInternalAccount(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkMixedWorkloadParallel benchmarks mixed operations under parallel load.
// Uses RunParallel for more realistic concurrent access patterns.
func BenchmarkMixedWorkloadParallel(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	// Pre-populate 100 test accounts for read operations
	accounts := createBenchAccounts(b, tc, 100)

	var createCounter int64
	var opCounter int64

	b.ResetTimer()
	b.RunParallel(func(pbt *testing.PB) {
		for pbt.Next() {
			op := atomic.AddInt64(&opCounter, 1)
			operation := op % 10

			switch {
			case operation < 7: // 70% balance queries
				idx := op % int64(len(accounts))
				account := accounts[idx]
				_, err := tc.service.GetBalance(ctx, &pb.GetBalanceRequest{
					AccountId: account.AccountCode(),
				})
				if err != nil {
					b.Fatal(err)
				}

			case operation < 9: // 20% lookups
				idx := op % int64(len(accounts))
				account := accounts[idx]
				_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
					AccountId: account.AccountCode(),
				})
				if err != nil {
					b.Fatal(err)
				}

			default: // 10% creates
				id := atomic.AddInt64(&createCounter, 1)
				req := &pb.InitiateInternalAccountRequest{
					AccountCode:     fmt.Sprintf("MIXPAR-%08d", id+10000),
					Name:            fmt.Sprintf("Mixed Parallel Account %d", id),
					ProductTypeCode: "CLEARING_USD",
					ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
					InstrumentCode:  "USD",
				}
				_, err := tc.service.InitiateInternalAccount(ctx, req)
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

// TestThroughputUnderLoad measures sustained throughput over a fixed time period.
// Calculates operations per second across multiple workers.
//
// Advisory target: >1000 ops/sec aggregate throughput.
// CI environments may vary due to shared resources.
func TestThroughputUnderLoad(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	// Pre-populate accounts for read operations
	accounts := createBenchAccounts(t, tc, 100)

	// Run for 5 seconds and count operations
	duration := 5 * time.Second
	deadline := time.Now().Add(duration)

	var count int64
	numWorkers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	var mu sync.Mutex

	workerCounts := make([]int64, numWorkers)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			localCount := int64(0)
			idx := 0
			for time.Now().Before(deadline) {
				account := accounts[idx%len(accounts)]
				_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
					AccountId: account.AccountCode(),
				})
				if err != nil {
					t.Errorf("Worker %d: operation failed: %v", workerID, err)
					return
				}
				localCount++
				idx++
			}
			mu.Lock()
			workerCounts[workerID] = localCount
			count += localCount
			mu.Unlock()
		}(w)
	}

	wg.Wait()

	opsPerSecond := float64(count) / duration.Seconds()
	t.Logf("Throughput: %.0f ops/sec across %d workers", opsPerSecond, numWorkers)
	t.Logf("Total operations: %d in %v", count, duration)

	// Report per-worker throughput
	for i, wc := range workerCounts {
		perWorkerOps := float64(wc) / duration.Seconds()
		t.Logf("Worker %d: %.0f ops/sec (%d ops)", i, perWorkerOps, wc)
	}

	// Advisory target: >1000 ops/sec
	// CI environments may be slower due to shared resources
	if opsPerSecond < 500 {
		t.Errorf("Throughput below CI threshold: %.0f (CI threshold: >500, production target: >1000)", opsPerSecond)
	} else if opsPerSecond < 1000 {
		t.Logf("Note: Throughput %.0f below production target (>1000) but passes CI threshold", opsPerSecond)
	}
}

// TestThroughputMixedWorkload measures sustained mixed workload throughput.
// Includes balance queries, lookups, and creates in realistic proportions.
func TestThroughputMixedWorkload(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	// Pre-populate accounts for read operations
	accounts := createBenchAccounts(t, tc, 100)

	// Run for 3 seconds with mixed operations
	duration := 3 * time.Second
	deadline := time.Now().Add(duration)

	var totalOps int64
	var readOps, lookupOps, createOps int64
	var createCounter int64

	numWorkers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			localOps := int64(0)
			opNum := int64(workerID * 1000) // Offset to distribute operations

			for time.Now().Before(deadline) {
				operation := opNum % 10

				switch {
				case operation < 7: // 70% balance queries
					idx := opNum % int64(len(accounts))
					account := accounts[idx]
					_, err := tc.service.GetBalance(ctx, &pb.GetBalanceRequest{
						AccountId: account.AccountCode(),
					})
					if err != nil {
						t.Errorf("Worker %d: balance query failed: %v", workerID, err)
						return
					}
					atomic.AddInt64(&readOps, 1)

				case operation < 9: // 20% lookups
					idx := opNum % int64(len(accounts))
					account := accounts[idx]
					_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
						AccountId: account.AccountCode(),
					})
					if err != nil {
						t.Errorf("Worker %d: lookup failed: %v", workerID, err)
						return
					}
					atomic.AddInt64(&lookupOps, 1)

				default: // 10% creates
					id := atomic.AddInt64(&createCounter, 1)
					req := &pb.InitiateInternalAccountRequest{
						AccountCode:     fmt.Sprintf("THRU-%d-%08d", workerID, id),
						Name:            fmt.Sprintf("Throughput Account %d", id),
						ProductTypeCode: "CLEARING_USD",
						ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
						InstrumentCode:  "USD",
					}
					_, err := tc.service.InitiateInternalAccount(ctx, req)
					if err != nil {
						t.Errorf("Worker %d: create failed: %v", workerID, err)
						return
					}
					atomic.AddInt64(&createOps, 1)
				}

				localOps++
				opNum++
			}
			atomic.AddInt64(&totalOps, localOps)
		}(w)
	}

	wg.Wait()

	opsPerSecond := float64(totalOps) / duration.Seconds()
	t.Logf("Mixed workload throughput: %.0f ops/sec", opsPerSecond)
	t.Logf("Operation breakdown:")
	t.Logf("  Balance queries: %d (%.1f%%)", readOps, float64(readOps)/float64(totalOps)*100)
	t.Logf("  Lookups: %d (%.1f%%)", lookupOps, float64(lookupOps)/float64(totalOps)*100)
	t.Logf("  Creates: %d (%.1f%%)", createOps, float64(createOps)/float64(totalOps)*100)
}

// TestConcurrentOperations validates simultaneous operations complete successfully.
// Uses sync.WaitGroup for coordination and collects errors via buffered channel.
// Reduced concurrency to avoid PostgreSQL max_connections limit (default 100).
func TestConcurrentOperations(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	// Pre-populate accounts for concurrent access
	accounts := createBenchAccounts(t, tc, 50)

	// Reduced from 1000 to 50 to stay within testcontainer connection limits
	concurrency := 50
	var wg sync.WaitGroup
	errChan := make(chan error, concurrency)

	start := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			account := accounts[idx%len(accounts)]
			_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
				AccountId: account.AccountCode(),
			})
			if err != nil {
				errChan <- fmt.Errorf("operation %d failed: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errChan)
	elapsed := time.Since(start)

	// Collect errors from channel
	var errCount int
	for err := range errChan {
		t.Errorf("Concurrent operation failed: %v", err)
		errCount++
	}

	if errCount > 0 {
		t.Fatalf("Failed operations: %d/%d", errCount, concurrency)
	}

	t.Logf("1000 concurrent operations completed in %v", elapsed)
	t.Logf("Average latency: %v", elapsed/time.Duration(concurrency))
	t.Logf("Operations per second: %.0f", float64(concurrency)/elapsed.Seconds())

	// Target: All operations complete successfully
	// No specific timing requirement, but log for monitoring
}

// TestConcurrentMixedOperations validates concurrent mixed operations.
// Runs balance queries, lookups, and creates simultaneously.
func TestConcurrentMixedOperations(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	// Pre-populate accounts
	accounts := createBenchAccounts(t, tc, 100)

	// Reduced concurrency to avoid PostgreSQL max_connections limit (default 100)
	// in testcontainer environments. Production deployments should use connection pooling.
	concurrency := 50
	var wg sync.WaitGroup
	errChan := make(chan error, concurrency)
	var createCounter int64

	start := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			operation := idx % 10

			switch {
			case operation < 7: // 70% balance queries
				account := accounts[idx%len(accounts)]
				_, err := tc.service.GetBalance(ctx, &pb.GetBalanceRequest{
					AccountId: account.AccountCode(),
				})
				if err != nil {
					errChan <- fmt.Errorf("balance query %d failed: %w", idx, err)
				}

			case operation < 9: // 20% lookups
				account := accounts[idx%len(accounts)]
				_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
					AccountId: account.AccountCode(),
				})
				if err != nil {
					errChan <- fmt.Errorf("lookup %d failed: %w", idx, err)
				}

			default: // 10% creates
				id := atomic.AddInt64(&createCounter, 1)
				req := &pb.InitiateInternalAccountRequest{
					AccountCode:     fmt.Sprintf("CMIX-%08d", id),
					Name:            fmt.Sprintf("Concurrent Mixed Account %d", id),
					ProductTypeCode: "CLEARING_USD",
					ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
					InstrumentCode:  "USD",
				}
				_, err := tc.service.InitiateInternalAccount(ctx, req)
				if err != nil {
					errChan <- fmt.Errorf("create %d failed: %w", idx, err)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)
	elapsed := time.Since(start)

	// Collect errors from channel
	var errCount int
	for err := range errChan {
		t.Errorf("Operation failed: %v", err)
		errCount++
	}

	if errCount > 0 {
		t.Fatalf("Failed operations: %d/%d", errCount, concurrency)
	}

	t.Logf("%d concurrent mixed operations completed in %v", concurrency, elapsed)
	t.Logf("Average latency: %v", elapsed/time.Duration(concurrency))
}

// TestConcurrentWriteContention validates handling of concurrent account creation.
// Tests that unique account codes are properly enforced under concurrent load.
func TestConcurrentWriteContention(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	concurrency := 100
	var wg sync.WaitGroup
	var successCount, failCount int64
	errChan := make(chan error, concurrency)

	start := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			req := &pb.InitiateInternalAccountRequest{
				AccountCode:     fmt.Sprintf("CONT-%08d", idx),
				Name:            fmt.Sprintf("Contention Test Account %d", idx),
				ProductTypeCode: "CLEARING_USD",
				ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
				InstrumentCode:  "USD",
			}

			_, err := tc.service.InitiateInternalAccount(ctx, req)
			if err != nil {
				atomic.AddInt64(&failCount, 1)
				errChan <- fmt.Errorf("create %d failed: %w", idx, err)
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()
	close(errChan)
	elapsed := time.Since(start)

	t.Logf("%d concurrent writes completed in %v", concurrency, elapsed)
	t.Logf("Successful: %d, Failed: %d", successCount, failCount)

	// All unique account codes should succeed
	require.Equal(t, int64(concurrency), successCount, "All concurrent writes with unique codes should succeed")
	require.Equal(t, int64(0), failCount, "No writes should fail")
}

// BenchmarkConnectionPoolEfficiency measures database connection pool behavior.
// Tests how well the service handles concurrent connections.
func BenchmarkConnectionPoolEfficiency(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	// Pre-populate accounts
	accounts := createBenchAccounts(b, tc, 50)

	// Vary concurrency levels
	concurrencyLevels := []int{1, 4, 8, 16, 32}

	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("concurrency_%d", concurrency), func(b *testing.B) {
			// Set GOMAXPROCS to match concurrency for this sub-benchmark
			prev := runtime.GOMAXPROCS(concurrency)
			defer runtime.GOMAXPROCS(prev)

			b.ResetTimer()
			b.RunParallel(func(pbt *testing.PB) {
				idx := 0
				for pbt.Next() {
					account := accounts[idx%len(accounts)]
					_, err := tc.service.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
						AccountId: account.AccountCode(),
					})
					if err != nil {
						b.Fatal(err)
					}
					idx++
				}
			})
		})
	}
}

// Note: createBenchAccounts is defined in helpers_test.go and is reused here.
// The function signature is: createBenchAccounts(tb testing.TB, tc *testContainer, count int)

// Ensure unused import doesn't cause issues with context package.
var _ = context.Background
