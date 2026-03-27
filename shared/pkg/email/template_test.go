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
	require.ErrorIs(t, err, ErrRendererNotInitialized)
}

func TestRender_EmptyName_ReturnsError(t *testing.T) {
	r := newTestRenderer(t)
	_, _, err := r.Render("", nil)
	require.ErrorIs(t, err, ErrEmptyTemplateName)
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
		require.ErrorIsf(t, err, ErrInvalidDunningSeverity, "expected ErrInvalidDunningSeverity for severity %d", severity)
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

// ---- Verify Email ----

func TestRender_VerifyEmail_ReturnsBothOutputs(t *testing.T) {
	r := newTestRenderer(t)
	data := VerifyEmailData{
		TenantName:       "Acme Platform",
		VerificationLink: "https://app.example.com/verify?token=abc123",
		SupportEmail:     "support@acme.example.com",
	}

	html, text, err := r.Render("verify-email", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Acme Platform")
	assert.Contains(t, html, "https://app.example.com/verify?token=abc123")
	assert.Contains(t, html, "support@acme.example.com")
	assert.Contains(t, html, "24 hours")

	assert.Contains(t, text, "Acme Platform")
	assert.Contains(t, text, "https://app.example.com/verify?token=abc123")
	assert.Contains(t, text, "24 hours")
}

func TestRender_VerifyEmail_HTMLEscaping(t *testing.T) {
	r := newTestRenderer(t)
	data := VerifyEmailData{
		TenantName:       "<script>evil</script>",
		VerificationLink: "https://app.example.com/verify",
		SupportEmail:     "support@example.com",
	}

	html, _, err := r.Render("verify-email", data)
	require.NoError(t, err)
	assert.NotContains(t, html, "<script>")
	assert.Contains(t, html, "&lt;script&gt;")
}

func TestRender_VerifyEmail_EmptyData_DoesNotPanic(t *testing.T) {
	r := newTestRenderer(t)
	_, _, err := r.Render("verify-email", VerifyEmailData{})
	require.NoError(t, err)
}

// ---- Password Reset ----

func TestRender_PasswordReset_ReturnsBothOutputs(t *testing.T) {
	r := newTestRenderer(t)
	data := PasswordResetData{
		TenantName:   "Acme Platform",
		ResetLink:    "https://app.example.com/reset?token=xyz789",
		SupportEmail: "support@acme.example.com",
	}

	html, text, err := r.Render("password-reset", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Acme Platform")
	assert.Contains(t, html, "https://app.example.com/reset?token=xyz789")
	assert.Contains(t, html, "support@acme.example.com")
	assert.Contains(t, html, "1 hour")

	assert.Contains(t, text, "Acme Platform")
	assert.Contains(t, text, "https://app.example.com/reset?token=xyz789")
	assert.Contains(t, text, "1 hour")
}

func TestRender_PasswordReset_HTMLEscaping(t *testing.T) {
	r := newTestRenderer(t)
	data := PasswordResetData{
		TenantName:   "<b>Hacker</b>",
		ResetLink:    "https://app.example.com/reset",
		SupportEmail: "support@example.com",
	}

	html, _, err := r.Render("password-reset", data)
	require.NoError(t, err)
	assert.NotContains(t, html, "<b>Hacker</b>")
	assert.Contains(t, html, "&lt;b&gt;Hacker&lt;/b&gt;")
}

func TestRender_PasswordReset_EmptyData_DoesNotPanic(t *testing.T) {
	r := newTestRenderer(t)
	_, _, err := r.Render("password-reset", PasswordResetData{})
	require.NoError(t, err)
}

// ---- Invite User ----

func TestRender_InviteUser_ReturnsBothOutputs(t *testing.T) {
	r := newTestRenderer(t)
	data := InviteUserData{
		TenantName:   "Acme Platform",
		TenantSlug:   "acme-corp",
		InviterEmail: "admin@acme.example.com",
		AcceptLink:   "https://app.example.com/invite?token=inv456",
		SupportEmail: "support@acme.example.com",
	}

	html, text, err := r.Render("invite-user", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Acme Platform")
	assert.Contains(t, html, "acme-corp")
	assert.Contains(t, html, "admin@acme.example.com")
	assert.Contains(t, html, "https://app.example.com/invite?token=inv456")
	assert.Contains(t, html, "72 hours")

	assert.Contains(t, text, "acme-corp")
	assert.Contains(t, text, "admin@acme.example.com")
	assert.Contains(t, text, "https://app.example.com/invite?token=inv456")
	assert.Contains(t, text, "72 hours")
}

func TestRender_InviteUser_HTMLEscaping(t *testing.T) {
	r := newTestRenderer(t)
	data := InviteUserData{
		TenantName:   "Acme <>&",
		TenantSlug:   "acme-corp",
		InviterEmail: "admin@acme.example.com",
		AcceptLink:   "https://app.example.com/invite",
		SupportEmail: "support@example.com",
	}

	html, _, err := r.Render("invite-user", data)
	require.NoError(t, err)
	assert.NotContains(t, html, "Acme <>&")
	assert.Contains(t, html, "Acme &lt;&gt;&amp;")
}

func TestRender_InviteUser_EmptyData_DoesNotPanic(t *testing.T) {
	r := newTestRenderer(t)
	_, _, err := r.Render("invite-user", InviteUserData{})
	require.NoError(t, err)
}

// ---- Welcome ----

func TestRender_Welcome_ReturnsBothOutputs(t *testing.T) {
	r := newTestRenderer(t)
	data := WelcomeData{
		TenantName:        "Acme Platform",
		LoginURL:          "https://app.example.com/login",
		GettingStartedURL: "https://docs.example.com/getting-started",
	}

	html, text, err := r.Render("welcome", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Acme Platform")
	assert.Contains(t, html, "https://app.example.com/login")
	assert.Contains(t, html, "https://docs.example.com/getting-started")

	assert.Contains(t, text, "Acme Platform")
	assert.Contains(t, text, "https://app.example.com/login")
	assert.Contains(t, text, "https://docs.example.com/getting-started")
}

func TestRender_Welcome_HTMLEscaping(t *testing.T) {
	r := newTestRenderer(t)
	data := WelcomeData{
		TenantName:        "<script>xss</script>",
		LoginURL:          "https://app.example.com/login",
		GettingStartedURL: "https://docs.example.com/getting-started",
	}

	html, _, err := r.Render("welcome", data)
	require.NoError(t, err)
	assert.NotContains(t, html, "<script>")
	assert.Contains(t, html, "&lt;script&gt;")
}

func TestRender_Welcome_EmptyData_DoesNotPanic(t *testing.T) {
	r := newTestRenderer(t)
	_, _, err := r.Render("welcome", WelcomeData{})
	require.NoError(t, err)
}

// ---- Account Lockout ----

func TestRender_AccountLockout_ReturnsBothOutputs(t *testing.T) {
	r := newTestRenderer(t)
	data := AccountLockoutData{
		TenantName:   "Acme Platform",
		SupportEmail: "support@acme.example.com",
		LockoutTime:  "2026-03-27 14:32:00 UTC",
	}

	html, text, err := r.Render("account-lockout", data)
	require.NoError(t, err)

	assert.Contains(t, html, "Acme Platform")
	assert.Contains(t, html, "support@acme.example.com")
	assert.Contains(t, html, "2026-03-27 14:32:00 UTC")
	assert.Contains(t, html, "5 consecutive failed sign-in attempts")

	assert.Contains(t, text, "Acme Platform")
	assert.Contains(t, text, "support@acme.example.com")
	assert.Contains(t, text, "2026-03-27 14:32:00 UTC")
	assert.Contains(t, text, "5 consecutive failed sign-in attempts")
}

func TestRender_AccountLockout_HTMLEscaping(t *testing.T) {
	r := newTestRenderer(t)
	data := AccountLockoutData{
		TenantName:   "Acme <>&",
		SupportEmail: "support@example.com",
		LockoutTime:  "2026-03-27 14:32:00 UTC",
	}

	html, _, err := r.Render("account-lockout", data)
	require.NoError(t, err)
	assert.NotContains(t, html, "Acme <>&")
	assert.Contains(t, html, "Acme &lt;&gt;&amp;")
}

func TestRender_AccountLockout_EmptyData_DoesNotPanic(t *testing.T) {
	r := newTestRenderer(t)
	_, _, err := r.Render("account-lockout", AccountLockoutData{})
	require.NoError(t, err)
}
