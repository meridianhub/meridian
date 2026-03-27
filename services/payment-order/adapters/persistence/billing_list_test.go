package persistence

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingRepository_ListBillingRuns_Empty(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	page, err := repo.ListBillingRuns(ctx, BillingRunFilter{}, 10, "")
	require.NoError(t, err)
	assert.Empty(t, page.BillingRuns)
	assert.Equal(t, int64(0), page.TotalCount)
	assert.Empty(t, page.NextCursor)
}

func TestBillingRepository_ListBillingRuns_Pagination(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)

	// Create 5 billing runs with staggered times.
	for i := range 5 {
		start := time.Date(2026, time.Month(i+1), 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2026, time.Month(i+2), 1, 0, 0, 0, 0, time.UTC)
		run, err := domain.NewBillingRun("tenant-list-test", start, end)
		require.NoError(t, err)
		require.NoError(t, repo.CreateBillingRun(ctx, run))
	}

	// First page of 2.
	page1, err := repo.ListBillingRuns(ctx, BillingRunFilter{}, 2, "")
	require.NoError(t, err)
	assert.Len(t, page1.BillingRuns, 2)
	assert.Equal(t, int64(5), page1.TotalCount)
	assert.NotEmpty(t, page1.NextCursor)

	// Second page.
	page2, err := repo.ListBillingRuns(ctx, BillingRunFilter{}, 2, page1.NextCursor)
	require.NoError(t, err)
	assert.Len(t, page2.BillingRuns, 2)
	assert.NotEmpty(t, page2.NextCursor)

	// Third page (last item).
	page3, err := repo.ListBillingRuns(ctx, BillingRunFilter{}, 2, page2.NextCursor)
	require.NoError(t, err)
	assert.Len(t, page3.BillingRuns, 1)
	assert.Empty(t, page3.NextCursor)

	// All IDs should be unique across pages.
	allIDs := make(map[uuid.UUID]bool)
	for _, r := range page1.BillingRuns {
		allIDs[r.ID] = true
	}
	for _, r := range page2.BillingRuns {
		allIDs[r.ID] = true
	}
	for _, r := range page3.BillingRuns {
		allIDs[r.ID] = true
	}
	assert.Len(t, allIDs, 5)
}

func TestBillingRepository_ListBillingRuns_StatusFilter(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)

	// Create one INITIATED and one COMPLETED run.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	run1, err := domain.NewBillingRun("tenant-filter", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run1))

	start2 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	run2, err := domain.NewBillingRun("tenant-filter", start2, end2)
	require.NoError(t, err)
	require.NoError(t, run2.StartProcessing())
	require.NoError(t, run2.Complete())
	require.NoError(t, repo.CreateBillingRun(ctx, run2))

	// Filter by COMPLETED only.
	page, err := repo.ListBillingRuns(ctx, BillingRunFilter{
		Statuses: []string{string(domain.BillingRunStatusCompleted)},
	}, 10, "")
	require.NoError(t, err)
	assert.Len(t, page.BillingRuns, 1)
	assert.Equal(t, domain.BillingRunStatusCompleted, page.BillingRuns[0].Status)
}

func TestBillingRepository_ListBillingRuns_InvalidCursor(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	_, err := repo.ListBillingRuns(ctx, BillingRunFilter{}, 10, "not-valid-base64!!!")
	assert.ErrorIs(t, err, ErrInvalidBillingCursor)
}

func TestBillingRepository_ListInvoices_WithFilters(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	run, err := domain.NewBillingRun("tenant-inv-list", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	// Create invoices for different parties.
	for i := range 3 {
		inv := &domain.Invoice{
			ID:            uuid.New(),
			BillingRunID:  run.ID,
			PartyID:       "party-A",
			AccountID:     "acc-1",
			InvoiceNumber: "INV-LIST-A" + string(rune('0'+i)),
			PeriodStart:   start,
			PeriodEnd:     end,
			LineItems:     []domain.InvoiceLineItem{{Description: "Fee", Quantity: decimal.NewFromInt(1), UnitPriceCents: 100, TotalCents: 100}},
			SubtotalCents: 100,
			Currency:      "GBP",
			Status:        domain.InvoiceStatusDraft,
			CreatedAt:     time.Now().UTC(),
		}
		require.NoError(t, repo.CreateInvoice(ctx, inv))
	}

	inv := &domain.Invoice{
		ID:            uuid.New(),
		BillingRunID:  run.ID,
		PartyID:       "party-B",
		AccountID:     "acc-2",
		InvoiceNumber: "INV-LIST-B0",
		PeriodStart:   start,
		PeriodEnd:     end,
		LineItems:     []domain.InvoiceLineItem{{Description: "Fee", Quantity: decimal.NewFromInt(1), UnitPriceCents: 200, TotalCents: 200}},
		SubtotalCents: 200,
		Currency:      "GBP",
		Status:        domain.InvoiceStatusDraft,
		CreatedAt:     time.Now().UTC(),
	}
	require.NoError(t, repo.CreateInvoice(ctx, inv))

	// List all.
	page, err := repo.ListInvoices(ctx, InvoiceFilter{}, 10, "")
	require.NoError(t, err)
	assert.Equal(t, int64(4), page.TotalCount)

	// Filter by party.
	page, err = repo.ListInvoices(ctx, InvoiceFilter{PartyID: "party-A"}, 10, "")
	require.NoError(t, err)
	assert.Equal(t, int64(3), page.TotalCount)

	// Filter by billing run.
	page, err = repo.ListInvoices(ctx, InvoiceFilter{BillingRunID: run.ID.String()}, 10, "")
	require.NoError(t, err)
	assert.Equal(t, int64(4), page.TotalCount)
}

func TestBillingRepository_CountInvoicesByBillingRun(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)

	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	run, err := domain.NewBillingRun("tenant-count", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	for i := range 3 {
		inv := &domain.Invoice{
			ID:            uuid.New(),
			BillingRunID:  run.ID,
			PartyID:       "party-1",
			AccountID:     "acc-1",
			InvoiceNumber: "INV-CNT-" + string(rune('0'+i)),
			PeriodStart:   start,
			PeriodEnd:     end,
			LineItems:     []domain.InvoiceLineItem{{Description: "Fee", Quantity: decimal.NewFromInt(1), UnitPriceCents: 100, TotalCents: 100}},
			SubtotalCents: 100,
			Currency:      "GBP",
			Status:        domain.InvoiceStatusDraft,
			CreatedAt:     time.Now().UTC(),
		}
		require.NoError(t, repo.CreateInvoice(ctx, inv))
	}

	count, err := repo.CountInvoicesByBillingRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)

	// Non-existent run.
	count, err = repo.CountInvoicesByBillingRun(ctx, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestBillingRepository_SumInvoiceTotalsByBillingRun(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	run, err := domain.NewBillingRun("tenant-sum", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	amounts := []int64{1000, 2000, 3000}
	for i, amt := range amounts {
		inv := &domain.Invoice{
			ID:            uuid.New(),
			BillingRunID:  run.ID,
			PartyID:       "party-1",
			AccountID:     "acc-1",
			InvoiceNumber: "INV-SUM-" + string(rune('0'+i)),
			PeriodStart:   start,
			PeriodEnd:     end,
			LineItems:     []domain.InvoiceLineItem{{Description: "Fee", Quantity: decimal.NewFromInt(1), UnitPriceCents: amt, TotalCents: amt}},
			SubtotalCents: amt,
			Currency:      "GBP",
			Status:        domain.InvoiceStatusDraft,
			CreatedAt:     time.Now().UTC(),
		}
		require.NoError(t, repo.CreateInvoice(ctx, inv))
	}

	sum, err := repo.SumInvoiceTotalsByBillingRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(6000), sum)

	// Non-existent run.
	sum, err = repo.SumInvoiceTotalsByBillingRun(ctx, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, int64(0), sum)
}
