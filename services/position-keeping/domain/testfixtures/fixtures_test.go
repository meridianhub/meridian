package testfixtures_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/domain/testfixtures"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFinancialPositionLog_Defaults(t *testing.T) {
	log := testfixtures.NewFinancialPositionLog(t)

	assert.NotEqual(t, uuid.Nil, log.LogID)
	assert.Equal(t, "TEST-ACC-001", log.AccountID)
	assert.Equal(t, domain.TransactionStatusPending, log.StatusTracking.CurrentStatus)
}

func TestNewFinancialPositionLog_CustomOptions(t *testing.T) {
	customAccountID := "CUSTOM-ACC-999"
	customAmount := decimal.NewFromInt(5000)

	log := testfixtures.NewFinancialPositionLog(t,
		testfixtures.WithAccountID(customAccountID),
		testfixtures.WithAmount(customAmount),
		testfixtures.WithCurrency(domain.CurrencyUSD),
		testfixtures.WithDirection(domain.PostingDirectionCredit),
	)

	assert.Equal(t, customAccountID, log.AccountID)

	// Verify the transaction entry has correct amount
	entries := log.TransactionLogEntries
	require.Len(t, entries, 1)
	assert.Equal(t, customAmount.String(), entries[0].Amount.Amount().String())
	assert.Equal(t, domain.CurrencyUSD, entries[0].Amount.Currency())
	assert.Equal(t, domain.PostingDirectionCredit, entries[0].Direction)
}

func TestNewTransactionCapturedEvent_Defaults(t *testing.T) {
	event := testfixtures.NewTransactionCapturedEvent(t)

	assert.NotEqual(t, uuid.Nil, event.LogID)
	assert.Equal(t, "TEST-ACC-001", event.AccountID)
	assert.NotEqual(t, uuid.Nil, event.TransactionID)
	assert.Equal(t, domain.PostingDirectionDebit, event.Direction)
	assert.Equal(t, domain.TransactionSourceManual, event.Source)
	assert.NotEmpty(t, event.CorrelationID)
}

func TestNewBulkTransactionCapturedEvent(t *testing.T) {
	tests := []struct {
		name  string
		count int
		want  int
	}{
		{"small batch", 5, 5},
		{"medium batch", 100, 100},
		{"large batch", 1000, 1000},
		{"max batch", 10000, 10000},
		{"default when zero", 0, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := testfixtures.NewBulkTransactionCapturedEvent(t, tt.count)

			assert.NotEqual(t, uuid.Nil, event.BatchID)
			assert.Equal(t, int32(tt.want), event.TransactionCount)
			assert.Len(t, event.LogIDs, tt.want)
			assert.Equal(t, domain.TransactionSourceImported, event.Source)
		})
	}
}

func TestDefaultMoneyHelpers(t *testing.T) {
	gbp := testfixtures.DefaultGBPMoney(t)
	assert.Equal(t, domain.CurrencyGBP, gbp.Currency())
	assert.Equal(t, "100", gbp.Amount().String())

	usd := testfixtures.DefaultUSDMoney(t)
	assert.Equal(t, domain.CurrencyUSD, usd.Currency())
	assert.Equal(t, "100", usd.Amount().String())

	jpy := testfixtures.DefaultJPYMoney(t)
	assert.Equal(t, domain.CurrencyJPY, jpy.Currency())
	assert.Equal(t, "10000", jpy.Amount().String())
}

func TestFixtures_ChainedOptions(t *testing.T) {
	// Demonstrate creating multiple logs with slight variations
	accountID := "TEST-CHAIN-001"
	baseAmount := decimal.NewFromInt(1000)

	log1 := testfixtures.NewFinancialPositionLog(t,
		testfixtures.WithAccountID(accountID),
		testfixtures.WithAmount(baseAmount),
		testfixtures.WithDirection(domain.PostingDirectionDebit),
	)

	log2 := testfixtures.NewFinancialPositionLog(t,
		testfixtures.WithAccountID(accountID),
		testfixtures.WithAmount(baseAmount.Mul(decimal.NewFromInt(2))),
		testfixtures.WithDirection(domain.PostingDirectionCredit),
	)

	// Both should have the same account but different transactions
	assert.Equal(t, log1.AccountID, log2.AccountID)
	assert.NotEqual(t, log1.LogID, log2.LogID)

	entries1 := log1.TransactionLogEntries
	entries2 := log2.TransactionLogEntries
	assert.NotEqual(t, entries1[0].Direction, entries2[0].Direction)
}

func TestNewBulkTransactionCapturedEvent_ZeroDefaultsToTen(t *testing.T) {
	event := testfixtures.NewBulkTransactionCapturedEvent(t, 0)

	assert.Equal(t, int32(10), event.TransactionCount)
	assert.Len(t, event.LogIDs, 10)
}

func TestNewBulkTransactionCapturedEvent_NegativeDefaultsToTen(t *testing.T) {
	event := testfixtures.NewBulkTransactionCapturedEvent(t, -5)

	assert.Equal(t, int32(10), event.TransactionCount)
	assert.Len(t, event.LogIDs, 10)
}
