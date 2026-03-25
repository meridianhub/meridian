---
name: prd-email-platform
description: End-to-end email capability for Meridian - transactional emails, templates, and integration with existing billing/auth flows
triggers:
  - Setting up email sending infrastructure
  - Implementing password reset or email verification
  - Sending invoices or billing notifications
  - Dunning escalation email delivery
  - Customer notification workflows
instructions: |
  ~34 story points. Start with email service foundation (tasks 1-3),
  then auth flows (4-6), then billing/invoice delivery (7-10),
  then UI integration (11-14). Tasks 1-3 unblock everything else.
  Use Resend as the email provider. Domain: meridianhub.cloud.
---

# Email Platform: Transactional Email for Meridian

## Problem Statement

Meridian has billing infrastructure (invoice generation, dunning escalation,
billing runs) and auth flows (registration, login) but **zero ability to
send emails**. The dunning saga calls `notification.send(type="EMAIL")` -
which silently does nothing. Registration completes without email
verification. There is no password reset flow. Invoices are generated but
never delivered. This gap affects every customer-facing workflow.

## Technical Context

### What Exists

| Capability | Status | Location |
|-----------|--------|----------|
| Invoice generation | Working | `services/payment-order/worker/invoice_generator.go` |
| Billing runs & scheduling | Working | `services/payment-order/worker/billing_scheduler.go` |
| Dunning escalation saga | Working (no email) | `services/reference-data/saga/defaults/dunning_escalation/` |
| Self-service registration | Working (no verification) | `services/api-gateway/registration_handler.go` |
| Password auth + JWT | Working | `services/api-gateway/auth_handler.go` |
| Identity domain (PENDING_INVITE, LOCKED) | Working | `services/identity/domain/identity.go` |
| Webhook notifications | Working | `services/current-account/webhook/notifier.go` |
| Customer/Party management | Working | `shared/domain/models/customer.go`, `services/party/` |
| Frontend registration page | Working (no verify step) | `frontend/src/features/registration/pages/register-page.tsx` |
| Frontend login page | Working (no reset link) | `frontend/src/pages/login.tsx` |
| Billing UI | **Missing** | No pages exist |
| Password reset flow | **Missing** | No backend or frontend |
| Email verification | **Missing** | No backend or frontend |
| Email sending | **Missing** | No SMTP/API integration anywhere |
| Email templates | **Missing** | No template system |

### Infrastructure

- **Email provider**: Resend (resend.com) - Go SDK, 3k emails/mo free, scales to enterprise
- **Sending domain**: `meridianhub.cloud` (DNS records: DKIM, SPF, DMARC)
- **From addresses**: `noreply@meridianhub.cloud`, `billing@meridianhub.cloud`
- **Demo environment**: Docker Compose on DigitalOcean droplet (68.183.40.239)

### Architectural Constraints

1. **Multi-tenant**: Every email must be scoped to a tenant. Templates may be tenant-customizable later.
2. **Audit trail**: All email sends must be logged (who, what, when, delivery status) for compliance.
3. **Idempotency**: Retry-safe. Sending the same invoice email twice must not produce duplicate deliveries.
4. **Saga integration**: Email sending must be available as a Starlark service module so sagas can trigger emails.
5. **CockroachDB**: No LISTEN/NOTIFY. Use outbox pattern for reliable email delivery.

## Solution

### Architecture Overview

```text
Starlark Sagas / Go Services
        |
        v
  Email Outbox Table (CockroachDB)
        |
        v
  Email Worker (polls outbox)
        |
        v
  Template Engine (Go html/template)
        |
        v
  Resend API (HTTPS)
        |
        v
  Delivery Status Callback (webhook)
        |
        v
  Email Audit Log Table
```

### Email Service Design

A new `shared/pkg/email/` package providing:

```go
// Core interface - provider-agnostic
type Sender interface {
    Send(ctx context.Context, msg Message) (deliveryID string, err error)
}

type Message struct {
    To        []string
    From      string          // defaults to noreply@meridianhub.cloud
    Subject   string
    HTML      string          // rendered template
    Text      string          // plain text fallback
    TenantID  string
    IdempotencyKey string     // prevents duplicate sends
    Tags      map[string]string // for tracking: {"type": "invoice", "invoice_id": "INV-001"}
}

// Template rendering
type TemplateRenderer interface {
    Render(templateName string, data any) (html string, text string, err error)
}
```

**Provider implementation**: `shared/pkg/email/resend/` wraps the Resend Go SDK.

**Outbox pattern**: Emails are written to an `email_outbox` table within the same
transaction as the business operation (e.g., invoice creation). A background worker
polls the outbox and sends via Resend. This guarantees exactly-once delivery
semantics even if the service crashes mid-operation.

### Email Templates

Templates live in `shared/pkg/email/templates/` as Go `html/template` files with plain text fallbacks:

| Template | Trigger | Data |
|----------|---------|------|
| `welcome.html` | Registration complete | tenant name, login URL, getting started link |
| `verify-email.html` | Registration / email change | verification link (token-based, 24h expiry) |
| `password-reset.html` | Forgot password request | reset link (token-based, 1h expiry) |
| `invoice.html` | Invoice issued | invoice number, line items, total, due date, payment link |
| `payment-received.html` | Payment confirmed | invoice number, amount, receipt |
| `dunning-notice-1.html` | Dunning level 1 (24h overdue) | invoice number, amount, days overdue, payment link |
| `dunning-notice-2.html` | Dunning level 2 (72h overdue) | invoice number, amount, days overdue, escalation warning |
| `dunning-final-warning.html` | Dunning level 3 (168h overdue) | invoice number, amount, account freeze warning |
| `account-frozen.html` | Account frozen (dunning level 3) | account ID, frozen reason, support contact |
| `invite-user.html` | Admin invites team member | inviter name, tenant name, accept link |

**Design principles**:
- Responsive HTML email (single-column, mobile-first)
- Meridian branding: minimal, professional, no heavy images
- Every HTML email has a plain text fallback
- All links use absolute URLs with the tenant's subdomain
- Unsubscribe link on non-critical emails (marketing/notifications, not auth flows)

### Starlark Service Module

Email becomes available to sagas via a new service module:

```yaml
# In handlers.yaml
notification.send_email:
  params:
    to:
      type: String
      required: true
    template:
      type: String
      required: true
    data:
      type: Map
      required: false
    idempotency_key:
      type: String
      required: true
```

```python
# In dunning_escalation saga (updated)
notification.send_email(
    to=party.email,
    template="dunning-notice-1",
    data={"invoice_number": invoice.number, "amount": invoice.total},
    idempotency_key="dunning-1-" + invoice.id,
)
```

This replaces the current no-op `notification.send(type="EMAIL")` calls.

## Detailed Requirements

### Phase 1: Email Service Foundation (8 points)

**Task 1: Email outbox and audit tables** (3 points)

Create migrations for:

```sql
-- email_outbox: transactional outbox for reliable delivery
CREATE TABLE email_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    to_addresses TEXT[] NOT NULL,
    from_address VARCHAR(255) NOT NULL DEFAULT 'noreply@meridianhub.cloud',
    subject VARCHAR(500) NOT NULL,
    template_name VARCHAR(100) NOT NULL,
    template_data JSONB NOT NULL DEFAULT '{}',
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',  -- PENDING, SENDING, SENT, FAILED
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, idempotency_key)
);

-- email_audit_log: immutable record of all sends
CREATE TABLE email_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    outbox_id UUID NOT NULL REFERENCES email_outbox(id),
    provider_id VARCHAR(255),          -- Resend delivery ID
    to_addresses TEXT[] NOT NULL,
    from_address VARCHAR(255) NOT NULL,
    subject VARCHAR(500) NOT NULL,
    template_name VARCHAR(100) NOT NULL,
    status VARCHAR(20) NOT NULL,       -- DELIVERED, BOUNCED, COMPLAINED, FAILED
    sent_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    provider_response JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

**Task 2: Email service package** (3 points)

`shared/pkg/email/` with:
- `Sender` interface and `Message` type
- `TemplateRenderer` using Go `html/template`
- Resend provider implementation (`shared/pkg/email/resend/`)
- Outbox repository (write to outbox, read pending, update status)
- Configuration: `RESEND_API_KEY`, `EMAIL_FROM_DEFAULT`, `EMAIL_ENABLED` (feature flag)

**Task 3: Email worker** (2 points)

Background worker that:
- Polls `email_outbox` for PENDING/FAILED (where attempts < max_attempts and next_attempt_at <= NOW())
- Renders template, sends via Resend
- Updates outbox status and writes audit log
- Exponential backoff on failure (1min, 5min, 30min)
- Distributed lock to prevent duplicate processing across replicas
- Graceful shutdown on SIGTERM

### Phase 2: Auth Email Flows (8 points)

**Task 4: Email verification on registration** (3 points)

- After registration, identity status stays `PENDING_INVITE` until email verified
- Generate verification token (crypto/rand, 32 bytes, base64url encoded)
- Store token hash in `email_verification_tokens` table (token_hash, identity_id, expires_at)
- Send `verify-email` template with link: `https://{slug}.meridianhub.cloud/verify?token={token}`
- Verification endpoint: `POST /api/v1/verify-email` validates token, sets identity to ACTIVE
- Token expiry: 24 hours. Resend button on verification page.
- Frontend: post-registration redirect to "check your email" page, verification landing page

**Task 5: Password reset flow** (3 points)

- `POST /api/v1/forgot-password` accepts email, always returns 200 (timing-safe)
- If email exists: generate reset token (same pattern as verification), send `password-reset` template
- Reset link: `https://{slug}.meridianhub.cloud/reset-password?token={token}`
- `POST /api/v1/reset-password` validates token, accepts new password, invalidates token
- Token expiry: 1 hour. Single-use (deleted after consumption).
- Rate limit: 3 reset requests per email per hour
- Frontend: "Forgot password?" link on login page, reset form page, success confirmation

**Task 6: User invitation emails** (2 points)

- When admin creates a user (identity with PENDING_INVITE status), send `invite-user` template
- Invitation link goes to a set-password page (similar to reset flow but for new users)
- Token expiry: 7 days
- Frontend: invitation acceptance page with password creation form

### Phase 3: Billing Email Delivery (10 points)

**Task 7: Invoice email template** (2 points)

- Professional invoice template with: tenant logo placeholder, invoice number,
  date, due date, line items table, subtotal/total, payment instructions
- Plain text fallback with formatted table
- PDF attachment support (stretch goal - initially just HTML email)

**Task 8: Invoice delivery integration** (3 points)

- After `invoice_generator.go` creates an invoice, write to email outbox
- Look up party email from party service
- Idempotency key: `invoice-{invoice_id}`
- Only send for ISSUED invoices (not DRAFT)
- Billing run in shadow mode skips email delivery

**Task 9: Dunning email integration** (3 points)

- Wire dunning saga's `notification.send_email` to the email outbox
- Three escalation templates with increasing urgency
- Account freeze notification on level 3
- Idempotency key: `dunning-{level}-{invoice_id}`

**Task 10: Payment confirmation email** (2 points)

- When invoice status transitions to PAID, send `payment-received` template
- Idempotency key: `payment-confirm-{invoice_id}`

### Phase 4: UI Integration (8 points)

**Task 11: Billing dashboard page** (3 points)

- New route: `/billing`
- List view: billing runs with status, period, invoice count, total amount
- Click-through to billing run detail showing all invoices
- Invoice detail: line items, status, payment status, email delivery status

**Task 12: Invoice detail page** (2 points)

- Route: `/billing/invoices/:invoiceId`
- Shows: invoice header, line items, totals, status timeline
- Actions: "Resend email" button, "Mark as paid" (manual override), "Void invoice"
- Email delivery status indicator (sent, delivered, bounced)

**Task 13: Auth flow UI updates** (2 points)

- Post-registration "verify your email" page with resend button
- "Forgot password?" link on login page
- Password reset form page
- Invitation acceptance page
- Success/error states for all flows

**Task 14: User profile page** (1 point)

- Route: `/profile` or `/settings`
- Shows: email, display name, role
- Actions: change password (requires current password)
- Future: email notification preferences

## Email Sending Rules

| Email Type | Can Suppress? | Requires Opt-in? | Rate Limit |
|-----------|--------------|-------------------|------------|
| Email verification | No | No (system) | 3/hour per email |
| Password reset | No | No (system) | 3/hour per email |
| User invitation | No | No (system) | 1/day per recipient |
| Invoice | No | No (contractual) | 1 per invoice |
| Payment confirmation | Yes | No (default on) | 1 per payment |
| Dunning notice | No | No (contractual) | Per escalation schedule |
| Account frozen | No | No (system) | 1 per freeze event |
| Welcome email | Yes | No (default on) | 1 per registration |

## Security Considerations

1. **Token security**: All tokens (verify, reset, invite) stored as SHA-256 hashes. Raw token only exists in the email link.
2. **Timing safety**: Password reset always returns 200 regardless of email existence (prevents email enumeration).
3. **Rate limiting**: All token-generating endpoints rate-limited per IP and per email.
4. **Link expiry**: Verification 24h, reset 1h, invite 7d. Single-use where applicable.
5. **SPF/DKIM/DMARC**: DNS records on meridianhub.cloud for deliverability and anti-spoofing.
6. **No PII in logs**: Email addresses logged only in audit table, not in application logs.
7. **Tenant isolation**: Outbox and audit tables scoped by tenant_id. No cross-tenant email leakage.

## Configuration

```bash
# Required
RESEND_API_KEY=re_xxxxxxxxxxxx

# Optional (defaults shown)
EMAIL_ENABLED=true                              # Feature flag - false disables all sending
EMAIL_FROM_DEFAULT=noreply@meridianhub.cloud    # Default from address
EMAIL_FROM_BILLING=billing@meridianhub.cloud    # Billing-specific from
EMAIL_OUTBOX_POLL_INTERVAL=5s                   # Worker poll frequency
EMAIL_OUTBOX_BATCH_SIZE=50                      # Max emails per poll cycle
EMAIL_BASE_URL=https://meridianhub.cloud        # For link generation
```

## Success Criteria

1. Registration sends verification email; unverified users cannot log in
2. Password reset flow works end-to-end (request, email, reset, login)
3. Invoices delivered via email on billing run completion
4. Dunning emails sent at each escalation level
5. All email sends have audit trail entries
6. Email delivery visible in billing UI (sent/delivered/bounced status)
7. Resend dashboard shows healthy deliverability metrics (>95% delivery rate)
8. Demo environment sends real emails to real addresses

## Dependencies

- Resend account setup and API key provisioned
- DNS records (DKIM, SPF, DMARC) configured on meridianhub.cloud
- `EMAIL_ENABLED=false` in CI/test environments to prevent accidental sends
- Testcontainers-based integration tests use a mock Sender implementation

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Resend rate limits on free tier | Low | Medium | 3k/mo is generous for demo. Upgrade to paid ($20/mo) when needed |
| Email deliverability issues | Medium | High | SPF/DKIM/DMARC from day one. Monitor Resend dashboard |
| Outbox table growth | Low | Low | Add retention policy (delete SENT entries after 90 days) |
| Token brute-force | Low | High | 256-bit tokens, rate limiting, short expiry windows |
| Demo spam abuse | Medium | Medium | Rate limit registration. Consider CAPTCHA if abused |

## Out of Scope (Future)

- Tenant-customizable email templates (brand colors, logos)
- Marketing/newsletter emails
- Email notification preferences UI
- PDF invoice attachments
- Webhook delivery status callbacks from Resend
- Multi-language email templates (i18n)
- In-app notification center (bell icon with email/notification history)
