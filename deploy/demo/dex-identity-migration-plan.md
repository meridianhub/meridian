# Dex → Meridian Identity Connector Migration Plan

## Current State (Demo)

The demo environment uses Dex's built-in password database (`enablePasswordDB: true`)
with static credentials defined in `dex.yaml`. This is intentional for the demo:
credentials are predictable, the environment is throwaway, and it requires zero
runtime dependency on the identity service being fully bootstrapped.

## Target State (Production)

Dex authenticates users via the **Meridian identity connector** — a Go implementation
of `dex/connector.PasswordConnector` that validates credentials directly against the
identity domain repository (no network hop).

### Architecture

```text
User → Dex (password grant)
         │
         └─ PasswordConnector.Login()
                  │
                  └─ services/identity/connector.Connector
                           │
                           ├─ tenant.RequireFromContext(ctx)   ← subdomain → tenant ID
                           ├─ repo.FindByEmail(ctx, email)     ← identity lookup
                           ├─ credentials.ValidatePassword()   ← bcrypt verify
                           └─ repo.FindRoleAssignments()       ← groups → JWT claims
```

The connector is a **compile-time, in-process** integration. Dex is not a separate
binary that calls Meridian over the network — it is embedded as a library or its
server is initialised with a custom connector registered under the ID `"meridian"`.

### Why Not a Network Connector

Dex supports 16 connector types (LDAP, OIDC, GitHub, SAML, etc.). None is a generic
gRPC or HTTP connector. A `type: grpc` does not exist in Dex v2.x. Implementing a
custom connector as a separate Dex plugin would require forking Dex or using the
(experimental) gRPC connector interface — neither is appropriate for the demo.

The in-process approach is superior:

- Zero network latency for authentication
- No TLS configuration required
- Single binary deployment preserved
- Tenant context propagated via Go `context.Context`, not HTTP headers

## Migration Steps

### 1. Wire the Connector at Startup

In `services/identity/bootstrap/` (or wherever the Dex server is initialised),
register the Meridian connector:

```go
import (
    dexserver "github.com/dexidp/dex/server"
    "github.com/meridianhub/meridian/services/identity/connector"
)

meridianConn, err := connector.New(identityRepo, logger)
// ...

cfg := dexserver.Config{
    PasswordConnector: meridianConn,  // replaces the built-in password DB
    // ...
}
```

### 2. Update dex.yaml

Once the connector is wired:

```yaml
# Remove these lines:
enablePasswordDB: true
staticPasswords: [...]

# Update oauth2 section:
oauth2:
  passwordConnector: meridian   # matches the ID registered at startup
  skipApprovalScreen: true
```

### 3. Subdomain → Tenant Resolution

The connector uses `tenant.RequireFromContext(ctx)` to scope identity lookups.
Ensure the gateway populates the tenant in context before the Dex password flow
reaches the connector — typically via subdomain extraction in the HTTP middleware.

### 4. Seed Demo Users via Identity Service

Replace `staticPasswords` with seeded identities created through the identity
service bootstrap (see `services/identity/bootstrap/`). The `admin@volterra.energy`
and `operator@volterra.energy` users should be created via the identity API with
appropriate role assignments.

## Reference

- Connector implementation: `services/identity/connector/connector.go`
- Claims mapping: `services/identity/connector/claims.go`
- Bootstrap: `services/identity/bootstrap/`
- Dex connector interface: `github.com/dexidp/dex/connector.PasswordConnector`
