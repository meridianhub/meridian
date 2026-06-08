package worker

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/await"
)

// newBranchWorker builds a worker against the given pool with a fast,
// low-threshold config suitable for exercising error and lifecycle branches.
func newBranchWorker(t *testing.T, pool *pgxpool.Pool) *CompactionWorker {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 2,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)
	return w
}

// TestFindFragmentedBuckets_BeginError exercises the pool.Begin failure path
// by closing the pool before querying.
func TestFindFragmentedBuckets_BeginError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	pool.Close()

	buckets, err := w.findFragmentedBuckets(context.Background())
	require.Error(t, err)
	assert.Nil(t, buckets)
	assert.Contains(t, err.Error(), "failed to begin transaction")
}

// TestFindFragmentedBuckets_QueryError exercises the query failure path by
// dropping the position table so the SELECT fails.
func TestFindFragmentedBuckets_QueryError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	_, err := pool.Exec(context.Background(), "DROP TABLE position")
	require.NoError(t, err)

	buckets, err := w.findFragmentedBuckets(context.Background())
	require.Error(t, err)
	assert.Nil(t, buckets)
	assert.Contains(t, err.Error(), "failed to query fragmented buckets")
}

// TestCompactBucket_BeginError exercises the BeginTx failure path on a closed pool.
func TestCompactBucket_BeginError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	pool.Close()

	rows, err := w.compactBucket(context.Background(), "ACC", "GBP", "default")
	require.Error(t, err)
	assert.Equal(t, 0, rows)
	assert.Contains(t, err.Error(), "failed to begin transaction")
}

// TestCompactBucket_LockError exercises the lockAndGetPositions failure path
// by dropping the position table after the worker is constructed.
func TestCompactBucket_LockError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	_, err := pool.Exec(context.Background(), "DROP TABLE position")
	require.NoError(t, err)

	rows, err := w.compactBucket(context.Background(), "ACC", "GBP", "default")
	require.Error(t, err)
	assert.Equal(t, 0, rows)
	assert.Contains(t, err.Error(), "failed to lock and query positions")
}

// TestRunCompactionIteration_FindError exercises the findFragmentedBuckets
// error branch inside runCompactionIteration (scan-error path).
func TestRunCompactionIteration_FindError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	_, err := pool.Exec(context.Background(), "DROP TABLE position")
	require.NoError(t, err)

	// Should not panic; the error is handled internally and logged.
	w.runCompactionIteration(context.Background())
}

// TestRunCompactionIteration_NoBuckets covers the no-op branch inside
// runCompactionIteration where findFragmentedBuckets returns zero buckets.
func TestRunCompactionIteration_NoBuckets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 100, // high threshold -> nothing fragmented
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	insertTestPosition(t, pool, "ACC-EMPTY", "GBP", "default", 10.0)

	// Should take the "no fragmented buckets found" early return without error.
	w.runCompactionIteration(context.Background())
}

// TestProcessFragmentedBuckets_CompactError exercises the per-bucket compact
// failure branch: a bucket is reported as fragmented, then the table is dropped
// so compaction of that bucket fails and the error is tallied.
func TestProcessFragmentedBuckets_CompactError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	buckets := []FragmentedBucket{
		{AccountID: "ACC", InstrumentCode: "GBP", BucketKey: "default", RowCount: 5},
	}

	// Drop the table so compactBucket fails for the reported bucket.
	_, err := pool.Exec(context.Background(), "DROP TABLE position")
	require.NoError(t, err)

	processed, consolidated, errs := w.processFragmentedBuckets(context.Background(), buckets)
	assert.Equal(t, 0, processed)
	assert.Equal(t, 0, consolidated)
	require.Len(t, errs, 1)
}

// TestProcessFragmentedBuckets_ContextCancelled exercises the ctx.Done early
// return inside the per-bucket loop.
func TestProcessFragmentedBuckets_ContextCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before processing

	buckets := []FragmentedBucket{
		{AccountID: "ACC", InstrumentCode: "GBP", BucketKey: "default", RowCount: 5},
	}

	processed, consolidated, errs := w.processFragmentedBuckets(ctx, buckets)
	assert.Equal(t, 0, processed)
	assert.Equal(t, 0, consolidated)
	assert.Empty(t, errs)
}

// TestProcessFragmentedBuckets_DoneSignal exercises the w.done early return
// inside the per-bucket loop.
func TestProcessFragmentedBuckets_DoneSignal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	// Signal shutdown so the loop takes the w.done branch.
	close(w.done)

	buckets := []FragmentedBucket{
		{AccountID: "ACC", InstrumentCode: "GBP", BucketKey: "default", RowCount: 5},
	}

	processed, consolidated, errs := w.processFragmentedBuckets(context.Background(), buckets)
	assert.Equal(t, 0, processed)
	assert.Equal(t, 0, consolidated)
	assert.Empty(t, errs)
}

// TestRunCompactionIteration_FullSuccessPath drives the success path through
// runCompactionIteration where buckets are found and consolidated, covering the
// "found fragmented buckets" log branch and processFragmentedBuckets success.
func TestRunCompactionIteration_FullSuccessPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	ctx := context.Background()
	for i := 0; i < 4; i++ {
		insertTestPosition(t, pool, "ACC-FULL", "GBP", "default", 10.0)
	}

	w.runCompactionIteration(ctx)

	var active int64
	err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM position WHERE account_id = 'ACC-FULL' AND deleted_at IS NULL").Scan(&active)
	require.NoError(t, err)
	assert.Equal(t, int64(1), active)
}

// TestCompactBucket_AuditMissingPoisonsCommit covers both the
// tryInsertAuditRecord warn branch and the compactBucket commit-error branch.
//
// Note on real behavior: tryInsertAuditRecord swallows its error and logs a
// warning, but under PostgreSQL a failed statement aborts the surrounding
// transaction. So when the optional audit table is absent, the audit INSERT
// fails, the transaction is poisoned, and the subsequent Commit() resolves to
// a rollback. The function therefore surfaces a commit error rather than
// succeeding. This test pins that observable behavior and exercises both
// branches in one pass.
func TestCompactBucket_AuditMissingPoisonsCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	ctx := context.Background()

	// Remove the optional audit table so the audit INSERT fails.
	_, err := pool.Exec(ctx, "DROP TABLE position_compaction_audit")
	require.NoError(t, err)

	insertTestPosition(t, pool, "ACC-NOAUDIT", "GBP", "default", 10.0)
	insertTestPosition(t, pool, "ACC-NOAUDIT", "GBP", "default", 20.0)

	rows, err := w.compactBucket(ctx, "ACC-NOAUDIT", "GBP", "default")
	require.Error(t, err)
	assert.Equal(t, 0, rows)
	assert.Contains(t, err.Error(), "failed to commit compaction transaction")

	// Originals remain active because the transaction rolled back.
	var active int64
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM position WHERE account_id = 'ACC-NOAUDIT' AND deleted_at IS NULL").Scan(&active)
	require.NoError(t, err)
	assert.Equal(t, int64(2), active)
}

// TestCompactBucket_InsertError covers the insertConsolidatedPosition error
// branch by adding a CHECK constraint that the consolidated row violates.
// The two source rows individually satisfy amount < 100, but their sum (150)
// does not, so the consolidated insert fails.
func TestCompactBucket_InsertError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	ctx := context.Background()

	insertTestPosition(t, pool, "ACC-INS", "GBP", "default", 50.0)
	insertTestPosition(t, pool, "ACC-INS", "GBP", "default", 100.0)

	// Constraint passes for the existing rows but fails for the 150 sum.
	_, err := pool.Exec(ctx, "ALTER TABLE position ADD CONSTRAINT amount_cap CHECK (amount < 150)")
	require.NoError(t, err)

	rows, err := w.compactBucket(ctx, "ACC-INS", "GBP", "default")
	require.Error(t, err)
	assert.Equal(t, 0, rows)
	assert.Contains(t, err.Error(), "failed to insert consolidated position")

	// Originals must remain active (transaction rolled back).
	var active int64
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM position WHERE account_id = 'ACC-INS' AND deleted_at IS NULL").Scan(&active)
	require.NoError(t, err)
	assert.Equal(t, int64(2), active)
}

// TestCompactBucket_SoftDeleteError covers the softDeletePositions error
// branch (and its propagation through executeCompaction). A CHECK constraint
// forbids setting deleted_at to a non-null value, so the soft-delete UPDATE
// fails after the consolidated row is inserted.
func TestCompactBucket_SoftDeleteError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	w := newBranchWorker(t, pool)

	ctx := context.Background()

	insertTestPosition(t, pool, "ACC-DELERR", "GBP", "default", 10.0)
	insertTestPosition(t, pool, "ACC-DELERR", "GBP", "default", 20.0)

	// Forbid soft-deletes: deleted_at must stay NULL. The consolidated INSERT
	// (deleted_at NULL) passes; the soft-delete UPDATE violates the constraint.
	_, err := pool.Exec(ctx, "ALTER TABLE position ADD CONSTRAINT no_delete CHECK (deleted_at IS NULL)")
	require.NoError(t, err)

	rows, err := w.compactBucket(ctx, "ACC-DELERR", "GBP", "default")
	require.Error(t, err)
	assert.Equal(t, 0, rows)
	assert.Contains(t, err.Error(), "failed to soft delete original positions")

	// Originals remain active because the transaction rolled back.
	var active int64
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM position WHERE account_id = 'ACC-DELERR' AND deleted_at IS NULL").Scan(&active)
	require.NoError(t, err)
	assert.Equal(t, int64(2), active)
}

// TestStart_RunsInitialIterationAndStops drives Start through a real iteration
// (initial immediate compaction) and then a clean Stop, covering the running
// transition, the initial tryStartIteration path, and shutdown.
func TestStart_RunsInitialIterationAndStops(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       50 * time.Millisecond,
		FragmentThreshold: 2,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	ctx := context.Background()

	// Seed fragmented data so the initial iteration actually compacts.
	for i := 0; i < 3; i++ {
		insertTestPosition(t, pool, "ACC-START", "GBP", "default", 5.0)
	}

	startErr := make(chan error, 1)
	go func() {
		startErr <- w.Start(ctx)
	}()

	// Wait for the worker to report running.
	require.NoError(t, await.New().AtMost(2*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.running
	}), "worker should reach running state")

	// Wait until the initial compaction collapses the bucket to one active row.
	require.NoError(t, await.New().AtMost(3*time.Second).PollInterval(20*time.Millisecond).Until(func() bool {
		var active int64
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM position WHERE account_id = 'ACC-START' AND deleted_at IS NULL").Scan(&active); err != nil {
			return false
		}
		return active == 1
	}), "initial iteration should consolidate the bucket")

	w.Stop()

	select {
	case err := <-startErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}

// TestStart_TickFiresAfterStopped covers the ticker branch where
// tryStartIteration returns false (worker stopping) and Start exits via the
// explicit-shutdown path inside the ticker case.
func TestStart_TickFiresAfterStopped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupCompactionTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w, err := NewCompactionWorker(pool, CompactionConfig{
		RunInterval:       40 * time.Millisecond,
		FragmentThreshold: 2,
		BatchSize:         10,
	}, logger)
	require.NoError(t, err)

	// Mark stopped before Start so both the initial tryStartIteration and the
	// first ticker tryStartIteration return false, driving the ticker-case
	// shutdown branch (tryStartIteration == false -> markStopped, return nil).
	w.mu.Lock()
	w.stopped = true
	w.mu.Unlock()

	startErr := make(chan error, 1)
	go func() {
		startErr <- w.Start(context.Background())
	}()

	select {
	case err := <-startErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return via ticker shutdown branch")
	}

	// Worker should have marked itself not running on exit.
	w.mu.Lock()
	running := w.running
	w.mu.Unlock()
	assert.False(t, running)
}
