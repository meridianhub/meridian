package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBillingTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	return testdb.SetupTestDB(t,
		testdb.WithModels(&BillingRunEntity{}, &InvoiceEntity{}),
		testdb.WithTenant(testTenantID),
	)
}

func TestBillingRepository_CreateAndFindBillingRun(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	run, err := domain.NewBillingRun("tenant-billing-test", start, end)
	require.NoError(t, err)

	err = repo.CreateBillingRun(ctx, run)
	require.NoError(t, err)

	// FindByID
	found, err := repo.FindBillingRunByID(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, run.ID, found.ID)
	assert.Equal(t, "tenant-billing-test", found.TenantID)
	assert.Equal(t, domain.BillingRunStatusInitiated, found.Status)
}

func TestBillingRepository_FindBillingRunByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	_, err := repo.FindBillingRunByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrBillingRunNotFound)
}

func TestBillingRepository_FindBillingRunByTenantAndPeriod(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	run, err := domain.NewBillingRun("tenant-period-test", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	found, err := repo.FindBillingRunByTenantAndPeriod(ctx, "tenant-period-test", start, end)
	require.NoError(t, err)
	assert.Equal(t, run.ID, found.ID)

	// Not found for different period
	_, err = repo.FindBillingRunByTenantAndPeriod(ctx, "tenant-period-test",
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	assert.ErrorIs(t, err, ErrBillingRunNotFound)
}

func TestBillingRepository_UpdateBillingRun(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	run, err := domain.NewBillingRun("tenant-update-test", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	// Transition to processing
	require.NoError(t, run.StartProcessing())
	require.NoError(t, repo.UpdateBillingRun(ctx, run))

	found, err := repo.FindBillingRunByID(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BillingRunStatusProcessing, found.Status)
}

func TestBillingRepository_UpdateBillingRun_NotFound(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	run := &domain.BillingRun{
		ID:       uuid.New(),
		TenantID: "nonexistent",
		Status:   domain.BillingRunStatusInitiated,
	}

	err := repo.UpdateBillingRun(ctx, run)
	assert.ErrorIs(t, err, ErrBillingRunNotFound)
}

func TestBillingRepository_CreateBillingRun_Duplicate(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	run1, err := domain.NewBillingRun("tenant-dup-test", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run1))

	// Same ID should trigger duplicate key error
	err = repo.CreateBillingRun(ctx, run1)
	assert.Error(t, err)
}

func TestBillingRepository_CreateAndFindInvoice(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)

	// Create parent billing run first
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	run, err := domain.NewBillingRun("tenant-inv-test", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	inv := &domain.Invoice{
		ID:            uuid.New(),
		BillingRunID:  run.ID,
		PartyID:       "party-1",
		AccountID:     "acc-1",
		InvoiceNumber: "INV-2026-001",
		PeriodStart:   start,
		PeriodEnd:     end,
		LineItems: []domain.InvoiceLineItem{
			{Description: "Service Fee", Quantity: decimal.NewFromInt(1), UnitPriceCents: 5000, TotalCents: 5000},
		},
		SubtotalCents: 5000,
		Currency:      "GBP",
		Status:        domain.InvoiceStatusDraft,
		CreatedAt:     time.Now().UTC(),
	}

	err = repo.CreateInvoice(ctx, inv)
	require.NoError(t, err)

	found, err := repo.FindInvoiceByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, inv.ID, found.ID)
	assert.Equal(t, "party-1", found.PartyID)
	assert.Equal(t, int64(5000), found.SubtotalCents)
	assert.Len(t, found.LineItems, 1)
}

func TestBillingRepository_FindInvoiceByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	_, err := repo.FindInvoiceByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrInvoiceNotFound)
}

func TestBillingRepository_FindInvoicesByBillingRunID(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)

	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	run, err := domain.NewBillingRun("tenant-find-inv", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	for i := range 3 {
		inv := &domain.Invoice{
			ID:            uuid.New(),
			BillingRunID:  run.ID,
			PartyID:       "party-1",
			AccountID:     "acc-1",
			InvoiceNumber: "INV-2026-0" + string(rune('1'+i)),
			PeriodStart:   start,
			PeriodEnd:     end,
			LineItems:     []domain.InvoiceLineItem{},
			SubtotalCents: int64((i + 1) * 1000),
			Currency:      "GBP",
			Status:        domain.InvoiceStatusDraft,
			CreatedAt:     time.Now().UTC(),
		}
		require.NoError(t, repo.CreateInvoice(ctx, inv))
	}

	invoices, err := repo.FindInvoicesByBillingRunID(ctx, run.ID)
	require.NoError(t, err)
	assert.Len(t, invoices, 3)

	// Non-existent billing run should return empty
	invoices, err = repo.FindInvoicesByBillingRunID(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, invoices)
}

func TestBillingRepository_UpdateInvoice(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)

	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	run, err := domain.NewBillingRun("tenant-upd-inv", start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(ctx, run))

	inv := &domain.Invoice{
		ID:            uuid.New(),
		BillingRunID:  run.ID,
		PartyID:       "party-1",
		AccountID:     "acc-1",
		InvoiceNumber: "INV-UPD-001",
		PeriodStart:   start,
		PeriodEnd:     end,
		LineItems:     []domain.InvoiceLineItem{},
		SubtotalCents: 1000,
		Currency:      "GBP",
		Status:        domain.InvoiceStatusDraft,
		CreatedAt:     time.Now().UTC(),
	}
	require.NoError(t, repo.CreateInvoice(ctx, inv))

	// Update with payment order ID
	poID := uuid.New()
	inv.PaymentOrderID = &poID
	inv.Status = domain.InvoiceStatusPaid
	require.NoError(t, repo.UpdateInvoice(ctx, inv))

	found, err := repo.FindInvoiceByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.InvoiceStatusPaid, found.Status)
	require.NotNil(t, found.PaymentOrderID)
	assert.Equal(t, poID, *found.PaymentOrderID)
}

func TestBillingRepository_UpdateInvoice_NotFound(t *testing.T) {
	db, ctx, cleanup := setupBillingTestDB(t)
	defer cleanup()

	repo := NewBillingRepository(db)
	inv := &domain.Invoice{
		ID:            uuid.New(),
		BillingRunID:  uuid.New(),
		InvoiceNumber: "INV-GHOST",
		LineItems:     []domain.InvoiceLineItem{},
		Status:        domain.InvoiceStatusDraft,
		CreatedAt:     time.Now().UTC(),
	}

	err := repo.UpdateInvoice(ctx, inv)
	assert.ErrorIs(t, err, ErrInvoiceNotFound)
}
