// Package worker provides the email dispatch worker that polls the outbox table
// and sends emails via the configured Sender. It uses the generic dispatch.Worker
// for the poll loop and delegates email-specific processing to EmailProcessor.
package worker

import (
	"log/slog"

	"github.com/meridianhub/meridian/shared/pkg/dispatch"
	"github.com/meridianhub/meridian/shared/pkg/email"
)

// EmailMetrics wraps email.Metrics for use in the worker package.
type EmailMetrics struct {
	inner *email.Metrics
}

// NewEmailMetrics wraps an email.Metrics for use in the worker package.
func NewEmailMetrics(m *email.Metrics) *EmailMetrics {
	if m == nil {
		return nil
	}
	return &EmailMetrics{inner: m}
}

// ObserveSendDuration records the duration of an email send.
func (m *EmailMetrics) ObserveSendDuration(seconds float64) { m.inner.ObserveSendDuration(seconds) }

// RecordSendError increments the send error counter for a template and error type.
func (m *EmailMetrics) RecordSendError(tmpl, errType string) {
	m.inner.RecordSendError(tmpl, errType)
}

// RecordDeadLetter increments the dead letter counter.
func (m *EmailMetrics) RecordDeadLetter() { m.inner.RecordDeadLetter() }

// RecordCancelled increments the cancellation counter.
func (m *EmailMetrics) RecordCancelled() { m.inner.RecordCancelled() }

// NewEmailWorker creates a dispatch.Worker configured for the email outbox.
func NewEmailWorker(
	outboxRepo email.OutboxRepository,
	auditRepo email.AuditRepository,
	renderer email.TemplateRenderer,
	sender email.Sender,
	suppressionRepo email.SuppressionRepository,
	invoiceChecker InvoiceStatusChecker,
	metrics *email.Metrics,
	config dispatch.WorkerConfig,
	logger *slog.Logger,
) *dispatch.Worker[*OutboxInstruction] {
	fetcher := NewOutboxFetcher(outboxRepo)
	emailMetrics := NewEmailMetrics(metrics)
	processor := NewEmailProcessor(renderer, sender, outboxRepo, auditRepo, suppressionRepo, invoiceChecker, emailMetrics, logger)

	return dispatch.NewWorker[*OutboxInstruction](
		fetcher,
		processor.ProcessBatch,
		config,
		logger,
	)
}
