---
name: prd-email-platform
description: Email infrastructure MVP for Meridian - outbox, worker, Resend integration, invoice/dunning delivery
triggers:
  - Setting up email sending infrastructure
  - Sending invoices or billing notifications
  - Dunning escalation email delivery
  - Wiring the notification.send saga handler
instructions: |
  ~21 story points. Single-phase delivery: outbox + worker + Resend +
  templates + webhooks + billing wiring. Reuse dispatch.Worker[I],
  events/metrics.go, circuitbreaker.go, and shared/pkg/tokens.
  Host worker in unified binary. Domain: meridianhub.cloud.
  Auth flows (verification, password reset) are PRD 053.
  Billing UI (dashboard, invoice detail) is PRD 054.
---

# Email Infrastructure MVP

## Problem Statement

Meridian has billing infrastructure (invoice generation, dunning escalation,
billing runs) but zero ability to send emails. The dunning saga calls
`notification.send(type="EMAIL")` which returns a `stubNotImplemented`
error. Invoices are generated but never delivered to customers. This gap
means:

- Customers never receive invoices
- Dunning escalation cannot notify before freezing accounts
- Payment confirmations are silent
- The demo cannot show a complete billing lifecycle
- Email is a platform cost with no tenant attribution or metering

This PRD focuses on the **email infrastructure and billing email delivery**.
Auth email flows (verification, password reset, invitations) and billing UI
are separate PRDs (053, 054) that build on this foundation.

### Cost Attribution: Email as a Metered Platform Resource

Email sending has a direct cost (Resend charges per email). As a
multi-tenant platform, Meridian must attribute this cost to tenants.
The outbox table is the natural metering record - every email has
`tenant_id`, `template_name`, `status`, and `created_at`.

Platform-level email billing becomes a saga that queries the outbox:

```sql
SELECT tenant_id, COUNT(*) as emails_sent
FROM email_outbox
WHERE status = 'SENT'
  AND created_at BETWEEN $period_start AND $period_end
GROUP BY tenant_id;
```

This feeds into Meridian's own billing infrastructure - the platform
bills tenants for email usage using the same invoice generation and
dunning escalation that tenants use for their customers. The audit log
(7-year retention) provides the dispute-proof record.

This is the "Kubernetes for economies" thesis in practice: the platform
operates its own commercial model using the same primitives it offers
tenants. Any tenant-defined saga can include `notification.send` steps,
and the platform meters every send.

## Technical Context

### What Exists

| Capability | Status | Location |
|-----------|--------|----------|
| Invoice generation | Working | `services/payment-order/worker/invoice_generator.go` |
| Billing runs and scheduling | Working | `services/payment-order/worker/billing_scheduler.go` |
| Dunning escalation saga | Working (errors on email) | `services/reference-data/saga/defaults/dunning_escalation/` |
| Dunning cancellation | Working | `CancelDunningRetry()` in dunning worker |
| Customer/Party management | Working | `shared/domain/models/customer.go`, `services/party/` |
| Webhook notifications | Working | `services/current-account/webhook/notifier.go` |

### Existing Infrastructure to Reuse

| Component | Location | How It Applies |
|-----------|----------|----------------|
| `dispatch.Worker[I]` | `shared/pkg/dispatch/` | Generic poll-dispatch-ack worker with batching, `FOR UPDATE SKIP LOCKED`, circuit breaker, retry policy, CANCELLED status, graceful shutdown |
| Event outbox pattern | `shared/platform/events/outbox.go` | `FetchAndLockForProcessing`, atomic retry, stuck-entry recovery |
| Prometheus metrics | `shared/platform/events/metrics.go` | 7 metrics (depth, latency, DLQ, retries) - adapt with `s/event_outbox/email/` |
| Circuit breaker | `shared/pkg/clients/circuitbreaker.go` | Wraps `sony/gobreaker/v2`, used by 114+ files |
| Worker lifecycle | `shared/platform/scheduler/lifecycle.go` | Start/stop, graceful shutdown, context cancellation |
| Token package | `shared/pkg/tokens/` | `GenerateToken`, `HashToken`, `ValidateTokenHash` with constant-time comparison |

**Key insight**: The email worker is `dispatch.Worker[EmailOutboxRow]` with a
`BatchProcessor` that renders templates and calls Resend. The polling,
shutdown, batching, retry scheduling, and circuit breaking are handled by
existing infrastructure. The net-new Go code is ~700 lines + templates.

### Infrastructure

- **Email provider**: Resend (resend.com) - Go SDK v3, 3k emails/mo free
- **Sending domain**: `meridianhub.cloud` (DNS: DKIM, SPF, DMARC)
- **From addresses**: `noreply@meridianhub.cloud`, `billing@meridianhub.cloud`
- **Migration directory**: `services/notification/migrations/`
- **Worker hosting**: `cmd/meridian/main.go` (unified binary, alongside existing workers)

### Architectural Constraints

1. **Multi-tenant**: Outbox and audit tables scoped by `tenant_id`.
2. **Audit trail**: Immutable audit log records every send attempt and
   delivery status.
3. **Idempotency**: At-least-once delivery from Meridian, deduplicated at
   Resend via idempotency key header.
4. **Saga integration**: Email available via existing `notification.send`
   handler with real implementation replacing the stub.
5. **CockroachDB**: No LISTEN/NOTIFY. Outbox pattern with
   `SELECT FOR UPDATE SKIP LOCKED`.
6. **Template safety**: Go `html/template` only (auto-escapes by context,
   template injection structurally impossible). Never use `text/template`
7. **Provider-agnostic (ports and adapters)**: The `Sender` interface is
   the port. Resend is the first adapter. The outbox, worker, templates,
   audit log, and metrics never touch the provider directly - only the
   worker's `BatchProcessor` calls `Sender.Send()`. Swapping to SendGrid,
   AWS SES, Postmark, or self-hosted SMTP means implementing one interface
   in one file. No changes to sagas, templates, outbox, or audit trail.
   This also enables per-tenant provider routing in future (e.g., a tenant
   on a regulated network that requires on-premise SMTP).
   for email rendering.

## Solution

### Architecture Overview

```text
Starlark Sagas / Go Services
        |
        v
  Email Outbox Table (CockroachDB)
        |
        v
  dispatch.Worker[EmailOutboxRow]
        |
        v
  Template Engine (Go html/template)
        |
        v
  Resend API (HTTPS, circuit breaker)
        |
        v
  Resend Webhook -> Audit Log (delivery/bounce status)
```

### Email Service Design

New `shared/pkg/email/` package:

```go
// Core interface - provider-agnostic
type Sender interface {
    Send(ctx context.Context, msg Message) (deliveryID string, err error)
}

type Message struct {
    To             []string
    From           string            // defaults to noreply@meridianhub.cloud
    Subject        string
    HTML           string            // rendered template
    Text           string            // plain text fallback
    TenantID       string
    IdempotencyKey string            // forwarded as Resend Idempotency-Key header
    Tags           map[string]string // for tracking: {"type": "invoice"}
}

// Template rendering
type TemplateRenderer interface {
    Render(name string, data any) (html string, text string, err error)
}
```

**Three Sender implementations**:
- `resend.Sender` - production, wraps Resend Go SDK with circuit breaker
- `log.Sender` - writes to application log, no API call (for `EMAIL_MODE=log`)
- `noop.Sender` - discards silently (for `EMAIL_MODE=disabled` and tests)

### Outbox Design

The outbox pattern guarantees emails survive crashes: the outbox row is
written in the same database transaction as the business operation (e.g.,
invoice creation). The worker polls independently and delivers via Resend.

**Delivery guarantee**: At-least-once from Meridian. The idempotency key is
forwarded as Resend's `Idempotency-Key` header, so Resend deduplicates on
their side. This is honest: the worker can crash after sending but before
updating status, causing a resend on restart. Resend's deduplication
prevents the customer from receiving duplicates.

**Locking**: `SELECT FOR UPDATE SKIP LOCKED` via `dispatch.Worker[I]` -
the established pattern throughout the codebase. No Redis, no distributed
lock, no new infrastructure.

**Retry policy**: 5 attempts with exponential backoff: 1min, 15min, 1h, 4h,
24h. Total retry window ~29 hours. After exhaustion, status transitions to
`DEAD_LETTER`. Circuit breaker (via `circuitbreaker.go`) prevents retry
exhaustion during provider outages by failing fast when Resend is down.

**Statuses**: `PENDING` -> `SENDING` -> `SENT` | `FAILED` (retryable) |
`DEAD_LETTER` (exhausted) | `CANCELLED` (superseded by business event).

### Email Templates

Four templates using Go `html/template` with `embed.FS`, stored in
`shared/pkg/email/templates/`:

| Template | Trigger | Data |
|----------|---------|------|
| `invoice.html` | Invoice issued | invoice number, line items, total, due date, payment link |
| `dunning-notice.html` | Dunning escalation (parameterized by severity 1/2/3) | invoice number, amount, days overdue, severity-specific copy and action |
| `payment-received.html` | Payment confirmed | invoice number, amount, receipt |
| `account-frozen.html` | Account frozen (dunning level 3) | account ID, frozen reason, support contact |

**Design principles**:
- Single dunning template with severity parameter (reduces duplication,
  ensures brand consistency across escalation levels)
- Responsive HTML (single-column, mobile-first)
- Meridian branding: minimal, professional, no heavy images
- Every HTML email has a plain text fallback
- All links use absolute URLs with the tenant's subdomain

Auth templates (verify-email, password-reset, invite-user, welcome) ship
with PRD 053.

### Saga Integration

The existing `notification.send` handler stub is replaced with a real
implementation. No new handler name required - existing saga scripts work
unchanged:

```yaml
# handlers.yaml - existing handler, real implementation
notification.send:
  params:
    type:
      type: String
      required: true       # "EMAIL" (future: "WEBHOOK", "SMS")
    to:
      type: String
      required: false      # email address, or omit to resolve from party_id
    party_id:
      type: String
      required: false      # resolved server-side to party's email
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
# Existing dunning_escalation saga - no changes needed
notification.send(
    type="EMAIL",
    party_id=party.id,
    template="dunning-notice",
    data={"invoice_number": invoice.number, "amount": invoice.total,
          "severity": 1},
    idempotency_key="dunning-1-" + invoice.id,
)
```

Using `party_id` instead of raw email addresses closes the
arbitrary-recipient vector - the handler resolves the address server-side
from a known party within the tenant.

### Dunning Safety: Pre-Send Validation and Cancellation

Two mechanisms prevent the dunning-after-payment race condition:

**1. Payment cancels pending dunning emails.** When payment confirmation
marks an invoice as PAID, it also cancels pending dunning outbox rows:

```sql
UPDATE email_outbox
SET status = 'CANCELLED', updated_at = NOW()
WHERE idempotency_key LIKE 'dunning-%'
  AND template_data->>'invoice_id' = $1
  AND status = 'PENDING'
  AND tenant_id = $2;
```

**2. Worker validates invoice status before sending dunning emails.** The
worker's `BatchProcessor` checks: if the template is `dunning-notice`,
query invoice status. If PAID, set outbox row to CANCELLED, skip. This
catches the narrow race window between payment confirmation and outbox poll.

**3. Dunning delivery guard.** The dunning saga checks prior-level email
status before escalating. If the prior notification is `DEAD_LETTER` (never
delivered), pause escalation and flag for manual review instead of silently
freezing the account.

### Resend Webhook Handler

`POST /api/v1/webhooks/resend` receives delivery status callbacks:

- Verifies `Svix-Signature` header (Resend uses Svix for webhook signing)
- Updates `email_audit_log` status: `DELIVERED`, `BOUNCED`, `COMPLAINED`
- Populates `delivered_at` timestamp on successful delivery
- Logs bounce reasons for operational debugging

Without this, the audit log permanently shows `SENT` for every email -
including bounced ones. This is the difference between "we sent it" and "we
know it arrived."

### Observability

Adapted from `shared/platform/events/metrics.go` (rename subsystem):

| Metric | Type | Description |
|--------|------|-------------|
| `email_outbox_pending_total` | Gauge | Current PENDING + FAILED rows |
| `email_outbox_send_duration_seconds` | Histogram | Resend API call latency |
| `email_outbox_send_errors_total` | Counter | Failed send attempts |
| `email_outbox_dead_letter_total` | Counter | Emails that exhausted retries |
| `email_outbox_cancelled_total` | Counter | Emails cancelled (e.g., payment received) |
| `email_circuit_breaker_state` | Gauge | 0=closed, 1=half-open, 2=open |

**Alerting rules** (Prometheus/Alertmanager):
- `email_outbox_pending_total > 100` for 5 minutes -> warn (backlog)
- `email_outbox_dead_letter_total` increase > 0 -> alert (permanent failure)
- `email_circuit_breaker_state == 2` for 5 minutes -> alert (Resend down)

## Detailed Requirements

### Task 1: Email outbox and audit tables (2 points)

Migrations in `services/notification/migrations/`:

```sql
CREATE TABLE email_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id VARCHAR(255) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    to_addresses TEXT[] NOT NULL,
    from_address VARCHAR(255) NOT NULL
        DEFAULT 'noreply@meridianhub.cloud',
    subject VARCHAR(500) NOT NULL,
    template_name VARCHAR(100) NOT NULL,
    template_data JSONB NOT NULL DEFAULT '{}',
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT,
    cancelled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, idempotency_key)
);

-- Partial index: worker only queries actionable rows
CREATE INDEX idx_email_outbox_pending
    ON email_outbox (next_attempt_at)
    WHERE status IN ('PENDING', 'FAILED')
    AND attempts < max_attempts;

CREATE TABLE email_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id VARCHAR(255) NOT NULL,
    outbox_id UUID NOT NULL,  -- correlation only, no FK
    provider_id VARCHAR(255),
    to_addresses TEXT[] NOT NULL,
    from_address VARCHAR(255) NOT NULL,
    subject VARCHAR(500) NOT NULL,
    template_name VARCHAR(100) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'SENT',
    sent_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    bounce_reason TEXT,
    provider_response JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_email_audit_tenant
    ON email_audit_log (tenant_id, created_at DESC);
```

**Statuses for email_outbox**: `PENDING`, `SENDING`, `SENT`, `FAILED`,
`DEAD_LETTER`, `CANCELLED`.

**Statuses for email_audit_log**: `SENT`, `DELIVERED`, `BOUNCED`,
`COMPLAINED`, `FAILED`.

**No foreign key** from audit log to outbox. The outbox is ephemeral
(retained 90 days). The audit log is the permanent compliance record.
`outbox_id` is a correlation UUID for debugging, not a constraint.

**`tenant_id` type**: `VARCHAR(255)` to match existing billing tables in
payment-order service.

### Task 2: Email service package (3 points)

`shared/pkg/email/`:
- `sender.go` - `Sender` interface, `Message` type
- `template.go` - `TemplateRenderer` using `html/template` + `embed.FS`
- `outbox.go` - Outbox repository (write, read pending, update status,
  cancel by idempotency key pattern)
- `resend/provider.go` - Resend SDK wrapper implementing `Sender`, wrapped
  with circuit breaker from `shared/pkg/clients/circuitbreaker.go`
- `log/provider.go` - Log-mode sender (writes structured log, no API call)
- `noop/provider.go` - No-op sender for tests

**Reuse explicitly**:
- `circuitbreaker.go` wrapping Resend client
- `shared/pkg/tokens/` for any future token operations
- Go `html/template` only (lint rule: no `text/template` in email package)

### Task 3: Email worker (3 points)

Implement `dispatch.DispatchableInstruction` for `EmailOutboxRow`. Write a
`BatchProcessor` callback that:
1. Renders template via `TemplateRenderer`
2. For dunning emails: checks invoice status, cancels if PAID
3. Calls `Sender.Send()` with idempotency key as Resend header
4. In same transaction: updates outbox status + inserts audit log entry
5. On failure: increments attempts, calculates next_attempt_at with backoff
6. On max attempts exceeded: sets status to `DEAD_LETTER`

**Host in unified binary** (`cmd/meridian/main.go`) alongside existing
workers.

**Circuit breaker integration**: Wrap Resend `Sender` with circuit breaker.
When open, `Send()` returns immediately with error - worker marks row as
FAILED for retry. Retries are preserved for transient per-email issues,
not wasted on a known-down provider.

### Task 4: Prometheus metrics (1 point)

Copy `shared/platform/events/metrics.go`, rename subsystem from
`event_outbox` to `email_outbox`. Wire `Record*` calls in the same places
as the event worker. Add circuit breaker state gauge. Mechanical adaptation.

### Task 5: Resend webhook endpoint (2 points)

`POST /api/v1/webhooks/resend`:
- Verify `Svix-Signature` header (Resend webhook signing)
- Parse event type: `email.delivered`, `email.bounced`, `email.complained`
- Look up audit log entry by `provider_id`
- Update status and `delivered_at`/`bounce_reason`
- Return 200 on success (Resend retries on non-2xx)

Register in api-gateway routes. No authentication (signature verification
is the auth mechanism).

### Task 6: Templates (3 points)

Four templates in `shared/pkg/email/templates/`:
- `invoice.html` + `invoice.txt`
- `dunning-notice.html` + `dunning-notice.txt` (parameterized: severity
  1/2/3 with `{{if eq .Severity 1}}` blocks)
- `payment-received.html` + `payment-received.txt`
- `account-frozen.html` + `account-frozen.txt`

Responsive HTML, plain text fallbacks, Meridian branding. Loaded via
`embed.FS` at compile time. This is primarily design work, not engineering
risk.

### Task 7: Invoice and dunning delivery wiring (3 points)

**Invoice delivery**: After `invoice_generator.go` creates an ISSUED
invoice, write to email outbox. Look up party email from party service.
Idempotency key: `invoice-{invoice_id}`. Shadow mode billing runs skip
email delivery.

**Dunning wiring**: Replace `notification.send` stub in `saga_handlers.go`
with real implementation that writes to email outbox. Resolve email from
`party_id` server-side. Idempotency key: `dunning-{level}-{invoice_id}`.

**Payment cancellation**: When invoice transitions to PAID, cancel pending
dunning outbox rows and send payment-received email. Idempotency key:
`payment-confirm-{invoice_id}`.

**Dunning delivery guard**: Before escalating to next dunning level, check
prior level's email status. If `DEAD_LETTER`, pause escalation and flag for
manual review.

### Task 8: DNS and Resend setup (1 point)

- Create Resend account
- Add DKIM, SPF, DMARC records on meridianhub.cloud
- Provision API key, add to demo `.env`
- Configure webhook endpoint URL in Resend dashboard
- Set `EMAIL_MODE=log` in CI, `EMAIL_MODE=live` on demo
- Verify domain sending with a test email

## Email Sending Rules

| Email Type | Can Suppress? | Rate Control |
|-----------|--------------|-------------|
| Invoice | No (contractual) | 1 per invoice (idempotency key) |
| Payment confirmation | Yes | 1 per payment (idempotency key) |
| Dunning notice | No (contractual) | Per escalation schedule |
| Account frozen | No (system) | 1 per freeze event |

Auth email rules (verification, password reset, invitation) will be defined
in PRD 053.

## Configuration

```bash
# Required
RESEND_API_KEY=re_xxxxxxxxxxxx

# Optional (defaults shown)
EMAIL_MODE=live                  # disabled | log | live
EMAIL_FROM_DEFAULT=noreply@meridianhub.cloud
EMAIL_FROM_BILLING=billing@meridianhub.cloud
EMAIL_OUTBOX_POLL_INTERVAL=5s
EMAIL_OUTBOX_BATCH_SIZE=50
EMAIL_MAX_ATTEMPTS=5
EMAIL_BASE_URL=https://meridianhub.cloud
```

**EMAIL_MODE**:
- `disabled` - No outbox writes, no sending. For unit tests.
- `log` - Writes outbox + audit log, renders templates, but does not call
  Resend API. For integration tests and CI.
- `live` - Full sending via Resend. For demo and production.

## Success Criteria

1. Invoice email in outbox within 5s of invoice creation
2. Worker sends PENDING entries and records `provider_id` in audit log
3. Dunning emails queued at each escalation level with correct
   idempotency keys
4. Payment confirmation cancels pending dunning emails for same invoice
5. Dead-lettered emails retrievable and manually retryable
6. `EMAIL_MODE=log` writes outbox + audit, zero Resend API calls
7. Circuit breaker opens after 5 consecutive Resend failures, closes on
   recovery
8. All 6 Prometheus metrics emit correctly under normal and failure
   conditions

## Security Considerations

1. **Template safety**: Go `html/template` auto-escapes all output by
   context. Template injection is structurally impossible. Lint rule
   against `text/template` in the email package.
2. **Recipient validation**: `notification.send` handler accepts `party_id`,
   resolves email server-side. Raw `to` addresses validated against known
   identities/parties within the tenant.
3. **SPF/DKIM/DMARC**: DNS records on meridianhub.cloud from day one for
   deliverability and anti-spoofing.
4. **No PII in application logs**: Email addresses exist only in outbox and
   audit tables. Structured logging references `outbox_id`, not addresses.
5. **Tenant isolation**: All queries scoped by `tenant_id`. Worker uses
   cross-tenant `FindPending()` (worker-only); all user-facing queries
   require tenant context.
6. **Webhook verification**: Resend webhook endpoint validates
   `Svix-Signature` header. No unauthenticated state mutation.
7. **Tamper evidence**: The outbox is the only path to Resend. Any email in
   Resend without an outbox entry is evidence of unauthorized activity.

## Dependencies

- Resend account and API key provisioned
- DNS records (DKIM, SPF, DMARC) configured on meridianhub.cloud
- `EMAIL_MODE=log` in CI environments
- Integration tests use `noop.Sender` or `log.Sender`

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Resend outage (< 1h) | Medium | Low | Circuit breaker + 29h retry window |
| Resend outage (> 24h) | Low | High | Dead letter queue + manual retry endpoint |
| Demo spam abuse | Medium | Medium | Registration rate limiter (existing), consider CAPTCHA if volume increases |
| Outbox table growth | Low | Medium | Partial index on actionable rows, 90-day retention job for SENT/CANCELLED |
| Domain reputation damage | Low | High | SPF/DKIM/DMARC from day one, Resend bounce suppression |
| Dunning email after payment | N/A | N/A | Eliminated by design: payment cancellation + pre-send check + delivery guard |

## Retention Policy

- **email_outbox**: SENT and CANCELLED rows deleted after 90 days.
  DEAD_LETTER rows retained until manually resolved.
- **email_audit_log**: Retained for 7 years (financial compliance).
  Audit log access pattern: compliance queries by tenant + date range.

## Related PRDs

| PRD | Scope | Depends On | Status |
|-----|-------|------------|--------|
| **052** (this) | Email infrastructure + billing delivery | Nothing | Active |
| **053** | Auth email flows (verification, password reset, invitations) | 052 | Planned |
| **054** | Billing UI (dashboard, invoice detail, delivery status) | 052 | Planned |

053 and 054 are independent of each other and can be staffed in parallel
once 052 lands.

## Out of Scope

- Email verification on registration (PRD 053)
- Password reset flow (PRD 053)
- User invitation emails (PRD 053)
- Welcome email (PRD 053)
- Billing dashboard and invoice detail UI (PRD 054)
- Tenant-customizable email templates (brand colors, logos)
- Marketing/newsletter emails
- PDF invoice attachments
- Multi-language email templates (i18n)
- In-app notification center
