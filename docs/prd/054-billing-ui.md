---
name: prd-billing-ui
description: Billing dashboard and invoice detail pages with email delivery status visibility
triggers:
  - Building billing or invoice UI pages
  - Displaying billing runs, invoices, or dunning status
  - Showing email delivery status for invoices
instructions: |
  ~8 story points. Depends on PRD 052 (email infrastructure MVP).
  Can be worked in parallel with PRD 053 (auth email flows).
  Requires backend API endpoints for billing data aggregation.
  Resend webhook data (from 052) populates delivery status.
---

# Billing UI

## Problem Statement

Meridian generates invoices, runs billing cycles, and escalates dunning -
but none of this is visible in the frontend. There are no billing pages.
Tenant admins cannot see invoices, billing run history, or email delivery
status. When a dunning email bounces or an invoice fails to deliver, there
is no visibility until a customer calls support.

PRD 052 adds email delivery with audit trails and webhook-based delivery
status. This PRD makes that data visible to tenant admins.

## Dependency

**Requires PRD 052** (Email Infrastructure MVP) to be complete. This PRD
reads data from the outbox, audit log, and billing tables populated by 052.

**Independent of PRD 053** (Auth Email Flows). These can be staffed in
parallel.

## Technical Context

### Existing Backend (no UI)

| Capability | Location | API Status |
|-----------|----------|------------|
| Billing runs (INITIATED, PROCESSING, COMPLETED, FAILED) | `services/payment-order/worker/billing_scheduler.go` | No gRPC/HTTP API |
| Invoices (DRAFT, ISSUED, PAID, VOID, OVERDUE) | `services/payment-order/domain/billing.go` | No gRPC/HTTP API |
| Invoice line items (JSONB) | `services/payment-order/adapters/persistence/billing_entity.go` | No API |
| Dunning escalation (levels 0-3) | `services/payment-order/worker/dunning_worker.go` | No API |
| Email audit log (SENT, DELIVERED, BOUNCED) | PRD 052: `shared/pkg/email/` (tables in each service DB) | Webhook-populated |
| Billing metrics (Prometheus) | `services/payment-order/worker/billing_metrics.go` | Prometheus only |

### Key Insight: Backend APIs Must Be Built First

The billing UI requires **new gRPC/HTTP endpoints** that don't exist today.
The billing data lives in payment-order's internal domain with no external
API. This PRD includes the API work, not just frontend pages.

### Frontend Patterns

| Pattern | Example | Location |
|---------|---------|----------|
| List page with filters | Accounts list | `frontend/src/features/accounts/pages/index.tsx` |
| Detail page with tabs | Account detail | `frontend/src/features/accounts/pages/[accountId].tsx` |
| Status badges | Payment status | `frontend/src/features/payments/` |
| Data tables | Ledger postings | `frontend/src/features/ledger/` |
| Action dialogs | Freeze account | `frontend/src/features/accounts/` |

## Scope

### In Scope

1. Billing runs list page
2. Invoice list and detail pages
3. Email delivery status indicators
4. Manual actions: resend email, mark as paid, void invoice
5. Backend API endpoints for billing data

### Out of Scope

- Billing configuration UI (schedule, shadow mode settings)
- Invoice PDF generation or download
- Payment method management
- Subscription/plan management
- Usage/metering dashboard
- Billing analytics or reporting

## Detailed Requirements

### Task 1: Billing API endpoints (3 points)

New endpoints in payment-order service (or api-gateway BFF):

**Billing Runs**:
- `GET /api/v1/billing/runs` - list billing runs with pagination,
  filter by status
- `GET /api/v1/billing/runs/{runId}` - detail with invoice summary

**Invoices**:
- `GET /api/v1/billing/invoices` - list invoices with pagination,
  filter by status, party, billing run
- `GET /api/v1/billing/invoices/{invoiceId}` - detail with line items
- `POST /api/v1/billing/invoices/{invoiceId}/resend` - re-queue
  invoice email (writes new outbox row with fresh idempotency key)
- `POST /api/v1/billing/invoices/{invoiceId}/mark-paid` - manual
  override (triggers payment confirmation email, cancels pending dunning)
- `POST /api/v1/billing/invoices/{invoiceId}/void` - void invoice
  (cancels pending emails)

**Email Status** (joins across billing and notification tables):
- `GET /api/v1/billing/invoices/{invoiceId}/emails` - list email
  audit log entries for this invoice (by idempotency key pattern
  `invoice-{id}` and `dunning-*-{id}`)

All endpoints tenant-scoped. Require ADMIN or OPERATOR role.

### Task 2: Billing runs list page (1 point)

Route: `/billing`

| Column | Source |
|--------|--------|
| Period | billing_run.billing_period |
| Status | billing_run.status (badge) |
| Invoices | COUNT of invoices in run |
| Total Amount | SUM of invoice totals |
| Created | billing_run.created_at |

Filters: status (INITIATED, PROCESSING, COMPLETED, FAILED).
Click row -> billing run detail showing its invoices.

### Task 3: Invoice list and detail pages (2 points)

**List** - Route: `/billing/invoices`

| Column | Source |
|--------|--------|
| Invoice # | invoice.invoice_number |
| Party | party name (resolved) |
| Amount | invoice.total_amount |
| Status | invoice.status (badge) |
| Email | delivery status indicator |
| Due Date | invoice.due_date |

Filters: status, party, billing run.

**Detail** - Route: `/billing/invoices/:invoiceId`

Tabs:
- **Overview**: Invoice header, party info, status timeline, due date
- **Line Items**: Table of line items from JSONB (description, quantity,
  unit price, amount)
- **Email History**: List of email audit log entries for this invoice
  (sent, delivered, bounced with timestamps and bounce reasons)

Actions (conditional on status):
- "Resend Email" (any status) - re-queues invoice email
- "Mark as Paid" (ISSUED or OVERDUE) - manual payment confirmation
- "Void Invoice" (ISSUED or OVERDUE) - voids and cancels pending emails

### Task 4: Email delivery status indicators (1 point)

Reusable component showing email delivery state:

| Status | Display | Color |
|--------|---------|-------|
| PENDING | Queued | Gray |
| SENT | Sent | Blue |
| DELIVERED | Delivered | Green |
| BOUNCED | Bounced | Red |
| DEAD_LETTER | Failed | Red |
| CANCELLED | Cancelled | Gray |

Shown inline on invoice list rows and on invoice detail's email history
tab. Tooltip shows timestamp and bounce reason where applicable.

Data source: email audit log joined by idempotency key pattern.

### Task 5: Navigation and routing (1 point)

- Add "Billing" to sidebar navigation (between Payments and Ledger)
- Feature guard: only show when billing feature is enabled for tenant
- Route registration in `App.tsx`
- Breadcrumbs: Billing > Invoices > INV-001

## Security Considerations

1. **Role-based access**: All billing endpoints require ADMIN or OPERATOR
   role. AUDITOR gets read-only access (no resend/void/mark-paid actions).
2. **Tenant scoping**: All queries filtered by tenant from JWT context.
3. **Action audit**: Resend, mark-paid, and void actions write to the
   existing audit trail.
4. **No PII exposure**: Invoice list shows party name, not email address.
   Email addresses visible only in invoice detail's email history tab
   (admin-only).

## Success Criteria

1. Billing runs page lists all runs with correct invoice counts
2. Invoice detail shows line items matching the JSONB data
3. Email delivery status reflects actual Resend webhook data
   (DELIVERED/BOUNCED, not just SENT)
4. "Resend Email" creates new outbox row and email is delivered
5. "Mark as Paid" triggers payment confirmation email and cancels
   pending dunning
6. "Void Invoice" cancels all pending emails for that invoice
7. Non-admin users cannot access billing pages

## Estimated Effort

| Task | Points |
|------|--------|
| Billing API endpoints | 3 |
| Billing runs list page | 1 |
| Invoice list and detail pages | 2 |
| Email delivery status indicators | 1 |
| Navigation and routing | 1 |
| **Total** | **8** |
