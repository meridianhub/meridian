# Dex -> Meridian Identity Connector Migration Plan

## Status: COMPLETED

All migration steps have been implemented and merged. Dex now runs as an embedded
library within the Meridian unified binary, authenticating users via the Meridian
identity connector rather than a static password database.

## Current State (Implemented)

Dex is embedded in the Meridian binary as a library (`services/identity/dex/`). The
standalone `dex` container has been removed from `docker-compose.yml`. Authentication
flows through the following path:

```text
User -> Caddy (/dex/*) -> API Gateway -> Embedded Dex Server
                                            |
                                            v
                                   MeridianConnector (PasswordConnector)
                                            |
                                   +--------+--------+
                                   |        |        |
                                FindByEmail  Bcrypt  RoleAssignments
                                (identity   verify  (groups -> JWT
                                 repo)              claims)
```

The connector is a compile-time, in-process integration. Dex is not a separate
binary that calls Meridian over the network -- it is embedded as a library with
a custom connector registered under the ID `"meridian"`.

### Why Not a Network Connector

Dex supports 16 connector types (LDAP, OIDC, GitHub, SAML, etc.). None is a generic
gRPC or HTTP connector. The in-process approach provides:

- Zero network latency for authentication
- No TLS configuration required
- Single binary deployment preserved
- Tenant context propagated via Go `context.Context`, not HTTP headers

## Migration Steps (All Completed)

### 1. Wire the Connector at Startup -- DONE

The embedded Dex server is initialized in `services/identity/dex/` and wired into
the unified binary at `cmd/meridian/main.go`. The `MeridianConnector` adapter bridges
the identity domain repository to Dex's `PasswordConnector` interface.

### 2. Remove Standalone Dex Container -- DONE

The `dex` service was removed from `deploy/demo/docker-compose.yml`. The `dex.yaml`
configuration file is no longer needed on the host. Dex configuration (issuer URL,
static clients) is now driven by environment variables (`DEX_ISSUER`, `BASE_DOMAIN`).

### 3. Mount Dex at /dex/* in API Gateway -- DONE

The API gateway mounts the embedded Dex handler at `/dex/*` without auth middleware.
These endpoints must bypass authentication since they ARE the authentication entry
point. Caddy routes `/dex/*` to `meridian:8090`.

### 4. Subdomain -> Tenant Resolution -- DONE

The gateway resolves tenant context from the request subdomain before the Dex password
flow reaches the connector. The connector uses `tenant.RequireFromContext(ctx)` to
scope identity lookups to the correct tenant.

### 5. Seed Demo Users via Identity Service -- DONE

Static passwords in `dex.yaml` have been replaced with users seeded through the
identity service bootstrap. The `operator@volterra.energy` user is created at startup
when `DEMO_OPERATOR_EMAIL` and `DEMO_OPERATOR_PASSWORD` environment variables are set.

### 6. JWT Validation Against Embedded Dex JWKS -- DONE

The API gateway fetches JWKS from the embedded Dex server (`DEX_ISSUER/keys`) for
token validation. No external JWKS endpoint is required.

### 7. Update Caddy Configuration -- DONE

Caddy now routes `/dex/*` requests to `meridian:8090` (the unified binary) instead
of to a standalone Dex container. The `dex.yaml` volume mount was removed.

### 8. Integration Tests -- DONE

End-to-end integration tests verify the full authentication flow: password grant
through the embedded Dex server, token issuance, and JWT validation.

## Reference

- Embedded Dex server: `services/identity/dex/`
- Connector implementation: `services/identity/connector/`
- Bootstrap (demo user seeding): `services/identity/bootstrap/`
- Gateway Dex mount: `services/api-gateway/server.go` (WithDexHandler)
- Docker Compose: `deploy/demo/docker-compose.yml`
- Caddy config: `deploy/demo/Caddyfile`
