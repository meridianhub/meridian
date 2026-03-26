package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock PartyClient ---

type mockPartyClient struct {
	mu         sync.Mutex
	contacts   map[string]PartyContact
	contactErr map[string]error
}

func newMockPartyClient() *mockPartyClient {
	return &mockPartyClient{
		contacts:   make(map[string]PartyContact),
		contactErr: make(map[string]error),
	}
}

func (m *mockPartyClient) GetPartyContact(_ context.Context, partyID string) (PartyContact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err, ok := m.contactErr[partyID]; ok {
		return PartyContact{}, err
	}
	return m.contacts[partyID], nil
}

// --- Mock OutboxRepository ---

type mockEmailOutbox struct {
	mu         sync.Mutex
	entries    []*email.OutboxEntry
	enqueueErr error
}

func (m *mockEmailOutbox) Enqueue(_ context.Context, entry *email.OutboxEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.enqueueErr != nil {
		return m.enqueueErr
	}
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockEmailOutbox) FetchDispatchable(_ context.Context, _ int) ([]email.OutboxEntry, error) {
	return nil, nil
}

func (m *mockEmailOutbox) MarkSent(_ context.Context, _ uuid.UUID) error { return nil }

func (m *mockEmailOutbox) MarkFailed(_ context.Context, _ uuid.UUID, _ string) error { return nil }

func (m *mockEmailOutbox) Cancel(_ context.Context, _ uuid.UUID) error { return nil }

var (
	_ email.OutboxRepository = (*mockEmailOutbox)(nil)
	_ PartyClient            = (*mockPartyClient)(nil)
)

// --- Helpers ---

func makeInvoiceWithLineItems(t *testing.T, billingRunID uuid.UUID, partyID string) *domain.Invoice {
	t.Helper()
	lineItems := []domain.InvoiceLineItem{
		{
			Description:    "Service fee",
			Quantity:       decimal.NewFromInt(1),
			UnitPriceCents: 5000,
			TotalCents:     5000,
		},
	}
	inv, err := domain.NewInvoice(
		billingRunID,
		partyID,
		"acct-1",
		"INV-tenant-2026-01-0001",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		lineItems,
		"GBP",
	)
	require.NoError(t, err)
	return inv
}

// --- Tests ---

func TestQueueInvoiceEmail_ShadowMode(t *testing.T) {
	ctx := context.Background()
	partyClient := newMockPartyClient()
	partyClient.contacts["party-a"] = PartyContact{Email: "customer@example.com", Name: "Acme Corp"}
	outbox := &mockEmailOutbox{}

	repo := newMockBillingRepo()
	gen := NewInvoiceGenerator(
		&mockPositionClient{},
		repo,
		testInvoiceMetrics(t),
		testInvoiceLogger(),
	).WithEmailDelivery(partyClient, outbox, true /* shadowMode */)

	inv := makeInvoiceWithLineItems(t, uuid.New(), "party-a")
	gen.queueInvoiceEmail(ctx, inv)

	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	assert.Empty(t, outbox.entries, "shadow mode should skip email queueing")
}

func TestQueueInvoiceEmail_MissingPartyContact(t *testing.T) {
	ctx := context.Background()
	partyClient := newMockPartyClient()
	partyClient.contactErr["party-missing"] = errors.New("party not found")
	outbox := &mockEmailOutbox{}

	repo := newMockBillingRepo()
	gen := NewInvoiceGenerator(
		&mockPositionClient{},
		repo,
		testInvoiceMetrics(t),
		testInvoiceLogger(),
	).WithEmailDelivery(partyClient, outbox, false)

	inv := makeInvoiceWithLineItems(t, uuid.New(), "party-missing")
	// Should not panic or return error — logs warning and returns
	gen.queueInvoiceEmail(ctx, inv)

	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	assert.Empty(t, outbox.entries, "missing email should log warning and not enqueue")
}

func TestQueueInvoiceEmail_EnqueueFailure_NonFatal(t *testing.T) {
	ctx := context.Background()
	partyClient := newMockPartyClient()
	partyClient.contacts["party-a"] = PartyContact{Email: "customer@example.com", Name: "Acme Corp"}
	outbox := &mockEmailOutbox{enqueueErr: errors.New("database unavailable")}

	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	repo := newMockBillingRepo()
	posClient := &mockPositionClient{
		accounts: []AccountInfo{
			{AccountID: "acct-1", PartyID: "party-a", Currency: "GBP"},
		},
		entries: map[string][]PositionEntry{
			"acct-1": {
				{Description: "Service fee", AmountCents: 5000, Quantity: decimal.NewFromInt(1), UnitPriceCents: 5000},
			},
		},
	}

	billingRun := createTestBillingRun(t, "tenant-1", periodStart, periodEnd)
	require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

	gen := NewInvoiceGenerator(posClient, repo, testInvoiceMetrics(t), testInvoiceLogger()).
		WithEmailDelivery(partyClient, outbox, false)

	// Invoice generation should succeed even when email queueing fails
	invoices, err := gen.GenerateInvoices(ctx, billingRun)
	require.NoError(t, err)
	assert.Len(t, invoices, 1, "invoice should be created even if email queueing fails")
}

func TestQueueInvoiceEmail_Success(t *testing.T) {
	ctx := context.Background()
	partyClient := newMockPartyClient()
	partyClient.contacts["party-a"] = PartyContact{Email: "customer@example.com", Name: "Acme Corp"}
	outbox := &mockEmailOutbox{}

	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	repo := newMockBillingRepo()
	posClient := &mockPositionClient{
		accounts: []AccountInfo{
			{AccountID: "acct-1", PartyID: "party-a", Currency: "GBP"},
		},
		entries: map[string][]PositionEntry{
			"acct-1": {
				{Description: "Service fee", AmountCents: 5000, Quantity: decimal.NewFromInt(1), UnitPriceCents: 5000},
			},
		},
	}

	billingRun := createTestBillingRun(t, "tenant-1", periodStart, periodEnd)
	require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

	gen := NewInvoiceGenerator(posClient, repo, testInvoiceMetrics(t), testInvoiceLogger()).
		WithEmailDelivery(partyClient, outbox, false)

	invoices, err := gen.GenerateInvoices(ctx, billingRun)
	require.NoError(t, err)
	require.Len(t, invoices, 1)

	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	require.Len(t, outbox.entries, 1, "one email should be queued per invoice")

	entry := outbox.entries[0]
	assert.Equal(t, []string{"customer@example.com"}, entry.ToAddresses)
	assert.Equal(t, "invoice", entry.TemplateName)
	assert.Equal(t, "invoice-"+invoices[0].ID.String(), entry.IdempotencyKey)
	assert.Contains(t, entry.Subject, invoices[0].InvoiceNumber)

	data := entry.TemplateData
	assert.Equal(t, "Acme Corp", data["CustomerName"])
	assert.Equal(t, invoices[0].InvoiceNumber, data["InvoiceNumber"])
	assert.Equal(t, "GBP 50.00", data["Total"])
}

func TestQueueInvoiceEmail_NoEmailClient(t *testing.T) {
	ctx := context.Background()
	// No WithEmailDelivery call — email delivery not configured
	repo := newMockBillingRepo()
	gen := NewInvoiceGenerator(
		&mockPositionClient{},
		repo,
		testInvoiceMetrics(t),
		testInvoiceLogger(),
	)

	inv := makeInvoiceWithLineItems(t, uuid.New(), "party-a")
	// Should be a no-op, no panic
	gen.queueInvoiceEmail(ctx, inv)
}
