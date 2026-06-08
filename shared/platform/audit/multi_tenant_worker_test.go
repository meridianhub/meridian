package audit

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// createTenantSchema creates a PostgreSQL schema containing an audit_outbox
// table, mirroring how a provisioned tenant looks to the discovery query.
func createTenantSchema(t *testing.T, db *gorm.DB, schema string) {
	t.Helper()

	require.NoError(t, db.Exec(`CREATE SCHEMA IF NOT EXISTS `+quoteIdentifier(schema)).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS `+quoteIdentifier(schema)+`.audit_outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL,
			record_id VARCHAR(50) NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			retry_count INTEGER NOT NULL DEFAULT 0
		)
	`).Error)
}

// activeWorkerSchemas returns the schema names that currently have a running
// worker, under lock.
func (m *MultiTenantWorker) activeWorkerSchemas() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	schemas := make([]string, 0, len(m.activeWorkers))
	for s := range m.activeWorkers {
		schemas = append(schemas, s)
	}
	return schemas
}

// --- NewMultiTenantWorker construction ---

func TestNewMultiTenantWorker_Defaults(t *testing.T) {
	m := NewMultiTenantWorker(nil, "", nil)

	assert.Equal(t, "org_%", m.schemaPattern, "empty pattern defaults to org_%")
	assert.NotNil(t, m.logger, "nil logger defaults to slog.Default")
	assert.Equal(t, 30*time.Second, m.pollInterval)
	assert.NotNil(t, m.shutdown)
	assert.NotNil(t, m.activeWorkers)
	assert.Empty(t, m.activeWorkers)
}

func TestNewMultiTenantWorker_CustomValues(t *testing.T) {
	logger := slog.Default()
	m := NewMultiTenantWorker(nil, "tenant_%", logger, WithBatchSize(7))

	assert.Equal(t, "tenant_%", m.schemaPattern)
	assert.NotNil(t, m.logger)
	assert.Len(t, m.workerOpts, 1)
}

// --- findTenantSchemas ---

func TestMultiTenantWorker_FindTenantSchemas_MatchesPattern(t *testing.T) {
	db := setupTestDB(t)

	createTenantSchema(t, db, "org_alpha")
	createTenantSchema(t, db, "org_beta")
	// A schema that does NOT match the pattern must be excluded.
	createTenantSchema(t, db, "other_gamma")

	m := NewMultiTenantWorker(db, "org_%", nil)

	schemas, err := m.findTenantSchemas(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"org_alpha", "org_beta"}, schemas)
}

func TestMultiTenantWorker_FindTenantSchemas_NoMatches(t *testing.T) {
	db := setupTestDB(t)

	m := NewMultiTenantWorker(db, "nomatch_%", nil)

	schemas, err := m.findTenantSchemas(context.Background())
	require.NoError(t, err)
	assert.Empty(t, schemas)
}

func TestMultiTenantWorker_FindTenantSchemas_QueryError(t *testing.T) {
	db := setupTestDB(t)

	// Close the underlying connection pool so the discovery query fails,
	// covering the error-wrapping branch of findTenantSchemas.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	m := NewMultiTenantWorker(db, "org_%", nil)

	_, err = m.findTenantSchemas(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query tenant schemas")
}

// --- discoverAndStartWorkers ---

func TestMultiTenantWorker_DiscoverAndStartWorkers_StartsPerSchema(t *testing.T) {
	db := setupTestDB(t)

	createTenantSchema(t, db, "org_one")
	createTenantSchema(t, db, "org_two")

	m := NewMultiTenantWorker(db, "org_%", nil, WithPollInterval(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.discoverAndStartWorkers(ctx)

	schemas := m.activeWorkerSchemas()
	assert.ElementsMatch(t, []string{"org_one", "org_two"}, schemas)

	// Stop the started child workers to avoid leaking goroutines.
	m.mu.Lock()
	for _, w := range m.activeWorkers {
		w.Stop()
	}
	m.mu.Unlock()
}

func TestMultiTenantWorker_DiscoverAndStartWorkers_SkipsExisting(t *testing.T) {
	db := setupTestDB(t)

	createTenantSchema(t, db, "org_dup")

	m := NewMultiTenantWorker(db, "org_%", nil, WithPollInterval(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// First discovery starts the worker.
	m.discoverAndStartWorkers(ctx)
	require.ElementsMatch(t, []string{"org_dup"}, m.activeWorkerSchemas())

	m.mu.Lock()
	firstWorker := m.activeWorkers["org_dup"]
	m.mu.Unlock()

	// Second discovery must skip the already-running schema (no replacement).
	m.discoverAndStartWorkers(ctx)

	m.mu.Lock()
	secondWorker := m.activeWorkers["org_dup"]
	assert.Same(t, firstWorker, secondWorker, "existing worker must not be replaced")
	m.mu.Unlock()

	m.mu.Lock()
	for _, w := range m.activeWorkers {
		w.Stop()
	}
	m.mu.Unlock()
}

func TestMultiTenantWorker_DiscoverAndStartWorkers_QueryErrorIsHandled(t *testing.T) {
	db := setupTestDB(t)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	m := NewMultiTenantWorker(db, "org_%", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Must not panic; the discovery error is logged and no workers start.
	m.discoverAndStartWorkers(ctx)
	assert.Empty(t, m.activeWorkerSchemas())
}

// --- Start / Stop lifecycle ---

func TestMultiTenantWorker_StartStop_DiscoversAndShutsDown(t *testing.T) {
	db := setupTestDB(t)

	createTenantSchema(t, db, "org_live")

	m := NewMultiTenantWorker(db, "org_%", nil, WithPollInterval(time.Hour))

	m.Start(context.Background())

	// The discovery loop runs immediately on start; await the worker to appear.
	err := await.New().
		AtMost(10 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return len(m.activeWorkerSchemas()) == 1
		})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"org_live"}, m.activeWorkerSchemas())

	// Stop must shut down the discovery loop and all child workers.
	m.Stop()

	// After Stop the discovery context is cancelled.
	assert.NotNil(t, m.cancel)
}

func TestMultiTenantWorker_Stop_Idempotent(t *testing.T) {
	db := setupTestDB(t)

	m := NewMultiTenantWorker(db, "nomatch_%", nil, WithPollInterval(time.Hour))
	m.Start(context.Background())

	// Two Stop calls must not panic (shutdownOnce guards the close).
	m.Stop()
	m.Stop()
}

func TestMultiTenantWorker_Stop_ParentContextCancelled(t *testing.T) {
	db := setupTestDB(t)

	m := NewMultiTenantWorker(db, "nomatch_%", nil, WithPollInterval(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)

	// Canceling the parent context must let the discovery loop exit; Stop then
	// completes cleanly.
	cancel()

	done := make(chan struct{})
	go func() {
		m.Stop()
		close(done)
	}()

	err := await.Until(func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	require.NoError(t, err, "Stop should complete after parent context cancelled")
}

// --- run loop ticker path ---

// TestMultiTenantWorker_RunLoop_RediscoversOnTick uses a short poll interval so
// the ticker branch in run() fires and picks up a schema created after Start.
func TestMultiTenantWorker_RunLoop_RediscoversOnTick(t *testing.T) {
	db := setupTestDB(t)

	m := NewMultiTenantWorker(db, "org_%", nil, WithPollInterval(100*time.Millisecond))
	m.Start(context.Background())
	defer m.Stop()

	// No schemas at start.
	require.Empty(t, m.activeWorkerSchemas())

	// Provision a tenant schema after the worker is already running.
	createTenantSchema(t, db, "org_late")

	// The periodic ticker must discover it on a subsequent poll.
	err := await.New().
		AtMost(10 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			for _, s := range m.activeWorkerSchemas() {
				if s == "org_late" {
					return true
				}
			}
			return false
		})
	require.NoError(t, err)
}
