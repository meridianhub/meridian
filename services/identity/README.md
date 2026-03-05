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
| **Standalone** | No (requires OIDC provider) |

## Purpose

The Identity service provides authentication for the Meridian platform by:

- Integrating with OIDC-compliant identity providers (Dex, Keycloak)
- Validating tokens and managing JWKS endpoints
- Configuring identity connectors for external providers
- Bootstrapping initial identity configuration

## Directory Structure

```text
services/identity/
├── adapters/           # External adapters
├── atlas/              # Atlas migration configuration
├── bootstrap/          # Bootstrap utilities for initial setup
├── connector/          # Identity provider connectors
├── domain/             # Domain models
├── migrations/         # CockroachDB migrations
└── service/            # gRPC service implementation
```

## References

- [Service Architecture](../README.md)
