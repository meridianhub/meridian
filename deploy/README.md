# Deployment Configurations

Environment-specific deployment configuration for Meridian. Each subdirectory holds the
Docker Compose files, database bootstrap SQL, and supporting config for one environment.

## Environments

| Directory | Purpose |
|-----------|---------|
| `dev/` | Local developer Docker Compose stack. |
| `develop/` | The shared `develop` integration environment (auto-deployed on merge). |
| `demo/` | The public demo environment on the DigitalOcean droplet. |

## Demo Environment

The demo environment runs the Backend-For-Frontend auth architecture behind Caddy with Dex OIDC.
Its configuration and operational runbooks live in `demo/`:

- [Demo Auth Architecture](demo/README.md) - BFF authentication design and request flows
- [Cloudflare Setup Checklist](demo/cloudflare-setup-checklist.md) - origin certificate and DNS setup
- [Dex Identity Migration Plan](demo/dex-identity-migration-plan.md) - migrating identity onto embedded Dex
- [Postgres Migration Compatibility Report](demo/pg-migration-compatibility-report.md) - schema compatibility findings
