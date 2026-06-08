package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	billingpb "github.com/meridianhub/meridian/api/proto/meridian/billing/v1"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/services/payment-order/domain/testfixtures"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// -----------------------------------------------------------------------------
// billing_grpc_mappers.go - pure proto<->domain mapping coverage
// -----------------------------------------------------------------------------

func TestMapBillingRunStatusToProto_AllValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   domain.BillingRunStatus
		want billingpb.BillingRunStatus
	}{
		{domain.BillingRunStatusInitiated, billingpb.BillingRunStatus_BILLING_RUN_STATUS_INITIATED},
		{domain.BillingRunStatusProcessing, billingpb.BillingRunStatus_BILLING_RUN_STATUS_PROCESSING},
		{domain.BillingRunStatusCompleted, billingpb.BillingRunStatus_BILLING_RUN_STATUS_COMPLETED},
		{domain.BillingRunStatusFailed, billingpb.BillingRunStatus_BILLING_RUN_STATUS_FAILED},
		{domain.BillingRunStatus("UNKNOWN"), billingpb.BillingRunStatus_BILLING_RUN_STATUS_UNSPECIFIED},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, mapBillingRunStatusToProto(tc.in), "status %q", tc.in)
	}
}

func TestMapInvoiceStatusToProto_AllValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   domain.InvoiceStatus
		want billingpb.InvoiceStatus
	}{
		{domain.InvoiceStatusDraft, billingpb.InvoiceStatus_INVOICE_STATUS_DRAFT},
		{domain.InvoiceStatusIssued, billingpb.InvoiceStatus_INVOICE_STATUS_ISSUED},
		{domain.InvoiceStatusPaid, billingpb.InvoiceStatus_INVOICE_STATUS_PAID},
		{domain.InvoiceStatusVoid, billingpb.InvoiceStatus_INVOICE_STATUS_VOID},
		{domain.InvoiceStatusOverdue, billingpb.InvoiceStatus_INVOICE_STATUS_OVERDUE},
		{domain.InvoiceStatus("UNKNOWN"), billingpb.InvoiceStatus_INVOICE_STATUS_UNSPECIFIED},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, mapInvoiceStatusToProto(tc.in), "status %q", tc.in)
	}
}

func TestMapEmailStatusToProto_AllValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want billingpb.EmailStatus
	}{
		{"PENDING", billingpb.EmailStatus_EMAIL_STATUS_PENDING},
		{"SENT", billingpb.EmailStatus_EMAIL_STATUS_SENT},
		{"SENDING", billingpb.EmailStatus_EMAIL_STATUS_SENT},
		{"DELIVERED", billingpb.EmailStatus_EMAIL_STATUS_DELIVERED},
		{"BOUNCED", billingpb.EmailStatus_EMAIL_STATUS_BOUNCED},
		{"DEAD_LETTER", billingpb.EmailStatus_EMAIL_STATUS_DEAD_LETTER},
		{"FAILED", billingpb.EmailStatus_EMAIL_STATUS_DEAD_LETTER},
		{"CANCELLED", billingpb.EmailStatus_EMAIL_STATUS_CANCELLED},
		{"WHATEVER", billingpb.EmailStatus_EMAIL_STATUS_UNSPECIFIED},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, mapEmailStatusToProto(tc.in), "status %q", tc.in)
	}
}

func TestMapProtoBillingRunStatuses(t *testing.T) {
	t.Parallel()

	assert.Nil(t, mapProtoBillingRunStatuses(nil))

	got := mapProtoBillingRunStatuses([]billingpb.BillingRunStatus{
		billingpb.BillingRunStatus_BILLING_RUN_STATUS_INITIATED,
		billingpb.BillingRunStatus_BILLING_RUN_STATUS_PROCESSING,
		billingpb.BillingRunStatus_BILLING_RUN_STATUS_COMPLETED,
		billingpb.BillingRunStatus_BILLING_RUN_STATUS_FAILED,
		billingpb.BillingRunStatus_BILLING_RUN_STATUS_UNSPECIFIED, // skipped
	})
	assert.Equal(t, []string{
		string(domain.BillingRunStatusInitiated),
		string(domain.BillingRunStatusProcessing),
		string(domain.BillingRunStatusCompleted),
		string(domain.BillingRunStatusFailed),
	}, got)
}

func TestMapProtoInvoiceStatuses(t *testing.T) {
	t.Parallel()

	assert.Nil(t, mapProtoInvoiceStatuses(nil))

	got := mapProtoInvoiceStatuses([]billingpb.InvoiceStatus{
		billingpb.InvoiceStatus_INVOICE_STATUS_DRAFT,
		billingpb.InvoiceStatus_INVOICE_STATUS_ISSUED,
		billingpb.InvoiceStatus_INVOICE_STATUS_PAID,
		billingpb.InvoiceStatus_INVOICE_STATUS_VOID,
		billingpb.InvoiceStatus_INVOICE_STATUS_OVERDUE,
		billingpb.InvoiceStatus_INVOICE_STATUS_UNSPECIFIED, // skipped
	})
	assert.Equal(t, []string{
		string(domain.InvoiceStatusDraft),
		string(domain.InvoiceStatusIssued),
		string(domain.InvoiceStatusPaid),
		string(domain.InvoiceStatusVoid),
		string(domain.InvoiceStatusOverdue),
	}, got)
}

func TestInvoiceToProto_WithLineItemsAndValuation(t *testing.T) {
	t.Parallel()

	inv := &domain.Invoice{
		ID:            uuid.New(),
		BillingRunID:  uuid.New(),
		PartyID:       "party-1",
		AccountID:     "acct-1",
		InvoiceNumber: "INV-100",
		PeriodStart:   time.Now().Add(-24 * time.Hour),
		PeriodEnd:     time.Now(),
		SubtotalCents: 12345,
		Currency:      "GBP",
		Status:        domain.InvoiceStatusIssued,
		CreatedAt:     time.Now(),
		LineItems: []domain.InvoiceLineItem{
			{
				Description:    "energy usage",
				Quantity:       decimal.NewFromInt(42),
				UnitPriceCents: 100,
				TotalCents:     4200,
				ValuationAnalysis: map[string]any{
					"source":  "position-keeping",
					"quality": "ACTUAL",
				},
			},
			{
				Description:    "no valuation",
				Quantity:       decimal.NewFromInt(1),
				UnitPriceCents: 50,
				TotalCents:     50,
			},
		},
	}

	pbInv := invoiceToProto(inv)

	assert.Equal(t, inv.ID.String(), pbInv.Id)
	assert.Equal(t, "INV-100", pbInv.InvoiceNumber)
	assert.Equal(t, int64(12345), pbInv.SubtotalCents)
	assert.Equal(t, billingpb.InvoiceStatus_INVOICE_STATUS_ISSUED, pbInv.Status)
	require.Len(t, pbInv.LineItems, 2)

	first := pbInv.LineItems[0]
	assert.Equal(t, "energy usage", first.Description)
	assert.Equal(t, "42", first.Quantity)
	assert.Equal(t, int64(100), first.UnitPriceCents)
	assert.Equal(t, int64(4200), first.TotalCents)
	require.NotNil(t, first.ValuationAnalysis)
	assert.Equal(t, "position-keeping", first.ValuationAnalysis.Fields["source"].GetStringValue())

	// Second line item has no valuation analysis.
	assert.Nil(t, pbInv.LineItems[1].ValuationAnalysis)
}

func TestBillingRunToProto_AllFields(t *testing.T) {
	t.Parallel()

	run := &domain.BillingRun{
		ID:            uuid.New(),
		TenantID:      "tenant-x",
		CycleStart:    time.Now().Add(-720 * time.Hour),
		CycleEnd:      time.Now(),
		Status:        domain.BillingRunStatusFailed,
		DunningLevel:  2,
		FailureReason: "gateway timeout",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	pbRun := billingRunToProto(run)

	assert.Equal(t, run.ID.String(), pbRun.Id)
	assert.Equal(t, "tenant-x", pbRun.TenantId)
	assert.Equal(t, billingpb.BillingRunStatus_BILLING_RUN_STATUS_FAILED, pbRun.Status)
	assert.Equal(t, int32(2), pbRun.DunningLevel)
	assert.Equal(t, "gateway timeout", pbRun.FailureReason)
	require.NotNil(t, pbRun.PeriodStart)
	require.NotNil(t, pbRun.PeriodEnd)
}

func TestEmailAuditToProto(t *testing.T) {
	t.Parallel()

	entry := &persistence.EmailAuditEntry{
		IdempotencyKey: "invoice-abc-1",
		TemplateName:   "invoice_issued",
		ToAddresses:    []string{"a@example.com", "b@example.com"},
		Status:         "DELIVERED",
		CreatedAt:      time.Now(),
	}

	pbEmail := emailAuditToProto(entry)

	assert.Equal(t, "invoice-abc-1", pbEmail.IdempotencyKey)
	assert.Equal(t, "invoice_issued", pbEmail.TemplateName)
	assert.Equal(t, []string{"a@example.com", "b@example.com"}, pbEmail.ToAddresses)
	assert.Equal(t, billingpb.EmailStatus_EMAIL_STATUS_DELIVERED, pbEmail.Status)
}

// -----------------------------------------------------------------------------
// billing_grpc.go - handler branches needing an email repo / richer repo
// -----------------------------------------------------------------------------

// fakeOutboxRepo is a configurable email.OutboxRepository for billing handler tests.
type fakeOutboxRepo struct {
	enqueueErr       error
	enqueued         []*email.OutboxEntry
	cancelErr        error
	cancelledPattern string
	cancelledCount   int64
}

func (f *fakeOutboxRepo) Enqueue(_ context.Context, entry *email.OutboxEntry) error {
	if f.enqueueErr != nil {
		return f.enqueueErr
	}
	f.enqueued = append(f.enqueued, entry)
	return nil
}

func (f *fakeOutboxRepo) FetchDispatchable(_ context.Context, _ int) ([]email.OutboxEntry, error) {
	return nil, nil
}

func (f *fakeOutboxRepo) MarkSent(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeOutboxRepo) MarkFailed(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (f *fakeOutboxRepo) Cancel(_ context.Context, _ uuid.UUID) error { return nil }

func (f *fakeOutboxRepo) CancelByIdempotencyKeyPattern(_ context.Context, pattern string) (int64, error) {
	f.cancelledPattern = pattern
	if f.cancelErr != nil {
		return 0, f.cancelErr
	}
	return f.cancelledCount, nil
}

var _ email.OutboxRepository = (*fakeOutboxRepo)(nil)

// emailListBillingRepo wraps mockBillingRepo behavior but returns configurable
// email audit entries, which the base mock always returns empty.
type emailListBillingRepo struct {
	*mockBillingRepo
	emails    []*persistence.EmailAuditEntry
	emailsErr error
}

func (r *emailListBillingRepo) ListEmailsByInvoice(_ context.Context, _ uuid.UUID) ([]*persistence.EmailAuditEntry, error) {
	if r.emailsErr != nil {
		return nil, r.emailsErr
	}
	return r.emails, nil
}

var _ persistence.BillingRepository = (*emailListBillingRepo)(nil)

func TestBillingGRPC_ListInvoices(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:            uuid.New(),
		BillingRunID:  uuid.New(),
		InvoiceNumber: "INV-LIST",
		Status:        domain.InvoiceStatusIssued,
	}
	repo.invoices[inv.ID] = inv

	resp, err := svc.ListInvoices(context.Background(), &billingpb.ListInvoicesRequest{
		Pagination: &commonpb.Pagination{PageSize: 10},
		PartyId:    "p1",
	})
	require.NoError(t, err)
	require.Len(t, resp.Invoices, 1)
	assert.Equal(t, "INV-LIST", resp.Invoices[0].InvoiceNumber)
	assert.Equal(t, int64(1), resp.Pagination.TotalCount)
}

func TestBillingGRPC_ListInvoiceEmails_MapsEntries(t *testing.T) {
	t.Parallel()
	repo := &emailListBillingRepo{
		mockBillingRepo: newMockBillingRepo(),
		emails: []*persistence.EmailAuditEntry{
			{IdempotencyKey: "invoice-1-a", TemplateName: "invoice_issued", Status: "SENT"},
			{IdempotencyKey: "invoice-1-b", TemplateName: "invoice_issued", Status: "BOUNCED"},
		},
	}
	svc := NewBillingService(repo, nil, nil)

	resp, err := svc.ListInvoiceEmails(context.Background(), &billingpb.ListInvoiceEmailsRequest{
		InvoiceId: uuid.New().String(),
	})
	require.NoError(t, err)
	require.Len(t, resp.Emails, 2)
	assert.Equal(t, billingpb.EmailStatus_EMAIL_STATUS_SENT, resp.Emails[0].Status)
	assert.Equal(t, billingpb.EmailStatus_EMAIL_STATUS_BOUNCED, resp.Emails[1].Status)
	assert.Equal(t, int64(2), resp.Pagination.TotalCount)
}

func TestBillingGRPC_ListInvoiceEmails_RepoError(t *testing.T) {
	t.Parallel()
	repo := &emailListBillingRepo{
		mockBillingRepo: newMockBillingRepo(),
		emailsErr:       errors.New("db down"),
	}
	svc := NewBillingService(repo, nil, nil)

	_, err := svc.ListInvoiceEmails(context.Background(), &billingpb.ListInvoiceEmailsRequest{
		InvoiceId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestBillingGRPC_ResendInvoiceEmail_Success(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	outbox := &fakeOutboxRepo{}
	svc := NewBillingService(repo, outbox, nil)

	inv := &domain.Invoice{
		ID:            uuid.New(),
		InvoiceNumber: "INV-RS",
		SubtotalCents: 9900,
		Currency:      "GBP",
		Status:        domain.InvoiceStatusIssued,
	}
	repo.invoices[inv.ID] = inv

	resp, err := svc.ResendInvoiceEmail(context.Background(), &billingpb.ResendInvoiceEmailRequest{
		InvoiceId:      inv.ID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "caller-key"},
	})
	require.NoError(t, err)
	assert.Equal(t, billingpb.EmailStatus_EMAIL_STATUS_PENDING, resp.Email.Status)
	require.Len(t, outbox.enqueued, 1)
	// Idempotency key is namespaced under the invoice prefix with the caller key.
	assert.Contains(t, outbox.enqueued[0].IdempotencyKey, "invoice-"+inv.ID.String())
	assert.Contains(t, outbox.enqueued[0].IdempotencyKey, "caller-key")
}

func TestBillingGRPC_ResendInvoiceEmail_GeneratedKey(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	outbox := &fakeOutboxRepo{}
	svc := NewBillingService(repo, outbox, nil)

	inv := &domain.Invoice{
		ID:            uuid.New(),
		InvoiceNumber: "INV-GEN",
		Status:        domain.InvoiceStatusIssued,
	}
	repo.invoices[inv.ID] = inv

	// No caller idempotency key supplied -> service generates a resend key.
	_, err := svc.ResendInvoiceEmail(context.Background(), &billingpb.ResendInvoiceEmailRequest{
		InvoiceId: inv.ID.String(),
	})
	require.NoError(t, err)
	require.Len(t, outbox.enqueued, 1)
	assert.Contains(t, outbox.enqueued[0].IdempotencyKey, "-resend-")
}

func TestBillingGRPC_ResendInvoiceEmail_DuplicateIsIdempotent(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	outbox := &fakeOutboxRepo{enqueueErr: email.ErrDuplicateIdempotency}
	svc := NewBillingService(repo, outbox, nil)

	inv := &domain.Invoice{
		ID:            uuid.New(),
		InvoiceNumber: "INV-DUP",
		Status:        domain.InvoiceStatusIssued,
	}
	repo.invoices[inv.ID] = inv

	// Duplicate enqueue is swallowed and the call still succeeds.
	resp, err := svc.ResendInvoiceEmail(context.Background(), &billingpb.ResendInvoiceEmailRequest{
		InvoiceId: inv.ID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, billingpb.EmailStatus_EMAIL_STATUS_PENDING, resp.Email.Status)
}

func TestBillingGRPC_ResendInvoiceEmail_EnqueueError(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	outbox := &fakeOutboxRepo{enqueueErr: errors.New("queue unavailable")}
	svc := NewBillingService(repo, outbox, nil)

	inv := &domain.Invoice{
		ID:            uuid.New(),
		InvoiceNumber: "INV-ERR",
		Status:        domain.InvoiceStatusIssued,
	}
	repo.invoices[inv.ID] = inv

	_, err := svc.ResendInvoiceEmail(context.Background(), &billingpb.ResendInvoiceEmailRequest{
		InvoiceId: inv.ID.String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestBillingGRPC_ResendInvoiceEmail_InvalidID(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, &fakeOutboxRepo{}, nil)

	_, err := svc.ResendInvoiceEmail(context.Background(), &billingpb.ResendInvoiceEmailRequest{
		InvoiceId: "not-a-uuid",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestBillingGRPC_ResendInvoiceEmail_NotFound(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, &fakeOutboxRepo{}, nil)

	_, err := svc.ResendInvoiceEmail(context.Background(), &billingpb.ResendInvoiceEmailRequest{
		InvoiceId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestBillingGRPC_VoidInvoice_CancelsEmails(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	outbox := &fakeOutboxRepo{cancelledCount: 3}
	svc := NewBillingService(repo, outbox, nil)

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
	// Void should attempt to cancel pending emails under the invoice prefix.
	assert.Equal(t, "invoice-"+inv.ID.String()+"%", outbox.cancelledPattern)
}

func TestBillingGRPC_VoidInvoice_EmailCancelError_StillSucceeds(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	outbox := &fakeOutboxRepo{cancelErr: errors.New("cancel failed")}
	svc := NewBillingService(repo, outbox, nil)

	inv := &domain.Invoice{
		ID:     uuid.New(),
		Status: domain.InvoiceStatusDraft,
	}
	repo.invoices[inv.ID] = inv

	// Email cancel failure is logged but does not fail the void operation.
	resp, err := svc.VoidInvoice(context.Background(), &billingpb.VoidInvoiceRequest{
		InvoiceId: inv.ID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, billingpb.InvoiceStatus_INVOICE_STATUS_VOID, resp.Invoice.Status)
}

func TestBillingGRPC_VoidInvoice_UpdateError(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	repo.updateErr = errors.New("update failed")
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:     uuid.New(),
		Status: domain.InvoiceStatusDraft,
	}
	repo.invoices[inv.ID] = inv

	_, err := svc.VoidInvoice(context.Background(), &billingpb.VoidInvoiceRequest{
		InvoiceId: inv.ID.String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestBillingGRPC_VoidInvoice_InvalidID(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	_, err := svc.VoidInvoice(context.Background(), &billingpb.VoidInvoiceRequest{
		InvoiceId: "bad",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestBillingGRPC_VoidInvoice_NotFound(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	_, err := svc.VoidInvoice(context.Background(), &billingpb.VoidInvoiceRequest{
		InvoiceId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestBillingGRPC_MarkInvoicePaid_UpdateError(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	repo.updateErr = errors.New("update failed")
	svc := NewBillingService(repo, nil, nil)

	inv := &domain.Invoice{
		ID:     uuid.New(),
		Status: domain.InvoiceStatusIssued,
	}
	repo.invoices[inv.ID] = inv

	_, err := svc.MarkInvoicePaid(context.Background(), &billingpb.MarkInvoicePaidRequest{
		InvoiceId: inv.ID.String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestBillingGRPC_MarkInvoicePaid_InvalidID(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	_, err := svc.MarkInvoicePaid(context.Background(), &billingpb.MarkInvoicePaidRequest{
		InvoiceId: "bad",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestBillingGRPC_MarkInvoicePaid_NotFound(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	_, err := svc.MarkInvoicePaid(context.Background(), &billingpb.MarkInvoicePaidRequest{
		InvoiceId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestBillingGRPC_GetInvoice_InvalidID(t *testing.T) {
	t.Parallel()
	repo := newMockBillingRepo()
	svc := NewBillingService(repo, nil, nil)

	_, err := svc.GetInvoice(context.Background(), &billingpb.GetInvoiceRequest{Id: "bad"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestParsePagination_Bounds(t *testing.T) {
	t.Parallel()

	// nil pagination -> defaults.
	size, token := parsePagination(nil)
	assert.Equal(t, billingDefaultPageSize, size)
	assert.Empty(t, token)

	// Over-max is clamped.
	size, _ = parsePagination(&commonpb.Pagination{PageSize: billingMaxPageSize + 500})
	assert.Equal(t, billingMaxPageSize, size)

	// Explicit token + size passed through.
	size, token = parsePagination(&commonpb.Pagination{PageSize: 25, PageToken: "tok"})
	assert.Equal(t, 25, size)
	assert.Equal(t, "tok", token)
}

// -----------------------------------------------------------------------------
// payment_orchestrator_saga.go - failure / validation branches
// -----------------------------------------------------------------------------

// reservedLienSaga creates a real lien via the typed payment_order module (the
// mock current-account client returns a lien_id), then fails on a nonexistent
// handler. This exercises extractLienIDFromSteps + persistPartialLienReservation
// before the payment order is marked FAILED.
const reservedLienSaga = `# Saga: reserved_then_fail
def payment_saga():
    ctx = input_data
    step(name="reserve_funds")
    lien_result = payment_order.create_lien(
        account_id=ctx.get("debtor_account_id"),
        amount_cents=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        payment_order_id=ctx.get("payment_order_id"),
        instrument_code=ctx.get("instrument_code", ""),
        payment_attributes=ctx.get("payment_attributes", {}),
    )

    step(name="send_to_gateway")
    nonexistent.handler()

    return {"lien_id": lien_result.lien_id}

output = payment_saga()
`

func TestExecutePaymentSaga_FailureAfterLien_PersistsReservation(t *testing.T) {
	refClient := NewMockReferenceDataClient()
	refClient.sagaScript = reservedLienSaga

	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.ReferenceDataClient = refClient
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	output, err := orchestrator.ExecutePaymentSaga(context.Background(), po.ID, "payment_execution", po)
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.False(t, output.Success)

	// Payment order should be FAILED after the failure-handling path runs.
	updated, findErr := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
}

func TestOrchestrate_DependenciesNotConfigured_MarksFailed(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t, func(cfg *PaymentOrchestratorConfig) {
		cfg.CurrentAccountClient = nil
		cfg.PaymentGateway = nil
	})

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	// Orchestrate swallows the returned error but still marks the PO failed.
	orchestrator.Orchestrate(context.Background(), po)

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusFailed, updated.Status)
	assert.Equal(t, "INTERNAL_ERROR", updated.ErrorCode)
}

func TestEvaluateBucketID_NoConfig_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	orchestrator, repo, _ := sagaTestOrchestrator(t)

	po := testfixtures.NewPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	// With the default mock reference-data (no bucket config), evaluation should
	// return an empty bucket ID without error.
	bucketID, err := orchestrator.evaluateBucketID(context.Background(), po)
	require.NoError(t, err)
	assert.Empty(t, bucketID)
}

// -----------------------------------------------------------------------------
// grpc_lifecycle.go - Cancel / Reverse / reverse-ledger branches
// -----------------------------------------------------------------------------

func newLifecycleService(t *testing.T, repo *MockRepository, faClient *MockFinancialAccountingClient, caClient *MockCurrentAccountClient) *Service {
	t.Helper()
	gatewayConfig, err := config.NewGatewayAccountConfig(map[string]*config.GatewayAccountMapping{
		"mock": {GatewayID: "mock", ContraAccountID: "CONTRA-123", AccountType: config.AccountTypeNostro},
	})
	require.NoError(t, err)

	svc, err := NewServiceWithConfig(Config{
		Repository:                repo,
		CurrentAccountClient:      caClient,
		FinancialAccountingClient: faClient,
		ReferenceDataClient:       NewMockReferenceDataClient(),
		PaymentGateway:            &MockPaymentGateway{response: gateway.PaymentResponse{Status: gateway.StatusAccepted, GatewayReferenceID: "GW-123"}},
		GatewayAccountConfig:      gatewayConfig,
		IdempotencyService:        NewMockIdempotencyService(),
	})
	require.NoError(t, err)
	return svc
}

// reservedPaymentOrder builds a payment order in RESERVED state (lien held).
func reservedPaymentOrder(t *testing.T) *domain.PaymentOrder {
	t.Helper()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)
	po, err := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "lifecycle-key-"+uuid.New().String(), uuid.New().String())
	require.NoError(t, err)
	require.NoError(t, po.Reserve("lien-"+uuid.New().String()))
	return po
}

func TestCancelPaymentOrder_ReservedReleasesLien(t *testing.T) {
	t.Parallel()
	repo := NewMockRepository()
	caClient := &MockCurrentAccountClient{}
	svc := newLifecycleService(t, repo, &MockFinancialAccountingClient{}, caClient)

	po := reservedPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))

	resp, err := svc.CancelPaymentOrder(context.Background(), &pb.CancelPaymentOrderRequest{
		PaymentOrderId:     po.ID.String(),
		CancellationReason: "customer changed mind",
		CancelledBy:        "ops@example.com",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_CANCELLED, resp.PaymentOrder.Status)
	// A held lien on a RESERVED order must be terminated on cancel.
	assert.Equal(t, 1, caClient.terminateLienCalls)
}

func TestCancelPaymentOrder_UpdateError(t *testing.T) {
	t.Parallel()
	repo := NewMockRepository()
	svc := newLifecycleService(t, repo, &MockFinancialAccountingClient{}, &MockCurrentAccountClient{})

	po := reservedPaymentOrder(t)
	require.NoError(t, repo.Create(context.Background(), po))
	repo.updateErr = errors.New("disk full")

	_, err := svc.CancelPaymentOrder(context.Background(), &pb.CancelPaymentOrderRequest{
		PaymentOrderId:     po.ID.String(),
		CancellationReason: "reason",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// completedPaymentOrderWithLedger builds a COMPLETED payment order with a ledger
// booking and a gateway reference so reversal resolves a contra-account.
func completedPaymentOrderWithLedger(t *testing.T) *domain.PaymentOrder {
	t.Helper()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)
	po, err := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "rev-key-"+uuid.New().String(), uuid.New().String())
	require.NoError(t, err)
	require.NoError(t, po.Reserve("lien-"+uuid.New().String()))
	require.NoError(t, po.Execute("mock-GW-ref-123"))
	require.NoError(t, po.Complete("ledger-booking-"+uuid.New().String()))
	return po
}

func TestReversePaymentOrder_CaptureDebitFailure(t *testing.T) {
	t.Parallel()
	repo := NewMockRepository()
	// Fail only the second CaptureLedgerPosting (the DEBIT) to exercise that branch.
	faClient := &MockFinancialAccountingClient{
		captureErr:       ErrFAServiceUnavailable,
		captureErrOnCall: 2,
	}
	svc := newLifecycleService(t, repo, faClient, &MockCurrentAccountClient{})

	po := completedPaymentOrderWithLedger(t)
	require.NoError(t, repo.Create(context.Background(), po))

	_, err := svc.ReversePaymentOrder(context.Background(), &pb.ReversePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		ReversalReason: "refund",
		ReversedBy:     "ops@example.com",
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "rev-debit-fail"},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	// Both postings attempted before the debit failed.
	assert.Equal(t, 2, faClient.captureCallCount)
}

func TestReversePaymentOrder_FinalizeBookingLogFailure(t *testing.T) {
	t.Parallel()
	repo := NewMockRepository()
	// Postings succeed but the final POSTED status update fails.
	faClient := &MockFinancialAccountingClient{
		updateErr: errors.New("status update failed"),
	}
	svc := newLifecycleService(t, repo, faClient, &MockCurrentAccountClient{})

	po := completedPaymentOrderWithLedger(t)
	require.NoError(t, repo.Create(context.Background(), po))

	_, err := svc.ReversePaymentOrder(context.Background(), &pb.ReversePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		ReversalReason: "refund",
		ReversedBy:     "ops@example.com",
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "rev-finalize-fail"},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.True(t, faClient.updateCalled)
}
