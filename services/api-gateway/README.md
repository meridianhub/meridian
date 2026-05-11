---
name: api-gateway
description: Multi-tenant API gateway with HTTP/JSON transcoding, JWT and API key authentication, and request routing for the Meridian platform
triggers:
  - JWT authentication and JWKS configuration
  - API key authentication and rate limiting
  - Tenant resolution from subdomain or headers
  - HTTP/JSON to gRPC transcoding via Vanguard
  - Adding a new backend service to the gateway
  - Local development mode setup
instructions: |
  The API Gateway is the externally-addressable HTTP edge of the platform. It handles
  authentication (JWT or API key), tenant resolution, and HTTP/JSON to gRPC transcoding.
  Backend services always speak gRPC; Vanguard translates REST/JSON, Connect, and
  gRPC-Web at the gateway edge.

  Ports: 8080 (HTTP gateway, `PORT` env var default), 50051 (native gRPC on backend
  services directly).

  After any proto change, regenerate the descriptor: `make proto-descriptors`.
  To add a new service, add a `ServiceBackend` entry in `cmd/meridian/main.go` and rerun
  `make proto-descriptors`.

  Local dev: set `AUTH_ENABLED=false`. Pass `X-Tenant-ID` (or `X-Tenant-Slug` with
  `LOCAL_DEV_MODE=true`) for tenant identification.
---

# API Gateway

Multi-tenant API gateway that provides authentication, authorization, HTTP/JSON
transcoding, and request routing for the Meridian platform. Sits on the
[Edge Layer](../../docs/architecture-layers.md#1-edge-layer) of the architecture.

## Overview

| Attribute | Value |
|-----------|-------|
| **BIAN Domain** | Infrastructure (non-BIAN) |
| **Layer** | Edge Layer |
| **Port** | 8080 (HTTP, `PORT` env var default) - backend gRPC services on `:50051` |
| **Database** | CockroachDB (tenant lookup cache only - no domain data) |
| **Standalone** | No (requires every backend service it proxies to, plus a JWKS provider when `AUTH_ENABLED=true`) |

## API Surface

The api-gateway is a transcoding proxy. Every backend RPC is reachable through it.
The protocols accepted at the perimeter are:

| Protocol | Content-Type | URL Pattern |
|----------|--------------|-------------|
| REST/JSON | `application/json` | `/v1/parties`, `/v1/current-accounts`, etc. (per `google.api.http` annotations) |
| Connect | `application/connect+json` | `/<package>.<Service>/<Method>` |
| gRPC-Web | `application/grpc-web+proto` | `/<package>.<Service>/<Method>` |
| Native gRPC | - | Direct to backend `:50051` (dev only) |

Health and tenant-info endpoints are first-class:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/health`, `/healthz` | Liveness probe |
| `GET` | `/ready`, `/readyz` | Readiness probe |
| `GET` | `/v1/tenant-info` | Returns the resolved tenant for the request |
| `POST` | `/v1/auth/*` | Registration, password reset, SSO, MCP consent flows |

For the per-service RPC catalogue, follow the proto imports from the backend
service READMEs listed under Dependencies. Do not duplicate that catalogue here.

## Domain Model

The api-gateway is a stateless adapter. It owns no domain entities. Persistent
state is limited to a tenant lookup cache and OIDC consent/registration tracking
in CockroachDB. Authoritative tenant data lives in the `tenant` service; party
and account data live in their respective services.

## Dependencies

| Service | Protocol | Purpose |
|---------|----------|---------|
| All Core Ledger services (`current-account`, `internal-account`, `financial-accounting`) | gRPC | Customer-facing account operations |
| `payment-order` | gRPC | Tenant-initiated payment lifecycle |
| `party` | gRPC | Party reference data reads |
| `identity` | gRPC | Token introspection, JWKS, OIDC bridging |
| `market-information` | gRPC | Market data reads |
| `reconciliation` | gRPC | Reconciliation status reads |
| `tenant` | gRPC | Tenant lifecycle and lookup |
| External: JWKS provider | HTTPS | JWT signature validation (`JWKS_URL`) |
| External: Redis | TCP | Optional cache for tenant lookups and rate limiting |
| External: CockroachDB | SQL | Tenant lookup cache and OIDC consent state |

The gateway has no business logic of its own. It forwards to backends after
authentication, tenant resolution, and transcoding.

## Dependents

| Service | Entry Point | Purpose |
|---------|-------------|---------|
| `mcp-server` | [`services/mcp-server/internal/clients/clients.go`](../mcp-server/internal/clients/clients.go) | Dials the api-gateway via `MERIDIAN_API_URL`; the gateway is the single gRPC entry point for all backend RPCs invoked by MCP tools |
| `mcp-server` | [`services/mcp-server/cmd/main.go`](../mcp-server/cmd/main.go) | Uses `MERIDIAN_API_KEY` to authenticate to the gateway with an API key, bypassing JWT tenant-binding |
| External: web frontend (`web/`) | HTTP | All browser-originated requests transit the gateway |
| External: CLI tools (`tenantctl`, `meridianctl`) | HTTP / gRPC | Operator tooling routes through the gateway |
| External: third-party API clients | HTTP | Any partner consuming Meridian's REST or Connect APIs |

The api-gateway is the only externally addressable Meridian service for non-MCP
clients. `mcp-server` is the equivalent edge for LLM clients.

## Load-Bearing Files

| File | Why It Matters |
|------|----------------|
| `cmd/main.go` | Process entry point. Wires Vanguard, auth middleware, backends, and the listener |
| `server.go` | HTTP server construction; the order of middleware composition here defines the request lifecycle |
| `config.go` | Single source of truth for environment variable parsing and validation |
| `transcoder.go` | Vanguard transcoder wiring; changes affect every REST and Connect route |
| `auth/jwt_middleware.go` | JWT validation, JWKS fetch, claims extraction. Subtle invariants around clock skew and key rotation |
| `auth/apikey_middleware.go` | API key validation and per-key rate limiting. Token-bucket invariants and cleanup loop |
| `auth/combined_middleware.go` | Combines JWT and API key validators; decides which auth path applies to a request |
| `internal/middleware/mapping_middleware.go` | Identity header injection / spoofing prevention. Changes affect every downstream service's view of the caller |
| `internal/mapping/engine.go` | Request and response mapping engine used by the transcoder |
| `proxy.go` | Backend selection and forwarding logic |
| `headers.go` | Canonical list of identity headers. Changes here are load-bearing for every backend |
| `auth_sso_handler.go`, `auth_sso_state.go` | OIDC SSO state machine for browser flows |
| `mcp_consent_handler.go` | OIDC consent handler shared with `mcp-server`; storage is paired with the MCP server's `OIDCStateStore` |

Paths are relative to `services/api-gateway/`.

## Configuration

### Core

| Variable | Required | Default | Purpose |
|----------|----------|---------|---------|
| `BASE_DOMAIN` | Yes | - | Base domain for subdomain-based tenant identification (e.g., `api.meridianhub.cloud`) |
| `DATABASE_URL` | Yes | - | CockroachDB / PostgreSQL connection string for tenant lookups |
| `PORT` | No | `8080` | HTTP server port |
| `LOCAL_DEV_MODE` | No | `false` | Enable `X-Tenant-Slug` header for local development. Blocked in production namespaces |
| `REDIS_URL` | No | - | Redis URL for caching (uses in-memory if not set) |
| `BACKENDS` | No | `[]` | JSON array of backend routes (see below) |

### Authentication

| Variable | Required | Default | Purpose |
|----------|----------|---------|---------|
| `AUTH_ENABLED` | No | `true` | Enable authentication for API routes |
| `JWKS_URL` | When `AUTH_ENABLED=true` | - | JWKS endpoint URL for JWT validation |
| `JWKS_CACHE_TTL` | No | `24h` | How long to cache JWKS keys |
| `JWKS_REFRESH_TTL` | No | `1h` | Background refresh interval for JWKS keys |
| `JWT_ISSUER` | No | - | Expected JWT issuer (`iss` claim). Validation skipped if empty |
| `JWT_AUDIENCE` | No | - | Expected JWT audience (`aud` claim). Validation skipped if empty |

### API Keys

| Variable | Required | Default | Purpose |
|----------|----------|---------|---------|
| `API_KEYS` | No | - | Comma-separated list of `key:identity` pairs |
| `API_KEY_RATE_LIMIT_PER_SECOND` | No | `100` | Requests per second per API key |
| `API_KEY_RATE_LIMIT_BURST` | No | `200` | Maximum burst size for rate limiting |
| `API_KEY_CLEANUP_INTERVAL` | No | `5m` | Cleanup interval for idle rate limiters |
| `API_KEY_IDLE_TIMEOUT` | No | `10m` | Idle timeout before rate limiter cleanup |

### Backend Routes

The `BACKENDS` environment variable accepts a JSON array of route mappings:

```json
[
  {"prefix": "/v1/party", "target": "party-service:50051"},
  {"prefix": "/v1/accounts", "target": "current-account-service:50051"},
  {"prefix": "/v1/payments", "target": "payment-order-service:50051"}
]
```

### Example

```bash
export BASE_DOMAIN="api.meridianhub.cloud"
export DATABASE_URL="postgres://gateway:password@localhost:5432/meridian?sslmode=disable"

export AUTH_ENABLED="true"
export JWKS_URL="https://auth.meridianhub.cloud/.well-known/jwks.json"
export JWT_ISSUER="https://auth.meridianhub.cloud"
export JWT_AUDIENCE="https://api.meridianhub.cloud"

export API_KEYS="svc-payments-prod:payments-service,svc-reporting-prod:reporting-service"
export API_KEY_RATE_LIMIT_PER_SECOND="100"
export API_KEY_RATE_LIMIT_BURST="200"

export BACKENDS='[{"prefix":"/v1/party","target":"party-service:50051"}]'
```

## Architecture

```mermaid
flowchart LR
    subgraph Clients
        REST["REST/JSON Client"]
        ConnectClient["Connect Client"]
        GRPCWeb["gRPC-Web Client"]
        GRPC["gRPC Client"]
    end

    subgraph Gateway["api-gateway :8080"]
        Auth["Auth Middleware<br/>(JWT / API Key)"]
        TenantRes["Tenant Resolver"]
        TenantAuthz["Tenant Authz"]
        Vanguard["Vanguard Transcoder<br/>(REST or Connect to gRPC)"]
    end

    subgraph Backends["Backends :50051"]
        Party["Party"]
        Account["Current Account"]
        PK["Position Keeping"]
        Other["..."]
    end

    JWKS["JWKS Provider"]

    REST -->|HTTP/JSON| Auth
    ConnectClient -->|Connect| Auth
    GRPCWeb -->|gRPC-Web| Auth
    GRPC -->|gRPC| Party

    Auth --> TenantRes
    JWKS -.->|JWKS fetch| Auth
    TenantRes --> TenantAuthz
    TenantAuthz --> Vanguard
    Vanguard -->|gRPC| Party
    Vanguard -->|gRPC| Account
    Vanguard -->|gRPC| PK
    Vanguard -->|gRPC| Other
```

### Middleware Chain

The gateway applies middleware in this order for all routes:

1. **Auth Middleware** (outermost): Validates JWT or API key, returns 401 if invalid
2. **Tenant Middleware**: Resolves tenant from subdomain or header, injects into context
3. **Tenant Authorization**: Verifies JWT tenant matches resolved tenant, returns 403 if mismatch
4. **Identity Propagation**: Strips spoofed identity headers; injects authenticated context as gRPC metadata
5. **Vanguard Transcoder**: Translates REST/JSON, Connect, or gRPC-Web to native gRPC; routes to backend

Health endpoints (`/health`, `/ready`) bypass all middleware for Kubernetes probe compatibility.

## HTTP/JSON Transcoding

The gateway uses [Vanguard](https://pkg.go.dev/connectrpc.com/vanguard) to translate
between REST/JSON and gRPC. See
[ADR-0032](../../docs/adr/0032-vanguard-json-transcoding-gateway.md) for the
decision rationale.

### Proto Descriptor

Vanguard discovers service schemas from a compiled `FileDescriptorSet` embedded in the binary:

```bash
make proto-descriptors
# runs: buf build api/proto -o cmd/meridian/descriptor.binpb
```

Commit the updated `cmd/meridian/descriptor.binpb` after regenerating.

### Adding a New Service

1. Add `google.api.http` annotations to the proto file.
2. Run `make proto-descriptors` to regenerate the descriptor.
3. Add a `ServiceBackend` entry in `cmd/meridian/main.go`:

   ```go
   {ServiceName: "meridian.myservice.v1.MyService", BackendAddr: "my-service:50051"},
   ```

4. Commit both `descriptor.binpb` and the `main.go` change.

## JWT Token Structure

```json
{
  "sub": "user-uuid-here",
  "iss": "https://auth.meridianhub.cloud",
  "aud": "https://api.meridianhub.cloud",
  "exp": 1735660800,
  "iat": 1735657200,
  "user_id": "user-uuid-here",
  "tenant_id": "acme_bank",
  "roles": ["admin", "operator"],
  "scopes": ["read:accounts", "write:payments"]
}
```

### Required Claims

- `exp`: Token expiration time
- `tenant_id`: The tenant this user belongs to (must match subdomain tenant)

### Optional Claims

- `user_id`: User identifier (defaults to `sub` if not present)
- `roles`: Array of role names for authorization
- `scopes`: Array of OAuth2 scopes

## API Key Authentication

API keys provide an alternative to JWT for service-to-service communication.

```bash
export API_KEYS="sk_prod_abc123:payments-service,sk_prod_def456:reporting-service"
```

```bash
curl -H "X-API-Key: $API_KEY" https://acme.api.meridianhub.cloud/v1/accounts
```

### Rate Limiting

Each API key has independent rate limiting:

- Token bucket: allows bursts up to `BURST`, refills at `PER_SECOND` rate
- Per-key isolation: one key hitting limits does not affect other keys
- Automatic cleanup: idle rate limiters are reclaimed to prevent memory leaks

### API Key vs JWT

| Feature | JWT | API Key |
|---------|-----|---------|
| Use case | User authentication | Service-to-service |
| Tenant isolation | Enforced (must match subdomain) | Bypassed (trusted service) |
| Expiration | Yes (`exp` claim) | No (permanent until rotated) |
| Rate limiting | No | Yes |
| Identity context | Full claims | Identity string only |

### Rotation Procedure

1. Generate new key for the service
2. Add new key to `API_KEYS` alongside existing key
3. Update the calling service to use new key
4. Verify traffic uses new key in logs
5. Remove old key from `API_KEYS`
6. Restart gateway to apply changes

## Error Responses

### 401 Unauthorized

```json
{"error": "missing authorization header"}
{"error": "token expired"}
{"error": "invalid token signature"}
{"error": "invalid API key"}
{"error": "missing API key"}
```

### 403 Forbidden

```json
{"error": "not authorized for this tenant"}
{"error": "missing tenant claim in token"}
```

### 429 Too Many Requests

```json
{"error": "rate limit exceeded"}
```

## Local Development

Enable `LOCAL_DEV_MODE=true` to bypass subdomain-based tenant resolution. In this
mode, use the `X-Tenant-Slug` header to specify the tenant:

```bash
export LOCAL_DEV_MODE=true
curl -H "Authorization: Bearer $JWT" \
     -H "X-Tenant-Slug: acme_bank" \
     http://localhost:8080/v1/accounts
```

This mode is blocked in production namespaces (any namespace prefixed with `prod`).

### Running Tests

```bash
go test ./services/api-gateway/...

# With coverage
go test -coverprofile=coverage.out ./services/api-gateway/...
go tool cover -html=coverage.out

# Specific subpackage
go test ./services/api-gateway/auth/...
```

## Security Considerations

1. **JWKS caching** - keys are cached to reduce latency and load on the auth
   provider. Adjust `JWKS_CACHE_TTL` based on your key rotation frequency.
2. **API key storage** - store keys securely in your deployment configuration
   (Kubernetes secrets, HashiCorp Vault, etc.)
3. **Rate limiting** - default limits (100 req/s, 200 burst) are conservative.
   Adjust based on your service requirements.
4. **Header spoofing prevention** - identity headers (`X-User-ID`,
   `X-Tenant-ID`, etc.) are stripped from incoming requests and only set by the
   gateway after successful authentication.
5. **Production safety** - `LOCAL_DEV_MODE` cannot be enabled in production
   namespaces.

## References

- [Architecture Layers](../../docs/architecture-layers.md#1-edge-layer)
- [Cross-Service Patterns](../../docs/patterns.md)
- [Service README Template](../../docs/service-readme-template.md)
- [ADR-0032: Vanguard JSON Transcoding Gateway](../../docs/adr/0032-vanguard-json-transcoding-gateway.md)
- [Services Architecture](../README.md)
