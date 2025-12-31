# Gateway Service

The Gateway Service is a multi-tenant API gateway that provides authentication,
authorization, and request routing for the Meridian platform.

## Overview

The gateway handles:

- **JWT Authentication**: Validates Bearer tokens using JWKS (JSON Web Key Set)
- **API Key Authentication**: Validates service-to-service API keys with rate limiting
- **Tenant Resolution**: Extracts tenant identity from subdomain or headers
- **Tenant Authorization**: Verifies authenticated identity is authorized for the resolved tenant
- **Request Proxying**: Routes authenticated requests to backend services

## Architecture

```text
                                    +-------------------+
                                    |   JWKS Provider   |
                                    | (Auth0, Keycloak) |
                                    +--------+----------+
                                             |
                                             | JWKS fetch
                                             v
+--------+     +----------+     +-----------+-----------+     +----------+
| Client | --> | Gateway  | --> | Auth      | Tenant    | --> | Backend  |
|        | <-- | (proxy)  | <-- | Middleware| Resolver  | <-- | Services |
+--------+     +----------+     +-----------+-----------+     +----------+
                                             |
                                             v
                                    +-------------------+
                                    | Identity Context  |
                                    | - User ID         |
                                    | - Tenant ID       |
                                    | - Roles/Scopes    |
                                    +-------------------+
```

### Middleware Chain

The gateway applies middleware in this order for `/api/*` routes:

1. **Auth Middleware** (outermost): Validates JWT or API key, returns 401 if invalid
2. **Tenant Middleware**: Resolves tenant from subdomain/header, injects into context
3. **Tenant Authorization**: Verifies JWT tenant matches resolved tenant, returns 403 if mismatch
4. **Backend Proxy**: Routes request to appropriate backend service

Health endpoints (`/health`, `/ready`) bypass all middleware for Kubernetes probe compatibility.

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
- `roles`: Array of role names for authorization
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
curl -H "X-API-Key: $API_KEY" https://acme.api.meridianhub.cloud/api/v1/accounts
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
{"error": "missing authorization header"}
{"error": "token expired"}
{"error": "invalid token signature"}
{"error": "invalid API key"}
{"error": "missing API key"}
```

### 403 Forbidden

Returned when authorization fails:

```json
{"error": "not authorized for this tenant"}
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
     http://localhost:8080/api/v1/accounts
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
