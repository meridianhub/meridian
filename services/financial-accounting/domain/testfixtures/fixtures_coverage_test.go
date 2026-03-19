package testfixtures_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/services/financial-accounting/domain/testfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostingBuilder_MustBuild_Panics_OnInvalidInput(t *testing.T) {
	// Build with nil booking log ID should panic since uuid.Nil triggers validation error
	assert.Panics(t, func() {
		testfixtures.NewPostingBuilder().
			WithBookingLogID(uuid.Nil).
			MustBuild()
	})
}

func TestBalancedPostingPair_InvalidAmount(t *testing.T) {
	logID := uuid.New()
	// Create a zero-amount Money which should fail validation in NewLedgerPosting
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	zeroAmount := domain.NewMoney(
		// Use the decimal package via the fixture helper
		testfixtures.NewMoneyFixture().GBP("0.00").Amount,
		inst,
	)

	debit, credit, err := testfixtures.BalancedPostingPair(logID, zeroAmount)
	require.Error(t, err)
	assert.Nil(t, debit)
	assert.Nil(t, credit)
}

func TestBalancedPostingPair_AccountIDs(t *testing.T) {
	logID := uuid.New()
	m := testfixtures.NewMoneyFixture()
	amount := m.GBP("50.00")

	debit, credit, err := testfixtures.BalancedPostingPair(logID, amount)
	require.NoError(t, err)

	assert.Equal(t, "ACC-DEBIT", debit.AccountID)
	assert.Equal(t, "ACC-CREDIT", credit.AccountID)
	assert.Equal(t, logID, debit.FinancialBookingLogID)
	assert.Equal(t, logID, credit.FinancialBookingLogID)
}
