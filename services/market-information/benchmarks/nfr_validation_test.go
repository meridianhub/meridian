// Package benchmarks_test contains NFR (Non-Functional Requirements) validation tests
// for the Market Information service.
//
// These tests validate that the service meets its performance targets:
//   - NFR-1.1: Point-in-Time Queries P99 < 50ms (CI threshold: 200ms)
//   - NFR-1.2: Observation Ingestion P99 < 100ms (CI threshold: 400ms)
//   - NFR-1.3: Dataset Activation < 500ms (CI threshold: 2000ms)
//   - NFR-1.4: Concurrent Ingestion (50 writers)
//   - NFR-1.5: Supersession Performance < 5ms overhead (CI threshold: 50ms)
//
// Run with: go test -v ./services/market-information/benchmarks/
package benchmarks_test

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// TestNFR_PointInTimeQueryLatency validates P99 < 50ms target for point-in-time queries.
// This is the critical path for rate lookups (FX rates, commodity prices, etc.).
func TestNFR_PointInTimeQueryLatency(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	// Create test data source and dataset
	source := createTestDataSource(t, tc, "NFR-PIT-SOURCE", 80)
	dataset := createTestDataSet(t, tc, "NFR_PIT_DATASET")

	// Create observations with different timestamps for point-in-time queries
	numObservations := 100
	baseTime := time.Now().Add(-24 * time.Hour)
	resolutionKey := "PIT-KEY"

	for i := 0; i < numObservations; i++ {
		observedAt := baseTime.Add(time.Duration(i) * 10 * time.Minute)
		obs := domain.NewMarketPriceObservationBuilder().
			WithDataSetCode(dataset.Code()).
			WithSourceID(source.ID()).
			WithResolutionKey(resolutionKey).
			WithValue(decimal.NewFromFloat(1.0 + float64(i)*0.001)).
			WithObservedAt(observedAt).
			WithValidFrom(observedAt).
			WithValidTo(observedAt.Add(10 * time.Minute)).
			WithCreatedAt(time.Now()).
			WithQualityLevel(domain.QualityLevelActual).
			WithTrustLevel(80).
			Build()

		err := tc.repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	iterations := 1000
	latencies := make([]time.Duration, iterations)

	// Warm up
	for i := 0; i < 10; i++ {
		queryTime := baseTime.Add(time.Duration(i%numObservations) * 10 * time.Minute)
		_, _ = tc.repos.Observation.RetrieveObservation(ctx, dataset.Code(), resolutionKey, queryTime)
	}

	// Measure latencies
	for i := 0; i < iterations; i++ {
		// Query at different points in time
		queryTime := baseTime.Add(time.Duration(i%numObservations) * 10 * time.Minute)

		start := time.Now()
		_, err := tc.repos.Observation.RetrieveObservation(ctx, dataset.Code(), resolutionKey, queryTime)
		latencies[i] = time.Since(start)

		// Some queries will return not found, which is expected
		if err != nil && !errors.Is(err, domain.ErrObservationNotFound) {
			require.NoError(t, err, "Unexpected error on iteration %d", i)
		}
	}

	p50 := calculateP50(latencies)
	p99 := calculateP99(latencies)
	t.Logf("Point-in-time query P50 latency: %v", p50)
	t.Logf("Point-in-time query P99 latency: %v", p99)

	target := 50 * time.Millisecond
	ciThreshold := 200 * time.Millisecond // 4x headroom for CI

	if p99 > ciThreshold {
		t.Errorf("P99 latency too high: %v (CI threshold: <%v, production target: <%v)", p99, ciThreshold, target)
	} else if p99 > target {
		t.Logf("Note: P99 %v exceeds production target (%v) but passes CI threshold", p99, target)
	}
}

// TestNFR_ObservationIngestionLatency validates P99 < 100ms target for observation ingestion.
func TestNFR_ObservationIngestionLatency(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	// Create test data source and dataset
	source := createTestDataSource(t, tc, "NFR-INGEST-SOURCE", 80)
	dataset := createTestDataSet(t, tc, "NFR_INGEST_DATASET")

	iterations := 200 // Fewer iterations for write operations
	latencies := make([]time.Duration, iterations)

	// Warm up
	for i := 0; i < 10; i++ {
		obs := domain.NewMarketPriceObservationBuilder().
			WithDataSetCode(dataset.Code()).
			WithSourceID(source.ID()).
			WithResolutionKey(fmt.Sprintf("WARMUP-KEY-%d", i)).
			WithValue(decimal.NewFromFloat(1.234)).
			WithObservedAt(time.Now()).
			WithValidFrom(time.Now()).
			WithValidTo(time.Now().Add(24 * time.Hour)).
			WithCreatedAt(time.Now()).
			WithQualityLevel(domain.QualityLevelActual).
			WithTrustLevel(80).
			Build()

		err := tc.repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	// Measure latencies
	for i := 0; i < iterations; i++ {
		obs := domain.NewMarketPriceObservationBuilder().
			WithDataSetCode(dataset.Code()).
			WithSourceID(source.ID()).
			WithResolutionKey(fmt.Sprintf("INGEST-KEY-%08d", i)).
			WithValue(decimal.NewFromFloat(float64(i) + 1.0)).
			WithObservedAt(time.Now()).
			WithValidFrom(time.Now()).
			WithValidTo(time.Now().Add(24 * time.Hour)).
			WithCreatedAt(time.Now()).
			WithQualityLevel(domain.QualityLevelActual).
			WithTrustLevel(80).
			Build()

		start := time.Now()
		err := tc.repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
		latencies[i] = time.Since(start)
	}

	p50 := calculateP50(latencies)
	p99 := calculateP99(latencies)
	t.Logf("Observation ingestion P50 latency: %v", p50)
	t.Logf("Observation ingestion P99 latency: %v", p99)

	target := 100 * time.Millisecond
	ciThreshold := 400 * time.Millisecond // 4x headroom for CI

	if p99 > ciThreshold {
		t.Errorf("P99 latency too high: %v (CI threshold: <%v, production target: <%v)", p99, ciThreshold, target)
	} else if p99 > target {
		t.Logf("Note: P99 %v exceeds production target (%v) but passes CI threshold", p99, target)
	}
}

// TestNFR_DatasetActivationLatency validates dataset activation < 500ms.
func TestNFR_DatasetActivationLatency(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	iterations := 50
	latencies := make([]time.Duration, iterations)

	for i := 0; i < iterations; i++ {
		// Create a new dataset
		dataset, err := domain.NewDataSetDefinition(
			fmt.Sprintf("NFR_ACTIVATION_%03d", i),
			fmt.Sprintf("Activation Test Dataset %d", i),
			"",
			domain.DataCategoryPricing,
			"decimal(value) > 0",
			`observation_context.key`,
			"",
		)
		require.NoError(t, err)

		err = tc.repos.DataSet.Save(ctx, dataset)
		require.NoError(t, err)

		// Measure activation latency
		start := time.Now()
		activatedDataset, err := dataset.ActivateDataSet()
		require.NoError(t, err)

		err = tc.repos.DataSet.Save(ctx, activatedDataset)
		require.NoError(t, err)
		latencies[i] = time.Since(start)
	}

	p50 := calculateP50(latencies)
	p99 := calculateP99(latencies)
	t.Logf("Dataset activation P50 latency: %v", p50)
	t.Logf("Dataset activation P99 latency: %v", p99)

	target := 500 * time.Millisecond
	ciThreshold := 2000 * time.Millisecond // 4x headroom for CI

	if p99 > ciThreshold {
		t.Errorf("P99 latency too high: %v (CI threshold: <%v, production target: <%v)", p99, ciThreshold, target)
	} else if p99 > target {
		t.Logf("Note: P99 %v exceeds production target (%v) but passes CI threshold", p99, target)
	}
}

// TestNFR_ConcurrentIngestion validates handling concurrent write operations.
// Uses reduced concurrency (50) to stay within PostgreSQL max_connections (100)
// in testcontainer environments.
func TestNFR_ConcurrentIngestion(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	// Create shared data source and dataset
	source := createTestDataSource(t, tc, "NFR-CONC-SOURCE", 80)
	dataset := createTestDataSet(t, tc, "NFR_CONC_DATASET")

	concurrency := 50
	operationsPerWorker := 10
	var wg sync.WaitGroup
	var successCount int64
	var errorCount int64

	latencies := make([]time.Duration, concurrency*operationsPerWorker)
	var latencyMu sync.Mutex
	latencyIdx := int64(0)

	start := time.Now()
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for op := 0; op < operationsPerWorker; op++ {
				obs := domain.NewMarketPriceObservationBuilder().
					WithDataSetCode(dataset.Code()).
					WithSourceID(source.ID()).
					WithResolutionKey(fmt.Sprintf("CONC-%03d-%04d", workerID, op)).
					WithValue(decimal.NewFromFloat(float64(workerID*100+op) + 1.0)).
					WithObservedAt(time.Now()).
					WithValidFrom(time.Now()).
					WithValidTo(time.Now().Add(24 * time.Hour)).
					WithCreatedAt(time.Now()).
					WithQualityLevel(domain.QualityLevelActual).
					WithTrustLevel(80).
					Build()

				opStart := time.Now()
				err := tc.repos.Observation.Record(ctx, obs)
				opDuration := time.Since(opStart)

				idx := atomic.AddInt64(&latencyIdx, 1) - 1
				latencyMu.Lock()
				if int(idx) < len(latencies) {
					latencies[idx] = opDuration
				}
				latencyMu.Unlock()

				if err != nil {
					atomic.AddInt64(&errorCount, 1)
				} else {
					atomic.AddInt64(&successCount, 1)
				}
			}
		}(w)
	}

	wg.Wait()
	totalDuration := time.Since(start)
	totalOps := concurrency * operationsPerWorker

	// Trim latencies to actual count
	actualLatencies := latencies[:latencyIdx]
	p50 := calculateP50(actualLatencies)
	p99 := calculateP99(actualLatencies)
	opsPerSec := float64(totalOps) / totalDuration.Seconds()

	t.Logf("Concurrent ingestion: %d workers x %d ops = %d total", concurrency, operationsPerWorker, totalOps)
	t.Logf("Total duration: %v", totalDuration)
	t.Logf("Throughput: %.0f ops/sec", opsPerSec)
	t.Logf("Success count: %d", successCount)
	t.Logf("Error count: %d", errorCount)
	t.Logf("P50 latency: %v", p50)
	t.Logf("P99 latency: %v", p99)

	// Validate success rate
	successRate := float64(successCount) / float64(totalOps) * 100
	if successRate < 99.0 {
		t.Errorf("Success rate too low: %.2f%% (expected >= 99%%)", successRate)
	}

	// All concurrent ops should complete
	if errorCount > 0 {
		t.Errorf("Expected zero errors for concurrent operations, got %d", errorCount)
	}

	// Throughput should meet minimum threshold
	minThroughput := 100.0 // ops/sec CI threshold for testcontainer
	if opsPerSec < minThroughput {
		t.Errorf("Throughput below CI threshold: %.0f (CI threshold: >%.0f)", opsPerSec, minThroughput)
	}
}

// TestNFR_SupersessionPerformance validates that supersession adds < 5ms overhead.
func TestNFR_SupersessionPerformance(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	// Create test data source and dataset
	source := createTestDataSource(t, tc, "NFR-SUPER-SOURCE", 80)
	dataset := createTestDataSet(t, tc, "NFR_SUPER_DATASET")

	iterations := 100
	baselineLatencies := make([]time.Duration, iterations)
	supersessionLatencies := make([]time.Duration, iterations)

	// Measure baseline ingestion (no supersession)
	for i := 0; i < iterations; i++ {
		obs := domain.NewMarketPriceObservationBuilder().
			WithDataSetCode(dataset.Code()).
			WithSourceID(source.ID()).
			WithResolutionKey(fmt.Sprintf("BASELINE-KEY-%08d", i)).
			WithValue(decimal.NewFromFloat(1.0)).
			WithObservedAt(time.Now()).
			WithValidFrom(time.Now()).
			WithValidTo(time.Now().Add(24 * time.Hour)).
			WithCreatedAt(time.Now()).
			WithQualityLevel(domain.QualityLevelActual).
			WithTrustLevel(80).
			Build()

		start := time.Now()
		err := tc.repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
		baselineLatencies[i] = time.Since(start)
	}

	// Measure supersession ingestion (each insert supersedes previous)
	for i := 0; i < iterations; i++ {
		// First, insert an ESTIMATE
		estimate := domain.NewMarketPriceObservationBuilder().
			WithDataSetCode(dataset.Code()).
			WithSourceID(source.ID()).
			WithResolutionKey(fmt.Sprintf("SUPER-KEY-%08d", i)).
			WithValue(decimal.NewFromFloat(1.0)).
			WithObservedAt(time.Now()).
			WithValidFrom(time.Now()).
			WithValidTo(time.Now().Add(24 * time.Hour)).
			WithCreatedAt(time.Now()).
			WithQualityLevel(domain.QualityLevelEstimate).
			WithTrustLevel(80).
			Build()

		err := tc.repos.Observation.Record(ctx, estimate)
		require.NoError(t, err)

		// Then insert an ACTUAL (which should supersede the ESTIMATE)
		actual := domain.NewMarketPriceObservationBuilder().
			WithDataSetCode(dataset.Code()).
			WithSourceID(source.ID()).
			WithResolutionKey(fmt.Sprintf("SUPER-KEY-%08d", i)).
			WithValue(decimal.NewFromFloat(1.1)).
			WithObservedAt(time.Now()).
			WithValidFrom(time.Now()).
			WithValidTo(time.Now().Add(24 * time.Hour)).
			WithCreatedAt(time.Now()).
			WithQualityLevel(domain.QualityLevelActual).
			WithTrustLevel(80).
			Build()

		start := time.Now()
		err = tc.repos.Observation.Record(ctx, actual)
		require.NoError(t, err)
		supersessionLatencies[i] = time.Since(start)
	}

	baselineP50 := calculateP50(baselineLatencies)
	baselineP99 := calculateP99(baselineLatencies)
	supersessionP50 := calculateP50(supersessionLatencies)
	supersessionP99 := calculateP99(supersessionLatencies)

	t.Logf("Baseline (no supersession) P50: %v, P99: %v", baselineP50, baselineP99)
	t.Logf("With supersession P50: %v, P99: %v", supersessionP50, supersessionP99)

	// Calculate overhead
	overheadP99 := supersessionP99 - baselineP99
	t.Logf("Supersession overhead P99: %v", overheadP99)

	target := 5 * time.Millisecond
	ciThreshold := 50 * time.Millisecond // 10x headroom for CI

	if overheadP99 > ciThreshold {
		t.Errorf("Supersession overhead too high: %v (CI threshold: <%v, production target: <%v)", overheadP99, ciThreshold, target)
	} else if overheadP99 > target {
		t.Logf("Note: Overhead %v exceeds production target (%v) but passes CI threshold", overheadP99, target)
	}
}

// TestNFR_BiTemporalQueryLatency validates bi-temporal queries with knowledge_base_time.
func TestNFR_BiTemporalQueryLatency(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	// Create test data source and dataset
	source := createTestDataSource(t, tc, "NFR-BITEMP-SOURCE", 80)
	dataset := createTestDataSet(t, tc, "NFR_BITEMP_DATASET")

	// Create observations at different knowledge times
	resolutionKey := "BITEMP-KEY"
	baseKnowledgeTime := time.Now().Add(-24 * time.Hour)

	for i := 0; i < 50; i++ {
		// Simulate observations recorded at different times
		knowledgeTime := baseKnowledgeTime.Add(time.Duration(i) * 30 * time.Minute)

		obs := domain.NewMarketPriceObservationBuilder().
			WithID(uuid.New()).
			WithDataSetCode(dataset.Code()).
			WithSourceID(source.ID()).
			WithResolutionKey(resolutionKey).
			WithValue(decimal.NewFromFloat(1.0 + float64(i)*0.01)).
			WithObservedAt(time.Now().Add(-48 * time.Hour)).
			WithValidFrom(time.Now().Add(-48 * time.Hour)).
			WithValidTo(time.Now().Add(-24 * time.Hour)).
			WithCreatedAt(knowledgeTime). // Different knowledge times
			WithQualityLevel(domain.QualityLevelActual).
			WithTrustLevel(80).
			Build()

		err := tc.repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	iterations := 500
	latencies := make([]time.Duration, iterations)

	// Warm up
	for i := 0; i < 10; i++ {
		queryKnowledgeTime := baseKnowledgeTime.Add(time.Duration(i%50) * 30 * time.Minute)
		_, _ = tc.repos.Observation.RetrieveObservation(ctx, dataset.Code(), resolutionKey, queryKnowledgeTime)
	}

	// Measure latencies for bi-temporal queries
	for i := 0; i < iterations; i++ {
		queryKnowledgeTime := baseKnowledgeTime.Add(time.Duration(i%50) * 30 * time.Minute)

		start := time.Now()
		_, err := tc.repos.Observation.RetrieveObservation(ctx, dataset.Code(), resolutionKey, queryKnowledgeTime)
		latencies[i] = time.Since(start)

		if err != nil && !errors.Is(err, domain.ErrObservationNotFound) {
			require.NoError(t, err, "Unexpected error on iteration %d", i)
		}
	}

	p50 := calculateP50(latencies)
	p99 := calculateP99(latencies)
	t.Logf("Bi-temporal query P50 latency: %v", p50)
	t.Logf("Bi-temporal query P99 latency: %v", p99)

	target := 50 * time.Millisecond
	ciThreshold := 200 * time.Millisecond

	if p99 > ciThreshold {
		t.Errorf("P99 latency too high: %v (CI threshold: <%v, production target: <%v)", p99, ciThreshold, target)
	} else if p99 > target {
		t.Logf("Note: P99 %v exceeds production target (%v) but passes CI threshold", p99, target)
	}
}

// TestNFR_SustainedThroughput measures throughput over a sustained period.
func TestNFR_SustainedThroughput(t *testing.T) {
	tc := setupTestContainer(t)
	ctx := tc.ctx

	// Create test data source and dataset
	source := createTestDataSource(t, tc, "NFR-SUSTAINED-SOURCE", 80)
	dataset := createTestDataSet(t, tc, "NFR_SUSTAINED_DATASET")

	// Pre-create some observations for queries
	numObservations := 100
	for i := 0; i < numObservations; i++ {
		obs := domain.NewMarketPriceObservationBuilder().
			WithDataSetCode(dataset.Code()).
			WithSourceID(source.ID()).
			WithResolutionKey(fmt.Sprintf("SUSTAINED-KEY-%04d", i)).
			WithValue(decimal.NewFromFloat(float64(i) + 1.0)).
			WithObservedAt(time.Now()).
			WithValidFrom(time.Now()).
			WithValidTo(time.Now().Add(24 * time.Hour)).
			WithCreatedAt(time.Now()).
			WithQualityLevel(domain.QualityLevelActual).
			WithTrustLevel(80).
			Build()

		err := tc.repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	// Run for 1 second
	duration := 1 * time.Second
	deadline := time.Now().Add(duration)

	var readCount, writeCount int64
	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers > 8 {
		numWorkers = 8
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allLatencies []time.Duration

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			localCount := int64(0)
			var localLatencies []time.Duration

			for time.Now().Before(deadline) {
				if localCount%5 == 0 {
					// 20% writes
					obs := domain.NewMarketPriceObservationBuilder().
						WithDataSetCode(dataset.Code()).
						WithSourceID(source.ID()).
						WithResolutionKey(fmt.Sprintf("SUSTAINED-WRITE-%d-%d", workerID, localCount)).
						WithValue(decimal.NewFromFloat(float64(localCount) + 1.0)).
						WithObservedAt(time.Now()).
						WithValidFrom(time.Now()).
						WithValidTo(time.Now().Add(24 * time.Hour)).
						WithCreatedAt(time.Now()).
						WithQualityLevel(domain.QualityLevelActual).
						WithTrustLevel(80).
						Build()

					start := time.Now()
					err := tc.repos.Observation.Record(ctx, obs)
					if err == nil {
						localLatencies = append(localLatencies, time.Since(start))
						atomic.AddInt64(&writeCount, 1)
					}
				} else {
					// 80% reads
					key := fmt.Sprintf("SUSTAINED-KEY-%04d", int(localCount)%numObservations)
					start := time.Now()
					_, err := tc.repos.Observation.GetLatest(ctx, dataset.Code(), key)
					if err == nil || errors.Is(err, domain.ErrObservationNotFound) {
						localLatencies = append(localLatencies, time.Since(start))
						atomic.AddInt64(&readCount, 1)
					}
				}
				localCount++
			}

			mu.Lock()
			allLatencies = append(allLatencies, localLatencies...)
			mu.Unlock()
		}(w)
	}

	wg.Wait()

	totalOps := readCount + writeCount
	opsPerSecond := float64(totalOps) / duration.Seconds()
	p50 := calculateP50(allLatencies)
	p99 := calculateP99(allLatencies)

	t.Logf("=== Sustained Throughput Results ===")
	t.Logf("Workers: %d", numWorkers)
	t.Logf("Total operations: %d (reads: %d, writes: %d)", totalOps, readCount, writeCount)
	t.Logf("Throughput: %.0f ops/sec", opsPerSecond)
	t.Logf("P50 latency: %v", p50)
	t.Logf("P99 latency: %v", p99)

	// Throughput targets
	targetThroughput := 1000.0
	ciThreshold := 100.0 // Relaxed for CI testcontainer overhead

	if opsPerSecond < ciThreshold {
		t.Errorf("Throughput below CI threshold: %.0f (CI threshold: >%.0f, production target: >%.0f)", opsPerSecond, ciThreshold, targetThroughput)
	} else if opsPerSecond < targetThroughput {
		t.Logf("Note: Throughput %.0f below production target (>%.0f) but passes CI threshold", opsPerSecond, targetThroughput)
	}
}

// TestNFR_VersionInfo logs test environment info for audit trail.
func TestNFR_VersionInfo(t *testing.T) {
	t.Logf("=== Market Information NFR Validation ===")
	t.Logf("GOMAXPROCS: %d", runtime.GOMAXPROCS(0))
	t.Logf("NumCPU: %d", runtime.NumCPU())
	t.Logf("")
	t.Logf("Performance targets (production):")
	t.Logf("  - NFR-1.1: Point-in-time queries P99 < 50ms")
	t.Logf("  - NFR-1.2: Observation ingestion P99 < 100ms")
	t.Logf("  - NFR-1.3: Dataset activation < 500ms")
	t.Logf("  - NFR-1.4: Concurrent ingestion (50 writers)")
	t.Logf("  - NFR-1.5: Supersession overhead < 5ms")
	t.Logf("")
	t.Logf("CI thresholds (with headroom for testcontainers):")
	t.Logf("  - Point-in-time queries: P99 < 200ms (4x)")
	t.Logf("  - Observation ingestion: P99 < 400ms (4x)")
	t.Logf("  - Dataset activation: P99 < 2000ms (4x)")
	t.Logf("  - Supersession overhead: P99 < 50ms (10x)")
	t.Logf("")
	t.Logf("Run full benchmarks with: go test -v ./services/market-information/benchmarks/")
}
