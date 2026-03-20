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

func TestReservationRepository_UpdateStatus_ActiveReturnsInvalid(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Trying to transition to ACTIVE is invalid
	lienID := uuid.New()
	r, err := domain.NewReservation(lienID, "ACC-001", "GBP", "", decimal.NewFromInt(100))
	require.NoError(t, err)

	err = tc.ReservationRepo.Create(ctx, r)
	require.NoError(t, err)

	err = tc.ReservationRepo.UpdateStatus(ctx, lienID, domain.ReservationStatusActive)
	assert.ErrorIs(t, err, domain.ErrInvalidReservationState)
}

func TestReservationRepository_CreateNilReservation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	err := tc.ReservationRepo.Create(ctx, nil)
	require.Error(t, err)
}
