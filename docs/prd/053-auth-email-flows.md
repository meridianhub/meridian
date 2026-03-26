---
name: prd-auth-email-flows
description: Email verification, password reset, and user invitation flows building on email infrastructure (PRD 052)
triggers:
  - Implementing password reset or forgot password
  - Adding email verification to registration
  - Sending user invitation emails
  - Adding auth-related frontend pages (verify, reset, invite)
instructions: |
  ~13 story points. Depends on PRD 052 (email infrastructure MVP).
  Can be worked in parallel with PRD 054 (billing UI).
  Reuse shared/pkg/tokens for all token operations.
  Add PENDING_VERIFICATION status to identity domain.
  Split EMAIL_VERIFICATION_REQUIRED as separate config flag.
---

# Auth Email Flows

## Problem Statement

Meridian's registration flow activates users immediately with no email
verification. There is no password reset flow - locked-out users must
contact support. Admin-created users receive no invitation email. These
gaps are acceptable for an internal-only platform but block self-service
adoption and the demo sandbox experience.

This PRD adds email-based auth flows on top of the email infrastructure
established by PRD 052.

## Dependency

**Requires PRD 052** (Email Infrastructure MVP) to be complete. This PRD
uses the outbox, worker, Sender interface, and template system from 052.

**Modifies flows from PRD 044** (Auth Flow Architecture). PRD 044
documents the current BFF password login and Dex SSO flows. This PRD
adds email verification as an optional gate before login and adds
password reset as a new unauthenticated flow. The registration handler
behavior change (optional `PENDING_VERIFICATION` status) is a
modification to Flow 1 in PRD 044.

**Independent of PRD 054** (Billing UI). These can be staffed in parallel.

## Technical Context

### Existing Infrastructure (from PRD 052)

- Email outbox + worker + Resend integration
- `shared/pkg/email/` Sender interface and TemplateRenderer
- `EMAIL_MODE=disabled/log/live` configuration

### Existing Infrastructure (pre-052)

| Component | Location | Relevance |
|-----------|----------|-----------|
| Token generation (crypto/rand, SHA-256 hash) | `shared/pkg/tokens/token.go` | Reuse for all auth tokens |
| Constant-time token validation | `shared/pkg/tokens/token.go` | Prevents timing side-channels |
| Password reset TTL (1h) | `shared/pkg/tokens/expiry.go` | Already defined |
| Invitation TTL (72h) | `shared/pkg/tokens/expiry.go` | Already defined |
| Invitation domain model | `services/identity/domain/invitation.go` | Reuse for invite flow |
| Identity state machine | `services/identity/domain/identity.go` | Extend with PENDING_VERIFICATION |
| Registration handler | `services/api-gateway/registration_handler.go` | Modify for optional verification |
| Registration rate limiter | `services/api-gateway/registration_rate_limiter.go` | Reuse for forgot-password |
| Login connector | `services/identity/connector/connector.go` | Add verification-aware error |

## Scope

### In Scope

1. Email verification on registration (optional, per-tenant config)
2. Password reset (forgot password) flow
3. User invitation emails
4. Welcome email on registration
5. Frontend pages for all flows
6. Account lockout notification email

### Out of Scope

- Email change flow (re-verification on email update)
- MFA setup/recovery emails
- Login-from-new-context alerts
- Email notification preferences UI

## Detailed Requirements

### Task 1: Identity status extension (2 points)

- Add `PENDING_VERIFICATION` status to identity domain
- New `NewSelfRegisteredIdentity()` constructor (creates with
  `PENDING_VERIFICATION` when verification required, `ACTIVE` otherwise)
- New `Verify()` method on identity (transitions
  `PENDING_VERIFICATION` -> `ACTIVE`)
- Login connector: return distinct error for `PENDING_VERIFICATION`
  ("please verify your email") vs generic "invalid credentials"
- Configuration: `EMAIL_VERIFICATION_REQUIRED=false` (default off,
  preserves current behavior, enable per-environment)

### Task 2: Email verification flow (3 points)

**Backend**:
- Migration: `email_verification_tokens` table (token_hash, identity_id,
  expires_at, consumed_at)
- On registration (when verification required): generate token via
  `shared/pkg/tokens`, write to outbox with `verify-email` template
- `POST /api/v1/verify-email` - validates token hash, calls
  `identity.Verify()`, marks token consumed
- Token expiry: 24 hours. Single-use.
- Resend verification: `POST /api/v1/resend-verification` - rate limited
  (3/hour per identity via DB count query on outbox)

**Frontend**:
- Post-registration "check your email" page with resend button
- Verification landing page (success, expired, already-verified, invalid
  states)
- Support contact info on all verification pages

### Task 3: Password reset flow (3 points)

**Backend**:
- Migration: `password_reset_tokens` table (token_hash, identity_id,
  expires_at, consumed_at)
- `POST /api/v1/forgot-password` - always returns 200 (timing-safe:
  generate token and write noop outbox row even for invalid emails to
  equalize work profile)
- `POST /api/v1/reset-password` - validates token, updates password,
  invalidates all active sessions, marks token consumed
- Token expiry: 1 hour (matches `shared/pkg/tokens/expiry.go`).
  Single-use.
- Rate limit: 3 requests per email per hour via DB count query:
  `SELECT COUNT(*) FROM email_outbox WHERE to_addresses @> ARRAY[$1]
  AND template_name = 'password-reset' AND created_at > NOW() -
  INTERVAL '1 hour'`

**Frontend**:
- "Forgot password?" link on login page
- Email input form -> "check your email" confirmation
- Reset form (new password + confirm) -> success -> redirect to login

### Task 4: User invitation emails (2 points)

- When admin creates identity with `PENDING_INVITE` status, write to
  outbox with `invite-user` template
- Template shows `invited by [admin-email] at [slug].meridianhub.cloud`
  (not user-controlled display name, prevents phishing)
- Invitation link -> set-password page (reuses reset flow UI with
  different copy)
- Token expiry: 72 hours (matches `shared/pkg/tokens/expiry.go`)

### Task 5: Templates (2 points)

Four templates in `shared/pkg/email/templates/`:

| Template | Trigger | Data |
|----------|---------|------|
| `verify-email.html` | Registration (when verification required) | verification link, tenant name, support contact |
| `password-reset.html` | Forgot password request | reset link, expiry time, support contact |
| `invite-user.html` | Admin creates user | inviter email, tenant slug, accept link |
| `welcome.html` | Registration complete | tenant name, login URL, getting started link |

Plus: `account-lockout.html` - triggered when identity auto-locks at 5
failed attempts. "Your account was locked after 5 failed login attempts.
If this wasn't you, contact support."

### Task 6: Admin verification override (1 point)

- `POST /api/v1/admin/identities/{id}/verify-override` - requires
  ADMIN or PLATFORM_ADMIN JWT scope
- Calls existing `identity.Activate()` bypassing email verification
- Use case: email delivery failure, support escalation, demo environment
- Audit logged

## Security Considerations

1. **Timing-safe forgot-password**: Generate token and write noop outbox
   row for invalid emails to equalize work profile. Prevents email
   enumeration via response timing.
2. **Token security**: All tokens via `shared/pkg/tokens` - crypto/rand,
   32 bytes, SHA-256 hash storage, `ConstantTimeCompare` validation.
3. **Rate limiting**: Per-email rate limiting via DB count query on outbox
   table (cross-replica safe, no Redis). Per-IP via existing rate limiter.
4. **CSRF protection**: Document that JSON Content-Type requiring CORS
   preflight provides implicit CSRF protection for unauthenticated POST
   endpoints. Make this explicit in API documentation.
5. **Invitation phishing**: Template shows programmatically-derived slug
   and admin email, not user-controlled display names.
6. **Single-use tokens**: All tokens marked consumed after use. Prevents
   replay.

## Success Criteria

1. Unverified users see "please verify your email" (not "invalid
   credentials") when `EMAIL_VERIFICATION_REQUIRED=true`
2. Password reset works end-to-end: request -> email -> reset -> login
3. Forgot-password response time is constant regardless of email validity
4. Admin can override verification for stuck users
5. Invitation email contains no user-controllable display names
6. All token operations use `shared/pkg/tokens` (no custom crypto)
7. Rate limits enforced: max 3 reset/verification requests per email
   per hour

## Estimated Effort

| Task | Points |
|------|--------|
| Identity status extension | 2 |
| Email verification flow | 3 |
| Password reset flow | 3 |
| User invitation emails | 2 |
| Templates (4 + lockout) | 2 |
| Admin verification override | 1 |
| **Total** | **13** |
