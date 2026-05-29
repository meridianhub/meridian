// Integration tests for the CSV import resume guard.
//
// These tests drive the real importProcessor.processRow path against a live
// CockroachDB container to prove the resume off-by-one fix end to end:
// a data row already committed before an interruption must not be reprocessed
// (and thus duplicated as a ledger position) when the import resumes and
// re-reads the source file from the top.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	csvadapter "github.com/meridianhub/meridian/cmd/position-tool/internal/adapters/csv"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/checkpoint"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/infra"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/validation"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

const (
	resumeTestInstrument = "CARBON_CREDIT"
	resumeTestTenant     = "resume-test-tenant"
	// fungibilityExpr yields a non-empty bucket key from row attributes so
	// domain.NewPosition (which rejects empty bucket keys) succeeds.
	resumeTestFungibilityExpr = `attributes["registry"]`
)

// setupResumeTestPool returns a pgx pool on a CockroachDB testcontainer with a
// minimal position table.
//
// It uses testdb.NewCockroachTestPool for production parity: the BatchInserter
// requires a *pgxpool.Pool, so SetupCockroachDB (which returns a *gorm.DB) does
// not fit, and NewTestPool is Postgres-backed. Migrations are applied manually
// rather than via WithMigrations because the production position-keeping
// migrations include plpgsql triggers, which CockroachDB does not support; this
// test only INSERTs (never UPDATEs) positions, so the append-only trigger and
// the rest of the schema are unnecessary.
func setupResumeTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool := testdb.NewCockroachTestPool(t)

	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE "position" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"created_by" character varying(100) NOT NULL,
			"deleted_at" timestamptz NULL,
			"account_id" character varying(34) NOT NULL,
			"instrument_code" character varying(32) NOT NULL,
			"bucket_key" character varying(256) NOT NULL,
			"amount" decimal(38, 18) NOT NULL,
			"dimension" character varying(32) NOT NULL DEFAULT 'Monetary',
			"attributes" jsonb NULL,
			"reference_id" uuid NULL,
			PRIMARY KEY ("id")
		)
	`)
	require.NoError(t, err, "failed to create position table")

	return pool
}

// buildResumeTestRows builds n data rows mirroring a CSV layout: the header
// occupies line 1, so the first data row is line 2 and the nth is line n+1.
func buildResumeTestRows(n int) []csvadapter.ImportRow {
	rows := make([]csvadapter.ImportRow, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, csvadapter.ImportRow{
			LineNumber:     i + 1, // line 1 is the header
			AccountID:      fmt.Sprintf("ACC-%03d", i),
			InstrumentCode: resumeTestInstrument,
			Amount:         fmt.Sprintf("%d", 100+i),
			Timestamp:      time.Now().UTC(),
			Attributes:     map[string]string{"registry": "VERRA", "vintage_year": "2024"},
		})
	}
	return rows
}

// newResumeTestProcessor builds an importProcessor wired to the live pool but
// with a stubbed instrument checker so no reference-data gRPC service is needed.
// Each call models a fresh process invocation (its own batch inserter) while the
// caller carries a single checkpoint across phases, exactly as resume does.
func newResumeTestProcessor(t *testing.T, pool *pgxpool.Pool, cp *checkpoint.Checkpoint) *importProcessor {
	t.Helper()

	celEval, err := infra.NewCELEvaluatorDefault()
	require.NoError(t, err, "failed to create CEL evaluator")

	pipeline, err := validation.NewPipeline(validation.PipelineConfig{
		DuplicateChecker: validation.NewDuplicateChecker(
			validation.DefaultBloomFilterConfig(),
			createDuplicateLookup(pool),
		),
		InstrumentChecker: &validation.MockInstrumentChecker{
			CheckFunc: func(_ context.Context, _ string, _ int) (*validation.InstrumentCheckResult, error) {
				return &validation.InstrumentCheckResult{Exists: true, IsActive: true}, nil
			},
		},
		Logger: slog.Default(),
	})
	require.NoError(t, err, "failed to create validation pipeline")

	batchInserter, err := infra.NewBatchInserter(infra.BatchInserterConfig{
		Pool:      pool,
		BatchSize: 100,
	})
	require.NoError(t, err, "failed to create batch inserter")

	return &importProcessor{
		cfg:           &importConfig{TenantID: resumeTestTenant, BatchSize: 100},
		cp:            cp,
		result:        &importResult{},
		celEval:       celEval,
		pipeline:      pipeline,
		batchInserter: batchInserter,
		logger:        slog.Default(),
		// Pre-populated so processRow never calls the (nil) concrete instrument
		// checker for the fungibility-key lookup.
		fungibilityExprs: map[string]string{resumeTestInstrument: resumeTestFungibilityExpr},
	}
}

// runImportPass processes every supplied row through a fresh processor and
// flushes the batch, returning positions to the database. The shared checkpoint
// is mutated in place so a subsequent pass observes prior progress.
func runImportPass(ctx context.Context, t *testing.T, pool *pgxpool.Pool, cp *checkpoint.Checkpoint, rows []csvadapter.ImportRow) {
	t.Helper()
	proc := newResumeTestProcessor(t, pool, cp)
	for i := range rows {
		require.NoError(t, proc.processRow(ctx, &rows[i]))
	}
	require.NoError(t, proc.batchInserter.Flush(ctx))
}

func countAllResumePositions(ctx context.Context, t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var count int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM "position"`).Scan(&count))
	return count
}

func countResumePositionsForAccount(ctx context.Context, t *testing.T, pool *pgxpool.Pool, accountID string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM "position" WHERE account_id = $1`, accountID).Scan(&count))
	return count
}

// TestIntegration_ImportResumeGuard exercises the resume guard at its boundaries.
//
// A source file has a header on line 1 and 10 data rows on lines 2..11. M
// processed data rows therefore occupy lines 2..M+1. On resume the import
// re-reads the whole file; the guard must skip exactly the already-committed
// lines and process the rest once.
func TestIntegration_ImportResumeGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	const totalDataRows = 10
	ctx := context.Background()
	pool := setupResumeTestPool(t)
	rows := buildResumeTestRows(totalDataRows)

	t.Run("boundary row is not reprocessed on resume", func(t *testing.T) {
		const committed = 5
		// Fresh state for this subtest.
		_, err := pool.Exec(ctx, `DELETE FROM "position"`)
		require.NoError(t, err)

		cp := &checkpoint.Checkpoint{TenantID: resumeTestTenant, Status: checkpoint.StatusRunning}

		// Phase 1: commit the first 5 data rows (lines 2..6), then interrupt.
		runImportPass(ctx, t, pool, cp, rows[:committed])
		require.Equal(t, committed, cp.ProcessedRows, "five data rows committed before interruption")
		require.Equal(t, int64(committed), countAllResumePositions(ctx, t, pool))

		// Phase 2: resume - re-read the whole file (all 10 rows). The guard must
		// skip lines 2..6 and process lines 7..11 exactly once. The boundary is
		// line 6 (the 5th, last-committed data row -> ACC-005): the pre-fix guard
		// (<= ProcessedRows) would reprocess it and duplicate its position.
		runImportPass(ctx, t, pool, cp, rows)

		assert.Equal(t, int64(totalDataRows), countAllResumePositions(ctx, t, pool),
			"resume must not duplicate any committed row")
		for i := 1; i <= totalDataRows; i++ {
			acct := fmt.Sprintf("ACC-%03d", i)
			assert.Equal(t, int64(1), countResumePositionsForAccount(ctx, t, pool, acct),
				"account %s must have exactly one position", acct)
		}
	})

	t.Run("resume after zero rows processes all", func(t *testing.T) {
		_, err := pool.Exec(ctx, `DELETE FROM "position"`)
		require.NoError(t, err)

		cp := &checkpoint.Checkpoint{TenantID: resumeTestTenant, Status: checkpoint.StatusRunning}
		runImportPass(ctx, t, pool, cp, rows)

		assert.Equal(t, totalDataRows, cp.ProcessedRows)
		assert.Equal(t, int64(totalDataRows), countAllResumePositions(ctx, t, pool),
			"a fresh import (ProcessedRows=0) must process every row")
	})

	t.Run("resume after all rows processes none", func(t *testing.T) {
		_, err := pool.Exec(ctx, `DELETE FROM "position"`)
		require.NoError(t, err)

		cp := &checkpoint.Checkpoint{TenantID: resumeTestTenant, Status: checkpoint.StatusRunning}
		// Commit all 10 rows.
		runImportPass(ctx, t, pool, cp, rows)
		require.Equal(t, totalDataRows, cp.ProcessedRows)
		require.Equal(t, int64(totalDataRows), countAllResumePositions(ctx, t, pool))

		// Resume after a completed pass: re-reading the file must add nothing.
		runImportPass(ctx, t, pool, cp, rows)
		assert.Equal(t, int64(totalDataRows), countAllResumePositions(ctx, t, pool),
			"resuming a fully-processed file must not insert duplicates")
	})
}
