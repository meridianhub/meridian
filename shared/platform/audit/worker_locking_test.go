package audit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// createPendingOutboxEntries inserts count pending audit_outbox rows for locking tests.
func createPendingOutboxEntries(t *testing.T, db *gorm.DB, count int) {
	t.Helper()

	for i := 0; i < count; i++ {
		entry := &AuditOutbox{
			ID:        uuid.New(),
			Table:     "customer",
			Operation: "INSERT",
			RecordID:  uuid.New().String(),
			NewValues: `{"id": "123", "name": "Test Customer"}`,
			Status:    StatusPending,
			CreatedAt: time.Now(),
		}
		require.NoError(t, db.Create(entry).Error, "failed to create pending outbox entry")
	}
}

// TestAuditWorker_ClaimPendingEntries_ConcurrentWorkersClaimDisjointRows verifies that
// two worker instances polling the same outbox table concurrently never claim the same
// row, and that neither blocks waiting on the other's lock (FOR UPDATE SKIP LOCKED).
//
// Because SELECT ... FOR UPDATE SKIP LOCKED combined with ORDER BY/LIMIT picks its
// candidate rows before checking locks, a worker that loses the race for the same
// candidate window can legitimately claim zero rows in a single round rather than
// falling back to the next unlocked rows - this is documented Postgres/CockroachDB
// behavior, not a bug. What must hold is: no entry is ever claimed twice, no worker
// blocks, and nothing is lost - unclaimed entries stay pending for the next poll.
func TestAuditWorker_ClaimPendingEntries_ConcurrentWorkersClaimDisjointRows(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()
	testdb.CreateAuditTables(t, db)

	const totalEntries = 40
	createPendingOutboxEntries(t, db, totalEntries)

	workerA := NewAuditWorker(db, "", nil)
	workerA.batchSize = totalEntries / 2
	workerB := NewAuditWorker(db, "", nil)
	workerB.batchSize = totalEntries / 2

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([][]AuditOutbox, 2)
	errs := make([]error, 2)
	durations := make([]time.Duration, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		begin := time.Now()
		results[0], errs[0] = workerA.claimPendingEntries(context.Background())
		durations[0] = time.Since(begin)
	}()
	go func() {
		defer wg.Done()
		<-start
		begin := time.Now()
		results[1], errs[1] = workerB.claimPendingEntries(context.Background())
		durations[1] = time.Since(begin)
	}()
	close(start)
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	// SKIP LOCKED must never block: both claims should complete quickly even though
	// they raced to lock the same candidate rows.
	assert.Less(t, durations[0], 5*time.Second, "worker A's claim should not block on worker B's lock")
	assert.Less(t, durations[1], 5*time.Second, "worker B's claim should not block on worker A's lock")

	// No two workers may ever claim the same row.
	seen := make(map[uuid.UUID]bool, totalEntries)
	for _, batch := range results {
		for _, entry := range batch {
			assert.False(t, seen[entry.ID], "entry %s claimed by more than one worker", entry.ID)
			seen[entry.ID] = true
		}
	}
	require.NotEmpty(t, seen, "at least one worker should have claimed entries in the initial race")

	var processingCount int64
	require.NoError(t, db.Model(&AuditOutbox{}).Where("status = ?", StatusProcessing).Count(&processingCount).Error)
	assert.Equal(t, int64(len(seen)), processingCount, "only entries actually claimed should be marked processing")

	// Anything not claimed in the concurrent round is still pending, not lost. Simulate
	// subsequent polls (sequentially, standing in for the ticker's next tick) until every
	// entry has been claimed exactly once, proving SKIP LOCKED never drops work.
	for len(seen) < totalEntries {
		more, err := workerA.claimPendingEntries(context.Background())
		require.NoError(t, err)
		require.NotEmpty(t, more, "remaining pending entries must eventually be claimable")

		for _, entry := range more {
			assert.False(t, seen[entry.ID], "entry %s claimed by more than one worker across polls", entry.ID)
			seen[entry.ID] = true
		}
	}

	assert.Len(t, seen, totalEntries, "every entry should eventually be claimed exactly once")

	var pendingCount int64
	require.NoError(t, db.Model(&AuditOutbox{}).Where("status = ?", StatusPending).Count(&pendingCount).Error)
	assert.Equal(t, int64(0), pendingCount, "no pending entries should remain once all rounds complete")
}

// TestAuditWorker_ClaimPendingEntries_RespectsBatchSize verifies that claiming stops at
// batchSize even when more pending entries exist, leaving the remainder for the next poll.
func TestAuditWorker_ClaimPendingEntries_RespectsBatchSize(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()
	testdb.CreateAuditTables(t, db)

	createPendingOutboxEntries(t, db, 15)

	worker := NewAuditWorker(db, "", nil)
	worker.batchSize = 10

	entries, err := worker.claimPendingEntries(context.Background())
	require.NoError(t, err)
	assert.Len(t, entries, 10)

	var pendingCount int64
	require.NoError(t, db.Model(&AuditOutbox{}).Where("status = ?", StatusPending).Count(&pendingCount).Error)
	assert.Equal(t, int64(5), pendingCount, "unclaimed entries should remain pending")
}
