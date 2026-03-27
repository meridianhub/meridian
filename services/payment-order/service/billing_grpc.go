package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	billingpb "github.com/meridianhub/meridian/api/proto/meridian/billing/v1"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	billingDefaultPageSize = 50
	billingMaxPageSize     = 1000
)

// BillingService implements the BillingService gRPC server.
type BillingService struct {
	billingpb.UnimplementedBillingServiceServer
	billingRepo persistence.BillingRepository
	emailRepo   email.OutboxRepository
	logger      *slog.Logger
}

// NewBillingService creates a new billing gRPC service.
func NewBillingService(billingRepo persistence.BillingRepository, emailRepo email.OutboxRepository, logger *slog.Logger) *BillingService {
	if logger == nil {
		logger = slog.Default()
	}
	return &BillingService{
		billingRepo: billingRepo,
		emailRepo:   emailRepo,
		logger:      logger,
	}
}

func (s *BillingService) ListBillingRuns(ctx context.Context, req *billingpb.ListBillingRunsRequest) (*billingpb.ListBillingRunsResponse, error) {
	pageSize, pageToken := parsePagination(req.GetPagination())

	filter := persistence.BillingRunFilter{
		Statuses: mapProtoBillingRunStatuses(req.GetStatus()),
	}

	page, err := s.billingRepo.ListBillingRuns(ctx, filter, pageSize, pageToken)
	if err != nil {
		if errors.Is(err, persistence.ErrInvalidBillingCursor) {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		s.logger.Error("failed to list billing runs", "error", err)
		return nil, status.Error(codes.Internal, "failed to list billing runs")
	}

	runs := make([]*billingpb.BillingRun, 0, len(page.BillingRuns))
	for _, run := range page.BillingRuns {
		pbRun := billingRunToProto(run)
		// Enrich with invoice summary.
		count, countErr := s.billingRepo.CountInvoicesByBillingRun(ctx, run.ID)
		if countErr != nil {
			s.logger.Error("failed to count invoices", "billing_run_id", run.ID, "error", countErr)
		} else {
			pbRun.InvoiceCount = int32(count)
		}
		total, totalErr := s.billingRepo.SumInvoiceTotalsByBillingRun(ctx, run.ID)
		if totalErr != nil {
			s.logger.Error("failed to sum invoice totals", "billing_run_id", run.ID, "error", totalErr)
		} else {
			pbRun.TotalAmountCents = total
		}
		runs = append(runs, pbRun)
	}

	return &billingpb.ListBillingRunsResponse{
		BillingRuns: runs,
		Pagination: &commonpb.PaginationResponse{
			NextPageToken: page.NextCursor,
			TotalCount:    page.TotalCount,
		},
	}, nil
}

func (s *BillingService) GetBillingRun(ctx context.Context, req *billingpb.GetBillingRunRequest) (*billingpb.GetBillingRunResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid billing run ID: %v", err)
	}

	run, err := s.billingRepo.FindBillingRunByID(ctx, id)
	if err != nil {
		if errors.Is(err, persistence.ErrBillingRunNotFound) {
			return nil, status.Errorf(codes.NotFound, "billing run not found: %s", req.GetId())
		}
		s.logger.Error("failed to get billing run", "error", err)
		return nil, status.Error(codes.Internal, "failed to get billing run")
	}

	pbRun := billingRunToProto(run)

	count, _ := s.billingRepo.CountInvoicesByBillingRun(ctx, id)
	pbRun.InvoiceCount = int32(count)

	total, _ := s.billingRepo.SumInvoiceTotalsByBillingRun(ctx, id)
	pbRun.TotalAmountCents = total

	return &billingpb.GetBillingRunResponse{
		BillingRun: pbRun,
	}, nil
}

func (s *BillingService) ListInvoices(ctx context.Context, req *billingpb.ListInvoicesRequest) (*billingpb.ListInvoicesResponse, error) {
	pageSize, pageToken := parsePagination(req.GetPagination())

	filter := persistence.InvoiceFilter{
		Statuses:     mapProtoInvoiceStatuses(req.GetStatus()),
		PartyID:      req.GetPartyId(),
		BillingRunID: req.GetBillingRunId(),
	}

	page, err := s.billingRepo.ListInvoices(ctx, filter, pageSize, pageToken)
	if err != nil {
		if errors.Is(err, persistence.ErrInvalidBillingCursor) {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		s.logger.Error("failed to list invoices", "error", err)
		return nil, status.Error(codes.Internal, "failed to list invoices")
	}

	invoices := make([]*billingpb.Invoice, 0, len(page.Invoices))
	for _, inv := range page.Invoices {
		invoices = append(invoices, invoiceToProto(inv))
	}

	return &billingpb.ListInvoicesResponse{
		Invoices: invoices,
		Pagination: &commonpb.PaginationResponse{
			NextPageToken: page.NextCursor,
			TotalCount:    page.TotalCount,
		},
	}, nil
}

func (s *BillingService) GetInvoice(ctx context.Context, req *billingpb.GetInvoiceRequest) (*billingpb.GetInvoiceResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid invoice ID: %v", err)
	}

	inv, err := s.billingRepo.FindInvoiceByID(ctx, id)
	if err != nil {
		if errors.Is(err, persistence.ErrInvoiceNotFound) {
			return nil, status.Errorf(codes.NotFound, "invoice not found: %s", req.GetId())
		}
		s.logger.Error("failed to get invoice", "error", err)
		return nil, status.Error(codes.Internal, "failed to get invoice")
	}

	return &billingpb.GetInvoiceResponse{
		Invoice: invoiceToProto(inv),
	}, nil
}

func (s *BillingService) ResendInvoiceEmail(ctx context.Context, req *billingpb.ResendInvoiceEmailRequest) (*billingpb.ResendInvoiceEmailResponse, error) {
	invoiceID, err := uuid.Parse(req.GetInvoiceId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid invoice ID: %v", err)
	}

	inv, err := s.billingRepo.FindInvoiceByID(ctx, invoiceID)
	if err != nil {
		if errors.Is(err, persistence.ErrInvoiceNotFound) {
			return nil, status.Errorf(codes.NotFound, "invoice not found: %s", req.GetInvoiceId())
		}
		s.logger.Error("failed to find invoice for email resend", "error", err)
		return nil, status.Error(codes.Internal, "failed to find invoice")
	}

	if inv.IsTerminal() && inv.Status == domain.InvoiceStatusVoid {
		return nil, status.Error(codes.FailedPrecondition, "cannot resend email for voided invoice")
	}

	idempKey := req.GetIdempotencyKey().GetKey()
	if idempKey == "" {
		idempKey = fmt.Sprintf("invoice-%s-resend-%s", invoiceID, uuid.New().String())
	}

	if s.emailRepo == nil {
		return nil, status.Error(codes.Unavailable, "email delivery not configured")
	}

	entry := &email.OutboxEntry{
		IdempotencyKey: idempKey,
		TemplateName:   "invoice_issued",
		ToAddresses:    []string{}, // Will be resolved by the email worker via party lookup.
		Subject:        fmt.Sprintf("Invoice %s", inv.InvoiceNumber),
		TemplateData: map[string]any{
			"invoice_id":     inv.ID.String(),
			"invoice_number": inv.InvoiceNumber,
			"amount_cents":   inv.SubtotalCents,
			"currency":       inv.Currency,
		},
		MaxAttempts: 5,
	}

	if enqueueErr := s.emailRepo.Enqueue(ctx, entry); enqueueErr != nil {
		if errors.Is(enqueueErr, email.ErrDuplicateIdempotency) {
			s.logger.Info("duplicate email resend request", "idempotency_key", idempKey)
		} else {
			s.logger.Error("failed to enqueue invoice email", "error", enqueueErr)
			return nil, status.Error(codes.Internal, "failed to queue email")
		}
	}

	return &billingpb.ResendInvoiceEmailResponse{
		Email: &billingpb.InvoiceEmail{
			IdempotencyKey: idempKey,
			TemplateName:   "invoice_issued",
			Status:         billingpb.EmailStatus_EMAIL_STATUS_PENDING,
		},
	}, nil
}

func (s *BillingService) MarkInvoicePaid(ctx context.Context, req *billingpb.MarkInvoicePaidRequest) (*billingpb.MarkInvoicePaidResponse, error) {
	invoiceID, err := uuid.Parse(req.GetInvoiceId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid invoice ID: %v", err)
	}

	inv, err := s.billingRepo.FindInvoiceByID(ctx, invoiceID)
	if err != nil {
		if errors.Is(err, persistence.ErrInvoiceNotFound) {
			return nil, status.Errorf(codes.NotFound, "invoice not found: %s", req.GetInvoiceId())
		}
		s.logger.Error("failed to find invoice", "error", err)
		return nil, status.Error(codes.Internal, "failed to find invoice")
	}

	// Idempotent: already paid.
	if inv.Status == domain.InvoiceStatusPaid {
		return &billingpb.MarkInvoicePaidResponse{
			Invoice: invoiceToProto(inv),
		}, nil
	}

	syntheticPOID := uuid.New()
	if markErr := inv.MarkPaid(syntheticPOID); markErr != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot mark invoice as paid: %v", markErr)
	}

	if updateErr := s.billingRepo.UpdateInvoice(ctx, inv); updateErr != nil {
		s.logger.Error("failed to update invoice", "error", updateErr)
		return nil, status.Error(codes.Internal, "failed to update invoice")
	}

	return &billingpb.MarkInvoicePaidResponse{
		Invoice: invoiceToProto(inv),
	}, nil
}

func (s *BillingService) VoidInvoice(ctx context.Context, req *billingpb.VoidInvoiceRequest) (*billingpb.VoidInvoiceResponse, error) {
	invoiceID, err := uuid.Parse(req.GetInvoiceId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid invoice ID: %v", err)
	}

	inv, err := s.billingRepo.FindInvoiceByID(ctx, invoiceID)
	if err != nil {
		if errors.Is(err, persistence.ErrInvoiceNotFound) {
			return nil, status.Errorf(codes.NotFound, "invoice not found: %s", req.GetInvoiceId())
		}
		s.logger.Error("failed to find invoice", "error", err)
		return nil, status.Error(codes.Internal, "failed to find invoice")
	}

	// Idempotent: already voided.
	if inv.Status == domain.InvoiceStatusVoid {
		return &billingpb.VoidInvoiceResponse{
			Invoice: invoiceToProto(inv),
		}, nil
	}

	if voidErr := inv.Void(); voidErr != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot void invoice: %v", voidErr)
	}

	if updateErr := s.billingRepo.UpdateInvoice(ctx, inv); updateErr != nil {
		s.logger.Error("failed to update invoice", "error", updateErr)
		return nil, status.Error(codes.Internal, "failed to update invoice")
	}

	// Cancel pending email deliveries for this invoice.
	if s.emailRepo != nil {
		pattern := "invoice-" + invoiceID.String() + "%"
		cancelled, cancelErr := s.emailRepo.CancelByIdempotencyKeyPattern(ctx, pattern)
		if cancelErr != nil {
			s.logger.Error("failed to cancel pending emails", "invoice_id", invoiceID, "error", cancelErr)
		} else if cancelled > 0 {
			s.logger.Info("cancelled pending emails for voided invoice", "invoice_id", invoiceID, "count", cancelled)
		}
	}

	return &billingpb.VoidInvoiceResponse{
		Invoice: invoiceToProto(inv),
	}, nil
}

func (s *BillingService) ListInvoiceEmails(ctx context.Context, req *billingpb.ListInvoiceEmailsRequest) (*billingpb.ListInvoiceEmailsResponse, error) {
	invoiceID, err := uuid.Parse(req.GetInvoiceId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid invoice ID: %v", err)
	}

	entries, err := s.billingRepo.ListEmailsByInvoice(ctx, invoiceID)
	if err != nil {
		s.logger.Error("failed to list invoice emails", "error", err)
		return nil, status.Error(codes.Internal, "failed to list invoice emails")
	}

	emails := make([]*billingpb.InvoiceEmail, 0, len(entries))
	for _, entry := range entries {
		emails = append(emails, emailAuditToProto(entry))
	}

	return &billingpb.ListInvoiceEmailsResponse{
		Emails: emails,
		Pagination: &commonpb.PaginationResponse{
			TotalCount: int64(len(emails)),
		},
	}, nil
}

func parsePagination(p *commonpb.Pagination) (int, string) {
	pageSize := billingDefaultPageSize
	var pageToken string
	if p != nil {
		if p.PageSize > 0 {
			pageSize = int(p.PageSize)
			if pageSize > billingMaxPageSize {
				pageSize = billingMaxPageSize
			}
		}
		pageToken = p.PageToken
	}
	return pageSize, pageToken
}
