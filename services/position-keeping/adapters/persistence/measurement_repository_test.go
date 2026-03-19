package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestMeasurement creates a valid domain.Measurement for testing.
func createTestMeasurement(t *testing.T, positionLogID uuid.UUID) *domain.Measurement {
	t.Helper()
	m, err := domain.NewMeasurement(
		positionLogID,
		domain.MeasurementTypeKWh,
		decimal.NewFromFloat(42.5),
		"kWh",
		time.Now().UTC().Add(-1*time.Hour),
		map[string]string{"source": "meter-001"},
		"bucket-alpha",
		"test-user",
	)
	require.NoError(t, err)
	return m
}

// setupMeasurementTestContainer creates a postgres testcontainer with the
// measurement table added on top of the base schema used by other persistence tests.
func setupMeasurementTestContainer(t *testing.T) (*testContainer, *persistence.MeasurementRepository) {
	t.Helper()

	tc := setupTestContainer(t)

	// Add measurement table
	ctx := context.Background()
	_, err := tc.pool.Exec(ctx, `
		CREATE TABLE position_keeping.measurement (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			financial_position_log_id uuid NOT NULL,
			measurement_type character varying(50) NOT NULL,
			value decimal(38, 18) NOT NULL,
			unit character varying(20) NOT NULL,
			timestamp timestamptz NOT NULL,
			metadata jsonb NULL,
			bucket_id character varying(256) NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_measurement_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err)

	measurementRepo := persistence.NewMeasurementRepository(tc.pool)
	return tc, measurementRepo
}

// persistTestLog creates and persists a FinancialPositionLog, returning its LogID.
func persistTestLog(ctx context.Context, t *testing.T, tc *testContainer, accountID string) uuid.UUID {
	t.Helper()
	log := createTestLog(t, accountID)
	err := tc.repo.Create(ctx, log)
	require.NoError(t, err)
	return log.LogID
}

func TestMeasurementRepository_Create(t *testing.T) {
	tc, repo := setupMeasurementTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	logID := persistTestLog(ctx, t, tc, testAccountID)

	t.Run("successful create", func(t *testing.T) {
		m := createTestMeasurement(t, logID)
		err := repo.Create(ctx, m)
		require.NoError(t, err)
	})

	t.Run("nil measurement returns error", func(t *testing.T) {
		err := repo.Create(ctx, nil)
		assert.ErrorIs(t, err, persistence.ErrNilMeasurement)
	})

	t.Run("duplicate ID returns conflict", func(t *testing.T) {
		m := createTestMeasurement(t, logID)
		err := repo.Create(ctx, m)
		require.NoError(t, err)

		err = repo.Create(ctx, m)
		assert.ErrorIs(t, err, domain.ErrConflict)
	})

	t.Run("nonexistent position log returns not found", func(t *testing.T) {
		m := createTestMeasurement(t, uuid.New())
		err := repo.Create(ctx, m)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("nil metadata serializes correctly", func(t *testing.T) {
		m := createTestMeasurement(t, logID)
		m.Metadata = nil
		err := repo.Create(ctx, m)
		require.NoError(t, err)
	})

	t.Run("empty bucket_id stored as NULL", func(t *testing.T) {
		m := createTestMeasurement(t, logID)
		m.BucketID = ""
		err := repo.Create(ctx, m)
		require.NoError(t, err)

		found, err := repo.FindByID(ctx, m.ID)
		require.NoError(t, err)
		assert.Empty(t, found.BucketID)
	})
}

func TestMeasurementRepository_FindByID(t *testing.T) {
	tc, repo := setupMeasurementTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	logID := persistTestLog(ctx, t, tc, testAccountID)

	t.Run("found", func(t *testing.T) {
		m := createTestMeasurement(t, logID)
		err := repo.Create(ctx, m)
		require.NoError(t, err)

		found, err := repo.FindByID(ctx, m.ID)
		require.NoError(t, err)
		assert.Equal(t, m.ID, found.ID)
		assert.Equal(t, logID, found.FinancialPositionLogID)
		assert.Equal(t, domain.MeasurementTypeKWh, found.MeasurementType)
		assert.True(t, m.Value.Equal(found.Value))
		assert.Equal(t, "kWh", found.Unit)
		assert.Equal(t, "bucket-alpha", found.BucketID)
		assert.Equal(t, "meter-001", found.Metadata["source"])
	})

	t.Run("not found", func(t *testing.T) {
		_, err := repo.FindByID(ctx, uuid.New())
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("nil metadata round-trips", func(t *testing.T) {
		m := createTestMeasurement(t, logID)
		m.Metadata = nil
		err := repo.Create(ctx, m)
		require.NoError(t, err)

		found, err := repo.FindByID(ctx, m.ID)
		require.NoError(t, err)
		assert.Nil(t, found.Metadata)
	})
}

func TestMeasurementRepository_FindByPositionLogID(t *testing.T) {
	tc, repo := setupMeasurementTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	logID := persistTestLog(ctx, t, tc, testAccountID)

	t.Run("returns measurements ordered by timestamp desc", func(t *testing.T) {
		now := time.Now().UTC()
		for i := 0; i < 3; i++ {
			m := createTestMeasurement(t, logID)
			m.Timestamp = now.Add(time.Duration(i) * time.Hour)
			err := repo.Create(ctx, m)
			require.NoError(t, err)
		}

		measurements, err := repo.FindByPositionLogID(ctx, logID)
		require.NoError(t, err)
		assert.Len(t, measurements, 3)
		// Should be desc order
		assert.True(t, measurements[0].Timestamp.After(measurements[2].Timestamp))
	})

	t.Run("nonexistent log returns empty slice", func(t *testing.T) {
		measurements, err := repo.FindByPositionLogID(ctx, uuid.New())
		require.NoError(t, err)
		assert.Empty(t, measurements)
	})

	t.Run("no measurements returns empty", func(t *testing.T) {
		emptyLogID := persistTestLog(ctx, t, tc, "GB33BUKB20201555555556")
		measurements, err := repo.FindByPositionLogID(ctx, emptyLogID)
		require.NoError(t, err)
		assert.Empty(t, measurements)
	})
}

func TestMeasurementRepository_CreateWithTx(t *testing.T) {
	tc, repo := setupMeasurementTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	logID := persistTestLog(ctx, t, tc, testAccountID)

	t.Run("successful create within tx", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		dbLogID, err := repo.GetDBPositionLogID(ctx, tx, logID)
		require.NoError(t, err)

		m := createTestMeasurement(t, logID)
		err = repo.CreateWithTx(ctx, tx, m, dbLogID)
		require.NoError(t, err)

		err = tx.Commit(ctx)
		require.NoError(t, err)

		found, err := repo.FindByID(ctx, m.ID)
		require.NoError(t, err)
		assert.Equal(t, m.ID, found.ID)
	})

	t.Run("nil measurement returns error", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		err = repo.CreateWithTx(ctx, tx, nil, uuid.New())
		assert.ErrorIs(t, err, persistence.ErrNilMeasurement)
	})

	t.Run("duplicate ID returns conflict", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		dbLogID, err := repo.GetDBPositionLogID(ctx, tx, logID)
		require.NoError(t, err)

		m := createTestMeasurement(t, logID)
		err = repo.CreateWithTx(ctx, tx, m, dbLogID)
		require.NoError(t, err)
		err = tx.Commit(ctx)
		require.NoError(t, err)

		// Second insert with same ID
		tx2, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer func() { _ = tx2.Rollback(ctx) }()

		err = repo.CreateWithTx(ctx, tx2, m, dbLogID)
		assert.ErrorIs(t, err, domain.ErrConflict)
	})
}

func TestMeasurementRepository_GetDBPositionLogID(t *testing.T) {
	tc, repo := setupMeasurementTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	logID := persistTestLog(ctx, t, tc, testAccountID)

	t.Run("found", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		dbID, err := repo.GetDBPositionLogID(ctx, tx, logID)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, dbID)
	})

	t.Run("not found", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		_, err = repo.GetDBPositionLogID(ctx, tx, uuid.New())
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestMeasurementRepository_BeginTx(t *testing.T) {
	tc, repo := setupMeasurementTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	assert.NotNil(t, tx)
	_ = tx.Rollback(ctx)
}
