package persistence

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// makeTestInvoice builds a draft invoice attached to the given billing run.
func makeTestInvoice(runID uuid.UUID, number, partyID string, amount int64, start, end time.Time) *domain.Invoice {
	return &domain.Invoice{
		ID:            uuid.New(),
		BillingRunID:  runID,
		PartyID:       partyID,
		AccountID:     "acc-" + partyID,
		InvoiceNumber: number,
		PeriodStart:   start,
		PeriodEnd:     end,
		LineItems: []domain.InvoiceLineItem{
			{Description: "Fee", Quantity: decimal.NewFromInt(1), UnitPriceCents: amount, TotalCents: amount},
		},
		SubtotalCents: amount,
		Currency:      "GBP",
		Status:        domain.InvoiceStatusDraft,
		CreatedAt:     time.Now().UTC(),
	}
}

// TestBillingRepository_CreateInvoice_NumberConflict exercises the duplicate
// invoice-number path that maps a unique-constraint violation to
// ErrInvoiceNumberConflict.
func TestBillingRepository_CreateInvoice_NumberConflict(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	run, err := domain.NewBillingRun("tenant-conflict", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	inv1 := makeTestInvoice(run.ID, "INV-CONFLICT-001", "party-1", 1000, start, end)
	require.NoError(t, repo.CreateInvoice(ctx, inv1))

	// A different invoice (new ID) reusing the same invoice number must collide
	// on the unique idx_invoice_number index.
	inv2 := makeTestInvoice(run.ID, "INV-CONFLICT-001", "party-2", 2000, start, end)
	err = repo.CreateInvoice(ctx, inv2)
	assert.ErrorIs(t, err, ErrInvoiceNumberConflict)
}

// TestBillingRepository_ListInvoices_Empty covers the zero-results early return.
func TestBillingRepository_ListInvoices_Empty(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	page, err := repo.ListInvoices(ctx, InvoiceFilter{}, 10, "")
	require.NoError(t, err)
	assert.Empty(t, page.Invoices)
	assert.Equal(t, int64(0), page.TotalCount)
	assert.Empty(t, page.NextCursor)
}

// TestBillingRepository_ListInvoices_Pagination drives the cursor encode/decode
// and hasMore/NextCursor branches across multiple pages.
func TestBillingRepository_ListInvoices_Pagination(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	run, err := domain.NewBillingRun("tenant-inv-page", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	for i := range 5 {
		inv := makeTestInvoice(run.ID, "INV-PAGE-"+string(rune('A'+i)), "party-1", int64((i+1)*100), start, end)
		require.NoError(t, repo.CreateInvoice(ctx, inv))
	}

	// First page of 2.
	page1, err := repo.ListInvoices(ctx, InvoiceFilter{}, 2, "")
	require.NoError(t, err)
	assert.Len(t, page1.Invoices, 2)
	assert.Equal(t, int64(5), page1.TotalCount)
	require.NotEmpty(t, page1.NextCursor)

	// Second page of 2.
	page2, err := repo.ListInvoices(ctx, InvoiceFilter{}, 2, page1.NextCursor)
	require.NoError(t, err)
	assert.Len(t, page2.Invoices, 2)
	require.NotEmpty(t, page2.NextCursor)

	// Final page (1 item, no further cursor).
	page3, err := repo.ListInvoices(ctx, InvoiceFilter{}, 2, page2.NextCursor)
	require.NoError(t, err)
	assert.Len(t, page3.Invoices, 1)
	assert.Empty(t, page3.NextCursor)

	// IDs unique across pages.
	seen := make(map[uuid.UUID]bool)
	for _, p := range [][]*domain.Invoice{page1.Invoices, page2.Invoices, page3.Invoices} {
		for _, inv := range p {
			seen[inv.ID] = true
		}
	}
	assert.Len(t, seen, 5)
}

// TestBillingRepository_ListInvoices_StatusFilter covers the status branch of
// applyInvoiceFilter through the public List path.
func TestBillingRepository_ListInvoices_StatusFilter(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	run, err := domain.NewBillingRun("tenant-inv-status", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	draft := makeTestInvoice(run.ID, "INV-STATUS-DRAFT", "party-1", 100, start, end)
	require.NoError(t, repo.CreateInvoice(ctx, draft))

	paid := makeTestInvoice(run.ID, "INV-STATUS-PAID", "party-1", 200, start, end)
	require.NoError(t, repo.CreateInvoice(ctx, paid))
	paid.Status = domain.InvoiceStatusPaid
	require.NoError(t, repo.UpdateInvoice(ctx, paid))

	page, err := repo.ListInvoices(ctx, InvoiceFilter{
		Statuses: []string{string(domain.InvoiceStatusPaid)},
	}, 10, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.TotalCount)
	require.Len(t, page.Invoices, 1)
	assert.Equal(t, domain.InvoiceStatusPaid, page.Invoices[0].Status)
}

// TestBillingRepository_ListInvoices_InvalidCursor covers cursor-decode error
// propagation from ListInvoices.
func TestBillingRepository_ListInvoices_InvalidCursor(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	_, err := repo.ListInvoices(ctx, InvoiceFilter{}, 10, "not-valid-base64!!!")
	assert.ErrorIs(t, err, ErrInvalidBillingCursor)
}

// TestBillingRepository_DecodeCursor_MalformedTokens drives the remaining
// decodeBillingCursor failure branches (missing separator, bad timestamp,
// bad UUID) through the public ListBillingRuns path.
func TestBillingRepository_DecodeCursor_MalformedTokens(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)

	tokens := map[string]string{
		"no separator":  base64.URLEncoding.EncodeToString([]byte("nopipe")),
		"bad timestamp": base64.URLEncoding.EncodeToString([]byte("bad-time|" + uuid.Nil.String())),
		"bad uuid":      base64.URLEncoding.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano) + "|not-a-uuid")),
	}

	for name, token := range tokens {
		t.Run(name, func(t *testing.T) {
			_, err := repo.ListBillingRuns(ctx, BillingRunFilter{}, 10, token)
			assert.ErrorIs(t, err, ErrInvalidBillingCursor)
		})
	}
}

// TestBillingRepository_ListInvoices_CorruptLineItems covers the invoiceToDomain
// error branch inside ListInvoices when stored line-item JSON is unparseable.
func TestBillingRepository_ListInvoices_CorruptLineItems(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	run, err := domain.NewBillingRun("tenant-corrupt", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	// Insert a row directly with invalid line-item JSON, bypassing the
	// repository's marshal step, so the read path hits the unmarshal error.
	entity := &InvoiceEntity{
		ID:            uuid.New(),
		BillingRunID:  run.ID,
		PartyID:       "party-1",
		AccountID:     "acc-1",
		InvoiceNumber: "INV-CORRUPT-001",
		PeriodStart:   start,
		PeriodEnd:     end,
		LineItems:     `"this-is-a-string-not-an-array"`,
		SubtotalCents: 100,
		Currency:      "GBP",
		Status:        string(domain.InvoiceStatusDraft),
		CreatedAt:     time.Now().UTC(),
	}
	seedTenantRow(t, db, ctx, entity)

	_, err = repo.ListInvoices(ctx, InvoiceFilter{}, 10, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal line items")
}

// TestBillingRepository_ListEmailsByInvoice covers the email audit-trail query,
// including the prefix LIKE match, ordering, and field mapping.
func TestBillingRepository_ListEmailsByInvoice(t *testing.T) {
	db, ctx, cleanup := testdb.SetupTestDB(
		t,
		testdb.WithModels(&BillingRunEntity{}, &InvoiceEntity{}, &email.OutboxEntity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewBillingRepository(db)
	invoiceID := uuid.New()
	otherInvoiceID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)

	// Two outbox rows for our invoice (different templates / times) plus one
	// row for a different invoice that must NOT be returned.
	rows := []*email.OutboxEntity{
		{
			ID:             uuid.New(),
			TenantID:       testTenantID,
			IdempotencyKey: "invoice-" + invoiceID.String() + "-issued",
			ToAddresses:    pq.StringArray{"a@example.com"},
			Subject:        "Invoice issued",
			TemplateName:   "invoice_issued",
			Status:         "SENT",
			NextAttemptAt:  now,
			CreatedAt:      now.Add(-2 * time.Hour),
			UpdatedAt:      now,
		},
		{
			ID:             uuid.New(),
			TenantID:       testTenantID,
			IdempotencyKey: "invoice-" + invoiceID.String() + "-reminder",
			ToAddresses:    pq.StringArray{"a@example.com", "b@example.com"},
			Subject:        "Invoice reminder",
			TemplateName:   "invoice_reminder",
			Status:         "PENDING",
			NextAttemptAt:  now,
			CreatedAt:      now.Add(-1 * time.Hour),
			UpdatedAt:      now,
		},
		{
			ID:             uuid.New(),
			TenantID:       testTenantID,
			IdempotencyKey: "invoice-" + otherInvoiceID.String() + "-issued",
			ToAddresses:    pq.StringArray{"c@example.com"},
			Subject:        "Other invoice",
			TemplateName:   "invoice_issued",
			Status:         "SENT",
			NextAttemptAt:  now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	for _, r := range rows {
		seedTenantRow(t, db, ctx, r)
	}

	entries, err := repo.ListEmailsByInvoice(ctx, invoiceID)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Ordered by created_at DESC: reminder (newer) before issued (older).
	assert.Equal(t, "invoice_reminder", entries[0].TemplateName)
	assert.Equal(t, "PENDING", entries[0].Status)
	assert.Equal(t, []string{"a@example.com", "b@example.com"}, entries[0].ToAddresses)

	assert.Equal(t, "invoice_issued", entries[1].TemplateName)
	assert.Equal(t, "SENT", entries[1].Status)
}

// TestBillingRepository_ListEmailsByInvoice_NoMatches covers the empty-result
// branch (no outbox rows for the invoice).
func TestBillingRepository_ListEmailsByInvoice_NoMatches(t *testing.T) {
	db, ctx, cleanup := testdb.SetupTestDB(
		t,
		testdb.WithModels(&BillingRunEntity{}, &InvoiceEntity{}, &email.OutboxEntity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewBillingRepository(db)
	entries, err := repo.ListEmailsByInvoice(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// seedTenantRow inserts a row directly so it is visible to the repository's
// tenant-scoped reads. The test DB connection is already bound to the tenant
// schema via testdb.WithTenant, matching the direct-insert pattern in
// audit_test.go.
func seedTenantRow(t *testing.T, db *gorm.DB, ctx context.Context, row any) {
	t.Helper()
	require.NoError(t, db.WithContext(ctx).Create(row).Error)
}
