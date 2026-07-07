package service_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
)

func gbp(amount int64) domain.Money {
	return domain.MustNewMoney(decimal.NewFromInt(amount), domain.CurrencyGBP)
}

func usd(amount int64) domain.Money {
	return domain.MustNewMoney(decimal.NewFromInt(amount), domain.CurrencyUSD)
}

// TestResolveAggregateInstrument covers instrument selection from the per-instrument
// aggregate returned by the repository. Currency resolution feeds every downstream
// balance computation, so a wrong instrument silently misattributes a balance.
func TestResolveAggregateInstrument(t *testing.T) {
	t.Run("empty instrument code with single instrument returns that instrument", func(t *testing.T) {
		balances := []domain.AccountInstrumentBalance{
			{Instrument: gbp(0).Instrument, NetMovement: gbp(175)},
		}

		got, err := service.ResolveAggregateInstrumentForTesting(balances, "")
		require.NoError(t, err)
		assert.Equal(t, "GBP", got.Instrument.Code)
		assert.Equal(t, "175", got.Amount.String())
	})

	t.Run("empty instrument code with no entries falls back to zero GBP", func(t *testing.T) {
		got, err := service.ResolveAggregateInstrumentForTesting(nil, "")
		require.NoError(t, err)
		assert.Equal(t, "GBP", got.Instrument.Code)
		assert.True(t, got.IsZero())
	})

	t.Run("empty instrument code with multiple instruments is ambiguous", func(t *testing.T) {
		balances := []domain.AccountInstrumentBalance{
			{Instrument: gbp(0).Instrument, NetMovement: gbp(100)},
			{Instrument: usd(0).Instrument, NetMovement: usd(200)},
		}

		_, err := service.ResolveAggregateInstrumentForTesting(balances, "")
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("matching instrument code selects that instrument", func(t *testing.T) {
		balances := []domain.AccountInstrumentBalance{
			{Instrument: gbp(0).Instrument, NetMovement: gbp(100)},
			{Instrument: usd(0).Instrument, NetMovement: usd(200)},
		}

		got, err := service.ResolveAggregateInstrumentForTesting(balances, "USD")
		require.NoError(t, err)
		assert.Equal(t, "USD", got.Instrument.Code)
		assert.Equal(t, "200", got.Amount.String())
	})

	t.Run("non-matching instrument code returns NotFound", func(t *testing.T) {
		balances := []domain.AccountInstrumentBalance{
			{Instrument: gbp(0).Instrument, NetMovement: gbp(100)},
		}

		_, err := service.ResolveAggregateInstrumentForTesting(balances, "USD")
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Contains(t, st.Message(), "USD")
	})
}

// TestBuildAggregateLog verifies the synthetic aggregate log encodes the net
// movement so the LogBalanceComputer derives the correct balance.
func TestBuildAggregateLog(t *testing.T) {
	t.Run("positive net movement becomes a single DEBIT entry", func(t *testing.T) {
		log, err := service.BuildAggregateLogForTesting("ACC-POS", gbp(175))
		require.NoError(t, err)
		require.Len(t, log.TransactionLogEntries, 1)
		entry := log.TransactionLogEntries[0]
		assert.Equal(t, domain.PostingDirectionDebit, entry.Direction)
		assert.Equal(t, "175", entry.Amount.Amount.String())
		assert.Equal(t, "GBP", entry.Amount.Instrument.Code)
	})

	t.Run("negative net movement becomes a single CREDIT entry", func(t *testing.T) {
		log, err := service.BuildAggregateLogForTesting("ACC-NEG", gbp(-50))
		require.NoError(t, err)
		require.Len(t, log.TransactionLogEntries, 1)
		entry := log.TransactionLogEntries[0]
		assert.Equal(t, domain.PostingDirectionCredit, entry.Direction)
		assert.Equal(t, "50", entry.Amount.Amount.String())
	})

	t.Run("zero net movement yields a log with no entries", func(t *testing.T) {
		log, err := service.BuildAggregateLogForTesting("ACC-ZERO", gbp(0))
		require.NoError(t, err)
		assert.Empty(t, log.TransactionLogEntries)
	})
}
