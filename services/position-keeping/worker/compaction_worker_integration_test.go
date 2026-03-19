package worker

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

func setupCompactionTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_compaction"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(30*time.Second)),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, pgContainer.Terminate(ctx))
	})

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable", "search_path=public")
	require.NoError(t, err)

	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Close()
	})

	// Create position table matching production schema
	_, err = pool.Exec(ctx, `
		CREATE TABLE position (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL DEFAULT 'test',
			deleted_at timestamptz NULL,
			account_id character varying(34) NOT NULL,
			instrument_code character varying(32) NOT NULL,
			bucket_key character varying(256) NOT NULL,
			amount decimal(38, 18) NOT NULL,
			dimension character varying(32) NOT NULL DEFAULT 'Monetary',
			attributes jsonb NULL,
			reference_id uuid NULL,
			PRIMARY KEY (id)
		)
	`)
	require.NoError(t, err)

	// Create indexes
	_, err = pool.Exec(ctx, `
		CREATE INDEX idx_position_aggregation ON position (account_id, instrument_code, bucket_key);
		CREATE INDEX idx_position_active ON position (account_id, instrument_code, bucket_key) WHERE deleted_at IS NULL;
	`)
	require.NoError(t, err)

	// Create compaction audit table (optional, worker handles missing table)
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_compaction_audit (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			compaction_ref uuid NOT NULL,
			consolidated_position_id uuid NOT NULL,
			original_position_ids jsonb NOT NULL,
			original_count integer NOT NULL,
			account_id character varying(34) NOT NULL,
			instrument_code character varying(32) NOT NULL,
			bucket_key character varying(256) NOT NULL,
			PRIMARY KEY (id)
		)
	`)
	require.NoError(t, err)

	return pool
}

func insertTestPosition(t *testing.T, pool *pgxpool.Pool, accountID, instrumentCode, bucketKey string, amount float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO position (account_id, instrument_code, bucket_key, amount, dimension)
		VALUES ($1, $2, $3, $4, 'Monetary')`,
		accountID, instrumentCode, bucketKey, decimal.NewFromFloat(amount))
	require.NoError(t, err)
}

func TestCompactionWorker_FindFragmentedBuckets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 3, // Low threshold for testing
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	ctx := context.Background()

	// Insert 5 rows for bucket A (above threshold of 3)
	for i := 0; i < 5; i++ {
		insertTestPosition(t, pool, "ACC-001", "GBP", "default", 10.0)
	}
	// Insert 2 rows for bucket B (below threshold)
	for i := 0; i < 2; i++ {
		insertTestPosition(t, pool, "ACC-001", "USD", "default", 20.0)
	}

	buckets, err := w.findFragmentedBuckets(ctx)
	require.NoError(t, err)

	// Only bucket A should be fragmented
	require.Len(t, buckets, 1)
	assert.Equal(t, "ACC-001", buckets[0].AccountID)
	assert.Equal(t, "GBP", buckets[0].InstrumentCode)
	assert.Equal(t, "default", buckets[0].BucketKey)
	assert.Equal(t, int64(5), buckets[0].RowCount)
}

func TestCompactionWorker_FindFragmentedBuckets_NoBuckets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 100, // High threshold
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	insertTestPosition(t, pool, "ACC-001", "GBP", "default", 10.0)

	buckets, err := w.findFragmentedBuckets(context.Background())
	require.NoError(t, err)
	assert.Empty(t, buckets)
}

func TestCompactionWorker_CompactBucket(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 2,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	ctx := context.Background()

	// Insert 4 rows for the same bucket: 10 + 20 + 30 + 40 = 100
	insertTestPosition(t, pool, "ACC-COMPACT", "GBP", "default", 10.0)
	insertTestPosition(t, pool, "ACC-COMPACT", "GBP", "default", 20.0)
	insertTestPosition(t, pool, "ACC-COMPACT", "GBP", "default", 30.0)
	insertTestPosition(t, pool, "ACC-COMPACT", "GBP", "default", 40.0)

	// Verify 4 active rows before compaction
	var countBefore int64
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM position WHERE account_id = 'ACC-COMPACT' AND deleted_at IS NULL").Scan(&countBefore)
	require.NoError(t, err)
	assert.Equal(t, int64(4), countBefore)

	// Compact the bucket
	rowsConsolidated, err := w.compactBucket(ctx, "ACC-COMPACT", "GBP", "default")
	require.NoError(t, err)
	assert.Equal(t, 4, rowsConsolidated)

	// Verify only 1 active row after compaction (the consolidated one)
	var countAfter int64
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM position WHERE account_id = 'ACC-COMPACT' AND deleted_at IS NULL").Scan(&countAfter)
	require.NoError(t, err)
	assert.Equal(t, int64(1), countAfter)

	// Verify the consolidated amount is the sum
	var totalAmount decimal.Decimal
	err = pool.QueryRow(ctx, "SELECT amount FROM position WHERE account_id = 'ACC-COMPACT' AND deleted_at IS NULL").Scan(&totalAmount)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(100.0).Equal(totalAmount))

	// Verify soft-deleted rows still exist
	var deletedCount int64
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM position WHERE account_id = 'ACC-COMPACT' AND deleted_at IS NOT NULL").Scan(&deletedCount)
	require.NoError(t, err)
	assert.Equal(t, int64(4), deletedCount)
}

func TestCompactionWorker_CompactBucket_SingleRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 1,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	// Only 1 row - should not compact
	insertTestPosition(t, pool, "ACC-SINGLE", "GBP", "default", 50.0)

	rows, err := w.compactBucket(context.Background(), "ACC-SINGLE", "GBP", "default")
	require.NoError(t, err)
	assert.Equal(t, 0, rows) // Need at least 2 to compact
}

func TestCompactionWorker_RunCompactionIteration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 2,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	ctx := context.Background()

	// Insert fragmented data for 2 buckets
	for i := 0; i < 5; i++ {
		insertTestPosition(t, pool, "ACC-ITER", "GBP", "bucket-a", 10.0)
	}
	for i := 0; i < 3; i++ {
		insertTestPosition(t, pool, "ACC-ITER", "USD", "bucket-b", 20.0)
	}

	// Run one iteration
	w.runCompactionIteration(ctx)

	// Verify bucket-a compacted: 5 -> 1
	var countA int64
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM position WHERE account_id = 'ACC-ITER' AND instrument_code = 'GBP' AND deleted_at IS NULL").Scan(&countA)
	require.NoError(t, err)
	assert.Equal(t, int64(1), countA)

	// Verify bucket-b compacted: 3 -> 1
	var countB int64
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM position WHERE account_id = 'ACC-ITER' AND instrument_code = 'USD' AND deleted_at IS NULL").Scan(&countB)
	require.NoError(t, err)
	assert.Equal(t, int64(1), countB)
}

func TestCompactionWorker_RunCompactionIteration_ContextCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 2,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Should return early without error
	w.runCompactionIteration(ctx)
}

func TestCompactionWorker_LockAndGetPositions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 2,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	ctx := context.Background()

	// Insert positions with attributes
	_, err = pool.Exec(ctx, `
		INSERT INTO position (account_id, instrument_code, bucket_key, amount, dimension, attributes, reference_id)
		VALUES
			('ACC-LOCK', 'GBP', 'default', 100.0, 'Monetary', '{"key":"val1"}', gen_random_uuid()),
			('ACC-LOCK', 'GBP', 'default', 200.0, 'Monetary', '{"key":"val2"}', gen_random_uuid())
	`)
	require.NoError(t, err)

	// Start a transaction and lock positions
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	positions, err := w.lockAndGetPositions(ctx, tx, "ACC-LOCK", "GBP", "default")
	require.NoError(t, err)
	assert.Len(t, positions, 2)

	// Verify attributes deserialized
	for _, pos := range positions {
		assert.NotNil(t, pos.Attributes)
		assert.Contains(t, pos.Attributes, "key")
		assert.False(t, pos.ReferenceID.String() == "00000000-0000-0000-0000-000000000000")
	}
}

func TestCompactionWorker_SetSearchPath_NoTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 2,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	ctx := context.Background() // No tenant context

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// Should return nil (no-op) when no tenant in context
	err = w.setSearchPath(ctx, tx)
	assert.NoError(t, err)
}

func TestCompactionWorker_SetSearchPath_WithTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 2,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	// Create a tenant schema in the test DB
	tenantID := tenant.MustNewTenantID("test_org")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	_, err = pool.Exec(context.Background(), "CREATE SCHEMA IF NOT EXISTS "+tenantID.SchemaName())
	require.NoError(t, err)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// Should set search_path to tenant schema
	err = w.setSearchPath(ctx, tx)
	assert.NoError(t, err)

	// Verify the search_path was actually set
	var searchPath string
	err = tx.QueryRow(ctx, "SHOW search_path").Scan(&searchPath)
	require.NoError(t, err)
	assert.Contains(t, searchPath, tenantID.SchemaName())
}

func TestCompactionWorker_FindFragmentedBuckets_ExcludesDeleted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 3,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	ctx := context.Background()

	// Insert 5 rows for the same bucket
	for i := 0; i < 5; i++ {
		insertTestPosition(t, pool, "ACC-DEL", "GBP", "default", 10.0)
	}

	// Soft-delete 3 of them
	_, err = pool.Exec(ctx, "UPDATE position SET deleted_at = now() WHERE id IN (SELECT id FROM position WHERE account_id = 'ACC-DEL' AND deleted_at IS NULL LIMIT 3)")
	require.NoError(t, err)

	// Only 2 active rows remain, below threshold of 3
	buckets, err := w.findFragmentedBuckets(ctx)
	require.NoError(t, err)
	assert.Empty(t, buckets)
}
