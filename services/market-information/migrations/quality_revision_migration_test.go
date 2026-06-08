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
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
