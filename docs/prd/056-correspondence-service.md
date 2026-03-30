# Correspondence Service (BIAN-Aligned)

**Status:** Draft
**Version:** 1.0
**Author:** Platform Team
**Companion PRDs:** [052 - Email Platform](./052-email-platform.md),
[053 - Auth Email Flows](./053-auth-email-flows.md)
**ADR Reference:** [ADR-002](../adr/0002-microservices-per-bian-domain.md)
**Saga Contract:** [docs/spec/saga-contract.md](../spec/saga-contract.md)

---

## 1. Problem Statement

Meridian's `notification.send` saga handler uses inline parameter definitions
in `handlers.yaml` because no proto service exists for it. This is the last
non-composite handler without a `proto_ref`, breaking the Saga Contract's
principle that handler types are resolved from proto at schema load time.

PRD 052 defines the email infrastructure (outbox, worker, Resend integration)
but does not define the BIAN-aligned service boundary. The handler is called
`notification.send` - a generic name that doesn't align with BIAN's service
domain taxonomy.

BIAN v14 defines **Correspondence** as the service domain responsible for
outbound communication. This PRD creates a Correspondence service that:

1. Provides the proto-referenced handler for saga orchestration
2. Aligns with BIAN's Correspondence service domain taxonomy
3. Replaces the last non-composite inline handler in handlers.yaml
4. Supports multiple channel types (email initially, SMS/webhook later)

## 2. BIAN Alignment

### BIAN Correspondence Service Domain (v14)

The Correspondence service domain orchestrates the production and delivery of
formatted correspondence. BIAN defines the following behaviour qualifiers:

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
| `Update` | Modify pending correspondence | `UpdateOutbound` |
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
}

message InitiateOutboundRequest {
  // Channel: EMAIL, SMS, WEBHOOK (extensible)
  string channel = 1;
  // Recipient party ID (resolved to contact details server-side)
  string recipient_party_id = 2;
  // Template name (e.g., "invoice_delivery", "dunning_notice")
  string template_name = 3;
  // Template data (key-value pairs for template rendering)
  map<string, string> template_data = 4;
  // Idempotency key (auto-generated from saga execution if omitted)
  string idempotency_key = 5;
  // Priority: NORMAL, HIGH, LOW
  string priority = 6;
}

message InitiateOutboundResponse {
  string correspondence_id = 1;
  string status = 2;  // QUEUED, SENT, FAILED
}

message InitiateOutboundWithResponseRequest {
  string channel = 1;
  string recipient_party_id = 2;
  string template_name = 3;
  map<string, string> template_data = 4;
  string idempotency_key = 5;
  string priority = 6;
  // Expected response deadline (e.g., "P7D" for 7 days)
  string response_deadline = 7;
}

message InitiateOutboundWithResponseResponse {
  string correspondence_id = 1;
  string status = 2;
  string tracking_id = 3;  // For response matching
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
```

## 4. Handler Schema Migration

### Current (inline params)

```yaml
notification.send:
  description: "Send a notification (email) to a party"
  compensation_strategy: none
  params:
    type:
      type: string
      required: true
    recipient:
      type: string
      required: true
    template:
      type: string
      required: false
    data:
      type: map
      required: false
    idempotency_key:
      type: string
      required: false
```

### Target (proto-referenced)

```yaml
correspondence.initiate_outbound:
  description: "Initiate outbound correspondence (email, SMS, webhook)"
  compensation_strategy: none
  proto_ref:
    proto_rpc: "meridian.correspondence.v1.CorrespondenceService/InitiateOutbound"
    exposed_params:
      - channel
      - recipient_party_id
      - template_name
      - template_data
      - idempotency_key
      - priority
    param_aliases:
      recipient_party_id: recipient
      channel: type
      template_name: template

correspondence.initiate_outbound_with_response:
  description: "Initiate outbound correspondence with tracked response"
  compensation_strategy: none
  proto_ref:
    proto_rpc: "meridian.correspondence.v1.CorrespondenceService/InitiateOutboundWithResponse"

correspondence.retrieve_outbound:
  description: "Retrieve correspondence status"
  compensation_strategy: none
  proto_ref:
    proto_rpc: "meridian.correspondence.v1.CorrespondenceService/RetrieveOutbound"
```

### Backward Compatibility

The `param_aliases` section maps new proto field names to old saga parameter
names (`recipient` -> `recipient_party_id`, `type` -> `channel`,
`template` -> `template_name`). Existing saga scripts using
`notification.send(type="EMAIL", recipient=party_id, template="invoice")`
continue to work unchanged via the alias mechanism.

Saga scripts referencing `notification.send` will need to be updated to
`correspondence.initiate_outbound`. This is a search-and-replace in
cookbook patterns and default saga definitions.

## 5. Integration with PRD 052

PRD 052 defines the email infrastructure that this service wraps:

| PRD 052 Component | Correspondence Service Relationship |
|---|---|
| Email outbox table | Correspondence writes to it via InitiateOutbound |
| Resend worker | Reads from outbox, delivers via Resend API |
| Templates | Correspondence resolves template_name to template content |
| Webhooks (delivery status) | Updates correspondence status on delivery/bounce |
| Metrics | Correspondence exposes BIAN-aligned metrics |

The Correspondence service is the **gRPC entry point**. PRD 052's email
worker is the **delivery engine**. They are separate concerns:
Correspondence handles orchestration-layer contract, email worker handles
provider integration.

## 6. Scope

### In Scope

- Proto definition at `api/proto/meridian/correspondence/v1/`
- Minimal service stub in `services/correspondence/`
- Starlark service bindings (`RegisterStarlarkHandlers`)
- handlers.yaml migration (notification.send -> correspondence.initiate_outbound)
- Saga script updates (cookbook patterns referencing notification.send)
- Proto generation (`buf generate api/proto`)

### Out of Scope

- Email delivery infrastructure (PRD 052)
- Auth email flows (PRD 053)
- SMS/webhook channel implementations (future)
- BlockMailing batch operations (future)
- Inbound correspondence processing (future)

## 7. Success Criteria

1. `correspondence.initiate_outbound` in handlers.yaml with `proto_ref`
2. `notification.send` removed from handlers.yaml
3. All saga scripts updated to use `correspondence.*` namespace
4. Proto generation succeeds (`buf generate api/proto`)
5. Existing saga tests pass with the new handler namespace
6. Service follows standard Meridian service directory structure

## 8. Complexity Estimate

3 story points. Proto definition is straightforward. Service stub is
minimal (delegates to PRD 052 infrastructure). Main effort is the handler
migration and updating all saga script references.

| Task | Points | Deps |
|---|---|---|
| Proto + buf generate | 1 | - |
| Service stub + Starlark bindings | 1 | Proto |
| handlers.yaml + saga script migration | 1 | Service stub |
