package stripe

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"
)

func TestSettlementTransformer_TransformToSnapshots(t *testing.T) {
	transformer := NewSettlementTransformer()
	runID := uuid.New()
	accountID := "meridian-acct-001"

	t.Run("transforms payment transaction", func(t *testing.T) {
		availableOn := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
		transactions := []*stripego.BalanceTransaction{
			{
				ID:          "txn_payment_001",
				Amount:      1000, // 10.00 GBP
				Net:         970,  // 9.70 GBP (after fees)
				Fee:         30,   // 0.30 GBP
				Currency:    "gbp",
				Type:        stripego.BalanceTransactionTypeCharge,
				AvailableOn: availableOn.Unix(),
				Description: "Payment for order #123",
				Source:      &stripego.BalanceTransactionSource{ID: "ch_abc123"},
			},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		require.Len(t, snapshots, 1)

		snap := snapshots[0]
		assert.Equal(t, runID, snap.RunID)
		assert.Equal(t, accountID, snap.AccountID)
		assert.Equal(t, "GBP", snap.InstrumentCode)
		assert.True(t, decimal.Zero.Equal(snap.ExpectedBalance))
		assert.True(t, decimal.NewFromFloat(9.70).Equal(snap.ActualBalance))
		assert.Equal(t, SourceSystemStripe, snap.SourceSystem)

		// Verify attributes
		assert.Equal(t, "txn_payment_001", snap.Attributes["external_reference_id"])
		assert.Equal(t, "charge", snap.Attributes["stripe_type"])
		assert.Equal(t, "PAYMENT", snap.Attributes["settlement_type"])
		assert.Equal(t, "9.7", snap.Attributes["net_amount"])
		assert.Equal(t, "10", snap.Attributes["gross_amount"])
		assert.Equal(t, "0.3", snap.Attributes["fee_amount"])
		assert.Equal(t, "Payment for order #123", snap.Attributes["description"])
		assert.Equal(t, "ch_abc123", snap.Attributes["stripe_source_id"])
		assert.Equal(t, "NOSTRO_VOSTRO", snap.Attributes["data_source_type"])
	})

	t.Run("transforms refund transaction", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{
				ID:          "txn_refund_001",
				Amount:      -500, // -5.00 GBP
				Net:         -500,
				Fee:         0,
				Currency:    "gbp",
				Type:        stripego.BalanceTransactionTypeRefund,
				AvailableOn: time.Now().Unix(),
			},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		require.Len(t, snapshots, 1)

		snap := snapshots[0]
		assert.True(t, decimal.NewFromFloat(-5.00).Equal(snap.ActualBalance))
		assert.Equal(t, "REFUND", snap.Attributes["settlement_type"])
		assert.Equal(t, "refund", snap.Attributes["stripe_type"])
	})

	t.Run("transforms payout transaction", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{
				ID:          "txn_payout_001",
				Amount:      -10000,
				Net:         -10000,
				Fee:         0,
				Currency:    "gbp",
				Type:        stripego.BalanceTransactionTypePayout,
				AvailableOn: time.Now().Unix(),
			},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		require.Len(t, snapshots, 1)

		assert.Equal(t, "PAYOUT", snapshots[0].Attributes["settlement_type"])
	})

	t.Run("transforms fee transaction", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{
				ID:          "txn_fee_001",
				Amount:      -25,
				Net:         -25,
				Fee:         0,
				Currency:    "gbp",
				Type:        stripego.BalanceTransactionTypeStripeFee,
				AvailableOn: time.Now().Unix(),
			},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		require.Len(t, snapshots, 1)

		assert.Equal(t, "FEE", snapshots[0].Attributes["settlement_type"])
	})

	t.Run("transforms transfer transaction", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{
				ID:          "txn_transfer_001",
				Amount:      5000,
				Net:         5000,
				Fee:         0,
				Currency:    "gbp",
				Type:        stripego.BalanceTransactionTypeTransfer,
				AvailableOn: time.Now().Unix(),
			},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		require.Len(t, snapshots, 1)

		assert.Equal(t, "TRANSFER", snapshots[0].Attributes["settlement_type"])
	})

	t.Run("transforms adjustment transaction", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{
				ID:          "txn_adjust_001",
				Amount:      150,
				Net:         150,
				Fee:         0,
				Currency:    "gbp",
				Type:        stripego.BalanceTransactionTypeAdjustment,
				AvailableOn: time.Now().Unix(),
			},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		require.Len(t, snapshots, 1)

		assert.Equal(t, "ADJUSTMENT", snapshots[0].Attributes["settlement_type"])
	})

	t.Run("unknown type maps to OTHER", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{
				ID:          "txn_unknown_001",
				Amount:      100,
				Net:         100,
				Fee:         0,
				Currency:    "gbp",
				Type:        stripego.BalanceTransactionTypeContribution,
				AvailableOn: time.Now().Unix(),
			},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		require.Len(t, snapshots, 1)

		assert.Equal(t, "OTHER", snapshots[0].Attributes["settlement_type"])
	})

	t.Run("transforms multiple transactions", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{ID: "txn_1", Amount: 1000, Net: 970, Fee: 30, Currency: "gbp", Type: stripego.BalanceTransactionTypeCharge, AvailableOn: time.Now().Unix()},
			{ID: "txn_2", Amount: -200, Net: -200, Fee: 0, Currency: "gbp", Type: stripego.BalanceTransactionTypeRefund, AvailableOn: time.Now().Unix()},
			{ID: "txn_3", Amount: -30, Net: -30, Fee: 0, Currency: "gbp", Type: stripego.BalanceTransactionTypeStripeFee, AvailableOn: time.Now().Unix()},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		assert.Len(t, snapshots, 3)

		assert.Equal(t, "PAYMENT", snapshots[0].Attributes["settlement_type"])
		assert.Equal(t, "REFUND", snapshots[1].Attributes["settlement_type"])
		assert.Equal(t, "FEE", snapshots[2].Attributes["settlement_type"])
	})

	t.Run("empty transactions produces empty result", func(t *testing.T) {
		snapshots, err := transformer.TransformToSnapshots(runID, accountID, nil)
		require.NoError(t, err)
		assert.Empty(t, snapshots)
	})

	t.Run("currency is uppercased", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{ID: "txn_usd", Amount: 100, Net: 100, Currency: "usd", Type: stripego.BalanceTransactionTypeCharge, AvailableOn: time.Now().Unix()},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		assert.Equal(t, "USD", snapshots[0].InstrumentCode)
	})

	t.Run("source without ID omits stripe_source_id", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{ID: "txn_nosrc", Amount: 100, Net: 100, Currency: "gbp", Type: stripego.BalanceTransactionTypeCharge, AvailableOn: time.Now().Unix()},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		_, hasSourceID := snapshots[0].Attributes["stripe_source_id"]
		assert.False(t, hasSourceID)
	})

	t.Run("zero-decimal currency JPY amounts are not divided by 100", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{
				ID:          "txn_jpy_001",
				Amount:      1000, // 1000 JPY (NOT 10.00 JPY)
				Net:         970,  // 970 JPY
				Fee:         30,   // 30 JPY
				Currency:    "jpy",
				Type:        stripego.BalanceTransactionTypeCharge,
				AvailableOn: time.Now().Unix(),
			},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		require.Len(t, snapshots, 1)

		snap := snapshots[0]
		assert.Equal(t, "JPY", snap.InstrumentCode)
		assert.True(t, decimal.NewFromInt(970).Equal(snap.ActualBalance), "expected 970 JPY, got %s", snap.ActualBalance)
		assert.Equal(t, "1000", snap.Attributes["gross_amount"])
		assert.Equal(t, "30", snap.Attributes["fee_amount"])
		assert.Equal(t, "970", snap.Attributes["net_amount"])
	})

	t.Run("empty description is omitted", func(t *testing.T) {
		transactions := []*stripego.BalanceTransaction{
			{ID: "txn_nodesc", Amount: 100, Net: 100, Currency: "gbp", Type: stripego.BalanceTransactionTypeCharge, AvailableOn: time.Now().Unix()},
		}

		snapshots, err := transformer.TransformToSnapshots(runID, accountID, transactions)
		require.NoError(t, err)
		_, hasDesc := snapshots[0].Attributes["description"]
		assert.False(t, hasDesc)
	})
}

func TestMapTransactionType(t *testing.T) {
	tests := []struct {
		stripeType stripego.BalanceTransactionType
		expected   SettlementTransactionType
	}{
		{stripego.BalanceTransactionTypeCharge, SettlementTypePayment},
		{stripego.BalanceTransactionTypePayment, SettlementTypePayment},
		{stripego.BalanceTransactionTypeRefund, SettlementTypeRefund},
		{stripego.BalanceTransactionTypePaymentRefund, SettlementTypeRefund},
		{stripego.BalanceTransactionTypeApplicationFeeRefund, SettlementTypeRefund},
		{stripego.BalanceTransactionTypeTransferRefund, SettlementTypeRefund},
		{stripego.BalanceTransactionTypePayout, SettlementTypePayout},
		{stripego.BalanceTransactionTypePayoutCancel, SettlementTypePayout},
		{stripego.BalanceTransactionTypePayoutFailure, SettlementTypePayout},
		{stripego.BalanceTransactionTypeStripeFee, SettlementTypeFee},
		{stripego.BalanceTransactionTypeStripeFxFee, SettlementTypeFee},
		{stripego.BalanceTransactionTypeTaxFee, SettlementTypeFee},
		{stripego.BalanceTransactionTypeApplicationFee, SettlementTypeFee},
		{stripego.BalanceTransactionTypeIssuingDispute, SettlementTypeDispute},
		{stripego.BalanceTransactionTypeTransfer, SettlementTypeTransfer},
		{stripego.BalanceTransactionTypeTransferCancel, SettlementTypeTransfer},
		{stripego.BalanceTransactionTypeTransferFailure, SettlementTypeTransfer},
		{stripego.BalanceTransactionTypeConnectCollectionTransfer, SettlementTypeTransfer},
		{stripego.BalanceTransactionTypeAdjustment, SettlementTypeAdjustment},
		{stripego.BalanceTransactionTypeContribution, SettlementTypeOther},
		{stripego.BalanceTransactionTypeTopup, SettlementTypeOther},
	}

	for _, tt := range tests {
		t.Run(string(tt.stripeType), func(t *testing.T) {
			result := mapTransactionType(tt.stripeType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAmountToDecimal(t *testing.T) {
	t.Run("standard currency divides by 100", func(t *testing.T) {
		tests := []struct {
			amount   int64
			expected string
		}{
			{1000, "10"},
			{999, "9.99"},
			{1, "0.01"},
			{0, "0"},
			{-500, "-5"},
			{-1, "-0.01"},
			{100050, "1000.5"},
		}

		for _, tt := range tests {
			t.Run(tt.expected, func(t *testing.T) {
				result := amountToDecimal(tt.amount, "GBP")
				assert.Equal(t, tt.expected, result.String())
			})
		}
	})

	t.Run("zero-decimal currency does not divide", func(t *testing.T) {
		tests := []struct {
			amount   int64
			currency string
			expected string
		}{
			{1000, "JPY", "1000"},
			{500, "KRW", "500"},
			{1, "VND", "1"},
			{0, "BIF", "0"},
			{-200, "XAF", "-200"},
		}

		for _, tt := range tests {
			t.Run(tt.currency+"_"+tt.expected, func(t *testing.T) {
				result := amountToDecimal(tt.amount, tt.currency)
				assert.Equal(t, tt.expected, result.String())
			})
		}
	})

	t.Run("three-decimal currency divides by 1000", func(t *testing.T) {
		tests := []struct {
			amount   int64
			currency string
			expected string
		}{
			{3590, "BHD", "3.59"},
			{10000, "JOD", "10"},
			{500, "KWD", "0.5"},
			{1, "OMR", "0.001"},
			{-2500, "TND", "-2.5"},
		}

		for _, tt := range tests {
			t.Run(tt.currency+"_"+tt.expected, func(t *testing.T) {
				result := amountToDecimal(tt.amount, tt.currency)
				assert.Equal(t, tt.expected, result.String())
			})
		}
	})

	t.Run("case insensitive currency check", func(t *testing.T) {
		result := amountToDecimal(1000, "jpy")
		assert.Equal(t, "1000", result.String())

		result = amountToDecimal(3590, "bhd")
		assert.Equal(t, "3.59", result.String())
	})
}
