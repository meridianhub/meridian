# Calling Meridian APIs

This guide explains how to call Meridian backend services using HTTP/JSON, Connect, and gRPC.

## Overview

Meridian exposes its gRPC services through an HTTP gateway that supports four protocols:

| Protocol | Port | Content-Type | Best for |
|----------|------|--------------|----------|
| REST/JSON | 8090 | `application/json` | curl, browsers, any HTTP client |
| Connect | 8090 | `application/connect+json` | Browser clients needing RPC semantics |
| gRPC-Web | 8090 | `application/grpc-web+proto` | Legacy proxies, browsers without HTTP/2 |
| Native gRPC | 50051 | — (gRPC framing) | Server-to-server, grpcurl, Go clients |

The gateway automatically selects the transcoding path from the request `Content-Type` header.
Backend services always receive native gRPC; protocol translation happens at the gateway edge.

## Prerequisites

Start the dev stack:

```bash
make dev-up
```

This starts CockroachDB, runs schema migrations, and starts the Meridian binary.

Verify the gateway is running:

```bash
curl -s http://localhost:8090/healthz
# OK
```

## HTTP/JSON (REST)

The JSON API follows RESTful conventions. URL paths and HTTP methods come from `google.api.http`
annotations in the proto files; the same annotations generate the OpenAPI spec in
`api/openapi/meridian.swagger.json`.

### Authentication and Tenant Identification

In local development mode (`AUTH_MODE=disabled`), authentication is disabled. Pass the tenant
identifier via the `X-Tenant-ID` header:

```bash
curl -H "X-Tenant-ID: default" http://localhost:8090/v1/parties
```

In production, include a signed JWT in the `Authorization: Bearer <token>` header. The gateway
validates the token, extracts the tenant from the `tenant_id` claim, and propagates the identity
to backend services.

### Quick Start: Register a Party and Open an Account

```bash
# 1. Register a party
PARTY=$(curl -s -X POST http://localhost:8090/v1/parties \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: default" \
  -d '{"partyType": "PARTY_TYPE_PERSON", "legalName": "Alice Smith"}')
echo $PARTY | jq .
PARTY_ID=$(echo $PARTY | jq -r '.party.partyId')

# 2. Open a current account (GBP)
ACCOUNT=$(curl -s -X POST http://localhost:8090/v1/current-accounts \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: default" \
  -d "{
    \"partyId\": \"$PARTY_ID\",
    \"accountIdentification\": \"GB29NWBK60161331926819\",
    \"baseCurrency\": \"CURRENCY_GBP\"
  }")
echo $ACCOUNT | jq .
ACCOUNT_ID=$(echo $ACCOUNT | jq -r '.facility.accountId')

# 3. Deposit 100 GBP
curl -s -X POST "http://localhost:8090/v1/current-accounts/$ACCOUNT_ID/deposits" \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: default" \
  -d '{
    "amount": {
      "amount": {"currencyCode": "GBP", "units": "100"}
    },
    "description": "Initial deposit",
    "reference": "REF-001",
    "idempotencyKey": {"key": "dep-001"}
  }' | jq .

# 4. Check balances
curl -s "http://localhost:8090/v1/accounts/$ACCOUNT_ID/balances" \
  -H "X-Tenant-ID: default" | jq .
```

### Field Naming

Proto field names use `snake_case`. The gateway maps them to `camelCase` in JSON responses
following the [proto JSON mapping](https://protobuf.dev/programming-guides/proto3/#json) convention.

Request bodies accept both `camelCase` and `snake_case` field names.

### Idempotency

Mutating operations (POST/PATCH) accept an optional `idempotencyKey` in the request body:

```json
{
  "idempotencyKey": {"key": "unique-client-chosen-key"}
}
```

Submitting the same key twice returns the original response without reprocessing the operation.
Use UUIDs or deterministic keys based on your client's operation context.

### Error Format

Errors follow Google's canonical JSON error format:

```json
{
  "code": 5,
  "message": "party not found",
  "details": []
}
```

The `code` field is the gRPC status code integer (3 = InvalidArgument, 5 = NotFound, etc.).
HTTP status codes are mapped from gRPC status codes:

| gRPC Code | HTTP Status |
|-----------|-------------|
| OK (0) | 200 |
| InvalidArgument (3) | 400 |
| NotFound (5) | 404 |
| AlreadyExists (6) | 409 |
| PermissionDenied (7) | 403 |
| Unauthenticated (16) | 401 |
| Internal (13) | 500 |
| Unavailable (14) | 503 |

## Native gRPC

Backend services expose native gRPC on port 50051. Use this for server-to-server communication
or tooling like grpcurl. All services have reflection enabled in development.

```bash
# List all services
grpcurl -plaintext localhost:50051 list

# List methods for a service
grpcurl -plaintext localhost:50051 list meridian.party.v1.PartyService

# Describe a request message
grpcurl -plaintext localhost:50051 describe meridian.party.v1.RegisterPartyRequest

# Register a party
grpcurl -plaintext \
  -H "x-tenant-id: default" \
  -d '{"partyType": "PARTY_TYPE_PERSON", "legalName": "Alice Smith"}' \
  localhost:50051 meridian.party.v1.PartyService/RegisterParty

# Open a current account
grpcurl -plaintext \
  -H "x-tenant-id: default" \
  -d '{"partyId": "PARTY_ID", "accountIdentification": "GB29NWBK60161331926819", "baseCurrency": "CURRENCY_GBP"}' \
  localhost:50051 meridian.current_account.v1.CurrentAccountService/InitiateCurrentAccount
```

## Connect Protocol

The Connect protocol is compatible with HTTP/1.1 and HTTP/2. It is the recommended choice for
browser clients that need RPC-style semantics (unary and server-streaming calls).

Use the `connectrpc.com/connect` Go client or the [connect-es](https://connectrpc.com/docs/web/getting-started)
TypeScript client:

```http
POST http://localhost:8090/api/meridian.party.v1.PartyService/RegisterParty
Content-Type: application/connect+json

{"partyType": "PARTY_TYPE_PERSON", "legalName": "Alice Smith"}
```

Note the `/api/` prefix and the fully-qualified service path. The gateway strips `/api` before
dispatching to Vanguard.

## gRPC-Web

gRPC-Web is supported for environments where native gRPC (HTTP/2) is not available (e.g., some
browser or proxy configurations):

```http
POST http://localhost:8090/api/meridian.party.v1.PartyService/RegisterParty
Content-Type: application/grpc-web+proto
```

The request body uses length-prefixed protobuf frames (5-byte prefix per message). Most gRPC-Web
client libraries handle framing automatically.

## API Explorer (Swagger UI)

Browse the full REST API interactively while the dev stack is running:

```bash
make swagger-ui
# Open http://localhost:8091/swagger-ui.html
```

The Swagger UI serves the OpenAPI spec generated from the proto annotations. Use the service
dropdown to switch between individual service specs. The "Try it out" feature sends requests
directly to the local gateway on port 8090.

## API Services Reference

| Service | Base Path | gRPC Package |
|---------|-----------|--------------|
| Party | `/v1/parties` | `meridian.party.v1.PartyService` |
| Current Account | `/v1/current-accounts` | `meridian.current_account.v1.CurrentAccountService` |
| Position Keeping | `/v1/accounts/{id}/balances` | `meridian.position_keeping.v1.PositionKeepingService` |
| Financial Accounting | `/v1/financial-accounting` | `meridian.financial_accounting.v1.FinancialAccountingService` |
| Payment Order | `/v1/payment-orders` | `meridian.payment_order.v1.PaymentOrderService` |
| Market Information | `/v1/market-information` | `meridian.market_information.v1.MarketInformationService` |
| Reconciliation | `/v1/reconciliation` | `meridian.reconciliation.v1.ReconciliationService` |
| Internal Account | `/v1/internal-accounts` | `meridian.internal_account.v1.InternalAccountService` |
| Saga Registry | `/v1/sagas` | `meridian.saga.v1.SagaRegistryService` |
| Tenant | `/v1/tenants` | `meridian.tenant.v1.TenantService` |
| Admin | `/v1/admin` | `meridian.control_plane.v1.AdminService` |

## Using the HTTP Client Files

The `http/` directory contains IntelliJ HTTP Client files (`.http` and `.grpc.http`) with
pre-built requests for all services. See [http/README.md](../../http/README.md) for usage
instructions.

## Tenant Isolation

All operations are scoped to the authenticated tenant. In local development mode:

- The `X-Tenant-ID: default` header specifies the tenant for HTTP/JSON requests.
- The `x-tenant-id: default` metadata header specifies the tenant for gRPC requests.

In production:

- The `tenant_id` claim in the JWT token identifies the tenant.
- The gateway rejects requests where the JWT tenant does not match the subdomain tenant.
- API key authentication (for service-to-service) trusts the `X-Tenant-ID` header from the
  calling service.

## See Also

- [ADR-0032: Vanguard HTTP/JSON Transcoding Gateway](../adr/0032-vanguard-json-transcoding-gateway.md)
- [Gateway Service README](../../services/api-gateway/README.md)
- [HTTP Client Files](../../http/README.md)
- [API Proto Documentation](../../api/proto/README.md)
