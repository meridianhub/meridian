---
name: identity
description: OIDC-based identity and authentication service for Meridian platform access
triggers:
  - Working on authentication and identity management
  - Configuring OIDC providers and token validation
  - Debugging authentication failures
  - Managing user identity connectors
instructions: |
  Identity service manages authentication and identity for the Meridian platform
  using OIDC (OpenID Connect) providers.

  Key concepts:
  - OIDC provider integration (Dex, Keycloak, etc.)
  - Token validation and JWKS endpoint management
  - Identity connector configuration
  - Bootstrap utilities for initial setup

  Port: gRPC + HTTP
---

# Identity

OIDC-based identity and authentication service for Meridian platform access.

## Overview

| Attribute | Value |
|-----------|-------|
| **Domain** | Identity & Access Management |
| **Language** | Go |
| **Database** | CockroachDB |
| **Standalone** | No (embedded Dex OIDC server) |

## Purpose

The Identity service provides authentication for the Meridian platform by:

- Running an embedded Dex OIDC server within the unified binary
- Bridging Dex authentication to Meridian's identity domain via a custom `PasswordConnector`
- Managing users, credentials, and role assignments per tenant
- Bootstrapping demo/operator users at startup
- Serving OIDC endpoints at `/dex/*` (token, JWKS, discovery)

## Architecture

Dex runs as an embedded library within the Meridian binary rather than as a standalone container.
The API gateway mounts Dex endpoints at `/dex/*` without auth middleware, since these endpoints
are the authentication entry point. Tenant context is resolved from the request subdomain before
reaching the connector.

```text
Client -> Caddy (/dex/*) -> API Gateway -> Embedded Dex Server
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

## Directory Structure

```text
services/identity/
├── adapters/           # External adapters
├── atlas/              # Atlas migration configuration
├── bootstrap/          # Bootstrap utilities and demo user seeding
├── connector/          # Meridian PasswordConnector bridging Dex to identity domain
├── dex/                # Embedded Dex OIDC server package
├── domain/             # Domain models (users, credentials, roles)
├── migrations/         # CockroachDB migrations
└── service/            # gRPC service implementation
```

## References

- [Service Architecture](../README.md)
- [Dex Identity Migration Plan](../../deploy/demo/dex-identity-migration-plan.md)
