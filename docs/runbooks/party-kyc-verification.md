---
name: party-kyc-verification
description: >-
  Operational procedures for KYC/AML identity verification via Onfido,
  including setup, monitoring, troubleshooting, and manual operations
triggers:
  - Troubleshooting KYC verification failures
  - Investigating stuck PENDING verifications
  - Webhook signature validation errors
  - High manual review rate from Onfido
  - Timeout handler behaviour questions
  - Force-updating a verification status
instructions: |
  Use this runbook for KYC/AML verification operations in the Party service.
  Verification provider: Onfido (identity_enhanced + watchlist_enhanced checks).
  Webhook endpoint: POST /webhooks/verification/{provider} on HTTP port (default 8081).
  Timeout handler polls every 1h for PENDING verifications older than 24h.
  Terminal statuses: APPROVED, REJECTED, MANUAL_REVIEW. Only PENDING is non-terminal.
---

# Party KYC/AML Verification Runbook

**When to use this runbook**: Managing KYC/AML identity verification
operations, investigating webhook failures, resolving stuck verifications,
or configuring the Onfido integration.

## Verification Flow Overview

The Party service uses Onfido as its external KYC/AML provider.
The flow is asynchronous:

1. Party onboarding triggers `VerifyIdentity` and `CheckSanctions`
   calls to Onfido
2. Onfido creates an applicant, initiates checks
   (`identity_enhanced`, `watchlist_enhanced`)
3. Onfido processes the check asynchronously and sends a webhook
   callback when complete
4. The webhook handler validates the HMAC signature, parses the
   result, and updates the verification record
5. If no webhook arrives within 24 hours, the timeout handler polls
   Onfido for status and resolves the verification

### Verification Statuses

| Status | Meaning | Terminal |
|--------|---------|----------|
| `PENDING` | Awaiting provider result | No |
| `APPROVED` | Identity verified, sanctions clear | Yes |
| `REJECTED` | Verification failed or sanctions match | Yes |
| `MANUAL_REVIEW` | Requires human review | Yes |

### Sanctions Screening Statuses

| Status | Meaning |
|--------|---------|
| `CLEAR` | No watchlist matches found |
| `MATCH` | Potential watchlist match detected |
| `PENDING` | Screening in progress |
| `ERROR` | Screening failed |

## Environment Variables

| Variable | Required | Default |
|----------|----------|---------|
| `VERIFICATION_PROVIDER` | Yes | - |
| `VERIFICATION_API_KEY` | Yes (non-mock) | - |
| `VERIFICATION_API_SECRET` | Yes (non-mock) | - |
| `VERIFICATION_BASE_URL` | No | `https://api.onfido.com/v3.6` |
| `VERIFICATION_WEBHOOK_SECRET` | Yes (non-mock) | - |
| `VERIFICATION_WEBHOOK_URL` | Yes (non-mock) | - |
| `HTTP_PORT` | No | `8081` |
| `ENVIRONMENT` | No | `development` |
| `LOG_LEVEL` | No | `info` |

**Variable descriptions:**

- `VERIFICATION_PROVIDER` -- Provider name: `onfido` (production)
  or `mock` (development)
- `VERIFICATION_API_KEY` -- Onfido API token
- `VERIFICATION_API_SECRET` -- Onfido API secret
- `VERIFICATION_BASE_URL` -- Onfido API base URL (uses default if
  not set)
- `VERIFICATION_WEBHOOK_SECRET` -- HMAC-SHA256 secret for webhook
  signature validation (min 32 chars in production)
- `VERIFICATION_WEBHOOK_URL` -- Public URL where Onfido sends
  callbacks (must be HTTPS in production)
- `HTTP_PORT` -- Port for the webhook HTTP server
- `ENVIRONMENT` -- Set to `production` or `prod` for strict
  validation
- `LOG_LEVEL` -- Logging level: `debug`, `info`, `warn`, `error`

### Production Validation Rules

When `ENVIRONMENT` is `production` or `prod`:

- `mock` provider is rejected (`ErrMockProviderInProduction`)
- Webhook URL must use HTTPS
- Webhook secret must be at least 32 characters

## Onfido Setup

### 1. Create an Onfido Account

Obtain API credentials from the Onfido dashboard. You need:

- An API token (used as `VERIFICATION_API_KEY`)
- A webhook secret (used as `VERIFICATION_WEBHOOK_SECRET`)

### 2. Configure Webhook in Onfido Dashboard

Register a webhook endpoint in Onfido pointing to your deployment:

```text
URL: https://<your-domain>/webhooks/verification/onfido
Events: check.completed
```

The webhook handler extracts the provider name from the URL path
segment after `/webhooks/verification/`.

### 3. Set Environment Variables

```bash
export VERIFICATION_PROVIDER=onfido
export VERIFICATION_API_KEY=<your-onfido-api-token>
export VERIFICATION_API_SECRET=<your-onfido-api-secret>
export VERIFICATION_WEBHOOK_SECRET=<your-webhook-secret-min-32-chars>
export VERIFICATION_WEBHOOK_URL=\
  https://<your-domain>/webhooks/verification/onfido
export ENVIRONMENT=production
```

### 4. Verify Configuration

The service validates configuration at startup. Check logs for:

```json
{"msg":"verification provider initialized","provider":"onfido"}
{"msg":"starting HTTP server for webhooks","port":"8081"}
```

If configuration is invalid in production, the service exits
immediately. In development, it logs a warning and starts without
verification:

```json
{"msg":"verification config not loaded - KYC provider disabled"}
```

## Monitoring

### Key Log Entries

| Log Message | Level |
|-------------|-------|
| `verification provider initialized` | INFO |
| `identity verification initiated` | INFO |
| `sanctions screening initiated` | INFO |
| `webhook processed successfully` | INFO |
| `webhook already processed (idempotent)` | INFO |
| `invalid webhook signature` | WARN |
| `missing webhook signature` | WARN |
| `webhook timestamp too old` | WARN |
| `timed-out verification resolved` | INFO |
| `authentication failed` | ERROR |
| `rate limited by Onfido API` | WARN |
| `timeout handler started` | INFO |

### Health Check

The HTTP server exposes a health endpoint:

```bash
curl http://<host>:8081/health
```

Response:

```json
{
  "status": "ok",
  "timestamp": "2026-02-14T12:00:00Z",
  "verification_enabled": true,
  "verification_provider": "onfido"
}
```

## Troubleshooting

### Stuck PENDING Verifications

**Symptoms**: Verifications remain in `PENDING` status indefinitely.

**Automatic resolution**: The timeout handler runs every 1 hour and
checks for verifications older than 24 hours. For each stuck
verification, it:

1. Calls `GetVerificationStatus` on the Onfido API to check if a
   result is available
2. If Onfido returns a terminal status (`complete` with
   `clear`/`consider`/other), uses that status
3. If Onfido still returns `in_progress`, escalates to
   `MANUAL_REVIEW` with reason "Verification timed out after 24h"

**Manual investigation**:

```sql
-- Find PENDING verifications older than 24 hours
SELECT id, party_id, verification_id, provider, status, created_at
FROM party_verifications
WHERE status = 'PENDING'
  AND created_at < NOW() - INTERVAL '24 hours'
ORDER BY created_at;
```

**Possible causes**:

- Onfido webhook not configured or pointing to wrong URL
- Network/firewall blocking incoming webhooks
- Webhook signature mismatch causing rejection
- Onfido experiencing delays in processing

### Webhook Signature Failures

**Symptoms**: Logs show `invalid webhook signature` or
`missing webhook signature`.

**Diagnosis**:

1. Verify `VERIFICATION_WEBHOOK_SECRET` matches the secret
   configured in Onfido dashboard
2. Check that the webhook request includes the
   `X-Webhook-Signature` header
3. The signature must be a hex-encoded HMAC-SHA256 of the raw
   request body using the shared secret

**Signature computation**:

```text
HMAC-SHA256(body, secret) -> hex_encode -> X-Webhook-Signature
```

**Common causes**:

- Secret mismatch between Onfido dashboard and environment variable
- Reverse proxy modifying the request body before it reaches the
  handler
- Using a different HMAC algorithm (must be SHA-256)

### High Manual Review Rate

**Symptoms**: Large percentage of verifications ending in
`MANUAL_REVIEW`.

**Onfido status mapping**:

| Onfido Status | Onfido Result | Meridian Status |
|---------------|---------------|-----------------|
| `in_progress` | - | `PENDING` |
| `complete` | `clear` | `APPROVED` |
| `complete` | `consider` | `MANUAL_REVIEW` |
| `complete` | (other) | `REJECTED` |
| `paused` | - | `MANUAL_REVIEW` |
| `withdrawn` | - | `REJECTED` |
| (timeout, still pending) | - | `MANUAL_REVIEW` |

**Investigation steps**:

- Check if Onfido is returning `consider` results (document quality
  issues, partial matches)
- Check if verifications are timing out (look for
  "Verification timed out" in reason field)
- Review Onfido dashboard for applicant-level details

### Timeout Handler Not Running

**Symptoms**: No `timeout handler started` log entry, stuck
verifications not being resolved.

**Possible causes**:

- Verification config not loaded (check for
  `verification config not loaded` warning)
- Service started without `VERIFICATION_PROVIDER` set
- Timeout handler creation failed (check for
  `failed to create timeout handler` error)

The timeout handler starts only when both `verificationSvc` and
`verificationCfg` are non-nil (i.e., verification is fully
configured).

### Onfido API Errors

| Error | Meaning | Action |
|-------|---------|--------|
| `onfido: unauthorized` | 401 | Check API key |
| `onfido: rate limited` | 429 | Back off; auto-recovers |
| `onfido: server error` | 5xx | Transient; retries next attempt |
| `onfido: validation error` | 422 | Check request data |

## Manual Operations

### Force-Update a Verification Status

To manually resolve a stuck or incorrectly-classified verification,
update the database directly:

```sql
-- Update a specific verification to APPROVED
UPDATE party_verifications
SET status = 'APPROVED',
    risk_score = 0.1,
    reason = 'Manually approved by operator',
    completed_at = NOW(),
    version = version + 1,
    updated_at = NOW()
WHERE id = '<verification-uuid>'
  AND status = 'PENDING';
```

```sql
-- Update a specific verification to MANUAL_REVIEW
UPDATE party_verifications
SET status = 'MANUAL_REVIEW',
    reason = 'Escalated for manual review by operator',
    completed_at = NOW(),
    version = version + 1,
    updated_at = NOW()
WHERE id = '<verification-uuid>'
  AND status = 'PENDING';
```

The `version` column implements optimistic locking. Always increment
it and include the current status in the `WHERE` clause to avoid
overwriting concurrent updates.

### Query Verification History for a Party

```sql
SELECT id, verification_id, provider, status, risk_score, reason,
       created_at, completed_at
FROM party_verifications
WHERE party_id = '<party-uuid>'
ORDER BY created_at DESC;
```

### Replay a Webhook Manually

If a webhook was lost or rejected, you can replay it using the same
format the handler expects:

```bash
# Generate the HMAC signature
BODY='{"verification_id":"<onfido-check-id>","status":"APPROVED",'\
'"risk_score":0.1,"reason":"Identity verified",'\
'"timestamp":"2026-02-14T12:00:00Z"}'
SIGNATURE=$(echo -n "$BODY" \
  | openssl dgst -sha256 -hmac "<webhook-secret>" \
  | awk '{print $2}')

# Send the webhook
curl -X POST http://<host>:8081/webhooks/verification/onfido \
  -H "Content-Type: application/json" \
  -H "X-Webhook-Signature: $SIGNATURE" \
  -d "$BODY"
```

The handler is idempotent: if the verification is already in a
terminal state, it returns success without modifying the record.

### Webhook Timestamp Validation

The handler rejects webhooks with timestamps older than 5 minutes
(`DefaultWebhookMaxAge`) or more than 30 seconds in the future
(`DefaultClockDriftTolerance`). If replaying a webhook, ensure the
timestamp is current.
