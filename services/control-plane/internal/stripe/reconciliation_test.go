package stripe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStripeSource struct {
	charges []ChargeRecord
	err     error
}

func (m *mockStripeSource) ListCharges(_ context.Context, _, _ time.Time) ([]ChargeRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.charges, nil
}

type mockLedgerSource struct {
	entries []LedgerRecord
	err     error
}

func (m *mockLedgerSource) ListEntriesByExternalRef(_ context.Context, _ []string) ([]LedgerRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.entries, nil
}

func TestReconciliation_PerfectMatch(t *testing.T) {
	svc, err := NewReconciliationService(
		&mockStripeSource{
			charges: []ChargeRecord{
				{ChargeID: "ch_1", AmountCents: 10000, Currency: "gbp"},
				{ChargeID: "ch_2", AmountCents: 5000, Currency: "gbp"},
			},
		},
		&mockLedgerSource{
			entries: []LedgerRecord{
				{LogID: "log-1", ExternalReferenceID: "ch_1", AmountCents: 10000, Currency: "gbp"},
				{LogID: "log-2", ExternalReferenceID: "ch_2", AmountCents: 5000, Currency: "gbp"},
			},
		},
		nil,
	)
	require.NoError(t, err)

	report, err := svc.RunDailyReconciliation(context.Background(), time.Now())
	require.NoError(t, err)

	assert.Equal(t, 2, report.StripeChargeCount)
	assert.Equal(t, 2, report.LedgerEntryCount)
	assert.Equal(t, int64(15000), report.StripeTotalCents)
	assert.Equal(t, int64(15000), report.LedgerTotalCents)
	assert.Equal(t, int64(0), report.VarianceCents)
	assert.Empty(t, report.MissingInLedger)
	assert.Empty(t, report.MissingInStripe)
	assert.False(t, report.VarianceExceedsThreshold)
}

func TestReconciliation_MissingInLedger(t *testing.T) {
	svc, err := NewReconciliationService(
		&mockStripeSource{
			charges: []ChargeRecord{
				{ChargeID: "ch_1", AmountCents: 10000, Currency: "gbp"},
				{ChargeID: "ch_2", AmountCents: 5000, Currency: "gbp"},
			},
		},
		&mockLedgerSource{
			entries: []LedgerRecord{
				{LogID: "log-1", ExternalReferenceID: "ch_1", AmountCents: 10000, Currency: "gbp"},
				// ch_2 missing from ledger
			},
		},
		nil,
	)
	require.NoError(t, err)

	report, err := svc.RunDailyReconciliation(context.Background(), time.Now())
	require.NoError(t, err)

	assert.Equal(t, 2, report.StripeChargeCount)
	assert.Equal(t, 1, report.LedgerEntryCount)
	assert.Equal(t, int64(5000), report.VarianceCents)
	assert.Contains(t, report.MissingInLedger, "ch_2")
}

func TestReconciliation_LargeVarianceAlert(t *testing.T) {
	// Create a variance > 10 GBP (1000 pence)
	svc, err := NewReconciliationService(
		&mockStripeSource{
			charges: []ChargeRecord{
				{ChargeID: "ch_1", AmountCents: 200000, Currency: "gbp"}, // 2000 GBP
			},
		},
		&mockLedgerSource{
			entries: []LedgerRecord{
				{LogID: "log-1", ExternalReferenceID: "ch_1", AmountCents: 198000, Currency: "gbp"}, // 1980 GBP
			},
		},
		nil,
	)
	require.NoError(t, err)

	report, err := svc.RunDailyReconciliation(context.Background(), time.Now())
	require.NoError(t, err)

	assert.Equal(t, int64(2000), report.VarianceCents) // 20 GBP
	assert.True(t, report.VarianceExceedsThreshold)
	assert.NotEmpty(t, report.AlertMessage)
	assert.Contains(t, report.AlertMessage, "absolute variance")
}

func TestReconciliation_PercentageVarianceAlert(t *testing.T) {
	// Create variance > 1% of daily volume
	// 100 GBP total, 2 GBP variance = 2%
	svc, err := NewReconciliationService(
		&mockStripeSource{
			charges: []ChargeRecord{
				{ChargeID: "ch_1", AmountCents: 10000, Currency: "gbp"}, // 100 GBP
			},
		},
		&mockLedgerSource{
			entries: []LedgerRecord{
				{LogID: "log-1", ExternalReferenceID: "ch_1", AmountCents: 9800, Currency: "gbp"}, // 98 GBP
			},
		},
		nil,
	)
	require.NoError(t, err)

	report, err := svc.RunDailyReconciliation(context.Background(), time.Now())
	require.NoError(t, err)

	assert.Equal(t, int64(200), report.VarianceCents) // 2 GBP
	assert.True(t, report.VarianceExceedsThreshold)
	assert.Contains(t, report.AlertMessage, "relative variance")
}

func TestReconciliation_NoCharges(t *testing.T) {
	svc, err := NewReconciliationService(
		&mockStripeSource{charges: nil},
		&mockLedgerSource{entries: nil},
		nil,
	)
	require.NoError(t, err)

	report, err := svc.RunDailyReconciliation(context.Background(), time.Now())
	require.NoError(t, err)

	assert.Equal(t, 0, report.StripeChargeCount)
	assert.Equal(t, 0, report.LedgerEntryCount)
	assert.Equal(t, int64(0), report.VarianceCents)
	assert.False(t, report.VarianceExceedsThreshold)
}

func TestNewReconciliationService_Validation(t *testing.T) {
	t.Run("nil stripe source", func(t *testing.T) {
		_, err := NewReconciliationService(nil, &mockLedgerSource{}, nil)
		assert.ErrorIs(t, err, ErrNilStripeSource)
	})

	t.Run("nil ledger source", func(t *testing.T) {
		_, err := NewReconciliationService(&mockStripeSource{}, nil, nil)
		assert.ErrorIs(t, err, ErrNilLedgerSource)
	})
}

func TestReconciliation_MissingInStripe(t *testing.T) {
	// Ledger has an extra entry not in Stripe
	svc, err := NewReconciliationService(
		&mockStripeSource{
			charges: []ChargeRecord{
				{ChargeID: "ch_1", AmountCents: 10000, Currency: "gbp"},
			},
		},
		&mockLedgerSource{
			entries: []LedgerRecord{
				{LogID: "log-1", ExternalReferenceID: "ch_1", AmountCents: 10000, Currency: "gbp"},
				{LogID: "log-2", ExternalReferenceID: "ch_orphan", AmountCents: 3000, Currency: "gbp"},
			},
		},
		nil,
	)
	require.NoError(t, err)

	report, err := svc.RunDailyReconciliation(context.Background(), time.Now())
	require.NoError(t, err)

	assert.Equal(t, 1, report.StripeChargeCount)
	assert.Equal(t, 2, report.LedgerEntryCount)
	assert.Contains(t, report.MissingInStripe, "ch_orphan")
	assert.Empty(t, report.MissingInLedger)
}

func TestReconciliation_StripeSourceError(t *testing.T) {
	svc, err := NewReconciliationService(
		&mockStripeSource{err: errors.New("stripe API timeout")},
		&mockLedgerSource{},
		nil,
	)
	require.NoError(t, err)

	_, err = svc.RunDailyReconciliation(context.Background(), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch stripe charges")
}

func TestReconciliation_LedgerSourceError(t *testing.T) {
	svc, err := NewReconciliationService(
		&mockStripeSource{
			charges: []ChargeRecord{
				{ChargeID: "ch_1", AmountCents: 10000, Currency: "gbp"},
			},
		},
		&mockLedgerSource{err: errors.New("db connection refused")},
		nil,
	)
	require.NoError(t, err)

	_, err = svc.RunDailyReconciliation(context.Background(), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch ledger entries")
}

func TestReconciliation_BothThresholdsExceeded(t *testing.T) {
	// Create scenario where both absolute AND percentage thresholds are exceeded
	// 100 GBP total, 25 GBP variance = 25% (exceeds 1%) and 25 GBP (exceeds 10 GBP)
	svc, err := NewReconciliationService(
		&mockStripeSource{
			charges: []ChargeRecord{
				{ChargeID: "ch_1", AmountCents: 10000, Currency: "gbp"},
			},
		},
		&mockLedgerSource{
			entries: []LedgerRecord{
				{LogID: "log-1", ExternalReferenceID: "ch_1", AmountCents: 7500, Currency: "gbp"},
			},
		},
		nil,
	)
	require.NoError(t, err)

	report, err := svc.RunDailyReconciliation(context.Background(), time.Now())
	require.NoError(t, err)

	assert.True(t, report.VarianceExceedsThreshold)
	assert.Contains(t, report.AlertMessage, "absolute variance")
	assert.Contains(t, report.AlertMessage, "relative variance")
}

func TestAbs(t *testing.T) {
	assert.Equal(t, int64(5), abs(5))
	assert.Equal(t, int64(5), abs(-5))
	assert.Equal(t, int64(0), abs(0))
}
