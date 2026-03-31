# Correspondence Service (BIAN-Aligned)

**Status:** Draft
**Version:** 2.0
**Author:** Platform Team
**Companion PRDs:** [052 - Email Platform](./052-email-platform.md),
[053 - Auth Email Flows](./053-auth-email-flows.md)
**ADR Reference:** [ADR-002](../adr/0002-microservices-per-bian-domain.md)
**Saga Contract:** Saga Contract Specification (PR #2051)

---

## 1. Problem Statement

Meridian's email and notification capabilities are scattered across three
locations with no single service owning the domain:

| Component | Current Location | Problem |
|---|---|---|
| `notification.send` saga handler | `shared/pkg/email/notification_handler.go` | Inline params in handlers.yaml (no proto_ref), registered as a local handler in current-account service |
| Resend webhook handler | `services/api-gateway/resend_webhook_handler.go` | Delivery status callbacks owned by the API gateway, not the correspondence domain |
| Email outbox + worker + sender | `shared/pkg/email/` | Shared package with no gRPC service boundary, no BIAN alignment |
| Audit trail | `shared/pkg/email/audit_postgres.go` | Delivery audit lives in shared package, not owned by a service |

### Latent Bugs (Pre-existing)

Investigation during PRD review revealed three bugs that exist today:

1. **Identity outbox never polled** - `wire_identity.go:20` creates
   outbox repo against the identity DB, but the email worker only polls
   the payment-order DB. Identity-originated emails (invites, lockout
   notifications) are silently lost.

2. **notification.send handler never wired** - `WithNotificationHandler`
   is never called in `cmd/meridian/`. The 5 `notification.send` calls
   in dunning saga scripts fail silently with `errHandlerNotImplemented`.
   We are not migrating a working system - we are completing an
   unfinished one.

3. **Complaint feedback loop broken** - Webhook handler records
   COMPLAINED/BOUNCED in audit log, but the worker never reads it back.
   Complained addresses get re-emailed, risking domain reputation
   degradation. Gmail/Yahoo mandate < 0.3% complaint rates.

### Issues

1. **No Saga Contract compliance** - `notification.send` is the last
   non-composite handler using inline params instead of proto_ref
2. **No service boundary** - email logic is a shared package injected
   into current-account, not a service that can be independently
   deployed, scaled, or monitored
3. **No BIAN alignment** - BIAN v14 defines Correspondence as a service
   domain; we're using ad-hoc naming
4. **No communication preferences** - no consent management, unsubscribe
   capability, or GDPR right-to-erasure support for party communications
5. **Webhook misownership** - Resend delivery callbacks are handled by
   the API gateway, which has no domain knowledge of correspondence
6. **No category enforcement** - saga scripts self-declare the category
   (TRANSACTIONAL, MARKETING, OPERATIONAL) with no server-side
   validation that the content matches the declared category

## 2. BIAN Alignment

### BIAN Correspondence Service Domain (v14)

The Correspondence service domain orchestrates the production and delivery
of formatted correspondence. BIAN defines the following behaviour
qualifiers:

| Behaviour Qualifier | BIAN Purpose | Meridian Mapping |
|---|---|---|
| **Outbound** | Fire-and-forget outbound items | Email notifications, invoice delivery |
| **OutboundWithResponse** | Outbound with tracked response | Payment confirmations requiring acknowledgement |
| **Inbound** | Inbound correspondence processing | Future: webhook/reply handling |
| **BlockMailing** | Batch correspondence | Billing run email batches, dunning campaigns |

### BIAN Action Terms

| Action Term | BIAN Meaning | Meridian RPC |
|---|---|---|
| `Initiate` | Create new outbound correspondence | `InitiateOutbound` |
| `Retrieve` | Get correspondence status/details | `RetrieveOutbound` |
| `Update` | Modify pending correspondence | Future (out of scope) |
| `Execute` | Run automated task (e.g., batch send) | Future (`ExecuteBlockMailing`) |

### Service Domain Naming

Following Meridian's convention (ADR-002: microservices per BIAN domain):

- Proto package: `meridian.correspondence.v1`
- Service name: `CorrespondenceService`
- Go module: `services/correspondence/`
- Handler namespace: `correspondence` (replaces `notification`)

## 3. Phased Delivery

Each phase ships independently and delivers value. Phase 0 fixes
production bugs that exist today regardless of this PRD. The PRD serves
as the architectural north star for all phases.

### Phase 0: Bug Fixes (3 points)

Fix the three latent bugs. No new architecture - just wire what exists.

| Task | Description |
|---|---|
| Wire notification handler | Call `WithNotificationHandler` in `cmd/meridian/` so dunning saga `notification.send` calls work |
| Fix identity outbox | Add a second `dispatch.Worker[EmailOutboxRow]` instance polling the identity DB. Do NOT consolidate to a single outbox DB (violates ADR-0002 database-per-service). Each service DB that originates emails must have its own dedicated worker. |
| Complaint suppression | Worker checks audit trail for COMPLAINED/BOUNCED before sending; `suppressed_addresses` table |

### Phase 1: Deliverability (3 points)

Protect domain reputation and relocate webhook ownership.

| Task | Description |
|---|---|
| `suppressed_addresses` table | Pre-send check against bounced/complained addresses |
| Webhook relocation | Move Resend webhook handler from api-gateway to correspondence domain (gRPC `RecordDeliveryStatus`) |
| Complaint rate metric | Per-tenant complaint rate Prometheus metric; alert on > 0.1% |

### Phase 2: Domain Model + GDPR (8 points)

Proto definition, handler migration, communication preferences.

| Task | Description |
|---|---|
| Proto + buf generate | Define `meridian.correspondence.v1.CorrespondenceService` |
| handlers.yaml migration | Replace `notification.send` (inline) with `correspondence.initiate_outbound` (proto_ref) with `param_aliases` for backward compat |
| Saga script updates | Update dunning_escalation, dunning_unfreeze (atomic with handlers.yaml change) |
| Operational gateway route migration | Data migration for persisted `notification.send` instruction types |
| Communication preferences | Preferences table with consent provenance, enforcement in InitiateOutbound |
| Category enforcement | `CorrespondenceCategory` enum + `templateCategoryMap` binding to prevent self-declaration bypass |
| Unsubscribe headers | RFC 2369 (`List-Unsubscribe`) + RFC 8058 (`List-Unsubscribe-Post`) on all non-transactional emails |
| Template modifications | Add unsubscribe links, update `template_data` struct for 18 template files |

### Phase 3: Service Extraction (8 points, deferred)

Full service boundary. Deferred until multi-channel support or
independent scaling justifies the operational overhead.

**Outbox coordination (ADR-0002 constraint):** Today `payment-order`
writes invoice + outbox row in one ACID transaction. Extracting the
outbox to a separate `meridian_correspondence` DB breaks this guarantee.
Phase 3 must move to saga-driven coordination: the invoice generation
saga explicitly calls `correspondence.initiate_outbound` as a distinct
step with compensation, replacing the cross-service atomic DB write.
This is the natural Meridian pattern - the saga engine already exists.

| Task | Description |
|---|---|
| Service scaffold | `services/correspondence/` with standard directory structure, gRPC server, migrations, Atlas config |
| Code relocation | Move 26 Go files from `shared/pkg/email/` (outbox, audit, sender, templates, worker) |
| Import rewiring | Update 29+ import sites across the codebase |
| gRPC endpoints | `InitiateOutbound` wraps existing handler logic, `RecordDeliveryStatus` wraps webhook logic |
| Saga-driven outbox | Replace atomic cross-DB writes with saga step calls to `correspondence.initiate_outbound` |
| Starlark bindings | `RegisterStarlarkHandlers` for `correspondence.*` namespace |
| Integration tests | End-to-end saga execution through the new service boundary |

## 4. Proto Definition

```protobuf
syntax = "proto3";
package meridian.correspondence.v1;

import "google/protobuf/struct.proto";
import "google/protobuf/timestamp.proto";
import "meridian/common/v1/types.proto";

enum CorrespondenceCategory {
  CORRESPONDENCE_CATEGORY_UNSPECIFIED = 0;
  CORRESPONDENCE_CATEGORY_TRANSACTIONAL = 1;
  CORRESPONDENCE_CATEGORY_OPERATIONAL = 2;
  CORRESPONDENCE_CATEGORY_MARKETING = 3;
}

service CorrespondenceService {
  rpc InitiateOutbound(InitiateOutboundRequest)
      returns (InitiateOutboundResponse);

  rpc InitiateOutboundWithResponse(InitiateOutboundWithResponseRequest)
      returns (InitiateOutboundWithResponseResponse);

  rpc RetrieveOutbound(RetrieveOutboundRequest)
      returns (RetrieveOutboundResponse);

  rpc RecordDeliveryStatus(RecordDeliveryStatusRequest)
      returns (RecordDeliveryStatusResponse);

  rpc GetCommunicationPreferences(GetCommunicationPreferencesRequest)
      returns (GetCommunicationPreferencesResponse);

  rpc UpdateCommunicationPreferences(UpdateCommunicationPreferencesRequest)
      returns (UpdateCommunicationPreferencesResponse);
}

message InitiateOutboundRequest {
  string channel = 1;
  string recipient_party_id = 2;
  string template_name = 3;
  google.protobuf.Struct template_data = 4;
  meridian.common.v1.IdempotencyKey idempotency_key = 5;
  string priority = 6;
  CorrespondenceCategory category = 7;
}

message InitiateOutboundResponse {
  string correspondence_id = 1;
  string status = 2;
  string suppression_reason = 3;
}

message InitiateOutboundWithResponseRequest {
  string channel = 1;
  string recipient_party_id = 2;
  string template_name = 3;
  google.protobuf.Struct template_data = 4;
  meridian.common.v1.IdempotencyKey idempotency_key = 5;
  string priority = 6;
  CorrespondenceCategory category = 7;
  google.protobuf.Timestamp response_deadline = 8;
}

message InitiateOutboundWithResponseResponse {
  string correspondence_id = 1;
  string status = 2;
  string suppression_reason = 3;
  string tracking_id = 4;
}

message RetrieveOutboundRequest {
  string correspondence_id = 1;
}

message RetrieveOutboundResponse {
  string correspondence_id = 1;
  string status = 2;
  string channel = 3;
  string recipient_party_id = 4;
  string template_name = 5;
  google.protobuf.Timestamp created_at = 6;
  google.protobuf.Timestamp sent_at = 7;
  google.protobuf.Timestamp delivered_at = 8;
}

message RecordDeliveryStatusRequest {
  string provider_id = 1;
  string status = 2;
  google.protobuf.Struct provider_metadata = 3;
}

message RecordDeliveryStatusResponse {
  string correspondence_id = 1;
  string status = 2;
}

message GetCommunicationPreferencesRequest {
  string party_id = 1;
}

message GetCommunicationPreferencesResponse {
  string party_id = 1;
  repeated ChannelPreference preferences = 2;
  bool global_unsubscribe = 3;
}

message ChannelPreference {
  string channel = 1;
  CorrespondenceCategory category = 2;
  bool opted_in = 3;
  google.protobuf.Timestamp updated_at = 4;
  string consent_source = 5;
  google.protobuf.Timestamp consent_granted_at = 6;
  string consent_text = 7;
}

message UpdateCommunicationPreferencesRequest {
  string party_id = 1;
  optional bool global_unsubscribe = 2;
  repeated ChannelPreference preferences = 3;
}

message UpdateCommunicationPreferencesResponse {
  string party_id = 1;
  bool global_unsubscribe = 2;
}
```

Key design decisions:
- `category` uses a proto enum (`CorrespondenceCategory`) not a string,
  preventing invalid categories at the wire level
- `template_data` uses `google.protobuf.Struct` (not `map<string, string>`)
  to support mixed types - existing dunning scripts pass integers
- `ChannelPreference` includes consent provenance fields as NOT NULL in
  the migration - you cannot create a preference without documenting
  provenance (GDPR Art. 7(1) requirement)

## 5. Communication Preferences (GDPR)

### Requirements

| Requirement | Implementation |
|---|---|
| **Right to object (Art. 21)** | `global_unsubscribe` flag. When set, all non-transactional comms suppressed. |
| **Unsubscribe per channel/category** | `ChannelPreference` records per party. |
| **Transactional exemption** | `TRANSACTIONAL` category bypasses all preferences. |
| **Right to erasure (Art. 17)** | Preferences deletable via party lifecycle. Audit trail redacted, not deleted (7-year financial regulation). |
| **Consent demonstration (Art. 7(1))** | Append-only preference table IS the audit trail. `consent_source`, `consent_granted_at`, `consent_text` are NOT NULL. Single SELECT proves consent existed. |

### Enforcement Point

`InitiateOutbound` checks preferences before queuing:

```text
1. Validate category against templateCategoryMap (server-side,
   prevents self-declaration bypass)
2. Resolve party preferences
3. If global_unsubscribe AND category != TRANSACTIONAL → SUPPRESSED
4. If channel+category explicitly opted out → SUPPRESSED
5. If category == TRANSACTIONAL → allow (legally required)
6. If category == OPERATIONAL AND no preference record → allow
   (legitimate interest, Art. 6(1)(f))
7. If category == MARKETING AND no preference record → SUPPRESSED
   (explicit consent required, Art. 7)
8. Check suppressed_addresses for bounce/complaint → SUPPRESSED
9. Queue to outbox
```

Suppressed correspondence is recorded in the audit trail with the
GDPR article reference that triggered suppression.

### Category Enforcement

A server-side `templateCategoryMap` binds template names to their
allowed categories. This prevents a saga script from declaring a
marketing template as TRANSACTIONAL to bypass preferences:

```go
var templateCategoryMap = map[string]CorrespondenceCategory{
    "dunning-notice":        TRANSACTIONAL,
    "account-frozen":        TRANSACTIONAL,
    "payment-confirmation":  TRANSACTIONAL,
    "invoice-delivery":      TRANSACTIONAL,
    "dunning-resolved":      OPERATIONAL,
    "welcome":               OPERATIONAL,
    "service-update":        OPERATIONAL,
    "promotional-offer":     MARKETING,
}
```

If a saga declares `category=TRANSACTIONAL` but the template maps to
`MARKETING`, the request is rejected with a validation error.

### Category Definitions

- `TRANSACTIONAL`: Legally required communications (invoices, account
  freezes, dunning notices, password resets). Cannot be suppressed.
- `OPERATIONAL`: Service-related communications sent under legitimate
  interest (Art. 6(1)(f)) - welcome emails, service updates, account
  notifications. Allowed by default, party can opt out.
- `MARKETING`: Promotional communications requiring explicit opt-in
  consent (Art. 7). Suppressed by default, party must opt in.

### Unsubscribe Mechanism

Every non-transactional email includes both `List-Unsubscribe`
(RFC 2369) and `List-Unsubscribe-Post` (RFC 8058) headers. Both are
required - RFC 8058 Section 3.1 uses MUST language for the companion
header. Gmail and Yahoo's 2024 sender requirements mandate both for
bulk/marketing email.

The unsubscribe link calls `UpdateCommunicationPreferences` via the
API gateway.

## 6. Backward Compatibility

### Handler Name Transition

During Phase 2, both handler names can coexist:

1. **Phase 2 (dual support)**: Retain `notification.send` as a
   deprecated alias in handlers.yaml mapping to the same proto_ref
2. **Phase 2 (data migration)**: Update persisted operational gateway
   route/instruction-type records
3. **Phase 2 (removal)**: After all tenants migrated, remove alias

### Parameter Aliases

The `param_aliases` mechanism maps old saga parameter names to new
proto fields:

```yaml
correspondence.initiate_outbound:
  proto_ref:
    proto_rpc: "meridian.correspondence.v1.CorrespondenceService/InitiateOutbound"
    param_aliases:
      recipient_party_id: recipient
      channel: type
      template_name: template
      template_data: data
```

## 7. Integration with PRD 052

PRD 052 defined the email infrastructure. This PRD phases its promotion
to a BIAN-aligned service:

- **Phase 0-1**: PRD 052 infrastructure stays in `shared/pkg/email/`,
  bugs are fixed in place
- **Phase 2**: Proto definition and handler migration add the domain
  model on top of existing infrastructure
- **Phase 3**: Physical service extraction moves code to
  `services/correspondence/`

## 8. Success Criteria

### Phase 0
1. Dunning saga `notification.send` calls work end-to-end
2. Identity-originated emails reach the outbox
3. Complained/bounced addresses are not re-emailed

### Phase 2
4. `correspondence.initiate_outbound` in handlers.yaml with `proto_ref`
5. `notification.send` removed (or deprecated alias only)
6. Category enforcement prevents self-declaration bypass
7. Communication preferences enforced on non-transactional correspondence
8. Unsubscribe headers (RFC 2369 + 8058) on all non-transactional emails
9. Proto generation succeeds (`buf generate api/proto`)
10. All existing and new tests pass

### Phase 3
11. `shared/pkg/email/` reduced to interface + types only
12. Service follows standard Meridian service directory structure
13. Resend webhook handled by Correspondence service via gRPC

## 9. Complexity Estimate

~22 story points total across all phases.

| Phase | Task | Points | Deps |
|---|---|---|---|
| **0** | Wire notification handler | 1 | - |
| **0** | Fix identity outbox wiring | 1 | - |
| **0** | Complaint suppression (suppressed_addresses + pre-send check) | 1 | - |
| **1** | Webhook relocation + RecordDeliveryStatus | 2 | Phase 0 |
| **1** | Per-tenant complaint rate metric | 1 | Phase 0 |
| **2** | Proto + buf generate + CorrespondenceCategory enum | 1 | - |
| **2** | handlers.yaml + saga script migration (atomic) | 1 | Proto |
| **2** | Operational gateway route migration | 1 | Proto |
| **2** | Communication preferences + consent provenance | 3 | Proto |
| **2** | Category enforcement (templateCategoryMap) | 1 | Proto |
| **2** | Unsubscribe headers (RFC 2369 + 8058) + template modifications | 2 | Preferences |
| **3** | Service scaffold + migrations | 2 | Phase 2 |
| **3** | Code relocation (26 files, 29+ import sites) | 3 | Scaffold |
| **3** | gRPC endpoints + Starlark bindings + integration tests | 3 | Relocation |

Phase 0 tasks are independent and can run in parallel. Phase 3 is
deferred until multi-channel or independent scaling justifies the
operational overhead of a separate service boundary.

## 10. Future Considerations (Out of Scope)

- **SMS/webhook channel implementations** - interface ready via
  `channel` field
- **BlockMailing batch operations** - BIAN behaviour qualifier defined
  but not implemented
- **Inbound correspondence processing** - future BIAN behaviour qualifier
- **Self-service preference management UI** - API ready
- **Auth email flows** - PRD 053, uses shared `Sender` interface directly
- **Tenant-configurable templates** - tenants will eventually want to
  author their own email templates or bring their own provider API keys.
  Templates and provider credentials should move into the Tenant Manifest
  (similar to `payment_rails` and `operational_gateway.provider_connections`
  in PRD-033). Not required for initial phases.
- **CAN-SPAM compliance** - physical address in footer, explicit opt-out
  mechanism for US recipients. Can be addressed when US market is active.
