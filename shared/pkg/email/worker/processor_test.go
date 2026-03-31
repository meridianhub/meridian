package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test doubles ---

type mockRenderer struct {
	html string
	text string
	err  error
}

func (m *mockRenderer) Render(_ string, _ any) (string, string, error) {
	return m.html, m.text, m.err
}

type mockSender struct {
	result email.SendResult
	err    error
	calls  int
}

func (m *mockSender) Send(_ context.Context, _ email.Message) (email.SendResult, error) {
	m.calls++
	return m.result, m.err
}

type mockOutboxRepo struct {
	fetchResult     []email.OutboxEntry
	fetchErr        error
	markSentErr     error
	markFailedErr   error
	cancelErr       error
	markSentCalls   int
	markFailedCalls int
	cancelCalls     int
	lastFailedMsg   string
}

func (m *mockOutboxRepo) Enqueue(_ context.Context, _ *email.OutboxEntry) error { return nil }
func (m *mockOutboxRepo) FetchDispatchable(_ context.Context, _ int) ([]email.OutboxEntry, error) {
	return m.fetchResult, m.fetchErr
}

func (m *mockOutboxRepo) MarkSent(_ context.Context, _ uuid.UUID) error {
	m.markSentCalls++
	return m.markSentErr
}

func (m *mockOutboxRepo) MarkFailed(_ context.Context, _ uuid.UUID, errMsg string) error {
	m.markFailedCalls++
	m.lastFailedMsg = errMsg
	return m.markFailedErr
}

func (m *mockOutboxRepo) Cancel(_ context.Context, _ uuid.UUID) error {
	m.cancelCalls++
	return m.cancelErr
}

func (m *mockOutboxRepo) CancelByIdempotencyKeyPattern(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

type mockAuditRepo struct {
	recordCalls int
	recordErr   error
}

func (m *mockAuditRepo) Record(_ context.Context, _ *email.AuditEntry) error {
	m.recordCalls++
	return m.recordErr
}

func (m *mockAuditRepo) FindByOutboxID(_ context.Context, _ uuid.UUID) ([]email.AuditEntry, error) {
	return nil, nil
}

func (m *mockAuditRepo) RecordByProviderID(_ context.Context, _ string, _ email.AuditStatus, _ map[string]any) error {
	return nil
}

type mockInvoiceChecker struct {
	paid bool
	err  error
}

func (m *mockInvoiceChecker) IsInvoicePaid(_ context.Context, _, _ string) (bool, error) {
	return m.paid, m.err
}

type mockSuppressionRepo struct {
	suppressed    bool
	isSuppErr     error
	addCalls      int
	lastAddEntry  *email.SuppressionEntry
	checkedAddrs  []string
}

func (m *mockSuppressionRepo) IsSuppressed(_ context.Context, addr string) (bool, error) {
	m.checkedAddrs = append(m.checkedAddrs, addr)
	return m.suppressed, m.isSuppErr
}

func (m *mockSuppressionRepo) AddSuppression(_ context.Context, entry *email.SuppressionEntry) error {
	m.addCalls++
	m.lastAddEntry = entry
	return nil
}

// --- Helpers ---

func newTestEntry(templateName string) email.OutboxEntry {
	return email.OutboxEntry{
		ID:             uuid.New(),
		TenantID:       "tenant-1",
		IdempotencyKey: "key-1",
		ToAddresses:    []string{"user@example.com"},
		FromAddress:    "noreply@meridianhub.cloud",
		Subject:        "Test",
		TemplateName:   templateName,
		TemplateData:   map[string]any{"CustomerName": "Alice"},
		Status:         email.StatusSending,
		Attempts:       0,
		MaxAttempts:    5,
		NextAttemptAt:  time.Now(),
	}
}

func newTestMetrics(t *testing.T) (*email.Metrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m := email.NewMetricsWithRegistry(reg)
	return m, reg
}

func getMetricValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() == name {
			for _, m := range f.GetMetric() {
				if m.Counter != nil {
					return m.Counter.GetValue()
				}
				if m.Gauge != nil {
					return m.Gauge.GetValue()
				}
				if m.Histogram != nil {
					return float64(m.Histogram.GetSampleCount())
				}
			}
		}
	}
	return 0
}

// --- Tests ---

func TestProcessBatch_HappyPath(t *testing.T) {
	ctx := context.Background()
	m, reg := newTestMetrics(t)

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{result: email.SendResult{ProviderID: "msg-123", SentAt: time.Now()}}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, nil, NewEmailMetrics(m), nil)

	entry := newTestEntry("invoice")
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 1, sender.calls, "sender should be called once")
	assert.Equal(t, 1, outbox.markSentCalls, "outbox should be marked sent")
	assert.Equal(t, 1, audit.recordCalls, "audit should be recorded")
	assert.Equal(t, 0, outbox.markFailedCalls, "should not mark failed")

	// Verify send duration histogram recorded one sample.
	assert.Equal(t, float64(1), getMetricValue(t, reg, "meridian_email_outbox_send_duration_seconds"))
}

func TestProcessBatch_TemplateRenderFailure_DeadLettersImmediately(t *testing.T) {
	ctx := context.Background()
	m, reg := newTestMetrics(t)

	renderer := &mockRenderer{err: errors.New("bad template")}
	sender := &mockSender{}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, nil, NewEmailMetrics(m), nil)

	entry := newTestEntry("invoice")
	entry.Attempts = 0
	entry.MaxAttempts = 5
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 0, sender.calls, "sender should not be called on render failure")
	assert.Equal(t, 1, outbox.markFailedCalls, "should mark as failed")
	assert.Contains(t, outbox.lastFailedMsg, "template render failed")

	// Template render failures are deterministic - should dead-letter immediately.
	assert.Equal(t, float64(1), getMetricValue(t, reg, "meridian_email_outbox_dead_letter_total"),
		"render failure should dead-letter immediately, not retry")
}

func TestProcessBatch_SendFailure_WithRetry(t *testing.T) {
	ctx := context.Background()
	m, reg := newTestMetrics(t)

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{err: errors.New("network error")}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, nil, NewEmailMetrics(m), nil)

	entry := newTestEntry("invoice")
	entry.Attempts = 1
	entry.MaxAttempts = 5
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 1, sender.calls)
	assert.Equal(t, 1, outbox.markFailedCalls, "should mark failed for retry")
	assert.Equal(t, 0, outbox.markSentCalls)
	assert.Contains(t, outbox.lastFailedMsg, "send failed")

	// Dead letter should NOT be recorded (attempts+1=2 < maxAttempts=5).
	assert.Equal(t, float64(0), getMetricValue(t, reg, "meridian_email_outbox_dead_letter_total"))
}

func TestProcessBatch_SendFailure_DeadLetter(t *testing.T) {
	ctx := context.Background()
	m, reg := newTestMetrics(t)

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{err: errors.New("persistent error")}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, nil, NewEmailMetrics(m), nil)

	entry := newTestEntry("invoice")
	entry.Attempts = 4
	entry.MaxAttempts = 5
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 1, outbox.markFailedCalls)

	// Verify dead letter metric (attempts+1=5 >= maxAttempts=5).
	assert.Equal(t, float64(1), getMetricValue(t, reg, "meridian_email_outbox_dead_letter_total"))
}

func TestProcessBatch_DunningCancellation_InvoicePaid(t *testing.T) {
	ctx := context.Background()
	m, reg := newTestMetrics(t)

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}
	checker := &mockInvoiceChecker{paid: true}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, checker, NewEmailMetrics(m), nil)

	entry := newTestEntry("dunning-notice")
	entry.TemplateData = map[string]any{
		"InvoiceNumber": "INV-001",
		"CustomerName":  "Alice",
	}
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 0, sender.calls, "should not send when invoice is paid")
	assert.Equal(t, 1, outbox.cancelCalls, "should cancel the entry")
	assert.Equal(t, 0, outbox.markSentCalls)

	assert.Equal(t, float64(1), getMetricValue(t, reg, "meridian_email_outbox_cancelled_total"))
}

func TestProcessBatch_DunningNotCancelled_InvoiceUnpaid(t *testing.T) {
	ctx := context.Background()

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{result: email.SendResult{ProviderID: "msg-456", SentAt: time.Now()}}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}
	checker := &mockInvoiceChecker{paid: false}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, checker, nil, nil)

	entry := newTestEntry("dunning-notice")
	entry.TemplateData = map[string]any{
		"InvoiceNumber": "INV-002",
		"CustomerName":  "Bob",
	}
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 1, sender.calls, "should send when invoice is unpaid")
	assert.Equal(t, 1, outbox.markSentCalls)
	assert.Equal(t, 0, outbox.cancelCalls)
}

func TestProcessBatch_DunningCancellation_CheckerError(t *testing.T) {
	ctx := context.Background()

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{result: email.SendResult{ProviderID: "msg-789", SentAt: time.Now()}}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}
	checker := &mockInvoiceChecker{err: errors.New("db error")}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, checker, nil, nil)

	entry := newTestEntry("dunning-notice")
	entry.TemplateData = map[string]any{
		"InvoiceNumber": "INV-003",
		"CustomerName":  "Charlie",
	}
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 1, sender.calls, "should send when checker errors")
	assert.Equal(t, 1, outbox.markSentCalls)
	assert.Equal(t, 0, outbox.cancelCalls)
}

func TestProcessBatch_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, nil, nil, nil)

	entry := newTestEntry("invoice")
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 0, sender.calls, "should not process when context is cancelled")
}

func TestProcessBatch_AuditFailureDoesNotBlockSend(t *testing.T) {
	ctx := context.Background()

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{result: email.SendResult{ProviderID: "msg-audit", SentAt: time.Now()}}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{recordErr: errors.New("audit db error")}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, nil, nil, nil)

	entry := newTestEntry("invoice")
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 1, outbox.markSentCalls, "should still mark sent despite audit failure")
	assert.Equal(t, 1, audit.recordCalls, "audit was attempted")
}

func TestProcessBatch_NilInvoiceChecker_DunningProceedsNormally(t *testing.T) {
	ctx := context.Background()

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{result: email.SendResult{ProviderID: "msg-nil", SentAt: time.Now()}}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, nil, nil, nil)

	entry := newTestEntry("dunning-notice")
	entry.TemplateData = map[string]any{
		"InvoiceNumber": "INV-NIL",
		"CustomerName":  "Dave",
	}
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 1, sender.calls, "should send when no invoice checker configured")
	assert.Equal(t, 1, outbox.markSentCalls)
}

func TestProcessBatch_SendErrorMetricsRecorded(t *testing.T) {
	ctx := context.Background()
	m, reg := newTestMetrics(t)

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{err: errors.New("timeout")}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, nil, NewEmailMetrics(m), nil)

	entry := newTestEntry("invoice")
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	// Check send duration was still recorded (even on failure).
	assert.Equal(t, float64(1), getMetricValue(t, reg, "meridian_email_outbox_send_duration_seconds"))

	// Check send_errors_total was recorded with correct labels.
	families, err := reg.Gather()
	require.NoError(t, err)
	var foundError bool
	for _, f := range families {
		if f.GetName() == "meridian_email_outbox_send_errors_total" {
			for _, metric := range f.GetMetric() {
				foundError = true
				assert.Equal(t, float64(1), metric.Counter.GetValue())
				labels := make(map[string]string)
				for _, lp := range metric.GetLabel() {
					labels[lp.GetName()] = lp.GetValue()
				}
				assert.Equal(t, "invoice", labels["template"])
				assert.Equal(t, "send_failed", labels["error_type"])
			}
		}
	}
	assert.True(t, foundError, "send_errors_total metric should exist")
}

func TestProcessBatch_SuppressedRecipient_CancelsEntry(t *testing.T) {
	ctx := context.Background()
	m, reg := newTestMetrics(t)

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}
	suppression := &mockSuppressionRepo{suppressed: true}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, suppression, nil, NewEmailMetrics(m), nil)

	entry := newTestEntry("invoice")
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 0, sender.calls, "should not send to suppressed recipient")
	assert.Equal(t, 1, outbox.cancelCalls, "should cancel the entry")
	assert.Equal(t, 0, outbox.markSentCalls)
	assert.Equal(t, float64(1), getMetricValue(t, reg, "meridian_email_outbox_cancelled_total"))
}

func TestProcessBatch_NotSuppressed_SendsNormally(t *testing.T) {
	ctx := context.Background()

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{result: email.SendResult{ProviderID: "msg-ok", SentAt: time.Now()}}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}
	suppression := &mockSuppressionRepo{suppressed: false}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, suppression, nil, nil, nil)

	entry := newTestEntry("invoice")
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 1, sender.calls, "should send when not suppressed")
	assert.Equal(t, 1, outbox.markSentCalls)
	assert.Equal(t, 0, outbox.cancelCalls)
}

func TestProcessBatch_SuppressionCheckError_ProceedsWithSend(t *testing.T) {
	ctx := context.Background()

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{result: email.SendResult{ProviderID: "msg-err", SentAt: time.Now()}}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}
	suppression := &mockSuppressionRepo{isSuppErr: errors.New("db error")}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, suppression, nil, nil, nil)

	entry := newTestEntry("invoice")
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 1, sender.calls, "should send when suppression check fails")
	assert.Equal(t, 1, outbox.markSentCalls)
}

func TestProcessBatch_NilSuppressionRepo_SkipsCheck(t *testing.T) {
	ctx := context.Background()

	renderer := &mockRenderer{html: "<h1>Hi</h1>", text: "Hi"}
	sender := &mockSender{result: email.SendResult{ProviderID: "msg-nil-supp", SentAt: time.Now()}}
	outbox := &mockOutboxRepo{}
	audit := &mockAuditRepo{}

	proc := NewEmailProcessor(renderer, sender, outbox, audit, nil, nil, nil, nil)

	entry := newTestEntry("invoice")
	instr := &OutboxInstruction{Entry: entry}

	proc.ProcessBatch(ctx, []*OutboxInstruction{instr})

	assert.Equal(t, 1, sender.calls, "should send when no suppression repo configured")
	assert.Equal(t, 1, outbox.markSentCalls)
}
