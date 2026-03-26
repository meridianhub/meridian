package email

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// updateGolden regenerates golden files when run with -update flag.
var updateGolden = flag.Bool("update", false, "update golden files")

func newTestRenderer(t *testing.T) *EmbeddedRenderer {
	t.Helper()
	r, err := NewEmbeddedRenderer()
	require.NoError(t, err)
	return r
}

func assertGolden(t *testing.T, got, name string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		err := os.WriteFile(path, []byte(got), 0o644)
		require.NoError(t, err, "writing golden file")
		return
	}
	expected, err := os.ReadFile(path)
	require.NoError(t, err, "reading golden file %s (run with -update to regenerate)", path)
	assert.Equal(t, string(expected), got)
}

// ---- NewEmbeddedRenderer ----

func TestNewEmbeddedRenderer_Success(t *testing.T) {
	_, err := NewEmbeddedRenderer()
	require.NoError(t, err)
}

// ---- Guard conditions ----

func TestRender_NilRenderer_ReturnsError(t *testing.T) {
	var r *EmbeddedRenderer
	_, _, err := r.Render("invoice", InvoiceData{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestRender_EmptyName_ReturnsError(t *testing.T) {
	r := newTestRenderer(t)
	_, _, err := r.Render("", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template name cannot be empty")
}

// ---- Missing template ----

func TestRender_UnknownTemplate_ReturnsError(t *testing.T) {
	r := newTestRenderer(t)
	_, _, err := r.Render("does-not-exist", nil)
	require.Error(t, err)
}

// ---- Invoice ----

func TestRender_Invoice_ReturnsBothOutputs(t *testing.T) {
	r := newTestRenderer(t)
	data := InvoiceData{
		CustomerName:  "Acme Corp",
		InvoiceNumber: "INV-001",
		LineItems: []LineItem{
			{Description: "Platform Fee", Amount: "£500.00"},
			{Description: "Energy Credits", Amount: "£250.00"},
		},
		Total:       "£750.00",
		DueDate:     "2026-04-01",
		PaymentLink: "https://pay.example.com/INV-001",
	}

	html, text, err := r.Render("invoice", data)
	require.NoError(t, err)

	assert.Contains(t, html, "INV-001")
	assert.Contains(t, html, "Acme Corp")
	assert.Contains(t, html, "£750.00")
	assert.Contains(t, html, "Platform Fee")
	assert.Contains(t, html, "2026-04-01")
	assert.Contains(t, html, "https://pay.example.com/INV-001")

	assert.Contains(t, text, "INV-001")
	assert.Contains(t, text, "Acme Corp")
	assert.Contains(t, text, "£750.00")
	assert.Contains(t, text, "Platform Fee")
}

func TestRender_Invoice_HTMLEscaping(t *testing.T) {
	r := newTestRenderer(t)
	data := InvoiceData{
		CustomerName:  "<script>alert('xss')</script>",
		InvoiceNumber: "INV-XSS",
		Total:         "£0.00",
		DueDate:       "2026-04-01",
		PaymentLink:   "https://pay.example.com/INV-XSS",
	}

	html, _, err := r.Render("invoice", data)
	require.NoError(t, err)
	assert.NotContains(t, html, "<script>")
	assert.Contains(t, html, "&lt;script&gt;")
}

func TestRender_Invoice_GoldenHTML(t *testing.T) {
	r := newTestRenderer(t)
	data := InvoiceData{
		CustomerName:  "Golden Corp",
		InvoiceNumber: "INV-GOLDEN",
		LineItems: []LineItem{
			{Description: "Monthly Subscription", Amount: "£1,000.00"},
		},
		Total:       "£1,000.00",
		DueDate:     "2026-05-01",
		PaymentLink: "https://pay.example.com/INV-GOLDEN",
	}
	html, text, err := r.Render("invoice", data)
	require.NoError(t, err)
	assertGolden(t, html, "invoice.html")
	assertGolden(t, text, "invoice.txt")
}

// ---- Dunning Notice ----

func TestRender_DunningNotice_InvalidSeverity_ReturnsError(t *testing.T) {
	r := newTestRenderer(t)
	for _, severity := range []int{0, 4, -1} {
		data := DunningNoticeData{
			CustomerName:   "Test",
			InvoiceNumber:  "INV-ERR",
			Amount:         "£100.00",
			DaysOverdue:    5,
			Severity:       severity,
			SupportContact: "support@meridian.io",
		}
		_, _, err := r.Render("dunning-notice", data)
		require.Errorf(t, err, "expected error for severity %d", severity)
		assert.Contains(t, err.Error(), "Severity")
	}
}

func TestRender_DunningNotice_Severity1(t *testing.T) {
	r := newTestRenderer(t)
	data := DunningNoticeData{
		CustomerName:   "Gentle Customer",
		InvoiceNumber:  "INV-100",
		Amount:         "£200.00",
		DaysOverdue:    5,
		Severity:       1,
		SupportContact: "support@meridian.io",
	}

	html, text, err := r.Render("dunning-notice", data)
	require.NoError(t, err)

	assert.Contains(t, html, "friendly reminder")
	assert.Contains(t, html, "INV-100")
	assert.Contains(t, html, "5")
	assert.Contains(t, text, "friendly reminder")
	assert.NotContains(t, html, "FINAL")
	assert.NotContains(t, html, "suspension imminent")
}

func TestRender_DunningNotice_Severity2(t *testing.T) {
	r := newTestRenderer(t)
	data := DunningNoticeData{
		CustomerName:   "Late Customer",
		InvoiceNumber:  "INV-200",
		Amount:         "£500.00",
		DaysOverdue:    30,
		Severity:       2,
		SupportContact: "support@meridian.io",
	}

	html, text, err := r.Render("dunning-notice", data)
	require.NoError(t, err)

	assert.Contains(t, html, "immediate attention")
	assert.Contains(t, html, "INV-200")
	assert.Contains(t, text, "immediate attention")
	assert.NotContains(t, html, "friendly reminder")
	assert.NotContains(t, html, "FINAL")
}

func TestRender_DunningNotice_Severity3(t *testing.T) {
	r := newTestRenderer(t)
	data := DunningNoticeData{
		CustomerName:   "Delinquent Customer",
		InvoiceNumber:  "INV-300",
		Amount:         "£1,000.00",
		DaysOverdue:    60,
		Severity:       3,
		SupportContact: "support@meridian.io",
	}

	html, text, err := r.Render("dunning-notice", data)
	require.NoError(t, err)

	assert.Contains(t, html, "frozen")
	assert.Contains(t, html, "INV-300")
	assert.Contains(t, text, "FINAL NOTICE")
	assert.NotContains(t, html, "friendly reminder")
	assert.NotContains(t, html, "immediate attention")
}

func TestRender_DunningNotice_GoldenAllSeverities(t *testing.T) {
	r := newTestRenderer(t)

	for _, tc := range []struct {
		severity int
		suffix   string
	}{
		{1, "s1"},
		{2, "s2"},
		{3, "s3"},
	} {
		data := DunningNoticeData{
			CustomerName:   "Golden Customer",
			InvoiceNumber:  "INV-GOLDEN",
			Amount:         "£500.00",
			DaysOverdue:    15 * tc.severity,
			Severity:       tc.severity,
			SupportContact: "support@meridian.io",
		}
		html, text, err := r.Render("dunning-notice", data)
		require.NoError(t, err)
		assertGolden(t, html, "dunning-notice-"+tc.suffix+".html")
		assertGolden(t, text, "dunning-notice-"+tc.suffix+".txt")
	}
}

// ---- Payment Received ----

func TestRender_PaymentReceived_ReturnsBothOutputs(t *testing.T) {
	r := newTestRenderer(t)
	data := PaymentReceivedData{
		CustomerName:  "Happy Customer",
		InvoiceNumber: "INV-PAID",
		Amount:        "£750.00",
		PaymentDate:   "2026-03-26",
		ReceiptNumber: "REC-001",
	}

	html, text, err := r.Render("payment-received", data)
	require.NoError(t, err)

	assert.Contains(t, html, "REC-001")
	assert.Contains(t, html, "INV-PAID")
	assert.Contains(t, html, "£750.00")
	assert.Contains(t, html, "2026-03-26")
	assert.Contains(t, html, "Happy Customer")

	assert.Contains(t, text, "REC-001")
	assert.Contains(t, text, "INV-PAID")
}

func TestRender_PaymentReceived_HTMLEscaping(t *testing.T) {
	r := newTestRenderer(t)
	data := PaymentReceivedData{
		CustomerName:  "<b>Hacker</b>",
		InvoiceNumber: "INV-ESC",
		Amount:        "£0.00",
		PaymentDate:   "2026-03-26",
		ReceiptNumber: "REC-ESC",
	}

	html, _, err := r.Render("payment-received", data)
	require.NoError(t, err)
	assert.NotContains(t, html, "<b>Hacker</b>")
	assert.Contains(t, html, "&lt;b&gt;Hacker&lt;/b&gt;")
}

func TestRender_PaymentReceived_Golden(t *testing.T) {
	r := newTestRenderer(t)
	data := PaymentReceivedData{
		CustomerName:  "Golden Customer",
		InvoiceNumber: "INV-GOLDEN",
		Amount:        "£1,000.00",
		PaymentDate:   "2026-03-26",
		ReceiptNumber: "REC-GOLDEN",
	}
	html, text, err := r.Render("payment-received", data)
	require.NoError(t, err)
	assertGolden(t, html, "payment-received.html")
	assertGolden(t, text, "payment-received.txt")
}

// ---- Account Frozen ----

func TestRender_AccountFrozen_ReturnsBothOutputs(t *testing.T) {
	r := newTestRenderer(t)
	data := AccountFrozenData{
		CustomerName:   "Locked Customer",
		AccountID:      "ACC-001",
		FrozenReason:   "Outstanding balance exceeds credit limit",
		SupportContact: "support@meridian.io",
	}

	html, text, err := r.Render("account-frozen", data)
	require.NoError(t, err)

	assert.Contains(t, html, "ACC-001")
	assert.Contains(t, html, "Locked Customer")
	assert.Contains(t, html, "Outstanding balance exceeds credit limit")
	assert.Contains(t, html, "support@meridian.io")

	assert.Contains(t, text, "ACC-001")
	assert.Contains(t, text, "Outstanding balance exceeds credit limit")
}

func TestRender_AccountFrozen_HTMLEscaping(t *testing.T) {
	r := newTestRenderer(t)
	data := AccountFrozenData{
		CustomerName:   "Customer <>&",
		AccountID:      "ACC-ESC",
		FrozenReason:   "<script>bad</script>",
		SupportContact: "support@meridian.io",
	}

	html, _, err := r.Render("account-frozen", data)
	require.NoError(t, err)
	assert.NotContains(t, html, "<script>")
	assert.True(t, strings.Contains(html, "&lt;script&gt;") || strings.Contains(html, "&#34;"))
}

func TestRender_AccountFrozen_Golden(t *testing.T) {
	r := newTestRenderer(t)
	data := AccountFrozenData{
		CustomerName:   "Golden Customer",
		AccountID:      "ACC-GOLDEN",
		FrozenReason:   "Non-payment of outstanding invoices",
		SupportContact: "support@meridian.io",
	}
	html, text, err := r.Render("account-frozen", data)
	require.NoError(t, err)
	assertGolden(t, html, "account-frozen.html")
	assertGolden(t, text, "account-frozen.txt")
}
