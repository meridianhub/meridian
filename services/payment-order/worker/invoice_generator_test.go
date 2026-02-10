package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock position-keeping client ---

type mockPositionClient struct {
	accounts       []AccountInfo
	entries        map[string][]PositionEntry // accountID -> entries
	balances       map[string]int64           // accountID -> balanceCents
	currencies     map[string]string          // accountID -> currency
	accountsErr    error
	entriesErr     error
	entriesErrAcct string // if set, only this account returns entriesErr
	balanceErr     error
}

func (m *mockPositionClient) GetAccountBalance(_ context.Context, accountID string) (int64, string, error) {
	if m.balanceErr != nil {
		return 0, "", m.balanceErr
	}
	return m.balances[accountID], m.currencies[accountID], nil
}

func (m *mockPositionClient) ListAccountsForTenant(_ context.Context, _ string) ([]AccountInfo, error) {
	if m.accountsErr != nil {
		return nil, m.accountsErr
	}
	return m.accounts, nil
}

func (m *mockPositionClient) GetPositionLogEntries(_ context.Context, accountID string, _, _ time.Time) ([]PositionEntry, error) {
	if m.entriesErr != nil && (m.entriesErrAcct == "" || m.entriesErrAcct == accountID) {
		return nil, m.entriesErr
	}
	return m.entries[accountID], nil
}

// --- Tests ---

func testInvoiceMetrics(t *testing.T) *BillingMetrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return NewBillingMetricsWithRegistry(reg)
}

func testInvoiceLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestGenerateInvoices(t *testing.T) {
	ctx := context.Background()

	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	t.Run("creates invoices grouped by party", func(t *testing.T) {
		repo := newMockBillingRepo()
		posClient := &mockPositionClient{
			accounts: []AccountInfo{
				{AccountID: "acct-1", PartyID: "party-a", Currency: "USD"},
				{AccountID: "acct-2", PartyID: "party-a", Currency: "USD"},
				{AccountID: "acct-3", PartyID: "party-b", Currency: "USD"},
			},
			entries: map[string][]PositionEntry{
				"acct-1": {
					{Description: "Service fee", AmountCents: 5000, Quantity: decimal.NewFromInt(1), UnitPriceCents: 5000},
				},
				"acct-2": {
					{Description: "Usage charge", AmountCents: 3000, Quantity: decimal.NewFromInt(10), UnitPriceCents: 300},
				},
				"acct-3": {
					{Description: "Platform fee", AmountCents: 2500, Quantity: decimal.NewFromInt(1), UnitPriceCents: 2500},
				},
			},
		}

		billingRun := createTestBillingRun(t, "tenant-1", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		gen := NewInvoiceGenerator(posClient, repo, testInvoiceMetrics(t), testInvoiceLogger())
		invoices, err := gen.GenerateInvoices(ctx, billingRun)
		require.NoError(t, err)

		assert.Len(t, invoices, 2, "should create one invoice per party")

		// Find invoice for each party
		var partyAInvoice, partyBInvoice *domain.Invoice
		for _, inv := range invoices {
			switch inv.PartyID {
			case "party-a":
				partyAInvoice = inv
			case "party-b":
				partyBInvoice = inv
			}
		}

		require.NotNil(t, partyAInvoice)
		assert.Len(t, partyAInvoice.LineItems, 2, "party-a has 2 accounts with entries")
		assert.Equal(t, int64(8000), partyAInvoice.SubtotalCents) // 5000 + 3000

		require.NotNil(t, partyBInvoice)
		assert.Len(t, partyBInvoice.LineItems, 1)
		assert.Equal(t, int64(2500), partyBInvoice.SubtotalCents)
	})

	t.Run("skips accounts with zero or negative entries", func(t *testing.T) {
		repo := newMockBillingRepo()
		posClient := &mockPositionClient{
			accounts: []AccountInfo{
				{AccountID: "acct-1", PartyID: "party-a", Currency: "GBP"},
			},
			entries: map[string][]PositionEntry{
				"acct-1": {
					{Description: "Credit refund", AmountCents: -1000, Quantity: decimal.NewFromInt(1), UnitPriceCents: -1000},
					{Description: "Service fee", AmountCents: 5000, Quantity: decimal.NewFromInt(1), UnitPriceCents: 5000},
					{Description: "Zero entry", AmountCents: 0, Quantity: decimal.NewFromInt(0), UnitPriceCents: 0},
				},
			},
		}

		billingRun := createTestBillingRun(t, "tenant-1", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		gen := NewInvoiceGenerator(posClient, repo, testInvoiceMetrics(t), testInvoiceLogger())
		invoices, err := gen.GenerateInvoices(ctx, billingRun)
		require.NoError(t, err)

		require.Len(t, invoices, 1)
		assert.Len(t, invoices[0].LineItems, 1, "should only include positive amount entries")
		assert.Equal(t, int64(5000), invoices[0].SubtotalCents)
		assert.Equal(t, "GBP", invoices[0].Currency)
	})

	t.Run("returns nil when no accounts exist", func(t *testing.T) {
		repo := newMockBillingRepo()
		posClient := &mockPositionClient{
			accounts: nil,
		}

		billingRun := createTestBillingRun(t, "tenant-1", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		gen := NewInvoiceGenerator(posClient, repo, testInvoiceMetrics(t), testInvoiceLogger())
		invoices, err := gen.GenerateInvoices(ctx, billingRun)
		require.NoError(t, err)
		assert.Nil(t, invoices)
	})

	t.Run("returns error when listing accounts fails", func(t *testing.T) {
		repo := newMockBillingRepo()
		posClient := &mockPositionClient{
			accountsErr: errors.New("position-keeping unavailable"),
		}

		billingRun := createTestBillingRun(t, "tenant-1", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		gen := NewInvoiceGenerator(posClient, repo, testInvoiceMetrics(t), testInvoiceLogger())
		_, err := gen.GenerateInvoices(ctx, billingRun)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "list accounts for tenant")
	})

	t.Run("surfaces position entry errors as partial failure", func(t *testing.T) {
		repo := newMockBillingRepo()
		posClient := &mockPositionClient{
			accounts: []AccountInfo{
				{AccountID: "acct-good", PartyID: "party-good", Currency: "USD"},
				{AccountID: "acct-bad", PartyID: "party-bad", Currency: "USD"},
			},
			entries: map[string][]PositionEntry{
				"acct-good": {
					{Description: "Fee", AmountCents: 1000, Quantity: decimal.NewFromInt(1), UnitPriceCents: 1000},
				},
			},
			entriesErr:     errors.New("position-keeping timeout"),
			entriesErrAcct: "acct-bad", // only acct-bad fails
		}

		billingRun := createTestBillingRun(t, "tenant-1", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		gen := NewInvoiceGenerator(posClient, repo, testInvoiceMetrics(t), testInvoiceLogger())
		invoices, err := gen.GenerateInvoices(ctx, billingRun)

		// Should return error indicating partial failure
		assert.ErrorIs(t, err, ErrPartialInvoiceGeneration)

		// But should still return successful invoices
		assert.Len(t, invoices, 1)
		assert.Equal(t, "party-good", invoices[0].PartyID)
	})

	t.Run("rejects mixed currencies within a party", func(t *testing.T) {
		repo := newMockBillingRepo()
		posClient := &mockPositionClient{
			accounts: []AccountInfo{
				{AccountID: "acct-usd", PartyID: "party-mixed", Currency: "USD"},
				{AccountID: "acct-gbp", PartyID: "party-mixed", Currency: "GBP"},
			},
			entries: map[string][]PositionEntry{
				"acct-usd": {
					{Description: "Fee", AmountCents: 1000, Quantity: decimal.NewFromInt(1), UnitPriceCents: 1000},
				},
				"acct-gbp": {
					{Description: "Fee", AmountCents: 2000, Quantity: decimal.NewFromInt(1), UnitPriceCents: 2000},
				},
			},
		}

		billingRun := createTestBillingRun(t, "tenant-1", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		gen := NewInvoiceGenerator(posClient, repo, testInvoiceMetrics(t), testInvoiceLogger())
		invoices, err := gen.GenerateInvoices(ctx, billingRun)

		// Should return error for the mixed-currency party
		assert.ErrorIs(t, err, ErrPartialInvoiceGeneration)
		// No invoices created for the mixed-currency party
		assert.Empty(t, invoices)
	})

	t.Run("preserves valuation analysis in line items", func(t *testing.T) {
		repo := newMockBillingRepo()
		valuation := map[string]any{
			"method":     "market_price",
			"confidence": 0.95,
			"source":     "exchange_feed",
		}
		posClient := &mockPositionClient{
			accounts: []AccountInfo{
				{AccountID: "acct-1", PartyID: "party-a", Currency: "USD"},
			},
			entries: map[string][]PositionEntry{
				"acct-1": {
					{
						Description:       "Energy charge",
						AmountCents:       15000,
						Quantity:          decimal.NewFromFloat(100.5),
						UnitPriceCents:    149,
						ValuationAnalysis: valuation,
					},
				},
			},
		}

		billingRun := createTestBillingRun(t, "tenant-1", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		gen := NewInvoiceGenerator(posClient, repo, testInvoiceMetrics(t), testInvoiceLogger())
		invoices, err := gen.GenerateInvoices(ctx, billingRun)
		require.NoError(t, err)

		require.Len(t, invoices, 1)
		require.Len(t, invoices[0].LineItems, 1)

		li := invoices[0].LineItems[0]
		assert.Equal(t, "Energy charge", li.Description)
		assert.True(t, decimal.NewFromFloat(100.5).Equal(li.Quantity))
		assert.Equal(t, int64(149), li.UnitPriceCents)
		assert.Equal(t, int64(15000), li.TotalCents)
		assert.Equal(t, "market_price", li.ValuationAnalysis["method"])
		assert.Equal(t, 0.95, li.ValuationAnalysis["confidence"])
	})
}

func TestFormatInvoiceNumber(t *testing.T) {
	t.Run("includes tenant prefix and zero-padded sequence", func(t *testing.T) {
		periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, "INV-tenant-1-2026-02-0001", formatInvoiceNumber("tenant-1", periodEnd, 1))
		assert.Equal(t, "INV-tenant-1-2026-02-0042", formatInvoiceNumber("tenant-1", periodEnd, 42))
	})

	t.Run("truncates long tenant IDs to 8 chars", func(t *testing.T) {
		periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, "INV-very-lon-2026-02-0001", formatInvoiceNumber("very-long-tenant-id", periodEnd, 1))
	})

	t.Run("prevents cross-tenant collisions", func(t *testing.T) {
		periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		num1 := formatInvoiceNumber("tenant-a", periodEnd, 1)
		num2 := formatInvoiceNumber("tenant-b", periodEnd, 1)
		assert.NotEqual(t, num1, num2, "different tenants should produce different invoice numbers")
	})

	t.Run("handles large sequence numbers", func(t *testing.T) {
		periodEnd := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, "INV-tenant-1-2026-12-10000", formatInvoiceNumber("tenant-1", periodEnd, 10000))
	})
}

func TestPadSequence(t *testing.T) {
	assert.Equal(t, "0001", padSequence(1))
	assert.Equal(t, "0010", padSequence(10))
	assert.Equal(t, "0100", padSequence(100))
	assert.Equal(t, "1000", padSequence(1000))
	assert.Equal(t, "12345", padSequence(12345))
}

func createTestBillingRun(t *testing.T, tenantID string, start, end time.Time) *domain.BillingRun {
	t.Helper()
	run, err := domain.NewBillingRun(tenantID, start, end)
	require.NoError(t, err)
	_ = run.StartProcessing() // move to PROCESSING for invoice generation
	return run
}

// Verify mock implements interface.
var _ PositionKeepingClient = (*mockPositionClient)(nil)

// Ensure unused import of uuid doesn't cause issues.
var _ = uuid.New
