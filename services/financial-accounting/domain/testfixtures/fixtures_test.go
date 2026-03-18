package testfixtures_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/services/financial-accounting/domain/testfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBookingLogBuilder_Defaults(t *testing.T) {
	log := testfixtures.NewBookingLogBuilder().Build()
	require.NotNil(t, log)
}

func TestBookingLogBuilder_WithAllOptions(t *testing.T) {
	log := testfixtures.NewBookingLogBuilder().
		WithAccountType("LIABILITY").
		WithProductServiceRef("PROD-999").
		WithBusinessUnitRef("BU-RISK").
		WithChartOfAccountsRules("IFRS-2024").
		WithCurrency(domain.CurrencyUSD).
		Build()
	require.NotNil(t, log)
}

func TestPostingBuilder_Defaults(t *testing.T) {
	posting, err := testfixtures.NewPostingBuilder().Build()
	require.NoError(t, err)
	require.NotNil(t, posting)
	assert.Equal(t, domain.PostingDirectionDebit, posting.Direction)
}

func TestPostingBuilder_WithAllOptions(t *testing.T) {
	logID := uuid.New()
	valueDate := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	posting, err := testfixtures.NewPostingBuilder().
		WithBookingLogID(logID).
		WithDirection(domain.PostingDirectionCredit).
		WithAmount(testfixtures.NewMoneyFixture().GBP("250.00")).
		WithAccountID("ACC-CREDIT").
		WithValueDate(valueDate).
		WithCorrelationID("corr-custom").
		Build()
	require.NoError(t, err)
	require.NotNil(t, posting)
	assert.Equal(t, domain.PostingDirectionCredit, posting.Direction)
	assert.Equal(t, "ACC-CREDIT", posting.AccountID)
}

func TestPostingBuilder_WithAmountCents(t *testing.T) {
	posting, err := testfixtures.NewPostingBuilder().
		WithAmountCents(10050). // £100.50
		Build()
	require.NoError(t, err)
	require.NotNil(t, posting)
}

func TestPostingBuilder_MustBuild(t *testing.T) {
	posting := testfixtures.NewPostingBuilder().MustBuild()
	require.NotNil(t, posting)
}

func TestMoneyFixture_GBP(t *testing.T) {
	m := testfixtures.NewMoneyFixture()
	money := m.GBP("100.00")
	assert.Equal(t, "100", money.Amount.String())
}

func TestMoneyFixture_USD(t *testing.T) {
	m := testfixtures.NewMoneyFixture()
	money := m.USD("250.00")
	assert.Equal(t, "250", money.Amount.String())
}

func TestMoneyFixture_EUR(t *testing.T) {
	m := testfixtures.NewMoneyFixture()
	money := m.EUR("75.50")
	assert.Equal(t, "75.5", money.Amount.String())
}

func TestMoneyFixture_GBPCents(t *testing.T) {
	m := testfixtures.NewMoneyFixture()
	money := m.GBPCents(10000) // £100.00
	assert.Equal(t, "100", money.Amount.String())
}

func TestMoneyFixture_USDCents(t *testing.T) {
	m := testfixtures.NewMoneyFixture()
	money := m.USDCents(5050) // $50.50
	assert.Equal(t, "50.5", money.Amount.String())
}

func TestBalancedPostingPair(t *testing.T) {
	logID := uuid.New()
	m := testfixtures.NewMoneyFixture()
	amount := m.GBP("100.00")

	debit, credit, err := testfixtures.BalancedPostingPair(logID, amount)
	require.NoError(t, err)
	require.NotNil(t, debit)
	require.NotNil(t, credit)
	assert.Equal(t, domain.PostingDirectionDebit, debit.Direction)
	assert.Equal(t, domain.PostingDirectionCredit, credit.Direction)
	assert.Equal(t, debit.FinancialBookingLogID, credit.FinancialBookingLogID)
}
