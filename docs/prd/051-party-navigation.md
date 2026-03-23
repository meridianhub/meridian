# Party Navigation and Service Boundary Cleanup

## Problem Statement

Two related problems that should be addressed together:

### Navigation gap

Meridian's party, account, and transaction models have the
right primitives - org-scoped accounts (`org_party_id`), party
associations with relationship types, and per-account
transaction logs - but the API layer doesn't expose the
filtering needed to navigate between them. The frontend works
around this with client-side filtering (fetching all accounts,
filtering in JS), which won't scale and produces a fragile
user experience.

### Service boundary violations

Several services have accumulated cross-service imports that
violate BIAN domain separation. current-account directly
imports party, financial-accounting, position-keeping, and
internal-account clients. financial-accounting imports
internal-account. These create a hub-and-spoke coupling
pattern that blocks independent deployment and makes changes
ripple across service boundaries.

Fixing both together ensures we don't add new navigation
features on top of a brittle foundation.

### Navigation goals

A user should be able to:

1. View a party and see its accounts (current and internal)
1. View an org-type party and see its member parties
1. From any account, see its transactions
1. Navigate the full tree:
   Org -> Member Parties -> Their Accounts -> Transactions

This matters beyond financial services. In energy, a GSP
(Grid Supply Point) is an org with internal accounts tracking
counterparty costs across the distribution network. DNOs and
suppliers are member parties with their own accounts. The same
drill-down works across any domain. The primitives are
universal; the navigation just needs to be wired through.

> Note: This PRD covers navigation only - drilling down to see
> individual account balances and transactions. Aggregate
> treasury views (net position across all accounts), margin
> reporting (bill vs running costs), and P&L summaries are
> separate concerns that require BIAN component analysis
> (Treasury Management, Margin Management, Financial Statement
> Assessment) and their own PRDs.

## Technical Context

### What exists today

Party service:

- `ListParticipants(org_party_id, relationship_type)` returns
  members of an org. Works server-side.
- `party_association` table with relationship types
  (SYNDICATE_PARTICIPANT, BENEFICIAL_OWNER, etc.) and JSONB
  metadata.
- A party can belong to multiple orgs via multiple
  associations.

Current Account service:

- `org_party_id` column on the `account` table (added in
  migration `20260214000001`).
- `ListByOrganization(orgPartyID)` exists in the Go
  repository layer but is NOT exposed via the gRPC
  `ListCurrentAccounts` endpoint.
- `ListCurrentAccounts` proto has no `party_id` or
  `org_party_id` filter - only `status` and `iban`.

Position Keeping service:

- `ListFinancialPositionLogs` filters by `account_id` only.

Financial Accounting service:

- `ListLedgerPostings` filters by `account_id` and
  `financial_booking_log_id`.

Internal Account service:

- `ListInternalAccounts` filters by `behavior_class`,
  `instrument_code`, `status`, `clearing_purpose` - but not
  `org_party_id`, despite the column existing.

Frontend:

- Party detail page has 8 tabs. The Accounts tab fetches all
  accounts and filters client-side (up to 10 pages, broken
  pagination semantics).
- Associations tab is a stub - data is fetched via
  `usePartyAssociations()` but the UI renders
  "No associations information available".
- Account detail page has Overview, Transactions, Liens,
  Audit Trail tabs. Transactions shows ledger postings by
  account_id.

### Service boundary violations (existing)

<!-- markdownlint-disable MD013 -->

| Violation | Location | Severity |
|-----------|----------|----------|
| current-account imports party client | `current-account/cmd/main.go` | Critical |
| current-account imports financial-accounting client | `current-account/cmd/main.go` | Critical |
| current-account imports position-keeping client | `current-account/cmd/main.go` | Critical |
| current-account imports internal-account client | `current-account/cmd/main.go` | Critical |
| financial-accounting imports internal-account | `financial-accounting/cmd/main.go` | High |
| internal-account imports position-keeping | `internal-account/cmd/main.go` | High |
| PartyClientWrapper hardcodes party status validation | `current-account/cmd/party_wrapper.go` | Medium |
| Frontend client-side account filtering | `frontend/.../accounts-tab.tsx` | High |

<!-- markdownlint-enable MD013 -->

### Architectural principle

Each BIAN service domain owns its data and exposes filters
for its own columns. Cross-domain resolution (e.g., "which
accounts belong to this party?") happens at the boundary -
the frontend or a future BFF - not inside individual
services. Services communicate via account_id strings, not by
importing each other's Go clients.

---

## Part 1: API Filtering

Expose what each service already owns. No cross-service
knowledge.

### 1. Add party_id and org_party_id to ListCurrentAccounts

The current_account table already has both columns. The
repository already has `ListByOrganization` internally.
Expose both as optional proto filter fields. When both
provided, AND them (accounts for this party within this org).

### 2. Add org_party_id to ListInternalAccounts

The internal_bank_account table already has an `org_party_id`
column. Expose it as an optional filter so org-scoped
internal accounts (clearing, nostro, holding) can be queried
directly.

### 3. Add account_ids to ListLedgerPostings

Financial-accounting already filters by single `account_id`.
Extend to accept multiple account IDs in one query. This is a
performance convenience - the caller resolves party ->
accounts, then passes the IDs. Financial-accounting has no
knowledge of parties.

### 4. Add account_ids to ListFinancialPositionLogs

Same pattern as financial-accounting. Position-keeping already
understands account_id. Multiple IDs in one call avoids N+1
queries from the frontend.

---

## Part 2: Service Boundary Cleanup

Remove cross-service Go imports. Move cross-domain resolution
to the boundary layer.

### 5. Remove party client import from current-account

current-account should not validate party existence or
status. Party validation belongs at the API boundary (the
handler that receives the "create account for party X"
request validates the party exists before calling
current-account). Remove `party_wrapper.go` and the party
client import.

### 6. Remove position-keeping import from current-account

Balance hydration (enriching account responses with balances)
should happen at the frontend or API gateway level, not
inside current-account. The frontend already has access to
position-keeping - it can query balances separately.

### 7. Remove financial-accounting import from current-account

If current-account triggers double-entry postings as part of
deposit/withdrawal, this should be orchestrated by a saga
(which already exists) rather than a direct client call. The
saga is the boundary that coordinates across services.

### 8. Remove internal-account import from current-account

Dynamic clearing account resolution should happen in the saga
or at the API boundary.

### 9. Remove internal-account import from financial-accounting

Same principle - clearing account resolution belongs in the
saga that orchestrates the transaction, not embedded inside
financial-accounting.

### 10. Remove position-keeping import from internal-account

Balance queries for internal accounts should be a separate
frontend call, not embedded in the internal-account service.

---

## Part 3: Frontend Navigation

With proper API filters in place, the frontend can navigate
cleanly.

### 11. Party detail Accounts tab: server-side filtering

Replace the client-side fetch-and-filter workaround with
`ListCurrentAccounts(party_id=X)`. For org-type parties, also
show org-scoped internal accounts via
`ListInternalAccounts(org_party_id=X)`.

### 12. Party detail Associations/Members tab

For ORGANIZATION-type parties, render a "Members" section
showing associated parties via `ListParticipants`. Display:
party name, relationship type, status, metadata summary. Each
row links to that member's party detail page.

For PERSON-type parties, show their org memberships (reverse
lookup - "Organizations this party belongs to"). This uses
existing `party_association` data queried from the party's
perspective.

### 13. Party detail Transactions tab (new)

Add a tab showing ledger postings across all of the party's
accounts. The frontend resolves party -> account IDs via the
Accounts tab data, then queries
`ListLedgerPostings(account_ids=[...])`. No cross-service
coupling - the frontend is the boundary that joins the data.

### 14. Org drill-down navigation

From an org's Members list, clicking a member party navigates
to that party's detail. The Accounts tab shows accounts
scoped to the org context (if navigated from an org) or all
accounts (if navigated directly).

---

## Scope

### In scope

- Proto filter additions to 4 existing endpoints
  (current-account, internal-account, financial-accounting,
  position-keeping)
- Backend handler/repository changes to implement the new
  filters
- Service boundary cleanup: remove 6 cross-service Go client
  imports
- Move cross-domain validation/resolution to saga layer or
  frontend
- Frontend: Accounts tab server-side filtering (current +
  internal accounts for orgs)
- Frontend: Members/Associations tab for org-type parties
- Frontend: Cross-account Transactions tab on party detail
- Frontend: Org membership view for person-type parties

### Out of scope

- Aggregate treasury views (net position across all org
  accounts) - requires BIAN Treasury Management component
  analysis
- Margin reporting (bill vs running costs, exposure
  tracking) - requires BIAN Margin Management component
  analysis
- New BFF/aggregation service (frontend acts as the boundary
  for now)
- Changes to the party association model itself (relationship
  types, metadata schema)
- New gRPC endpoints (adding filters to existing endpoints
  only)

---

## Success Criteria

### API filtering

1. `ListCurrentAccounts` supports server-side `party_id` and
   `org_party_id` filters - verified by integration test
1. `ListInternalAccounts` supports server-side `org_party_id`
   filter - verified by integration test
1. `ListLedgerPostings` supports filtering by multiple
   `account_ids` - verified by integration test
1. `ListFinancialPositionLogs` supports filtering by multiple
   `account_ids` - verified by integration test

### Service boundaries

1. current-account has zero cross-service Go client imports
   (party, financial-accounting, position-keeping,
   internal-account all removed)
1. financial-accounting has zero cross-service Go client
   imports (internal-account removed)
1. internal-account has zero cross-service Go client imports
   (position-keeping removed)
1. Cross-domain validation (party status checks) moved to
   saga or API boundary
1. All existing tests pass after decoupling (functionality
   preserved, just relocated)

### Frontend navigation

1. Party detail Accounts tab uses server-side filtering (no
   client-side fetch-all)
1. Org-type party detail shows both current and internal
   accounts scoped to the org
1. Org-type party detail shows member parties with clickable
   navigation
1. Person-type party detail shows org memberships
1. Party detail has a Transactions tab showing cross-account
   postings
1. Full drill-down works:
   Org -> Members -> Party -> Accounts -> Transactions

---

## Complexity Estimate

~21 points total:

- API filter additions (items 1-4): 5 points
  (4 endpoints, integration tests)
- Service boundary cleanup (items 5-10): 8 points
  (6 import removals, logic relocation to sagas/boundary,
  test preservation)
- Frontend Accounts tab refactor (item 11): 3 points
  (server-side filtering, internal accounts for orgs)
- Frontend Members/Associations tab (item 12): 3 points
  (new UI, two modes for org vs person)
- Frontend Transactions tab (items 13-14): 2 points
  (new tab, uses resolved account IDs)

Dependency graph:

- API filters (5) and service boundary cleanup (8) can run
  in parallel
- Frontend work (8) depends on API filters being complete
- Service boundary cleanup is independent of frontend work
- Critical path: max(API filters, boundary cleanup) ->
  Frontend = max(5, 8) -> 8 = 16 points
