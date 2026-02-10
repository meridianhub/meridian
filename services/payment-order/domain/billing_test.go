package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBillingRun(t *testing.T) {
	now := time.Now()
	start := now.Add(-24 * time.Hour)
	end := now

	t.Run("creates valid billing run", func(t *testing.T) {
		run, err := NewBillingRun("tenant-1", start, end)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, run.ID)
		assert.Equal(t, "tenant-1", run.TenantID)
		assert.Equal(t, start, run.CycleStart)
		assert.Equal(t, end, run.CycleEnd)
		assert.Equal(t, BillingRunStatusInitiated, run.Status)
		assert.Zero(t, run.DunningLevel)
		assert.False(t, run.CreatedAt.IsZero())
	})

	t.Run("rejects empty tenant ID", func(t *testing.T) {
		_, err := NewBillingRun("", start, end)
		assert.ErrorIs(t, err, ErrMissingTenantID)
	})

	t.Run("rejects invalid period where end is before start", func(t *testing.T) {
		_, err := NewBillingRun("tenant-1", end, start)
		assert.ErrorIs(t, err, ErrInvalidBillingPeriod)
	})

	t.Run("rejects equal start and end", func(t *testing.T) {
		_, err := NewBillingRun("tenant-1", start, start)
		assert.ErrorIs(t, err, ErrInvalidBillingPeriod)
	})
}

func TestBillingRunStatusTransitions(t *testing.T) {
	now := time.Now()
	start := now.Add(-24 * time.Hour)

	newRun := func() *BillingRun {
		run, err := NewBillingRun("tenant-1", start, now)
		require.NoError(t, err)
		return run
	}

	t.Run("happy path: INITIATED -> PROCESSING -> COMPLETED", func(t *testing.T) {
		run := newRun()
		assert.Equal(t, BillingRunStatusInitiated, run.Status)

		require.NoError(t, run.StartProcessing())
		assert.Equal(t, BillingRunStatusProcessing, run.Status)

		require.NoError(t, run.Complete())
		assert.Equal(t, BillingRunStatusCompleted, run.Status)
		assert.True(t, run.IsTerminal())
	})

	t.Run("cannot start processing from non-INITIATED state", func(t *testing.T) {
		run := newRun()
		require.NoError(t, run.StartProcessing())

		err := run.StartProcessing()
		assert.ErrorIs(t, err, ErrInvalidBillingRunTransition)
	})

	t.Run("cannot complete from non-PROCESSING state", func(t *testing.T) {
		run := newRun()
		err := run.Complete()
		assert.ErrorIs(t, err, ErrInvalidBillingRunTransition)
	})

	t.Run("fail from INITIATED", func(t *testing.T) {
		run := newRun()
		require.NoError(t, run.Fail("some error"))
		assert.Equal(t, BillingRunStatusFailed, run.Status)
		assert.Equal(t, "some error", run.FailureReason)
		assert.True(t, run.IsTerminal())
	})

	t.Run("fail from PROCESSING", func(t *testing.T) {
		run := newRun()
		require.NoError(t, run.StartProcessing())
		require.NoError(t, run.Fail("processing error"))
		assert.Equal(t, BillingRunStatusFailed, run.Status)
	})

	t.Run("fail is idempotent when already failed", func(t *testing.T) {
		run := newRun()
		require.NoError(t, run.Fail("first error"))
		require.NoError(t, run.Fail("second error"))
		assert.Equal(t, "first error", run.FailureReason)
	})

	t.Run("cannot fail from COMPLETED", func(t *testing.T) {
		run := newRun()
		require.NoError(t, run.StartProcessing())
		require.NoError(t, run.Complete())

		err := run.Fail("too late")
		assert.ErrorIs(t, err, ErrBillingRunTerminal)
	})
}

func TestBillingRunIdempotencyKey(t *testing.T) {
	tenantID := "tenant-123"
	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	t.Run("deterministic for same inputs", func(t *testing.T) {
		key1 := BillingRunIdempotencyKey(tenantID, start, end)
		key2 := BillingRunIdempotencyKey(tenantID, start, end)
		assert.Equal(t, key1, key2)
	})

	t.Run("uses RFC3339 format", func(t *testing.T) {
		key := BillingRunIdempotencyKey(tenantID, start, end)
		assert.Equal(t, "billing_run_tenant-123_2026-02-01T00:00:00Z_2026-03-01T00:00:00Z", key)
	})

	t.Run("different tenants produce different keys", func(t *testing.T) {
		key1 := BillingRunIdempotencyKey("tenant-a", start, end)
		key2 := BillingRunIdempotencyKey("tenant-b", start, end)
		assert.NotEqual(t, key1, key2)
	})

	t.Run("different periods produce different keys", func(t *testing.T) {
		key1 := BillingRunIdempotencyKey(tenantID, start, end)
		key2 := BillingRunIdempotencyKey(tenantID, start, end.Add(time.Hour))
		assert.NotEqual(t, key1, key2)
	})

	t.Run("normalizes to UTC", func(t *testing.T) {
		loc := time.FixedZone("UTC+5", 5*3600)
		localStart := start.In(loc)
		localEnd := end.In(loc)

		keyUTC := BillingRunIdempotencyKey(tenantID, start, end)
		keyLocal := BillingRunIdempotencyKey(tenantID, localStart, localEnd)
		assert.Equal(t, keyUTC, keyLocal)
	})
}

func TestNewInvoice(t *testing.T) {
	billingRunID := uuid.New()
	start := time.Now().Add(-24 * time.Hour)
	end := time.Now()
	lineItems := []InvoiceLineItem{
		{
			Description:    "Energy usage 100 kWh",
			Quantity:       decimal.NewFromInt(100),
			UnitPriceCents: 15,
			TotalCents:     1500,
		},
		{
			Description:    "Service fee",
			Quantity:       decimal.NewFromInt(1),
			UnitPriceCents: 500,
			TotalCents:     500,
		},
	}

	t.Run("creates valid invoice with calculated subtotal", func(t *testing.T) {
		inv, err := NewInvoice(billingRunID, "party-1", "acc-1", "INV-2026-02-0001", start, end, lineItems, "GBP")
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, inv.ID)
		assert.Equal(t, billingRunID, inv.BillingRunID)
		assert.Equal(t, "party-1", inv.PartyID)
		assert.Equal(t, "acc-1", inv.AccountID)
		assert.Equal(t, "INV-2026-02-0001", inv.InvoiceNumber)
		assert.Equal(t, int64(2000), inv.SubtotalCents)
		assert.Equal(t, "GBP", inv.Currency)
		assert.Equal(t, InvoiceStatusDraft, inv.Status)
		assert.Nil(t, inv.PaymentOrderID)
		assert.Len(t, inv.LineItems, 2)
	})

	t.Run("rejects nil billing run ID", func(t *testing.T) {
		_, err := NewInvoice(uuid.Nil, "party-1", "acc-1", "INV-001", start, end, lineItems, "GBP")
		assert.ErrorIs(t, err, ErrMissingBillingRunID)
	})

	t.Run("rejects empty party ID", func(t *testing.T) {
		_, err := NewInvoice(billingRunID, "", "acc-1", "INV-001", start, end, lineItems, "GBP")
		assert.ErrorIs(t, err, ErrMissingPartyID)
	})

	t.Run("rejects empty account ID", func(t *testing.T) {
		_, err := NewInvoice(billingRunID, "party-1", "", "INV-001", start, end, lineItems, "GBP")
		assert.ErrorIs(t, err, ErrMissingAccountID)
	})

	t.Run("rejects empty invoice number", func(t *testing.T) {
		_, err := NewInvoice(billingRunID, "party-1", "acc-1", "", start, end, lineItems, "GBP")
		assert.ErrorIs(t, err, ErrMissingInvoiceNumber)
	})

	t.Run("rejects empty line items", func(t *testing.T) {
		_, err := NewInvoice(billingRunID, "party-1", "acc-1", "INV-001", start, end, nil, "GBP")
		assert.ErrorIs(t, err, ErrEmptyLineItems)
	})
}

func TestInvoiceStatusTransitions(t *testing.T) {
	billingRunID := uuid.New()
	start := time.Now().Add(-24 * time.Hour)
	end := time.Now()
	lineItems := []InvoiceLineItem{
		{Description: "Test item", Quantity: decimal.NewFromInt(1), UnitPriceCents: 100, TotalCents: 100},
	}

	newInvoice := func() *Invoice {
		inv, err := NewInvoice(billingRunID, "party-1", "acc-1", "INV-001", start, end, lineItems, "GBP")
		require.NoError(t, err)
		return inv
	}

	t.Run("happy path: DRAFT -> ISSUED -> PAID", func(t *testing.T) {
		inv := newInvoice()
		assert.Equal(t, InvoiceStatusDraft, inv.Status)

		require.NoError(t, inv.Issue())
		assert.Equal(t, InvoiceStatusIssued, inv.Status)

		poID := uuid.New()
		require.NoError(t, inv.MarkPaid(poID))
		assert.Equal(t, InvoiceStatusPaid, inv.Status)
		assert.Equal(t, &poID, inv.PaymentOrderID)
		assert.True(t, inv.IsTerminal())
	})

	t.Run("ISSUED -> OVERDUE -> PAID", func(t *testing.T) {
		inv := newInvoice()
		require.NoError(t, inv.Issue())
		require.NoError(t, inv.MarkOverdue())
		assert.Equal(t, InvoiceStatusOverdue, inv.Status)
		assert.False(t, inv.IsTerminal())

		poID := uuid.New()
		require.NoError(t, inv.MarkPaid(poID))
		assert.Equal(t, InvoiceStatusPaid, inv.Status)
	})

	t.Run("void from DRAFT", func(t *testing.T) {
		inv := newInvoice()
		require.NoError(t, inv.Void())
		assert.Equal(t, InvoiceStatusVoid, inv.Status)
		assert.True(t, inv.IsTerminal())
	})

	t.Run("void from ISSUED", func(t *testing.T) {
		inv := newInvoice()
		require.NoError(t, inv.Issue())
		require.NoError(t, inv.Void())
		assert.Equal(t, InvoiceStatusVoid, inv.Status)
	})

	t.Run("cannot void from PAID", func(t *testing.T) {
		inv := newInvoice()
		require.NoError(t, inv.Issue())
		require.NoError(t, inv.MarkPaid(uuid.New()))

		err := inv.Void()
		assert.ErrorIs(t, err, ErrInvalidInvoiceTransition)
	})

	t.Run("cannot issue from PAID", func(t *testing.T) {
		inv := newInvoice()
		require.NoError(t, inv.Issue())
		require.NoError(t, inv.MarkPaid(uuid.New()))

		err := inv.Issue()
		assert.ErrorIs(t, err, ErrInvalidInvoiceTransition)
	})

	t.Run("cannot mark overdue from DRAFT", func(t *testing.T) {
		inv := newInvoice()
		err := inv.MarkOverdue()
		assert.ErrorIs(t, err, ErrInvalidInvoiceTransition)
	})

	t.Run("cannot mark paid from DRAFT", func(t *testing.T) {
		inv := newInvoice()
		err := inv.MarkPaid(uuid.New())
		assert.ErrorIs(t, err, ErrInvalidInvoiceTransition)
	})
}

func TestInvoiceLineItemValuationAnalysis(t *testing.T) {
	t.Run("line item can carry valuation analysis metadata", func(t *testing.T) {
		item := InvoiceLineItem{
			Description:    "Energy consumed",
			Quantity:       decimal.NewFromFloat(42.5),
			UnitPriceCents: 10,
			TotalCents:     425,
			ValuationAnalysis: map[string]any{
				"source":      "position-keeping",
				"quality":     "ACTUAL",
				"meter_id":    "MTR-001",
				"reading_kwh": 42.5,
			},
		}

		assert.Equal(t, "position-keeping", item.ValuationAnalysis["source"])
		assert.Equal(t, "ACTUAL", item.ValuationAnalysis["quality"])
	})
}
