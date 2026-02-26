---
name: gateway-service
description: Multi-tenant API gateway with HTTP/JSON transcoding, JWT/API key authentication, and request routing
triggers:
  - JWT authentication and JWKS configuration
  - API key authentication and rate limiting
  - Tenant resolution from subdomain or headers
  - HTTP/JSON to gRPC transcoding via Vanguard
  - Adding a new backend service to the gateway
  - Local development mode setup
instructions: |
  Gateway handles authentication (JWT or API key), tenant resolution, and HTTP/JSON↔gRPC
  transcoding. Backend services always speak gRPC; Vanguard translates REST/JSON, Connect,
  and gRPC-Web at the gateway edge.

  Ports: 8090 (HTTP gateway), 50051 (native gRPC on backend services directly)

  After any proto change, regenerate the descriptor: `make proto-descriptors`
  To add a new service, add a ServiceBackend entry in cmd/meridian/main.go and rerun proto-descriptors.

  Local dev: AUTH_MODE=disabled. Pass X-Tenant-ID header for tenant identification.
---

# Gateway Service

The Gateway Service is a multi-tenant API gateway that provides authentication,
authorisation, HTTP/JSON transcoding, and request routing for the Meridian platform.

## Overview

The gateway handles:

- **HTTP/JSON Transcoding**: Accepts REST/JSON, Connect, and gRPC-Web requests and translates
  them to gRPC for backend services (via [Vanguard](https://connectrpc.com/vanguard))
- **JWT Authentication**: Validates Bearer tokens using JWKS (JSON Web Key Set)
- **API Key Authentication**: Validates service-to-service API keys with rate limiting
- **Tenant Resolution**: Extracts tenant identity from subdomain or headers
- **Tenant Authorisation**: Verifies authenticated identity is authorised for the resolved tenant
- **Identity Propagation**: Injects authenticated user/tenant context as gRPC metadata headers

## Architecture

```mermaid
flowchart LR
    subgraph Clients
        REST["REST/JSON Client<br/>(curl, browser)"]
        ConnectClient["Connect Client<br/>(browser RPC)"]
        GRPCWeb["gRPC-Web Client"]
        GRPC["gRPC Client<br/>(server-to-server)"]
    end

    subgraph Gateway["Gateway :8090"]
        Auth["Auth Middleware<br/>(JWT / API Key)"]
        TenantRes["Tenant Resolver"]
        TenantAuthz["Tenant Authz"]
        Vanguard["Vanguard Transcoder<br/>(REST ↔ gRPC)"]
    end

    subgraph Backends[":50051"]
        Party["Party Service"]
        Account["Current Account"]
        PK["Position Keeping"]
        Other["... other services"]
    end

    JWKS["JWKS Provider"]

    REST -->|HTTP/JSON| Auth
    ConnectClient -->|Connect| Auth
    GRPCWeb -->|gRPC-Web| Auth
    GRPC -->|gRPC| Party
    GRPC -->|gRPC| Account

    Auth --> TenantRes
    JWKS -.->|JWKS fetch| Auth
    TenantRes --> TenantAuthz
    TenantAuthz --> Vanguard
    Vanguard -->|gRPC| Party
    Vanguard -->|gRPC| Account
    Vanguard -->|gRPC| PK
    Vanguard -->|gRPC| Other
```

### Ports

| Port | Purpose |
|------|---------|
| **8090** | HTTP gateway — accepts REST/JSON, Connect, gRPC-Web |
| **50051** | Native gRPC — direct backend access (dev only) |

### Middleware Chain

The gateway applies middleware in this order for all routes:

1. **Auth Middleware** (outermost): Validates JWT or API key, returns 401 if invalid
2. **Tenant Middleware**: Resolves tenant from subdomain/header, injects into context
3. **Tenant Authorisation**: Verifies JWT tenant matches resolved tenant, returns 403 if mismatch
4. **Identity Propagation**: Strips spoofed identity headers; injects authenticated context as gRPC metadata
5. **Vanguard Transcoder**: Translates REST/JSON, Connect, or gRPC-Web to native gRPC; routes to backend

Health endpoints (`/health`, `/ready`) bypass all middleware for Kubernetes probe compatibility.

## HTTP/JSON Transcoding

The gateway uses [Vanguard](https://pkg.go.dev/connectrpc.com/vanguard) to translate between
REST/JSON and gRPC. See [ADR-0032](../../docs/adr/0032-vanguard-json-transcoding-gateway.md) for
the decision rationale.

### Supported Protocols

| Protocol | Content-Type | URL Pattern |
|----------|--------------|-------------|
| REST/JSON | `application/json` | `/v1/parties`, `/v1/current-accounts`, etc. |
| Connect | `application/connect+json` | `/<package>.<Service>/<Method>` |
| gRPC-Web | `application/grpc-web+proto` | `/<package>.<Service>/<Method>` |
| Native gRPC | — | Direct to `:50051` |

### Proto Descriptor

Vanguard discovers service schemas from a compiled `FileDescriptorSet` embedded in the binary:

```bash
# Regenerate after any proto change
make proto-descriptors
# This runs: buf build api/proto -o cmd/meridian/descriptor.binpb
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

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `BASE_DOMAIN` | Yes | - | Base domain for subdomain-based tenant identification (e.g., `api.meridianhub.cloud`) |
| `DATABASE_URL` | Yes | - | PostgreSQL connection string for tenant lookups |
| `PORT` | No | `8080` | HTTP server port |
| `LOCAL_DEV_MODE` | No | `false` | Enable X-Tenant-Slug header for local development |
| `REDIS_URL` | No | - | Redis URL for caching (uses in-memory if not set) |
| `BACKENDS` | No | `[]` | JSON array of backend routes (see below) |

#### Authentication Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `AUTH_ENABLED` | No | `false` | Enable authentication for API routes |
| `JWKS_URL` | When AUTH_ENABLED=true | - | JWKS endpoint URL for JWT validation |
| `JWKS_CACHE_TTL` | No | `24h` | How long to cache JWKS keys |
| `JWKS_REFRESH_TTL` | No | `1h` | Background refresh interval for JWKS keys |
| `JWT_ISSUER` | No | - | Expected JWT issuer (`iss` claim). Validation skipped if empty |
| `JWT_AUDIENCE` | No | - | Expected JWT audience (`aud` claim). Validation skipped if empty |

#### API Key Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `API_KEYS` | No | - | Comma-separated list of `key:identity` pairs |
| `API_KEY_RATE_LIMIT_PER_SECOND` | No | `100` | Requests per second per API key |
| `API_KEY_RATE_LIMIT_BURST` | No | `200` | Maximum burst size for rate limiting |
| `API_KEY_CLEANUP_INTERVAL` | No | `5m` | Cleanup interval for idle rate limiters |
| `API_KEY_IDLE_TIMEOUT` | No | `10m` | Idle timeout before rate limiter cleanup |

### Backend Routes Configuration

The `BACKENDS` environment variable accepts a JSON array of route mappings:

```json
[
  {"prefix": "/v1/party", "target": "party-service:50051"},
  {"prefix": "/v1/accounts", "target": "current-account-service:50051"},
  {"prefix": "/v1/payments", "target": "payment-order-service:50051"}
]
```

### Example Configuration

```bash
# Required
export BASE_DOMAIN="api.meridianhub.cloud"
export DATABASE_URL="postgres://gateway:password@localhost:5432/meridian?sslmode=disable"

# Authentication
export AUTH_ENABLED="true"
export JWKS_URL="https://auth.meridianhub.cloud/.well-known/jwks.json"
export JWT_ISSUER="https://auth.meridianhub.cloud"
export JWT_AUDIENCE="https://api.meridianhub.cloud"

# API Keys for service-to-service auth
export API_KEYS="svc-payments-prod:payments-service,svc-reporting-prod:reporting-service"
export API_KEY_RATE_LIMIT_PER_SECOND="100"
export API_KEY_RATE_LIMIT_BURST="200"

# Backend routes
export BACKENDS='[{"prefix":"/v1/party","target":"party-service:50051"}]'
```

## JWT Token Structure

The gateway expects JWTs with the following claims:

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

- `exp`: Token expiration time (standard JWT claim)
- `tenant_id`: The tenant this user belongs to (must match subdomain tenant)

### Optional Claims

- `user_id`: User identifier (defaults to `sub` if not present)
- `roles`: Array of role names for authorisation
- `scopes`: Array of OAuth2 scopes

## API Key Authentication

API keys provide an alternative to JWT for service-to-service communication.

### Configuration

```bash
# Format: key:identity,key:identity,...
export API_KEYS="sk_prod_abc123:payments-service,sk_prod_def456:reporting-service"
```

### Usage

```bash
curl -H "X-API-Key: $API_KEY" https://acme.api.meridianhub.cloud/v1/accounts
```

### Rate Limiting

Each API key has independent rate limiting:

- **Token bucket algorithm**: Allows bursts up to `BURST` then refills at `PER_SECOND` rate
- **Per-key isolation**: One key hitting limits doesn't affect other keys
- **Automatic cleanup**: Idle rate limiters are cleaned up to prevent memory leaks

### API Key vs JWT

| Feature | JWT | API Key |
|---------|-----|---------|
| Use case | User authentication | Service-to-service |
| Tenant isolation | Enforced (must match subdomain) | Bypassed (trusted service) |
| Expiration | Yes (exp claim) | No (permanent until rotated) |
| Rate limiting | No | Yes |
| Identity context | Full claims | Identity string only |

## API Key Generation and Rotation

### Best Practices

1. **Use secure random keys**: Generate keys with at least 256 bits of entropy

   ```bash
   openssl rand -base64 32 | tr -d '/+=' | head -c 32
   ```

2. **Prefix keys for identification**: Use prefixes like `sk_prod_`, `sk_staging_`

3. **Rotate regularly**: Implement key rotation without downtime by supporting multiple keys temporarily

4. **Audit key usage**: Monitor API key identity in access logs

### Rotation Procedure

1. Generate new key for the service
2. Add new key to `API_KEYS` alongside existing key
3. Update the calling service to use new key
4. Verify traffic uses new key in logs
5. Remove old key from `API_KEYS`
6. Restart gateway to apply changes

## Health Endpoints

| Endpoint | Purpose | Response |
|----------|---------|----------|
| `GET /health` | Liveness probe | `200 OK` |
| `GET /ready` | Readiness probe | `200 READY` |
| `GET /healthz` | Legacy liveness | `200 OK` |
| `GET /readyz` | Legacy readiness | `200 READY` |

These endpoints bypass all authentication middleware to ensure Kubernetes probes work correctly.

## Error Responses

### 401 Unauthorized

Returned when authentication fails:

```json
{"error": "missing authorisation header"}
{"error": "token expired"}
{"error": "invalid token signature"}
{"error": "invalid API key"}
{"error": "missing API key"}
```

### 403 Forbidden

Returned when authorisation fails:

```json
{"error": "not authorised for this tenant"}
{"error": "missing tenant claim in token"}
```

### 429 Too Many Requests

Returned when API key rate limit is exceeded:

```json
{"error": "rate limit exceeded"}
```

## Development

### Local Development Mode

Enable `LOCAL_DEV_MODE=true` to bypass subdomain-based tenant resolution.
In this mode, use the `X-Tenant-Slug` header to specify the tenant:

```bash
export LOCAL_DEV_MODE=true
curl -H "Authorization: Bearer $JWT" \
     -H "X-Tenant-Slug: acme_bank" \
     http://localhost:8080/v1/accounts
```

This mode is blocked in production namespaces (any namespace prefixed with `prod`).

### Running Tests

```bash
# Run all gateway tests
go test ./services/gateway/...

# Run with coverage
go test -coverprofile=coverage.out ./services/gateway/...
go tool cover -html=coverage.out

# Run specific test packages
go test ./services/gateway/auth/...
```

### Test Categories

- **Unit tests**: Test individual middleware components in isolation
- **Integration tests**: Test full HTTP request flow through the server
- **Benchmark tests**: Measure middleware performance overhead

## Security Considerations

1. **JWKS Caching**: Keys are cached to reduce latency and load on the auth provider.
   Adjust `JWKS_CACHE_TTL` based on your key rotation frequency.

2. **API Key Storage**: Store API keys securely in your deployment configuration (Kubernetes secrets, HashiCorp Vault, etc.)

3. **Rate Limiting**: Default limits (100 req/s, 200 burst) are conservative. Adjust based on your service requirements.

4. **Header Spoofing Prevention**: Identity headers (`X-User-ID`, `X-Tenant-ID`, etc.) are
   stripped from incoming requests and only set by the gateway after successful authentication.

5. **Production Safety**: `LOCAL_DEV_MODE` cannot be enabled in production namespaces.
