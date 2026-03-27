package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/email"
)

// InvoiceStatusChecker checks whether a dunning-related invoice has already been paid.
type InvoiceStatusChecker interface {
	IsInvoicePaid(ctx context.Context, tenantID string, invoiceNumber string) (bool, error)
}

// EmailProcessor handles the per-entry processing logic for the email dispatch worker.
// It renders templates, sends emails, and updates outbox/audit state.
type EmailProcessor struct {
	renderer       email.TemplateRenderer
	sender         email.Sender
	outboxRepo     email.OutboxRepository
	auditRepo      email.AuditRepository
	invoiceChecker InvoiceStatusChecker
	metrics        *EmailMetrics
	logger         *slog.Logger
}

// NewEmailProcessor creates an EmailProcessor with the given dependencies.
func NewEmailProcessor(
	renderer email.TemplateRenderer,
	sender email.Sender,
	outboxRepo email.OutboxRepository,
	auditRepo email.AuditRepository,
	invoiceChecker InvoiceStatusChecker,
	metrics *EmailMetrics,
	logger *slog.Logger,
) *EmailProcessor {
	if logger == nil {
		logger = slog.Default()
	}
	return &EmailProcessor{
		renderer:       renderer,
		sender:         sender,
		outboxRepo:     outboxRepo,
		auditRepo:      auditRepo,
		invoiceChecker: invoiceChecker,
		metrics:        metrics,
		logger:         logger.With("component", "email-processor"),
	}
}

// ProcessBatch processes a batch of outbox instructions. This is the BatchProcessor
// callback wired into dispatch.Worker.
func (p *EmailProcessor) ProcessBatch(ctx context.Context, instructions []*OutboxInstruction) {
	for _, instr := range instructions {
		if ctx.Err() != nil {
			return
		}
		if err := p.processOne(ctx, instr); err != nil {
			p.logger.ErrorContext(ctx, "failed to process email",
				"outbox_id", instr.Entry.ID,
				"template", instr.Entry.TemplateName,
				"error", err,
			)
		}
	}
}

// processOne handles a single outbox entry through the full lifecycle:
// check cancellation conditions, render template, send, update outbox, create audit.
func (p *EmailProcessor) processOne(ctx context.Context, instr *OutboxInstruction) error {
	entry := &instr.Entry

	// Check if dunning notice should be cancelled (invoice already paid).
	if entry.TemplateName == "dunning-notice" {
		cancelled, err := p.checkDunningCancellation(ctx, entry)
		if err != nil {
			return fmt.Errorf("checking dunning cancellation: %w", err)
		}
		if cancelled {
			return nil
		}
	}

	// Render the template. Render errors are deterministic (same template+data will
	// always fail), so we set attempts to max to dead-letter immediately rather than
	// burning through retries.
	htmlBody, textBody, err := p.renderer.Render(entry.TemplateName, entry.TemplateData)
	if err != nil {
		entry.Attempts = entry.MaxAttempts - 1 // next MarkFailed will dead-letter
		return p.handleSendFailure(ctx, entry, fmt.Sprintf("template render failed: %v", err))
	}

	// Build and send message.
	msg := email.Message{
		To:             entry.ToAddresses,
		From:           entry.FromAddress,
		Subject:        entry.Subject,
		HTMLBody:       htmlBody,
		TextBody:       textBody,
		IdempotencyKey: entry.IdempotencyKey,
	}

	start := time.Now()
	result, err := p.sender.Send(ctx, msg)
	duration := time.Since(start)

	if p.metrics != nil {
		p.metrics.ObserveSendDuration(duration.Seconds())
	}

	if err != nil {
		if p.metrics != nil {
			p.metrics.RecordSendError(entry.TemplateName, "send_failed")
		}
		return p.handleSendFailure(ctx, entry, fmt.Sprintf("send failed: %v", err))
	}

	// Mark sent in outbox.
	if err := p.outboxRepo.MarkSent(ctx, entry.ID); err != nil {
		return fmt.Errorf("marking outbox entry sent: %w", err)
	}

	// Create audit entry.
	sentAt := result.SentAt
	providerID := result.ProviderID
	auditEntry := &email.AuditEntry{
		TenantID:     entry.TenantID,
		OutboxID:     entry.ID,
		ProviderID:   &providerID,
		ToAddresses:  entry.ToAddresses,
		FromAddress:  entry.FromAddress,
		Subject:      entry.Subject,
		TemplateName: entry.TemplateName,
		Status:       email.AuditStatusSent,
		SentAt:       &sentAt,
	}
	if err := p.auditRepo.Record(ctx, auditEntry); err != nil {
		// Log but don't fail - the email was sent successfully.
		p.logger.WarnContext(ctx, "failed to record audit entry",
			"outbox_id", entry.ID,
			"error", err,
		)
	}

	p.logger.InfoContext(ctx, "email sent",
		"outbox_id", entry.ID,
		"template", entry.TemplateName,
		"provider_id", result.ProviderID,
		"duration_ms", duration.Milliseconds(),
	)
	return nil
}

// checkDunningCancellation checks if the invoice for a dunning notice has been paid.
// Returns true if the entry was cancelled.
func (p *EmailProcessor) checkDunningCancellation(ctx context.Context, entry *email.OutboxEntry) (bool, error) {
	if p.invoiceChecker == nil {
		return false, nil
	}

	invoiceNumber, _ := entry.TemplateData["InvoiceNumber"].(string)
	if invoiceNumber == "" {
		return false, nil
	}

	paid, err := p.invoiceChecker.IsInvoicePaid(ctx, entry.TenantID, invoiceNumber)
	if err != nil {
		// Log but don't cancel - send the dunning notice if we can't check.
		p.logger.WarnContext(ctx, "failed to check invoice status, proceeding with send",
			"outbox_id", entry.ID,
			"invoice_number", invoiceNumber,
			"error", err,
		)
		return false, nil
	}

	if paid {
		if err := p.outboxRepo.Cancel(ctx, entry.ID); err != nil {
			return false, fmt.Errorf("cancelling dunning notice for paid invoice: %w", err)
		}
		if p.metrics != nil {
			p.metrics.RecordCancelled()
		}
		p.logger.InfoContext(ctx, "cancelled dunning notice - invoice already paid",
			"outbox_id", entry.ID,
			"invoice_number", invoiceNumber,
		)
		return true, nil
	}

	return false, nil
}

// handleSendFailure marks the outbox entry as failed, which triggers backoff/dead-letter
// logic in the repository. Records appropriate metrics.
func (p *EmailProcessor) handleSendFailure(ctx context.Context, entry *email.OutboxEntry, errMsg string) error {
	if err := p.outboxRepo.MarkFailed(ctx, entry.ID, errMsg); err != nil {
		return fmt.Errorf("marking outbox entry failed: %w", err)
	}

	// Check if this failure exhausted retries (entry will be dead-lettered by repo).
	if entry.Attempts+1 >= entry.MaxAttempts {
		if p.metrics != nil {
			p.metrics.RecordDeadLetter()
		}
		p.logger.WarnContext(ctx, "email dead-lettered after max attempts",
			"outbox_id", entry.ID,
			"template", entry.TemplateName,
			"attempts", entry.Attempts+1,
		)
	}

	return nil
}
