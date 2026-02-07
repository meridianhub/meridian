package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReservationRepository_Create(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("creates reservation successfully", func(t *testing.T) {
		lienID := uuid.New()
		r, err := domain.NewReservation(lienID, "ACC-001", "GBP", "bucket-1", decimal.NewFromInt(100))
		require.NoError(t, err)

		err = tc.ReservationRepo.Create(ctx, r)
		require.NoError(t, err)

		// Verify we can retrieve it
		found, err := tc.ReservationRepo.FindByLienID(ctx, lienID)
		require.NoError(t, err)
		assert.Equal(t, lienID, found.LienID)
		assert.Equal(t, "ACC-001", found.AccountID)
		assert.Equal(t, "GBP", found.InstrumentCode)
		assert.Equal(t, "bucket-1", found.BucketID)
		assert.True(t, decimal.NewFromInt(100).Equal(found.ReservedAmount))
		assert.Equal(t, domain.ReservationStatusActive, found.Status)
	})

	t.Run("returns conflict for duplicate lien_id", func(t *testing.T) {
		lienID := uuid.New()
		r1, err := domain.NewReservation(lienID, "ACC-001", "GBP", "", decimal.NewFromInt(50))
		require.NoError(t, err)

		err = tc.ReservationRepo.Create(ctx, r1)
		require.NoError(t, err)

		r2, err := domain.NewReservation(lienID, "ACC-002", "USD", "", decimal.NewFromInt(75))
		require.NoError(t, err)

		err = tc.ReservationRepo.Create(ctx, r2)
		assert.ErrorIs(t, err, domain.ErrConflict)
	})
}

func TestReservationRepository_FindByLienID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("returns not found for missing lien_id", func(t *testing.T) {
		_, err := tc.ReservationRepo.FindByLienID(ctx, uuid.New())
		assert.ErrorIs(t, err, domain.ErrReservationNotFound)
	})
}

func TestReservationRepository_UpdateStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("transitions active to executed", func(t *testing.T) {
		lienID := uuid.New()
		r, _ := domain.NewReservation(lienID, "ACC-001", "GBP", "", decimal.NewFromInt(100))
		require.NoError(t, tc.ReservationRepo.Create(ctx, r))

		err := tc.ReservationRepo.UpdateStatus(ctx, lienID, domain.ReservationStatusExecuted)
		require.NoError(t, err)

		found, err := tc.ReservationRepo.FindByLienID(ctx, lienID)
		require.NoError(t, err)
		assert.Equal(t, domain.ReservationStatusExecuted, found.Status)
		assert.NotNil(t, found.ExecutedAt)
		assert.Nil(t, found.TerminatedAt)
	})

	t.Run("transitions active to terminated", func(t *testing.T) {
		lienID := uuid.New()
		r, _ := domain.NewReservation(lienID, "ACC-001", "GBP", "", decimal.NewFromInt(100))
		require.NoError(t, tc.ReservationRepo.Create(ctx, r))

		err := tc.ReservationRepo.UpdateStatus(ctx, lienID, domain.ReservationStatusTerminated)
		require.NoError(t, err)

		found, err := tc.ReservationRepo.FindByLienID(ctx, lienID)
		require.NoError(t, err)
		assert.Equal(t, domain.ReservationStatusTerminated, found.Status)
		assert.Nil(t, found.ExecutedAt)
		assert.NotNil(t, found.TerminatedAt)
	})

	t.Run("returns already final for executed reservation", func(t *testing.T) {
		lienID := uuid.New()
		r, _ := domain.NewReservation(lienID, "ACC-001", "GBP", "", decimal.NewFromInt(100))
		require.NoError(t, tc.ReservationRepo.Create(ctx, r))
		require.NoError(t, tc.ReservationRepo.UpdateStatus(ctx, lienID, domain.ReservationStatusExecuted))

		err := tc.ReservationRepo.UpdateStatus(ctx, lienID, domain.ReservationStatusTerminated)
		assert.ErrorIs(t, err, domain.ErrReservationAlreadyFinal)
	})

	t.Run("returns not found for missing lien_id", func(t *testing.T) {
		err := tc.ReservationRepo.UpdateStatus(ctx, uuid.New(), domain.ReservationStatusExecuted)
		assert.ErrorIs(t, err, domain.ErrReservationNotFound)
	})
}

func TestReservationRepository_SumActiveReservations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("returns zero when no reservations exist", func(t *testing.T) {
		total, err := tc.ReservationRepo.SumActiveReservations(ctx, "ACC-EMPTY", "GBP", "")
		require.NoError(t, err)
		assert.True(t, decimal.Zero.Equal(total))
	})

	t.Run("sums only active reservations", func(t *testing.T) {
		accountID := "ACC-SUM-" + uuid.New().String()[:8]

		// Create 3 active reservations
		for i := 0; i < 3; i++ {
			r, _ := domain.NewReservation(uuid.New(), accountID, "GBP", "bucket-1", decimal.NewFromInt(100))
			require.NoError(t, tc.ReservationRepo.Create(ctx, r))
		}

		// Create 1 executed reservation
		executedLienID := uuid.New()
		r, _ := domain.NewReservation(executedLienID, accountID, "GBP", "bucket-1", decimal.NewFromInt(50))
		require.NoError(t, tc.ReservationRepo.Create(ctx, r))
		require.NoError(t, tc.ReservationRepo.UpdateStatus(ctx, executedLienID, domain.ReservationStatusExecuted))

		// Sum should only include active reservations (3 * 100 = 300)
		total, err := tc.ReservationRepo.SumActiveReservations(ctx, accountID, "GBP", "bucket-1")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(300).Equal(total), "expected 300, got %s", total)
	})

	t.Run("filters by bucket_id", func(t *testing.T) {
		accountID := "ACC-BUCKET-" + uuid.New().String()[:8]

		// Create reservation in bucket-1
		r1, _ := domain.NewReservation(uuid.New(), accountID, "GBP", "bucket-1", decimal.NewFromInt(100))
		require.NoError(t, tc.ReservationRepo.Create(ctx, r1))

		// Create reservation in bucket-2
		r2, _ := domain.NewReservation(uuid.New(), accountID, "GBP", "bucket-2", decimal.NewFromInt(200))
		require.NoError(t, tc.ReservationRepo.Create(ctx, r2))

		// Sum for bucket-1 only
		total, err := tc.ReservationRepo.SumActiveReservations(ctx, accountID, "GBP", "bucket-1")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(100).Equal(total))

		// Sum across all buckets
		totalAll, err := tc.ReservationRepo.SumActiveReservations(ctx, accountID, "GBP", "")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(300).Equal(totalAll))
	})

	t.Run("isolates by instrument_code", func(t *testing.T) {
		accountID := "ACC-INST-" + uuid.New().String()[:8]

		r1, _ := domain.NewReservation(uuid.New(), accountID, "GBP", "", decimal.NewFromInt(100))
		require.NoError(t, tc.ReservationRepo.Create(ctx, r1))

		r2, _ := domain.NewReservation(uuid.New(), accountID, "USD", "", decimal.NewFromInt(200))
		require.NoError(t, tc.ReservationRepo.Create(ctx, r2))

		gbpTotal, err := tc.ReservationRepo.SumActiveReservations(ctx, accountID, "GBP", "")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(100).Equal(gbpTotal))

		usdTotal, err := tc.ReservationRepo.SumActiveReservations(ctx, accountID, "USD", "")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(200).Equal(usdTotal))
	})
}

// TestReservationLifecycle_Integration tests the full lifecycle:
// RecordReservation -> GetProjectedBalance -> ReleaseReservation
func TestReservationLifecycle_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-LIFECYCLE-" + uuid.New().String()[:8]

	// Step 1: Insert some position entries
	pos1, _ := domain.NewPosition(accountID, "GBP", "default", decimal.NewFromInt(1000), "Monetary", nil, uuid.Nil, "system")
	require.NoError(t, tc.PositionRepo.Insert(ctx, pos1))

	// Step 2: Create reservations
	lien1 := uuid.New()
	lien2 := uuid.New()
	r1, _ := domain.NewReservation(lien1, accountID, "GBP", "default", decimal.NewFromInt(200))
	r2, _ := domain.NewReservation(lien2, accountID, "GBP", "default", decimal.NewFromInt(300))
	require.NoError(t, tc.ReservationRepo.Create(ctx, r1))
	require.NoError(t, tc.ReservationRepo.Create(ctx, r2))

	// Step 3: Verify projected balance
	agg, err := tc.PositionRepo.GetAggregatedPosition(ctx, accountID, "GBP", "default")
	require.NoError(t, err)
	require.NotNil(t, agg)
	assert.True(t, decimal.NewFromInt(1000).Equal(agg.TotalAmount))

	reservedTotal, err := tc.ReservationRepo.SumActiveReservations(ctx, accountID, "GBP", "default")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(500).Equal(reservedTotal))

	projectedBalance := agg.TotalAmount.Sub(reservedTotal)
	assert.True(t, decimal.NewFromInt(500).Equal(projectedBalance))

	// Step 4: Release one reservation
	require.NoError(t, tc.ReservationRepo.UpdateStatus(ctx, lien1, domain.ReservationStatusExecuted))

	// Step 5: Verify projected balance updated
	reservedTotal2, err := tc.ReservationRepo.SumActiveReservations(ctx, accountID, "GBP", "default")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(300).Equal(reservedTotal2))

	projectedBalance2 := agg.TotalAmount.Sub(reservedTotal2)
	assert.True(t, decimal.NewFromInt(700).Equal(projectedBalance2))
}
