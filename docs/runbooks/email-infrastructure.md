# Email Infrastructure Runbook

## Overview

Meridian sends transactional emails (payment confirmations, dunning notices) via the email dispatch worker. Emails are queued in an outbox table in the payment-order database and dispatched by a background worker that polls for pending entries.

**Provider**: [Resend](https://resend.com) (HTTP API, Svix-signed webhooks for delivery status).

## Architecture

```
Saga/Service -> OutboxRepository (INSERT) -> email_outbox table
                                                  |
                                          Email Worker (polls)
                                                  |
                                          TemplateRenderer (embed.FS)
                                                  |
                                          Sender (Resend / Log / Noop)
                                                  |
                                          AuditRepository (INSERT)
```

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `EMAIL_MODE` | No | `disabled` (dev) | `disabled` / `log` / `live` |
| `RESEND_API_KEY` | For `live` | - | Resend API key |
| `RESEND_WEBHOOK_SECRET` | No | - | Svix signature secret for delivery webhooks |
| `EMAIL_WORKER_BATCH_SIZE` | No | `50` | Max emails per poll cycle |
| `EMAIL_WORKER_POLL_INTERVAL` | No | `5s` | Outbox poll interval |

### Modes

- **disabled**: No-op sender, worker does not start. Default in local dev.
- **log**: Logs email content to stdout via slog. Useful for integration testing.
- **live**: Sends real emails via Resend API. Requires `RESEND_API_KEY`.

## DNS Setup (meridianhub.cloud)

Configure these DNS records for the sending domain:

### SPF

```
TXT  meridianhub.cloud  "v=spf1 include:amazonses.com ~all"
```

### DKIM

Add the DKIM records provided by Resend during domain verification. Typically three CNAME records:

```
CNAME  resend._domainkey.meridianhub.cloud  -> (value from Resend)
CNAME  s1._domainkey.meridianhub.cloud      -> (value from Resend)
CNAME  s2._domainkey.meridianhub.cloud      -> (value from Resend)
```

### DMARC

```
TXT  _dmarc.meridianhub.cloud  "v=DMARC1; p=quarantine; rua=mailto:dmarc@meridianhub.cloud"
```

### Verification

After adding records, verify in the Resend dashboard (Settings > Domains). DNS propagation may take up to 48 hours.

## Resend Account Setup

1. Create account at [resend.com](https://resend.com)
2. Add and verify sending domain (meridianhub.cloud)
3. Create an API key with "Sending access" permission
4. Set `RESEND_API_KEY` in the deployment environment
5. (Optional) Configure webhook endpoint at `https://<domain>/webhooks/resend` and set `RESEND_WEBHOOK_SECRET`

## Troubleshooting

### Worker not starting

Check logs for `email worker disabled`. Common causes:
- `EMAIL_MODE=disabled` (intentional in dev)
- `EMAIL_MODE=live` but `RESEND_API_KEY` not set
- Template parsing failure (check embedded templates)

### Dead-lettered emails

Emails that exhaust all retry attempts are moved to `dead_letter` status. Query:

```sql
SELECT id, tenant_id, template_name, recipient_email, last_error, attempts, max_attempts
FROM email_outbox
WHERE status = 'dead_letter'
ORDER BY updated_at DESC
LIMIT 20;
```

To retry dead-lettered emails, reset their status:

```sql
UPDATE email_outbox
SET status = 'pending', attempts = 0, last_error = NULL
WHERE id = '<uuid>';
```

### Circuit breaker open

The email processor includes a circuit breaker that opens after repeated send failures. When open, all sends are skipped until the half-open timeout expires.

Monitor via Prometheus metric: `meridian_email_outbox_circuit_breaker_state` (0=closed, 1=half-open, 2=open).

### Webhook delivery failures

If the Resend webhook is not updating delivery status:
- Verify `RESEND_WEBHOOK_SECRET` matches the signing secret in the Resend dashboard
- Check that the webhook endpoint is accessible: `POST /webhooks/resend`
- Review audit table for received events:

```sql
SELECT id, resend_email_id, event_type, created_at
FROM email_audit
ORDER BY created_at DESC
LIMIT 20;
```

## Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `meridian_email_outbox_pending_total` | Gauge | Current pending outbox entries |
| `meridian_email_outbox_send_duration_seconds` | Histogram | Email send API call duration |
| `meridian_email_outbox_send_errors_total` | Counter | Failed sends by template and error type |
| `meridian_email_outbox_dead_letter_total` | Counter | Emails exhausting all retries |
| `meridian_email_outbox_cancelled_total` | Counter | Cancelled emails (e.g., paid invoices) |
| `meridian_email_outbox_circuit_breaker_state` | Gauge | Circuit breaker state (0/1/2) |
