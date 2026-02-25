# HTTP Client Files

This directory contains [IntelliJ HTTP Client](https://www.jetbrains.com/help/idea/http-client-in-product-code-editor.html)
request files for all Meridian API services.

## File Naming

Each service has two files:

| File | Protocol | Port | Notes |
|------|----------|------|-------|
| `NN-service-name.http` | REST/JSON over HTTP | 8090 (gateway) | Standard curl-style requests |
| `NN-service-name.grpc.http` | Native gRPC | 50051 (direct) | Requires IntelliJ gRPC plugin |

The numbered prefix (`00-`, `01-`, etc.) indicates the recommended order for end-to-end flows
(party before account, account before deposit, etc.).

### When to Use Each Format

**Use `.http` files (REST/JSON)** when you want to:

- Test the full gateway stack including auth and transcoding
- Quickly inspect request/response shapes without gRPC tooling
- Share requests with teammates who do not have gRPC plugins installed

**Use `.grpc.http` files (native gRPC)** when you want to:

- Bypass the gateway and call backend services directly
- Test proto message shapes before they are exposed through the gateway
- Debug gRPC metadata propagation

## Available Files

| Prefix | Service | `.http` | `.grpc.http` |
|--------|---------|---------|-------------|
| 00 | Happy Path (end-to-end) | `00-happy-path.http` | `00-happy-path.grpc.http` |
| 01 | Party | `01-party.http` | `01-party.grpc.http` |
| 02 | Current Account | `02-current-account.http` | `02-current-account.grpc.http` |
| 03 | Position Keeping | `03-position-keeping.http` | `03-position-keeping.grpc.http` |
| 04 | Financial Accounting | `04-financial-accounting.http` | `04-financial-accounting.grpc.http` |
| 05 | Payment Order | `05-payment-order.http` | `05-payment-order.grpc.http` |
| 06 | Market Information | `06-market-information.http` | `06-market-information.grpc.http` |
| 07 | Reconciliation | `07-reconciliation.http` | `07-reconciliation.grpc.http` |
| 08 | Internal Account | `08-internal-account.http` | `08-internal-account.grpc.http` |
| 09 | Saga Registry | `09-saga-registry.http` | `09-saga-registry.grpc.http` |
| 10 | Tenant | `10-tenant.http` | `10-tenant.grpc.http` |
| 11 | Admin | `11-admin.http` | `11-admin.grpc.http` |

## Setup

### Prerequisites

1. Start the dev stack: `make dev-up`
2. In IntelliJ IDEA / GoLand, open the `http/` directory

### Environment Configuration

The `http-client.env.json` file defines environments for all request files:

```json
{
  "local": {
    "host": "http://localhost:8090",
    "grpc_host": "localhost:50051",
    "tenant_id": "default"
  }
}
```

Select the **local** environment in IntelliJ before running requests. Variables like `{{host}}`,
`{{grpc_host}}`, and `{{tenant_id}}` are substituted automatically.

### Running Requests

1. Open a `.http` or `.grpc.http` file in IntelliJ
2. Select **local** from the environment dropdown (top right of editor)
3. Click the green run button next to any request

For `.grpc.http` files, the [Protocol Buffers](https://plugins.jetbrains.com/plugin/14004-protocol-buffers)
plugin must be installed. IntelliJ discovers proto schemas from the workspace automatically.

### Running with VS Code

The `.http` files use IntelliJ HTTP Client syntax. For VS Code, use the
[REST Client](https://marketplace.visualstudio.com/items?itemName=humao.rest-client) extension.
The variable syntax (`{{variable}}`) and test scripts (`> {% ... %}`) are compatible with REST
Client with minor adjustments:

1. Copy `http-client.env.json` to `.env` and adjust the format if needed.
2. Replace `client.assert(...)` test blocks with REST Client assertions or remove them.

### Running with curl

Each request in the `.http` files maps directly to a curl command. For example:

```bash
# From 01-party.http: Register Party
curl -s -X POST http://localhost:8090/v1/parties \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: default" \
  -d '{"partyType": "PARTY_TYPE_PERSON", "legalName": "Alice Smith"}' | jq .
```

## Chaining Requests (Response Variables)

The `.http` files use IntelliJ's response handler scripts to store values between requests:

```javascript
// At the end of "Register Party":
client.global.set("party_id", response.body.party.partyId);

// In the next request:
GET {{host}}/v1/parties/{{party_id}}
```

Run the **00-happy-path.http** file as a sequence to execute a complete end-to-end flow
automatically.

## See Also

- [Developer Guide: Calling Meridian APIs](../docs/guides/calling-meridian-apis.md)
- [API Explorer (Swagger UI)](../api/openapi/swagger-ui.html) — run `make swagger-ui`
- [Gateway Service README](../services/gateway/README.md)
