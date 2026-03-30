# Correspondence Service (BIAN-Aligned)

**Status:** Draft
**Version:** 1.1
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

This creates several issues:

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
| `Execute` | Run automated task (e.g., batch send) | `ExecuteBlockMailing` |

### Service Domain Naming

Following Meridian's convention (ADR-002: microservices per BIAN domain):

- Proto package: `meridian.correspondence.v1`
- Service name: `CorrespondenceService`
- Go module: `services/correspondence/`
- Handler namespace: `correspondence` (replaces `notification`)

## 3. Proto Definition

```protobuf
syntax = "proto3";
package meridian.correspondence.v1;

import "google/protobuf/struct.proto";

service CorrespondenceService {
  // Initiate outbound correspondence (fire-and-forget)
  rpc InitiateOutbound(InitiateOutboundRequest)
      returns (InitiateOutboundResponse);

  // Initiate outbound with tracked response
  rpc InitiateOutboundWithResponse(InitiateOutboundWithResponseRequest)
      returns (InitiateOutboundWithResponseResponse);

  // Retrieve correspondence status
  rpc RetrieveOutbound(RetrieveOutboundRequest)
      returns (RetrieveOutboundResponse);

  // Record delivery status from provider webhook
  rpc RecordDeliveryStatus(RecordDeliveryStatusRequest)
      returns (RecordDeliveryStatusResponse);

  // Check communication preferences for a party
  rpc GetCommunicationPreferences(GetCommunicationPreferencesRequest)
      returns (GetCommunicationPreferencesResponse);

  // Update communication preferences (subscribe/unsubscribe)
  rpc UpdateCommunicationPreferences(UpdateCommunicationPreferencesRequest)
      returns (UpdateCommunicationPreferencesResponse);
}

message InitiateOutboundRequest {
  // Channel: EMAIL, SMS, WEBHOOK (extensible)
  string channel = 1;
  // Recipient party ID (resolved to contact details server-side)
  string recipient_party_id = 2;
  // Template name (e.g., "invoice_delivery", "dunning_notice")
  string template_name = 3;
  // Template data (key-value pairs for template rendering, supports mixed types)
  google.protobuf.Struct template_data = 4;
  // Idempotency key (auto-generated from saga execution if omitted)
  string idempotency_key = 5;
  // Priority: NORMAL, HIGH, LOW
  string priority = 6;
  // Category for preference checking: TRANSACTIONAL, MARKETING, OPERATIONAL
  // TRANSACTIONAL bypasses unsubscribe (legally required comms)
  // MARKETING and OPERATIONAL respect party preferences
  string category = 7;
}

message InitiateOutboundResponse {
  string correspondence_id = 1;
  // QUEUED, SENT, SUPPRESSED (party opted out), FAILED
  string status = 2;
  // When suppressed, explains why (e.g., "party_unsubscribed")
  string suppression_reason = 3;
}

message InitiateOutboundWithResponseRequest {
  string channel = 1;
  string recipient_party_id = 2;
  string template_name = 3;
  map<string, string> template_data = 4;
  string idempotency_key = 5;
  string priority = 6;
  string category = 7;
  // Expected response deadline (e.g., "P7D" for 7 days)
  string response_deadline = 8;
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
  string created_at = 6;
  string sent_at = 7;
  string delivered_at = 8;
}

message RecordDeliveryStatusRequest {
  string provider_id = 1;
  // DELIVERED, BOUNCED, COMPLAINED
  string status = 2;
  map<string, string> provider_metadata = 3;
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
  // Per-channel, per-category preferences
  repeated ChannelPreference preferences = 2;
  // GDPR: if true, all non-transactional comms are suppressed
  bool global_unsubscribe = 3;
}

message ChannelPreference {
  string channel = 1;    // EMAIL, SMS, WEBHOOK
  string category = 2;   // MARKETING, OPERATIONAL, TRANSACTIONAL
  bool opted_in = 3;
  string updated_at = 4;
}

message UpdateCommunicationPreferencesRequest {
  string party_id = 1;
  // Set global unsubscribe (GDPR right to object)
  optional bool global_unsubscribe = 2;
  // Per-channel, per-category opt-in/opt-out
  repeated ChannelPreference preferences = 3;
}

message UpdateCommunicationPreferencesResponse {
  string party_id = 1;
  bool global_unsubscribe = 2;
}
```

## 4. Refactoring Plan

### What Moves

| From | To | Notes |
|---|---|---|
| `shared/pkg/email/notification_handler.go` | `services/correspondence/handler/` | Saga handler becomes a gRPC client call instead of local function |
| `shared/pkg/email/outbox_*.go` | `services/correspondence/adapters/persistence/` | Outbox is now owned by the service |
| `shared/pkg/email/audit_*.go` | `services/correspondence/adapters/persistence/` | Audit trail owned by the service |
| `shared/pkg/email/resend.go` | `services/correspondence/adapters/provider/` | Resend adapter behind a `Sender` interface |
| `shared/pkg/email/sender*.go` | `services/correspondence/adapters/provider/` | Sender interface + factory |
| `shared/pkg/email/template*.go` | `services/correspondence/templates/` | Template rendering |
| `shared/pkg/email/worker/` | `services/correspondence/worker/` | Email dispatch worker |
| `services/api-gateway/resend_webhook_handler.go` | `services/correspondence/webhook/` | API gateway proxies to Correspondence service via gRPC `RecordDeliveryStatus` |

### What Stays

| Component | Why |
|---|---|
| `shared/pkg/email/` package | Becomes thin - just the `Message` type and `Sender` interface for any service that needs to send email directly (e.g., auth verification in identity service). The interface stays shared; the implementation moves. |

### Migration Strategy

1. **Create service scaffold** - standard Meridian service directory
   structure with gRPC server, migrations, Atlas config
2. **Move email package internals** - outbox, audit, sender, templates,
   worker into the new service
3. **Implement gRPC endpoints** - `InitiateOutbound` wraps the existing
   notification handler logic, `RecordDeliveryStatus` wraps the existing
   webhook handler logic
4. **Register Starlark bindings** - `correspondence.initiate_outbound`
   registered with the saga handler registry
5. **Update handlers.yaml** - replace `notification.send` (inline) with
   `correspondence.initiate_outbound` (proto_ref)
6. **Migrate saga scripts** - update dunning_escalation and
   dunning_unfreeze to use `correspondence.initiate_outbound`
7. **Proxy webhook** - API gateway's `/api/v1/webhooks/resend` route
   calls Correspondence service's `RecordDeliveryStatus` gRPC endpoint
   instead of writing directly to the audit table
8. **Add communication preferences** - new table, preference check in
   InitiateOutbound before queuing

### Backward Compatibility

The `param_aliases` mechanism in handlers.yaml maps old saga parameter
names to new proto fields:

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

Existing saga scripts using `notification.send(type="EMAIL", recipient=party_id)`
work unchanged via aliases during migration. Scripts are updated to use
`correspondence.initiate_outbound(channel="EMAIL", recipient_party_id=party_id)`
as a follow-up.

## 5. Communication Preferences (GDPR)

### Requirements

| Requirement | Implementation |
|---|---|
| **Right to object (Art. 21)** | `global_unsubscribe` flag on party preferences. When set, all non-transactional comms suppressed. |
| **Unsubscribe per channel/category** | `ChannelPreference` records per party. Marketing emails can be opted out while keeping transactional. |
| **Transactional exemption** | Correspondence with `category=TRANSACTIONAL` (invoices, account freezes, password resets) bypasses preferences. Legally required comms cannot be suppressed. |
| **Right to erasure (Art. 17)** | Preferences and correspondence history deletable via party lifecycle. Audit trail retained per financial regulation (7 years) - redacted, not deleted. |
| **Preference audit trail** | Every preference change recorded with timestamp, source (party self-service, admin, API), and previous value. |

### Enforcement Point

`InitiateOutbound` checks preferences before queuing:

```text
1. Resolve party preferences
2. If global_unsubscribe AND category != TRANSACTIONAL → SUPPRESSED
3. If channel+category opted out → SUPPRESSED
4. If no preference record AND category == TRANSACTIONAL → allow (legally required)
5. If no preference record AND category != TRANSACTIONAL → SUPPRESSED (GDPR Art. 7: no consent, no send)
6. Otherwise → queue to outbox
```

Suppressed correspondence is still recorded in the audit trail (proof
that the system respected the preference).

### Unsubscribe Mechanism

Every non-transactional email includes an unsubscribe link. The link
calls `UpdateCommunicationPreferences` via the API gateway. One-click
unsubscribe uses the `List-Unsubscribe-Post` header (RFC 8058) for
email client native support.

## 6. Integration with PRD 052

PRD 052 defined the email infrastructure. This PRD promotes it to a
BIAN-aligned service:

| PRD 052 Component | Correspondence Service Relationship |
|---|---|
| Email outbox table | Moves to `services/correspondence/migrations/` |
| Resend worker | Moves to `services/correspondence/worker/` |
| Templates | Moves to `services/correspondence/templates/` |
| Webhook handler | Moves from api-gateway, proxied via gRPC |
| Metrics | Adapted with BIAN-aligned labels |

PRD 052 is effectively **absorbed** into this service. The infrastructure
it defined becomes the implementation of the Correspondence service domain.

## 7. Scope

### In Scope

- Service scaffold: `services/correspondence/` with standard directory structure
- Proto definition at `api/proto/meridian/correspondence/v1/`
- gRPC implementation: InitiateOutbound, InitiateOutboundWithResponse,
  RetrieveOutbound, RecordDeliveryStatus, GetCommunicationPreferences,
  UpdateCommunicationPreferences
- Move email package internals (outbox, audit, sender, templates, worker)
- Starlark service bindings (`RegisterStarlarkHandlers`)
- handlers.yaml migration (notification.send -> correspondence.initiate_outbound)
- Saga script updates (dunning_escalation, dunning_unfreeze)
- Webhook proxy: api-gateway -> Correspondence gRPC
- Communication preferences table + GDPR enforcement
- Unsubscribe link generation in non-transactional emails
- Atlas migrations for correspondence + preferences tables

### Out of Scope

- SMS/webhook channel implementations (future - interface ready)
- BlockMailing batch operations (future)
- Inbound correspondence processing (future)
- Self-service preference management UI (future - API ready)
- Auth email flows (PRD 053 - uses shared Sender interface directly)

## 8. Success Criteria

1. `correspondence.initiate_outbound` in handlers.yaml with `proto_ref`
2. `notification.send` removed from handlers.yaml
3. All saga scripts use `correspondence.*` namespace
4. Resend webhook handled by Correspondence service (api-gateway proxies)
5. Communication preferences enforced on non-transactional correspondence
6. Unsubscribe link in all marketing/operational emails
7. `shared/pkg/email/` reduced to interface + types only
8. Proto generation succeeds (`buf generate api/proto`)
9. All existing and new tests pass
10. Service follows standard Meridian service directory structure

## 9. Complexity Estimate

8 story points (tasks sum to 9 but proto + scaffold parallelize).
The main effort is moving code without breaking existing functionality
and wiring the preference checks.

| Task | Points | Deps |
|---|---|---|
| Proto + buf generate | 1 | - |
| Service scaffold + migrations | 1 | Proto |
| Move email internals + gRPC implementation | 2 | Scaffold |
| handlers.yaml + saga script migration | 1 | gRPC impl |
| Webhook proxy refactor | 1 | gRPC impl |
| Communication preferences + GDPR | 2 | gRPC impl |
| Starlark bindings + integration tests | 1 | All above |

**Note:** Some tasks parallelize. Proto + scaffold can run with preference
table design. Webhook proxy is independent of saga migration.
