// Package migrations_test verifies the market-information SQL migrations apply
// cleanly on CockroachDB (production parity) and that the quality-ladder
// reconciliation migrations behave as specified in ADR-0017:
//   - the quality CHECK constraint widens to the four-level ladder IN (1,2,3,4)
//   - the revision column is added with a default of 0
//   - the revision backfill marks correction rows
package migrations_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readMigration reads a migration SQL file from this test's own directory.
func readMigration(t *testing.T, name string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to resolve test file path")
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filename), name))
	require.NoError(t, err, "failed to read migration %s", name)
	return string(data)
}

// pgErrorCode extracts the SQLSTATE code from a pgx error, or "" if not a PgError.
func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// seedIDs fetches a dataset definition id and data source id from the seed data
// loaded by the initial migration, for use as foreign keys when inserting
// observations.
func seedIDs(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (datasetID, sourceID uuid.UUID) {
	t.Helper()

	err := pool.QueryRow(ctx,
		`SELECT id FROM dataset_definition WHERE code = 'FX_RATE' LIMIT 1`).Scan(&datasetID)
	require.NoError(t, err, "expected seeded FX_RATE dataset definition")

	err = pool.QueryRow(ctx,
		`SELECT id FROM data_source WHERE code = 'ECB_DAILY' LIMIT 1`).Scan(&sourceID)
	require.NoError(t, err, "expected seeded ECB_DAILY data source")

	return datasetID, sourceID
}

// insertObservation inserts a market price observation with the given quality and
// returns its generated id. It returns the underlying error (if any) so callers
// can assert on constraint rejections.
func insertObservation(ctx context.Context, pool *pgxpool.Pool, datasetID, sourceID uuid.UUID, resolutionKey string, quality int) (uuid.UUID, error) {
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO market_price_observation (
			id, dataset_definition_id, data_source_id, resolution_key,
			observed_at, created_by, quality, numeric_value
		) VALUES ($1, $2, $3, $4, now(), 'test', $5, 1.2345)
	`, id, datasetID, sourceID, resolutionKey, quality)
	return id, err
}

// TestMigrations_QualityLadder_AcceptsFourLevels verifies the widened CHECK
// constraint accepts the full four-level confidence ladder, including VERIFIED(4),
// and still rejects out-of-range values.
func TestMigrations_QualityLadder_AcceptsFourLevels(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migration test in short mode")
	}

	ctx := context.Background()
	pool := testdb.NewCockroachTestPool(t, testdb.WithMigrations("market-information"))
	datasetID, sourceID := seedIDs(ctx, t, pool)

	t.Run("accepts each level 1-4 including VERIFIED", func(t *testing.T) {
		// quality=4 is the VERIFIED level under the four-level confidence encoding
		// (proto slot 4, still spelled QUALITY_LEVEL_REVISED; the symbol rename is
		// pending task 14). PR #2249 only widened the CHECK to accept value 4; it
		// did NOT re-encode existing rows. The data re-encode from the legacy
		// three-level encoding to the four-level ladder lands in
		// 20260608000005_remap_quality_to_4level.sql (this PR), exercised by
		// TestMigrations_RemapQuality_ReEncodesLegacyLevels below.
		for quality := 1; quality <= 4; quality++ {
			_, err := insertObservation(ctx, pool, datasetID, sourceID, "EUR/USD", quality)
			require.NoError(t, err, "quality %d should be accepted", quality)
		}
	})

	t.Run("rejects out-of-range quality", func(t *testing.T) {
		// CockroachDB reports the constraint expression rather than its name, so
		// assert on the SQLSTATE check_violation code (23514) instead.
		for _, quality := range []int{0, 5, 99} {
			_, err := insertObservation(ctx, pool, datasetID, sourceID, "EUR/USD", quality)
			require.Error(t, err, "quality %d should be rejected", quality)
			assert.Equal(t, "23514", pgErrorCode(err), "quality %d should violate the check constraint", quality)
		}
	})
}

// TestMigrations_RevisionColumn_DefaultsToZero verifies the revision column exists
// and defaults to 0 (original) for newly inserted observations.
func TestMigrations_RevisionColumn_DefaultsToZero(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migration test in short mode")
	}

	ctx := context.Background()
	pool := testdb.NewCockroachTestPool(t, testdb.WithMigrations("market-information"))
	datasetID, sourceID := seedIDs(ctx, t, pool)

	id, err := insertObservation(ctx, pool, datasetID, sourceID, "GBP/USD", 3)
	require.NoError(t, err)

	var revision int
	err = pool.QueryRow(ctx,
		`SELECT revision FROM market_price_observation WHERE id = $1`, id).Scan(&revision)
	require.NoError(t, err)
	assert.Equal(t, 0, revision, "revision should default to 0 (original)")
}

// revisionOf returns the revision value for a given observation id.
func revisionOf(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var revision int
	err := pool.QueryRow(ctx,
		`SELECT revision FROM market_price_observation WHERE id = $1`, id).Scan(&revision)
	require.NoError(t, err)
	return revision
}

// TestMigrations_BackfillRevision_MarksCorrectionRows verifies the backfill sets
// revision=1 on correction rows (the newer rows that supersede an earlier
// observation) while leaving the superseded originals at revision 0.
func TestMigrations_BackfillRevision_MarksCorrectionRows(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migration test in short mode")
	}

	ctx := context.Background()
	pool := testdb.NewCockroachTestPool(t, testdb.WithMigrations("market-information"))
	datasetID, sourceID := seedIDs(ctx, t, pool)

	// Build a supersession chain: original was replaced by correction.
	// superseded_by is a forward pointer, so original.superseded_by = correction.id.
	correctionID, err := insertObservation(ctx, pool, datasetID, sourceID, "EUR/USD", 3)
	require.NoError(t, err)
	originalID, err := insertObservation(ctx, pool, datasetID, sourceID, "EUR/USD", 1)
	require.NoError(t, err)

	_, err = pool.Exec(ctx,
		`UPDATE market_price_observation SET superseded_by = $1 WHERE id = $2`,
		correctionID, originalID)
	require.NoError(t, err)

	// A standalone observation with no supersession should remain at revision 0.
	standaloneID, err := insertObservation(ctx, pool, datasetID, sourceID, "GBP/USD", 2)
	require.NoError(t, err)

	// Re-run the actual backfill migration SQL against this data.
	_, err = pool.Exec(ctx, readMigration(t, "20260608000004_backfill_revision.sql"))
	require.NoError(t, err)

	assert.Equal(t, 1, revisionOf(ctx, t, pool, correctionID),
		"the correction row (which supersedes another) should be revision 1")
	assert.Equal(t, 0, revisionOf(ctx, t, pool, originalID),
		"the superseded original should remain revision 0")
	assert.Equal(t, 0, revisionOf(ctx, t, pool, standaloneID),
		"a standalone observation should remain revision 0")
}

// qualityOf returns the quality value for a given observation id.
func qualityOf(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var quality int
	err := pool.QueryRow(ctx,
		`SELECT quality FROM market_price_observation WHERE id = $1`, id).Scan(&quality)
	require.NoError(t, err)
	return quality
}

// TestMigrations_RemapQuality_ReEncodesLegacyLevels verifies the data-remap
// migration re-encodes the legacy three-level quality encoding
// (1=ESTIMATE, 2=ACTUAL, 3=VERIFIED) to the four-level confidence ladder
// (1=ESTIMATE, 2=PROVISIONAL, 3=ACTUAL, 4=VERIFIED). Without this remap, the
// post-cutover code would silently misread every legacy 2 as PROVISIONAL and
// every legacy 3 as ACTUAL: a silent confidence downgrade.
func TestMigrations_RemapQuality_ReEncodesLegacyLevels(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migration test in short mode")
	}

	ctx := context.Background()
	pool := testdb.NewCockroachTestPool(t, testdb.WithMigrations("market-information"))
	datasetID, sourceID := seedIDs(ctx, t, pool)

	// Seed rows carrying the legacy encoding. legacy 1=ESTIMATE, 2=ACTUAL,
	// 3=VERIFIED. The widened CHECK (PR #2249) admits all of these.
	legacyEstimateID, err := insertObservation(ctx, pool, datasetID, sourceID, "EUR/USD", 1)
	require.NoError(t, err)
	legacyActualID, err := insertObservation(ctx, pool, datasetID, sourceID, "GBP/USD", 2)
	require.NoError(t, err)
	legacyVerifiedID, err := insertObservation(ctx, pool, datasetID, sourceID, "USD/JPY", 3)
	require.NoError(t, err)

	// Re-run the actual remap migration SQL against this legacy-encoded data.
	_, err = pool.Exec(ctx, readMigration(t, "20260608000005_remap_quality_to_4level.sql"))
	require.NoError(t, err)

	assert.Equal(t, 1, qualityOf(ctx, t, pool, legacyEstimateID),
		"legacy ESTIMATE (1) should remain 1 (ESTIMATE)")
	assert.Equal(t, 3, qualityOf(ctx, t, pool, legacyActualID),
		"legacy ACTUAL (2) should re-encode to 3 (ACTUAL)")
	assert.Equal(t, 4, qualityOf(ctx, t, pool, legacyVerifiedID),
		"legacy VERIFIED (3) should re-encode to 4 (VERIFIED)")
}
