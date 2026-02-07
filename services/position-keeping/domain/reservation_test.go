package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewReservation(t *testing.T) {
	t.Run("creates valid reservation", func(t *testing.T) {
		lienID := uuid.New()
		r, err := NewReservation(lienID, "acc-1", "GBP", "bucket-1", decimal.NewFromInt(100))
		require.NoError(t, err)
		assert.Equal(t, lienID, r.LienID)
		assert.Equal(t, "acc-1", r.AccountID)
		assert.Equal(t, "GBP", r.InstrumentCode)
		assert.Equal(t, "bucket-1", r.BucketID)
		assert.True(t, decimal.NewFromInt(100).Equal(r.ReservedAmount))
		assert.Equal(t, ReservationStatusActive, r.Status)
		assert.False(t, r.CreatedAt.IsZero())
		assert.Nil(t, r.ExecutedAt)
		assert.Nil(t, r.TerminatedAt)
	})

	t.Run("rejects nil lien_id", func(t *testing.T) {
		_, err := NewReservation(uuid.Nil, "acc-1", "GBP", "", decimal.NewFromInt(100))
		assert.ErrorIs(t, err, ErrEmptyLienID)
	})

	t.Run("rejects empty account_id", func(t *testing.T) {
		_, err := NewReservation(uuid.New(), "", "GBP", "", decimal.NewFromInt(100))
		assert.ErrorIs(t, err, ErrEmptyAccountID)
	})

	t.Run("rejects empty instrument_code", func(t *testing.T) {
		_, err := NewReservation(uuid.New(), "acc-1", "", "", decimal.NewFromInt(100))
		assert.ErrorIs(t, err, ErrEmptyInstrumentCode)
	})

	t.Run("rejects zero amount", func(t *testing.T) {
		_, err := NewReservation(uuid.New(), "acc-1", "GBP", "", decimal.Zero)
		assert.ErrorIs(t, err, ErrZeroReservedAmount)
	})

	t.Run("allows empty bucket_id", func(t *testing.T) {
		r, err := NewReservation(uuid.New(), "acc-1", "GBP", "", decimal.NewFromInt(50))
		require.NoError(t, err)
		assert.Equal(t, "", r.BucketID)
	})

	t.Run("allows negative amount (debit reservation)", func(t *testing.T) {
		r, err := NewReservation(uuid.New(), "acc-1", "GBP", "", decimal.NewFromInt(-50))
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(-50).Equal(r.ReservedAmount))
	})
}

func TestReservation_Release(t *testing.T) {
	t.Run("transitions to executed", func(t *testing.T) {
		r, err := NewReservation(uuid.New(), "acc-1", "GBP", "", decimal.NewFromInt(100))
		require.NoError(t, err)

		err = r.Release(ReservationStatusExecuted)
		require.NoError(t, err)
		assert.Equal(t, ReservationStatusExecuted, r.Status)
		assert.NotNil(t, r.ExecutedAt)
		assert.Nil(t, r.TerminatedAt)
	})

	t.Run("transitions to terminated", func(t *testing.T) {
		r, err := NewReservation(uuid.New(), "acc-1", "GBP", "", decimal.NewFromInt(100))
		require.NoError(t, err)

		err = r.Release(ReservationStatusTerminated)
		require.NoError(t, err)
		assert.Equal(t, ReservationStatusTerminated, r.Status)
		assert.Nil(t, r.ExecutedAt)
		assert.NotNil(t, r.TerminatedAt)
	})

	t.Run("rejects release of already executed reservation", func(t *testing.T) {
		r, err := NewReservation(uuid.New(), "acc-1", "GBP", "", decimal.NewFromInt(100))
		require.NoError(t, err)
		require.NoError(t, r.Release(ReservationStatusExecuted))

		err = r.Release(ReservationStatusTerminated)
		assert.ErrorIs(t, err, ErrReservationAlreadyFinal)
	})

	t.Run("rejects release of already terminated reservation", func(t *testing.T) {
		r, err := NewReservation(uuid.New(), "acc-1", "GBP", "", decimal.NewFromInt(100))
		require.NoError(t, err)
		require.NoError(t, r.Release(ReservationStatusTerminated))

		err = r.Release(ReservationStatusExecuted)
		assert.ErrorIs(t, err, ErrReservationAlreadyFinal)
	})

	t.Run("rejects release to ACTIVE", func(t *testing.T) {
		r, err := NewReservation(uuid.New(), "acc-1", "GBP", "", decimal.NewFromInt(100))
		require.NoError(t, err)

		err = r.Release(ReservationStatusActive)
		assert.ErrorIs(t, err, ErrInvalidReservationState)
	})
}

func TestReservationStatus_IsValid(t *testing.T) {
	assert.True(t, ReservationStatusActive.IsValid())
	assert.True(t, ReservationStatusExecuted.IsValid())
	assert.True(t, ReservationStatusTerminated.IsValid())
	assert.False(t, ReservationStatus("INVALID").IsValid())
}

func TestReservationStatus_IsTerminal(t *testing.T) {
	assert.False(t, ReservationStatusActive.IsTerminal())
	assert.True(t, ReservationStatusExecuted.IsTerminal())
	assert.True(t, ReservationStatusTerminated.IsTerminal())
}
