package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	billingpb "github.com/meridianhub/meridian/api/proto/meridian/billing/v1"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockBillingRepo implements persistence.BillingRepository for unit testing.
type mockBillingRepo struct {
	billingRuns  map[uuid.UUID]*domain.BillingRun
	invoices     map[uuid.UUID]*domain.Invoice
	invoiceCount int64
	invoiceSum   int64
	updateErr    error
}

func newMockBillingRepo() *mockBillingRepo {
	return &mockBillingRepo{
		billingRuns: make(map[uuid.UUID]*domain.BillingRun),
		invoices:    make(map[uuid.UUID]*domain.Invoice),
	}
}

func (m *mockBillingRepo) CreateBillingRun(_ context.Context, run *domain.BillingRun) error {
	m.billingRuns[run.ID] = run
	return nil
}

func (m *mockBillingRepo) FindBillingRunByID(_ context.Context, id uuid.UUID) (*domain.BillingRun, error) {
	run, ok := m.billingRuns[id]
	if !ok {
		return nil, persistence.ErrBillingRunNotFound
	}
	return run, nil
}

func (m *mockBillingRepo) FindBillingRunByTenantAndPeriod(_ context.Context, _ string, _, _ time.Time) (*domain.BillingRun, error) {
	return nil, persistence.ErrBillingRunNotFound
}

func (m *mockBillingRepo) UpdateBillingRun(_ context.Context, _ *domain.BillingRun) error {
	return nil
}

func (m *mockBillingRepo) CreateInvoice(_ context.Context, inv *domain.Invoice) error {
	m.invoices[inv.ID] = inv
	return nil
}

func (m *mockBillingRepo) FindInvoiceByID(_ context.Context, id uuid.UUID) (*domain.Invoice, error) {
	inv, ok := m.invoices[id]
	if !ok {
		return nil, persistence.ErrInvoiceNotFound
	}
	return inv, nil
}

func (m *mockBillingRepo) FindInvoicesByBillingRunID(_ context.Context, _ uuid.UUID) ([]*domain.Invoice, error) {
	return nil, nil
}

func (m *mockBillingRepo) UpdateInvoice(_ context.Context, inv *domain.Invoice) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.invoices[inv.ID] = inv
	return nil
}

func (m *mockBillingRepo) ListBillingRuns(_ context.Context, _ persistence.BillingRunFilter, pageSize int, _ string) (*persistence.BillingRunPage, error) {
	runs := make([]*domain.BillingRun, 0)
	for _, r := range m.billingRuns {
		runs = append(runs, r)
		if len(runs) >= pageSize {
			break
		}
	}
	return &persistence.BillingRunPage{
		BillingRuns: runs,
		TotalCount:  int64(len(m.billingRuns)),
	}, nil
}

func (m *mockBillingRepo) ListInvoices(_ context.Context, _ persistence.InvoiceFilter, pageSize int, _ string) (*persistence.InvoicePage, error) {
	invoices := make([]*domain.Invoice, 0)
	for _, inv := range m.invoices {
		invoices = append(invoices, inv)
		if len(invoices) >= pageSize {
			break
		}
	}
	return &persistence.InvoicePage{
		Invoices:   invoices,
		TotalCount: int64(len(m.invoices)),
	}, nil
}

func (m *mockBillingRepo) CountInvoicesByBillingRun(_ context.Context, _ uuid.UUID) (int64, error) {
	return m.invoiceCount, nil
}

func (m *mockBillingRepo) SumInvoiceTotalsByBillingRun(_ context.Context, _ uuid.UUID) (int64, error) {
	return m.invoiceSum, nil
}

func (m *mockBillingRepo) ListEmailsByInvoice(_ context.Context, _ uuid.UUID) ([]*persistence.EmailAuditEntry, error) {
	return []*persistence.EmailAuditEntry{}, nil
}

// Compile-time check.
var _ persistence.BillingRepository = (*mockBillingRepo)(nil)

func TestBillingGRPC_GetBillingRun(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	run := &domain.BillingRun{
		ID:       uuid.New(),
		TenantID: "tenant-1",
		Status:   domain.BillingRunStatusCompleted,
	}
	repo.billingRuns[run.ID] = run
	repo.invoiceCount = 5
	repo.invoiceSum = 50000

	resp, err := svc.GetBillingRun(context.Background(), &billingpb.GetBillingRunRequest{
		Id: run.ID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, run.ID.String(), resp.BillingRun.Id)
	assert.Equal(t, billingpb.BillingRunStatus_BILLING_RUN_STATUS_COMPLETED, resp.BillingRun.Status)
	assert.Equal(t, int32(5), resp.BillingRun.InvoiceCount)
	assert.Equal(t, int64(50000), resp.BillingRun.TotalAmountCents)
}

func TestBillingGRPC_GetBillingRun_NotFound(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	_, err := svc.GetBillingRun(context.Background(), &billingpb.GetBillingRunRequest{
		Id: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestBillingGRPC_GetBillingRun_InvalidID(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	_, err := svc.GetBillingRun(context.Background(), &billingpb.GetBillingRunRequest{
		Id: "not-a-uuid",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestBillingGRPC_ListBillingRuns(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	run := &domain.BillingRun{ID: uuid.New(), TenantID: "t1", Status: domain.BillingRunStatusInitiated}
	repo.billingRuns[run.ID] = run

	resp, err := svc.ListBillingRuns(context.Background(), &billingpb.ListBillingRunsRequest{
		Pagination: &commonpb.Pagination{PageSize: 10},
	})
	require.NoError(t, err)
	assert.Len(t, resp.BillingRuns, 1)
	assert.Equal(t, int64(1), resp.Pagination.TotalCount)
}

func TestBillingGRPC_GetInvoice(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:            uuid.New(),
		BillingRunID:  uuid.New(),
		PartyID:       "p1",
		AccountID:     "a1",
		InvoiceNumber: "INV-001",
		SubtotalCents: 5000,
		Currency:      "GBP",
		Status:        domain.InvoiceStatusDraft,
	}
	repo.invoices[inv.ID] = inv

	resp, err := svc.GetInvoice(context.Background(), &billingpb.GetInvoiceRequest{
		Id: inv.ID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, inv.ID.String(), resp.Invoice.Id)
	assert.Equal(t, "INV-001", resp.Invoice.InvoiceNumber)
	assert.Equal(t, int64(5000), resp.Invoice.SubtotalCents)
}

func TestBillingGRPC_GetInvoice_NotFound(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	_, err := svc.GetInvoice(context.Background(), &billingpb.GetInvoiceRequest{
		Id: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestBillingGRPC_MarkInvoicePaid(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:            uuid.New(),
		BillingRunID:  uuid.New(),
		InvoiceNumber: "INV-PAY",
		Status:        domain.InvoiceStatusIssued,
	}
	repo.invoices[inv.ID] = inv

	resp, err := svc.MarkInvoicePaid(context.Background(), &billingpb.MarkInvoicePaidRequest{
		InvoiceId: inv.ID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, billingpb.InvoiceStatus_INVOICE_STATUS_PAID, resp.Invoice.Status)
}

func TestBillingGRPC_MarkInvoicePaid_AlreadyPaid(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:     uuid.New(),
		Status: domain.InvoiceStatusPaid,
	}
	repo.invoices[inv.ID] = inv

	resp, err := svc.MarkInvoicePaid(context.Background(), &billingpb.MarkInvoicePaidRequest{
		InvoiceId: inv.ID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, billingpb.InvoiceStatus_INVOICE_STATUS_PAID, resp.Invoice.Status)
}

func TestBillingGRPC_MarkInvoicePaid_InvalidTransition(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:     uuid.New(),
		Status: domain.InvoiceStatusVoid,
	}
	repo.invoices[inv.ID] = inv

	_, err := svc.MarkInvoicePaid(context.Background(), &billingpb.MarkInvoicePaidRequest{
		InvoiceId: inv.ID.String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestBillingGRPC_VoidInvoice(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:     uuid.New(),
		Status: domain.InvoiceStatusDraft,
	}
	repo.invoices[inv.ID] = inv

	resp, err := svc.VoidInvoice(context.Background(), &billingpb.VoidInvoiceRequest{
		InvoiceId: inv.ID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, billingpb.InvoiceStatus_INVOICE_STATUS_VOID, resp.Invoice.Status)
}

func TestBillingGRPC_VoidInvoice_AlreadyVoided(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:     uuid.New(),
		Status: domain.InvoiceStatusVoid,
	}
	repo.invoices[inv.ID] = inv

	resp, err := svc.VoidInvoice(context.Background(), &billingpb.VoidInvoiceRequest{
		InvoiceId: inv.ID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, billingpb.InvoiceStatus_INVOICE_STATUS_VOID, resp.Invoice.Status)
}

func TestBillingGRPC_VoidInvoice_InvalidTransition(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:     uuid.New(),
		Status: domain.InvoiceStatusPaid,
	}
	repo.invoices[inv.ID] = inv

	_, err := svc.VoidInvoice(context.Background(), &billingpb.VoidInvoiceRequest{
		InvoiceId: inv.ID.String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestBillingGRPC_ListInvoiceEmails(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	resp, err := svc.ListInvoiceEmails(context.Background(), &billingpb.ListInvoiceEmailsRequest{
		InvoiceId: uuid.New().String(),
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Emails)
}

func TestBillingGRPC_ListInvoiceEmails_InvalidID(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	_, err := svc.ListInvoiceEmails(context.Background(), &billingpb.ListInvoiceEmailsRequest{
		InvoiceId: "bad",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestBillingGRPC_ResendInvoiceEmail_NoEmailRepo(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:            uuid.New(),
		InvoiceNumber: "INV-RESEND",
		Status:        domain.InvoiceStatusIssued,
	}
	repo.invoices[inv.ID] = inv

	_, err := svc.ResendInvoiceEmail(context.Background(), &billingpb.ResendInvoiceEmailRequest{
		InvoiceId: inv.ID.String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

func TestBillingGRPC_ResendInvoiceEmail_VoidedInvoice(t *testing.T) {
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:     uuid.New(),
		Status: domain.InvoiceStatusVoid,
	}
	repo.invoices[inv.ID] = inv

	_, err := svc.ResendInvoiceEmail(context.Background(), &billingpb.ResendInvoiceEmailRequest{
		InvoiceId: inv.ID.String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}
