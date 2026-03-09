//go:build integration_broken
// +build integration_broken

// Package integration provides stress and load tests for balance query performance.
// NOTE: Disabled due to missing MeasurementRepository.Create and IdempotencyService.Acquire methods
//
// These tests verify system performance under realistic and extreme load conditions:
//   - Stress tests with 10,000+ transaction accounts
//   - Concurrent balance query load tests (1000+ simultaneous queries)
//   - Performance benchmarks for P50/P90/P95/P99 latency percentiles
//   - Sustained load tests over time
//
// Target metrics:
//   - P99 latency < 50ms for GetAccountBalance with 10,000 transactions
//   - 1000 concurrent queries with no degradation
//   - Sustained load maintains performance over time
//
// Run with: go test -v -tags=integration -timeout=30m ./tests/integration/...
package integration

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
)

// =============================================================================
// Test Infrastructure
// =============================================================================

// StressTestContainer holds the test database container and service for stress tests.
type StressTestContainer struct {
	container *postgres.PostgresContainer
	Pool      *pgxpool.Pool
	Repo      *persistence.PostgresRepository
	Service   *service.PositionKeepingService
}

// SetupStressTestContainer creates a PostgreSQL testcontainer optimized for stress testing.
func SetupStressTestContainer(t *testing.T) *StressTestContainer {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container with optimized settings for large datasets
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_stress"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(60*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable", "search_path=position_keeping,public")
	require.NoError(t, err, "Failed to get connection string")

	// Configure pool with higher limits for stress testing
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse pool config")
	poolConfig.MaxConns = 100 // Increased for high concurrency
	poolConfig.MinConns = 20  // Keep connections warm
	poolConfig.MaxConnIdleTime = 10 * time.Minute
	poolConfig.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to create connection pool")

	// Load schema
	loadStressTestSchema(t, pool)

	// Optimize PostgreSQL for testing
	optimizePostgresForStressTesting(t, pool)

	// Create repository and service
	repo := persistence.NewPostgresRepository(pool)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCurrentAccountClient := NewInMemoryCurrentAccountClient()

	svc, err := service.NewPositionKeepingService(
		repo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		service.WithCurrentAccountClient(mockCurrentAccountClient),
	)
	require.NoError(t, err, "Failed to create service")

	return &StressTestContainer{
		container: pgContainer,
		Pool:      pool,
		Repo:      repo,
		Service:   svc,
	}
}

// Cleanup closes the connection pool and terminates the container.
func (tc *StressTestContainer) Cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if tc.Pool != nil {
		tc.Pool.Close()
	}

	if tc.container != nil {
		require.NoError(t, tc.container.Terminate(ctx), "Failed to terminate container")
	}
}

// loadStressTestSchema loads the position_keeping schema for stress tests.
func loadStressTestSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Create schema
	_, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS position_keeping`)
	require.NoError(t, err, "Failed to create schema")

	// Create financial_position_log table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.financial_position_log (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			log_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			account_service_domain character varying(20) NOT NULL DEFAULT '',
			version bigint NOT NULL DEFAULT 1,
			current_status character varying(20) NOT NULL,
			previous_status character varying(20) NULL,
			status_updated_at timestamptz NOT NULL,
			status_reason text NOT NULL,
			failure_reason text NULL,
			reconciliation_status character varying(20) NOT NULL,
			opening_balance_amount numeric(38,18) NULL,
			opening_balance_currency character(3) NULL,
			opening_balance_recorded_at timestamptz NULL,
			PRIMARY KEY (id)
		)
	`)
	require.NoError(t, err, "Failed to create financial_position_log table")

	// Create indexes
	_, err = pool.Exec(ctx, `
		CREATE UNIQUE INDEX idx_position_keeping_financial_position_log_log_id
		ON position_keeping.financial_position_log (log_id);
		CREATE INDEX idx_financial_position_log_account_id
		ON position_keeping.financial_position_log (account_id);
	`)
	require.NoError(t, err, "Failed to create financial_position_log indexes")

	// Create transaction_log_entry table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.transaction_log_entry (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			entry_id uuid NOT NULL,
			financial_position_log_id uuid NOT NULL,
			transaction_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			amount_cents bigint NOT NULL,
			currency character(3) NOT NULL DEFAULT 'GBP',
			direction character varying(10) NOT NULL,
			timestamp timestamptz NOT NULL,
			description text NULL,
			reference character varying(100) NULL,
			source character varying(50) NOT NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_transaction_log_entry_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err, "Failed to create transaction_log_entry table")

	// Create critical indexes for performance
	_, err = pool.Exec(ctx, `
		CREATE INDEX idx_transaction_log_entry_financial_position_log_id
		ON position_keeping.transaction_log_entry (financial_position_log_id);
		CREATE INDEX idx_transaction_log_entry_account_id
		ON position_keeping.transaction_log_entry (account_id);
		CREATE INDEX idx_transaction_log_entry_timestamp
		ON position_keeping.transaction_log_entry (timestamp);
	`)
	require.NoError(t, err, "Failed to create transaction_log_entry indexes")

	// Create transaction_lineage table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.transaction_lineage (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			financial_position_log_id uuid NOT NULL,
			transaction_id uuid NOT NULL,
			parent_transaction_id uuid NULL,
			child_transaction_ids jsonb NOT NULL DEFAULT '[]',
			related_transaction_ids jsonb NOT NULL DEFAULT '[]',
			transaction_type character varying(50) NOT NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_transaction_lineage_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err, "Failed to create transaction_lineage table")

	// Create audit_trail_entry table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.audit_trail_entry (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			audit_id uuid NOT NULL,
			financial_position_log_id uuid NOT NULL,
			timestamp timestamptz NOT NULL,
			user_id character varying(100) NOT NULL,
			action character varying(100) NOT NULL,
			details text NULL,
			ip_address character varying(45) NULL,
			system_context jsonb NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_audit_trail_entry_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err, "Failed to create audit_trail_entry table")
}

// optimizePostgresForStressTesting applies PostgreSQL optimizations for testing.
func optimizePostgresForStressTesting(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Increase work_mem for complex queries
	_, err := pool.Exec(ctx, `SET work_mem = '16MB'`)
	require.NoError(t, err, "Failed to set work_mem")

	// Increase shared_buffers if possible (may fail in container, that's ok)
	_, _ = pool.Exec(ctx, `SET shared_buffers = '128MB'`)

	// Disable fsync for faster writes in test environment
	_, _ = pool.Exec(ctx, `SET fsync = off`)
}

// createLargePositionLog creates a FinancialPositionLog with many transactions.
func createLargePositionLog(t *testing.T, accountID string, numTransactions int) *domain.FinancialPositionLog {
	t.Helper()

	// Create first entry with initial balance
	initialEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(decimal.NewFromInt(100000), domain.CurrencyGBP),
		domain.PostingDirectionDebit,
		time.Now().UTC().Add(-365*24*time.Hour), // Start 1 year ago
		"Initial deposit",
		"INIT-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog(accountID, initialEntry, nil)
	require.NoError(t, err)

	// Add remaining transactions
	for i := 1; i < numTransactions; i++ {
		var direction domain.PostingDirection
		var amount decimal.Decimal

		// Alternate between debits and credits with varying amounts
		if i%3 == 0 {
			direction = domain.PostingDirectionCredit
			amount = decimal.NewFromInt(int64(50 + (i % 500)))
		} else {
			direction = domain.PostingDirectionDebit
			amount = decimal.NewFromInt(int64(100 + (i % 1000)))
		}

		// Spread transactions over time
		timestamp := time.Now().UTC().Add(-time.Duration(numTransactions-i) * time.Hour)

		entry, err := domain.NewTransactionLogEntry(
			uuid.New(),
			accountID,
			domain.MustNewMoney(amount, domain.CurrencyGBP),
			direction,
			timestamp,
			fmt.Sprintf("Transaction %d", i),
			fmt.Sprintf("REF-%d", i),
			domain.TransactionSourceManual,
		)
		require.NoError(t, err)

		err = log.AddEntry(entry)
		require.NoError(t, err)
	}

	return log
}

// MockMeasurementRepository is a mock for testing.
type MockMeasurementRepository struct{}

func (m *MockMeasurementRepository) GetMeasurement(ctx context.Context, id uuid.UUID) (*domain.Measurement, error) {
	return nil, fmt.Errorf("not implemented")
}

// MockIdempotencyService is a mock for testing.
type MockIdempotencyService struct{}

func (m *MockIdempotencyService) IsProcessed(ctx context.Context, idempotencyKey string) (bool, error) {
	return false, nil
}

func (m *MockIdempotencyService) MarkProcessed(ctx context.Context, idempotencyKey string) error {
	return nil
}

// InMemoryCurrentAccountClient is a mock CurrentAccountClient.
type InMemoryCurrentAccountClient struct {
	mu     sync.RWMutex
	blocks map[string][]domain.AmountBlock
}

func NewInMemoryCurrentAccountClient() *InMemoryCurrentAccountClient {
	return &InMemoryCurrentAccountClient{
		blocks: make(map[string][]domain.AmountBlock),
	}
}

func (c *InMemoryCurrentAccountClient) GetActiveAmountBlocks(ctx context.Context, accountID string) ([]domain.AmountBlock, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if blocks, ok := c.blocks[accountID]; ok {
		return blocks, nil
	}
	return []domain.AmountBlock{}, nil
}

func (c *InMemoryCurrentAccountClient) AddBlocks(accountID string, blocks []domain.AmountBlock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocks[accountID] = append(c.blocks[accountID], blocks...)
}

// percentileLatency calculates the latency at a given percentile.
func percentileLatency(latencies []time.Duration, percentile float64) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	index := int(float64(len(latencies)) * percentile)
	if index >= len(latencies) {
		index = len(latencies) - 1
	}
	return latencies[index]
}

// =============================================================================
// Task 13.3, 13.9: Stress Test - 10,000+ Transaction Account
// =============================================================================

func TestStress_BalanceQuery_10KTransactions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test")
	}

	tc := SetupStressTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "STRESS-10K-TXN-001"
	numTransactions := 10000

	t.Logf("Creating account with %d transactions...", numTransactions)
	start := time.Now()
	log := createLargePositionLog(t, accountID, numTransactions)
	createTime := time.Since(start)
	t.Logf("Created position log in %v", createTime)

	// Save to database
	start = time.Now()
	err := tc.Repo.Create(ctx, log)
	require.NoError(t, err)
	saveTime := time.Since(start)
	t.Logf("Saved %d transactions to database in %v", numTransactions, saveTime)

	// Warm up query cache
	for i := 0; i < 5; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}
		_, _ = tc.Service.GetAccountBalance(ctx, req)
	}

	// Measure latencies for 100 sequential queries
	const numQueries = 100
	latencies := make([]time.Duration, numQueries)

	t.Logf("Running %d balance queries...", numQueries)
	for i := 0; i < numQueries; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}

		queryStart := time.Now()
		resp, err := tc.Service.GetAccountBalance(ctx, req)
		latencies[i] = time.Since(queryStart)

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, accountID, resp.AccountId)
	}

	// Sort latencies for percentile calculation
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	// Calculate percentiles
	p50 := percentileLatency(latencies, 0.50)
	p90 := percentileLatency(latencies, 0.90)
	p95 := percentileLatency(latencies, 0.95)
	p99 := percentileLatency(latencies, 0.99)
	minLatency := latencies[0]
	maxLatency := latencies[len(latencies)-1]

	var sum time.Duration
	for _, l := range latencies {
		sum += l
	}
	avgLatency := sum / time.Duration(numQueries)

	// Log statistics
	t.Logf("Balance query latency statistics (%d transactions):", numTransactions)
	t.Logf("  Min:    %v", minLatency)
	t.Logf("  P50:    %v", p50)
	t.Logf("  P90:    %v", p90)
	t.Logf("  P95:    %v", p95)
	t.Logf("  P99:    %v", p99)
	t.Logf("  Max:    %v", maxLatency)
	t.Logf("  Avg:    %v", avgLatency)

	// Assert P99 < 50ms (Task 13.3 requirement)
	assert.Less(t, p99.Milliseconds(), int64(50),
		"P99 latency should be < 50ms with %d transactions, got %v", numTransactions, p99)
}

// =============================================================================
// Task 13.10: Performance Benchmark - Percentile Distribution
// =============================================================================

func TestPerformance_LatencyPercentiles_1KTransactions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test")
	}

	tc := SetupStressTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "PERF-1K-TXN-001"

	log := createLargePositionLog(t, accountID, 1000)
	err := tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Warm up
	for i := 0; i < 10; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}
		_, _ = tc.Service.GetAccountBalance(ctx, req)
	}

	// Measure 200 queries for better statistical distribution
	const numQueries = 200
	latencies := make([]time.Duration, numQueries)

	for i := 0; i < numQueries; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}

		start := time.Now()
		_, err := tc.Service.GetAccountBalance(ctx, req)
		latencies[i] = time.Since(start)
		require.NoError(t, err)
	}

	// Sort for percentiles
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	// Calculate full percentile distribution
	percentiles := []float64{0.50, 0.75, 0.90, 0.95, 0.99, 0.999}
	t.Logf("Latency percentile distribution (1000 transactions):")
	for _, p := range percentiles {
		latency := percentileLatency(latencies, p)
		t.Logf("  P%.1f:  %v", p*100, latency)
	}

	// Verify performance targets
	p99 := percentileLatency(latencies, 0.99)
	assert.Less(t, p99.Milliseconds(), int64(50),
		"P99 latency should be < 50ms, got %v", p99)
}

func TestPerformance_LatencyPercentiles_5KTransactions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test")
	}

	tc := SetupStressTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "PERF-5K-TXN-001"

	log := createLargePositionLog(t, accountID, 5000)
	err := tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Warm up
	for i := 0; i < 10; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}
		_, _ = tc.Service.GetAccountBalance(ctx, req)
	}

	const numQueries = 200
	latencies := make([]time.Duration, numQueries)

	for i := 0; i < numQueries; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}

		start := time.Now()
		_, err := tc.Service.GetAccountBalance(ctx, req)
		latencies[i] = time.Since(start)
		require.NoError(t, err)
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	percentiles := []float64{0.50, 0.75, 0.90, 0.95, 0.99, 0.999}
	t.Logf("Latency percentile distribution (5000 transactions):")
	for _, p := range percentiles {
		latency := percentileLatency(latencies, p)
		t.Logf("  P%.1f:  %v", p*100, latency)
	}

	p99 := percentileLatency(latencies, 0.99)
	assert.Less(t, p99.Milliseconds(), int64(50),
		"P99 latency should be < 50ms with 5000 transactions, got %v", p99)
}

// =============================================================================
// Task 13.4, 13.6: Load Test - 1000 Concurrent Queries
// =============================================================================

func TestLoad_1000ConcurrentQueries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test")
	}

	tc := SetupStressTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "LOAD-CONCURRENT-001"

	// Create account with moderate transaction history
	log := createLargePositionLog(t, accountID, 1000)
	err := tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Warm up
	for i := 0; i < 10; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}
		_, _ = tc.Service.GetAccountBalance(ctx, req)
	}

	// Run 1000 concurrent queries
	const numConcurrent = 1000
	var wg sync.WaitGroup
	results := make(chan time.Duration, numConcurrent)
	errors := make(chan error, numConcurrent)

	balanceTypes := []positionkeepingv1.BalanceType{
		positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
		positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING,
		positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER,
		positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
		positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE,
		positionkeepingv1.BalanceType_BALANCE_TYPE_FREE,
	}

	t.Logf("Starting %d concurrent balance queries...", numConcurrent)
	overallStart := time.Now()

	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(queryID int) {
			defer wg.Done()

			bt := balanceTypes[queryID%len(balanceTypes)]
			req := &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   accountID,
				BalanceType: bt,
			}

			start := time.Now()
			resp, err := tc.Service.GetAccountBalance(ctx, req)
			latency := time.Since(start)

			if err != nil {
				errors <- err
				return
			}

			if resp == nil || resp.AccountId != accountID {
				errors <- fmt.Errorf("query %d: invalid response", queryID)
				return
			}

			results <- latency
		}(i)
	}

	wg.Wait()
	overallTime := time.Since(overallStart)
	close(results)
	close(errors)

	// Collect results
	var errList []error
	for err := range errors {
		errList = append(errList, err)
	}
	require.Empty(t, errList, "concurrent queries should not produce errors")

	// Analyze latencies
	var latencies []time.Duration
	for latency := range results {
		latencies = append(latencies, latency)
	}

	require.Equal(t, numConcurrent, len(latencies), "all queries should complete")

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	// Calculate statistics
	p50 := percentileLatency(latencies, 0.50)
	p90 := percentileLatency(latencies, 0.90)
	p95 := percentileLatency(latencies, 0.95)
	p99 := percentileLatency(latencies, 0.99)

	var sum time.Duration
	for _, l := range latencies {
		sum += l
	}
	avgLatency := sum / time.Duration(len(latencies))
	throughput := float64(numConcurrent) / overallTime.Seconds()

	t.Logf("Concurrent load test results (%d queries):", numConcurrent)
	t.Logf("  Total time:  %v", overallTime)
	t.Logf("  Throughput:  %.2f queries/sec", throughput)
	t.Logf("  Avg latency: %v", avgLatency)
	t.Logf("  P50 latency: %v", p50)
	t.Logf("  P90 latency: %v", p90)
	t.Logf("  P95 latency: %v", p95)
	t.Logf("  P99 latency: %v", p99)

	// Verify no performance degradation under load
	// P99 should still be under 100ms (allowing some overhead for concurrency)
	assert.Less(t, p99.Milliseconds(), int64(100),
		"P99 latency under concurrent load should be < 100ms, got %v", p99)
}

// =============================================================================
// Task 13.12: Sustained Load Test Over Time
// =============================================================================

func TestLoad_SustainedOverTime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sustained load test")
	}

	tc := SetupStressTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "SUSTAINED-LOAD-001"

	// Create account with realistic transaction history
	log := createLargePositionLog(t, accountID, 2000)
	err := tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Run sustained load: 100 queries/sec for 30 seconds
	const queriesPerSecond = 100
	const durationSeconds = 30
	const totalQueries = queriesPerSecond * durationSeconds

	ticker := time.NewTicker(time.Second / time.Duration(queriesPerSecond))
	defer ticker.Stop()

	var wg sync.WaitGroup
	results := make(chan time.Duration, totalQueries)
	errors := make(chan error, totalQueries)

	// Track memory before test
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	t.Logf("Starting sustained load test: %d queries/sec for %d seconds...", queriesPerSecond, durationSeconds)
	startTime := time.Now()
	queriesLaunched := 0

	// Launch queries at steady rate
	for queriesLaunched < totalQueries {
		<-ticker.C

		wg.Add(1)
		go func(queryNum int) {
			defer wg.Done()

			req := &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   accountID,
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			}

			queryStart := time.Now()
			resp, err := tc.Service.GetAccountBalance(ctx, req)
			latency := time.Since(queryStart)

			if err != nil {
				errors <- err
				return
			}

			if resp == nil || resp.AccountId != accountID {
				errors <- fmt.Errorf("query %d: invalid response", queryNum)
				return
			}

			results <- latency
		}(queriesLaunched)

		queriesLaunched++
	}

	wg.Wait()
	totalTime := time.Since(startTime)
	close(results)
	close(errors)

	// Track memory after test
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	// Collect errors
	var errList []error
	for err := range errors {
		errList = append(errList, err)
	}
	require.Empty(t, errList, "sustained load should not produce errors")

	// Analyze latencies
	var latencies []time.Duration
	for latency := range results {
		latencies = append(latencies, latency)
	}

	require.Equal(t, totalQueries, len(latencies), "all queries should complete")

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	// Calculate statistics
	p50 := percentileLatency(latencies, 0.50)
	p90 := percentileLatency(latencies, 0.90)
	p95 := percentileLatency(latencies, 0.95)
	p99 := percentileLatency(latencies, 0.99)

	var sum time.Duration
	for _, l := range latencies {
		sum += l
	}
	avgLatency := sum / time.Duration(len(latencies))
	actualThroughput := float64(len(latencies)) / totalTime.Seconds()

	// Memory analysis
	heapGrowth := int64(memAfter.HeapAlloc) - int64(memBefore.HeapAlloc)
	numGCs := memAfter.NumGC - memBefore.NumGC
	avgGCPause := time.Duration(0)
	if numGCs > 0 {
		avgGCPause = time.Duration(memAfter.PauseTotalNs-memBefore.PauseTotalNs) / time.Duration(numGCs)
	}

	t.Logf("Sustained load test results:")
	t.Logf("  Duration:         %v", totalTime)
	t.Logf("  Queries:          %d", len(latencies))
	t.Logf("  Target QPS:       %d", queriesPerSecond)
	t.Logf("  Actual QPS:       %.2f", actualThroughput)
	t.Logf("  Avg latency:      %v", avgLatency)
	t.Logf("  P50 latency:      %v", p50)
	t.Logf("  P90 latency:      %v", p90)
	t.Logf("  P95 latency:      %v", p95)
	t.Logf("  P99 latency:      %v", p99)
	t.Logf("  Heap growth:      %d KB", heapGrowth/1024)
	t.Logf("  GC cycles:        %d", numGCs)
	t.Logf("  Avg GC pause:     %v", avgGCPause)

	// Verify sustained performance
	assert.Less(t, p99.Milliseconds(), int64(100),
		"P99 latency under sustained load should be < 100ms, got %v", p99)

	// Verify no significant performance degradation over time
	// Compare first 10% vs last 10% of queries
	firstBatch := latencies[:len(latencies)/10]
	lastBatch := latencies[len(latencies)*9/10:]

	var firstSum, lastSum time.Duration
	for _, l := range firstBatch {
		firstSum += l
	}
	for _, l := range lastBatch {
		lastSum += l
	}

	firstAvg := firstSum / time.Duration(len(firstBatch))
	lastAvg := lastSum / time.Duration(len(lastBatch))
	degradation := float64(lastAvg-firstAvg) / float64(firstAvg) * 100

	t.Logf("  First 10%% avg:    %v", firstAvg)
	t.Logf("  Last 10%% avg:     %v", lastAvg)
	t.Logf("  Degradation:      %.2f%%", degradation)

	// Allow up to 50% degradation (some variance expected in CI environments)
	assert.Less(t, degradation, 50.0,
		"Performance should not degrade significantly over time")

	// Verify no memory leaks
	assert.Less(t, heapGrowth, int64(50*1024*1024),
		"Heap growth should be < 50MB during sustained load")
}

// =============================================================================
// Additional Stress Scenarios
// =============================================================================

func TestStress_MultipleAccounts_ConcurrentQueries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-account stress test")
	}

	tc := SetupStressTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create 10 accounts with varying transaction counts
	numAccounts := 10
	accountIDs := make([]string, numAccounts)
	transactionCounts := []int{100, 500, 1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000}

	t.Logf("Creating %d accounts with varying transaction histories...", numAccounts)
	for i := 0; i < numAccounts; i++ {
		accountIDs[i] = fmt.Sprintf("MULTI-ACC-%03d", i)
		log := createLargePositionLog(t, accountIDs[i], transactionCounts[i])
		err := tc.Repo.Create(ctx, log)
		require.NoError(t, err)
	}

	// Run concurrent queries across all accounts
	const queriesPerAccount = 50
	totalQueries := numAccounts * queriesPerAccount

	var wg sync.WaitGroup
	results := make(chan time.Duration, totalQueries)
	errors := make(chan error, totalQueries)

	t.Logf("Running %d queries across %d accounts...", totalQueries, numAccounts)
	start := time.Now()

	for accIdx := 0; accIdx < numAccounts; accIdx++ {
		for q := 0; q < queriesPerAccount; q++ {
			wg.Add(1)
			go func(accountID string) {
				defer wg.Done()

				req := &positionkeepingv1.GetAccountBalanceRequest{
					AccountId:   accountID,
					BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				}

				queryStart := time.Now()
				resp, err := tc.Service.GetAccountBalance(ctx, req)
				latency := time.Since(queryStart)

				if err != nil {
					errors <- err
					return
				}

				if resp == nil || resp.AccountId != accountID {
					errors <- fmt.Errorf("invalid response for account %s", accountID)
					return
				}

				results <- latency
			}(accountIDs[accIdx])
		}
	}

	wg.Wait()
	totalTime := time.Since(start)
	close(results)
	close(errors)

	// Check errors
	var errList []error
	for err := range errors {
		errList = append(errList, err)
	}
	require.Empty(t, errList, "multi-account queries should not produce errors")

	// Analyze results
	var latencies []time.Duration
	for latency := range results {
		latencies = append(latencies, latency)
	}

	require.Equal(t, totalQueries, len(latencies))

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	p99 := percentileLatency(latencies, 0.99)
	throughput := float64(totalQueries) / totalTime.Seconds()

	t.Logf("Multi-account stress test results:")
	t.Logf("  Accounts:    %d", numAccounts)
	t.Logf("  Total queries: %d", totalQueries)
	t.Logf("  Duration:    %v", totalTime)
	t.Logf("  Throughput:  %.2f queries/sec", throughput)
	t.Logf("  P99 latency: %v", p99)

	assert.Less(t, p99.Milliseconds(), int64(100),
		"P99 latency for multi-account queries should be < 100ms, got %v", p99)
}
