// Package benchmarks_test provides performance benchmarks for the Market Information service.
//
// These benchmarks measure service-level performance including repository persistence,
// point-in-time queries, and batch ingestion.
//
// Target metrics from PRD requirements:
//   - Point-in-time query: P99 < 50ms
//   - Observation ingestion: P99 < 100ms
//   - Batch ingestion: 1000 obs/sec throughput
//   - Dataset activation: < 500ms
//   - Supersession overhead: < 5ms
//
// Run with: go test -bench=. -benchmem -benchtime=10s ./services/market-information/benchmarks/...
package benchmarks_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/require"
)

// testContainer holds all benchmark infrastructure components.
type testContainer struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	repos    *persistence.Repositories
	tenantID tenant.TenantID
	tc       *testhelpers.TestContainer
}

// setupTestContainer creates a fresh test container for validation tests.
// Uses master context (no tenant) to match repository test patterns and avoid
// multi-tenant complexity in performance benchmarks.
func setupTestContainer(t *testing.T) *testContainer {
	t.Helper()

	// Use the shared test container setup
	tc := testhelpers.SetupTestContainer(t)
	t.Cleanup(func() {
		tc.Cleanup(t)
	})

	// Use master context (no tenant) for benchmark operations
	// This matches how repository tests work and avoids multi-tenant lookup complexity
	ctx := context.Background()

	return &testContainer{
		ctx:      ctx,
		pool:     tc.Pool,
		repos:    tc.Repos,
		tenantID: tenant.TenantID(""),
		tc:       tc,
	}
}

// createTestDataSource creates a data source for benchmarks.
func createTestDataSource(t testing.TB, tc *testContainer, code string, trustLevel int) domain.DataSource {
	t.Helper()

	source, err := domain.NewDataSource(
		code,
		fmt.Sprintf("Benchmark Source %s", code),
		"", // description
		domain.SourceTypeAPI,
		trustLevel,
	)
	require.NoError(t, err, "Failed to create data source")

	err = tc.repos.Source.Save(tc.ctx, source)
	require.NoError(t, err, "Failed to save data source")

	return source
}

// createTestDataSet creates and activates a dataset for benchmarks.
func createTestDataSet(t testing.TB, tc *testContainer, code string) domain.DataSetDefinition {
	t.Helper()

	dataset, err := domain.NewDataSetDefinition(
		code,
		fmt.Sprintf("Benchmark Dataset %s", code),
		"",
		domain.DataCategoryPricing,
		"true",                    // simple validation expression (always pass)
		`observation_context.key`, // simple resolution key
		"",                        // no error message expression
	)
	require.NoError(t, err, "Failed to create dataset definition")

	err = tc.repos.DataSet.Save(tc.ctx, dataset)
	require.NoError(t, err, "Failed to save dataset definition")

	// Activate the dataset
	activatedDataset, err := dataset.ActivateDataSet()
	require.NoError(t, err, "Failed to activate dataset")

	err = tc.repos.DataSet.Save(tc.ctx, activatedDataset)
	require.NoError(t, err, "Failed to save activated dataset")

	return activatedDataset
}

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
